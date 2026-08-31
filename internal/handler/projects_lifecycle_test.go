package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectLifecycleAPIUsesETagsAndExplicitArchiveListing(t *testing.T) {
	a := newAPI(t)
	get, _ := a.do(http.MethodGet, "/api/projects/awb", "")
	etag := get.Header.Get("ETag")
	require.NotEmpty(t, etag)

	archived, body := a.do(http.MethodPost, "/api/projects/awb/archive", "", "If-Match", etag)
	require.Equal(t, http.StatusOK, archived.StatusCode, body)
	assert.Contains(t, body, `"state":"archived"`)
	assert.NotEqual(t, etag, archived.Header.Get("ETag"))

	active, body := a.do(http.MethodGet, "/api/projects", "")
	require.Equal(t, http.StatusOK, active.StatusCode)
	assert.JSONEq(t, `[]`, body)
	history, body := a.do(http.MethodGet, "/api/projects?state=archived", "")
	require.Equal(t, http.StatusOK, history.StatusCode)
	assert.Contains(t, body, `"key":"awb"`)

	audit, body := a.do(http.MethodGet, "/api/projects/awb/activity", "")
	require.Equal(t, http.StatusOK, audit.StatusCode)
	assert.Equal(t, "1", audit.Header.Get("X-Total-Count"))
	assert.Contains(t, body, `"action":"archived"`)

	stale, _ := a.do(http.MethodPost, "/api/projects/awb/restore", "", "If-Match", etag)
	assert.Equal(t, http.StatusPreconditionFailed, stale.StatusCode)
	restored, body := a.do(http.MethodPost, "/api/projects/awb/restore", "", "If-Match", archived.Header.Get("ETag"))
	require.Equal(t, http.StatusOK, restored.StatusCode, body)
	assert.Contains(t, body, `"state":"active"`)
}
