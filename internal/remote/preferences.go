package remote

import (
	"context"
	"net/http"
	"net/url"

	"github.com/tofutools/awb/internal/domain"
)

func (b *Backend) ListProjectPreferences(ctx context.Context) ([]domain.ProjectPreference, error) {
	preferences := []domain.ProjectPreference{}
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/preferences/projects", nil),
		nil, "", &preferences)
	return preferences, err
}

func (b *Backend) SetProjectIgnored(ctx context.Context, key string, ignored bool) (
	*domain.ProjectPreference, error) {
	var preference domain.ProjectPreference
	_, err := b.call(ctx, http.MethodPut,
		b.endpoint("/api/preferences/projects/"+url.PathEscape(key), nil),
		struct {
			Ignored bool `json:"ignored"`
		}{Ignored: ignored}, "", &preference)
	return &preference, err
}
