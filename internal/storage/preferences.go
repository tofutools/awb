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

// IgnoredProjects returns one user's ignore set. It is intentionally not
// scoped: callers use it only after resolving that same user's ordinary
// authorization, and the recovery endpoint must be able to see the rows it is
// meant to remove.
func (t *Tx) IgnoredProjects(user string) (map[string]bool, error) {
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT project FROM ignored_projects WHERE user = ? ORDER BY project`, user)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list ignored workspaces of %s", user)
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read ignored workspace of %s", user)
		}
		result[project] = true
	}
	return result, awberr.Wrap(awberr.Runtime, rows.Err(), "list ignored workspaces of %s", user)
}

// SetProjectIgnored idempotently changes one preference.
func (t *Tx) SetProjectIgnored(user, project string, ignored bool) error {
	var err error
	if ignored {
		_, err = t.q.ExecContext(t.ctx,
			`INSERT INTO ignored_projects (user, project) VALUES (?, ?)
			 ON CONFLICT (user, project) DO NOTHING`, user, project)
	} else {
		_, err = t.q.ExecContext(t.ctx,
			`DELETE FROM ignored_projects WHERE user = ? AND project = ?`, user, project)
	}
	return awberr.Wrap(awberr.Runtime, err, "set ignored preference for %s in %s", user, project)
}

// ForgetProjectIgnored makes newly granted access active by default.
func (t *Tx) ForgetProjectIgnored(user, project string) error {
	_, err := t.q.ExecContext(t.ctx,
		`DELETE FROM ignored_projects WHERE user = ? AND project = ?`, user, project)
	return awberr.Wrap(awberr.Runtime, err, "forget ignored preference for %s in %s", user, project)
}

// ForgetUnownedIgnoredProjects removes project-admin preferences that cease to
// be authorized when that flag is withdrawn.
func (t *Tx) ForgetUnownedIgnoredProjects(user string) error {
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM ignored_projects
		WHERE user = ? AND project NOT IN
			(SELECT project FROM project_members WHERE user = ?)`, user, user)
	return awberr.Wrap(awberr.Runtime, err, "forget inaccessible ignored workspaces of %s", user)
}
