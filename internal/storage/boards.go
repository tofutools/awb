package storage

import (
	"cmp"
	"database/sql"
	"errors"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// BoardColumnKey identifies one independently paged board column.
type BoardColumnKey struct {
	Epic   string
	Status domain.Status
}

// BoardColumnPage is one board column's bounded cards and unpaged total.
type BoardColumnPage struct {
	Issues []domain.Issue
	Total  int
}

// ListBoardEpics returns visible epic issues in the workspaces and optional
// explicit epic set selected by a board. A nil set means every value allowed
// by the transaction; a non-nil empty set means none.
func (t *Tx) ListBoardEpics(workspaces, epics, hiddenEpics []string, closedAfter string, includeBacklog bool, limit, offset *int) ([]domain.Issue, int, error) {
	if (workspaces != nil && len(workspaces) == 0) || (epics != nil && len(epics) == 0) {
		return []domain.Issue{}, 0, nil
	}
	return t.ListIssues(&domain.Filter{
		Workspaces: workspaces, ExcludeIDs: hiddenEpics, Types: []domain.Type{domain.TypeEpic},
		IDs:   epics,
		Limit: limit, Offset: offset, Sort: domain.Sort{Key: domain.SortID},
		IncludeClosed: true, ClosedAfter: closedAfter, ExcludeBacklog: !includeBacklog,
	})
}

// ListBoardColumns reads every requested epic/status column as one set. Counts
// and card pages remain independent per column, but the issue selection and
// backlog graph traversal run once rather than once for every column.
func (t *Tx) ListBoardColumns(workspaces, epics []string, statuses []domain.Status, closedAfter string,
	labels, assignees []string, priorityMax int, includeBacklog bool, limit, offset *int,
) (map[BoardColumnKey]BoardColumnPage, error) {
	result := make(map[BoardColumnKey]BoardColumnPage, len(epics)*len(statuses))
	if len(epics) == 0 || len(statuses) == 0 {
		return result, nil
	}

	filter := &domain.Filter{
		Workspaces: workspaces, Types: []domain.Type{domain.TypeFeature, domain.TypeBug, domain.TypeTask, domain.TypeChore},
		Statuses: statuses, ClosedAfter: closedAfter, ExcludeBacklog: !includeBacklog,
		Labels: labels, Assignees: assignees, PriorityMax: &priorityMax, Sort: domain.DefaultSort,
	}
	c := t.selection(filter)
	laneSet := make(map[string]bool, len(epics))
	selectedEpics := make([]string, 0, len(epics))
	includeNoEpic := false
	for _, epic := range epics {
		laneSet[epic] = true
		if epic == "" {
			includeNoEpic = true
		} else {
			selectedEpics = append(selectedEpics, epic)
		}
	}
	type candidate struct {
		id        string
		order     int
		priority  int
		updatedAt string
	}
	candidates := make(map[BoardColumnKey][]candidate, len(epics)*len(statuses))
	readCandidates := func(query string, args []any) error {
		rows, err := t.q.QueryContext(t.ctx, query, args...)
		if err != nil {
			return awberr.Wrap(awberr.Runtime, err, "list board columns")
		}
		for rows.Next() {
			var card candidate
			var key BoardColumnKey
			if err := rows.Scan(&card.id, &key.Status, &card.order, &card.priority, &card.updatedAt, &key.Epic); err != nil {
				_ = rows.Close()
				return awberr.Wrap(awberr.Runtime, err, "list board columns")
			}
			if !laneSet[key.Epic] {
				continue
			}
			candidates[key] = append(candidates[key], card)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return awberr.Wrap(awberr.Runtime, err, "list board columns")
		}
		if err := rows.Close(); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "list board columns")
		}
		return nil
	}
	if includeNoEpic {
		if err := readCandidates(`
			SELECT i.id, i.status, i.issue_order, i.priority, i.updated_at, ''
			  FROM issues i
			  LEFT JOIN relations er ON er.subject = i.id AND er.type = 'has-parent'
			  LEFT JOIN issues parent ON parent.id = er.other AND parent.type = 'epic'
			                         AND parent.workspace = i.workspace
			 WHERE `+c.where()+` AND parent.id IS NULL`, c.args); err != nil {
			return nil, err
		}
	}
	if len(selectedEpics) > 0 {
		args := append(append([]any{}, c.args...), anyArgs(selectedEpics)...)
		if err := readCandidates(`
			SELECT i.id, i.status, i.issue_order, i.priority, i.updated_at, parent.id
			  FROM relations er INDEXED BY idx_relations_other
			  JOIN issues i ON i.id = er.subject
			  JOIN issues parent ON parent.id = er.other AND parent.type = 'epic'
			                    AND parent.workspace = i.workspace
			 WHERE `+c.where()+` AND er.type = 'has-parent'
			   AND er.other IN (`+placeholders(len(selectedEpics))+`)`, args); err != nil {
			return nil, err
		}
	}

	cardOffset, cardLimit := 0, 0
	if offset != nil {
		cardOffset = *offset
	}
	if limit != nil {
		cardLimit = *limit
	}
	selected := make(map[BoardColumnKey][]string, len(candidates))
	allSelected := []string{}
	for key, cards := range candidates {
		slices.SortFunc(cards, func(a, b candidate) int {
			if (a.order == 0) != (b.order == 0) {
				if a.order == 0 {
					return 1
				}
				return -1
			}
			if a.order != b.order {
				return cmp.Compare(a.order, b.order)
			}
			if a.priority != b.priority {
				return cmp.Compare(a.priority, b.priority)
			}
			if a.updatedAt != b.updatedAt {
				return cmp.Compare(b.updatedAt, a.updatedAt)
			}
			return cmp.Compare(a.id, b.id)
		})
		page := BoardColumnPage{Total: len(cards)}
		start := min(cardOffset, len(cards))
		end := min(start+cardLimit, len(cards))
		for _, card := range cards[start:end] {
			selected[key] = append(selected[key], card.id)
			allSelected = append(allSelected, card.id)
		}
		result[key] = page
	}

	byID := make(map[string]domain.Issue, len(allSelected))
	const chunkSize = 400
	for start := 0; start < len(allSelected); start += chunkSize {
		end := min(start+chunkSize, len(allSelected))
		ids := allSelected[start:end]
		issues, err := t.queryIssues(`SELECT `+issueColumns+` FROM issues i WHERE i.id IN (`+
			placeholders(len(ids))+`)`, anyArgs(ids))
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			byID[issue.ID] = issue
		}
	}
	for key, ids := range selected {
		page := result[key]
		page.Issues = make([]domain.Issue, 0, len(ids))
		for _, id := range ids {
			page.Issues = append(page.Issues, byID[id])
		}
		result[key] = page
	}
	return result, nil
}

// ListVisibleEpicIDs filters a saved view's configured epic IDs through the
// transaction's visibility and active-workspace scope without hydrating the
// full issues one at a time.
func (t *Tx) ListVisibleEpicIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	c := t.selection(&domain.Filter{IDs: ids, Types: []domain.Type{domain.TypeEpic}, IncludeClosed: true})
	rows, err := t.q.QueryContext(t.ctx, `SELECT i.id FROM issues i WHERE `+c.where()+` ORDER BY i.id`, c.args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list visible board view epics")
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "list visible board view epics")
		}
		result = append(result, id)
	}
	return result, awberr.Wrap(awberr.Runtime, rows.Err(), "list visible board view epics")
}

func scanBoardView(row rowScanner) (*domain.BoardView, error) {
	var view domain.BoardView
	err := row.Scan(&view.ID, &view.Name, &view.Owner, &view.Shared,
		&view.AllWorkspaces, &view.AllEpics, &view.IncludeNoEpic,
		&view.PriorityMax, &view.CardLimit, &view.ClosedDays, &view.EpicClosedDays, &view.CreatedAt, &view.UpdatedAt)
	return &view, err
}

// GetBoardView reads view metadata independently of workspace scope. The local
// layer checks ownership/sharing before exposing it, then separately filters
// the selected workspaces through the caller's transaction scope.
func (t *Tx) GetBoardView(id string) (*domain.BoardView, error) {
	view, err := scanBoardView(t.q.QueryRowContext(t.ctx, `
		SELECT id, name, owner, shared, all_workspaces, all_epics, include_no_epic,
		       priority_max, card_limit, closed_days, epic_closed_days, created_at, updated_at
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
		SELECT id, name, owner, shared, all_workspaces, all_epics, include_no_epic,
		       priority_max, card_limit, closed_days, epic_closed_days, created_at, updated_at
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
	if err := load(`SELECT epic FROM board_view_epics WHERE view = ? ORDER BY epic`, &view.Epics); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view epics")
	}
	if err := load(`SELECT label FROM board_view_labels WHERE view = ? ORDER BY label`, &view.Labels); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view labels")
	}
	if err := load(`SELECT assignee FROM board_view_assignees WHERE view = ? ORDER BY assignee`, &view.Assignees); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view assignees")
	}
	rows, err := t.q.QueryContext(t.ctx, `SELECT status FROM board_view_columns WHERE view = ? ORDER BY position`, view.ID)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view columns")
	}
	for rows.Next() {
		var status domain.Status
		if err := rows.Scan(&status); err != nil {
			_ = rows.Close()
			return awberr.Wrap(awberr.Runtime, err, "read board view columns")
		}
		view.Columns = append(view.Columns, status)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return awberr.Wrap(awberr.Runtime, err, "read board view columns")
	}
	if err := rows.Close(); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read board view columns")
	}
	view.Normalize()
	return nil
}

func (t *Tx) InsertBoardView(view *domain.BoardView) error {
	now := Now()
	view.CreatedAt, view.UpdatedAt = now, now
	_, err := t.q.ExecContext(t.ctx, `INSERT INTO board_views
		(id, name, owner, shared, all_workspaces, all_epics, include_no_epic,
		 priority_max, card_limit, closed_days, epic_closed_days, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, view.ID, view.Name, view.Owner, view.Shared,
		view.AllWorkspaces, view.AllEpics, view.IncludeNoEpic, view.PriorityMax, view.CardLimit, view.ClosedDays, view.EpicClosedDays, now, now)
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
		existing.AllWorkspaces == next.AllWorkspaces && existing.AllEpics == next.AllEpics &&
		existing.IncludeNoEpic == next.IncludeNoEpic && existing.PriorityMax == next.PriorityMax &&
		existing.CardLimit == next.CardLimit &&
		existing.ClosedDays == next.ClosedDays && existing.EpicClosedDays == next.EpicClosedDays &&
		slices.Equal(existing.Columns, next.Columns) &&
		slices.Equal(existing.Workspaces, next.Workspaces) && slices.Equal(existing.Labels, next.Labels) &&
		slices.Equal(existing.Assignees, next.Assignees) && slices.Equal(existing.Epics, next.Epics) {
		return nil
	}
	next.UpdatedAt = bumpedTimestamp(existing.UpdatedAt, Now())
	_, err := t.q.ExecContext(t.ctx, `UPDATE board_views SET name = ?, shared = ?,
		all_workspaces = ?, all_epics = ?, include_no_epic = ?, priority_max = ?, card_limit = ?, closed_days = ?, epic_closed_days = ?,
		updated_at = ? WHERE id = ?`, next.Name, next.Shared, next.AllWorkspaces,
		next.AllEpics, next.IncludeNoEpic, next.PriorityMax, next.CardLimit, next.ClosedDays, next.EpicClosedDays, next.UpdatedAt, existing.ID)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update board view %s", existing.ID)
	}
	return t.replaceBoardViewFilters(next)
}

func (t *Tx) replaceBoardViewFilters(view *domain.BoardView) error {
	for _, table := range []string{"board_view_workspaces", "board_view_epics", "board_view_labels", "board_view_assignees", "board_view_columns"} {
		if _, err := t.q.ExecContext(t.ctx, `DELETE FROM `+table+` WHERE view = ?`, view.ID); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "replace board view filters")
		}
	}
	for _, value := range view.Epics {
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO board_view_epics (view, epic) VALUES (?, ?)`, view.ID, value); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "store board view epic")
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
	for position, value := range view.Columns {
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO board_view_columns (view, status, position) VALUES (?, ?, ?)`, view.ID, value, position); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "store board view column")
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
		WHERE selected.workspace = ?
		UNION
		SELECT selected.view, views.updated_at
		FROM board_view_epics AS selected
		JOIN board_views AS views ON views.id = selected.view
		JOIN issues ON issues.id = selected.epic
		WHERE issues.workspace = ?`, workspace, workspace)
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

// bumpBoardViewsSelectingEpic moves each affected ETag before the issue's
// foreign-key cascade removes its pinned lane.
func (t *Tx) bumpBoardViewsSelectingEpic(epic string) error {
	rows, err := t.q.QueryContext(t.ctx, `SELECT selected.view, views.updated_at
		FROM board_view_epics AS selected
		JOIN board_views AS views ON views.id = selected.view
		WHERE selected.epic = ?`, epic)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "find board views selecting epic %s", epic)
	}
	type selectedView struct{ id, updatedAt string }
	views := []selectedView{}
	for rows.Next() {
		var view selectedView
		if err := rows.Scan(&view.id, &view.updatedAt); err != nil {
			_ = rows.Close()
			return awberr.Wrap(awberr.Runtime, err, "find board views selecting epic %s", epic)
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return awberr.Wrap(awberr.Runtime, err, "find board views selecting epic %s", epic)
	}
	if err := rows.Close(); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "find board views selecting epic %s", epic)
	}
	now := Now()
	for _, view := range views {
		if _, err := t.q.ExecContext(t.ctx, `UPDATE board_views SET updated_at = ? WHERE id = ?`,
			bumpedTimestamp(view.updatedAt, now), view.id); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "update board view %s before deleting epic", view.id)
		}
	}
	return nil
}

// removeBoardViewEpicSelections advances each affected version before an issue
// stops being an epic, then removes the now-invalid pinned lane.
func (t *Tx) removeBoardViewEpicSelections(epic string) error {
	if err := t.bumpBoardViewsSelectingEpic(epic); err != nil {
		return err
	}
	if _, err := t.q.ExecContext(t.ctx, `DELETE FROM board_view_epics WHERE epic = ?`, epic); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "remove board view selections of epic %s", epic)
	}
	return nil
}
