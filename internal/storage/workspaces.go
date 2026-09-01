package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// Now returns the current time in the form awb stores.
func Now() string { return domain.FormatTime(time.Now()) }

// bumpedTimestamp returns the timestamp to store for a row whose previous
// updated_at was stored, keeping the value strictly increasing per row. If the
// clock yields a value that is not greater than the stored one — the system
// clock may have a coarser resolution than a millisecond — the stored value
// plus one millisecond is written instead.
//
// This is what makes an ETag identify a version rather than an instant: two
// successive versions of one row can never carry the same timestamp, whatever
// the host clock's real resolution is.
func bumpedTimestamp(stored, now string) string {
	if now > stored {
		return now
	}
	t, err := domain.ParseTime(stored)
	if err != nil {
		// A stored timestamp always parses; if one somehow does not, moving forward
		// by taking the clock's value is better than failing a write.
		return now
	}
	return domain.FormatTime(t.Add(time.Millisecond))
}

// GetWorkspace reads one workspace, with its derived active-issue count.
//
// A workspace outside the transaction's scope is not found rather than refused,
// because a caller who is not a member of it is not being told that it exists.
func (t *Tx) GetWorkspace(key string) (*domain.Workspace, error) {
	visible, args := t.visibleClause("p.key")
	var p domain.Workspace
	err := t.q.QueryRowContext(t.ctx, `
		SELECT p.key, p.name, p.description, p.state, p.archived_at, p.archived_by,
		       p.created_at, p.updated_at,
		       (SELECT count(*) FROM issues i
		         WHERE i.workspace = p.key AND i.status <> 'closed')
		  FROM workspaces p
		 WHERE p.key = ? AND `+visible, append([]any{key}, args...)...,
	).Scan(&p.Key, &p.Name, &p.Description, &p.State, &p.ArchivedAt, &p.ArchivedBy,
		&p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such workspace: %s", key)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read workspace %s", key)
	}
	return &p, nil
}

// WorkspaceExists reports whether a workspace with this key is stored and visible
// to the caller.
func (t *Tx) WorkspaceExists(key string) (bool, error) {
	visible, args := t.visibleClause("key")
	var one int
	err := t.q.QueryRowContext(t.ctx,
		`SELECT 1 FROM workspaces WHERE key = ? AND `+visible,
		append([]any{key}, args...)...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read workspace %s", key)
	}
	return true, nil
}

// ActiveWorkspaceExists reports whether a current workspace with this key is
// stored and visible to the caller. Archived workspaces remain directly
// addressable history, but are not selectable by everyday board views.
func (t *Tx) ActiveWorkspaceExists(key string) (bool, error) {
	visible, args := t.visibleClause("key")
	var one int
	err := t.q.QueryRowContext(t.ctx,
		`SELECT 1 FROM workspaces WHERE key = ? AND state = 'active' AND `+visible,
		append([]any{key}, args...)...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read current workspace %s", key)
	}
	return true, nil
}

// ListWorkspaces returns workspaces in a total order. limit and offset page the
// result; total is the unpaged count.
func (t *Tx) ListWorkspaces(filter string, sort domain.WorkspaceSort,
	limit, offset *int) ([]domain.Workspace, int, error) {
	return t.ListWorkspacesByState(filter, domain.WorkspacesActive, sort, limit, offset)
}

func (t *Tx) ListWorkspacesByState(filter string, state domain.WorkspaceStateFilter, sort domain.WorkspaceSort,
	limit, offset *int) (workspaces []domain.Workspace, total int, err error) {
	visible, args := t.visibleClause("p.key")
	where := visible
	switch state {
	case domain.WorkspacesActive:
		where += ` AND p.state = 'active'`
	case domain.WorkspacesArchived:
		where += ` AND p.state = 'archived'`
	case domain.WorkspacesAll:
	}
	for _, word := range strings.Fields(filter) {
		where += ` AND instr(awb_casefold(p.key || ' ' || p.name || ' ' || p.description), awb_casefold(?)) > 0`
		args = append(args, word)
	}
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM workspaces p WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count workspaces")
	}

	direction := "ASC"
	if sort.Desc {
		direction = "DESC"
	}
	active := `(SELECT count(*) FROM issues i
		         WHERE i.workspace = p.key AND i.status <> 'closed')`
	// Every branch names its own order, including the by-key one. The direction
	// applies to the named key alone; the key tiebreak after a derived order
	// stays ascending, as the issue listings' id tiebreak does.
	order := "p.key ASC" // an absent sort key, which is the default order
	switch sort.Key {
	case domain.WorkspaceSortByKey:
		order = "p.key " + direction
	case domain.WorkspaceSortActive:
		order = active + " " + direction + ", p.key ASC"
	case domain.WorkspaceSortUpdated:
		order = "p.updated_at " + direction + ", p.key ASC"
	}

	query := `
		SELECT p.key, p.name, p.description, p.state, p.archived_at, p.archived_by,
		       p.created_at, p.updated_at,
		       ` + active + `
		  FROM workspaces p
		 WHERE ` + where + `
		 ORDER BY ` + order + limitOffsetClause(limit, offset)

	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list workspaces")
	}
	defer rows.Close()

	workspaces = []domain.Workspace{}
	for rows.Next() {
		var p domain.Workspace
		if err := rows.Scan(&p.Key, &p.Name, &p.Description, &p.State, &p.ArchivedAt, &p.ArchivedBy,
			&p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list workspaces")
		}
		workspaces = append(workspaces, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list workspaces")
	}
	return workspaces, total, nil
}

// SearchWorkspacesForNavigation performs a bounded substring search over the
// immutable key and display name, within the transaction's visible workspaces.
func (t *Tx) SearchWorkspacesForNavigation(query string, limit int) ([]domain.Workspace, error) {
	visible, args := t.visibleClause("p.key")
	args = append(args, query, query, limit)
	active := `(SELECT count(*) FROM issues i WHERE i.workspace = p.key AND i.status <> 'closed')`
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT p.key, p.name, p.description, p.state, p.archived_at, p.archived_by,
		       p.created_at, p.updated_at, `+active+`
		  FROM workspaces p
		 WHERE `+visible+` AND p.state = 'active'
		   AND (instr(lower(p.key), lower(?)) > 0 OR instr(lower(p.name), lower(?)) > 0)
		 ORDER BY p.key ASC LIMIT ?`, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "search workspaces for navigation")
	}
	defer rows.Close()
	workspaces := []domain.Workspace{}
	for rows.Next() {
		var p domain.Workspace
		if err := rows.Scan(&p.Key, &p.Name, &p.Description, &p.State, &p.ArchivedAt, &p.ArchivedBy,
			&p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "search workspaces for navigation")
		}
		workspaces = append(workspaces, p)
	}
	return workspaces, awberr.Wrap(awberr.Runtime, rows.Err(), "search workspaces for navigation")
}

// InsertWorkspace stores a new workspace.
func (t *Tx) InsertWorkspace(key, name, description string) error {
	now := Now()
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO workspaces (key, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, key, name, description, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return awberr.Conflictf("workspace %s already exists", key)
		}
		return awberr.Wrap(awberr.Runtime, err, "create workspace %s", key)
	}
	return nil
}

// UpdateWorkspace writes a workspace's name and description, moving updated_at
// only when something actually changed. Creating, changing or deleting an
// issue the workspace holds does not touch it: active_issues is derived, not
// stored.
func (t *Tx) UpdateWorkspace(p *domain.Workspace, name, description string) error {
	if name == p.Name && description == p.Description {
		return nil
	}
	updated := bumpedTimestamp(p.UpdatedAt, Now())
	_, err := t.q.ExecContext(t.ctx, `
		UPDATE workspaces SET name = ?, description = ?, updated_at = ? WHERE key = ?`,
		name, description, updated, p.Key)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update workspace %s", p.Key)
	}
	p.Name = name
	p.Description = description
	p.UpdatedAt = updated
	return nil
}

// SetWorkspaceState changes lifecycle state and appends its audit entry in the
// same transaction. Repeating the current state is idempotent and records no
// duplicate history.
func (t *Tx) SetWorkspaceState(p *domain.Workspace, state domain.WorkspaceState, actor string) (bool, error) {
	if p.State == state {
		return false, nil
	}
	now := Now()
	updated := bumpedTimestamp(p.UpdatedAt, now)
	archivedAt, archivedBy, action := "", "", "restored"
	if state == domain.WorkspaceArchived {
		archivedAt, archivedBy, action = now, actor, "archived"
	}
	if _, err := t.q.ExecContext(t.ctx, `UPDATE workspaces
		SET state = ?, archived_at = ?, archived_by = ?, updated_at = ? WHERE key = ?`,
		state, archivedAt, archivedBy, updated, p.Key); err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "change lifecycle of workspace %s", p.Key)
	}
	if _, err := t.q.ExecContext(t.ctx, `INSERT INTO workspace_activity
		(workspace, action, actor, created_at) VALUES (?, ?, ?, ?)`, p.Key, action, actor, now); err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "record lifecycle of workspace %s", p.Key)
	}
	p.State, p.ArchivedAt, p.ArchivedBy, p.UpdatedAt = state, archivedAt, archivedBy, updated
	return true, nil
}

func (t *Tx) ListWorkspaceActivity(key string, limit, offset *int) ([]domain.WorkspaceActivity, int, error) {
	var total int
	if err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM workspace_activity WHERE workspace = ?`, key).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count lifecycle of workspace %s", key)
	}
	rows, err := t.q.QueryContext(t.ctx, `SELECT id, workspace, action, actor, created_at
		FROM workspace_activity WHERE workspace = ? ORDER BY created_at DESC, id DESC`+
		limitOffsetClause(limit, offset), key)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list lifecycle of workspace %s", key)
	}
	defer rows.Close()
	entries := []domain.WorkspaceActivity{}
	for rows.Next() {
		var entry domain.WorkspaceActivity
		if err := rows.Scan(&entry.ID, &entry.Workspace, &entry.Action, &entry.Actor, &entry.CreatedAt); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list lifecycle of workspace %s", key)
		}
		entries = append(entries, entry)
	}
	return entries, total, awberr.Wrap(awberr.Runtime, rows.Err(), "list lifecycle of workspace %s", key)
}

// CountIssuesInWorkspace counts every issue the workspace holds, closed ones
// included. That is deliberately wider than the active count workspace list shows:
// workspace delete refuses while a workspace holds any issue at all, so --force alone
// can never destroy closed history.
func (t *Tx) CountIssuesInWorkspace(key string) (int, error) {
	var n int
	err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM issues WHERE workspace = ?`, key).Scan(&n)
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "count issues in workspace %s", key)
	}
	return n, nil
}

// WorkspaceRelationsTouchArchived reports whether deleting this workspace's
// issues would remove a relation whose other retained endpoint is read-only.
func (t *Tx) WorkspaceRelationsTouchArchived(key string) (bool, error) {
	var found bool
	err := t.q.QueryRowContext(t.ctx, `SELECT EXISTS (
		SELECT 1 FROM relations r
		JOIN issues subject ON subject.id = r.subject
		JOIN issues other ON other.id = r.other
		JOIN workspaces subject_workspace ON subject_workspace.key = subject.workspace
		JOIN workspaces other_workspace ON other_workspace.key = other.workspace
		WHERE (subject.workspace = ? OR other.workspace = ?)
		  AND (subject_workspace.state = 'archived' OR other_workspace.state = 'archived')
	)`, key, key).Scan(&found)
	return found, awberr.Wrap(awberr.Runtime, err, "inspect archived relations of workspace %s", key)
}

// DeleteWorkspaceIssues removes every issue the workspace holds, and with them
// their labels and every relation they take part in — including relations to
// issues in other workspaces, which may unblock work elsewhere. It reports how
// many issues went.
func (t *Tx) DeleteWorkspaceIssues(key string) (int, error) {
	result, err := t.q.ExecContext(t.ctx, `DELETE FROM issues WHERE workspace = ?`, key)
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issues in workspace %s", key)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issues in workspace %s", key)
	}
	return int(n), nil
}

// DeleteWorkspace removes the workspace row itself.
func (t *Tx) DeleteWorkspace(key string) error {
	if err := t.bumpBoardViewsSelectingWorkspace(key); err != nil {
		return err
	}
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM workspaces WHERE key = ?`, key)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete workspace %s", key)
	}
	return nil
}
