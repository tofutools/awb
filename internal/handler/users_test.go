package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// createUser posts a user and returns it.
func (a *api) createUser(body string) domain.User {
	a.t.Helper()
	resp, payload := a.do(http.MethodPost, "/api/users", body)
	require.Equal(a.t, http.StatusCreated, resp.StatusCode, payload)

	var user domain.User
	require.NoError(a.t, json.Unmarshal([]byte(payload), &user))
	return user
}

func TestCreateUser(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodPost, "/api/users",
		`{"name":"alice","full_name":"Alice Andersson","password":"hunter2","user_admin":true}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.Equal(t, "alice", user.Name)
	assert.Equal(t, "Alice Andersson", user.FullName)
	assert.True(t, user.UserAdmin)
	assert.False(t, user.WorkspaceAdmin)
	assert.Empty(t, user.Workspaces)

	assert.Equal(t, "/api/users/alice", resp.Header.Get("Location"))
	assert.Equal(t, backend.ETag(user.UpdatedAt), resp.Header.Get("ETag"))

	// The response says nothing about the credential, in either form.
	assert.NotContains(t, payload, "password")
	assert.NotContains(t, payload, "hunter2")
}

// A pre-computed bcrypt hash sets the password without the plaintext reaching
// the server, which is what "htpasswd -Bn" is for.
func TestCreateUserWithAPasswordHash(t *testing.T) {
	a := newAPI(t)
	user := a.createUser(
		`{"name":"alice","password_hash":"$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by"}`)
	assert.Equal(t, "alice", user.Name)

	// The two are two ways of stating one credential, and neither is optional.
	for _, body := range []string{
		`{"name":"bob"}`,
		`{"name":"bob","password":"hunter2","password_hash":"$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by"}`,
		`{"name":"bob","password_hash":"hunter2"}`,
	} {
		resp, payload := a.do(http.MethodPost, "/api/users", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, `"error"`, body)
	}
}

// Nothing beyond the recognised fields is accepted: a field the server never
// reads is a thing the client believes it said.
func TestCreateUserRejectsUnknownFields(t *testing.T) {
	a := newAPI(t)
	for _, body := range []string{
		`{"name":"alice","password":"hunter2","nonsense":1}`,
		`{"name":"alice","password":"hunter2","created_at":"2026-01-01T00:00:00.000Z"}`,
		`{"name":"alice","password":"hunter2","workspaces":[]}`,
		`{"name":"Alice","password":"hunter2"}`,
		`{"name":"alice","password":""}`,
		`{"name":"alice","password":null}`,
	} {
		resp, payload := a.do(http.MethodPost, "/api/users", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, `"error"`, body)
	}
}

func TestGetAndListUsers(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"bob","password":"hunter2"}`)
	a.createUser(`{"name":"alice","full_name":"Alice Andersson","password":"hunter2","workspace_admin":true}`)

	resp, payload := a.do(http.MethodGet, "/api/users", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "2", resp.Header.Get("X-Total-Count"))

	var users []domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &users))
	require.Len(t, users, 2)
	assert.Equal(t, "alice", users[0].Name, "ordered by name ascending")
	assert.Equal(t, "Alice Andersson", users[0].FullName)
	assert.Equal(t, "bob", users[1].Name)
	assert.Contains(t, payload, `"activity_workspaces":[]`)

	resp, payload = a.do(http.MethodGet, "/api/users?filter=andersson&limit=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &users))
	require.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].Name)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))

	resp, payload = a.do(http.MethodGet, "/api/users/alice", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.True(t, user.WorkspaceAdmin)
	assert.Equal(t, backend.ETag(user.UpdatedAt), resp.Header.Get("ETag"))

	resp, _ = a.do(http.MethodGet, "/api/users/nobody", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A name may appear in the body but may not change, exactly as a workspace key
// may not; the derived fields may appear and are ignored, so a UI can send back
// the object it read.
func TestUpdateUser(t *testing.T) {
	a := newAPI(t)
	created := a.createUser(`{"name":"alice","password":"hunter2"}`)

	resp, payload := a.do(http.MethodPatch, "/api/users/alice", `{"full_name":"Alice Andersson","user_admin":true}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.True(t, user.UserAdmin)
	assert.Equal(t, "Alice Andersson", user.FullName)
	assert.Greater(t, user.UpdatedAt, created.UpdatedAt)

	// An empty patch succeeds and changes nothing.
	resp, payload = a.do(http.MethodPatch, "/api/users/alice", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	// The object it read, sent back with one field changed.
	round, err := json.Marshal(map[string]any{
		"name": "alice", "full_name": "Alice Berg", "user_admin": false, "workspace_admin": user.WorkspaceAdmin,
		"workspaces": user.Workspaces, "created_at": user.CreatedAt, "updated_at": user.UpdatedAt,
	})
	require.NoError(t, err)
	resp, payload = a.do(http.MethodPatch, "/api/users/alice", string(round))
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.False(t, user.UserAdmin)
	assert.Equal(t, "Alice Berg", user.FullName)

	// But a name that differs from the path is refused rather than ignored.
	resp, _ = a.do(http.MethodPatch, "/api/users/alice", `{"name":"bob"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// If-Match guards a user as it guards an issue and a workspace.
func TestUpdateUserPrecondition(t *testing.T) {
	a := newAPI(t)
	user := a.createUser(`{"name":"alice","password":"hunter2"}`)
	stale := backend.ETag(user.UpdatedAt)

	resp, payload := a.do(http.MethodPatch, "/api/users/alice", `{"full_name":"Alice Andersson"}`,
		"If-Match", stale)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var updated domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &updated))
	assert.Equal(t, "Alice Andersson", updated.FullName)
	assert.Greater(t, updated.UpdatedAt, user.UpdatedAt,
		"a full-name-only change moves the user version")
	assert.Equal(t, backend.ETag(updated.UpdatedAt), resp.Header.Get("ETag"))

	resp, _ = a.do(http.MethodPatch, "/api/users/alice", `{"workspace_admin":true}`,
		"If-Match", stale)
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
}

// The response is the user as they were immediately before deletion, and
// carries no ETag: the version it describes is gone.
func TestDeleteUser(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"alice","password":"hunter2"}`)
	_, err := a.be.SetMember(a.t.Context(), "awb", "alice", domain.AccessAdmin)
	require.NoError(t, err)

	resp, payload := a.do(http.MethodDelete, "/api/users/alice", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.Equal(t, "alice", user.Name)
	require.Len(t, user.Workspaces, 1, "the memberships as they were")

	resp, _ = a.do(http.MethodDelete, "/api/users/alice", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Collection POST creates only, while the addressed resource's PUT remains an
// idempotent replacement.
func TestWorkspaceMembership(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"alice","password":"hunter2"}`)

	resp, payload := a.do(http.MethodPost, "/api/workspaces/awb/members",
		`{"user":"alice","access":"admin"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var membership domain.Membership
	require.NoError(t, json.Unmarshal([]byte(payload), &membership))
	assert.Equal(t, domain.Membership{
		Workspace: "awb", User: "alice", Access: domain.AccessAdmin}, membership)
	assert.Empty(t, resp.Header.Get("ETag"))

	// A stale create cannot replace the access granted by somebody else.
	resp, _ = a.do(http.MethodPost, "/api/workspaces/awb/members",
		`{"user":"alice","access":"regular"}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// Setting the same access again succeeds and changes nothing.
	resp, _ = a.do(http.MethodPut, "/api/workspaces/awb/members/alice", `{"access":"admin"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, payload = a.do(http.MethodGet, "/api/workspaces/awb/members", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))
	var members []domain.Membership
	require.NoError(t, json.Unmarshal([]byte(payload), &members))
	require.Len(t, members, 1)

	resp, payload = a.do(http.MethodDelete, "/api/workspaces/awb/members/alice", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &membership))
	assert.Equal(t, domain.AccessAdmin, membership.Access, "the access as it was before")

	// Withdrawing access nobody holds is a 404.
	resp, _ = a.do(http.MethodDelete, "/api/workspaces/awb/members/alice", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestWorkspaceMembershipRefusals(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"alice","password":"hunter2"}`)

	for _, body := range []string{
		`{"user":"nobody","access":"regular"}`,
		`{"access":"regular"}`,
		`{"user":"alice","access":"owner"}`,
		`{"user":"alice","access":"regular","nonsense":1}`,
	} {
		resp, payload := a.do(http.MethodPost, "/api/workspaces/awb/members", body)
		if body == `{"user":"nobody","access":"regular"}` {
			assert.Equal(t, http.StatusNotFound, resp.StatusCode, body)
		} else {
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		}
		assert.Contains(t, payload, `"error"`, body)
	}

	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/workspaces/awb/members/nobody", `{"access":"regular"}`, http.StatusNotFound},
		{"/api/workspaces/nosuch/members/alice", `{"access":"regular"}`, http.StatusNotFound},
		{"/api/workspaces/awb/members/alice", `{"access":"owner"}`, http.StatusBadRequest},
		{"/api/workspaces/awb/members/alice", `{}`, http.StatusBadRequest},
		{"/api/workspaces/awb/members/alice", `{"access":"regular","nonsense":1}`, http.StatusBadRequest},
		// The two ends are in the path; a body may repeat them but not disagree.
		{"/api/workspaces/awb/members/alice", `{"access":"regular","user":"bob"}`, http.StatusBadRequest},
		{"/api/workspaces/awb/members/alice", `{"access":"regular","workspace":"web"}`, http.StatusBadRequest},
	} {
		resp, payload := a.do(http.MethodPut, tc.path, tc.body)
		assert.Equal(t, tc.status, resp.StatusCode, "%s %s", tc.path, tc.body)
		assert.Contains(t, payload, `"error"`, tc.body)
	}

	// Repeating them and agreeing is accepted, so a UI can send back what it read.
	resp, payload := a.do(http.MethodPut, "/api/workspaces/awb/members/alice",
		`{"access":"regular","workspace":"awb","user":"alice"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
}

// The endpoints accept exactly the parameters the document declares, and no
// others.
func TestUserEndpointsAreStrict(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"alice","password":"hunter2"}`)

	resp, _ := a.do(http.MethodGet, "/api/users?sort=name", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp, _ = a.do(http.MethodGet, "/api/users/alice?limit=1", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// An operation that declares no body refuses one.
	resp, _ = a.do(http.MethodDelete, "/api/workspaces/awb/members/alice", `{"access":"admin"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// A method a path does not serve is a 405 naming what it does.
	resp, _ = a.do(http.MethodPost, "/api/users/alice", `{}`)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("Allow"))
}

// Paging a listing leaves the unpaged total alone, here as everywhere.
func TestUserListingPages(t *testing.T) {
	a := newAPI(t)
	for _, name := range []string{"alice", "bob", "carol"} {
		a.createUser(`{"name":"` + name + `","password":"hunter2"}`)
	}
	_, err := a.be.SetMember(a.t.Context(), "awb", "alice", domain.AccessRegular)
	require.NoError(t, err)

	resp, payload := a.do(http.MethodGet, "/api/users?limit=1&offset=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "3", resp.Header.Get("X-Total-Count"))

	var users []domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &users))
	require.Len(t, users, 1)
	assert.Equal(t, "bob", users[0].Name)

	// Every user in a listing carries their memberships, read in one query.
	resp, payload = a.do(http.MethodGet, "/api/users", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &users))
	require.Len(t, users, 3)
	assert.Len(t, users[0].Workspaces, 1)
	assert.Empty(t, users[1].Workspaces)
	assert.NotNil(t, users[1].Workspaces, "an empty membership list is [] and never null")
}

// The identity endpoint reports effective account-administration capability,
// which is not necessarily a stored flag in unrestricted direct/no-auth mode.
func TestIdentityReportsEffectiveUserAdministration(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodGet, "/api/identity", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.JSONEq(t, `{"identity":"mikael","may_manage_users":true}`, payload)

	_, err := a.be.CreateUser(a.t.Context(),
		backend.UserCreate{Name: "mikael", Password: "hunter2", WorkspaceAdmin: true})
	require.NoError(t, err)

	resp, payload = a.do(http.MethodGet, "/api/users/mikael", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.True(t, user.WorkspaceAdmin)
}
