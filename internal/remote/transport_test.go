package remote

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedirectPolicyRefusesCleartextBasicAuthentication(t *testing.T) {
	request := func(rawURL string, authenticated bool) *http.Request {
		parsed, err := url.Parse(rawURL)
		require.NoError(t, err)
		req := &http.Request{URL: parsed, Header: make(http.Header)}
		if authenticated {
			req.SetBasicAuth("alice", "hunter2")
		}
		return req
	}
	via := []*http.Request{{}}

	assert.Error(t, redirectPolicy(false)(request("http://example.com/api", true), via))
	assert.NoError(t, redirectPolicy(false)(request("https://example.com/api", true), via))
	assert.NoError(t, redirectPolicy(false)(request("http://127.0.0.1:7777/api", true), via))
	assert.NoError(t, redirectPolicy(false)(request("http://example.com/api", false), via))
	assert.NoError(t, redirectPolicy(true)(request("http://example.com/api", true), via))
}
