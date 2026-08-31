package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/domain"
)

func (h *Handler) ListProjectPreferences(ctx context.Context) ([]api.ProjectPreference, error) {
	preferences, err := h.backendFor(ctx).ListProjectPreferences(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]api.ProjectPreference, len(preferences))
	for i := range preferences {
		result[i] = toProjectPreference(&preferences[i])
	}
	return result, nil
}

func (h *Handler) SetProjectIgnored(ctx context.Context, req *api.ProjectPreferenceSet,
	params api.SetProjectIgnoredParams) (*api.ProjectPreference, error) {
	preference, err := h.backendFor(ctx).SetProjectIgnored(ctx, string(params.Key), req.Ignored)
	if err != nil {
		return nil, err
	}
	result := toProjectPreference(preference)
	return &result, nil
}

func toProjectPreference(preference *domain.ProjectPreference) api.ProjectPreference {
	return api.ProjectPreference{
		Project: toProject(&preference.Project),
		Ignored: preference.Ignored,
	}
}
