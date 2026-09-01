package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// projectResponse is issueResponse for a project, whose tag is built from its
// own updated_at: active_issues moving because somebody created or closed an
// issue does not invalidate it, exactly as a new relation does not invalidate
// an issue's.
func projectResponse(project *domain.Project) *api.ProjectHeaders {
	return &api.ProjectHeaders{
		Etag:     api.NewOptString(backend.ETag(project.UpdatedAt)),
		Response: toProject(project),
	}
}

func (h *Handler) ListProjects(ctx context.Context, params api.ListProjectsParams) (
	*api.ProjectListHeaders, error) {
	state, err := domain.ParseProjectStateFilter(string(params.State.Or(api.ListProjectsStateActive)))
	if err != nil {
		return nil, err
	}
	sort := domain.DefaultProjectSort
	if value, ok := params.Sort.Get(); ok {
		var err error
		if sort, err = domain.ParseProjectSort(string(value)); err != nil {
			return nil, err
		}
	}
	page, err := h.backendFor(ctx).ListProjectsByState(ctx, params.Filter.Or(""), state, sort,
		optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	return &api.ProjectListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toProjects(page.Projects),
	}, nil
}

func (h *Handler) ArchiveProject(ctx context.Context, params api.ArchiveProjectParams) (*api.ProjectHeaders, error) {
	project, err := h.backendFor(ctx).ArchiveProject(ctx, string(params.Key), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return projectResponse(project), nil
}

func (h *Handler) RestoreProject(ctx context.Context, params api.RestoreProjectParams) (*api.ProjectHeaders, error) {
	project, err := h.backendFor(ctx).RestoreProject(ctx, string(params.Key), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return projectResponse(project), nil
}

func (h *Handler) ListProjectActivity(ctx context.Context, params api.ListProjectActivityParams) (*api.ProjectActivityListHeaders, error) {
	page, err := h.backendFor(ctx).ListProjectActivity(ctx, string(params.Key), optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	entries := make([]api.ProjectActivity, len(page.Activity))
	for i, entry := range page.Activity {
		entries[i] = api.ProjectActivity{ID: entry.ID, Project: api.ProjectKey(entry.Project),
			Action: api.ProjectActivityAction(entry.Action), Actor: entry.Actor,
			CreatedAt: api.Timestamp(entry.CreatedAt)}
	}
	return &api.ProjectActivityListHeaders{XTotalCount: api.NewOptInt(page.Total), Response: entries}, nil
}

func (h *Handler) CreateProject(ctx context.Context, req *api.ProjectCreate) (
	*api.ProjectCreatedHeaders, error) {
	project, err := h.backendFor(ctx).CreateProject(ctx, backend.ProjectCreate{
		Key:         string(req.Key),
		Name:        req.Name.Or(""),
		Description: req.Description.Or(""),
	})
	if err != nil {
		return nil, err
	}
	return &api.ProjectCreatedHeaders{
		Etag:     api.NewOptString(backend.ETag(project.UpdatedAt)),
		Location: api.NewOptString("/api/projects/" + project.Key),
		Response: toProject(project),
	}, nil
}

func (h *Handler) GetProject(ctx context.Context, params api.GetProjectParams) (
	*api.ProjectHeaders, error) {
	project, err := h.backendFor(ctx).GetProject(ctx, string(params.Key))
	if err != nil {
		return nil, err
	}
	return projectResponse(project), nil
}

// UpdateProject replaces each field the body carries and leaves the others
// alone. key may appear but may not change, and is ignored when it equals the
// key in the path and refused when it differs, exactly as status is on an
// issue; active_issues and the timestamps are derived and their values
// ignored, so a UI can send back the object it read. As on an issue, they are
// still validated against the schema declared for each.
func (h *Handler) UpdateProject(ctx context.Context, req *api.ProjectPatch,
	params api.UpdateProjectParams) (*api.ProjectHeaders, error) {
	key := string(params.Key)
	if sent, ok := req.Key.Get(); ok && string(sent) != key {
		return nil, awberr.Usagef("a project key is immutable")
	}

	project, err := h.backendFor(ctx).UpdateProject(ctx, key, backend.ProjectPatch{
		Name:        optString(req.Name),
		Description: optString(req.Description),
	}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return projectResponse(project), nil
}

// DeleteProject takes --cascade as a boolean query parameter. There is no
// force parameter: the HTTP method is the confirmation that --force supplies
// on the command line.
//
// As with an issue, the response is the object as it was immediately before
// deletion and carries no ETag.
func (h *Handler) DeleteProject(ctx context.Context, params api.DeleteProjectParams) (
	*api.Project, error) {
	deleted, err := h.backendFor(ctx).DeleteProject(ctx, string(params.Key),
		params.Cascade.Or(false), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	project := toProject(&deleted.Project)
	return &project, nil
}
