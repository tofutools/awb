package cli

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/storage"
)

func newServeHandler(t *testing.T, corsOrigins ...string) http.Handler {
	t.Helper()
	db, err := storage.Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := buildHandler(local.New(db, "mikael"), nil, "awb", "127.0.0.1:7777", corsOrigins)
	require.NoError(t, err)
	return h
}

// get performs a request, decoding the response the way a browser would.
func get(t *testing.T, h http.Handler, method, path string, headers ...string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close() //nolint:errcheck

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		require.NoError(t, err, "the response claims gzip but is not gzip")
		defer gz.Close() //nolint:errcheck
		reader = gz
	}

	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	return resp, string(body)
}

// A response must be gzipped once, not twice.
//
// The static handler compresses what it serves, so wrapping the whole tree in
// another compressor encoded every asset twice. A browser decodes one layer and
// is left holding the other; curl, which does not ask for gzip unless told to,
// sees nothing wrong. This test asks for gzip and decodes exactly once.
func TestResponsesAreCompressedOnce(t *testing.T) {
	h := newServeHandler(t)

	for _, path := range []string{"/", "/app.js", "/app.css", "/api/identity", "/openapi.json"} {
		resp, body := get(t, h, http.MethodGet, path, "Accept-Encoding", "gzip")
		require.Equal(t, http.StatusOK, resp.StatusCode, path)

		// After a single decode the body must be the real thing, not more gzip.
		assert.False(t, strings.HasPrefix(body, "\x1f\x8b"), "%s is compressed twice", path)
		assert.NotEmpty(t, body, path)
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	h := newServeHandler(t)

	resp, body := get(t, h, http.MethodGet, "/", "Accept-Encoding", "gzip")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<!doctype html>")
	assert.Contains(t, body, `type="importmap"`)

	// The vendored browser bundles are committed and embedded.
	resp, body = get(t, h, http.MethodGet, "/vendor/markdown-it-14.1.0.js")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body)

	// A deep link falls back to the shell, so client-side routing works.
	resp, body = get(t, h, http.MethodGet, "/issues/awb-a1b2c3")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<!doctype html>")
}

// The import map's hash is in the CSP, so the committed bundles load and
// nothing else does.
func TestSecurityHeaders(t *testing.T) {
	h := newServeHandler(t)
	resp, _ := get(t, h, http.MethodGet, "/")

	csp := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "object-src 'none'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	assert.Regexp(t, `script-src 'self' 'sha256-[A-Za-z0-9+/=]+'`, csp)

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
}

func TestAPIResponsesAreNotCacheable(t *testing.T) {
	h := newServeHandler(t)
	resp, _ := get(t, h, http.MethodGet, "/api/issues")
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

func TestOpenAPIIsServed(t *testing.T) {
	h := newServeHandler(t)

	resp, body := get(t, h, http.MethodGet, "/openapi.json")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Contains(t, body, `"openapi"`)

	resp, body = get(t, h, http.MethodGet, "/openapi.yaml")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "openapi: 3.1.0")
}

// A browser attaches basic-authentication credentials to cross-site requests of
// its own accord, so a write must carry an Origin or Referer naming the server
// or an allowed origin.
func TestSameOriginWrites(t *testing.T) {
	h := newServeHandler(t, "https://ui.example.com")

	// A request carrying neither header is allowed: that is what every
	// non-browser client sends, and the CLI is one of them.
	resp, _ := get(t, h, http.MethodPost, "/api/projects")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, h, http.MethodPost, "/api/projects", "Origin", "http://127.0.0.1:7777")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "the server's own origin")

	resp, _ = get(t, h, http.MethodPost, "/api/projects", "Origin", "https://ui.example.com")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "an allowed --cors-origin")

	resp, _ = get(t, h, http.MethodPost, "/api/projects", "Origin", "https://evil.example.com")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, h, http.MethodPost, "/api/projects", "Origin", "null")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// A GET is never a state change and is exempt.
	resp, _ = get(t, h, http.MethodGet, "/api/issues", "Origin", "https://evil.example.com")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// Cross-origin access is opt in; the default allows none.
func TestCORS(t *testing.T) {
	allowed := newServeHandler(t, "https://ui.example.com")

	resp, _ := get(t, allowed, http.MethodGet, "/api/issues", "Origin", "https://ui.example.com")
	assert.Equal(t, "https://ui.example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))

	// Without ETag, X-Total-Count and Location exposed, a cross-origin UI could
	// use neither the optimistic concurrency nor the paging.
	exposed := resp.Header.Get("Access-Control-Expose-Headers")
	for _, header := range []string{"ETag", "X-Total-Count", "Location"} {
		assert.Contains(t, exposed, header)
	}

	// The preflight is answered, and must reach the handler rather than being
	// refused as a cross-site write.
	resp, _ = get(t, allowed, http.MethodOptions, "/api/issues", "Origin", "https://ui.example.com")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), http.MethodPatch)
	for _, header := range []string{"Content-Type", "If-Match", "Authorization"} {
		assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), header)
	}

	// An unlisted origin gets no CORS headers at all.
	resp, _ = get(t, allowed, http.MethodGet, "/api/issues", "Origin", "https://evil.example.com")
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))

	// And the default allows none.
	none := newServeHandler(t)
	resp, _ = get(t, none, http.MethodGet, "/api/issues", "Origin", "https://ui.example.com")
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}
