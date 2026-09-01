package remote_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/remote"
)

func TestRemoteWorkspaceLifecycleParity(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			assert.Equal(t, "/api/workspaces/awb/archive", r.URL.Path)
			assert.Equal(t, `"v1"`, r.Header.Get("If-Match"))
			_, _ = w.Write([]byte(`{"key":"awb","name":"awb","description":"","state":"archived","archived_at":"2026-09-01T00:00:00.000Z","archived_by":"mikael","active_issues":0,"created_at":"2026-09-01T00:00:00.000Z","updated_at":"2026-09-01T00:00:01.000Z"}`))
		case 2:
			assert.Equal(t, "/api/workspaces", r.URL.Path)
			assert.Equal(t, "archived", r.URL.Query().Get("state"))
			_, _ = w.Write([]byte(`[]`))
		case 3:
			assert.Equal(t, "/api/workspaces/awb/activity", r.URL.Path)
			w.Header().Set("X-Total-Count", "0")
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "operator")
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	workspace, err := client.ArchiveWorkspace(t.Context(), "awb", `"v1"`)
	require.NoError(t, err)
	assert.Equal(t, domain.WorkspaceArchived, workspace.State)
	_, err = client.ListWorkspacesByState(t.Context(), "", domain.WorkspacesArchived, domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	_, err = client.ListWorkspaceActivity(t.Context(), "awb", nil, nil)
	require.NoError(t, err)
	var _ backend.Backend = client
}
