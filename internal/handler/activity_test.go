package handler_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/remote"
)

func TestCommentsAndActivityAPI(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Timeline"}`)

	resp, payload := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/comments",
		`{"body":"A **Markdown** comment.\n"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode, payload)
	var comment domain.Activity
	require.NoError(t, json.Unmarshal([]byte(payload), &comment))
	assert.Equal(t, domain.ActivityKindComment, comment.Kind)
	assert.Equal(t, "mikael", comment.Actor)

	resp, payload = a.do(http.MethodGet,
		"/api/issues/"+issue.ID+"/activity?kind=comment&limit=1&offset=0", "")
	require.Equal(t, http.StatusOK, resp.StatusCode, payload)
	assert.Equal(t, "1", resp.Header.Get("X-Total-Count"))
	var entries []domain.Activity
	require.NoError(t, json.Unmarshal([]byte(payload), &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, comment.ID, entries[0].ID)
}

func TestCommentsRoundTripThroughRemoteBackend(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Remote timeline"}`)
	base, err := url.Parse(a.server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "mikael")
	t.Cleanup(func() { _ = client.Close() })

	comment, err := client.AddComment(t.Context(), issue.ID, "remote comment")
	require.NoError(t, err)
	assert.Equal(t, "remote comment", comment.Body)
	page, err := client.ListActivity(t.Context(), issue.ID, domain.ActivityKindComment, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, page.Total)
	require.Len(t, page.Activity, 1)
	assert.Equal(t, comment.ID, page.Activity[0].ID)
}

func TestCommentAndActivityRefusals(t *testing.T) {
	a := newAPI(t)
	issue := a.createIssue(`{"project":"awb","title":"Timeline"}`)

	resp, _ := a.do(http.MethodPost, "/api/issues/"+issue.ID+"/comments", `{"body":"  "}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = a.do(http.MethodGet, "/api/issues/"+issue.ID+"/activity?kind=nope", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = a.do(http.MethodGet, "/api/issues/"+issue.ID+"/activity?unknown=x", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
