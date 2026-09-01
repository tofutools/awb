package remote

import (
	"context"
	"net/http"
	"net/url"

	"github.com/tofutools/awb/internal/domain"
)

func (b *Backend) ListWorkspacePreferences(ctx context.Context) ([]domain.WorkspacePreference, error) {
	preferences := []domain.WorkspacePreference{}
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/preferences/workspaces", nil),
		nil, "", &preferences)
	return preferences, err
}

func (b *Backend) SetWorkspaceIgnored(ctx context.Context, key string, ignored bool) (
	*domain.WorkspacePreference, error) {
	var preference domain.WorkspacePreference
	_, err := b.call(ctx, http.MethodPut,
		b.endpoint("/api/preferences/workspaces/"+url.PathEscape(key), nil),
		struct {
			Ignored bool `json:"ignored"`
		}{Ignored: ignored}, "", &preference)
	return &preference, err
}
