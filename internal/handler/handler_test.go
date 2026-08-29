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
}

func newAPI(t *testing.T) *api {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	be := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	_, err = be.CreateProject(t.Context(), backend.ProjectCreate{Key: "awb", Name: "Agent Work Board"})
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
	return &api{t: t, server: server, be: be}
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
		`{"project":"awb","title":"Parser crashes","type":"bug","priority":1,"labels":["parser"]}`)

	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)

	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &issue))
	assert.Equal(t, "Parser crashes", issue.Title)
	assert.Equal(t, domain.TypeBug, issue.Type)

	// 201 carries the new object and a Location header naming it.
	assert.Equal(t, "/api/issues/"+issue.ID, resp.Header.Get("Location"))
	// Every response whose body is one Issue carries the ETag for that version.
	assert.Equal(t, local.ETag(issue.UpdatedAt), resp.Header.Get("ETag"))
	// No response under /api/ is cacheable.
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

// Nothing beyond the recognised fields is accepted: they are rejected rather
// than ignored.
func TestCreateIssueRejectsUnknownFields(t *testing.T) {
	a := newAPI(t)
	for _, body := range []string{
		`{"project":"awb","title":"t","id":"awb-aaaaaa"}`,
		`{"project":"awb","title":"t","status":"closed"}`,
		`{"project":"awb","title":"t","close_reason":"x"}`,
		`{"project":"awb","title":"t","created_at":"2026-01-01T00:00:00.000Z"}`,
		`{"project":"awb","title":"t","blocked":true}`,
		`{"project":"awb","title":"t","nonsense":1}`,
		`{"project":"awb","title":"t","relations":[{"type":"related","other":"x","direction":"out"}]}`,
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
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	for _, req := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/issues", `{"project":"awb","title":null}`},
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

	resp, payload := a.do(http.MethodPost, "/api/issues", `{"project":"awb","title":"a\ud800b"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, payload, "surrogate")

	// A properly paired surrogate is an ordinary character and is accepted.
	resp, payload = a.do(http.MethodPost, "/api/issues", `{"project":"awb","title":"a😀b"}`)
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
		a.server.URL+"/api/issues", strings.NewReader(`{"project":"awb","title":"t"}`))
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
	issue := a.createIssue(`{"project":"awb","title":"t","labels":["a","b"]}`)

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
		`{"assignee":"claude-1"}`,
		`{"close_reason":"done"}`,
		`{"labels":["a"]}`,
	} {
		resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, body)
		assert.Contains(t, payload, "cannot be changed", body)
	}
}

// Labels are compared as the sorted form, which is what a client read.
func TestPatchAcceptsLabelsInAnyOrder(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t","labels":["a","b"]}`)

	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"labels":["b","a"]}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
}

// The ETag/If-Match handshake.
func TestConditionalEdit(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

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

	// Omitting it is last-write-wins, which is what the CLI always does.
	resp, _ = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"fourth"}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// A relation added meanwhile does not move updated_at, so a conditional edit
// does not fail on it.
func TestETagSurvivesARelation(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)
	other := a.createIssue(`{"project":"awb","title":"other"}`)
	tag := local.ETag(issue.UpdatedAt)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/relations",
		`{"type":"blocked-by","other":"`+other.ID+`"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodPatch, "/api/issues/"+issue.ID, `{"title":"renamed"}`, "If-Match", tag)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload,
		"the tag guards the issue's own stored fields")
}

// A tree aggregates many issues and no one version tags it.
func TestTreeCarriesNoETag(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodGet, "/api/issues/"+issue.ID+"/tree", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
	assert.Contains(t, payload, `"children"`)
}

// A delete answers with the object as it was, and carries no ETag.
func TestDeleteCarriesNoETag(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodDelete, "/api/issues/"+issue.ID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
	assert.Contains(t, payload, issue.ID)

	resp, _ = a.do(http.MethodGet, "/api/issues/"+issue.ID, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// The compare-and-set of claim: two agents racing cannot both win.
func TestClaimCompareAndSet(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim",
		`{"assignee":"claude-1","expect_assignee":""}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim",
		`{"assignee":"claude-2","expect_assignee":""}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode, payload)
}

// assignee may be omitted, in which case the request's identity is used.
func TestClaimDefaultsToTheRequestIdentity(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var claimed domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &claimed))
	assert.Equal(t, "mikael", claimed.Assignee)
}

// Every mutating endpoint answers with the resulting object, the label and
// relation removals included: a client that renders the response must see the
// change.
func TestMutationsReturnTheObject(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t","labels":["x"]}`)
	other := a.createIssue(`{"project":"awb","title":"other"}`)

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
		a.createIssue(`{"project":"awb","title":"t"}`)
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
	a.createIssue(`{"project":"awb","title":"Parser crashes on empty input"}`)
	a.createIssue(`{"project":"awb","title":"Unrelated"}`)

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

// The facet endpoints honour the selection parameters, the facet's own
// included, so a UI can narrow progressively.
func TestFacets(t *testing.T) {
	a := newAPI(t)
	a.createIssue(`{"project":"awb","title":"a","labels":["parser","frontend"]}`)
	a.createIssue(`{"project":"awb","title":"b","labels":["parser"]}`)

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
}

func TestIdentity(t *testing.T) {
	a := newAPI(t)
	resp, payload := a.do(http.MethodGet, "/api/identity", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"identity":"mikael"}`, payload)
}

func TestProjects(t *testing.T) {
	a := newAPI(t)

	resp, payload := a.do(http.MethodPost, "/api/projects", `{"key":"web"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	assert.Equal(t, "/api/projects/web", resp.Header.Get("Location"))

	var project domain.Project
	require.NoError(t, json.Unmarshal([]byte(payload), &project))
	assert.Equal(t, "web", project.Name, "the name defaults to the key")
	assert.NotEmpty(t, resp.Header.Get("ETag"))

	// A key may appear in a PATCH but may not change.
	resp, payload = a.do(http.MethodPatch, "/api/projects/web", `{"key":"web","name":"Web UI"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, _ = a.do(http.MethodPatch, "/api/projects/web", `{"key":"other"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// The derived fields are ignored whatever they say.
	resp, payload = a.do(http.MethodPatch, "/api/projects/web",
		`{"active_issues":99,"created_at":"2000-01-01T00:00:00.000Z"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	// A duplicate is a conflict.
	resp, _ = a.do(http.MethodPost, "/api/projects", `{"key":"web"}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// Deletion refuses while the project holds issues, and cascade is a boolean
	// query parameter.
	a.createIssue(`{"project":"web","title":"t"}`)
	resp, _ = a.do(http.MethodDelete, "/api/projects/web", "")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, payload = a.do(http.MethodDelete, "/api/projects/web?cascade=true", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
}

// The status taxonomy.
func TestErrorStatuses(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"unknown issue", http.MethodGet, "/api/issues/awb-ffffff", "", http.StatusNotFound},
		{"unknown project", http.MethodGet, "/api/projects/nosuch", "", http.StatusNotFound},
		{"filter naming a missing project", http.MethodGet, "/api/issues?project=nosuch", "",
			http.StatusNotFound},
		{"bad enum", http.MethodPost, "/api/issues", `{"project":"awb","title":"t","type":"nonsense"}`,
			http.StatusBadRequest},
		{"bad priority", http.MethodPost, "/api/issues", `{"project":"awb","title":"t","priority":9}`,
			http.StatusBadRequest},
		{"bad label", http.MethodPost, "/api/issues", `{"project":"awb","title":"t","labels":["Bad"]}`,
			http.StatusBadRequest},
		{"empty title", http.MethodPost, "/api/issues", `{"project":"awb","title":"  "}`,
			http.StatusBadRequest},
		{"malformed JSON", http.MethodPost, "/api/issues", `{`, http.StatusBadRequest},
		{"self relation", http.MethodPost, "/api/issues/" + issue.ID + "/relations",
			`{"type":"blocked-by","other":"` + issue.ID + `"}`, http.StatusConflict},
		{"two parents", http.MethodPost, "/api/issues",
			`{"project":"awb","title":"t","relations":[` +
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
	issue := a.createIssue(`{"project":"awb","title":"t"}`)
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
		a.server.URL+"/api/issues", strings.NewReader(`{"project":"awb","title":"untyped"}`))
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
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

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
	issue := a.createIssue(`{"project":"awb","title":"t"}`)
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
	issue := a.createIssue(`{"project":"awb","title":"t","labels":["a","b"]}`)

	// Claim it, so the stored status and assignee differ from what a client
	// that read it earlier would send back.
	_, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", `{"assignee":"claude-1"}`)
	require.Contains(t, payload, "in_progress")

	// A patch carrying the stale values is refused, not silently applied.
	for _, body := range []string{
		`{"title":"x","status":"open"}`,
		`{"title":"x","assignee":""}`,
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
		`{"title":"renamed","status":"in_progress","assignee":"claude-1","labels":["b","a"]}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Contains(t, payload, `"title":"renamed"`)
}

// The derived and immutable fields may be carried and their values ignored, so
// that a UI can send back the object it read — but ignored is not unchecked:
// each is validated against the schema declared for it, so a caller sending
// something it never received is told rather than quietly obeyed in part.
func TestIgnoredFieldsAreIgnoredButStillChecked(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	// Values a caller really could have read, stale or not, are ignored.
	resp, payload := a.do(http.MethodPatch, "/api/issues/"+issue.ID,
		`{"title":"renamed","id":"awb-000000","project":"awb",`+
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
		`{"project":"NOT A KEY"}`,
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
	issue := a.createIssue(`{"project":"awb","title":"t"}`)
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
	assert.Contains(t, payload, `"assignee":""`)
}

// A body that holds no JSON value is refused even where the body is optional:
// optional means a body may be omitted, not that one may say nothing. Sending
// none is what says nothing, and that still claims the issue for the request's
// identity.
func TestWhitespaceBodyIsRefusedWhereABodyIsOptional(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"t"}`)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", "  \n ")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, "holds no JSON value")

	resp, payload = a.do(http.MethodPost, "/api/issues/"+issue.ID+"/claim", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var claimed domain.Issue
	require.NoError(t, json.Unmarshal([]byte(payload), &claimed))
	assert.Equal(t, "mikael", claimed.Assignee, "the request's identity, as with no body")
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
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)

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
	assert.Equal(t, "/api/attachments/"+attachment.ID, resp.Header.Get("Location"))
	assert.Empty(t, resp.Header.Get("ETag"))
}

// The stated content type is what is recorded, exactly as it arrived.
func TestAddAttachmentWithAStatedContentType(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)

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
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)

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
		a.server.URL+"/api/issues", strings.NewReader(`{"project":"awb","title":"t"}`))
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
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)

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
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "?name=trace.txt&sort=priority",
		"application/octet-stream", "boom\n")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
	assert.Contains(t, payload, "sort")
}

func TestListAndGetAttachments(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "boom\n")

	resp, payload := a.do(http.MethodGet, "/api/issues/"+issue.ID+"/attachments", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))

	var listed []domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, attachment, listed[0])

	resp, payload = a.do(http.MethodGet, "/api/attachments/"+attachment.ID, "")
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
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)

	resp, payload := a.upload(issue.ID, "?name=page.html&content-type=text%2Fhtml",
		"application/octet-stream", "<script>alert(1)</script>")
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	var attachment domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &attachment))
	assert.Equal(t, "text/html", attachment.ContentType, "the metadata records what it is")

	resp, payload = a.do(http.MethodGet, "/api/attachments/"+attachment.ID+"/content", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "<script>alert(1)</script>", payload, "the bytes exactly as uploaded")
	assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, `attachment; filename=page.html`, resp.Header.Get("Content-Disposition"))
}

// A name outside ASCII is encoded rather than dropped or mangled.
func TestAttachmentContentDispositionEncodesTheName(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, `Ω "notes".txt`, "boom\n")

	resp, _ := a.do(http.MethodGet, "/api/attachments/"+attachment.ID+"/content", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	disposition := resp.Header.Get("Content-Disposition")
	kind, params, err := mime.ParseMediaType(disposition)
	require.NoError(t, err, disposition)
	assert.Equal(t, "attachment", kind)
	assert.Equal(t, `Ω "notes".txt`, params["filename"])
}

func TestDeleteAttachment(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "boom\n")

	resp, payload := a.do(http.MethodDelete, "/api/attachments/"+attachment.ID, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	var deleted domain.Attachment
	require.NoError(t, json.Unmarshal([]byte(payload), &deleted))
	assert.Equal(t, attachment, deleted, "the object as it was immediately before deletion")
	assert.Empty(t, resp.Header.Get("ETag"))

	resp, payload = a.do(http.MethodGet, "/api/attachments/"+attachment.ID, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, payload)
}

// A prefix addresses an attachment, and one naming nothing is a 404.
func TestAttachmentReferences(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)
	attachment := a.attach(issue.ID, "trace.txt", "boom\n")

	resp, payload := a.do(http.MethodGet, "/api/attachments/"+attachment.ID[:6], "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet, "/api/attachments/ffffffffffff", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, payload)

	resp, payload = a.do(http.MethodGet, "/api/attachments/not-hex", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
}

// A PATCH may carry the attachments it read back, and their values are
// ignored, so a UI can send back the object it read.
func TestPatchIgnoresAttachments(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Parser crashes"}`)
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
