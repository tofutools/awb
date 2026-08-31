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
		`{"name":"alice","password":"hunter2","user_admin":true}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.Equal(t, "alice", user.Name)
	assert.True(t, user.UserAdmin)
	assert.False(t, user.ProjectAdmin)
	assert.Empty(t, user.Projects)

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
		`{"name":"alice","password":"hunter2","projects":[]}`,
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
	a.createUser(`{"name":"alice","password":"hunter2","project_admin":true}`)

	resp, payload := a.do(http.MethodGet, "/api/users", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "2", resp.Header.Get("X-Total-Count"))

	var users []domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &users))
	require.Len(t, users, 2)
	assert.Equal(t, "alice", users[0].Name, "ordered by name ascending")
	assert.Equal(t, "bob", users[1].Name)
	assert.Contains(t, payload, `"activity_projects":[]`)

	resp, payload = a.do(http.MethodGet, "/api/users/alice", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.True(t, user.ProjectAdmin)
	assert.Equal(t, backend.ETag(user.UpdatedAt), resp.Header.Get("ETag"))

	resp, _ = a.do(http.MethodGet, "/api/users/nobody", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A name may appear in the body but may not change, exactly as a project key
// may not; the derived fields may appear and are ignored, so a UI can send back
// the object it read.
func TestUpdateUser(t *testing.T) {
	a := newAPI(t)
	created := a.createUser(`{"name":"alice","password":"hunter2"}`)

	resp, payload := a.do(http.MethodPatch, "/api/users/alice", `{"user_admin":true}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.True(t, user.UserAdmin)
	assert.Greater(t, user.UpdatedAt, created.UpdatedAt)

	// An empty patch succeeds and changes nothing.
	resp, payload = a.do(http.MethodPatch, "/api/users/alice", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	// The object it read, sent back with one field changed.
	round, err := json.Marshal(map[string]any{
		"name": "alice", "user_admin": false, "project_admin": user.ProjectAdmin,
		"projects": user.Projects, "created_at": user.CreatedAt, "updated_at": user.UpdatedAt,
	})
	require.NoError(t, err)
	resp, payload = a.do(http.MethodPatch, "/api/users/alice", string(round))
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.False(t, user.UserAdmin)

	// But a name that differs from the path is refused rather than ignored.
	resp, _ = a.do(http.MethodPatch, "/api/users/alice", `{"name":"bob"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// If-Match guards a user as it guards an issue and a project.
func TestUpdateUserPrecondition(t *testing.T) {
	a := newAPI(t)
	user := a.createUser(`{"name":"alice","password":"hunter2"}`)
	stale := backend.ETag(user.UpdatedAt)

	resp, _ := a.do(http.MethodPatch, "/api/users/alice", `{"user_admin":true}`,
		"If-Match", stale)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = a.do(http.MethodPatch, "/api/users/alice", `{"project_admin":true}`,
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
	require.Len(t, user.Projects, 1, "the memberships as they were")

	resp, _ = a.do(http.MethodDelete, "/api/users/alice", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A membership is addressed by its own path and setting it is idempotent,
// which is why it is a PUT and why it takes no If-Match.
func TestProjectMembership(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"alice","password":"hunter2"}`)

	resp, payload := a.do(http.MethodPut, "/api/projects/awb/members/alice", `{"access":"admin"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var membership domain.Membership
	require.NoError(t, json.Unmarshal([]byte(payload), &membership))
	assert.Equal(t, domain.Membership{
		Project: "awb", User: "alice", Access: domain.AccessAdmin}, membership)
	assert.Empty(t, resp.Header.Get("ETag"))

	// Setting the same access again succeeds and changes nothing.
	resp, _ = a.do(http.MethodPut, "/api/projects/awb/members/alice", `{"access":"admin"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, payload = a.do(http.MethodGet, "/api/projects/awb/members", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))
	var members []domain.Membership
	require.NoError(t, json.Unmarshal([]byte(payload), &members))
	require.Len(t, members, 1)

	resp, payload = a.do(http.MethodDelete, "/api/projects/awb/members/alice", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &membership))
	assert.Equal(t, domain.AccessAdmin, membership.Access, "the access as it was before")

	// Withdrawing access nobody holds is a 404.
	resp, _ = a.do(http.MethodDelete, "/api/projects/awb/members/alice", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestProjectMembershipRefusals(t *testing.T) {
	a := newAPI(t)
	a.createUser(`{"name":"alice","password":"hunter2"}`)

	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/projects/awb/members/nobody", `{"access":"regular"}`, http.StatusNotFound},
		{"/api/projects/nosuch/members/alice", `{"access":"regular"}`, http.StatusNotFound},
		{"/api/projects/awb/members/alice", `{"access":"owner"}`, http.StatusBadRequest},
		{"/api/projects/awb/members/alice", `{}`, http.StatusBadRequest},
		{"/api/projects/awb/members/alice", `{"access":"regular","nonsense":1}`, http.StatusBadRequest},
		// The two ends are in the path; a body may repeat them but not disagree.
		{"/api/projects/awb/members/alice", `{"access":"regular","user":"bob"}`, http.StatusBadRequest},
		{"/api/projects/awb/members/alice", `{"access":"regular","project":"web"}`, http.StatusBadRequest},
	} {
		resp, payload := a.do(http.MethodPut, tc.path, tc.body)
		assert.Equal(t, tc.status, resp.StatusCode, "%s %s", tc.path, tc.body)
		assert.Contains(t, payload, `"error"`, tc.body)
	}

	// Repeating them and agreeing is accepted, so a UI can send back what it read.
	resp, payload := a.do(http.MethodPut, "/api/projects/awb/members/alice",
		`{"access":"regular","project":"awb","user":"alice"}`)
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
	resp, _ = a.do(http.MethodDelete, "/api/projects/awb/members/alice", `{"access":"admin"}`)
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
	assert.Len(t, users[0].Projects, 1)
	assert.Empty(t, users[1].Projects)
	assert.NotNil(t, users[1].Projects, "an empty membership list is [] and never null")
}

// The identity endpoint still says only who is calling; what they may do is
// their own user object, which they may always read.
func TestIdentityStillOnlySaysWho(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodGet, "/api/identity", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.JSONEq(t, `{"identity":"mikael"}`, payload)

	_, err := a.be.CreateUser(a.t.Context(),
		backend.UserCreate{Name: "mikael", Password: "hunter2", ProjectAdmin: true})
	require.NoError(t, err)

	resp, payload = a.do(http.MethodGet, "/api/users/mikael", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(payload), &user))
	assert.True(t, user.ProjectAdmin)
}
