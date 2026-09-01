package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/handler"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/openapi"
	"github.com/tofutools/awb/internal/storage"
)

type api struct {
	t      *testing.T
	server *httptest.Server
	be     *local.Backend
	// blobs is where attachment content is stored, for the tests that look at
	// the files rather than at the rows.
	blobs string
}

func newAPI(t *testing.T) *api {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	be := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	_, err = be.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "awb", Name: "Agent Work Board"})
	require.NoError(t, err)

	// The same server serve builds, over the same document, so what these tests
	// exercise is the whole surface: the generated router, decoders and
	// validators as well as the handler behind them.
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	operations, err := openapi.New(raw).Operations()
	require.NoError(t, err)

	apiServer, err := handler.NewServer(func(context.Context) backend.Backend { return be }, operations)
	require.NoError(t, err)

	server := httptest.NewServer(handler.NoStore(apiServer))
	t.Cleanup(server.Close)
	return &api{t: t, server: server, be: be, blobs: filepath.Join(dir, "attachments")}
}

// do performs a request and returns the response with its body read.
func (a *api) do(method, path, body string, headers ...string) (*http.Response, string) {
	a.t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(a.t.Context(), method, a.server.URL+path, reader)
	require.NoError(a.t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	resp, err := a.server.Client().Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close() //nolint:errcheck // the body is being read out

	data, err := io.ReadAll(resp.Body)
	require.NoError(a.t, err)
	return resp, string(data)
}

// createIssue posts an issue and returns it.
func (a *api) createIssue(body string) domain.Issue {
	a.t.Helper()
	resp, payload := a.do(http.MethodPost, "/api/issues", body)
	require.Equal(a.t, http.StatusCreated, resp.StatusCode, payload)

	var issue domain.Issue
	require.NoError(a.t, json.Unmarshal([]byte(payload), &issue))
	return issue
}

func TestCreateIssue(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodPost, "/api/issues",
		`{"workspace":"awb","title":"Parser crashes","type":"bug","priority":1,"labels":["parser"]}`)

	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issue))
	assert.Equal(t, "Parser crashes", issue.Title)
	assert.Equal(t, domain.TypeBug, issue.Type)

	// 201 carries the new object and a Location header naming it.
	assert.Equal(t, "/api/issues/"+issue.ID, resp.Header.Get("Location"))
	// Every response whose body is one Issue carries the ETag for that version.
	assert.Equal(t, backend.ETag(issue.UpdatedAt), resp.Header.Get("ETag"))
	// No response under /api/ is cacheable.
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

func TestSearchNavigation(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Keyboard Command Palette"}`)

	resp, payload := a.do(http.MethodGet, "/api/navigation?q=command+pal&limit=2", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var results struct {
		Issues     []domain.Issue     `json:"issues"`
		Workspaces []domain.Workspace `json:"workspaces"`
		Users      []domain.User      `json:"users"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &results))
	require.Len(t, results.Issues, 1)
	assert.Equal(t, issue.ID, results.Issues[0].ID)
	assert.Empty(t, results.Workspaces)
	assert.Empty(t, results.Users)

	resp, payload = a.do(http.MethodGet, "/api/navigation?q=palette&limit=21", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
}

func TestWorkspacePreferencesRecoverIgnoredWorkspaces(t *testing.T) {
	a := newAPI(t)
	_, err := a.be.CreateUser(t.Context(), backend.UserCreate{Name: "mikael", Password: "hunter2"})
	require.NoError(t, err)
	_, err = a.be.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "web", Name: "Web UI"})
	require.NoError(t, err)

	resp, payload := a.do(http.MethodPut, "/api/preferences/workspaces/web", `{"ignored":true}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Contains(t, payload, `"ignored":true`)

	resp, payload = a.do(http.MethodGet, "/api/workspaces/web", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, payload)
	resp, payload = a.do(http.MethodGet, "/api/preferences/workspaces", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var preferences []domain.WorkspacePreference
	require.NoError(t, json.Unmarshal([]byte(payload), &preferences))
	require.Len(t, preferences, 2)
	assert.Equal(t, "web", preferences[1].Workspace.Key)
	assert.True(t, preferences[1].Ignored)

	resp, payload = a.do(http.MethodPut, "/api/preferences/workspaces/web", `{"ignored":false}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	resp, payload = a.do(http.MethodGet, "/api/workspaces/web", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
}

func TestBoardViewsAndPagedBoard(t *testing.T) {
	a := newAPI(t)
	for _, title := range []string{"first", "second"} {
		a.createIssue(`{"workspace":"awb","title":"` + title + `","labels":["release"]}`)
	}
	resp, payload := a.do(http.MethodPost, "/api/board-views",
		`{"name":"Release","shared":true,"all_workspaces":false,"workspaces":["awb"],"labels":["release"],"priority_max":4}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	var view domain.BoardView
	require.NoError(t, json.Unmarshal([]byte(payload), &view))
	assert.Equal(t, "/api/board-views/"+view.ID, resp.Header.Get("Location"))
	assert.NotEmpty(t, resp.Header.Get("ETag"))

	resp, payload = a.do(http.MethodGet, "/api/board-views", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var views []domain.BoardView
	require.NoError(t, json.Unmarshal([]byte(payload), &views))
	require.Len(t, views, 1)

	resp, payload = a.do(http.MethodGet, "/api/boards/"+view.ID+"?lane-limit=1&card-limit=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var board domain.Board
	require.NoError(t, json.Unmarshal([]byte(payload), &board))
	assert.Equal(t, 1, board.LaneTotal)
	require.Len(t, board.Lanes, 1)
	assert.Equal(t, 2, board.Lanes[0].Columns[0].Total)
	assert.Len(t, board.Lanes[0].Columns[0].Issues, 1)
	resp, payload = a.do(http.MethodGet, "/api/boards/"+view.ID+"?card-limit=51", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)

	readBack := view
	readBack.Name = "Renamed"
	patch, err := json.Marshal(readBack)
	require.NoError(t, err)
	resp, payload = a.do(http.MethodPatch, "/api/board-views/"+view.ID, string(patch), "If-Match", backend.ETag(view.UpdatedAt))
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Contains(t, payload, `"name":"Renamed"`)
}

// Nothing beyond the recognised fields is accepted: they are rejected rather
// than ignored.
func TestCreateIssueRejectsUnknownFields(t *testing.T) {
	a := newAPI(t)
	for _, body := range []string{
		`{"workspace":"awb","title":"t","id":"awb-aaaaaa"}`,
		`{"workspace":"awb","title":"t","status":"closed"}`,
		`{"workspace":"awb","title":"t","close_reason":"x"}`,
		`{"workspace":"awb","title":"t","created_at":"2026-01-01T00:00:00.000Z"}`,
		`{"workspace":"awb","title":"t","blocked":true}`,
		`{"workspace":"awb","title":"t","nonsense":1}`,
		`{"workspace":"awb","title":"t","relations":[{"type":"related","other":"x","direction":"out"}]}`,
	} {
		resp, payload := a.do(http.MethodPost, "/api/issues", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, `"error"`, body)
	}
}

// A field present with a JSON null is a type error: there is no third state to
// encode.
func TestNullIsRejected(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/issues", `{"workspace":"awb","title":null}`},
		{http.MethodPatch, "/api/issues/" + issue.ID, `{"description":null}`},
		{http.MethodPost, "/api/issues/" + issue.ID + "/close", `{"reason":null}`},
	} {
		resp, payload := a.do(req.method, req.path, req.body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, req.body)
		assert.Contains(t, payload, "null", req.body)
	}
}

// A JSON escape denoting an unpaired surrogate is rejected rather than
// repaired into U+FFFD.
func TestUnpairedSurrogateIsRejected(t *testing.T) {
	a := newAPI(t)

	resp, payload := a.do(http.MethodPost, "/api/issues", `{"workspace":"awb","title":"a\ud800b"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, payload, "surrogate")

	// A properly paired surrogate is an ordinary character and is accepted.
	resp, payload = a.do(http.MethodPost, "/api/issues", `{"workspace":"awb","title":"a😀b"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issue))
	assert.Equal(t, "a\U0001F600b", issue.Title)
}

// A body carrying no JSON content type is a 415: that is how the client asked
// rather than what it asked for.
func TestUnsupportedMediaType(t *testing.T) {
	a := newAPI(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		a.server.URL+"/api/issues", strings.NewReader(`{"workspace":"awb","title":"t"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")

	resp, err := a.server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)
}

// A method the path does not support is a 405, which ServeMux answers itself.
func TestMethodNotAllowed(t *testing.T) {
	a := newAPI(t)
	resp, _ := a.do(http.MethodPut, "/api/issues", "")
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// PATCH takes only what awb update can change; the transitions are their own
// endpoints.
func TestPatchIssue(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t","labels":["a","b"]}`)

	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"renamed"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var updated domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &updated))
	assert.Equal(t, "renamed", updated.Title)

	// The unchangeable fields are ignored when they equal what is stored, so a UI
	// can send back the object it read.
	roundTrip, err := json.Marshal(updated)
	require.NoError(t, err)
	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, string(roundTrip))
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)

	// And rejected when they differ.
	for _, body := range []string{
		`{"status":"closed"}`,
		`{"assignees":["claude-1"]}`,
		`{"labels":["a"]}`,
	} {
		resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, "cannot be changed", body)
	}

	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"close_reason":"done"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, payload, "unexpected field")
}

// Labels are compared as the sorted form, which is what a client read.
func TestPatchAcceptsLabelsInAnyOrder(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t","labels":["a","b"]}`)

	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"labels":["b","a"]}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
}

// The ETag/If-Match handshake.
func TestConditionalEdit(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	resp, _ := a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	tag := resp.Header.Get("ETag")
	require.NotEmpty(t, tag)
	assert.Equal(t, `"`+issue.UpdatedAt+`"`, tag, "a strong tag of the updated_at")

	// A matching tag succeeds and returns the new one.
	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"first"}`, "If-Match", tag)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	fresh := resp.Header.Get("ETag")
	assert.NotEqual(t, tag, fresh)

	// The stale one is a 412.
	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"second"}`, "If-Match", tag)
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
	assert.Contains(t, payload, `"error"`)

	// The fresh one works, so a client need not repeat the GET.
	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"third"}`, "If-Match", fresh)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)

	// Omitting it is last-write-wins.
	resp, _ = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"fourth"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// A relation added meanwhile does not move updated_at, so a conditional edit
// does not fail on it.
func TestETagSurvivesARelation(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)
	other := a.createIssue(`{"workspace":"awb","title":"other"}`)
	tag := backend.ETag(issue.UpdatedAt)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/relations",
		`{"type":"blocked-by","other":"`+other.ID+`"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"renamed"}`, "If-Match", tag)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload,
		"the tag guards the issue's own stored fields")
}

// A comment is issue activity and moves updated_at, so a form based on the
// previous version must reload before it edits the issue.
func TestCommentInvalidatesIssueETag(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)
	tag := backend.ETag(issue.UpdatedAt)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/comments",
		`{"body":"new information"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID,
		`{"title":"renamed"}`, "If-Match", tag)
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode, payload)
}

// Attachment changes move updated_at, so both upload and deletion invalidate
// a form based on the preceding issue version.
func TestAttachmentChangesInvalidateIssueETag(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)
	beforeAdd := backend.ETag(issue.UpdatedAt)

	attachment := a.attach(issue.ID, "evidence.txt", "evidence")
	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID,
		`{"title":"after add"}`, "If-Match", beforeAdd)
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	beforeDelete := resp.Header.Get("ETag")

	resp, payload = a.do(http.MethodDelete, attachmentPath(&attachment), "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID,
		`{"title":"after delete"}`, "If-Match", beforeDelete)
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode, payload)
}

// A tree aggregates many issues and no one version tags it.
func TestTreeCarriesNoETag(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodGet, "/api/issues/"+issue.ID+"/tree", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
	assert.Contains(t, payload, `"children"`)
}

// A delete answers with the object as it was, and carries no ETag.
func TestDeleteCarriesNoETag(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodDelete, "/api/issues/"+issue.ID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
	assert.Contains(t, payload, issue.ID)

	resp, _ = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// assignee may be omitted, in which case the request's identity is used.
func TestClaimDefaultsToTheRequestIdentity(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var claimed domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &claimed))
	assert.Equal(t, []string{"mikael"}, claimed.Assignees)
}

func TestMoveIssueOverHTTP(t *testing.T) {
	a := newAPI(t)
	epic := a.createIssue(`{"workspace":"awb","title":"Epic","type":"epic"}`)
	issue := a.createIssue(`{"workspace":"awb","title":"move me"}`)
	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/move",
		`{"epic":"`+epic.ID+`","status":"in_progress"}`, "If-Match", backend.ETag(issue.UpdatedAt))
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var moved domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &moved))
	assert.Equal(t, issue.ID, moved.ID)
	assert.Equal(t, "awb", moved.Workspace)
	assert.Equal(t, domain.StatusInProgress, moved.Status)
	assert.Positive(t, moved.Order)
	assert.Contains(t, moved.Relations, domain.Relation{Type: domain.RelHasParent, Other: epic.ID, Direction: domain.DirectionOut})

	resp, payload = a.do(http.MethodPost, "/api/issues/"+issue.ID+"/move",
		`{"workspace":"web","status":"open"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, `unexpected field \"workspace\"`, "workspace movement is absent from the wire contract")
}

func TestMultipleAssigneesRoundTripThroughTheAPI(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t","assignees":["alice","bob"]}`)
	assert.Equal(t, []string{"alice", "bob"}, issue.Assignees)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim",
		`{"assignee":"carol"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var joined domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &joined))
	assert.Equal(t, []string{"alice", "bob", "carol"}, joined.Assignees)

	resp, payload = a.do(http.MethodPost, "/api/issues/"+issue.ID+"/release",
		`{"assignee":"bob"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var left domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &left))
	assert.Equal(t, []string{"alice", "carol"}, left.Assignees)
}

// Every mutating endpoint answers with the resulting object, the label and
// relation removals included: a client that renders the response must see the
// change.
func TestMutationsReturnTheObject(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t","labels":["x"]}`)
	other := a.createIssue(`{"workspace":"awb","title":"other"}`)

	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/api/issues/" + issue.ID + "/labels", `{"label":"y"}`},
		{http.MethodDelete, "/api/issues/" + issue.ID + "/labels?label=x", ""},
		{http.MethodPost, "/api/issues/" + issue.ID + "/relations",
			`{"type":"related","other":"` + other.ID + `"}`},
		{http.MethodDelete, "/api/issues/" + issue.ID + "/relations/related/" + other.ID, ""},
		{http.MethodPost, "/api/issues/" + issue.ID + "/close", `{"reason":"done"}`},
		{http.MethodPost, "/api/issues/" + issue.ID + "/reopen", ""},
	}
	for _, tc := range cases {
		resp, payload := a.do(tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusOK, resp.StatusCode, tc.path+" "+payload)

		var result domain.Issue
		require.NoError(t, json.Unmarshal([]byte(payload), &result), tc.path)
		assert.Equal(t, issue.ID, result.ID, tc.path)
		assert.NotEmpty(t, resp.Header.Get("ETag"), tc.path)
	}
}

// Paging, and the unpaged total X-Total-Count carries.
func TestPaging(t *testing.T) {
	a := newAPI(t)
	for range 5 {
		a.createIssue(`{"workspace":"awb","title":"t"}`)
	}

	resp, payload := a.do(http.MethodGet, "/api/issues?limit=2&offset=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "5", resp.Header.Get("X-Total-Count"))

	var issues []domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	assert.Len(t, issues, 2)

	// limit=0 returns no rows while preserving the total.
	resp, payload = a.do(http.MethodGet, "/api/issues?limit=0", "")
	assert.Equal(t, "5", resp.Header.Get("X-Total-Count"))
	assert.Equal(t, "[]", payload)

	// There is no default limit: omitting it returns every row.
	resp, payload = a.do(http.MethodGet, "/api/issues", "")
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	assert.Len(t, issues, 5)
	assert.Equal(t, "5", resp.Header.Get("X-Total-Count"))

	// Negative values are refused.
	for _, query := range []string{"limit=-1", "offset=-1", "limit=x"} {
		resp, _ := a.do(http.MethodGet, "/api/issues?"+query, "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, query)
	}
}

func TestPagingAppliesAfterIssueSorting(t *testing.T) {
	a := newAPI(t)
	a.createIssue(`{"workspace":"awb","title":"low","priority":4}`)
	a.createIssue(`{"workspace":"awb","title":"high","priority":0}`)

	resp, payload := a.do(http.MethodGet, "/api/issues?sort=-priority&limit=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var issues []domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	require.Len(t, issues, 1)
	assert.Equal(t, 4, issues[0].Priority)
	assert.Equal(t, "2", resp.Header.Get("X-Total-Count"))

	resp, payload = a.do(http.MethodGet, "/api/issues?filter=high&limit=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	require.Len(t, issues, 1)
	assert.Equal(t, "high", issues[0].Title)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))

	resp, payload = a.do(http.MethodGet, "/api/issues?filter=missing", "")
	assert.Equal(t, "0", resp.Header.Get("X-Total-Count"))
	assert.Equal(t, "[]", payload)
}

func TestWorkspacePagingAppliesAfterSorting(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodPost, "/api/workspaces", `{"key":"web"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	a.createIssue(`{"workspace":"awb","title":"active"}`)

	resp, payload = a.do(http.MethodGet, "/api/workspaces?sort=-active&limit=1", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var workspaces []domain.Workspace
	require.NoError(t, json.Unmarshal([]byte(payload), &workspaces))
	require.Len(t, workspaces, 1)
	assert.Equal(t, "awb", workspaces[0].Key)
	assert.Equal(t, "2", resp.Header.Get("X-Total-Count"))

	resp, payload = a.do(http.MethodGet, "/api/workspaces?filter=web", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &workspaces))
	require.Len(t, workspaces, 1)
	assert.Equal(t, "web", workspaces[0].Key)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))
}

// The endpoints that fix a filter for themselves reject the corresponding
// parameters.
func TestRejectedParameters(t *testing.T) {
	a := newAPI(t)
	cases := []string{
		"/api/ready?status=open",
		"/api/ready?include-closed=true",
		"/api/ready?assignee=mikael",
		"/api/ready?unassigned=true",
		"/api/blocked?status=open",
		"/api/blocked?include-closed=true",
		"/api/issues?q=x",
		"/api/issues?sort=relevance",
		"/api/labels?sort=value",
		"/api/assignees?sort=value",
	}
	for _, path := range cases {
		resp, payload := a.do(http.MethodGet, path, "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, path+" "+payload)
	}
}

// Search carries its terms as repeated q, and a request with no q is a 400.
func TestSearch(t *testing.T) {
	a := newAPI(t)
	a.createIssue(`{"workspace":"awb","title":"Parser crashes on empty input"}`)
	a.createIssue(`{"workspace":"awb","title":"Unrelated"}`)

	resp, payload := a.do(http.MethodGet, "/api/search?q=parser", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var issues []domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	assert.Len(t, issues, 1)

	// Several terms are ANDed.
	_, payload = a.do(http.MethodGet, "/api/search?q=parser&q=crashes", "")
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	assert.Len(t, issues, 1)

	resp, _ = a.do(http.MethodGet, "/api/search", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// A term the tokenizer reduces to nothing is a 400 too.
	resp, _ = a.do(http.MethodGet, "/api/search?q=-", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// No input can produce a query syntax error: FTS5 operators and syntax
	// characters inside a real term stay literal.
	for _, term := range []string{"AND", "OR", "NOT", "NEAR", "a%2A", "a%22b", "title%3Ax", "a%28b"} {
		resp, payload := a.do(http.MethodGet, "/api/search?q="+term, "")
		assert.Equal(t, http.StatusOK, resp.StatusCode, term+" "+payload)
	}
}

func TestIssueSuggestionsSearchIDsAndTitles(t *testing.T) {
	a := newAPI(t)
	created := a.createIssue(`{"workspace":"awb","title":"Parser crashes on empty input"}`)
	a.createIssue(`{"workspace":"awb","title":"Unrelated"}`)

	resp, payload := a.do(http.MethodGet, "/api/issues/suggestions?q=parser&limit=8", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var issues []domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	require.Len(t, issues, 1)
	assert.Equal(t, created.ID, issues[0].ID)

	resp, payload = a.do(http.MethodGet,
		"/api/issues/suggestions?q="+created.ID[:len(created.ID)-2], "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	require.NoError(t, json.Unmarshal([]byte(payload), &issues))
	require.Len(t, issues, 1)
	assert.Equal(t, created.ID, issues[0].ID)

	resp, _ = a.do(http.MethodGet, "/api/issues/suggestions", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// The facet endpoints honour the selection parameters, the facet's own
// included, so a UI can narrow progressively.
func TestFacets(t *testing.T) {
	a := newAPI(t)
	a.createIssue(`{"workspace":"awb","title":"a","labels":["parser","frontend"]}`)
	a.createIssue(`{"workspace":"awb","title":"b","labels":["parser"]}`)
	resp, payload := a.do(http.MethodGet, "/api/labels", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.JSONEq(t, `[{"value":"frontend","count":1},{"value":"parser","count":2}]`, payload)
	assert.Equal(t, "2", resp.Header.Get("X-Total-Count"))

	resp, payload = a.do(http.MethodGet, "/api/labels?label=frontend", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `[{"value":"frontend","count":1},{"value":"parser","count":1}]`, payload)

	// count is the same whatever page it appears on.
	resp, payload = a.do(http.MethodGet, "/api/labels?limit=1&offset=1", "")
	assert.Equal(t, "2", resp.Header.Get("X-Total-Count"))
	assert.JSONEq(t, `[{"value":"parser","count":2}]`, payload)

	// There is no row for the empty assignee.
	_, payload = a.do(http.MethodGet, "/api/assignees", "")
	assert.Equal(t, "[]", payload)

	ready := a.createIssue(`{"workspace":"awb","title":"Needle ready","labels":["ready-label"]}`)
	blocked := a.createIssue(`{"workspace":"awb","title":"Needle blocked","labels":["blocked-label"]}`)
	blocker := a.createIssue(`{"workspace":"awb","title":"Prerequisite"}`)
	resp, payload = a.do(http.MethodPost, "/api/issues/"+blocked.ID+"/relations",
		`{"type":"blocked-by","other":"`+blocker.ID+`"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet,
		"/api/labels?filter=needle&status=open&unassigned=true&readiness=ready", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.JSONEq(t, `[{"value":"ready-label","count":1}]`, payload,
		"a matching blocked issue does not contribute to Ready's facets")
	assert.NotEmpty(t, ready.ID)
}

func TestIdentity(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodGet, "/api/identity", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"identity":"mikael","may_manage_users":true}`, payload)
}

func TestWorkspaces(t *testing.T) {
	a := newAPI(t)

	resp, payload := a.do(http.MethodPost, "/api/workspaces", `{"key":"web"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	assert.Equal(t, "/api/workspaces/web", resp.Header.Get("Location"))

	var workspace domain.Workspace
	require.NoError(t, json.Unmarshal([]byte(payload), &workspace))
	assert.Equal(t, "web", workspace.Name, "the name defaults to the key")
	assert.NotEmpty(t, resp.Header.Get("ETag"))

	// A key may appear in a PATCH but may not change.
	resp, payload = a.do(http.MethodPatch, "/api/workspaces/web", `{"key":"web","name":"Web UI"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, _ = a.do(http.MethodPatch, "/api/workspaces/web", `{"key":"other"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// The derived fields are ignored whatever they say.
	resp, payload = a.do(http.MethodPatch, "/api/workspaces/web",
		`{"active_issues":99,"created_at":"2000-01-01T00:00:00.000Z"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	// A duplicate is a conflict.
	resp, _ = a.do(http.MethodPost, "/api/workspaces", `{"key":"web"}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// Deletion refuses while the workspace holds issues, and cascade is a boolean
	// query parameter.
	a.createIssue(`{"workspace":"web","title":"t"}`)
	resp, _ = a.do(http.MethodDelete, "/api/workspaces/web", "")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, payload = a.do(http.MethodDelete, "/api/workspaces/web?cascade=true", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
}

func TestProjectVocabularyIsNotAnAPICompatibilitySurface(t *testing.T) {
	a := newAPI(t)

	resp, _ := a.do(http.MethodGet, "/api/projects", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = a.do(http.MethodGet, "/api/issues?project=awb", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = a.do(http.MethodPost, "/api/issues", `{"project":"awb","title":"old client"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = a.do(http.MethodPost, "/api/board-views",
		`{"name":"old client","all_projects":false,"projects":["awb"],"priority_max":4}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// The status taxonomy.
func TestErrorStatuses(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"unknown issue", http.MethodGet, "/api/issues/awb-ffffff", "", http.StatusNotFound},
		{"unknown workspace", http.MethodGet, "/api/workspaces/nosuch", "", http.StatusNotFound},
		{"filter naming a missing workspace", http.MethodGet, "/api/issues?workspace=nosuch", "",
			http.StatusNotFound},
		{"bad enum", http.MethodPost, "/api/issues", `{"workspace":"awb","title":"t","type":"nonsense"}`,
			http.StatusBadRequest},
		{"bad priority", http.MethodPost, "/api/issues", `{"workspace":"awb","title":"t","priority":9}`,
			http.StatusBadRequest},
		{"bad label", http.MethodPost, "/api/issues", `{"workspace":"awb","title":"t","labels":["Bad"]}`,
			http.StatusBadRequest},
		{"empty title", http.MethodPost, "/api/issues", `{"workspace":"awb","title":"  "}`,
			http.StatusBadRequest},
		{"malformed JSON", http.MethodPost, "/api/issues", `{`, http.StatusBadRequest},
		{"self relation", http.MethodPost, "/api/issues/" + issue.ID + "/relations",
			`{"type":"blocked-by","other":"` + issue.ID + `"}`, http.StatusConflict},
		{"two parents", http.MethodPost, "/api/issues",
			`{"workspace":"awb","title":"t","relations":[` +
				`{"type":"has-parent","other":"` + issue.ID + `"},` +
				`{"type":"has-parent","other":"` + issue.ID + `"}]}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, payload := a.do(tc.method, tc.path, tc.body)
			assert.Equal(t, tc.want, resp.StatusCode, payload)
			assert.Contains(t, payload, `"error"`)
		})
	}
}

// An {id} path segment accepts an unambiguous prefix or a bare hash, so the
// CLI needs no extra round trip in remote mode.
func TestPathAcceptsPrefixes(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)
	_, hash, ok := domain.SplitID(issue.ID)
	require.True(t, ok)

	for _, ref := range []string{issue.ID, hash, hash[:3]} {
		resp, payload := a.do(http.MethodGet, "/api/issues/"+ref, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, ref+" "+payload)

		var got domain.Issue
		require.NoError(t, json.Unmarshal([]byte(payload), &got))
		assert.Equal(t, issue.ID, got.ID, ref)
	}
}

// Every listing endpoint returns [] rather than null when nothing matches.
func TestEmptyArrays(t *testing.T) {
	a := newAPI(t)
	for _, path := range []string{
		"/api/issues", "/api/ready", "/api/blocked", "/api/search?q=nothing",
		"/api/labels", "/api/assignees",
	} {
		resp, payload := a.do(http.MethodGet, path, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Equal(t, "[]", payload, path)
		assert.Equal(t, "0", resp.Header.Get("X-Total-Count"), path)
	}
}

// A body without a JSON content type is a 415, and a missing header is not a
// JSON content type either — a body claiming nothing claims nothing useful.
// Regression: the check used to run only when the header was present, so an
// untyped body was accepted and could create an issue.
func TestBodyWithoutContentTypeIsRejected(t *testing.T) {
	a := newAPI(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		a.server.URL+"/api/issues", strings.NewReader(`{"workspace":"awb","title":"untyped"}`))
	require.NoError(t, err)
	req.Header.Del("Content-Type")

	resp, err := a.server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode)

	// And nothing was created.
	_, payload := a.do(http.MethodGet, "/api/issues", "")
	assert.Equal(t, "[]", payload)
}

// A request that sends no body at all is never asked to describe one.
func TestNoBodyNeedsNoContentType(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	for _, path := range []string{"/claim", "/release", "/close", "/reopen"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			a.server.URL+"/api/issues/"+issue.ID+path, nil)
		require.NoError(t, err)

		resp, err := a.server.Client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
}

// reopen takes no body, and says so rather than ignoring one. Regression: it
// used never to read the body, so arbitrary bytes, unknown fields and nulls all
// passed silently.
func TestReopenRefusesABody(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)
	_, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/close", `{}`)
	require.NotEmpty(t, payload)

	for _, body := range []string{`{"nonsense":1}`, `{"reason":"x"}`, `{}`} {
		resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/reopen", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, "no request body", body)
	}

	// Still closed: a refused request changed nothing.
	_, payload = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	assert.Contains(t, payload, `"status":"closed"`)

	// And with no body it works.
	resp, _ := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/reopen", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// The "may appear but may not change" comparison happens inside the write
// transaction. This cannot observe the race directly, but it pins the rule to
// the layer that can enforce it: the refusal must survive the value being
// carried through the backend rather than checked in the adapter.
func TestUnchangeableFieldsAreRefusedByTheBackend(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t","labels":["a","b"]}`)

	// Claim it, so the stored status and assignees differ from what a client
	// that read it earlier would send back.
	_, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", `{"assignee":"claude-1"}`)
	require.Contains(t, payload, "in_progress")

	// A patch carrying the stale values is refused, not silently applied.
	for _, body := range []string{
		`{"title":"x","status":"open"}`,
		`{"title":"x","assignees":[]}`,
		`{"title":"x","labels":["a"]}`,
	} {
		resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, "cannot be changed", body)
	}

	// The title never changed.
	_, payload = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	assert.Contains(t, payload, `"title":"t"`)

	// Carrying the current values is fine, so a UI can send back what it read.
	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID,
		`{"title":"renamed","status":"in_progress","assignees":["claude-1"],"labels":["b","a"]}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Contains(t, payload, `"title":"renamed"`)
}

// The derived and immutable fields may be carried and their values ignored, so
// that a UI can send back the object it read — but ignored is not unchecked:
// each is validated against the schema declared for it, so a caller sending
// something it never received is told rather than quietly obeyed in part.
func TestIgnoredFieldsAreIgnoredButStillChecked(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	// Values a caller really could have read, stale or not, are ignored.
	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID,
		`{"title":"renamed","id":"awb-000000","workspace":"awb",`+
			`"created_at":"2020-01-01T00:00:00.000Z","updated_at":"2020-01-01T00:00:00.000Z",`+
			`"blocked":true,"blockers":["awb-000000"],`+
			`"relations":[{"type":"related","other":"awb-000000","direction":"out"}],`+
			`"links":[{"text":"a","url":"b"}]}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Contains(t, payload, `"title":"renamed"`)
	assert.Contains(t, payload, `"id":"`+issue.ID+`"`)
	assert.Contains(t, payload, `"blocked":false`)
	assert.NotContains(t, payload, "2020-01-01")

	// Values no caller could have read are refused.
	for _, body := range []string{
		`{"created_at":"whenever"}`,
		`{"workspace":"NOT A KEY"}`,
		`{"relations":[{"type":"related","other":"x","direction":"sideways"}]}`,
	} {
		resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, `"error"`, body)
	}
}

// A parameter present with an empty value is refused rather than read as
// absent. It is what a form submits for a control nobody touched, and reading
// it as absent would make "?limit=" mean something the document does not say.
func TestEmptyParameterValuesAreRefused(t *testing.T) {
	a := newAPI(t)

	for _, query := range []string{
		"?limit=", "?offset=", "?include-closed=", "?unassigned=",
		"?priority-max=", "?sort=", "?status=", "?type=",
	} {
		resp, payload := a.do(http.MethodGet, "/api/issues"+query, "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, query)
		assert.Contains(t, payload, `"error"`, query)
	}
}

// A body of whitespace is still a body: it has to declare what it is, and an
// endpoint that takes no body has still been sent one. Regression: presence was
// decided by the trimmed length, so a whitespace body slipped past both rules —
// an untyped one claimed an issue and a reopen with one went through.
func TestWhitespaceBodyCountsAsABody(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)
	_, _ = a.do(http.MethodPost, "/api/issues/"+issue.ID+"/close", `{}`)

	// It carries no JSON value, so there is nothing to read out of it...
	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, "   ")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, "holds no JSON value")

	// ...but it was carried, so it must declare what it is.
	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, "/api/issues/" + issue.ID},
		{http.MethodPost, "/api/issues/" + issue.ID + "/claim"},
		{http.MethodPost, "/api/issues/" + issue.ID + "/close"},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tc.method,
			a.server.URL+tc.path, strings.NewReader("  \n "))
		require.NoError(t, err)
		req.Header.Del("Content-Type")

		resp, err := a.server.Client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode, tc.path)
	}

	// And an endpoint that takes no body refuses it whatever it holds.
	for _, body := range []string{"   ", "\n", "\t"} {
		resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/reopen", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%q", body)
		assert.Contains(t, payload, "no request body")
	}

	// Nothing above changed the issue.
	_, payload = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	assert.Contains(t, payload, `"status":"closed"`)
	assert.Contains(t, payload, `"assignees":[]`)
}

// A body that holds no JSON value is refused even where the body is optional:
// optional means a body may be omitted, not that one may say nothing. Sending
// none is what says nothing, and that still claims the issue for the request's
// identity.
func TestWhitespaceBodyIsRefusedWhereABodyIsOptional(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", "  \n ")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, "holds no JSON value")

	resp, payload = a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var claimed domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &claimed))
	assert.Equal(t, []string{"mikael"}, claimed.Assignees, "the request's identity, as with no body")
}

// The attachment endpoints. The upload's body is the file's bytes and
// everything else about it is a query parameter, which is what leaves
// Content-Type free to describe the body on the wire.

// upload posts content and returns the response with its body read.
func (a *api) upload(issueID, query, contentType, content string) (*http.Response, string) {
	a.t.Helper()
	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodPost,
		a.server.URL+"/api/issues/"+issueID+"/attachments"+query, strings.NewReader(content))
	require.NoError(a.t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := a.server.Client().Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close() //nolint:errcheck // the body is being read out

	data, err := io.ReadAll(resp.Body)
	require.NoError(a.t, err)
	return resp, string(data)
}

// attachmentPath addresses one attachment the way the API does: the issue it
// belongs to and its name, escaped.
func attachmentPath(a *domain.Attachment) string {
	return "/api/issues/" + url.PathEscape(a.Issue) + "/attachments/" + url.PathEscape(a.Name)
}

// attach uploads one file and returns the attachment.
func (a *api) attach(issueID, name, content string) domain.Attachment {
	a.t.Helper()
	resp, payload := a.upload(issueID, "?name="+url.QueryEscape(name),
		"application/octet-stream", content)
	require.Equal(a.t, http.StatusCreated, resp.StatusCode, payload)

	var attachment domain.Attachment
	require.NoError(a.t, json.Unmarshal([]byte(payload), &attachment))
	return attachment
}

func TestAddAttachment(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "?name=trace.txt", "application/octet-stream", "boom\n")
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var attachment domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &attachment))
	assert.Equal(t, issue.ID, attachment.Issue)
	assert.Equal(t, "trace.txt", attachment.Name)
	assert.Equal(t, "text/plain; charset=utf-8", attachment.ContentType,
		"sniffed from the content when the caller states none")
	assert.EqualValues(t, 5, attachment.Size)

	// 201 carries the new object and a Location header naming it. It carries no
	// ETag: an attachment is immutable and has no version to guard.
	assert.Equal(t, "/api/issues/"+issue.ID+"/attachments/trace.txt",
		resp.Header.Get("Location"))
	assert.Empty(t, resp.Header.Get("ETag"))
}

// The stated content type is what is recorded, exactly as it arrived.
func TestAddAttachmentWithAStatedContentType(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID,
		"?name=notes.md&content-type="+url.QueryEscape("text/markdown; charset=utf-8"),
		"application/octet-stream", "# Notes\n")
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var attachment domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &attachment))
	assert.Equal(t, "text/markdown; charset=utf-8", attachment.ContentType)
}

// The upload declares application/octet-stream, so a body claiming JSON is the
// 415 the rule describes — and the rule is read from the document, so it says
// what this endpoint actually accepts rather than what most of them do.
func TestAddAttachmentRefusesTheWrongContentType(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "?name=trace.txt", "application/json", "boom\n")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode, payload)
	assert.Contains(t, payload, "application/octet-stream")

	resp, payload = a.upload(issue.ID, "?name=trace.txt", "", "boom\n")
	assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode,
		"a body claiming nothing claims nothing useful")
	assert.Contains(t, payload, "application/octet-stream")
}

// A JSON endpoint still names JSON, which is what says the rule follows the
// document rather than the one endpoint that differs.
func TestJSONEndpointsStillDemandJSON(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodPost, "/api/issues", "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		a.server.URL+"/api/issues", strings.NewReader(`{"workspace":"awb","title":"t"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/plain")
	got, err := a.server.Client().Do(req)
	require.NoError(t, err)
	defer got.Body.Close() //nolint:errcheck // the body is being read out
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnsupportedMediaType, got.StatusCode)
	assert.Contains(t, string(body), "application/json")
}

// name is required, and a name that is a path is refused rather than stripped.
func TestAddAttachmentRefusesABadName(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "", "application/octet-stream", "boom\n")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)

	resp, payload = a.upload(issue.ID, "?name="+url.QueryEscape("../escape.txt"),
		"application/octet-stream", "boom\n")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
}

// A query parameter the endpoint does not declare is refused rather than
// ignored, exactly as everywhere else.
func TestAddAttachmentRefusesAnUndeclaredParameter(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "?name=trace.txt&sort=priority",
		"application/octet-stream", "boom\n")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, "sort")
}

func TestListAndGetAttachments(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "boom\n")

	resp, payload := a.do(http.MethodGet, "/api/issues/"+issue.ID+"/attachments", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))

	var listed []domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, attachment, listed[0])

	resp, payload = a.do(http.MethodGet, attachmentPath(&attachment), "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var one domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &one))
	assert.Equal(t, attachment, one)

	// The same array is on the issue itself.
	resp, payload = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var read domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &read))
	assert.Equal(t, listed, read.Attachments)
}

// Content is always served as an octet-stream to be saved, whatever the
// recorded content type says: uploads come back from the same origin as the
// UI, and a browser must not be invited to render one there.
func TestGetAttachmentContent(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "?name=page.html&content-type=text%2Fhtml",
		"application/octet-stream", "<script>alert(1)</script>")
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	var attachment domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &attachment))
	assert.Equal(t, "text/html", attachment.ContentType, "the metadata records what it is")

	resp, payload = a.do(http.MethodGet, attachmentPath(&attachment)+"/content", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "<script>alert(1)</script>", payload, "the bytes exactly as uploaded")
	assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, `attachment; filename=page.html`, resp.Header.Get("Content-Disposition"))

	// The length is stated, so a client can show progress rather than reading
	// an unbounded stream to its end. It is the recorded size, and it is the
	// length of what actually arrived.
	assert.Equal(t, strconv.FormatInt(attachment.Size, 10),
		resp.Header.Get("Content-Length"))
	assert.EqualValues(t, attachment.Size, resp.ContentLength)
	assert.Len(t, payload, int(attachment.Size))
}

// An empty attachment states a length of zero rather than omitting the header,
// which is a different thing: omitted means "read until I close".
func TestGetEmptyAttachmentContentStatesZero(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "empty.bin", "")

	resp, payload := a.do(http.MethodGet, attachmentPath(&attachment)+"/content", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "0", resp.Header.Get("Content-Length"))
	assert.Empty(t, payload)
}

// A stored file that no longer matches its metadata breaks the transfer rather
// than arriving as a plausible short one, which is what stating the recorded
// size rather than a measured one buys.
func TestTruncatedContentBreaksTheTransfer(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "the whole thing\n")

	// Reach past awb and shorten the stored file, which nothing awb does can
	// bring about: what is being pinned is what happens if something else does.
	blob := filepath.Join(a.blobs, attachment.Sha256)
	require.NoError(t, os.WriteFile(blob, []byte("short"), 0o600))

	resp, err := a.server.Client().Get(
		a.server.URL + attachmentPath(&attachment) + "/content")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // the body is being read out

	assert.Equal(t, strconv.FormatInt(attachment.Size, 10), resp.Header.Get("Content-Length"),
		"the length is what was recorded, not what is on the disk")
	_, err = io.ReadAll(resp.Body)
	assert.Error(t, err, "the short body is a failed read rather than a complete one")
}

// A name outside ASCII is encoded rather than dropped or mangled.
func TestAttachmentContentDispositionEncodesTheName(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, `Ω "notes".txt`, "boom\n")

	resp, _ := a.do(http.MethodGet, attachmentPath(&attachment)+"/content", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	disposition := resp.Header.Get("Content-Disposition")
	kind, params, err := mime.ParseMediaType(disposition)
	require.NoError(t, err, disposition)
	assert.Equal(t, "attachment", kind)
	assert.Equal(t, `Ω "notes".txt`, params["filename"])
}

func TestDeleteAttachment(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "boom\n")

	resp, payload := a.do(http.MethodDelete, attachmentPath(&attachment), "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var deleted domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &deleted))
	assert.Equal(t, attachment, deleted, "the object as it was immediately before deletion")
	assert.Empty(t, resp.Header.Get("ETag"))

	resp, payload = a.do(http.MethodGet, attachmentPath(&attachment), "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, payload)
}

// An attachment is addressed by its issue and its name. A name the issue does
// not hold is a 404, and one outside the name rules is a 400.
func TestAttachmentReferences(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "boom\n")

	// The issue half takes any reference an issue takes.
	_, hash, _ := domain.SplitID(issue.ID)
	resp, payload := a.do(http.MethodGet, "/api/issues/"+hash+"/attachments/trace.txt", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet,
		"/api/issues/"+issue.ID+"/attachments/nothing.txt", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet,
		"/api/issues/awb-ffffff/attachments/trace.txt", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet,
		"/api/issues/"+issue.ID+"/attachments/"+url.PathEscape("../escape.txt"), "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)

	_ = attachment
}

// A name is unique within an issue, so a second under the same name is a 409;
// another issue may hold that name.
func TestOneAttachmentNamePerIssue(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	other := a.createIssue(`{"workspace":"awb","title":"Tokeniser drops a newline"}`)
	a.attach(issue.ID, "trace.txt", "the first one\n")

	resp, payload := a.upload(issue.ID, "?name=trace.txt", "application/octet-stream",
		"the second one\n")
	assert.Equal(t, http.StatusConflict, resp.StatusCode, payload)
	assert.Contains(t, payload, "trace.txt")

	resp, payload = a.upload(other.ID, "?name=trace.txt", "application/octet-stream",
		"the first one\n")
	assert.Equal(t, http.StatusCreated, resp.StatusCode, payload)
}

// A name that needs escaping survives the round trip through the path.
func TestAwkwardAttachmentNames(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	for _, name := range []string{
		"release notes.md", "100% done.txt", "what?.log", "a#b.txt", "Ωmega.txt",
		"quote\".txt", "percent%2Fnotslash.txt",
	} {
		attachment := a.attach(issue.ID, name, "content of "+name)
		require.Equal(t, name, attachment.Name)

		resp, payload := a.do(http.MethodGet, attachmentPath(&attachment), "")
		require.Equal(t, http.StatusOK, resp.StatusCode, "%q: %s", name, payload)
		var read domain.Attachment
		require.NoError(t, json.Unmarshal([]byte(payload), &read))
		assert.Equal(t, name, read.Name)

		resp, payload = a.do(http.MethodGet, attachmentPath(&attachment)+"/content", "")
		require.Equal(t, http.StatusOK, resp.StatusCode, "%q", name)
		assert.Equal(t, "content of "+name, payload)
	}
}

// A PATCH may carry the attachments it read back, and their values are
// ignored, so a UI can send back the object it read.
func TestPatchIgnoresAttachments(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)
	a.attach(issue.ID, "trace.txt", "boom\n")

	resp, payload := a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	var read map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &read))

	read["title"] = "Parser still crashes"
	edited, err := json.Marshal(read)
	require.NoError(t, err)

	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, string(edited))
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var updated domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &updated))
	assert.Equal(t, "Parser still crashes", updated.Title)
	assert.Len(t, updated.Attachments, 1)
}

// A body on an endpoint that declares none is refused for being there at all,
// and only one byte of it is ever read: reading it out in full would let a
// caller make the server hold the transport cap in memory to be told the
// endpoint wanted nothing.
func TestBodyOnANoBodyEndpointIsRefusedWithoutReadingIt(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"workspace":"awb","title":"Parser crashes"}`)

	// A body far larger than the transport cap: it is still the endpoint taking
	// no body that is reported, not its size, because its size was never the
	// problem and nothing here counted it.
	resp, payload := a.do(http.MethodGet, "/api/issues/"+issue.ID,
		strings.Repeat("x", 4<<20))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, "this endpoint takes no request body")

	// An empty body is not a body, so the request is answered normally.
	resp, payload = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
}
