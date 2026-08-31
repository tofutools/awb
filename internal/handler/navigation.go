package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
)

func (h *Handler) SearchNavigation(ctx context.Context, params api.SearchNavigationParams) (*api.NavigationResults, error) {
	results, err := h.backendFor(ctx).SearchNavigation(ctx, params.Q, params.Limit.Or(6))
	if err != nil {
		return nil, err
	}
	return &api.NavigationResults{
		Issues:   toIssues(results.Issues),
		Projects: toProjects(results.Projects),
		Users:    toDirectoryUsers(results.Users),
	}, nil
}
