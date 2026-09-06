package remote_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/remote"
)

func TestSearchNavigationPreservesDirectoryUserFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/navigation", r.URL.Path)
		assert.Equal(t, "mikael", r.URL.Query().Get("q"))
		assert.Equal(t, "6", r.URL.Query().Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issues": [],
			"workspaces": [],
			"users": [{
				"name": "mikael",
				"full_name": "Mikael Ståldal",
				"workspace_admin": false,
				"user_admin": false,
				"created_at": "2026-01-01T00:00:00.000Z",
				"updated_at": "2026-01-01T00:00:00.000Z",
				"workspaces": [],
				"activity_workspaces": ["awb"]
			}]
		}`))
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "operator", false)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	results, err := client.SearchNavigation(t.Context(), "mikael", 6)
	require.NoError(t, err)
	require.Len(t, results.Users, 1)
	assert.Equal(t, "Mikael Ståldal", results.Users[0].FullName)
	assert.Equal(t, []string{"awb"}, results.Users[0].ActivityWorkspaces)
}
