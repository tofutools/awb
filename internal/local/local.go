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
	// blobs is where attachment content lives: a directory of files beside the
	// database by default, which may be pointed at a filesystem of its own.
	blobs *storage.Blobs
	// identity is who this backend attributes an unattributed action to: the
	// CLI's resolved identity, or the identity of the request the server is
	// answering.
	identity string
	// authorized says the permissions of identity are to be applied to every
	// operation: what identity may see, and what identity may change.
	//
	// It is off by default, and direct mode leaves it off. The CLI on a
	// database file applies no authorization at all, because whoever can open
	// the file can already read and write every byte of it and a check there
	// would be a suggestion rather than a control. It is switched on by awb
	// serve, and only for a request it authenticated.
	authorized bool
	// userPreferences says the identity may own and apply ignored-workspace
	// preferences. Direct CLI backends and authenticated requests do. An open
	// HTTP server deliberately does not: --no-auth means its fixed identity is
	// attribution only and the users table is never consulted on its behalf.
	userPreferences bool
}

// New wraps an open database and the directory its attachment content lives
// in.
func New(db *storage.DB, blobs *storage.Blobs, identity string) *Backend {
	return &Backend{db: db, blobs: blobs, identity: identity, userPreferences: true}
}

// WithIdentity returns a direct-mode copy attributing actions to a different
// identity and applying no authorization. Its stored preferences still apply.
func (b *Backend) WithIdentity(identity string) *Backend {
	return &Backend{
		db: b.db, blobs: b.blobs, identity: identity, userPreferences: true,
	}
}

// WithoutUserPreferences returns the backend an unauthenticated server gives
// a request. Its fixed identity remains useful for activity attribution, but
// cannot acquire per-user meaning merely because an account has the same name.
func (b *Backend) WithoutUserPreferences() *Backend {
	return &Backend{db: b.db, blobs: b.blobs, identity: b.identity}
}

// WithUser returns a copy acting as an authenticated user: attributing actions
// to them and applying their permissions. The server uses it to give each
// authenticated request its own caller, without reopening the database.
func (b *Backend) WithUser(username string) *Backend {
	return &Backend{
		db: b.db, blobs: b.blobs, identity: username,
		authorized: true, userPreferences: true,
	}
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

// AuthenticatedIdentity is the local caller. Direct mode has no separate
// authentication boundary, so it is identical to Identity.
func (b *Backend) AuthenticatedIdentity(ctx context.Context) (string, error) {
	return b.Identity(ctx)
}

// MayManageUsers reports the effective capability resolved by the same caller
// path every account operation uses. In particular, an unrestricted no-auth
// server remains unrestricted even when its fixed identity matches a stored
// non-administrator account.
func (b *Backend) MayManageUsers(ctx context.Context) (bool, error) {
	var allowed bool
	err := b.read(ctx, func(_ *storage.Tx, caller domain.Caller) error {
		allowed = caller.MayManageUsers()
		return nil
	})
	return allowed, err
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

func ensureWorkspaceActive(tx *storage.Tx, key string) error {
	workspace, err := tx.GetWorkspace(key)
	if err != nil {
		return err
	}
	if workspace.State == domain.WorkspaceArchived {
		return awberr.Conflictf("workspace %s is archived and read-only", key)
	}
	return nil
}

func ensureIssueWritable(tx *storage.Tx, issue *domain.Issue) error {
	return ensureWorkspaceActive(tx, issue.Workspace)
}

// checkIfMatch enforces the optional conditional-edit precondition. An empty
// ifMatch means no precondition and gives the caller last-write-wins.
//
// The tag is a strong one over the entity version, so it is compared exactly.
func checkIfMatch(ifMatch, updatedAt, what string) error {
	if ifMatch == "" {
		return nil
	}
	if ifMatch != backend.ETag(updatedAt) {
		return awberr.PreconditionFailed(what)
	}
	return nil
}

// authorize resolves who is acting and restricts the transaction to what they
// may see. It is the one place either is decided, and every operation of this
// package goes through it, so an operation added later cannot quietly be the
// one that reads past the scope.
//
// Without authorization the caller is unrestricted and the transaction keeps
// its unscoped default, which is the SQL version 1 ran.
//
// With it, the permissions are read from the user row inside this very
// transaction, so they are the permissions at the moment of the write rather
// than at the moment the request arrived. A workspace administrator holds admin
// access everywhere and is therefore left unrestricted too; everybody else
// sees the workspaces they are a member of.
func (b *Backend) authorize(tx *storage.Tx, includeIgnored bool) (domain.Caller, error) {
	if !b.authorized {
		caller := domain.Caller{Name: b.identity, Unrestricted: true}
		if includeIgnored || b.identity == "" || !b.userPreferences {
			return caller, nil
		}
		exists, err := tx.UserExists(b.identity)
		if err != nil {
			return domain.Caller{}, err
		}
		if exists {
			tx.Restrict(storage.Everything().HideIgnoredBy(b.identity))
		}
		return caller, nil
	}
	caller, err := tx.Caller(b.identity)
	if err != nil {
		return domain.Caller{}, err
	}
	if !caller.WorkspaceAdmin {
		tx.Restrict(storage.VisibleTo(caller.Name))
	}
	if !includeIgnored {
		tx.Restrict(tx.Scope().HideIgnoredBy(caller.Name))
	}
	return caller, nil
}

// write runs one mutation inside a single BEGIN IMMEDIATE transaction, as the
// caller and with their scope. The object an operation returns is re-read
// inside that same transaction, so the derived fields reflect the change.
func (b *Backend) write(ctx context.Context, fn func(*storage.Tx, domain.Caller) error) error {
	return b.db.Write(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, false)
		if err != nil {
			return err
		}
		return fn(tx, caller)
	})
}

// writeIncludingIgnored is for a dedicated recovery/administration path whose
// authorization still comes from workspace membership but whose own preference
// boundary must not hide the workspace it administers. The check and mutation
// remain in the same BEGIN IMMEDIATE transaction.
func (b *Backend) writeIncludingIgnored(
	ctx context.Context, fn func(*storage.Tx, domain.Caller) error,
) error {
	return b.db.Write(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		return fn(tx, caller)
	})
}

// read runs one read-only operation as the caller and with their scope.
func (b *Backend) read(ctx context.Context, fn func(*storage.Tx, domain.Caller) error) error {
	return b.db.Read(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, false)
		if err != nil {
			return err
		}
		return fn(tx, caller)
	})
}

// readIncludingIgnored is the read half of the same dedicated administration
// path. It omits only the caller's preference boundary, never authorization.
func (b *Backend) readIncludingIgnored(
	ctx context.Context, fn func(*storage.Tx, domain.Caller) error,
) error {
	return b.db.Read(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		return fn(tx, caller)
	})
}

// readIssue is the common shape of the read-only operations.
func (b *Backend) readIssue(ctx context.Context, ref string) (*domain.Issue, error) {
	var issue *domain.Issue
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
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
