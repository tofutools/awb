package storage

import (
	"database/sql"
	"errors"
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
func (t *Tx) GetProject(key string) (*domain.Project, error) {
	var p domain.Project
	err := t.q.QueryRowContext(t.ctx, `
		SELECT p.key, p.name, p.description, p.created_at, p.updated_at,
		       (SELECT count(*) FROM issues i
		         WHERE i.project = p.key AND i.status <> 'closed')
		  FROM projects p
		 WHERE p.key = ?`, key,
	).Scan(&p.Key, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.ActiveIssues)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such project: %s", key)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read project %s", key)
	}
	return &p, nil
}

// ProjectExists reports whether a project with this key is stored.
func (t *Tx) ProjectExists(key string) (bool, error) {
	var one int
	err := t.q.QueryRowContext(t.ctx, `SELECT 1 FROM projects WHERE key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read project %s", key)
	}
	return true, nil
}

// ListProjects returns every project ordered by key ascending, which is what
// makes the corresponding endpoint pageable. limit and offset page the result;
// total is the unpaged count.
func (t *Tx) ListProjects(limit, offset *int) (projects []domain.Project, total int, err error) {
	if err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM projects`).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count projects")
	}

	query := `
		SELECT p.key, p.name, p.description, p.created_at, p.updated_at,
		       (SELECT count(*) FROM issues i
		         WHERE i.project = p.key AND i.status <> 'closed')
		  FROM projects p
		 ORDER BY p.key ASC` + limitOffsetClause(limit, offset)

	rows, err := t.q.QueryContext(t.ctx, query)
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
// included. That is deliberately wider than the active count project ls shows:
// project rm refuses while a project holds any issue at all, so --force alone
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
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM projects WHERE key = ?`, key)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete project %s", key)
	}
	return nil
}
