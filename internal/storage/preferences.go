package storage

import (
	"database/sql"
	"errors"

	"github.com/tofutools/awb/internal/awberr"
)

// UserExists reports whether an identity has a stored preference owner. Direct
// mode has an identity even when the database has no accounts, in which case
// there is deliberately no ignore set to apply.
func (t *Tx) UserExists(name string) (bool, error) {
	var one int
	err := t.q.QueryRowContext(t.ctx, `SELECT 1 FROM users WHERE name = ?`, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read preference user %s", name)
	}
	return true, nil
}

// IgnoredWorkspaces returns one user's ignore set. It is intentionally not
// scoped: callers use it only after resolving that same user's ordinary
// authorization, and the recovery endpoint must be able to see the rows it is
// meant to remove.
func (t *Tx) IgnoredWorkspaces(user string) (map[string]bool, error) {
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT workspace FROM ignored_workspaces WHERE user = ? ORDER BY workspace`, user)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list ignored workspaces of %s", user)
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var workspace string
		if err := rows.Scan(&workspace); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read ignored workspace of %s", user)
		}
		result[workspace] = true
	}
	return result, awberr.Wrap(awberr.Runtime, rows.Err(), "list ignored workspaces of %s", user)
}

// SetWorkspaceIgnored idempotently changes one preference.
func (t *Tx) SetWorkspaceIgnored(user, workspace string, ignored bool) error {
	var err error
	if ignored {
		_, err = t.q.ExecContext(t.ctx,
			`INSERT INTO ignored_workspaces (user, workspace) VALUES (?, ?)
			 ON CONFLICT (user, workspace) DO NOTHING`, user, workspace)
	} else {
		_, err = t.q.ExecContext(t.ctx,
			`DELETE FROM ignored_workspaces WHERE user = ? AND workspace = ?`, user, workspace)
	}
	return awberr.Wrap(awberr.Runtime, err, "set ignored preference for %s in %s", user, workspace)
}

// ForgetWorkspaceIgnored makes newly granted access active by default.
func (t *Tx) ForgetWorkspaceIgnored(user, workspace string) error {
	_, err := t.q.ExecContext(t.ctx,
		`DELETE FROM ignored_workspaces WHERE user = ? AND workspace = ?`, user, workspace)
	return awberr.Wrap(awberr.Runtime, err, "forget ignored preference for %s in %s", user, workspace)
}

// ForgetUnownedIgnoredWorkspaces removes workspace-admin preferences that cease to
// be authorized when that flag is withdrawn.
func (t *Tx) ForgetUnownedIgnoredWorkspaces(user string) error {
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM ignored_workspaces
		WHERE user = ? AND workspace NOT IN
			(SELECT workspace FROM workspace_members WHERE user = ?)`, user, user)
	return awberr.Wrap(awberr.Runtime, err, "forget inaccessible ignored workspaces of %s", user)
}
