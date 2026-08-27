package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mikaelstaldal/go-server-common/auth"
	"github.com/mikaelstaldal/go-server-common/csrf"
	"github.com/mikaelstaldal/go-server-common/httputil"
	"github.com/mikaelstaldal/go-server-common/recovery"
	commonweb "github.com/mikaelstaldal/go-server-common/web"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/handler"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/storage"
	"github.com/tofutools/awb/web"
)

// Server timeouts and the transport body cap of SPEC §9.
//
// The cap is a transport limit and not a second validation rule: it sits far
// above anything the field maxima of §2.6 permit for one issue or one project,
// so no body those rules would accept is ever refused for its size.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = time.Minute
	shutdownTimeout   = 10 * time.Second
	maxRequestBody    = 1 << 20
)

func newServeCommand(e *env) *cobra.Command {
	var (
		addr           string
		corsOrigins    []string
		identity       string
		basicAuthFile  string
		basicAuthRealm string
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
			"binds loopback.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := e.requireLocal("serve")
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("identity") && cmd.Flags().Changed("basic-auth-file") {
				return awberr.Usagef("--identity and --basic-auth-file are mutually exclusive")
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

			base := local.New(db, fixedIdentity)
			httpHandler, err := buildHandler(base, htpasswd, basicAuthRealm, addr, corsOrigins)
			if err != nil {
				return err
			}

			return runServer(cmd.Context(), e, addr, httpHandler)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "address to listen on")
	cmd.Flags().StringArrayVar(&corsOrigins, "cors-origin", nil,
		"allow this exact browser origin to call the API; repeatable")
	cmd.Flags().StringVar(&identity, "identity", "",
		"the single identity an unauthenticated server attributes every request to")
	cmd.Flags().StringVar(&basicAuthFile, "basic-auth-file", "",
		"htpasswd file of username:bcrypt-hash entries")
	cmd.Flags().StringVar(&basicAuthRealm, "basic-auth-realm", "awb",
		"realm presented to clients that supply no credentials")
	return cmd
}

// loadHtpasswd reads the credentials file strictly (SPEC §6).
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

// buildHandler assembles the middleware chain, outermost first.
func buildHandler(base *local.Backend, htpasswd *auth.HtpasswdFile, realm, addr string,
	corsOrigins []string) (http.Handler, error) {
	// Whichever of the two identity mechanisms is in force, the request has
	// exactly one identity, so the surface below never has to handle its
	// absence (SPEC §6).
	backendFor := func(r *http.Request) backend.Backend {
		if username, ok := auth.UsernameFromContext(r.Context()); ok {
			// What a caller states explicitly is still honoured; there being no
			// authorization, one user may claim an issue for another.
			return base.WithIdentity(username)
		}
		return base
	}

	mux := http.NewServeMux()
	handler.New(backendFor).Routes(mux)

	staticFS, err := web.StaticFS()
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read the bundled web UI")
	}
	uiHandler, err := httputil.StaticHandler(staticFS)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "serve the bundled web UI")
	}

	// Compression is applied per route rather than once around everything,
	// because StaticHandler already gzips what it serves and wrapping it again
	// would encode the body twice. A browser decodes one layer and is left with
	// the other; curl, which does not ask for gzip by default, sees nothing
	// wrong. Keeping the two apart is what stops that.
	withAPI := func(h http.Handler) http.Handler {
		return recovery.Middleware(httputil.Gzip(handler.NoStore(h)))
	}

	// Everything under /api/ is the JSON API and /openapi.json and
	// /openapi.yaml are the document; every other path belongs to the web UI
	// (SPEC §6).
	root := http.NewServeMux()
	root.Handle("/api/", withAPI(mux))
	root.Handle("GET /openapi.json", recovery.Middleware(httputil.Gzip(api.JSONHandler())))
	root.Handle("GET /openapi.yaml", recovery.Middleware(httputil.Gzip(api.YAMLHandler())))
	root.Handle("/", recovery.Middleware(web.SPAHandler(uiHandler, staticFS)))

	csp, err := contentSecurityPolicy()
	if err != nil {
		return nil, err
	}

	chain := handler.CORS(corsOrigins, root)

	// A request that is not a safe method must carry an Origin or Referer
	// naming the server itself or an allowed --cors-origin, because a browser
	// attaches basic-authentication credentials to cross-site requests of its
	// own accord. One carrying neither header is allowed, that being what every
	// non-browser client sends, and the CLI is one of them (SPEC §6.2).
	serverOrigin, err := csrf.ResolveServerOrigin("", hostOf(addr), portOf(addr))
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "resolve the server origin")
	}
	chain = csrf.MiddlewareOrigins(append([]string{serverOrigin}, corsOrigins...)...)(chain)

	chain = httputil.SecurityHeaders(httputil.SecurityHeadersOptions{
		CSP:            csp,
		ReferrerPolicy: "same-origin",
	})(chain)

	if htpasswd != nil {
		// Nothing is exempt: the API, the OpenAPI document and the web UI all
		// sit behind it (SPEC §6).
		chain = htpasswd.Middleware(realm)(chain)
	}

	return http.MaxBytesHandler(chain, maxRequestBody), nil
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

func runServer(ctx context.Context, e *env, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		_, _ = fmt.Fprintf(e.stderr, "awb serving on http://%s/\n", addr)
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

// hostOf and portOf split a listen address for the origin the CSRF check
// compares against.
func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return "127.0.0.1"
	}
	return host
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return number
}
