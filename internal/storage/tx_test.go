package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	connector := &rollbackFailureConnector{}
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

type rollbackFailureConnector struct {
	opened atomic.Int32
	closed atomic.Int32
}

func (c *rollbackFailureConnector) Connect(context.Context) (driver.Conn, error) {
	n := c.opened.Add(1)
	return &rollbackFailureConn{connector: c, failRollback: n == 1}, nil
}

func (*rollbackFailureConnector) Driver() driver.Driver { return rollbackFailureDriver{} }

type rollbackFailureDriver struct{}

func (rollbackFailureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type rollbackFailureConn struct {
	connector    *rollbackFailureConnector
	failRollback bool
}

func (*rollbackFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (c *rollbackFailureConn) Close() error {
	c.connector.closed.Add(1)
	return nil
}

func (*rollbackFailureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("database/sql transactions are not used")
}

func (c *rollbackFailureConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (
	driver.Result, error,
) {
	if query == "ROLLBACK" && c.failRollback {
		return nil, errors.New("forced rollback failure")
	}
	return driver.RowsAffected(0), nil
}
