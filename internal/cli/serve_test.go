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
	"net/http"
	"net/http/httptest"
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

	"github.com/mikaelstaldal/go-server-common/auth"
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
	return newServeHandlerAuth(t, opts, nil)
}

func newServeHandlerAuth(t *testing.T, opts serveOptions, htpasswd *auth.HtpasswdFile) http.Handler {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	h, err := buildHandler(local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael"),
		openapi.New(raw), htpasswd, opts)
	require.NoError(t, err)
	return h
}

// credentials is one htpasswd entry, mikael:hunter2 at the lowest bcrypt cost
// there is, because these tests care that the file is read and not how long
// hashing takes.
const credentials = "mikael:$2a$04$AL546dro6bMBAm/zEI.Yzet3uZisN1MTC1Rt4ut9s5F3qfJrx27iS\n"

func newHtpasswd(t *testing.T) *auth.HtpasswdFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "htpasswd")
	require.NoError(t, os.WriteFile(path, []byte(credentials), 0o600))
	file, err := loadHtpasswd(path)
	require.NoError(t, err)
	return file
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

	resp, _ := get(t, h, http.MethodPost, "/api/projects", "Origin", "http://[::1]:7777")
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

// The challenge is the first response a browser sees, so it is the one that has
// to carry the headers: a host pinned only on the way past authentication is
// pinned after the password has been typed rather than before.
func TestTheAuthenticationChallengeCarriesTheSecurityHeaders(t *testing.T) {
	h := newServeHandlerAuth(t,
		serveOptions{addr: "127.0.0.1", port: 7777, https: true, basicAuthRealm: "awb"},
		newHtpasswd(t))

	resp, _ := get(t, h, http.MethodGet, "/")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `realm="awb"`)
	assert.Equal(t, "max-age=31536000", resp.Header.Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))

	// And the credentials the file holds still get through.
	resp, _ = get(t, h, http.MethodGet, "/api/identity",
		"Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("mikael:hunter2")))
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
	go func() { stopped <- runServer(ctx, &env{stderr: &out}, opts, http.NotFoundHandler()) }()

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

	rec := call(http.MethodPost, "/api/projects", "application/json", `{"key":"awb"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = call(http.MethodPost, "/api/issues", "application/json",
		`{"project":"awb","title":"Parser crashes"}`)
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
		`{"project":"awb","title":"`+strings.Repeat("x", maxRequestBody)+`"}`)
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
	assert.False(t, upload(http.MethodPost, "/api/issues/a/b/attachments"))
	assert.False(t, upload(http.MethodPost, "/api/issues"))

	assert.True(t, download(http.MethodGet, "/api/attachments/3f2a91c40d17/content"))
	assert.False(t, download(http.MethodGet, "/api/attachments/3f2a91c40d17"))
	assert.False(t, download(http.MethodDelete, "/api/attachments/3f2a91c40d17/content"))
	assert.False(t, download(http.MethodGet, "/api/attachments//content"))
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
	h, err := buildHandler(be, openapi.New(raw), nil,
		serveOptions{port: 7777, basicAuthRealm: "awb"})
	require.NoError(t, err)

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)

	_, err = be.CreateProject(t.Context(), backend.ProjectCreate{Key: "awb"})
	require.NoError(t, err)
	issue, err := be.CreateIssue(t.Context(),
		backend.IssueCreate{Project: "awb", Title: "Parser crashes"})
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
	_, content, err := client.OpenAttachment(t.Context(), attachment.ID)
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
	rec := call(http.MethodPost, "/api/projects", "application/json", `{"key":"awb"}`, false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = call(http.MethodPost, "/api/issues", "application/json",
		`{"project":"awb","title":"Parser crashes"}`, false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var issue domain.Issue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))

	rec = call(http.MethodPost, "/api/issues/"+issue.ID+"/attachments?name=trace.txt",
		"application/octet-stream", "boom\n", false)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var attachment domain.Attachment
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &attachment))

	// A client that would take gzip is given none for the content, and gets the
	// bytes as they were stored.
	rec = call(http.MethodGet, "/api/attachments/"+attachment.ID+"/content", "", "", true)
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
	rec = call(http.MethodGet, "/api/attachments/"+attachment.ID, "", "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
}
