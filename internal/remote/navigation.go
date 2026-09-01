package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

type navigationResults struct {
	Issues     []domain.Issue     `json:"issues"`
	Workspaces []domain.Workspace `json:"workspaces"`
	Users      []directoryUser    `json:"users"`
}

func (b *Backend) SearchNavigation(ctx context.Context, query string, limit int) (backend.NavigationResults, error) {
	values := url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}
	var wire navigationResults
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/navigation", values), nil, "", &wire)
	if err != nil {
		return backend.NavigationResults{}, err
	}
	results := backend.NavigationResults{Issues: wire.Issues, Workspaces: wire.Workspaces}
	results.Users = make([]domain.User, len(wire.Users))
	for i := range wire.Users {
		results.Users[i] = wire.Users[i].User
		results.Users[i].ActivityWorkspaces = wire.Users[i].ActivityWorkspaces
	}
	return results, nil
}
