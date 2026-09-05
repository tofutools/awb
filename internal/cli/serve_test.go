package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/remote"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	stdhttputil "net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
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
	h, _ := newServeHandlerOn(t, opts)
	return h
}

func newProxyServeHandler(t *testing.T, proxyTo string) http.Handler {
	t.Helper()
	target, err := parseProxyURL(proxyTo)
	require.NoError(t, err)
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	h, err := buildProxyHandler(target, openapi.New(raw), serveOptions{
		addr: "127.0.0.1", port: 7777,
	}, log.New(io.Discard, "", 0))
	require.NoError(t, err)
	return h
}

// newServeHandlerOn builds the server and hands back the database behind it,
// so a test can add the user that turns authentication on.
func newServeHandlerOn(t *testing.T, opts serveOptions) (http.Handler, *local.Backend) {
	t.Helper()
	return newServeHandlerAuthenticating(t, opts, true)
}

// newServeHandlerAuthenticating builds one with or without an authenticator,
// which is the whole of what --no-auth decides; see the serve command.
func newServeHandlerAuthenticating(t *testing.T, opts serveOptions, authenticates bool) (
	http.Handler, *local.Backend) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	var credentials *authenticator
	if authenticates {
		credentials = &authenticator{db: db, realm: opts.basicAuthRealm}
	}
	be := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	h, err := buildHandler(be, openapi.New(raw), credentials, opts, log.New(io.Discard, "", 0))
	require.NoError(t, err)
	return h, be
}

// basicAuth is the header a client presents credentials in.
func basicAuth(username, password string) []string {
	return []string{"Authorization",
		"Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))}
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

// vendorBundle returns the embedded filename of the vendored bundle called
// name, which is version-stamped. Exactly one must be present: a superseded
// bundle left beside its replacement is a packaging mistake, not something to
// pick between.
func vendorBundle(t *testing.T, name string) string {
	t.Helper()
	staticFS, err := web.StaticFS()
	require.NoError(t, err)
	matches, err := fs.Glob(staticFS, "vendor/"+name+"-*.js")
	require.NoError(t, err)
	require.Len(t, matches, 1, "vendor/%s-*.js", name)
	return strings.TrimPrefix(matches[0], "vendor/")
}

func TestStaticAssetsAreServed(t *testing.T) {
	h := newServeHandler(t)

	resp, body := get(t, h, http.MethodGet, "/", "Accept-Encoding", "gzip")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<!doctype html>")
	assert.Contains(t, body, `type="importmap"`)
	shell := body

	// The vendored browser bundles are committed and embedded. Their filenames
	// carry the upstream version, so they are looked up by prefix: an upgrade
	// changes the name, and web/ts/vendor/rebuild.sh does not edit Go tests.
	for _, bundle := range []string{"codemirror", "dompurify", "markdown-it"} {
		name := vendorBundle(t, bundle)
		resp, body = get(t, h, http.MethodGet, "/vendor/"+name)
		assert.Equal(t, http.StatusOK, resp.StatusCode, name)
		assert.NotEmpty(t, body, name)

		// The import map is what makes the bundle reachable from the UI, so a
		// bundle whose name it does not carry would be embedded but unused.
		assert.Contains(t, shell, "./vendor/"+name)
	}

	// A deep link falls back to the shell, so client-side routing works.
	resp, body = get(t, h, http.MethodGet, "/issues/awb-a1b2c3")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<!doctype html>")
}

func TestUIProxyServesLocalUIAndForwardsAPIReads(t *testing.T) {
	var upstreamHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/awb/api/workspaces", r.URL.Path)
		assert.Equal(t, "page=2", r.URL.RawQuery)
		assert.Equal(t, upstreamHost, r.Host)
		assert.Equal(t, "Basic dGVzdDpzZWNyZXQ=", r.Header.Get("Authorization"))
		w.Header().Set("X-Total-Count", "7")
		w.Header().Set("Access-Control-Allow-Origin", "https://elsewhere.example")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		_, _ = io.WriteString(w, `[{"key":"awb"}]`)
	}))
	defer upstream.Close()
	upstreamHost = strings.TrimPrefix(upstream.URL, "http://")

	h := newProxyServeHandler(t, upstream.URL+"/awb/")
	resp, body := get(t, h, http.MethodGet, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<!doctype html>")

	resp, body = get(t, h, http.MethodGet, "/api/workspaces?page=2",
		"Authorization", "Basic dGVzdDpzZWNyZXQ=")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "7", resp.Header.Get("X-Total-Count"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.NotEqual(t, "default-src 'none'", resp.Header.Get("Content-Security-Policy"))
	assert.Len(t, resp.Header.Values("Content-Security-Policy"), 1)
	assert.JSONEq(t, `[{"key":"awb"}]`, body)
}

func TestUIProxyForwardsAuthenticationChallenge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="remote awb"`)
		http.Error(w, "credentials required", http.StatusUnauthorized)
	}))
	defer upstream.Close()

	resp, _ := get(t, newProxyServeHandler(t, upstream.URL), http.MethodGet, "/api/workspaces")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, `Basic realm="remote awb"`, resp.Header.Get("WWW-Authenticate"))
}

func TestUIProxyForwardsBrowserWritesAfterCheckingTheirOrigin(t *testing.T) {
	requests := 0
	var upstreamURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/awb/api/issues", r.URL.Path)
		assert.Equal(t, upstreamURL, r.Header.Get("Origin"))
		assert.Equal(t, upstreamURL+"/awb/", r.Header.Get("Referer"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(body))
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	upstreamURL = upstream.URL

	h := newProxyServeHandler(t, upstream.URL+"/awb")
	resp, _ := send(t, h, http.MethodPost, "/api/issues", `{}`,
		"Origin", "http://127.0.0.1:7777",
		"Referer", "http://127.0.0.1:7777/#/issues")
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, 1, requests)

	resp, _ = send(t, h, http.MethodPost, "/api/issues", `{}`,
		"Origin", "https://elsewhere.example")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, 1, requests)
}

func TestUIProxyCancelsAStalledUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	proxy := &stdhttputil.ReverseProxy{
		Rewrite:  func(request *stdhttputil.ProxyRequest) { request.SetURL(target) },
		ErrorLog: log.New(io.Discard, "", 0),
	}

	started := time.Now()
	resp, _ := get(t, proxyDeadlines(proxy, 20*time.Millisecond, time.Second),
		http.MethodGet, "/api/workspaces")
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Less(t, time.Since(started), time.Second)
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
	resp, _ := get(t, h, http.MethodPost, "/api/workspaces")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, h, http.MethodPost, "/api/workspaces", "Origin", "http://127.0.0.1:7777")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "the server's own origin")

	resp, _ = get(t, h, http.MethodPost, "/api/workspaces", "Origin", "https://ui.example.com")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "an allowed --cors-origin")

	resp, _ = get(t, h, http.MethodPost, "/api/workspaces", "Origin", "https://evil.example.com")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, h, http.MethodPost, "/api/workspaces", "Origin", "null")
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

	resp, _ = send(t, allowed, http.MethodPost, "/api/workspaces", `{"key":"web"}`,
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

func TestBasePathOfThePublicURL(t *testing.T) {
	for _, tc := range []struct{ publicURL, want string }{
		{"", "/"},
		{"https://example.com", "/"},
		{"https://example.com/", "/"},
		{"https://example.com/awb", "/awb/"},
		{"https://example.com/awb/", "/awb/"},
		{"http://example.com:8080/a/b", "/a/b/"},
	} {
		parsed, err := parsePublicURL(tc.publicURL)
		require.NoError(t, err, tc.publicURL)
		got, err := basePathOf(parsed)
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
		parsed, err := parsePublicURL(publicURL)
		require.NoError(t, err, publicURL)
		_, err = basePathOf(parsed)
		assert.Error(t, err, publicURL)
	}
}

// --public-url has to be an origin a browser can actually send. Anything else
// starts a server every browser write is refused by, which is a failure at
// startup rather than one to discover in a UI that cannot save.
func TestPublicURLMustBeAnOriginABrowserCanSend(t *testing.T) {
	for _, publicURL := range []string{
		"//example.com/awb",             // no scheme, so no origin
		"javascript://example.com/awb",  // a scheme no browser sends as an origin
		"https:///awb",                  // no host
		"https://user:pw@example.com/",  // credentials are not part of a base URL
		"https://example.com/awb?x=1",   // nor a query
		"https://example.com/awb#frag",  // nor a fragment
		"https://example.com:65536/awb", // a port no connection can be made to
		"https://example.com:0/awb",     // nor to this one
	} {
		_, err := parsePublicURL(publicURL)
		assert.Error(t, err, publicURL)
	}

	for _, publicURL := range []string{
		"http://example.com",
		"https://example.com/awb/",
		"https://example.com:8443/awb/",
	} {
		parsed, err := parsePublicURL(publicURL)
		assert.NoError(t, err, publicURL)
		assert.NotNil(t, parsed, publicURL)
	}
}

func TestProxyTargetMustBeAnHTTPBaseURL(t *testing.T) {
	for _, proxyTo := range []string{
		"//example.com/awb",
		"file:///tmp/awb",
		"https:///awb",
		"https://user:pw@example.com/awb",
		"https://example.com/awb?workspace=demo",
		"https://example.com/awb#issues",
		"https://example.com:65536/awb",
	} {
		_, err := parseProxyURL(proxyTo)
		assert.Error(t, err, proxyTo)
	}

	for _, proxyTo := range []string{
		"http://example.com",
		"https://example.com/awb/",
	} {
		parsed, err := parseProxyURL(proxyTo)
		require.NoError(t, err, proxyTo)
		assert.False(t, strings.HasSuffix(parsed.Path, "/"), proxyTo)
	}
}

// Loopback is what the default binds and is the local tracker the open server
// is for, so it starts with no user and no flag. A server is not started here:
// what is checked is that the refusal does not fire, which it would before the
// port is bound.
func TestServeAcceptsALoopbackBindingWithNoUsers(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "127.0.0.2", "::1", "localhost", "LOCALHOST"} {
		assert.Empty(t, serveOptions{addr: addr}.exposure(), addr)
	}
	for _, addr := range []string{"", "0.0.0.0", "192.0.2.10", "::", "example.com"} {
		assert.NotEmpty(t, serveOptions{addr: addr}.exposure(), addr)
	}
}

// --no-auth is how an operator says an open server is what they meant, and is
// the only thing that says so.
func TestWhatMayBeStartedWithoutAuthentication(t *testing.T) {
	none := userState{}
	deleted := userState{existed: true}
	users := userState{any: true, existed: true}

	assert.NoError(t, checkAuthentication(serveOptions{addr: "127.0.0.1"}, none),
		"a local tracker on loopback is the open server that is meant to work")
	assert.Error(t, checkAuthentication(serveOptions{addr: "0.0.0.0"}, none),
		"reaching other machines with nobody to authenticate is the accident")
	assert.NoError(t, checkAuthentication(serveOptions{addr: "0.0.0.0", noAuth: true}, none))

	// A locked server answers nothing until an account is added directly. It is
	// nevertheless held to the same transport boundary, because adding that
	// account makes the already-running server begin accepting Basic credentials.
	assert.NoError(t, checkAuthentication(serveOptions{addr: "127.0.0.1"}, deleted))
	assert.Error(t, checkAuthentication(serveOptions{addr: "0.0.0.0"}, deleted))
	assert.NoError(t, checkAuthentication(serveOptions{https: true}, deleted))
	assert.NoError(t, checkAuthentication(serveOptions{addr: "127.0.0.1", noAuth: true}, deleted))

	assert.Error(t, checkAuthentication(serveOptions{addr: "0.0.0.0"}, users),
		"Basic credentials may not cross a cleartext network by default")
	assert.NoError(t, checkAuthentication(serveOptions{addr: "0.0.0.0", https: true}, users),
		"--https says a TLS-terminating proxy protects the public connection")
	assert.NoError(t, checkAuthentication(serveOptions{
		addr: "0.0.0.0", publicURL: "https://example.com/awb/",
	}, users), "an HTTPS public URL is also a TLS proxy signal")
	assert.NoError(t, checkAuthentication(serveOptions{
		addr: "0.0.0.0", insecureTransport: true,
	}, users), "the explicit escape hatch accepts credential exposure")
	assert.NoError(t, checkAuthentication(serveOptions{addr: "0.0.0.0", noAuth: true}, users),
		"--no-auth means it: the users are simply not consulted")
}

func TestProxyRequiresSecureTransportBeforeForwardingCredentials(t *testing.T) {
	assert.NoError(t, serveOptions{port: 7777, proxyTo: "http://127.0.0.1:7777"}.validate())
	assert.NoError(t, serveOptions{port: 7777, proxyTo: "https://example.com/awb/"}.validate())
	assert.Error(t, serveOptions{port: 7777, proxyTo: "http://example.com/awb/"}.validate())
	assert.NoError(t, serveOptions{
		port: 7777, proxyTo: "http://example.com/awb/", insecureTransport: true,
	}.validate())
}

// A password is what turns authentication on, not an account. A user without
// one is an assignee the tracker knows about, and a server over a database
// holding only such users is still the open one — so adding an agent to the
// directory cannot lock everybody out of an installation that never had a
// password.
func TestOnlyAPasswordTurnsAuthenticationOn(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "claude-1"})
	require.NoError(t, err)

	users, err := readUserState(t.Context(), be.DB())
	require.NoError(t, err)
	assert.Equal(t, userState{}, users, "an assignee is not an account to authenticate")
	assert.NoError(t, checkAuthentication(serveOptions{addr: "127.0.0.1"}, users),
		"still the local tracker's open server")
	assert.Error(t, checkAuthentication(serveOptions{addr: "0.0.0.0"}, users),
		"and still refused where being reachable would be the accident")

	resp, _ := get(t, h, http.MethodGet, "/api/workspaces")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "no credentials are asked for")
	resp, _ = get(t, h, http.MethodGet, "/api/workspaces", basicAuth("claude-1", "")...)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "and none are checked")

	// Giving that same account a password closes the door on the next request,
	// with no restart, exactly as creating one with a password does.
	password := "hunter2"
	_, err = be.UpdateUser(t.Context(), "claude-1", backend.UserPatch{Password: &password}, "")
	require.NoError(t, err)

	users, err = readUserState(t.Context(), be.DB())
	require.NoError(t, err)
	assert.Equal(t, userState{any: true, existed: true}, users)

	resp, _ = get(t, h, http.MethodGet, "/api/workspaces")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp, _ = get(t, h, http.MethodGet, "/api/workspaces", basicAuth("claude-1", "hunter2")...)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// The bypass that lets the command line record an assignee the user directory
// does not know is the file's, not the API's. This runs it through the real
// server that serve builds, because what decides it is which backend a request
// is given — an assertion against local.Backend alone would not see a wiring
// mistake here.
func TestForceCannotRecordANameThatIsNoUserThroughTheAPI(t *testing.T) {
	for _, authenticates := range []bool{true, false} {
		name := "an authenticating server"
		if !authenticates {
			name = "an open server"
		}
		t.Run(name, func(t *testing.T) {
			h, be := newServeHandlerAuthenticating(t,
				serveOptions{addr: "127.0.0.1", port: 7777}, authenticates)
			_, err := be.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "awb"})
			require.NoError(t, err)
			issue, err := be.CreateIssue(t.Context(),
				backend.IssueCreate{Workspace: "awb", Title: "t"})
			require.NoError(t, err)
			_, err = be.CreateUser(t.Context(), backend.UserCreate{
				Name: "mikael", Password: "hunter2", WorkspaceAdmin: true})
			require.NoError(t, err)

			headers := []string{"Content-Type", "application/json"}
			if authenticates {
				headers = append(headers, basicAuth("mikael", "hunter2")...)
			}
			resp, payload := send(t, h, http.MethodPost, "/api/issues/"+issue.ID+"/claim",
				`{"assignee":"nobody","force":true}`, headers...)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, payload)
			assert.Contains(t, payload, "no such user: nobody")

			// The same request on the file, where whoever holds it could write
			// the row anyway, is what --force is for.
			claimed, err := be.Claim(t.Context(), issue.ID,
				backend.ClaimRequest{Assignee: "nobody", Force: true}, "")
			require.NoError(t, err)
			assert.Equal(t, []string{"nobody"}, claimed.Assignees)
		})
	}
}

// An account with no password is not a login with an empty one, on a server
// that authenticates: it is a name nothing can be presented for.
func TestAPasswordlessAccountCannotAuthenticate(t *testing.T) {
	h, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)
	_, err = be.CreateUser(t.Context(), backend.UserCreate{Name: "claude-1"})
	require.NoError(t, err)

	for _, password := range []string{"", "hunter2"} {
		resp, _ := get(t, h, http.MethodGet, "/api/workspaces", basicAuth("claude-1", password)...)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, password)
	}
}

func TestNoAuthServerDoesNotUseAStoredIdentityPreference(t *testing.T) {
	h, be := newServeHandlerAuthenticating(t, serveOptions{
		addr: "127.0.0.1", port: 7777, noAuth: true,
	}, false)
	_, err := be.CreateUser(t.Context(), backend.UserCreate{
		Name: "mikael", Password: "hunter2", WorkspaceAdmin: true,
	})
	require.NoError(t, err)
	_, err = be.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "web"})
	require.NoError(t, err)
	_, err = be.WithUser("mikael").SetWorkspaceIgnored(t.Context(), "web", true)
	require.NoError(t, err)

	resp, _ := get(t, h, http.MethodGet, "/api/workspaces/web")
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"the fixed identity is attribution, not a stored preference owner")
	resp, _ = get(t, h, http.MethodGet, "/api/preferences/workspaces")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"an open server has no per-user preference recovery endpoint")
	resp, _ = send(t, h, http.MethodPut, "/api/preferences/workspaces/web",
		`{"ignored":false}`, "Content-Type", "application/json")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	preferences, err := be.WithUser("mikael").ListWorkspacePreferences(t.Context())
	require.NoError(t, err)
	require.Len(t, preferences, 1)
	assert.True(t, preferences[0].Ignored,
		"the no-auth request must not mutate the matching stored user's preference")
}

// --https and --public-url describe one deployment, so they cannot disagree
// about whether it is behind TLS: a browser ignores Strict-Transport-Security
// received over plain HTTP, so the pair would leave the operator believing in a
// protection that is not there.
func TestHTTPSAndAnHTTPPublicURLContradictEachOther(t *testing.T) {
	behindTLS := serveOptions{addr: "127.0.0.1", port: 7777, https: true}

	contradictory := behindTLS
	contradictory.publicURL = "http://example.com/awb/"
	assert.Error(t, contradictory.validate())

	agreeing := behindTLS
	agreeing.publicURL = "https://example.com/awb/"
	assert.NoError(t, agreeing.validate())

	// --https on its own still says a proxy in front terminates TLS.
	assert.NoError(t, behindTLS.validate())

	// And an http public URL is fine when nothing claims otherwise.
	plain := serveOptions{addr: "127.0.0.1", port: 7777, publicURL: "http://example.com/awb/"}
	assert.NoError(t, plain.validate())
}

// An IPv6 listen address is bracketed in an origin, because that is the form a
// browser sends. Unbracketed, http://::1:7777 matches nothing and every write
// from the UI is refused.
func TestIPv6ListenerHasAnOriginABrowserSends(t *testing.T) {
	h := newServeHandlerWith(t, serveOptions{addr: "::1", port: 7777})

	resp, _ := get(t, h, http.MethodPost, "/api/workspaces", "Origin", "http://[::1]:7777")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)
}

// Behind a reverse proxy the browser names the proxy, so that — not the
// listener — is the origin a write must come from.
func TestPublicURLIsTheSameOrigin(t *testing.T) {
	h := newServeHandlerWith(t, serveOptions{
		addr:      "127.0.0.1",
		port:      7777,
		publicURL: "https://example.com/awb/",
	})

	resp, _ := get(t, h, http.MethodPost, "/api/workspaces", "Origin", "https://example.com")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, h, http.MethodPost, "/api/workspaces", "Origin", "http://127.0.0.1:7777")
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

// The challenge is the first response a browser sees, so it is the one that has
// to carry the headers: a host pinned only on the way past authentication is
// pinned after the password has been typed rather than before.
func TestTheAuthenticationChallengeCarriesTheSecurityHeaders(t *testing.T) {
	h, be := newServeHandlerOn(t,
		serveOptions{addr: "127.0.0.1", port: 7777, https: true, basicAuthRealm: "awb"})
	_, err := be.CreateUser(t.Context(),
		backend.UserCreate{Name: "mikael", Password: "hunter2"})
	require.NoError(t, err)

	resp, _ := get(t, h, http.MethodGet, "/")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `realm="awb"`)
	assert.Equal(t, "max-age=31536000", resp.Header.Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))

	// And the credentials the database holds still get through.
	resp, _ = get(t, h, http.MethodGet, "/api/identity", basicAuth("mikael", "hunter2")...)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
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

// lockedBuffer is a writer a test can read while the server writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// serve runs until it is stopped rather than answering and exiting, so what it
// writes is a log: every line is stamped with the time, and goes to the writer
// Execute was handed rather than straight to os.Stderr.
func TestServeLogsWithATimestamp(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Port 0 lets the operating system choose, so the test needs no free port
	// of its own. The command itself never reaches here with one: validate
	// refuses a port outside 1-65535.
	opts := serveOptions{addr: "127.0.0.1", port: 0, publicURL: "https://example.com/awb/"}

	stopped := make(chan error, 1)
	logger := log.New(&out, "", log.LstdFlags)
	go func() { stopped <- runServer(ctx, logger, opts, http.NotFoundHandler()) }()

	stamped := regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)
	require.Eventually(t, func() bool {
		return strings.Count(out.String(), "\n") >= 2
	}, 5*time.Second, 10*time.Millisecond, "the server logged nothing: %q", out.String())

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		assert.Regexp(t, stamped, line)
	}
	assert.Contains(t, lines[0], "awb serving on http://127.0.0.1:")
	assert.Contains(t, lines[1], "published at https://example.com/awb/")

	// And it stops when its context is cancelled, rather than on its own.
	cancel()
	select {
	case err := <-stopped:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not shut down")
	}
}

// The transport cap is per endpoint: the attachment upload gets the domain's
// attachment maximum and everything else gets the general one, which is far
// above anything the field maxima permit.
func TestAttachmentUploadHasItsOwnBodyCap(t *testing.T) {
	h := newServeHandler(t)

	// The handler is the whole server, so the fixtures are made through it.
	call := func(method, path, contentType, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := call(http.MethodPost, "/api/workspaces", "application/json", `{"key":"awb"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = call(http.MethodPost, "/api/issues", "application/json",
		`{"workspace":"awb","title":"Parser crashes"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var issue domain.Issue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))
	upload := "/api/issues/" + issue.ID + "/attachments?name=big.bin"

	// A file over the general cap is not too large: this endpoint's cap is the
	// attachment maximum.
	rec = call(http.MethodPost, upload, "application/octet-stream",
		strings.Repeat("x", maxRequestBody+1))
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// One over the attachment maximum is refused by the rule and not by the
	// transport, so it is the 400 the CLI turns into exit 2 — the same code and
	// the same message direct mode gives it. That is what the cap sitting above
	// the maximum is for.
	rec = call(http.MethodPost, upload, "application/octet-stream",
		strings.Repeat("x", domain.MaxAttachmentBytes+1))
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "attachment is too large")

	// A JSON body over the general cap still is, so raising one cap did not
	// raise the other.
	rec = call(http.MethodPost, "/api/issues", "application/json",
		`{"workspace":"awb","title":"`+strings.Repeat("x", maxRequestBody)+`"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
}

// The two requests that carry a file are recognised by their exact shape, so
// nothing else is given the long deadline.
func TestAttachmentTransferPaths(t *testing.T) {
	upload := func(method, path string) bool {
		return isAttachmentUpload(httptest.NewRequest(method, path, nil))
	}
	download := func(method, path string) bool {
		return isAttachmentDownload(httptest.NewRequest(method, path, nil))
	}

	assert.True(t, upload(http.MethodPost, "/api/issues/awb-5c1d84/attachments"))
	assert.True(t, upload(http.MethodPost, "/api/issues/awb-5c1d84/attachments?name=x"))
	assert.False(t, upload(http.MethodGet, "/api/issues/awb-5c1d84/attachments"))
	assert.False(t, upload(http.MethodPost, "/api/issues//attachments"))
	assert.False(t, upload(http.MethodPost, "/api/issues/awb-5c1d84/attachments/trace.txt"))
	assert.False(t, upload(http.MethodPost, "/api/issues"))

	assert.True(t, download(http.MethodGet,
		"/api/issues/awb-5c1d84/attachments/trace.txt/content"))
	assert.True(t, download(http.MethodGet,
		"/api/issues/awb-5c1d84/attachments/release%20notes.md/content"),
		"a name that needed escaping is still one segment")
	assert.False(t, download(http.MethodGet, "/api/issues/awb-5c1d84/attachments/trace.txt"))
	assert.False(t, download(http.MethodDelete,
		"/api/issues/awb-5c1d84/attachments/trace.txt/content"))
	assert.False(t, download(http.MethodGet, "/api/issues/awb-5c1d84/attachments//content"))

	// A name carrying an encoded slash is one segment on the wire and must be
	// read as one here, whatever the decoded path would look like.
	assert.True(t, download(http.MethodGet,
		"/api/issues/awb-5c1d84/attachments/a%2Fb.txt/content"))
}

// filler is a reader of arbitrary length that holds nothing: it is what an
// attachment of any size looks like to a caller that streams it.
type filler struct{ left int }

func (f *filler) Read(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, io.EOF
	}
	n := min(len(p), f.left)
	for i := range p[:n] {
		p[i] = 'x'
	}
	f.left -= n
	return n, nil
}

// totalAlloc is every byte the process has allocated so far. It only ever
// rises, so a difference across an operation is what that operation allocated
// whether or not the collector ran — which is the question here, since a body
// held whole shows up in it and a body streamed does not.
func totalAlloc() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.TotalAlloc
}

// Attachment content is streamed through the server in both directions and is
// never held whole.
//
// This runs the real thing: the whole serve middleware chain, over a real
// listener, driven by the remote backend the CLI uses in remote mode. What it
// measures is everything both ends allocated to move the payload — so a buffer
// anywhere on the path, in the client, the transport, the middleware, the
// generated decoder, the handler or the blob store, would show up here.
//
// The threshold is a fraction of the payload rather than a figure, because
// what is being pinned is the shape of the cost: bounded by the size of a copy
// buffer, not by the size of the file. It measured about 4% for the upload and
// 7% for the download when it was written, so the headroom is wide and only a
// real regression closes it.
func TestAttachmentContentIsStreamed(t *testing.T) {
	const (
		size    = 24 << 20
		allowed = size / 4
	)

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

	_, err = be.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "awb"})
	require.NoError(t, err)
	issue, err := be.CreateIssue(t.Context(),
		backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "mikael")
	t.Cleanup(func() { _ = client.Close() })

	before := totalAlloc()
	attachment, err := client.AddAttachment(t.Context(), issue.ID, backend.AttachmentCreate{
		Name:    "big.bin",
		Content: &filler{left: size},
	})
	require.NoError(t, err)
	uploaded := totalAlloc() - before

	require.EqualValues(t, size, attachment.Size)
	assert.Less(t, uploaded, uint64(allowed),
		"uploading %d bytes allocated %d: something on the path is holding the body", size, uploaded)

	before = totalAlloc()
	_, content, err := client.OpenAttachment(t.Context(), issue.ID, attachment.Name)
	require.NoError(t, err)
	written, err := io.Copy(io.Discard, content)
	require.NoError(t, err)
	require.NoError(t, content.Close())
	downloaded := totalAlloc() - before

	require.EqualValues(t, size, written)
	assert.Less(t, downloaded, uint64(allowed),
		"downloading %d bytes allocated %d: something on the path is holding the body",
		size, downloaded)
}

// An attachment's content is the one response that is not compressed, so a
// download costs neither the time nor the compressor state to make opaque
// bytes no smaller. Everything else the API answers still is.
func TestAttachmentContentIsNotCompressed(t *testing.T) {
	h := newServeHandler(t)

	call := func(method, path, contentType, body string, gzipped bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if gzipped {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// The fixtures ask for no compression, so their bodies can simply be read.
	rec := call(http.MethodPost, "/api/workspaces", "application/json", `{"key":"awb"}`, false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = call(http.MethodPost, "/api/issues", "application/json",
		`{"workspace":"awb","title":"Parser crashes"}`, false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var issue domain.Issue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))

	attachment := "/api/issues/" + issue.ID + "/attachments/trace.txt"
	rec = call(http.MethodPost, "/api/issues/"+issue.ID+"/attachments?name=trace.txt",
		"application/octet-stream", "boom\n", false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// A client that would take gzip is given none for the content, and gets the
	// bytes as they were stored.
	rec = call(http.MethodGet, attachment+"/content", "", "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "boom\n", rec.Body.String())

	// And the length it states is the length of what it sent. The two go
	// together: compressing this response would leave the header describing a
	// body of another length, so a Content-Encoding here would be a bug and so
	// would a Content-Length that disagreed with the body.
	assert.Equal(t, strconv.Itoa(len("boom\n")), rec.Header().Get("Content-Length"))

	// The same client asking for the metadata beside it is compressed as
	// before, so what was switched off is the one response and not the
	// mechanism.
	rec = call(http.MethodGet, attachment, "", "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
}

// A name that needs escaping survives the round trip through remote mode, and
// so does a server published under a base path.
//
// The two are one test because they are one bug: the path a request is sent to
// is assembled from an already-escaped name and a base that may carry a path
// of its own, and getting that wrong escapes the name twice — an attachment
// called "release notes.md" is then asked for as "release%2520notes.md" and
// answered 404 by a server that holds it.
func TestRemoteModeAddressesAwkwardNames(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	be := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	api, err := buildHandler(be, openapi.New(raw), &authenticator{db: db, realm: "awb"},
		serveOptions{port: 7777, basicAuthRealm: "awb"}, log.New(io.Discard, "", 0))
	require.NoError(t, err)

	// A reverse proxy publishing the server under /awb/ and stripping that base
	// before the request arrives, which is the contract the document states.
	proxy := http.NewServeMux()
	proxy.Handle("/awb/", http.StripPrefix("/awb", api))
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)

	_, err = be.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "awb"})
	require.NoError(t, err)
	issue, err := be.CreateIssue(t.Context(),
		backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)

	base, err := url.Parse(server.URL + "/awb")
	require.NoError(t, err)
	client := remote.New(base, "", "", "mikael")
	t.Cleanup(func() { _ = client.Close() })

	for _, name := range []string{
		"trace.txt", "release notes.md", "100% done?.txt", "Ωmega#1.txt",
		"a&b=c.txt", "semi;colon.txt", "quote\".txt", "percent%2Fnotslash.txt",
	} {
		content := "content of " + name
		added, err := client.AddAttachment(t.Context(), issue.ID, backend.AttachmentCreate{
			Name: name, Content: strings.NewReader(content),
		})
		require.NoError(t, err, "%q", name)
		require.Equal(t, name, added.Name)

		read, err := client.GetAttachment(t.Context(), issue.ID, name)
		require.NoError(t, err, "%q", name)
		assert.Equal(t, added, read)

		_, body, err := client.OpenAttachment(t.Context(), issue.ID, name)
		require.NoError(t, err, "%q", name)
		got, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		assert.Equal(t, content, string(got), "%q", name)

		deleted, err := client.DeleteAttachment(t.Context(), issue.ID, name)
		require.NoError(t, err, "%q", name)
		assert.Equal(t, name, deleted.Name)
	}
}

// Every listing the API publishes returns the same answer in the same sequence
// twice running — including on the sort keys the CLI does not offer, and on the
// endpoints that exist only over HTTP. The data below is deliberately tied on
// every key: same title, same priority, same type, same labels, same
// description, all created within the resolution of the stored timestamps, so
// what separates the rows is the tiebreak alone.
//
// This is the breadth half of the guarantee: it says every endpoint reaches an
// answer that does not vary. That each order is also the intended one is the
// job of the tests that name an expected sequence — TestListOrderIsTotal and
// the per-listing tests in internal/storage.
func TestEveryAPIListingIsDeterministic(t *testing.T) {
	h, be := newServeHandlerOn(t,
		serveOptions{addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb"})
	ctx := t.Context()

	_, err := be.CreateUser(ctx, backend.UserCreate{
		Name: "mikael", Password: "hunter2", WorkspaceAdmin: true, UserAdmin: true})
	require.NoError(t, err)
	// Two of these share a prefix with the awb workspace key and with the ids of
	// the issues in it, so one navigation query reaches all three of its groups.
	for _, name := range []string{"adam", "zoe", "awbot", "awbee"} {
		_, err := be.CreateUser(ctx, backend.UserCreate{Name: name, Password: "hunter2"})
		require.NoError(t, err)
	}
	for _, key := range []string{"awb", "web"} {
		_, err := be.CreateWorkspace(ctx, backend.WorkspaceCreate{Key: key, Name: "Agent Work Board"})
		require.NoError(t, err)
		for _, user := range []string{"mikael", "adam", "zoe", "awbot", "awbee"} {
			_, err := be.SetMember(ctx, key, user, domain.AccessRegular)
			require.NoError(t, err)
		}
	}

	// Archived, then restored, so the workspace activity has two entries to
	// order and the state listings are not empty.
	_, err = be.CreateWorkspace(ctx, backend.WorkspaceCreate{Key: "old", Name: "Retired"})
	require.NoError(t, err)
	_, err = be.ArchiveWorkspace(ctx, "old", "")
	require.NoError(t, err)
	_, err = be.RestoreWorkspace(ctx, "old", "")
	require.NoError(t, err)
	_, err = be.ArchiveWorkspace(ctx, "old", "")
	require.NoError(t, err)

	ids := make([]string, 0, 8)
	for range 4 {
		for _, workspace := range []string{"awb", "web"} {
			issue, err := be.CreateIssue(ctx, backend.IssueCreate{
				Workspace: workspace, Title: "tied", Description: "tied parser text",
				Labels: []string{"b", "a"}})
			require.NoError(t, err)
			ids = append(ids, issue.ID)
		}
	}
	blocker, parent := ids[0], ids[4]
	for _, id := range ids[1:4] {
		_, err := be.AddRelation(ctx, id,
			backend.RelationRequest{Type: domain.RelBlockedBy, Other: blocker}, "")
		require.NoError(t, err)
	}
	for _, id := range ids[5:8] {
		_, err := be.AddRelation(ctx, id,
			backend.RelationRequest{Type: domain.RelHasParent, Other: parent}, "")
		require.NoError(t, err)
	}
	// Two assignees on one issue, so sort=assignee joins a list rather than a
	// single name and the position order it joins in is what decides.
	for _, who := range []string{"zoe", "adam"} {
		_, err := be.Claim(ctx, ids[5], backend.ClaimRequest{Assignee: who}, "")
		require.NoError(t, err)
	}
	_, err = be.Claim(ctx, ids[6], backend.ClaimRequest{Assignee: "mikael"}, "")
	require.NoError(t, err)
	for _, body := range []string{"one", "two"} {
		_, err := be.AddComment(ctx, blocker, body)
		require.NoError(t, err)
	}
	for _, name := range []string{"c.txt", "a.txt", "b.txt"} {
		_, err := be.AddAttachment(ctx, blocker, backend.AttachmentCreate{
			Name: name, ContentType: "text/plain", Content: strings.NewReader("x")})
		require.NoError(t, err)
	}

	paths := []string{
		"/api/issues", "/api/ready", "/api/blocked", "/api/search?q=parser",
		"/api/issues/suggestions?q=tied",
		"/api/navigation?q=tied", "/api/navigation?q=awb",
		"/api/workspaces", "/api/workspaces?state=archived", "/api/workspaces?state=all",
		"/api/workspaces/old/activity",
		"/api/preferences/workspaces", "/api/workspaces/awb/members",
		"/api/users", "/api/labels", "/api/assignees",
		"/api/issues/" + blocker, "/api/issues/" + blocker + "/attachments",
		"/api/issues/" + blocker + "/activity",
		"/api/issues/" + blocker + "/activity?kind=comment",
		"/api/issues/" + parent + "/tree",
	}
	for _, key := range domain.SortKeys {
		paths = append(paths, "/api/issues?sort="+string(key), "/api/issues?sort=-"+string(key))
	}
	for _, key := range []string{"relevance", "-relevance"} {
		paths = append(paths, "/api/search?q=parser&sort="+key)
	}
	for _, key := range []string{"key", "active", "updated"} {
		paths = append(paths, "/api/workspaces?sort="+key, "/api/workspaces?sort=-"+key)
	}

	credentials := basicAuth("mikael", "hunter2")

	// Navigation returns three independently capped groups. A query that reached
	// only one of them would leave the other two orders untested while the
	// response as a whole still compared equal, so check all three are populated.
	_, navigation := get(t, h, http.MethodGet, "/api/navigation?q=awb", credentials...)
	var groups struct {
		Issues     []domain.Issue     `json:"issues"`
		Workspaces []domain.Workspace `json:"workspaces"`
		Users      []domain.User      `json:"users"`
	}
	require.NoError(t, json.Unmarshal([]byte(navigation), &groups))
	assert.NotEmpty(t, groups.Issues)
	assert.NotEmpty(t, groups.Workspaces)
	assert.NotEmpty(t, groups.Users)

	for _, path := range paths {
		resp, first := get(t, h, http.MethodGet, path, credentials...)
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.NotEmpty(t, first, path)
		// An empty array is a non-empty body, so comparing one against itself
		// would say nothing about the order of a listing that returned no rows.
		// Every path above is seeded to return some.
		assert.NotEqual(t, "[]", strings.TrimSpace(first),
			"%s returned no rows, so its order went untested", path)
		// Two invocations establish the promised comparison for every path;
		// further repetitions would not expand endpoint or sort-key coverage.
		_, again := get(t, h, http.MethodGet, path, credentials...)
		assert.Equal(t, first, again, path)
	}
}
