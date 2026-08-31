package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/openapi"
	"github.com/tofutools/awb/internal/remote"
	"github.com/tofutools/awb/internal/storage"
)

// A database holding no user is a server that authenticates nobody, which is
// what version 1 was and what a local tracker still is.
func TestAServerWithNoUsersAuthenticatesNobody(t *testing.T) {
	h := newServeHandler(t)

	resp, body := get(t, h, http.MethodGet, "/api/identity")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "mikael", "the fixed identity stands in for a caller")

	resp, _ = get(t, h, http.MethodGet, "/api/projects")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// Adding the first user closes the door, and it closes on the next request
// rather than on the next restart.
func TestAddingTheFirstUserTurnsAuthenticationOn(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})

	resp, _ := get(t, h, http.MethodGet, "/api/projects")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)

	// The same handler, no restart.
	resp, body := get(t, h, http.MethodGet, "/api/projects")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `realm="awb"`)
	assert.JSONEq(t, `{"error":"unauthorized"}`, body)

	resp, _ = get(t, h, http.MethodGet, "/api/projects", basicAuth("alice", "hunter2")...)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

}

// Deleting the last user does not undo that. A server that has authenticated
// answers nothing rather than reverting to serving everybody, because nobody
// asked for it to be opened and a client that arrives a moment later would
// otherwise be handed the whole database.
func TestDeletingTheLastUserDoesNotOpenTheServer(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)
	resp, _ := get(t, h, http.MethodGet, "/api/projects", basicAuth("alice", "hunter2")...)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = be.DeleteUser(t.Context(), "alice", "")
	require.NoError(t, err)

	// The same handler, no restart. There is no challenge, because no
	// credentials could open a server with no accounts.
	resp, body := get(t, h, http.MethodGet, "/api/projects")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("WWW-Authenticate"))
	assert.Contains(t, body, "no users")

	// Nor do the credentials that used to work.
	resp, _ = get(t, h, http.MethodGet, "/api/projects", basicAuth("alice", "hunter2")...)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	// Adding one again makes the next request work, with no restart.
	_, err = be.CreateUser(t.Context(), backend.UserCreate{Name: "bob", Password: "hunter2"})
	require.NoError(t, err)
	resp, _ = get(t, h, http.MethodGet, "/api/projects", basicAuth("bob", "hunter2")...)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// The lock does not depend on the server having watched it happen. A user
// added and deleted between two requests leaves the database saying exactly
// what it says after any other deletion, because what a server reads is a
// stored fact and not its own memory of one — which is the whole reason the
// fact is stored.
func TestALockDoesNotDependOnHavingSeenTheUser(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})

	// No request in between: this server never authenticates anybody.
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.DeleteUser(t.Context(), "alice", "")
	require.NoError(t, err)

	resp, _ := get(t, h, http.MethodGet, "/api/projects")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// Nothing is exempt from the lock either — not the UI, not the documents, and
// not the CORS preflight that authentication itself lets past, because no
// request could follow one successfully.
func TestTheLockCoversEverythingTheServerServes(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{
		addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb",
		corsOrigins: []string{"https://ui.example.com"},
	})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.DeleteUser(t.Context(), "alice", "")
	require.NoError(t, err)

	for _, path := range []string{
		"/", "/app.js", "/api/issues", "/openapi.json", "/openapi.yaml",
	} {
		resp, _ := get(t, h, http.MethodGet, path)
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, path)
	}

	resp, _ := get(t, h, http.MethodOptions, "/api/issues",
		"Origin", "https://ui.example.com",
		"Access-Control-Request-Method", "GET")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// --no-auth is not a weaker authenticator, it is none: the users table is not
// consulted at all, so a database that holds accounts is served openly and
// adding one to such a server does not close the door either. That is what the
// flag was asked to mean, and taking it back is a restart without it.
func TestNoAuthConsultsNoUsers(t *testing.T) {
	opts := serveOptions{addr: "127.0.0.1", port: 7777, noAuth: true}
	h, be := newServeHandlerAuthenticating(t, opts, false)

	resp, body := get(t, h, http.MethodGet, "/api/identity")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "mikael", "the fixed identity stands in for a caller")

	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)

	resp, _ = get(t, h, http.MethodGet, "/api/projects")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the first user does not close this door")
	assert.Empty(t, resp.Header.Get("WWW-Authenticate"))

	// Nor does deleting it lock one, there being nothing to lock.
	_, err = be.DeleteUser(t.Context(), "alice", "")
	require.NoError(t, err)
	resp, _ = get(t, h, http.MethodGet, "/api/projects")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// A wrong password and an unknown username are the same answer, and neither is
// distinguishable from the other in what the server says.
func TestWrongCredentialsSayNothingAboutTheAccount(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)

	wrong, wrongBody := get(t, h, http.MethodGet, "/api/projects", basicAuth("alice", "hunter3")...)
	unknown, unknownBody := get(t, h, http.MethodGet, "/api/projects", basicAuth("mallory", "hunter2")...)

	assert.Equal(t, http.StatusUnauthorized, wrong.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, unknown.StatusCode)
	assert.Equal(t, wrongBody, unknownBody)
}

// Nothing is exempt: the document and the web UI sit behind authentication
// with the API.
func TestAuthenticationCoversEverythingTheServerServes(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)

	for _, path := range []string{"/", "/api/issues", "/openapi.json", "/openapi.yaml"} {
		resp, _ := get(t, h, http.MethodGet, path)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
	}
}

// The API answers each request with the caller's own permissions, which is the
// whole point of authenticating.
func TestTheAPIAnswersWithTheCallersPermissions(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})
	ctx := t.Context()

	for _, key := range []string{"awb", "web"} {
		_, err := be.CreateProject(ctx, backend.ProjectCreate{Key: key})
		require.NoError(t, err)
	}
	_, err := be.CreateUser(ctx, backend.UserCreate{Name: "bob", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.SetMember(ctx, "awb", "bob", domain.AccessRegular)
	require.NoError(t, err)

	resp, body := get(t, h, http.MethodGet, "/api/projects", basicAuth("bob", "hunter2")...)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"), "the total counts what bob may see")

	var projects []domain.Project
	require.NoError(t, json.Unmarshal([]byte(body), &projects))
	require.Len(t, projects, 1)
	assert.Equal(t, "awb", projects[0].Key)

	// One he cannot see is not found, and one he can see but may not create is
	// forbidden.
	resp, _ = get(t, h, http.MethodGet, "/api/projects/web", basicAuth("bob", "hunter2")...)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	resp, _ = send(t, h, http.MethodPost, "/api/projects", `{"key":"third"}`,
		append(basicAuth("bob", "hunter2"), "Origin", "http://127.0.0.1:7777")...)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A 403 becomes exit code 5 in remote mode and a 404 becomes 3, so a command
// reports the same thing through a server as it would on a file.
func TestRemoteModeCarriesTheAuthorizationExitCodes(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	be := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	h, err := buildHandler(be, openapi.New(raw), &authenticator{db: db, realm: "awb"},
		serveOptions{port: 7777, basicAuthRealm: "awb"}, log.New(io.Discard, "", 0))
	require.NoError(t, err)

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	ctx := t.Context()
	for _, key := range []string{"awb", "web"} {
		_, err := be.CreateProject(ctx, backend.ProjectCreate{Key: key})
		require.NoError(t, err)
	}
	hidden, err := be.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "Button drifts"})
	require.NoError(t, err)
	_, err = be.CreateUser(ctx, backend.UserCreate{Name: "bob", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.SetMember(ctx, "awb", "bob", domain.AccessRegular)
	require.NoError(t, err)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "bob", "hunter2", "bob")
	t.Cleanup(func() { _ = client.Close() })

	// Forbidden: something bob can see and may not do.
	_, err = client.CreateProject(ctx, backend.ProjectCreate{Key: "third"})
	require.Error(t, err)
	assert.Equal(t, 5, awberr.ExitCode(err))

	// Not found: something he is not told about.
	_, err = client.GetIssue(ctx, hidden.ID)
	require.Error(t, err)
	assert.Equal(t, 3, awberr.ExitCode(err))

	// And what he may do, he does.
	issue, err := client.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "Parser crashes"})
	require.NoError(t, err)
	assert.Equal(t, "awb", issue.Project)

	// Wrong credentials are about the credentials rather than about the
	// request, so they are exit code 1 and not 5.
	wrong := remote.New(base, "bob", "hunter3", "bob")
	t.Cleanup(func() { _ = wrong.Close() })
	_, err = wrong.ListProjects(ctx, nil, nil)
	require.Error(t, err)
	assert.Equal(t, 1, awberr.ExitCode(err))
}

// Dynamic completion goes through the selected backend with its credentials,
// including search terms that the remote facet endpoints apply.
func TestRemoteCompletionUsesAuthenticatedSearchFacets(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{port: 7777, basicAuthRealm: "awb"})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	ctx := t.Context()
	for _, key := range []string{"awb", "hidden"} {
		_, err := be.CreateProject(ctx, backend.ProjectCreate{Key: key})
		require.NoError(t, err)
	}
	_, err := be.CreateIssue(ctx, backend.IssueCreate{
		Project: "awb", Title: "Parser failure", Labels: []string{"parser"},
	})
	require.NoError(t, err)
	_, err = be.CreateIssue(ctx, backend.IssueCreate{
		Project: "hidden", Title: "Parser elsewhere", Labels: []string{"secret"},
	})
	require.NoError(t, err)
	_, err = be.CreateUser(ctx, backend.UserCreate{Name: "bob", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.SetMember(ctx, "awb", "bob", domain.AccessRegular)
	require.NoError(t, err)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AWB_DB", server.URL)
	t.Setenv("AWB_USER", "bob")
	t.Setenv("AWB_PASSWORD", "hunter2")
	t.Setenv("AWB_IDENTITY", "bob")
	t.Setenv("AWB_CONFIG_FILE", "")
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	code := Execute(ctx, "test", openapi.New(raw),
		[]string{"__complete", "--no-context", "search", "Parser", "--label", ""},
		&stdout, &stderr, strings.NewReader(""))
	require.Equal(t, 0, code, stderr.String())
	assert.Equal(t, "parser\n:0\n", stdout.String())
}

func TestStatusShowsTheRemoteServerAndAuthenticatedIdentity(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{port: 7777, basicAuthRealm: "awb"})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	ctx := t.Context()
	_, err := be.CreateProject(ctx, backend.ProjectCreate{Key: "awb"})
	require.NoError(t, err)
	_, err = be.CreateUser(ctx, backend.UserCreate{Name: "bob", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.SetMember(ctx, "awb", "bob", domain.AccessRegular)
	require.NoError(t, err)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AWB_DB", server.URL)
	t.Setenv("AWB_USER", "bob")
	t.Setenv("AWB_PASSWORD", "hunter2")
	t.Setenv("AWB_IDENTITY", "local-default")
	t.Setenv("AWB_PROJECT", "")
	t.Setenv("AWB_CONFIG_FILE", "")
	t.Setenv("NO_COLOR", "1")
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	code := Execute(ctx, "test", openapi.New(raw), []string{"status", "--json"},
		&stdout, &stderr, strings.NewReader(""))
	require.Equal(t, 0, code, stderr.String())
	assert.NotContains(t, stdout.String(), "hunter2")
	assert.JSONEq(t, `{
		"connection": {
			"mode": "remote",
			"database": "",
			"server": "`+server.URL+`",
			"ui": "`+server.URL+`/#/projects",
			"attachments": ""
		},
		"configuration": {
			"identity": "bob",
			"configured_identity": "local-default",
			"user": "bob",
			"password_set": true,
			"default_project": "",
			"context_project": "",
			"context_label": "",
			"user_file": "",
			"local_file": "",
			"color": "never"
		},
		"environment": [
			{"name":"AWB_CONFIG_FILE","value":""},
			{"name":"AWB_DB","value":"`+server.URL+`"},
			{"name":"AWB_USER","value":"bob"},
			{"name":"AWB_PASSWORD","value":"<redacted>"},
			{"name":"AWB_IDENTITY","value":"local-default"},
			{"name":"AWB_PROJECT","value":""},
			{"name":"NO_COLOR","value":"1"}
		],
		"projects": [
			{"key":"awb","name":"awb","open":0,"in_progress":0,"closed":0,"total":0}
		]
	}`, stdout.String())
}

// The whole user and membership surface goes over the wire and behaves as it
// does on a file, which is what one interface with two implementations buys.
func TestRemoteModeManagesUsers(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	be := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	h, err := buildHandler(be, openapi.New(raw), &authenticator{db: db, realm: "awb"},
		serveOptions{port: 7777, basicAuthRealm: "awb"}, log.New(io.Discard, "", 0))
	require.NoError(t, err)

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	ctx := t.Context()
	_, err = be.CreateProject(ctx, backend.ProjectCreate{Key: "awb"})
	require.NoError(t, err)
	_, err = be.CreateUser(ctx, backend.UserCreate{
		Name: "alice", Password: "hunter2", ProjectAdmin: true, UserAdmin: true})
	require.NoError(t, err)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "alice", "hunter2", "alice")
	t.Cleanup(func() { _ = client.Close() })

	created, err := client.CreateUser(ctx, backend.UserCreate{Name: "bob", Password: "hunter2"})
	require.NoError(t, err)
	assert.Equal(t, "bob", created.Name)
	assert.False(t, created.UserAdmin)
	assert.Empty(t, created.Projects)

	membership, err := client.SetMember(ctx, "awb", "bob", domain.AccessAdmin)
	require.NoError(t, err)
	assert.Equal(t, domain.AccessAdmin, membership.Access)
	assert.Equal(t, "awb", membership.Project)
	assert.Equal(t, "bob", membership.User)

	members, err := client.ListMembers(ctx, "awb", nil, nil)
	require.NoError(t, err)
	require.Len(t, members.Members, 1)
	assert.Equal(t, 1, members.Total)

	users, err := client.ListUsers(ctx, nil, nil)
	require.NoError(t, err)
	assert.Len(t, users.Users, 2)

	yes := true
	updated, err := client.UpdateUser(ctx, "bob", backend.UserPatch{ProjectAdmin: &yes}, "")
	require.NoError(t, err)
	assert.True(t, updated.ProjectAdmin)
	require.Len(t, updated.Projects, 1)

	removed, err := client.RemoveMember(ctx, "awb", "bob")
	require.NoError(t, err)
	assert.Equal(t, domain.AccessAdmin, removed.Access)

	deleted, err := client.DeleteUser(ctx, "bob", "")
	require.NoError(t, err)
	assert.Equal(t, "bob", deleted.User.Name)

	_, err = client.GetUser(ctx, "bob")
	require.Error(t, err)
	assert.Equal(t, 3, awberr.ExitCode(err))
}

// A value the operator typed and got wrong is a usage mistake whatever the
// database holds, and is reported before anything is opened. Only having no
// identity at all depends on the database: a server that authenticates every
// request never attributes one to nobody.
func TestServeChecksTheIdentityFlagBeforeAnythingElse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	// No database at this path at all: reaching one would be a failure of its
	// own, so exit 2 here can only be the flag.
	t.Setenv("AWB_DB", filepath.Join(root, "nothing.db"))
	t.Setenv("AWB_CONFIG_FILE", "")

	var out, errOut bytes.Buffer
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	code := Execute(t.Context(), "test", openapi.New(raw),
		[]string{"serve", "--identity", "Mikael"}, &out, &errOut, strings.NewReader(""))

	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "--identity")
}

// Resolution itself reports only that there is none; whether that is fatal is
// the database's answer, which is why the two are separate.
func TestResolveServerIdentity(t *testing.T) {
	cfg := &config.Config{Identity: "mikael"}

	given := "alice"
	resolved, err := resolveServerIdentity(cfg, &given)
	require.NoError(t, err)
	assert.Equal(t, "alice", resolved, "the flag outranks everything below it")

	resolved, err = resolveServerIdentity(cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "mikael", resolved)

	_, err = resolveServerIdentity(&config.Config{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--identity")
}

// A browser never puts credentials on a preflight, the CORS specification
// forbidding it, so a server that authenticates must answer one anyway or
// --cors-origin would mean nothing on it.
func TestCORSPreflightPassesAuthentication(t *testing.T) {
	opts := serveOptions{
		addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb",
		corsOrigins: []string{"https://ui.example.com"},
	}
	h, be := newServeHandlerOn(t, opts)
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)

	resp, _ := get(t, h, http.MethodOptions, "/api/issues",
		"Origin", "https://ui.example.com",
		"Access-Control-Request-Method", "POST",
		"Access-Control-Request-Headers", "authorization, content-type")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "https://ui.example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))

	// The request the preflight asked about is authenticated like any other,
	// so the exemption opens nothing.
	resp, _ = get(t, h, http.MethodGet, "/api/issues", "Origin", "https://ui.example.com")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// And an OPTIONS that is not a preflight gets no exemption either.
	resp, _ = get(t, h, http.MethodOptions, "/api/issues", "Origin", "https://ui.example.com")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
