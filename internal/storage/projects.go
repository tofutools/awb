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

// GetProject reads one project, with its derived active-issue count.
//
// A project outside the transaction's scope is not found rather than refused,
// because a caller who is not a member of it is not being told that it exists.
func (t *Tx) GetProject(key string) (*domain.Project, error) {
	visible, args := t.visibleClause("p.key")
	var p domain.Project
	err := t.q.QueryRowContext(t.ctx, `
		SELECT p.key, p.name, p.description, p.state, p.archived_at, p.archived_by,
		       p.created_at, p.updated_at,
		       (SELECT count(*) FROM issues i
		         WHERE i.project = p.key AND i.status <> 'closed')
		  FROM projects p
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

// ProjectExists reports whether a project with this key is stored and visible
// to the caller.
func (t *Tx) ProjectExists(key string) (bool, error) {
	visible, args := t.visibleClause("key")
	var one int
	err := t.q.QueryRowContext(t.ctx,
		`SELECT 1 FROM projects WHERE key = ? AND `+visible,
		append([]any{key}, args...)...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read workspace %s", key)
	}
	return true, nil
}

// ListProjects returns projects in a total order. limit and offset page the
// result; total is the unpaged count.
func (t *Tx) ListProjects(filter string, sort domain.ProjectSort,
	limit, offset *int) ([]domain.Project, int, error) {
	return t.ListProjectsByState(filter, domain.ProjectsActive, sort, limit, offset)
}

func (t *Tx) ListProjectsByState(filter string, state domain.ProjectStateFilter, sort domain.ProjectSort,
	limit, offset *int) (projects []domain.Project, total int, err error) {
	visible, args := t.visibleClause("p.key")
	where := visible
	switch state {
	case domain.ProjectsActive:
		where += ` AND p.state = 'active'`
	case domain.ProjectsArchived:
		where += ` AND p.state = 'archived'`
	case domain.ProjectsAll:
	}
	for _, word := range strings.Fields(filter) {
		where += ` AND instr(awb_casefold(p.key || ' ' || p.name || ' ' || p.description), awb_casefold(?)) > 0`
		args = append(args, word)
	}
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM projects p WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count workspaces")
	}

	direction := "ASC"
	if sort.Desc {
		direction = "DESC"
	}
	active := `(SELECT count(*) FROM issues i
		         WHERE i.project = p.key AND i.status <> 'closed')`
	// Every branch names its own order, including the by-key one. The direction
	// applies to the named key alone; the key tiebreak after a derived order
	// stays ascending, as the issue listings' id tiebreak does.
	order := "p.key ASC" // an absent sort key, which is the default order
	switch sort.Key {
	case domain.ProjectSortByKey:
		order = "p.key " + direction
	case domain.ProjectSortActive:
		order = active + " " + direction + ", p.key ASC"
	case domain.ProjectSortUpdated:
		order = "p.updated_at " + direction + ", p.key ASC"
	}

	query := `
		SELECT p.key, p.name, p.description, p.state, p.archived_at, p.archived_by,
		       p.created_at, p.updated_at,
		       ` + active + `
		  FROM projects p
		 WHERE ` + where + `
		 ORDER BY ` + order + limitOffsetClause(limit, offset)

	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list workspaces")
	}
	defer rows.Close()

	projects = []domain.Project{}
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.Key, &p.Name, &p.Description, &p.State, &p.ArchivedAt, &p.ArchivedBy,
			&p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list workspaces")
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list workspaces")
	}
	return projects, total, nil
}

// SearchProjectsForNavigation performs a bounded substring search over the
// immutable key and display name, within the transaction's visible projects.
func (t *Tx) SearchProjectsForNavigation(query string, limit int) ([]domain.Project, error) {
	visible, args := t.visibleClause("p.key")
	args = append(args, query, query, limit)
	active := `(SELECT count(*) FROM issues i WHERE i.project = p.key AND i.status <> 'closed')`
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT p.key, p.name, p.description, p.state, p.archived_at, p.archived_by,
		       p.created_at, p.updated_at, `+active+`
		  FROM projects p
		 WHERE `+visible+` AND p.state = 'active'
		   AND (instr(lower(p.key), lower(?)) > 0 OR instr(lower(p.name), lower(?)) > 0)
		 ORDER BY p.key ASC LIMIT ?`, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "search workspaces for navigation")
	}
	defer rows.Close()
	projects := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.Key, &p.Name, &p.Description, &p.State, &p.ArchivedAt, &p.ArchivedBy,
			&p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "search workspaces for navigation")
		}
		projects = append(projects, p)
	}
	return projects, awberr.Wrap(awberr.Runtime, rows.Err(), "search workspaces for navigation")
}

// InsertProject stores a new project.
func (t *Tx) InsertProject(key, name, description string) error {
	now := Now()
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO projects (key, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, key, name, description, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return awberr.Conflictf("workspace %s already exists", key)
		}
		return awberr.Wrap(awberr.Runtime, err, "create workspace %s", key)
	}
	return nil
}

// UpdateProject writes a project's name and description, moving updated_at
// only when something actually changed. Creating, changing or deleting an
// issue the project holds does not touch it: active_issues is derived, not
// stored.
func (t *Tx) UpdateProject(p *domain.Project, name, description string) error {
	if name == p.Name && description == p.Description {
		return nil
	}
	updated := bumpedTimestamp(p.UpdatedAt, Now())
	_, err := t.q.ExecContext(t.ctx, `
		UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE key = ?`,
		name, description, updated, p.Key)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update workspace %s", p.Key)
	}
	p.Name = name
	p.Description = description
	p.UpdatedAt = updated
	return nil
}

// SetProjectState changes lifecycle state and appends its audit entry in the
// same transaction. Repeating the current state is idempotent and records no
// duplicate history.
func (t *Tx) SetProjectState(p *domain.Project, state domain.ProjectState, actor string) (bool, error) {
	if p.State == state {
		return false, nil
	}
	now := Now()
	updated := bumpedTimestamp(p.UpdatedAt, now)
	archivedAt, archivedBy, action := "", "", "restored"
	if state == domain.ProjectArchived {
		archivedAt, archivedBy, action = now, actor, "archived"
	}
	if _, err := t.q.ExecContext(t.ctx, `UPDATE projects
		SET state = ?, archived_at = ?, archived_by = ?, updated_at = ? WHERE key = ?`,
		state, archivedAt, archivedBy, updated, p.Key); err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "change lifecycle of workspace %s", p.Key)
	}
	if _, err := t.q.ExecContext(t.ctx, `INSERT INTO project_activity
		(project, action, actor, created_at) VALUES (?, ?, ?, ?)`, p.Key, action, actor, now); err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "record lifecycle of workspace %s", p.Key)
	}
	p.State, p.ArchivedAt, p.ArchivedBy, p.UpdatedAt = state, archivedAt, archivedBy, updated
	return true, nil
}

func (t *Tx) ListProjectActivity(key string, limit, offset *int) ([]domain.ProjectActivity, int, error) {
	var total int
	if err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM project_activity WHERE project = ?`, key).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count lifecycle of workspace %s", key)
	}
	rows, err := t.q.QueryContext(t.ctx, `SELECT id, project, action, actor, created_at
		FROM project_activity WHERE project = ? ORDER BY created_at DESC, id DESC`+
		limitOffsetClause(limit, offset), key)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list lifecycle of workspace %s", key)
	}
	defer rows.Close()
	entries := []domain.ProjectActivity{}
	for rows.Next() {
		var entry domain.ProjectActivity
		if err := rows.Scan(&entry.ID, &entry.Project, &entry.Action, &entry.Actor, &entry.CreatedAt); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list lifecycle of workspace %s", key)
		}
		entries = append(entries, entry)
	}
	return entries, total, awberr.Wrap(awberr.Runtime, rows.Err(), "list lifecycle of workspace %s", key)
}

// CountIssuesInProject counts every issue the project holds, closed ones
// included. That is deliberately wider than the active count project list shows:
// project delete refuses while a project holds any issue at all, so --force alone
// can never destroy closed history.
func (t *Tx) CountIssuesInProject(key string) (int, error) {
	var n int
	err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM issues WHERE project = ?`, key).Scan(&n)
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "count issues in workspace %s", key)
	}
	return n, nil
}

// ProjectRelationsTouchArchived reports whether deleting this project's
// issues would remove a relation whose other retained endpoint is read-only.
func (t *Tx) ProjectRelationsTouchArchived(key string) (bool, error) {
	var found bool
	err := t.q.QueryRowContext(t.ctx, `SELECT EXISTS (
		SELECT 1 FROM relations r
		JOIN issues subject ON subject.id = r.subject
		JOIN issues other ON other.id = r.other
		JOIN projects subject_project ON subject_project.key = subject.project
		JOIN projects other_project ON other_project.key = other.project
		WHERE (subject.project = ? OR other.project = ?)
		  AND (subject_project.state = 'archived' OR other_project.state = 'archived')
	)`, key, key).Scan(&found)
	return found, awberr.Wrap(awberr.Runtime, err, "inspect archived relations of workspace %s", key)
}

// DeleteProjectIssues removes every issue the project holds, and with them
// their labels and every relation they take part in — including relations to
// issues in other projects, which may unblock work elsewhere. It reports how
// many issues went.
func (t *Tx) DeleteProjectIssues(key string) (int, error) {
	result, err := t.q.ExecContext(t.ctx, `DELETE FROM issues WHERE project = ?`, key)
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issues in workspace %s", key)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issues in workspace %s", key)
	}
	return int(n), nil
}

// DeleteProject removes the project row itself.
func (t *Tx) DeleteProject(key string) error {
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM projects WHERE key = ?`, key)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete workspace %s", key)
	}
	return nil
}
