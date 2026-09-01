package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/domain"
)

func (h *Handler) ListWorkspacePreferences(ctx context.Context) ([]api.WorkspacePreference, error) {
	preferences, err := h.backendFor(ctx).ListWorkspacePreferences(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]api.WorkspacePreference, len(preferences))
	for i := range preferences {
		result[i] = toWorkspacePreference(&preferences[i])
	}
	return result, nil
}

func (h *Handler) SetWorkspaceIgnored(ctx context.Context, req *api.WorkspacePreferenceSet,
	params api.SetWorkspaceIgnoredParams) (*api.WorkspacePreference, error) {
	preference, err := h.backendFor(ctx).SetWorkspaceIgnored(ctx, string(params.Key), req.Ignored)
	if err != nil {
		return nil, err
	}
	result := toWorkspacePreference(preference)
	return &result, nil
}

func toWorkspacePreference(preference *domain.WorkspacePreference) api.WorkspacePreference {
	return api.WorkspacePreference{
		Workspace: toWorkspace(&preference.Workspace),
		Ignored:   preference.Ignored,
	}
}
