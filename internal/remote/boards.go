package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

type boardViewCreateBody struct {
	Name           string   `json:"name"`
	Shared         bool     `json:"shared"`
	AllWorkspaces  bool     `json:"all_workspaces"`
	Workspaces     []string `json:"workspaces"`
	AllEpics       bool     `json:"all_epics"`
	Epics          []string `json:"epics"`
	IncludeNoEpic  bool     `json:"include_no_epic"`
	Labels         []string `json:"labels"`
	Assignees      []string `json:"assignees"`
	PriorityMax    int      `json:"priority_max"`
	ClosedDays     int      `json:"closed_days"`
	EpicClosedDays int      `json:"epic_closed_days"`
}
type boardViewPatchBody struct {
	Name           *string   `json:"name,omitempty"`
	Shared         *bool     `json:"shared,omitempty"`
	AllWorkspaces  *bool     `json:"all_workspaces,omitempty"`
	Workspaces     *[]string `json:"workspaces,omitempty"`
	AllEpics       *bool     `json:"all_epics,omitempty"`
	Epics          *[]string `json:"epics,omitempty"`
	IncludeNoEpic  *bool     `json:"include_no_epic,omitempty"`
	Labels         *[]string `json:"labels,omitempty"`
	Assignees      *[]string `json:"assignees,omitempty"`
	PriorityMax    *int      `json:"priority_max,omitempty"`
	ClosedDays     *int      `json:"closed_days,omitempty"`
	EpicClosedDays *int      `json:"epic_closed_days,omitempty"`
}

func (b *Backend) ListBoardViews(ctx context.Context) ([]domain.BoardView, error) {
	views := []domain.BoardView{}
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/board-views", nil), nil, "", &views)
	return views, err
}
func (b *Backend) CreateBoardView(ctx context.Context, req backend.BoardViewCreate) (*domain.BoardView, error) {
	if !req.AllEpics && req.Epics == nil && !req.IncludeNoEpic {
		req.AllEpics, req.IncludeNoEpic = true, true
	}
	body := boardViewCreateBody(req)
	var view domain.BoardView
	_, err := b.call(ctx, http.MethodPost, b.endpoint("/api/board-views", nil), body, "", &view)
	return &view, err
}
func (b *Backend) GetBoardView(ctx context.Context, id string) (*domain.BoardView, error) {
	var view domain.BoardView
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/board-views/"+url.PathEscape(id), nil), nil, "", &view)
	return &view, err
}
func (b *Backend) UpdateBoardView(ctx context.Context, id string, req backend.BoardViewPatch, ifMatch string) (*domain.BoardView, error) {
	body := boardViewPatchBody{Name: req.Name, Shared: req.Shared, AllWorkspaces: req.AllWorkspaces,
		Workspaces: req.Workspaces, AllEpics: req.AllEpics, Epics: req.Epics, IncludeNoEpic: req.IncludeNoEpic,
		Labels: req.Labels, Assignees: req.Assignees, PriorityMax: req.PriorityMax, ClosedDays: req.ClosedDays,
		EpicClosedDays: req.EpicClosedDays}
	var view domain.BoardView
	_, err := b.call(ctx, http.MethodPatch, b.endpoint("/api/board-views/"+url.PathEscape(id), nil), body, ifMatch, &view)
	return &view, err
}
func (b *Backend) DeleteBoardView(ctx context.Context, id, ifMatch string) (*domain.BoardView, error) {
	var view domain.BoardView
	_, err := b.call(ctx, http.MethodDelete, b.endpoint("/api/board-views/"+url.PathEscape(id), nil), nil, ifMatch, &view)
	return &view, err
}
func (b *Backend) GetBoard(ctx context.Context, ref string, query backend.BoardQuery) (*domain.Board, error) {
	values := url.Values{}
	set := func(key string, value *int) {
		if value != nil {
			values.Set(key, strconv.Itoa(*value))
		}
	}
	set("lane-limit", query.LaneLimit)
	set("lane-offset", query.LaneOffset)
	set("card-limit", query.CardLimit)
	set("card-offset", query.CardOffset)
	set("closed-days", query.ClosedDays)
	set("epic-closed-days", query.EpicClosedDays)
	for _, workspace := range query.Workspaces {
		values.Add("workspace", workspace)
	}
	if query.AllWorkspaces != nil {
		values.Set("all-workspaces", strconv.FormatBool(*query.AllWorkspaces))
	}
	for _, epic := range query.HiddenEpics {
		values.Add("hidden-epic", epic)
	}
	if query.AllEpics != nil {
		values.Set("all-epics", strconv.FormatBool(*query.AllEpics))
	}
	for _, epic := range query.Epics {
		values.Add("selected-epic", epic)
	}
	if query.IncludeNoEpic != nil {
		values.Set("include-no-epic", strconv.FormatBool(*query.IncludeNoEpic))
	}
	for _, label := range query.Labels {
		values.Add("label", label)
	}
	for _, assignee := range query.Assignees {
		values.Add("assignee", assignee)
	}
	set("priority-max", query.PriorityMax)
	if query.Status != "" {
		values.Set("status", string(query.Status))
	}
	if query.Epic != nil {
		values.Set("epic", *query.Epic)
	}
	var board domain.Board
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/boards/"+url.PathEscape(ref), values), nil, "", &board)
	return &board, err
}
