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
		SELECT p.key, p.name, p.description, p.created_at, p.updated_at,
		       (SELECT count(*) FROM issues i
		         WHERE i.project = p.key AND i.status <> 'closed')
		  FROM projects p
		 WHERE p.key = ? AND `+visible, append([]any{key}, args...)...,
	).Scan(&p.Key, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such project: %s", key)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read project %s", key)
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
		return false, awberr.Wrap(awberr.Runtime, err, "read project %s", key)
	}
	return true, nil
}

// ListProjects returns projects in a total order. limit and offset page the
// result; total is the unpaged count.
func (t *Tx) ListProjects(filter string, sort domain.ProjectSort,
	limit, offset *int) (projects []domain.Project, total int, err error) {
	visible, args := t.visibleClause("p.key")
	where := visible
	for _, word := range strings.Fields(filter) {
		where += ` AND instr(awb_casefold(p.key || ' ' || p.name || ' ' || p.description), awb_casefold(?)) > 0`
		args = append(args, word)
	}
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM projects p WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count projects")
	}

	direction := "ASC"
	if sort.Desc {
		direction = "DESC"
	}
	active := `(SELECT count(*) FROM issues i
		         WHERE i.project = p.key AND i.status <> 'closed')`
	order := "p.key " + direction
	switch sort.Key {
	case domain.ProjectSortActive:
		order = active + " " + direction + ", p.key ASC"
	case domain.ProjectSortUpdated:
		order = "p.updated_at " + direction + ", p.key ASC"
	case domain.ProjectSortByKey:
	default:
		order = "p.key ASC"
	}

	query := `
		SELECT p.key, p.name, p.description, p.created_at, p.updated_at,
		       ` + active + `
		  FROM projects p
		 WHERE ` + where + `
		 ORDER BY ` + order + limitOffsetClause(limit, offset)

	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list projects")
	}
	defer rows.Close()

	projects = []domain.Project{}
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.Key, &p.Name, &p.Description,
			&p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list projects")
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list projects")
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
		SELECT p.key, p.name, p.description, p.created_at, p.updated_at, `+active+`
		  FROM projects p
		 WHERE `+visible+` AND (instr(lower(p.key), lower(?)) > 0 OR instr(lower(p.name), lower(?)) > 0)
		 ORDER BY p.key ASC LIMIT ?`, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "search projects for navigation")
	}
	defer rows.Close()
	projects := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.Key, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "search projects for navigation")
		}
		projects = append(projects, p)
	}
	return projects, awberr.Wrap(awberr.Runtime, rows.Err(), "search projects for navigation")
}

// InsertProject stores a new project.
func (t *Tx) InsertProject(key, name, description string) error {
	now := Now()
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO projects (key, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, key, name, description, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return awberr.Conflictf("project %s already exists", key)
		}
		return awberr.Wrap(awberr.Runtime, err, "create project %s", key)
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
		return awberr.Wrap(awberr.Runtime, err, "update project %s", p.Key)
	}
	p.Name = name
	p.Description = description
	p.UpdatedAt = updated
	return nil
}

// CountIssuesInProject counts every issue the project holds, closed ones
// included. That is deliberately wider than the active count project list shows:
// project delete refuses while a project holds any issue at all, so --force alone
// can never destroy closed history.
func (t *Tx) CountIssuesInProject(key string) (int, error) {
	var n int
	err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM issues WHERE project = ?`, key).Scan(&n)
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "count issues in project %s", key)
	}
	return n, nil
}

// DeleteProjectIssues removes every issue the project holds, and with them
// their labels and every relation they take part in — including relations to
// issues in other projects, which may unblock work elsewhere. It reports how
// many issues went.
func (t *Tx) DeleteProjectIssues(key string) (int, error) {
	result, err := t.q.ExecContext(t.ctx, `DELETE FROM issues WHERE project = ?`, key)
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issues in project %s", key)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issues in project %s", key)
	}
	return int(n), nil
}

// DeleteProject removes the project row itself.
func (t *Tx) DeleteProject(key string) error {
	if err := t.bumpBoardViewsSelectingProject(key); err != nil {
		return err
	}
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM projects WHERE key = ?`, key)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete project %s", key)
	}
	return nil
}
