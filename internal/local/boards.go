package local

import (
	"context"
	"slices"
	"time"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

const (
	defaultBoardLaneLimit  = 10
	maximumBoardLaneLimit  = 50
	defaultBoardCardLimit  = 50
	maximumBoardCardLimit  = 50
	defaultBoardClosedDays = 30
	maximumBoardClosedDays = 3650
)

func validateBoardView(req backend.BoardViewCreate) (*domain.BoardView, error) {
	// The backend interface predates epic scope. Preserve its former dynamic
	// board default when an in-process caller leaves every new field unset.
	if !req.AllEpics && req.Epics == nil && !req.IncludeNoEpic {
		req.AllEpics, req.IncludeNoEpic = true, true
	}
	name, err := domain.ValidateBoardViewName(req.Name)
	if err != nil {
		return nil, err
	}
	priority, err := domain.ParsePriority(req.PriorityMax)
	if err != nil {
		return nil, err
	}
	if req.ClosedDays < 0 || req.ClosedDays > maximumBoardClosedDays {
		return nil, awberr.Usagef("board closed days must be between 0 and %d", maximumBoardClosedDays)
	}
	view := &domain.BoardView{Name: name, Shared: req.Shared, AllWorkspaces: req.AllWorkspaces,
		AllEpics: req.AllEpics, IncludeNoEpic: req.IncludeNoEpic, PriorityMax: priority,
		ClosedDays: req.ClosedDays}
	seen := map[string]bool{}
	for _, value := range req.Workspaces {
		valid, err := domain.ValidateWorkspaceKey(value)
		if err != nil {
			return nil, err
		}
		if !seen["p:"+valid] {
			view.Workspaces = append(view.Workspaces, valid)
			seen["p:"+valid] = true
		}
	}
	for _, value := range req.Labels {
		valid, err := domain.ValidateLabel(value)
		if err != nil {
			return nil, err
		}
		if !seen["l:"+valid] {
			view.Labels = append(view.Labels, valid)
			seen["l:"+valid] = true
		}
	}
	for _, value := range req.Epics {
		valid, err := domain.ValidateIssueID(value)
		if err != nil {
			return nil, err
		}
		if !seen["e:"+valid] {
			view.Epics = append(view.Epics, valid)
			seen["e:"+valid] = true
		}
	}
	for _, value := range req.Assignees {
		valid, err := domain.ValidateAssignee(value)
		if err != nil {
			return nil, err
		}
		if !seen["a:"+valid] {
			view.Assignees = append(view.Assignees, valid)
			seen["a:"+valid] = true
		}
	}
	view.Normalize()
	return view, nil
}

func boardCreateFrom(view *domain.BoardView) backend.BoardViewCreate {
	return backend.BoardViewCreate{Name: view.Name, Shared: view.Shared, AllWorkspaces: view.AllWorkspaces,
		Workspaces: view.Workspaces, AllEpics: view.AllEpics, Epics: view.Epics,
		IncludeNoEpic: view.IncludeNoEpic, Labels: view.Labels, Assignees: view.Assignees,
		PriorityMax: view.PriorityMax, ClosedDays: view.ClosedDays}
}

func validateBoardViewEpics(tx *storage.Tx, view *domain.BoardView, requested []string) error {
	for _, id := range requested {
		epic, err := tx.GetIssue(id)
		if err != nil {
			return err
		}
		if epic.Type != domain.TypeEpic {
			return awberr.Usagef("board view epic must name an epic: %s", id)
		}
		active, err := tx.ActiveWorkspaceExists(epic.Workspace)
		if err != nil {
			return err
		}
		if !active || (!view.AllWorkspaces && !slices.Contains(view.Workspaces, epic.Workspace)) {
			return awberr.NotFoundf("no such board epic: %s", id)
		}
	}
	return nil
}

func (b *Backend) CreateBoardView(ctx context.Context, req backend.BoardViewCreate) (*domain.BoardView, error) {
	view, err := validateBoardView(req)
	if err != nil {
		return nil, err
	}
	view.ID, err = domain.NewBoardViewID()
	if err != nil {
		return nil, err
	}
	view.Owner = b.identity
	err = b.db.Write(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		view.Owner = caller.Name
		if view.Owner == "" {
			view.Owner = b.identity
		}
		if view.Owner, err = domain.ValidateAssignee(view.Owner); err != nil {
			return err
		}
		for _, workspace := range view.Workspaces {
			exists, err := tx.ActiveWorkspaceExists(workspace)
			if err != nil {
				return err
			}
			if !exists {
				return awberr.NotFoundf("no such workspace: %s", workspace)
			}
		}
		if err := validateBoardViewEpics(tx, view, view.Epics); err != nil {
			return err
		}
		if err := tx.InsertBoardView(view); err != nil {
			return err
		}
		created, err := tx.GetBoardView(view.ID)
		if err == nil {
			view = created
		}
		return err
	})
	return view, err
}

func (b *Backend) ListBoardViews(ctx context.Context) ([]domain.BoardView, error) {
	views := []domain.BoardView{}
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		owner := caller.Name
		if owner == "" {
			owner = b.identity
		}
		views, err = tx.ListBoardViews(owner)
		if err != nil {
			return err
		}
		for i := range views {
			views[i].Workspaces, _, err = visibleViewWorkspaces(tx, views[i].Workspaces)
			if err != nil {
				return err
			}
			views[i].Epics, err = visibleViewEpics(tx, views[i].Epics)
			if err != nil {
				return err
			}
			views[i].Normalize()
		}
		return nil
	})
	return views, err
}

// readBoardView retains ignored selections for the owner who is editing their
// configuration, while a shared viewer receives only normally visible keys.
func (b *Backend) readBoardView(ctx context.Context, id string) (*domain.BoardView, error) {
	if _, err := domain.ValidateBoardViewID(id); err != nil {
		return nil, err
	}
	var result *domain.BoardView
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		view, err := tx.GetBoardView(id)
		if err != nil {
			return err
		}
		owner := caller.MayManageBoardView(view.Owner)
		if !owner && !view.Shared {
			return awberr.NotFoundf("no such board view: %s", id)
		}
		if !owner {
			tx.Restrict(tx.Scope().HideIgnoredBy(caller.Name))
		}
		view.Workspaces, _, err = visibleViewWorkspaces(tx, view.Workspaces)
		if err != nil {
			return err
		}
		view.Epics, err = visibleViewEpics(tx, view.Epics)
		if err != nil {
			return err
		}
		view.Normalize()
		result = view
		return nil
	})
	return result, err
}

func (b *Backend) GetBoardView(ctx context.Context, id string) (*domain.BoardView, error) {
	return b.readBoardView(ctx, id)
}

func visibleViewWorkspaces(tx *storage.Tx, workspaces []string) ([]string, bool, error) {
	visible := []string{}
	for _, workspace := range workspaces {
		exists, err := tx.ActiveWorkspaceExists(workspace)
		if err != nil {
			return nil, false, err
		}
		if exists {
			visible = append(visible, workspace)
		}
	}
	return visible, len(visible) != len(workspaces), nil
}

func visibleViewEpics(tx *storage.Tx, epics []string) ([]string, error) {
	visible := []string{}
	for _, id := range epics {
		epic, err := tx.GetIssue(id)
		if err != nil {
			if awberr.KindOf(err) == awberr.NotFound {
				continue
			}
			return nil, err
		}
		active, err := tx.ActiveWorkspaceExists(epic.Workspace)
		if err != nil {
			return nil, err
		}
		if active && epic.Type == domain.TypeEpic {
			visible = append(visible, id)
		}
	}
	return visible, nil
}

func (b *Backend) UpdateBoardView(ctx context.Context, id string, req backend.BoardViewPatch, ifMatch string) (*domain.BoardView, error) {
	if _, err := domain.ValidateBoardViewID(id); err != nil {
		return nil, err
	}
	var updated *domain.BoardView
	err := b.db.Write(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		existing, err := tx.GetBoardView(id)
		if err != nil {
			return err
		}
		if !caller.MayManageBoardView(existing.Owner) {
			if !existing.Shared {
				return awberr.NotFoundf("no such board view: %s", id)
			}
			return awberr.Forbiddenf("only @%s may change board view %s", existing.Owner, id)
		}
		if err := checkIfMatch(ifMatch, existing.UpdatedAt, "the board view"); err != nil {
			return err
		}
		next := *existing
		if req.Name != nil {
			next.Name = *req.Name
		}
		if req.Shared != nil {
			next.Shared = *req.Shared
		}
		if req.AllWorkspaces != nil {
			next.AllWorkspaces = *req.AllWorkspaces
		}
		if req.Workspaces != nil {
			next.Workspaces = slices.Clone(*req.Workspaces)
			// A scoped response cannot disclose selections the owner can no
			// longer access. Preserve those stored keys when replacing the
			// visible set, otherwise an editor could silently delete them.
			for _, workspace := range existing.Workspaces {
				exists, err := tx.ActiveWorkspaceExists(workspace)
				if err != nil {
					return err
				}
				if !exists && !slices.Contains(next.Workspaces, workspace) {
					next.Workspaces = append(next.Workspaces, workspace)
				}
			}
		}
		if req.AllEpics != nil {
			next.AllEpics = *req.AllEpics
		}
		if req.Epics != nil {
			next.Epics = slices.Clone(*req.Epics)
			for _, epic := range existing.Epics {
				if _, err := tx.GetIssue(epic); awberr.KindOf(err) == awberr.NotFound && !slices.Contains(next.Epics, epic) {
					next.Epics = append(next.Epics, epic)
				} else if err != nil {
					return err
				}
			}
		}
		if req.IncludeNoEpic != nil {
			next.IncludeNoEpic = *req.IncludeNoEpic
		}
		if req.Labels != nil {
			next.Labels = slices.Clone(*req.Labels)
		}
		if req.Assignees != nil {
			next.Assignees = slices.Clone(*req.Assignees)
		}
		if req.PriorityMax != nil {
			next.PriorityMax = *req.PriorityMax
		}
		if req.ClosedDays != nil {
			next.ClosedDays = *req.ClosedDays
		}
		valid, err := validateBoardView(boardCreateFrom(&next))
		if err != nil {
			return err
		}
		valid.ID, valid.Owner, valid.CreatedAt, valid.UpdatedAt = existing.ID, existing.Owner, existing.CreatedAt, existing.UpdatedAt
		if req.Workspaces != nil {
			for _, workspace := range *req.Workspaces {
				exists, err := tx.ActiveWorkspaceExists(workspace)
				if err != nil {
					return err
				}
				if !exists {
					return awberr.NotFoundf("no such workspace: %s", workspace)
				}
			}
		}
		requestedEpics := valid.Epics
		if req.Epics != nil {
			requestedEpics = *req.Epics
		}
		if err := validateBoardViewEpics(tx, valid, requestedEpics); err != nil {
			return err
		}
		if err := tx.UpdateBoardView(existing, valid); err != nil {
			return err
		}
		updated, err = tx.GetBoardView(id)
		if err != nil {
			return err
		}
		updated.Workspaces, _, err = visibleViewWorkspaces(tx, updated.Workspaces)
		if err == nil {
			updated.Epics, err = visibleViewEpics(tx, updated.Epics)
		}
		if err == nil {
			updated.Normalize()
		}
		return err
	})
	return updated, err
}

func (b *Backend) DeleteBoardView(ctx context.Context, id, ifMatch string) (*domain.BoardView, error) {
	if _, err := domain.ValidateBoardViewID(id); err != nil {
		return nil, err
	}
	var deleted *domain.BoardView
	err := b.db.Write(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		deleted, err = tx.GetBoardView(id)
		if err != nil {
			return err
		}
		if !caller.MayManageBoardView(deleted.Owner) {
			if !deleted.Shared {
				return awberr.NotFoundf("no such board view: %s", id)
			}
			return awberr.Forbiddenf("only @%s may delete board view %s", deleted.Owner, id)
		}
		if err := checkIfMatch(ifMatch, deleted.UpdatedAt, "the board view"); err != nil {
			return err
		}
		deleted.Workspaces, _, err = visibleViewWorkspaces(tx, deleted.Workspaces)
		if err != nil {
			return err
		}
		deleted.Epics, err = visibleViewEpics(tx, deleted.Epics)
		if err != nil {
			return err
		}
		deleted.Normalize()
		return tx.DeleteBoardView(id)
	})
	return deleted, err
}

func (b *Backend) GetBoard(ctx context.Context, ref string, query backend.BoardQuery) (*domain.Board, error) {
	if ref != "default" {
		if _, err := domain.ValidateBoardViewID(ref); err != nil {
			return nil, err
		}
	}
	for _, workspace := range query.Workspaces {
		if _, err := domain.ValidateWorkspaceKey(workspace); err != nil {
			return nil, err
		}
	}
	if query.Status != "" {
		if _, err := domain.ParseStatus(string(query.Status)); err != nil {
			return nil, err
		}
	}
	var err error
	if query.LaneLimit, err = boundedBoardLimit(query.LaneLimit, defaultBoardLaneLimit, maximumBoardLaneLimit, "lane"); err != nil {
		return nil, err
	}
	if query.CardLimit, err = boundedBoardLimit(query.CardLimit, defaultBoardCardLimit, maximumBoardCardLimit, "card"); err != nil {
		return nil, err
	}
	closedDays := defaultBoardClosedDays
	if query.ClosedDays != nil {
		closedDays = *query.ClosedDays
	}
	if closedDays < 0 || closedDays > maximumBoardClosedDays {
		return nil, awberr.Usagef("board closed days must be between 0 and %d", maximumBoardClosedDays)
	}
	for name, offset := range map[string]*int{"lane": query.LaneOffset, "card": query.CardOffset} {
		if offset != nil && *offset < 0 {
			return nil, awberr.Usagef("board %s offset must not be negative", name)
		}
	}
	result := &domain.Board{Lanes: []domain.BoardLane{}}
	err = b.db.Read(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, false)
		if err != nil {
			return err
		}
		var view *domain.BoardView
		var selected []string
		if ref != "default" {
			view, err = tx.GetBoardView(ref)
			if err != nil {
				return err
			}
			if !caller.MayManageBoardView(view.Owner) && !view.Shared {
				return awberr.NotFoundf("no such board view: %s", ref)
			}
			if !view.AllWorkspaces {
				selected = slices.Clone(view.Workspaces)
			}
			visible, omitted, err := visibleViewWorkspaces(tx, view.Workspaces)
			if err != nil {
				return err
			}
			result.WorkspacesOmitted = omitted
			shown := *view
			shown.Workspaces = visible
			shown.Epics, err = visibleViewEpics(tx, view.Epics)
			if err != nil {
				return err
			}
			shown.Normalize()
			result.View = &shown
			closedDays = view.ClosedDays
		}
		closedAfter := boardClosedAfter(closedDays)
		laneSelection := selected
		if len(query.Workspaces) > 0 {
			requested := []string{}
			for _, workspace := range query.Workspaces {
				if laneSelection == nil || slices.Contains(laneSelection, workspace) {
					requested = append(requested, workspace)
				}
			}
			laneSelection = requested
		}
		laneLimit, laneOffset := *query.LaneLimit, 0
		if query.LaneOffset != nil {
			laneOffset = *query.LaneOffset
		}
		laneEpics := []*domain.Issue{}
		if query.Epic != nil {
			if view != nil && ((*query.Epic == "none" && !view.IncludeNoEpic) ||
				(*query.Epic != "none" && !view.AllEpics && !slices.Contains(view.Epics, *query.Epic))) {
				return awberr.NotFoundf("no such board epic: %s", *query.Epic)
			}
			result.LaneTotal = 1
			var epic *domain.Issue
			if *query.Epic != "none" {
				epic, err = load(tx, *query.Epic)
				if err != nil {
					return err
				}
				active, err := tx.ActiveWorkspaceExists(epic.Workspace)
				if err != nil {
					return err
				}
				if !active || epic.Type != domain.TypeEpic || !boardIssueVisible(epic, closedAfter) ||
					(laneSelection != nil && !slices.Contains(laneSelection, epic.Workspace)) {
					return awberr.NotFoundf("no such board epic: %s", *query.Epic)
				}
			}
			if laneOffset == 0 && laneLimit > 0 {
				laneEpics = append(laneEpics, epic)
			}
		} else if view != nil && !view.AllEpics {
			selectedEpics := []domain.Issue{}
			for _, id := range view.Epics {
				epic, loadErr := tx.GetIssue(id)
				if loadErr != nil {
					if awberr.KindOf(loadErr) == awberr.NotFound {
						continue
					}
					return loadErr
				}
				if epic.Type == domain.TypeEpic && boardIssueVisible(epic, closedAfter) &&
					(laneSelection == nil || slices.Contains(laneSelection, epic.Workspace)) {
					selectedEpics = append(selectedEpics, *epic)
				}
			}
			result.LaneTotal = len(selectedEpics)
			if view.IncludeNoEpic {
				result.LaneTotal++
			}
			all := []*domain.Issue{}
			if view.IncludeNoEpic {
				all = append(all, nil)
			}
			for i := range selectedEpics {
				all = append(all, &selectedEpics[i])
			}
			end := min(laneOffset+laneLimit, len(all))
			if laneOffset < len(all) {
				laneEpics = append(laneEpics, all[laneOffset:end]...)
			}
		} else {
			epicLimit, epicOffset := laneLimit, 0
			hasNoEpic := view == nil || view.IncludeNoEpic
			epicOffset = laneOffset
			if hasNoEpic && laneOffset > 0 {
				epicOffset = laneOffset - 1
			}
			includeNoEpic := hasNoEpic && laneOffset == 0 && laneLimit > 0
			if includeNoEpic {
				epicLimit--
			}
			epics, epicTotal, err := tx.ListBoardEpics(laneSelection, closedAfter, &epicLimit, &epicOffset)
			if err != nil {
				return err
			}
			result.LaneTotal = epicTotal
			if hasNoEpic {
				result.LaneTotal++
			}
			if includeNoEpic {
				laneEpics = append(laneEpics, nil)
			}
			for i := range epics {
				laneEpics = append(laneEpics, &epics[i])
			}
		}
		statuses := domain.Statuses
		if query.Status != "" {
			statuses = []domain.Status{query.Status}
		}
		cardTypes := []domain.Type{domain.TypeFeature, domain.TypeBug, domain.TypeTask, domain.TypeChore}
		for _, epic := range laneEpics {
			lane := domain.BoardLane{Epic: epic, Columns: []domain.BoardColumn{}}
			epicID := ""
			workspaces := laneSelection
			if epic != nil {
				epicID = epic.ID
				workspaces = []string{epic.Workspace}
			}
			for _, status := range statuses {
				filter := &domain.Filter{Workspaces: workspaces, Types: cardTypes, Epic: &epicID,
					Statuses: []domain.Status{status}, Limit: query.CardLimit,
					Offset: query.CardOffset, Sort: domain.DefaultSort, BoardOnly: true}
				if status == domain.StatusClosed {
					filter.ClosedAfter = closedAfter
				}
				if view != nil {
					filter.Labels = view.Labels
					filter.Assignees = view.Assignees
					max := view.PriorityMax
					filter.PriorityMax = &max
				}
				issues, total, err := []domain.Issue{}, 0, error(nil)
				if workspaces == nil || len(workspaces) > 0 {
					issues, total, err = tx.ListIssues(filter)
				}
				if err != nil {
					return err
				}
				lane.Columns = append(lane.Columns, domain.BoardColumn{Status: status, Issues: issues, Total: total})
			}
			result.Lanes = append(result.Lanes, lane)
		}
		return nil
	})
	return result, err
}

func boardClosedAfter(days int) string {
	if days == 0 {
		return "9999-12-31T23:59:59.999Z"
	}
	return domain.FormatTime(time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour))
}

func boardIssueVisible(issue *domain.Issue, closedAfter string) bool {
	return !issue.BoardHidden && (issue.Status != domain.StatusClosed || issue.ClosedAt >= closedAfter)
}

func boundedBoardLimit(value *int, fallback, maximum int, name string) (*int, error) {
	if value == nil {
		value = &fallback
	}
	if *value < 0 || *value > maximum {
		return nil, awberr.Usagef("board %s limit must be between 0 and %d", name, maximum)
	}
	return value, nil
}
