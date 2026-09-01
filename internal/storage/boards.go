package storage

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// ListBoardEpics returns visible epic issues in the workspaces selected by a
// board. A nil workspace set means every workspace allowed by the transaction;
// a non-nil empty set means none.
func (t *Tx) ListBoardEpics(workspaces []string, limit, offset *int) ([]domain.Issue, int, error) {
	if workspaces != nil && len(workspaces) == 0 {
		return []domain.Issue{}, 0, nil
	}
	return t.ListIssues(&domain.Filter{
		Workspaces: workspaces, Types: []domain.Type{domain.TypeEpic}, IncludeClosed: true,
		Limit: limit, Offset: offset, Sort: domain.Sort{Key: domain.SortID},
	})
}

func scanBoardView(row rowScanner) (*domain.BoardView, error) {
	var view domain.BoardView
	err := row.Scan(&view.ID, &view.Name, &view.Owner, &view.Shared,
		&view.AllWorkspaces, &view.PriorityMax, &view.CreatedAt, &view.UpdatedAt)
	return &view, err
}

// GetBoardView reads view metadata independently of workspace scope. The local
// layer checks ownership/sharing before exposing it, then separately filters
// the selected workspaces through the caller's transaction scope.
func (t *Tx) GetBoardView(id string) (*domain.BoardView, error) {
	view, err := scanBoardView(t.q.QueryRowContext(t.ctx, `
		SELECT id, name, owner, shared, all_workspaces, priority_max, created_at, updated_at
		  FROM board_views WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such board view: %s", id)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read board view %s", id)
	}
	if err := t.hydrateBoardView(view); err != nil {
		return nil, err
	}
	return view, nil
}

// ListBoardViews lists only one owner's views; shared views remain unlisted
// and are discovered by their stable URL.
func (t *Tx) ListBoardViews(owner string) ([]domain.BoardView, error) {
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT id, name, owner, shared, all_workspaces, priority_max, created_at, updated_at
		  FROM board_views WHERE owner = ? ORDER BY name, id`, owner)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list board views")
	}
	views := []domain.BoardView{}
	for rows.Next() {
		view, err := scanBoardView(rows)
		if err != nil {
			_ = rows.Close()
			return nil, awberr.Wrap(awberr.Runtime, err, "list board views")
		}
		views = append(views, *view)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, awberr.Wrap(awberr.Runtime, err, "list board views")
	}
	if err := rows.Close(); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list board views")
	}
	// Drain the metadata cursor before issuing the three child queries. This
	// works with drivers that do not permit a second active result set.
	for i := range views {
		if err := t.hydrateBoardView(&views[i]); err != nil {
			return nil, err
		}
	}
	return views, nil
}

func (t *Tx) hydrateBoardView(view *domain.BoardView) error {
	load := func(query string, target *[]string) error {
		rows, err := t.q.QueryContext(t.ctx, query, view.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				return err
			}
			*target = append(*target, value)
		}
		return rows.Err()
	}
	if err := load(`SELECT workspace FROM board_view_workspaces WHERE view = ? ORDER BY workspace`, &view.Workspaces); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view workspaces")
	}
	if err := load(`SELECT label FROM board_view_labels WHERE view = ? ORDER BY label`, &view.Labels); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view labels")
	}
	if err := load(`SELECT assignee FROM board_view_assignees WHERE view = ? ORDER BY assignee`, &view.Assignees); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view assignees")
	}
	view.Normalize()
	return nil
}

func (t *Tx) InsertBoardView(view *domain.BoardView) error {
	now := Now()
	view.CreatedAt, view.UpdatedAt = now, now
	_, err := t.q.ExecContext(t.ctx, `INSERT INTO board_views
		(id, name, owner, shared, all_workspaces, priority_max, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, view.ID, view.Name, view.Owner, view.Shared,
		view.AllWorkspaces, view.PriorityMax, now, now)
	if isUniqueViolation(err) {
		return awberr.Conflictf("board view already exists: %s", view.ID)
	}
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create board view")
	}
	return t.replaceBoardViewFilters(view)
}

// UpdateBoardView moves its ETag only when the stored representation changes.
func (t *Tx) UpdateBoardView(existing, next *domain.BoardView) error {
	if existing.Name == next.Name && existing.Shared == next.Shared &&
		existing.AllWorkspaces == next.AllWorkspaces && existing.PriorityMax == next.PriorityMax &&
		slices.Equal(existing.Workspaces, next.Workspaces) && slices.Equal(existing.Labels, next.Labels) &&
		slices.Equal(existing.Assignees, next.Assignees) {
		return nil
	}
	next.UpdatedAt = bumpedTimestamp(existing.UpdatedAt, Now())
	_, err := t.q.ExecContext(t.ctx, `UPDATE board_views SET name = ?, shared = ?,
		all_workspaces = ?, priority_max = ?, updated_at = ? WHERE id = ?`, next.Name,
		next.Shared, next.AllWorkspaces, next.PriorityMax, next.UpdatedAt, existing.ID)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update board view %s", existing.ID)
	}
	return t.replaceBoardViewFilters(next)
}

func (t *Tx) replaceBoardViewFilters(view *domain.BoardView) error {
	for _, table := range []string{"board_view_workspaces", "board_view_labels", "board_view_assignees"} {
		if _, err := t.q.ExecContext(t.ctx, `DELETE FROM `+table+` WHERE view = ?`, view.ID); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "replace board view filters")
		}
	}
	for _, value := range view.Workspaces {
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO board_view_workspaces (view, workspace) VALUES (?, ?)`, view.ID, value); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "store board view workspace")
		}
	}
	for _, value := range view.Labels {
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO board_view_labels (view, label) VALUES (?, ?)`, view.ID, value); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "store board view label")
		}
	}
	for _, value := range view.Assignees {
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO board_view_assignees (view, assignee) VALUES (?, ?)`, view.ID, value); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "store board view assignee")
		}
	}
	return nil
}

func (t *Tx) DeleteBoardView(id string) error {
	result, err := t.q.ExecContext(t.ctx, `DELETE FROM board_views WHERE id = ?`, id)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete board view %s", id)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete board view %s", id)
	}
	if changed == 0 {
		return awberr.NotFoundf("no such board view: %s", id)
	}
	return nil
}

// bumpBoardViewsSelectingWorkspace moves each affected view's ETag before the
// workspace's foreign-key cascade removes its selected-workspace row.
func (t *Tx) bumpBoardViewsSelectingWorkspace(workspace string) error {
	rows, err := t.q.QueryContext(t.ctx, `SELECT selected.view, views.updated_at
		FROM board_view_workspaces AS selected
		JOIN board_views AS views ON views.id = selected.view
		WHERE selected.workspace = ?`, workspace)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "find board views selecting workspace %s", workspace)
	}
	type selectedView struct{ id, updatedAt string }
	views := []selectedView{}
	for rows.Next() {
		var view selectedView
		if err := rows.Scan(&view.id, &view.updatedAt); err != nil {
			_ = rows.Close()
			return awberr.Wrap(awberr.Runtime, err, "find board views selecting workspace %s", workspace)
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return awberr.Wrap(awberr.Runtime, err, "find board views selecting workspace %s", workspace)
	}
	if err := rows.Close(); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "find board views selecting workspace %s", workspace)
	}
	now := Now()
	for _, view := range views {
		if _, err := t.q.ExecContext(t.ctx, `UPDATE board_views SET updated_at = ? WHERE id = ?`,
			bumpedTimestamp(view.updatedAt, now), view.id); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "update board view %s before deleting workspace", view.id)
		}
	}
	return nil
}
