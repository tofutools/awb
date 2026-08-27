package storage

import (
	"context"
	"database/sql"

	"github.com/tofutools/awb/internal/awberr"
)

// queryer is the part of database/sql that the query helpers use, so the same
// code runs on a pooled handle and on the dedicated connection a transaction
// holds.
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
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "acquire database connection")
	}
	defer conn.Close() //nolint:errcheck // returning the connection to the pool

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "begin transaction")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if err := fn(&Tx{ctx: ctx, q: conn}); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "commit transaction")
	}
	committed = true
	return nil
}

// Read runs fn inside a deferred transaction, so a composite read — an issue
// with its labels, relations and blockers — sees one consistent snapshot. In
// WAL mode a reader never blocks a writer.
func (d *DB) Read(ctx context.Context, fn func(*Tx) error) error {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "acquire database connection")
	}
	defer conn.Close() //nolint:errcheck // returning the connection to the pool

	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "begin transaction")
	}
	defer func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") }()

	return fn(&Tx{ctx: ctx, q: conn})
}
