package handler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/handler"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/storage"
)

type api struct {
	t      *testing.T
	server *httptest.Server
	be     *local.Backend
}

func newAPI(t *testing.T) *api {
	t.Helper()
	db, err := storage.Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	be := local.New(db, "mikael")
	_, err = be.CreateProject(t.Context(), backend.ProjectCreate{Key: "awb", Name: "Agent Work Board"})
	require.NoError(t, err)

	mux := http.NewServeMux()
	handler.New(func(*http.Request) backend.Backend { return be }).Routes(mux)

	server := httptest.NewServer(handler.NoStore(mux))
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

// A JSON escape denoting an unpaired surrogate is rejected rather than repaired
// into U+FFFD.
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

	// The unchangeable fields are ignored when they equal what is stored, so a
	// UI can send back the object it read.
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

// The ETag/If-Match handshake of SPEC §6.2.
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
	assert.Equal(t, "[]\n", payload)

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
	assert.Equal(t, "[]\n", payload)
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

	// Deletion refuses while the project holds issues, and cascade is a
	// boolean query parameter.
	a.createIssue(`{"project":"web","title":"t"}`)
	resp, _ = a.do(http.MethodDelete, "/api/projects/web", "")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, payload = a.do(http.MethodDelete, "/api/projects/web?cascade=true", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Empty(t, resp.Header.Get("ETag"))
}

// The status taxonomy of SPEC §6.1.
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

// An {id} path segment accepts an unambiguous prefix or a bare hash, so the CLI
// needs no extra round trip in remote mode.
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
		assert.Equal(t, "[]\n", payload, path)
		assert.Equal(t, "0", resp.Header.Get("X-Total-Count"), path)
	}
}
