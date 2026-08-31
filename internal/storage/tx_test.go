package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

func newTxTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	db.SQL().SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

// One connection makes reuse deterministic: if cancellation leaves it inside
// BEGIN, the next operation fails with "cannot start a transaction within a
// transaction" instead of being able to take another pooled connection.
func TestCancelledReadDoesNotPoisonConnection(t *testing.T) {
	db := newTxTestDB(t)
	ctx, cancel := context.WithCancel(t.Context())

	err := db.Read(ctx, func(*Tx) error {
		cancel()
		return ctx.Err()
	})
	require.ErrorIs(t, err, context.Canceled)
	assertPoolRemainsUsable(t, db)
}

func TestCancelledWriteDoesNotPoisonConnection(t *testing.T) {
	db := newTxTestDB(t)
	ctx, cancel := context.WithCancel(t.Context())

	// Returning nil reaches COMMIT with the cancelled request context. Its
	// failure must still be followed by cleanup that does not use that context.
	err := db.Write(ctx, func(*Tx) error {
		cancel()
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	assertPoolRemainsUsable(t, db)
}

func TestCancelledWriteAfterStatementFailureDoesNotPoisonConnection(t *testing.T) {
	db := newTxTestDB(t)
	ctx, cancel := context.WithCancel(t.Context())

	err := db.Write(ctx, func(tx *Tx) error {
		_, statementErr := tx.q.ExecContext(ctx, "not valid sql")
		require.Error(t, statementErr)
		cancel()
		return statementErr
	})
	require.Error(t, err)
	assertPoolRemainsUsable(t, db)
}

func TestCancelledHTTPRequestDoesNotPoisonConnection(t *testing.T) {
	db := newTxTestDB(t)
	passwordHash, err := domain.HashPassword("hunter2")
	require.NoError(t, err)
	require.NoError(t, db.Write(t.Context(), func(tx *Tx) error {
		return tx.InsertUser("alice", "Alice", passwordHash, false, false)
	}))
	transactionStarted := make(chan struct{})
	requestFinished := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/cancel", func(_ http.ResponseWriter, r *http.Request) {
		err := db.Read(r.Context(), func(*Tx) error {
			close(transactionStarted)
			<-r.Context().Done()
			return r.Context().Err()
		})
		requestFinished <- err
	})
	mux.HandleFunc("/healthy", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Write(r.Context(), func(tx *Tx) error {
			return tx.InsertProject("healthy", "Healthy", "")
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := db.Read(r.Context(), func(tx *Tx) error {
			if _, err := tx.GetProject("healthy"); err != nil {
				return err
			}
			hash, found, err := tx.PasswordHash("alice")
			if err != nil {
				return err
			}
			if !found || !domain.CheckPassword(hash, "hunter2") {
				return errors.New("subsequent authentication failed")
			}
			return nil
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/cancel", nil)
	require.NoError(t, err)
	response := make(chan error, 1)
	go func() {
		resp, doErr := server.Client().Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		response <- doErr
	}()
	<-transactionStarted
	cancel()
	require.ErrorIs(t, <-response, context.Canceled)
	require.ErrorIs(t, <-requestFinished, context.Canceled)

	resp, err := server.Client().Get(server.URL + "/healthy")
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestCommitFailureRollsBackConnection(t *testing.T) {
	db := newTxTestDB(t)

	err := db.Write(t.Context(), func(tx *Tx) error {
		_, err := tx.q.ExecContext(tx.ctx, "PRAGMA defer_foreign_keys = ON")
		if err != nil {
			return err
		}
		_, err = tx.q.ExecContext(tx.ctx,
			"INSERT INTO issue_labels (issue, label) VALUES ('missing', 'label')")
		return err
	})
	require.ErrorContains(t, err, "commit transaction")
	assertPoolRemainsUsable(t, db)
}

func TestRollbackFailureDiscardsConnection(t *testing.T) {
	connector := &transactionFailureConnector{failQuery: "ROLLBACK"}
	sqlDB := sql.OpenDB(connector)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	db := &DB{db: sqlDB}

	err := db.Read(t.Context(), func(*Tx) error { return nil })
	require.ErrorContains(t, err, "rollback transaction")

	// Raw returning driver.ErrBadConn closes the uncertain first connection.
	// The next operation therefore opens a fresh one instead of reusing it.
	require.NoError(t, db.Write(t.Context(), func(*Tx) error { return nil }))
	assert.Equal(t, int32(2), connector.opened.Load())
	assert.Equal(t, int32(1), connector.closed.Load())
}

func TestBeginFailureDiscardsConnection(t *testing.T) {
	cases := []struct {
		name      string
		failQuery string
		operation func(*DB, func(*Tx) error) error
	}{
		{"read", "BEGIN", func(db *DB, fn func(*Tx) error) error {
			return db.Read(t.Context(), fn)
		}},
		{"write", "BEGIN IMMEDIATE", func(db *DB, fn func(*Tx) error) error {
			return db.Write(t.Context(), fn)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connector := &transactionFailureConnector{failQuery: tc.failQuery}
			sqlDB := sql.OpenDB(connector)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			db := &DB{db: sqlDB}

			// The test driver enters a transaction and then reports that BEGIN
			// failed, modelling cancellation racing with modernc's SQLite step.
			callbackRan := false
			err := tc.operation(db, func(*Tx) error {
				callbackRan = true
				return nil
			})
			require.ErrorContains(t, err, "begin transaction")
			assert.False(t, callbackRan)

			require.NoError(t, db.Write(t.Context(), func(*Tx) error { return nil }))
			assert.Equal(t, int32(2), connector.opened.Load())
			assert.Equal(t, int32(1), connector.closed.Load())
		})
	}
}

func assertPoolRemainsUsable(t *testing.T, db *DB) {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(tx *Tx) error {
		return tx.InsertProject("healthy", "Healthy", "")
	}))
	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		project, err := tx.GetProject("healthy")
		if err == nil {
			assert.Equal(t, "Healthy", project.Name)
		}
		return err
	}))
}

func TestJoinTransactionErrorPreservesOperationError(t *testing.T) {
	operationErr := errors.New("operation")
	rollbackErr := errors.New("rollback")
	err := joinTransactionError(operationErr, rollbackErr)
	assert.ErrorIs(t, err, operationErr)
	assert.ErrorIs(t, err, rollbackErr)
}

type transactionFailureConnector struct {
	opened    atomic.Int32
	closed    atomic.Int32
	failQuery string
}

func (c *transactionFailureConnector) Connect(context.Context) (driver.Conn, error) {
	n := c.opened.Add(1)
	failQuery := ""
	if n == 1 {
		failQuery = c.failQuery
	}
	return &transactionFailureConn{connector: c, failQuery: failQuery}, nil
}

func (*transactionFailureConnector) Driver() driver.Driver { return transactionFailureDriver{} }

type transactionFailureDriver struct{}

func (transactionFailureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type transactionFailureConn struct {
	connector     *transactionFailureConnector
	failQuery     string
	inTransaction bool
}

func (*transactionFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (c *transactionFailureConn) Close() error {
	c.connector.closed.Add(1)
	return nil
}

func (*transactionFailureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("database/sql transactions are not used")
}

func (c *transactionFailureConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (
	driver.Result, error,
) {
	switch query {
	case "BEGIN", "BEGIN IMMEDIATE":
		if c.inTransaction {
			return nil, errors.New("cannot start a transaction within a transaction")
		}
		c.inTransaction = true
	case "ROLLBACK", "COMMIT":
		if query == c.failQuery {
			return nil, errors.New("forced transaction failure")
		}
		c.inTransaction = false
	}
	if query == c.failQuery {
		return nil, errors.New("forced transaction failure")
	}
	return driver.RowsAffected(0), nil
}
