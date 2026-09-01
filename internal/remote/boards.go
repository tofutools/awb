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
	Name        string   `json:"name"`
	Shared      bool     `json:"shared"`
	AllProjects bool     `json:"all_projects"`
	Projects    []string `json:"projects"`
	Labels      []string `json:"labels"`
	Assignees   []string `json:"assignees"`
	PriorityMax int      `json:"priority_max"`
}
type boardViewPatchBody struct {
	Name        *string   `json:"name,omitempty"`
	Shared      *bool     `json:"shared,omitempty"`
	AllProjects *bool     `json:"all_projects,omitempty"`
	Projects    *[]string `json:"projects,omitempty"`
	Labels      *[]string `json:"labels,omitempty"`
	Assignees   *[]string `json:"assignees,omitempty"`
	PriorityMax *int      `json:"priority_max,omitempty"`
}

func (b *Backend) ListBoardViews(ctx context.Context) ([]domain.BoardView, error) {
	views := []domain.BoardView{}
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/board-views", nil), nil, "", &views)
	return views, err
}
func (b *Backend) CreateBoardView(ctx context.Context, req backend.BoardViewCreate) (*domain.BoardView, error) {
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
	body := boardViewPatchBody{Name: req.Name, Shared: req.Shared, AllProjects: req.AllProjects, Projects: req.Projects, Labels: req.Labels, Assignees: req.Assignees, PriorityMax: req.PriorityMax}
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
	for _, project := range query.Projects {
		values.Add("project", project)
	}
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
