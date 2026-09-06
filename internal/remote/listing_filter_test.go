package remote_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/remote"
)

func TestListingFiltersAreSentByEveryRemoteBackendInput(t *testing.T) {
	want := map[string]string{
		"/api/issues":     "needle frontend",
		"/api/labels":     "needle frontend",
		"/api/workspaces": "agent tracking",
		"/api/users":      "alice awb",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, want[r.URL.Path], r.URL.Query().Get("filter"), r.URL.Path)
		switch r.URL.Path {
		case "/api/labels":
			assert.Equal(t, "ready", r.URL.Query().Get("readiness"))
			assert.Equal(t, "none", r.URL.Query().Get("ancestor"))
			assert.Equal(t, "epic", r.URL.Query().Get("ancestor-type"))
			assert.Equal(t, "true", r.URL.Query().Get("recursive"))
		case "/api/issues":
			assert.Equal(t, "awb-a1b2c3", r.URL.Query().Get("ancestor"))
			assert.Equal(t, "true", r.URL.Query().Get("recursive"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "operator")
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	ancestor := "awb-a1b2c3"
	_, err = client.ListIssues(t.Context(), &domain.Filter{
		ListingFilter: want["/api/issues"], Ancestor: &ancestor, Recursive: true,
	})
	require.NoError(t, err)
	wireSentinel := "none"
	_, err = client.ListIssues(t.Context(), &domain.Filter{Ancestor: &wireSentinel})
	require.Error(t, err, "remote mode rejects the wire sentinel as an internal filter value")
	epicType := domain.TypeEpic
	_, err = client.LabelFacets(t.Context(), &domain.Filter{
		ListingFilter: want["/api/labels"],
		Readiness:     domain.ReadinessReady,
		Ancestor:      new(string),
		AncestorType:  &epicType,
		Recursive:     true,
	})
	require.NoError(t, err)
	_, err = client.ListWorkspaces(t.Context(), want["/api/workspaces"], domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	_, err = client.ListUsers(t.Context(), want["/api/users"], nil, nil)
	require.NoError(t, err)
}
