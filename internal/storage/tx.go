package storage

import (
	"context"
	"database/sql"

	"github.com/tofutools/awb/internal/awberr"
)

// queryer is the part of database/sql.Tx that the query helpers use.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Tx is the handle the query helpers hang off. Every statement of one
// operation goes through the same Tx, and therefore the same connection and
// the same transaction.
type Tx struct {
	ctx context.Context
	q   queryer
	// scope is what the caller running this transaction may see; see scope.go.
	// The zero value hides nothing, which is direct mode.
	scope Scope
}

// Write runs fn inside a BEGIN IMMEDIATE transaction.
//
// IMMEDIATE takes the write lock before fn reads anything, which is what makes
// every check fn performs good at the moment it writes: the graph checks, the
// compare-and-set of claim, the strictly-increasing updated_at and the ID
// collision retry all read and write inside one writer's exclusive turn, and
// no concurrent commit can slip between the check and the change.
//
// A transaction that cannot take the lock within the busy timeout fails rather
// than being retried in a loop.
func (d *DB) Write(ctx context.Context, fn func(*Tx) error) error {
	// The driver's _txlock=immediate setting makes every non-read-only
	// transaction BEGIN IMMEDIATE; see dsn. Using database/sql's transaction
	// type lets it own cancellation, rollback and the pooled connection state.
	sqlTx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "begin transaction")
	}
	defer sqlTx.Rollback() //nolint:errcheck // safe after Commit; database/sql owns cleanup

	if err := fn(&Tx{ctx: ctx, q: sqlTx}); err != nil {
		return err
	}

	if err := sqlTx.Commit(); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "commit transaction")
	}
	return nil
}

// Read runs fn inside a deferred transaction, so a composite read — an issue
// with its labels, relations and blockers — sees one consistent snapshot. In
// WAL mode a reader never blocks a writer.
func (d *DB) Read(ctx context.Context, fn func(*Tx) error) error {
	// modernc treats read-only transactions as deferred even when _txlock is
	// immediate, so readers retain their WAL-mode non-blocking behaviour.
	sqlTx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "begin transaction")
	}
	defer sqlTx.Rollback() //nolint:errcheck // database/sql owns cleanup

	return fn(&Tx{ctx: ctx, q: sqlTx})
}
