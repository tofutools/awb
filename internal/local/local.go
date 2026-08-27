// Package local implements the backend over the SQLite database directly. It
// is what the CLI uses in direct mode and what awb serve puts the HTTP API on
// top of, so both surfaces exercise one set of operations.
//
// Every mutation is a single BEGIN IMMEDIATE transaction, so the graph checks,
// the compare-and-set of claim, the strictly-increasing updated_at and the ID
// collision retry all read and write inside one writer's exclusive turn, and
// no concurrent commit can slip between the check and the change.
package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// Backend is the direct-mode implementation of backend.Backend.
type Backend struct {
	db *storage.DB
	// identity is who this backend attributes an unattributed action to: the
	// CLI's resolved identity, or the identity of the request the server is
	// answering.
	identity string
}

// New wraps an open database.
func New(db *storage.DB, identity string) *Backend {
	return &Backend{db: db, identity: identity}
}

// WithIdentity returns a copy attributing actions to a different identity. The
// server uses it to give each request the identity it authenticated, without
// reopening the database.
func (b *Backend) WithIdentity(identity string) *Backend {
	return &Backend{db: b.db, identity: identity}
}

// DB exposes the underlying database, which awb serve needs in order to hand
// each request its own identity.
func (b *Backend) DB() *storage.DB { return b.db }

// Identity is the caller this backend acts as.
func (b *Backend) Identity(_ context.Context) (string, error) {
	if b.identity == "" {
		return "", awberr.Runtimef(
			"no identity is configured: set \"identity\" in the configuration file or AWB_IDENTITY")
	}
	return b.identity, nil
}

// Close releases the database.
func (b *Backend) Close() error { return b.db.Close() }

// resolve turns a reference — a full ID, an ID prefix, or a bare hash — into
// exactly one issue ID.
func resolve(tx *storage.Tx, ref string) (string, error) {
	parsed, err := domain.ParseIssueRef(ref)
	if err != nil {
		return "", err
	}
	return tx.ResolveIssueRef(parsed)
}

// load resolves a reference and reads the issue behind it.
func load(tx *storage.Tx, ref string) (*domain.Issue, error) {
	id, err := resolve(tx, ref)
	if err != nil {
		return nil, err
	}
	return tx.GetIssue(id)
}

// checkIfMatch enforces the optional conditional-edit precondition. An empty
// ifMatch means no precondition, which is what the CLI always sends and what
// gives it last-write-wins.
//
// The tag is a strong one over the entity's own stored fields, so it is
// compared exactly.
func checkIfMatch(ifMatch, updatedAt, what string) error {
	if ifMatch == "" {
		return nil
	}
	if ifMatch != ETag(updatedAt) {
		return awberr.PreconditionFailed(what)
	}
	return nil
}

// ETag is the entity tag: the entity's updated_at in quotes.
//
// What makes it reliable is not the millisecond resolution of updated_at but
// its being strictly increasing per row, so the tag identifies a version
// rather than an instant. It is a strong tag, because If-Match is compared
// strongly and a weak validator would never match, and it therefore says
// exactly what it guards: the entity's own stored fields, which are what a
// form edits.
func ETag(updatedAt string) string { return `"` + updatedAt + `"` }

// write runs one mutation and returns the resulting object, re-read inside the
// same transaction so the derived fields reflect the change.
func (b *Backend) write(ctx context.Context, fn func(*storage.Tx) error) error {
	return b.db.Write(ctx, fn)
}

// readIssue is the common shape of the read-only operations.
func (b *Backend) readIssue(ctx context.Context, ref string) (*domain.Issue, error) {
	var issue *domain.Issue
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		var err error
		issue, err = load(tx, ref)
		return err
	})
	if err != nil {
		return nil, err
	}
	return issue, nil
}

var _ backend.Backend = (*Backend)(nil)
