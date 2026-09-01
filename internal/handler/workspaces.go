package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// workspaceResponse is issueResponse for a workspace, whose tag is built from its
// own updated_at: active_issues moving because somebody created or closed an
// issue does not invalidate it, exactly as a new relation does not invalidate
// an issue's.
func workspaceResponse(workspace *domain.Workspace) *api.WorkspaceHeaders {
	return &api.WorkspaceHeaders{
		Etag:     api.NewOptString(backend.ETag(workspace.UpdatedAt)),
		Response: toWorkspace(workspace),
	}
}

func (h *Handler) ListWorkspaces(ctx context.Context, params api.ListWorkspacesParams) (
	*api.WorkspaceListHeaders, error) {
	state, err := domain.ParseWorkspaceStateFilter(string(params.State.Or(api.ListWorkspacesStateActive)))
	if err != nil {
		return nil, err
	}
	sort := domain.DefaultWorkspaceSort
	if value, ok := params.Sort.Get(); ok {
		var err error
		if sort, err = domain.ParseWorkspaceSort(string(value)); err != nil {
			return nil, err
		}
	}
	page, err := h.backendFor(ctx).ListWorkspacesByState(ctx, params.Filter.Or(""), state, sort,
		optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	return &api.WorkspaceListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toWorkspaces(page.Workspaces),
	}, nil
}

func (h *Handler) ArchiveWorkspace(ctx context.Context, params api.ArchiveWorkspaceParams) (*api.WorkspaceHeaders, error) {
	workspace, err := h.backendFor(ctx).ArchiveWorkspace(ctx, string(params.Key), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return workspaceResponse(workspace), nil
}

func (h *Handler) RestoreWorkspace(ctx context.Context, params api.RestoreWorkspaceParams) (*api.WorkspaceHeaders, error) {
	workspace, err := h.backendFor(ctx).RestoreWorkspace(ctx, string(params.Key), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return workspaceResponse(workspace), nil
}

func (h *Handler) ListWorkspaceActivity(ctx context.Context, params api.ListWorkspaceActivityParams) (*api.WorkspaceActivityListHeaders, error) {
	page, err := h.backendFor(ctx).ListWorkspaceActivity(ctx, string(params.Key), optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	entries := make([]api.WorkspaceActivity, len(page.Activity))
	for i, entry := range page.Activity {
		entries[i] = api.WorkspaceActivity{ID: entry.ID, Workspace: api.WorkspaceKey(entry.Workspace),
			Action: api.WorkspaceActivityAction(entry.Action), Actor: entry.Actor,
			CreatedAt: api.Timestamp(entry.CreatedAt)}
	}
	return &api.WorkspaceActivityListHeaders{XTotalCount: api.NewOptInt(page.Total), Response: entries}, nil
}

func (h *Handler) CreateWorkspace(ctx context.Context, req *api.WorkspaceCreate) (
	*api.WorkspaceCreatedHeaders, error) {
	workspace, err := h.backendFor(ctx).CreateWorkspace(ctx, backend.WorkspaceCreate{
		Key:         string(req.Key),
		Name:        req.Name.Or(""),
		Description: req.Description.Or(""),
	})
	if err != nil {
		return nil, err
	}
	return &api.WorkspaceCreatedHeaders{
		Etag:     api.NewOptString(backend.ETag(workspace.UpdatedAt)),
		Location: api.NewOptString("/api/workspaces/" + workspace.Key),
		Response: toWorkspace(workspace),
	}, nil
}

func (h *Handler) GetWorkspace(ctx context.Context, params api.GetWorkspaceParams) (
	*api.WorkspaceHeaders, error) {
	workspace, err := h.backendFor(ctx).GetWorkspace(ctx, string(params.Key))
	if err != nil {
		return nil, err
	}
	return workspaceResponse(workspace), nil
}

// UpdateWorkspace replaces each field the body carries and leaves the others
// alone. key may appear but may not change, and is ignored when it equals the
// key in the path and refused when it differs, exactly as status is on an
// issue; active_issues and the timestamps are derived and their values
// ignored, so a UI can send back the object it read. As on an issue, they are
// still validated against the schema declared for each.
func (h *Handler) UpdateWorkspace(ctx context.Context, req *api.WorkspacePatch,
	params api.UpdateWorkspaceParams) (*api.WorkspaceHeaders, error) {
	key := string(params.Key)
	if sent, ok := req.Key.Get(); ok && string(sent) != key {
		return nil, awberr.Usagef("a workspace key is immutable")
	}

	workspace, err := h.backendFor(ctx).UpdateWorkspace(ctx, key, backend.WorkspacePatch{
		Name:        optString(req.Name),
		Description: optString(req.Description),
	}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return workspaceResponse(workspace), nil
}

// DeleteWorkspace takes --cascade as a boolean query parameter. There is no
// force parameter: the HTTP method is the confirmation that --force supplies
// on the command line.
//
// As with an issue, the response is the object as it was immediately before
// deletion and carries no ETag.
func (h *Handler) DeleteWorkspace(ctx context.Context, params api.DeleteWorkspaceParams) (
	*api.Workspace, error) {
	deleted, err := h.backendFor(ctx).DeleteWorkspace(ctx, string(params.Key),
		params.Cascade.Or(false), params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	workspace := toWorkspace(&deleted.Workspace)
	return &workspace, nil
}
