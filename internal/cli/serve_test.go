package cli

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/openapi"
	"github.com/tofutools/awb/internal/storage"
	"github.com/tofutools/awb/web"
)

func newServeHandler(t *testing.T, corsOrigins ...string) http.Handler {
	t.Helper()
	return newServeHandlerWith(t, serveOptions{
		addr:           "127.0.0.1",
		port:           7777,
		corsOrigins:    corsOrigins,
		basicAuthRealm: "awb",
	})
}

func newServeHandlerWith(t *testing.T, opts serveOptions) http.Handler {
	t.Helper()
	db, err := storage.Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	h, err := buildHandler(local.New(db, "mikael"), openapi.New(raw), nil, opts)
	require.NoError(t, err)
	return h
}

// get performs a request without a body, decoding the response the way a
// browser would.
func get(t *testing.T, h http.Handler, method, path string, headers ...string) (*http.Response, string) {
	t.Helper()
	return send(t, h, method, path, "", headers...)
}

// send is get with a request body.
func send(t *testing.T, h http.Handler, method, path, body string,
	headers ...string) (*http.Response, string) {
	t.Helper()
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, requestBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
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

	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	return resp, string(payload)
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

// Every embedded asset is reachable at the path it is embedded under.
//
// /api/ belongs to the JSON API: the router sends every path under it to the
// API server, which knows nothing about files. An asset compiled to
// web/static/api/ would therefore be answered 404 by the API rather than
// served, and the UI would load a page whose script is missing. That is a
// mistake made by moving a source file, not by editing this server, which is
// why the check walks what is actually embedded rather than naming paths.
func TestEveryEmbeddedAssetIsReachable(t *testing.T) {
	h := newServeHandler(t)

	staticFS, err := web.StaticFS()
	require.NoError(t, err)

	assets := 0
	require.NoError(t, fs.WalkDir(staticFS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		assets++
		resp, body := get(t, h, http.MethodGet, "/"+path)
		if path == "index.html" {
			// The static handler redirects it to the directory it indexes,
			// which TestStaticAssetsAreServed asks for by that name.
			assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode, path)
			return nil
		}
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.NotEmpty(t, body, path)
		return nil
	}))
	assert.NotZero(t, assets, "the frontend is not embedded")
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
	// use neither the optimistic concurrency nor the paging. Each response
	// exposes the ones it carries: the generated encoders narrow what the
	// middleware set to exactly the headers that response has.
	assert.Contains(t, resp.Header.Get("Access-Control-Expose-Headers"), "X-Total-Count")

	resp, _ = send(t, allowed, http.MethodPost, "/api/projects", `{"key":"web"}`,
		"Origin", "https://ui.example.com")
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	exposed := resp.Header.Get("Access-Control-Expose-Headers")
	for _, header := range []string{"Etag", "Location"} {
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

// The UI resolves every URL it uses relatively, so the one thing a deployment
// under a path needs is the shell's <base href>.
func TestPublicURLSetsTheBaseHref(t *testing.T) {
	h := newServeHandlerWith(t, serveOptions{
		addr:      "127.0.0.1",
		port:      7777,
		publicURL: "https://example.com/awb/",
	})

	for _, path := range []string{"/", "/issues/awb-a1b2c3"} {
		resp, body := get(t, h, http.MethodGet, path)
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Contains(t, body, `<base href="/awb/">`, path)
	}

	// The import map is what the CSP pins by hash, so rewriting the shell must
	// leave it exactly as it was.
	resp, body := get(t, h, http.MethodGet, "/")
	assert.Contains(t, body, `type="importmap"`)
	assert.Regexp(t, `script-src 'self' 'sha256-[A-Za-z0-9+/=]+'`,
		resp.Header.Get("Content-Security-Policy"))

	// Served at the root, the shell is the compiled file unchanged.
	_, body = get(t, newServeHandler(t), http.MethodGet, "/")
	assert.Contains(t, body, `<base href="/">`)
}

func TestBasePathFromPublicURL(t *testing.T) {
	for _, tc := range []struct{ publicURL, want string }{
		{"", "/"},
		{"https://example.com", "/"},
		{"https://example.com/", "/"},
		{"https://example.com/awb", "/awb/"},
		{"https://example.com/awb/", "/awb/"},
		{"http://example.com:8080/a/b", "/a/b/"},
	} {
		got, err := basePathFromPublicURL(tc.publicURL)
		require.NoError(t, err, tc.publicURL)
		assert.Equal(t, tc.want, got, tc.publicURL)
	}

	// A protocol-relative base would re-point the whole UI at another host, and
	// anything outside the unreserved characters would have to be escaped into
	// an HTML attribute rather than trusted.
	for _, publicURL := range []string{
		"https://example.com//evil.example.com/",
		"https://example.com/awb\"><script>",
		"https://example.com/a b",
	} {
		_, err := basePathFromPublicURL(publicURL)
		assert.Error(t, err, publicURL)
	}
}

// Behind a reverse proxy the browser names the proxy, so that — not the
// listener — is the origin a write must come from.
func TestPublicURLIsTheSameOrigin(t *testing.T) {
	h := newServeHandlerWith(t, serveOptions{
		addr:      "127.0.0.1",
		port:      7777,
		publicURL: "https://example.com/awb/",
	})

	resp, _ := get(t, h, http.MethodPost, "/api/projects", "Origin", "https://example.com")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, h, http.MethodPost, "/api/projects", "Origin", "http://127.0.0.1:7777")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// Strict-Transport-Security is sent only when a proxy in front terminates TLS.
// Sent by a server that is meant to be reachable over plain HTTP it would break
// it, so it is opt in.
func TestStrictTransportSecurity(t *testing.T) {
	plain := newServeHandler(t)
	resp, _ := get(t, plain, http.MethodGet, "/")
	assert.Empty(t, resp.Header.Get("Strict-Transport-Security"))

	behindTLS := newServeHandlerWith(t, serveOptions{addr: "127.0.0.1", port: 7777, https: true})
	resp, _ = get(t, behindTLS, http.MethodGet, "/")
	assert.Equal(t, "max-age=31536000", resp.Header.Get("Strict-Transport-Security"))
	resp, _ = get(t, behindTLS, http.MethodGet, "/api/issues")
	assert.Equal(t, "max-age=31536000", resp.Header.Get("Strict-Transport-Security"))
}

// The shell is served from memory rather than by the file server, so it carries
// its own validator; without one "no-cache" could never be satisfied.
func TestShellIsRevalidated(t *testing.T) {
	h := newServeHandler(t)

	resp, _ := get(t, h, http.MethodGet, "/")
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))

	resp, body := get(t, h, http.MethodGet, "/", "If-None-Match", etag)
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.Empty(t, body)

	// A different base path is a different shell, so a client that cached one
	// does not keep it across a redeployment under a path.
	under := newServeHandlerWith(t, serveOptions{
		addr:      "127.0.0.1",
		port:      7777,
		publicURL: "https://example.com/awb/",
	})
	resp, _ = get(t, under, http.MethodGet, "/")
	assert.NotEqual(t, etag, resp.Header.Get("ETag"))
}
