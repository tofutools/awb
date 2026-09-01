package remote_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/remote"
)

func TestUserLifecyclePreservesTheRemoteContract(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 1:
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/users", r.URL.Path)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "safe password", body["password"])
			assert.Equal(t, true, body["user_admin"])
			w.WriteHeader(http.StatusCreated)
		case 2:
			assert.Equal(t, http.MethodPatch, r.Method)
			assert.Equal(t, "/api/users/alice", r.URL.Path)
			assert.Equal(t, `"user-v1"`, r.Header.Get("If-Match"))
		case 3:
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, "/api/users/alice", r.URL.Path)
			assert.Equal(t, `"user-v2"`, r.Header.Get("If-Match"))
		default:
			t.Fatalf("unexpected request %d", calls)
		}
		_, _ = w.Write([]byte(`{
			"name":"alice","full_name":"Alice Andersson",
			"project_admin":false,"user_admin":true,
			"created_at":"2026-09-01T06:00:00.000Z",
			"updated_at":"2026-09-01T06:00:00.000Z","projects":[]
		}`))
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "operator")
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	created, err := client.CreateUser(t.Context(), backend.UserCreate{
		Name: "alice", FullName: "Alice Andersson", Password: "safe password", UserAdmin: true,
	})
	require.NoError(t, err)
	assert.True(t, created.UserAdmin)

	fullName := "Alice Berg"
	updated, err := client.UpdateUser(t.Context(), "alice", backend.UserPatch{FullName: &fullName}, `"user-v1"`)
	require.NoError(t, err)
	assert.Equal(t, "Alice Andersson", updated.FullName)

	deleted, err := client.DeleteUser(t.Context(), "alice", `"user-v2"`)
	require.NoError(t, err)
	assert.Equal(t, "alice", deleted.User.Name)
	assert.Equal(t, 3, calls)
}
