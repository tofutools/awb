package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

func toBoardView(view *domain.BoardView) api.BoardView {
	workspaces := make([]api.WorkspaceKey, len(view.Workspaces))
	for i, v := range view.Workspaces {
		workspaces[i] = api.WorkspaceKey(v)
	}
	epics := make([]api.IssueID, len(view.Epics))
	for i, v := range view.Epics {
		epics[i] = api.IssueID(v)
	}
	return api.BoardView{ID: api.BoardViewID(view.ID), Name: view.Name, Owner: api.Assignee(view.Owner),
		Shared: view.Shared, AllWorkspaces: view.AllWorkspaces, Workspaces: workspaces,
		AllEpics: view.AllEpics, Epics: epics, IncludeNoEpic: view.IncludeNoEpic,
		Labels: toLabels(view.Labels), Assignees: toAssignees(view.Assignees),
		PriorityMax: api.Priority(view.PriorityMax), ClosedDays: view.ClosedDays,
		CreatedAt: api.Timestamp(view.CreatedAt), UpdatedAt: api.Timestamp(view.UpdatedAt)}
}

func toBoardViews(views []domain.BoardView) []api.BoardView {
	result := make([]api.BoardView, len(views))
	for i := range views {
		result[i] = toBoardView(&views[i])
	}
	return result
}

func boardViewResponse(view *domain.BoardView) *api.BoardViewHeaders {
	return &api.BoardViewHeaders{Etag: api.NewOptString(backend.ETag(view.UpdatedAt)), Response: toBoardView(view)}
}

func stringsFromWorkspaces(values []api.WorkspaceKey) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result
}
func stringsFromIssueIDs(values []api.IssueID) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result
}
func stringsFromLabels(values []api.Label) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result
}
func stringsFromAssignees(values []api.Assignee) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = string(v)
	}
	return result
}
func optWorkspaces(values []api.WorkspaceKey) *[]string {
	if values == nil {
		return nil
	}
	result := stringsFromWorkspaces(values)
	return &result
}
func optIssueIDs(values []api.IssueID) *[]string {
	if values == nil {
		return nil
	}
	result := stringsFromIssueIDs(values)
	return &result
}
func optLabels(values []api.Label) *[]string {
	if values == nil {
		return nil
	}
	result := stringsFromLabels(values)
	return &result
}
func optAssignees(values []api.Assignee) *[]string {
	if values == nil {
		return nil
	}
	result := stringsFromAssignees(values)
	return &result
}

func (h *Handler) ListBoardViews(ctx context.Context) ([]api.BoardView, error) {
	views, err := h.backendFor(ctx).ListBoardViews(ctx)
	if err != nil {
		return nil, err
	}
	return toBoardViews(views), nil
}

func (h *Handler) CreateBoardView(ctx context.Context, req *api.BoardViewCreate) (*api.BoardViewCreatedHeaders, error) {
	view, err := h.backendFor(ctx).CreateBoardView(ctx, backend.BoardViewCreate{Name: req.Name,
		Shared: req.Shared.Or(false), AllWorkspaces: req.AllWorkspaces.Or(true), Workspaces: stringsFromWorkspaces(req.Workspaces),
		AllEpics: req.AllEpics.Or(true), Epics: stringsFromIssueIDs(req.Epics), IncludeNoEpic: req.IncludeNoEpic.Or(true),
		Labels: stringsFromLabels(req.Labels), Assignees: stringsFromAssignees(req.Assignees),
		PriorityMax: int(req.PriorityMax.Or(4)), ClosedDays: req.ClosedDays.Or(30)})
	if err != nil {
		return nil, err
	}
	return &api.BoardViewCreatedHeaders{Etag: api.NewOptString(backend.ETag(view.UpdatedAt)),
		Location: api.NewOptString("/api/board-views/" + view.ID), Response: toBoardView(view)}, nil
}

func (h *Handler) GetBoardView(ctx context.Context, params api.GetBoardViewParams) (*api.BoardViewHeaders, error) {
	view, err := h.backendFor(ctx).GetBoardView(ctx, string(params.ID))
	if err != nil {
		return nil, err
	}
	return boardViewResponse(view), nil
}

func (h *Handler) UpdateBoardView(ctx context.Context, req *api.BoardViewPatch, params api.UpdateBoardViewParams) (*api.BoardViewHeaders, error) {
	view, err := h.backendFor(ctx).UpdateBoardView(ctx, string(params.ID), backend.BoardViewPatch{
		Name: optString(req.Name), Shared: optBool(req.Shared), AllWorkspaces: optBool(req.AllWorkspaces),
		Workspaces: optWorkspaces(req.Workspaces), Labels: optLabels(req.Labels), Assignees: optAssignees(req.Assignees),
		AllEpics: optBool(req.AllEpics), Epics: optIssueIDs(req.Epics), IncludeNoEpic: optBool(req.IncludeNoEpic),
		PriorityMax: optPriority(req.PriorityMax), ClosedDays: optInt(req.ClosedDays)}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return boardViewResponse(view), nil
}

func (h *Handler) DeleteBoardView(ctx context.Context, params api.DeleteBoardViewParams) (*api.BoardView, error) {
	view, err := h.backendFor(ctx).DeleteBoardView(ctx, string(params.ID), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	result := toBoardView(view)
	return &result, nil
}

func (h *Handler) GetBoard(ctx context.Context, params api.GetBoardParams) (*api.Board, error) {
	query := backend.BoardQuery{LaneLimit: optInt(params.LaneLimit), LaneOffset: optInt(params.LaneOffset),
		CardLimit: optInt(params.CardLimit), CardOffset: optInt(params.CardOffset)}
	for _, value := range params.Workspace {
		query.Workspaces = append(query.Workspaces, string(value))
	}
	if value, ok := params.Status.Get(); ok {
		query.Status = domain.Status(value)
	}
	if value, ok := params.Epic.Get(); ok {
		query.Epic = &value
	}
	query.ClosedDays = optInt(params.ClosedDays)
	board, err := h.backendFor(ctx).GetBoard(ctx, params.Ref, query)
	if err != nil {
		return nil, err
	}
	result := &api.Board{Lanes: make([]api.BoardLane, len(board.Lanes)), LaneTotal: board.LaneTotal, WorkspacesOmitted: board.WorkspacesOmitted}
	if board.View != nil {
		result.View = api.NewOptBoardView(toBoardView(board.View))
	}
	for i, lane := range board.Lanes {
		columns := make([]api.BoardColumn, len(lane.Columns))
		for j, column := range lane.Columns {
			columns[j] = api.BoardColumn{Status: api.Status(column.Status), Issues: toIssues(column.Issues), Total: column.Total}
		}
		result.Lanes[i] = api.BoardLane{Columns: columns}
		if lane.Epic != nil {
			result.Lanes[i].Epic = api.NewOptIssue(toIssue(lane.Epic))
		}
	}
	return result, nil
}
