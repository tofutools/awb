package cli

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mikaelstaldal/go-server-common/auth"
	"github.com/mikaelstaldal/go-server-common/csrf"
	"github.com/mikaelstaldal/go-server-common/httputil"
	"github.com/mikaelstaldal/go-server-common/recovery"
	commonweb "github.com/mikaelstaldal/go-server-common/web"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/handler"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/openapi"
	"github.com/tofutools/awb/internal/storage"
	"github.com/tofutools/awb/web"
)

// Server timeouts and the transport body cap.
//
// The cap is a transport limit and not a second validation rule: it sits far
// above anything the field maxima permit for one issue or one project, so no
// body those rules would accept is ever refused for its size.
//
// Uploading an attachment is the one request whose body is not a description
// of an issue, and it gets maxAttachmentBody instead. The two are separate on
// purpose: raising the general cap to make room for files would let any caller
// make the server buffer that much JSON.
//
// The two attachment requests get a longer deadline than everything else for
// the same reason: moving a file of the maximum size over a slow link takes
// longer than any request describing an issue ever could, and a bound short
// enough for those would make the size limit depend on the connection. It is
// granted per request rather than by raising readTimeout and writeTimeout, so
// no other request gets to hold a connection that long.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	contentTimeout    = 10 * time.Minute
	idleTimeout       = time.Minute
	shutdownTimeout   = 10 * time.Second
	maxRequestBody    = 1 << 20
)

// maxAttachmentBody is the transport cap on an upload, and sits above the
// domain's attachment maximum on purpose.
//
// A file over that maximum has to be refused by the rule rather than by the
// transport, because only the rule's refusal carries an exit code: 413 has
// none and collapses to 1, so a file the CLI refuses with exit 2 in direct
// mode would exit 1 through a server. The rule stops reading one byte past the
// maximum, so the extra room is never occupied; the cap stays as the backstop
// against a client that keeps sending after the rule has stopped listening.
const maxAttachmentBody = 2 * domain.MaxAttachmentBytes

// hsts is sent when --https says a TLS-terminating proxy sits in front. A year,
// and this host only: awb may be one application among several on a domain, and
// includeSubDomains would speak for all of them.
const hsts = "max-age=31536000"

// serveOptions is everything the server needs beyond the database: where it
// listens, where it is published, and who may call it.
type serveOptions struct {
	addr           string
	port           int
	publicURL      string
	https          bool
	corsOrigins    []string
	basicAuthRealm string
}

// listenAddr is the host:port to bind. An empty --addr means every interface.
func (o serveOptions) listenAddr() string {
	return net.JoinHostPort(o.addr, strconv.Itoa(o.port))
}

// validate checks everything the flags can be wrong about on their own, so the
// command fails on them before it opens a database or a port.
func (o serveOptions) validate() error {
	if o.port < 1 || o.port > 65535 {
		return awberr.Usagef("--port: %d is not a port number", o.port)
	}
	// --addr used to carry the port too, so an address that looks like
	// host:port is somebody carrying the old form forward. Refuse it rather
	// than binding a host named "127.0.0.1:7777". An IPv6 address is full of
	// colons and is not that mistake.
	if strings.Contains(o.addr, ":") && net.ParseIP(o.addr) == nil {
		return awberr.Usagef("--addr: %s is not an address; the port goes in --port", o.addr)
	}
	publicURL, err := parsePublicURL(o.publicURL)
	if err != nil {
		return err
	}
	if _, err := basePathOf(publicURL); err != nil {
		return err
	}
	// The two flags describe one deployment, so they cannot disagree about it.
	// A browser ignores Strict-Transport-Security received over plain HTTP, so
	// an http public URL with --https is a deployment the operator believes is
	// protected and is not.
	if o.https && publicURL != nil && publicURL.Scheme != "https" {
		return awberr.Usagef(
			"--https says a proxy terminates TLS, and --public-url %s says it does not",
			o.publicURL)
	}
	return nil
}

// serverOrigin is the origin a browser names when it reaches this server, which
// is what the cross-site write check compares against.
//
// Behind a reverse proxy the browser names the proxy, not this listener, so
// --public-url is where it comes from when it is given.
func (o serveOptions) serverOrigin() (string, error) {
	origin, err := csrf.ResolveServerOrigin(o.publicURL, o.originHost(), o.port)
	if err != nil {
		return "", awberr.Usagef(
			"--public-url: %s is not a full URL, like https://example.com/awb/", o.publicURL)
	}
	return origin, nil
}

// originHost is the host in that origin when there is no --public-url. A server
// bound to every interface has no host of its own, and loopback is the one a
// browser on this machine reaches it by. An IPv6 address is bracketed, because
// an origin is a URL authority and that is the form a browser sends.
func (o serveOptions) originHost() string {
	if o.addr == "" {
		return "127.0.0.1"
	}
	if strings.Contains(o.addr, ":") {
		return "[" + o.addr + "]"
	}
	return o.addr
}

func newServeCommand(e *env) *cobra.Command {
	var (
		opts          serveOptions
		identity      string
		basicAuthFile string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the HTTP API and the bundled read-only web UI",
		Long: "Serve the local database over HTTP, so that things other than the CLI can\n" +
			"reach it: third-party user interfaces, dashboards and integrations.\n\n" +
			"There is authentication but no authorization: every user the server knows\n" +
			"may do everything every other one may, and credentials serve only to say\n" +
			"who is calling.\n\n" +
			"Without --basic-auth-file there is no authentication and any client that can\n" +
			"reach the port has full read and write access, which is why the default\n" +
			"binds loopback.\n\n" +
			"The server never terminates TLS. To publish it beyond this machine, put a\n" +
			"reverse proxy in front of it: --public-url is the URL it is published under,\n" +
			"which the proxy maps to this server with that base path stripped, and --https\n" +
			"tells browsers to keep using TLS.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// What the flags say is checked before anything is opened, so a
			// flag that could never work is reported as the usage error it is
			// rather than behind an unrelated failure to find a database.
			if err := opts.validate(); err != nil {
				return err
			}
			if cmd.Flags().Changed("identity") && cmd.Flags().Changed("basic-auth-file") {
				return awberr.Usagef("--identity and --basic-auth-file are mutually exclusive")
			}

			cfg, err := e.requireLocal("serve")
			if err != nil {
				return err
			}

			db, err := storage.Open(cmd.Context(), cfg.DB)
			if err != nil {
				return err
			}
			defer db.Close() //nolint:errcheck // closing on the way out

			htpasswd, err := loadHtpasswd(basicAuthFile)
			if err != nil {
				return err
			}

			fixedIdentity := ""
			if htpasswd == nil {
				if fixedIdentity, err = resolveServerIdentity(cmd, cfg, identity); err != nil {
					return err
				}
			}

			base := local.New(db, storage.NewBlobs(cfg.Attachments), fixedIdentity)
			httpHandler, err := buildHandler(base, e.openAPI, htpasswd, opts)
			if err != nil {
				return err
			}

			return runServer(cmd.Context(), e, opts, httpHandler)
		},
	}

	cmd.Flags().StringVar(&opts.addr, "addr", "127.0.0.1",
		"address to listen on; empty for every interface")
	cmd.Flags().IntVar(&opts.port, "port", 7777, "port to listen on")
	cmd.Flags().StringVar(&opts.publicURL, "public-url", "",
		"the URL a reverse proxy publishes this server under, e.g. https://example.com/awb/")
	cmd.Flags().BoolVar(&opts.https, "https", false,
		"a reverse proxy in front terminates TLS: send Strict-Transport-Security")
	cmd.Flags().StringArrayVar(&opts.corsOrigins, "cors-origin", nil,
		"allow this exact browser origin to call the API; repeatable")
	cmd.Flags().StringVar(&identity, "identity", "",
		"the single identity an unauthenticated server attributes every request to")
	cmd.Flags().StringVar(&basicAuthFile, "basic-auth-file", "",
		"htpasswd file of username:bcrypt-hash entries")
	cmd.Flags().StringVar(&opts.basicAuthRealm, "basic-auth-realm", "awb",
		"realm presented to clients that supply no credentials")
	return cmd
}

// loadHtpasswd reads the credentials file strictly.
//
// Every line is read strictly: serve fails, naming the file and the line, when
// the file cannot be read, when it holds no entry at all, or when any line is
// not a username:bcrypt-hash pair — an MD5, crypt or SHA-1 hash included. A
// line skipped silently would be a login the operator believes in and the
// server does not.
//
// Every username in it must be a valid assignee, because that is what it
// becomes: one that is not fails serve the same way, refused rather than
// folded.
func loadHtpasswd(path string) (*auth.HtpasswdFile, error) {
	if path == "" {
		return nil, nil
	}
	file, err := auth.LoadHtpasswdStrict(path, func(username string) error {
		_, err := domain.ValidateAssignee(username)
		return err
	})
	if err != nil {
		return nil, awberr.Runtimef("%s", err.Error())
	}
	return file, nil
}

// resolveServerIdentity resolves the single identity an unauthenticated server
// attributes every request to, from the same sources and in the same order as
// the CLI's own: --identity, else AWB_IDENTITY, else identity in the user
// configuration file, else the OS username folded to the assignee set.
//
// An explicit --identity outside that set is a usage error rather than
// something to fold, and a resolution that yields nothing at all fails rather
// than starting a server whose every claim would fail.
func resolveServerIdentity(cmd *cobra.Command, cfg *config.Config, flag string) (string, error) {
	if cmd.Flags().Changed("identity") {
		if _, err := domain.ValidateAssignee(flag); err != nil {
			return "", awberr.Usagef("--identity: %s", err.Error())
		}
		return flag, nil
	}
	if cfg.Identity == "" {
		return "", awberr.Runtimef(
			"no identity to attribute requests to: give --identity, or set AWB_IDENTITY")
	}
	return cfg.Identity, nil
}

// basePathChars is what a base path may be built from. A path outside this set
// is refused rather than escaped, because it goes into an HTML attribute and
// into every URL the UI resolves.
var basePathChars = regexp.MustCompile(`^[A-Za-z0-9._~/-]*$`)

// parsePublicURL is --public-url as the two things the server takes from it: an
// origin a browser can name, and a base path the UI can resolve against. It
// returns nil when the flag was not given.
//
// It has to be a complete http or https URL and nothing more. A scheme-relative
// //example.com/awb would yield the origin "://example.com", which no browser
// can ever send, so every write would be refused as cross-site; credentials, a
// query or a fragment are not part of a base URL and mean the operator wrote
// down something other than what this flag names. Each is refused at startup
// rather than serving something that cannot work.
func parsePublicURL(publicURL string) (*url.URL, error) {
	if publicURL == "" {
		return nil, nil
	}
	parsed, err := url.Parse(publicURL)
	if err != nil {
		return nil, awberr.Usagef("--public-url: %s is not a URL", publicURL)
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return nil, awberr.Usagef(
			"--public-url: %s must be an http or https URL, like https://example.com/awb/",
			publicURL)
	case parsed.Host == "":
		return nil, awberr.Usagef(
			"--public-url: %s names no host, and a host is what a browser sends", publicURL)
	case parsed.User != nil:
		return nil, awberr.Usagef("--public-url: %s must carry no credentials", publicURL)
	case parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "":
		return nil, awberr.Usagef("--public-url: %s must be a base URL, with no query or fragment",
			publicURL)
	}
	// url.Parse asks only that a port be digits, so a URL naming a port no TCP
	// connection could ever be made to parses happily and yields an origin no
	// browser can send.
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, awberr.Usagef("--public-url: %s is not a port number", port)
		}
	}
	return parsed, nil
}

// basePathOf is the path component of --public-url, normalised to the form
// <base href> wants: it starts and ends with a single "/", and is "/" when no
// public URL is given or it names an origin with no path.
//
// The reverse proxy strips that base before the request arrives — which is what
// openapi.yaml's single "/" server URL says as well — so it never reaches the
// router. It reaches only the shell, where it is what the UI's relative URLs
// resolve against.
func basePathOf(publicURL *url.URL) (string, error) {
	if publicURL == nil {
		return "/", nil
	}
	path := publicURL.Path
	if path == "" || path == "/" {
		return "/", nil
	}
	if !basePathChars.MatchString(path) {
		return "", awberr.Usagef(
			"--public-url: the path %s may hold only the characters A-Z a-z 0-9 . _ ~ - /", path)
	}
	// A leading "//" would make <base href> protocol-relative, pointing the
	// whole UI at another host.
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", awberr.Usagef("--public-url: the path %s must start with a single /", path)
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path, nil
}

// buildHandler assembles the middleware chain, outermost first.
func buildHandler(base *local.Backend, document *openapi.Document, htpasswd *auth.HtpasswdFile,
	opts serveOptions) (http.Handler, error) {
	// Whichever of the two identity mechanisms is in force, the request has
	// exactly one identity, so the surface below never has to handle its absence.
	// The username arrives in the request context, which is what the generated
	// server passes on to the handler.
	backendFor := func(ctx context.Context) backend.Backend {
		if username, ok := auth.UsernameFromContext(ctx); ok {
			// What a caller states explicitly is still honoured; there being no
			// authorization, one user may claim an issue for another.
			return base.WithIdentity(username)
		}
		return base
	}

	// What each operation accepts is read from the document rather than
	// restated beside the handler; see the handler package.
	operations, err := document.Operations()
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read the OpenAPI document")
	}
	apiServer, err := handler.NewServer(backendFor, operations)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "build the API server")
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read the bundled web UI")
	}
	uiHandler, err := httputil.StaticHandler(staticFS)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "serve the bundled web UI")
	}

	publicURL, err := parsePublicURL(opts.publicURL)
	if err != nil {
		return nil, err
	}
	basePath, err := basePathOf(publicURL)
	if err != nil {
		return nil, err
	}
	shell, err := web.Shell(basePath)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read the bundled web UI")
	}

	// Compression is applied per route rather than once around everything,
	// because StaticHandler already gzips what it serves and wrapping it again
	// would encode the body twice. A browser decodes one layer and is left with
	// the other; curl, which does not ask for gzip by default, sees nothing
	// wrong. Keeping the two apart is what stops that.
	withAPI := func(h http.Handler) http.Handler {
		return recovery.Middleware(gzipExcept(isAttachmentDownload, handler.NoStore(h)))
	}

	// Everything under /api/ is the JSON API and /openapi.json and /openapi.yaml
	// are the document; every other path belongs to the web UI.
	root := http.NewServeMux()
	root.Handle("/api/", withAPI(apiServer))
	root.Handle("GET /openapi.json", recovery.Middleware(httputil.Gzip(document.JSONHandler())))
	root.Handle("GET /openapi.yaml", recovery.Middleware(httputil.Gzip(document.YAMLHandler())))
	root.Handle("/", recovery.Middleware(web.SPAHandler(uiHandler, staticFS, shell)))

	csp, err := contentSecurityPolicy()
	if err != nil {
		return nil, err
	}

	chain := handler.CORS(opts.corsOrigins, root)

	// A request that is not a safe method must carry an Origin or Referer naming
	// the server itself or an allowed --cors-origin, because a browser attaches
	// basic-authentication credentials to cross-site requests of its own accord.
	// One carrying neither header is allowed, that being what every non-browser
	// client sends, and the CLI is one of them.
	serverOrigin, err := opts.serverOrigin()
	if err != nil {
		return nil, err
	}
	chain = csrf.MiddlewareOrigins(append([]string{serverOrigin}, opts.corsOrigins...)...)(chain)

	if htpasswd != nil {
		// Nothing is exempt: the API, the OpenAPI document and the web UI all sit
		// behind it.
		chain = htpasswd.Middleware(opts.basicAuthRealm)(chain)
	}

	// Strict-Transport-Security only when a proxy terminates TLS: sent over
	// plain HTTP it is ignored, and sent by a server reachable over plain HTTP
	// on purpose it would break it.
	strictTransport := ""
	if opts.https {
		strictTransport = hsts
	}

	// Outside the authentication, because the first response a browser sees is
	// the challenge: headers set only on the way past it would pin the host
	// after the password had already been typed rather than before.
	chain = httputil.SecurityHeaders(httputil.SecurityHeadersOptions{
		CSP:            csp,
		ReferrerPolicy: "same-origin",
		HSTS:           strictTransport,
	})(chain)

	return transferLimits(chain), nil
}

// transferLimits caps how much of a request body the server will read, and
// gives the two requests that carry a file the time to carry it.
//
// Everything gets maxRequestBody, which is far above anything the field maxima
// permit; an attachment upload gets the domain's attachment maximum, that
// body being a file rather than a description of one. Both have to be applied
// before the router, which is inside the API server, so this recognises the
// two paths rather than reading them out of the document — a wider limit on a
// path that turns out to serve nothing is refused by the router a moment later
// either way.
//
// Extending the deadline is best effort: a server that will not have it moved
// leaves the request under the general timeouts, which is the behaviour
// everything else already has.
func transferLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxRequestBody)
		deadline := time.Now().Add(contentTimeout)
		switch {
		case isAttachmentUpload(r):
			limit = maxAttachmentBody
			_ = http.NewResponseController(w).SetReadDeadline(deadline)
		case isAttachmentDownload(r):
			_ = http.NewResponseController(w).SetWriteDeadline(deadline)
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// isAttachmentUpload recognises POST /api/issues/{id}/attachments, the one
// request whose body is a file.
func isAttachmentUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && matchPath(r, "api", "issues", "*", "attachments")
}

// isAttachmentDownload recognises GET
// /api/issues/{id}/attachments/{name}/content, the one response that is a
// file.
func isAttachmentDownload(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		matchPath(r, "api", "issues", "*", "attachments", "*", "content")
}

// matchPath compares the request's path against a pattern in which "*" stands
// for exactly one non-empty segment.
//
// It reads the escaped path, so a segment is what the client actually sent
// between two slashes. An attachment's name travels percent-encoded and cannot
// contain a slash; reading the decoded path would let one that had smuggled an
// encoded slash look like two segments and slip past.
func matchPath(r *http.Request, pattern ...string) bool {
	segments := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	if len(segments) != len(pattern) {
		return false
	}
	for i, want := range pattern {
		if segments[i] == "" || (want != "*" && segments[i] != want) {
			return false
		}
	}
	return true
}

// gzipExcept compresses every response but the ones skip names.
//
// The one it skips is an attachment's content. Everything else the API answers
// is JSON, which compresses to a fraction of itself; an attachment is opaque
// bytes the server never looks at, and is as likely as not already compressed
// — a screenshot, a zip, a captured core. Compressing those spends the time
// and about a megabyte of compressor state per download to make them no
// smaller, and the state is per concurrent request, which is exactly where a
// server should not be spending memory.
//
// That response is also the only one that states its own Content-Length, and
// compressing it would leave that header describing a body of another length:
// this middleware clears the header on its way in, and the generated encoder
// sets it again on the way out. So the two belong together — putting the
// content back through the compressor would need the header dropped in the
// same change.
func gzipExcept(skip func(*http.Request) bool, next http.Handler) http.Handler {
	compressed := httputil.Gzip(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skip(r) {
			next.ServeHTTP(w, r)
			return
		}
		compressed.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy pins the UI's script sources to the import map the
// bundled page carries, so the committed vendor bundles load and nothing else
// does.
func contentSecurityPolicy() (string, error) {
	importMapHash, err := commonweb.ImportMapCSPHash(web.Static)
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "read the bundled web UI")
	}
	return "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
		"base-uri 'self'; form-action 'self'; object-src 'none'; frame-ancestors 'none'; " +
		"script-src 'self' " + importMapHash, nil
}

func runServer(ctx context.Context, e *env, opts serveOptions, h http.Handler) error {
	// serve is the one command that runs until it is stopped rather than
	// answering and exiting, so what it writes is a log and is stamped with the
	// time. It goes to the writer Execute was handed rather than to the
	// package-level logger's os.Stderr, so that redirecting the command's
	// output still captures all of it.
	logger := log.New(e.stderr, "", log.LstdFlags)

	addr := opts.listenAddr()
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		// Otherwise the server's own complaints — a request too malformed to
		// reach a handler, a panic a handler did not recover — go to the
		// package-level logger and are the only unstamped lines in the log.
		ErrorLog: logger,
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		logger.Printf("awb serving on http://%s/", addr)
		if opts.publicURL != "" {
			logger.Printf("published at %s", opts.publicURL)
		}
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return awberr.Wrap(awberr.Runtime, err, "serve on %s", addr)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "shut down")
		}
		return nil
	}
}
