package storage

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestWriteBeginsImmediateBeforeCallback(t *testing.T) {
	db := newTxTestDB(t)
	contender := openImmediateContender(t, db.Path())

	require.NoError(t, db.Write(t.Context(), func(*Tx) error {
		_, err := contender.ExecContext(t.Context(), "BEGIN IMMEDIATE")
		require.ErrorContains(t, err, "database is locked")
		return nil
	}))

	_, err := contender.ExecContext(t.Context(), "BEGIN IMMEDIATE")
	require.NoError(t, err)
	_, err = contender.ExecContext(t.Context(), "ROLLBACK")
	require.NoError(t, err)
}

func TestReadRemainsDeferred(t *testing.T) {
	db := newTxTestDB(t)
	contender := openImmediateContender(t, db.Path())

	require.NoError(t, db.Read(t.Context(), func(tx *Tx) error {
		if _, err := tx.AnyUsers(); err != nil {
			return err
		}
		_, err := contender.ExecContext(t.Context(), "BEGIN IMMEDIATE")
		if err != nil {
			return err
		}
		_, err = contender.ExecContext(t.Context(), "ROLLBACK")
		return err
	}))
}

func openImmediateContender(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout=0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
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
