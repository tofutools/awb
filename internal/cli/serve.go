package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	stdhttputil "net/http/httputil"
	"net/url"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GiGurra/boa/pkg/boa"
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
// above anything the field maxima permit for one issue or one workspace, so no
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
	proxyRequestLimit = 30 * time.Second
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
	// realmGiven says --basic-auth-realm was typed rather than defaulted,
	// which is what makes it evidence of an intention to authenticate.
	realmGiven bool
	noAuth     bool
	proxyTo    string
}

// defaultBasicAuthRealm is what a client that supplies no credentials is asked
// in the name of. It is resolved here rather than by a default tag, because
// the flag's presence is itself an answer and a filled-in default would hide
// it; see exposure.
const defaultBasicAuthRealm = "awb"

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
	if _, err := parseProxyURL(o.proxyTo); err != nil {
		return err
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
	// A realm is the name a server authenticates in, and a server started with
	// --no-auth never asks anybody for anything. The two describe different
	// servers.
	if o.noAuth && o.realmGiven {
		return awberr.Usagef(
			"--no-auth serves without authentication, so there is no realm to present in")
	}
	return nil
}

// exposure says why this server looks like one meant to be reached from
// somewhere other than this machine, or is empty when it does not.
//
// Each of the four is a statement about a deployment: a public URL and TLS
// termination describe a reverse proxy publishing it, a realm is the name it
// authenticates in, and a binding that is not loopback lets other machines
// reach the port. On a database that holds no user with a password, any of
// them is a server that would hand full read and write access to whoever
// arrives, which is close enough to a mistake that it is refused rather than
// served.
func (o serveOptions) exposure() string {
	switch {
	case o.publicURL != "":
		return "--public-url says a reverse proxy publishes this server"
	case o.https:
		return "--https says a reverse proxy terminates TLS in front of this server"
	case o.realmGiven:
		return "--basic-auth-realm names the realm this server authenticates in"
	case o.addr == "":
		return "--addr is empty, which binds every interface"
	case !isLoopbackAddr(o.addr):
		return fmt.Sprintf("--addr %s is not a loopback address", o.addr)
	}
	return ""
}

// isLoopbackAddr reports whether binding this --addr keeps the port on this
// machine. The name resolves to a loopback address on every system awb runs
// on; any other name is a claim about DNS that cannot be checked here, and is
// treated as reaching further than this machine.
func isLoopbackAddr(addr string) bool {
	if strings.EqualFold(addr, "localhost") {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
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

type serveParams struct {
	Addr           string   `long:"addr" default:"127.0.0.1" optional:"true" help:"address to listen on; empty for every interface"`
	Port           int      `long:"port" default:"7777" optional:"true" help:"port to listen on"`
	PublicURL      string   `long:"public-url" optional:"true" help:"the URL a reverse proxy publishes this server under, e.g. https://example.com/awb/"`
	HTTPS          bool     `long:"https" optional:"true" help:"a reverse proxy in front terminates TLS: send Strict-Transport-Security"`
	CORSOrigins    []string `long:"cors-origin" collection:"array" optional:"true" help:"allow this exact browser origin to call the API; repeatable"`
	Identity       *string  `long:"identity" help:"the identity a server that authenticates nobody attributes every request to"`
	BasicAuthRealm *string  `long:"basic-auth-realm" help:"realm presented to clients that supply no credentials (default awb)"`
	NoAuth         bool     `long:"no-auth" optional:"true" help:"authenticate nobody, whatever the database holds; every client that reaches the port has full access"`
	ProxyTo        string   `long:"proxy-to" optional:"true" help:"serve the bundled UI while proxying API requests to this awb server"`
}

func (p *serveParams) options() serveOptions {
	realm := defaultBasicAuthRealm
	if p.BasicAuthRealm != nil {
		realm = *p.BasicAuthRealm
	}
	return serveOptions{
		addr: p.Addr, port: p.Port, publicURL: p.PublicURL, https: p.HTTPS,
		corsOrigins: p.CORSOrigins, basicAuthRealm: realm,
		realmGiven: p.BasicAuthRealm != nil, noAuth: p.NoAuth,
		proxyTo: p.ProxyTo,
	}
}

func newServeCommand(e *env) *cobra.Command {
	return boa.CmdT[serveParams]{
		Use:   "serve",
		Short: "Serve the HTTP API and the bundled web UI",
		Long: "Serve the local database over HTTP, so that things other than the CLI can\n" +
			"reach it: third-party user interfaces, dashboards and integrations.\n\n" +
			"With --proxy-to, serve this binary's bundled UI without opening a local\n" +
			"database, and proxy its API requests to another awb server. This is intended\n" +
			"for testing a locally built UI against an existing installation.\n\n" +
			"Authentication and authorization come from the database. A database that\n" +
			"holds at least one user with a password — see awb user — asks every request\n" +
			"for a username and password and answers it with that user's permissions. One\n" +
			"that holds none authenticates nobody, and any client that can reach the port\n" +
			"has full read and write access, which is why the default binds loopback. A\n" +
			"user without a password is an assignee, not an account: it never turns\n" +
			"authentication on.\n\n" +
			"Which of the two it is, is decided per request rather than at startup, so\n" +
			"adding the first password closes the door without a restart. The door does\n" +
			"not open again by itself: deleting the last such user leaves the server\n" +
			"answering nothing until another exists, rather than serving everybody.\n\n" +
			"A server over a database whose accounts are all gone starts, and refuses\n" +
			"every request until one is added; it is recovered without a restart, exactly\n" +
			"as a running one is. What refuses to start is a server that would\n" +
			"authenticate nobody where that looks like a mistake: one over a database that\n" +
			"never held a user with a password, bound to anything but loopback, or given\n" +
			"--public-url, --https or --basic-auth-realm, each of which describes a\n" +
			"deployment published beyond this machine.\n\n" +
			"--no-auth serves it anyway, and means it: a server started with it consults\n" +
			"no users at all, so adding one does not close the door either. Taking it\n" +
			"back is a restart without the flag.\n\n" +
			"The server never terminates TLS. To publish it beyond this machine, put a\n" +
			"reverse proxy in front of it: --public-url is the URL it is published under,\n" +
			"which the proxy maps to this server with that base path stripped, and --https\n" +
			"tells browsers to keep using TLS.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *serveParams, cmd *cobra.Command, _ []string) error {
			opts := p.options()
			// What the flags say is checked before anything is opened, so a
			// flag that could never work is reported as the usage error it is
			// rather than behind an unrelated failure to find a database.
			if err := opts.validate(); err != nil {
				return err
			}
			if opts.proxyTo != "" && p.Identity != nil {
				return awberr.Usagef("--identity cannot be used with --proxy-to")
			}
			if opts.proxyTo != "" && len(opts.corsOrigins) > 0 {
				return awberr.Usagef("--cors-origin cannot be used with --proxy-to")
			}
			// Proxy mode opens no database, so it has no users to serve
			// without: whether the installation it forwards to authenticates
			// is that installation's answer, not this flag's.
			if opts.proxyTo != "" && opts.noAuth {
				return awberr.Usagef("--no-auth cannot be used with --proxy-to")
			}
			if err := checkIdentityFlag(p.Identity); err != nil {
				return err
			}

			// Proxy mode serves this binary's bundled UI without opening a local
			// database. Browser writes are checked against this local server's
			// origin before they are forwarded to the remote installation.
			if opts.proxyTo != "" {
				target, err := parseProxyURL(opts.proxyTo)
				if err != nil {
					return err
				}
				logger := log.New(e.stderr, "", log.LstdFlags)
				httpHandler, err := buildProxyHandler(target, e.openAPI, opts, logger)
				if err != nil {
					return err
				}
				return runServer(cmd.Context(), logger, opts, httpHandler)
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

			// What the database says about users decides both what this server
			// may be started as and what an unauthenticated request would be
			// attributed to, so it is asked once, here, and both demands are
			// made at startup rather than on the first request.
			users, err := readUserState(cmd.Context(), db)
			if err != nil {
				return err
			}
			if err := checkAuthentication(opts, users); err != nil {
				return err
			}

			// The fixed identity is what an unauthenticated request is
			// attributed to, and is therefore only required on a server that
			// would answer one: one over a database that has never held a user
			// with a password, or one told to authenticate nobody. A server that authenticates
			// every request never uses the value, and neither does a locked one,
			// which answers none; demanding one there would be asking the
			// operator to name somebody who stands for nobody.
			fixedIdentity, missing := resolveServerIdentity(cfg, p.Identity)
			if missing != nil && (opts.noAuth || !users.any && !users.existed) {
				return missing
			}

			// serve is the one command that runs until it is stopped rather
			// than answering and exiting, so what it writes is a log and is
			// stamped with the time. It goes to the writer Execute was handed
			// rather than to the package-level logger's os.Stderr, so that
			// redirecting the command's output still captures all of it.
			logger := log.New(e.stderr, "", log.LstdFlags)

			// --no-auth is not a weaker authenticator, it is none: the whole
			// point of typing it is a server that does not consult the users
			// table, so adding one to a server started with it does not close
			// the door either. Taking it back is a restart without the flag.
			var credentials *authenticator
			if !opts.noAuth {
				credentials = &authenticator{db: db, realm: opts.basicAuthRealm}
			}

			base := local.New(db, storage.NewBlobs(cfg.Attachments), fixedIdentity)
			httpHandler, err := buildHandler(base, e.openAPI, credentials, opts, logger)
			if err != nil {
				return err
			}

			// What this server does about authentication is worth one line,
			// because both of these are states an operator may not know they
			// are in: one is a server that refuses everybody until they act,
			// and the other is one that asks nobody for anything.
			switch {
			case opts.noAuth:
				logger.Printf(
					"serving without authentication: every client that reaches the port " +
						"has full read and write access")
			case !users.any && users.existed:
				logger.Printf(
					"this database has had users with passwords and holds none: every request " +
						"is refused until one is added with \"awb user add --password\"")
			}

			return runServer(cmd.Context(), logger, opts, httpHandler)
		},
	}.ToCobra()
}

// checkAuthentication decides whether a server that would authenticate nobody
// may be started.
//
// Only one server would: the one over a database that has never held a user
// with a password, which serves everybody who can reach the port. That is the
// right answer for a local tracker on loopback and the wrong one everywhere
// else, so a flag saying this deployment is somewhere else refuses to start
// rather than opening the door. --no-auth is the operator saying it was meant.
//
// A database that has had such users and holds none is not that server. It is
// a locked one, which answers nothing to anybody and so exposes nothing
// wherever it is bound, and it starts: refusing would only move the same state
// from the port into a command that failed, and would take the recovery with
// it. A running server recovers from the next "awb user add --password"
// without a restart, and so must one that was restarted in that state — an
// operator whose service is supervised should not have to time account
// creation against a restart. The state is announced in the log instead; see
// runServer.
func checkAuthentication(opts serveOptions, users userState) error {
	if opts.noAuth || users.any || users.existed {
		return nil
	}
	if why := opts.exposure(); why != "" {
		return awberr.Usagef(
			"%s, and this database holds no user with a password: the server would "+
				"authenticate nobody, and every client that reached it would have full read "+
				"and write access; add one with \"awb user add --password\", or pass "+
				"--no-auth to serve without authentication anyway", why)
	}
	return nil
}

// userState is what the database says at startup about the users a server
// could authenticate: whether it holds any, and whether it ever has. The
// second is what tells a local tracker apart from a server whose credentials
// have all been deleted. A user with no password is neither, being an assignee
// rather than an account anybody logs in to.
type userState struct {
	any     bool
	existed bool
}

// readUserState asks both questions in one read transaction, so the answers
// cannot come from two different moments.
func readUserState(ctx context.Context, db *storage.DB) (userState, error) {
	var users userState
	err := db.Read(ctx, func(tx *storage.Tx) error {
		any, err := tx.AnyUsersWithPassword()
		if err != nil {
			return err
		}
		existed, err := tx.UsersWithPasswordHaveExisted()
		if err != nil {
			return err
		}
		users = userState{any: any, existed: existed}
		return nil
	})
	return users, err
}

// checkIdentityFlag applies the assignee vocabulary to an explicit --identity,
// which is what a nil pointer distinguishes from one that was never given.
//
// It is separate from resolving one, and unconditional, because the two
// failures are different failures: a value the operator typed and got wrong is
// a usage mistake whatever the database holds, while having no identity at all
// only matters on a server that would answer an unauthenticated request.
// Folding the first into the second would let "--identity Mikael" start a
// server that quietly ignored it.
func checkIdentityFlag(flag *string) error {
	if flag == nil {
		return nil
	}
	if _, err := domain.ValidateAssignee(*flag); err != nil {
		return awberr.Usagef("--identity: %s", err.Error())
	}
	return nil
}

// resolveServerIdentity resolves the identity an unauthenticated request is
// attributed to, from the same sources and in the same order as the CLI's own:
// --identity, else AWB_IDENTITY, else identity in the user configuration file,
// else the OS username folded to the assignee set.
//
// The error it returns says only that there is none. Whether that is fatal is
// the caller's question, and the database's answer.
func resolveServerIdentity(cfg *config.Config, flag *string) (string, error) {
	if flag != nil {
		return *flag, nil
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

// parseProxyURL validates the remote awb server whose API the local UI calls.
// It follows the same URL shape as --db in remote mode: an HTTP(S) origin with
// an optional base path, and no credentials or request-specific components.
func parseProxyURL(proxyTo string) (*url.URL, error) {
	if proxyTo == "" {
		return nil, nil
	}
	parsed, err := url.Parse(proxyTo)
	if err != nil {
		return nil, awberr.Usagef("--proxy-to: %s is not a URL", proxyTo)
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return nil, awberr.Usagef("--proxy-to: %s must be an http or https URL", proxyTo)
	case parsed.Host == "":
		return nil, awberr.Usagef("--proxy-to: %s names no host", proxyTo)
	case parsed.User != nil:
		return nil, awberr.Usagef("--proxy-to: %s must carry no credentials", proxyTo)
	case parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "":
		return nil, awberr.Usagef(
			"--proxy-to: %s must be a base URL, with no query or fragment", proxyTo)
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, awberr.Usagef("--proxy-to: %s is not a port number", port)
		}
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
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
func buildHandler(base *local.Backend, document *openapi.Document, credentials *authenticator,
	opts serveOptions, logger *log.Logger) (http.Handler, error) {
	// Whichever of the two mechanisms is in force, the request has exactly one
	// identity, so the surface below never has to handle its absence. The
	// username arrives in the request context, which is what the generated
	// server passes on to the handler.
	//
	// An authenticated request is also an authorized one: it acts as that user
	// and may do exactly what that user may. A request that authenticated
	// nobody, on a database that holds no user with a password, acts as the
	// server's fixed identity with no authorization at all — which is what
	// version 1 was. A fixed server identity attributes anonymous work; it is
	// not a user. In particular, --no-auth must not start consulting or
	// changing a stored account's preferences just because the two names
	// happen to match.
	withoutUser := base.WithoutUserPreferences()
	backendFor := func(ctx context.Context) backend.Backend {
		if username, ok := auth.UsernameFromContext(ctx); ok {
			return base.WithUser(username)
		}
		return withoutUser
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

	// Compression is applied per route rather than once around everything,
	// because StaticHandler already gzips what it serves and wrapping it again
	// would encode the body twice. A browser decodes one layer and is left with
	// the other; curl, which does not ask for gzip by default, sees nothing
	// wrong. Keeping the two apart is what stops that.
	withAPI := func(h http.Handler) http.Handler {
		return recovery.Middleware(gzipExcept(isAttachmentDownload, handler.NoStore(h)))
	}

	root, err := buildRoutes(withAPI(apiServer), document, opts)
	if err != nil {
		return nil, err
	}

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

	// Nothing is exempt: the API, the OpenAPI document and the web UI all sit
	// behind it. Whether it asks for anything is the database's answer, given
	// per request; see authenticator.
	//
	// There is no authenticator at all in proxy mode, which opens no database
	// to authenticate against, or under --no-auth, which is an operator saying
	// this server authenticates nobody whatever the database holds.
	if credentials != nil {
		chain = credentials.Middleware(logger)(chain)
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

// buildProxyHandler serves this binary's bundled UI and forwards its API calls
// to another awb server.
func buildProxyHandler(target *url.URL, document *openapi.Document, opts serveOptions,
	logger *log.Logger) (http.Handler, error) {
	proxy := &stdhttputil.ReverseProxy{
		Rewrite: func(request *stdhttputil.ProxyRequest) {
			request.SetURL(target)
			// The browser is talking to the local origin, which has already been
			// checked below. State that equivalent request in the remote origin's
			// terms so its own CSRF middleware can perform the same check.
			if request.Out.Header.Get("Origin") != "" {
				request.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			if request.Out.Header.Get("Referer") != "" {
				request.Out.Header.Set("Referer", target.String()+"/")
			}
		},
		ModifyResponse: func(response *http.Response) error {
			// These policies belong to the origin that sent them. Applying the
			// remote server's policy to the local proxy can duplicate or conflict
			// with the local UI's headers, and upstream CORS must not make a local
			// credentialed endpoint cross-origin readable.
			for _, header := range []string{
				"Access-Control-Allow-Credentials",
				"Access-Control-Allow-Headers",
				"Access-Control-Allow-Methods",
				"Access-Control-Allow-Origin",
				"Access-Control-Expose-Headers",
				"Content-Security-Policy",
				"Referrer-Policy",
				"Strict-Transport-Security",
				"X-Content-Type-Options",
				"X-Frame-Options",
			} {
				response.Header.Del(header)
			}
			return nil
		},
		ErrorLog: logger,
	}
	boundedProxy := proxyDeadlines(proxy, proxyRequestLimit, contentTimeout)
	root, err := buildRoutes(recovery.Middleware(boundedProxy), document, opts)
	if err != nil {
		return nil, err
	}

	csp, err := contentSecurityPolicy()
	if err != nil {
		return nil, err
	}
	strictTransport := ""
	if opts.https {
		strictTransport = hsts
	}
	serverOrigin, err := opts.serverOrigin()
	if err != nil {
		return nil, err
	}
	chain := csrf.MiddlewareOrigins(serverOrigin)(root)
	chain = httputil.SecurityHeaders(httputil.SecurityHeadersOptions{
		CSP:            csp,
		ReferrerPolicy: "same-origin",
		HSTS:           strictTransport,
	})(chain)
	return transferLimits(chain), nil
}

// proxyDeadlines bounds time spent waiting on the remote server. The listener's
// read and write deadlines govern only the browser-facing connection; without a
// context deadline an upstream that accepts a connection and then stalls can
// retain the proxy's goroutine and socket indefinitely.
func proxyDeadlines(next http.Handler, requestLimit, attachmentLimit time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := requestLimit
		if isAttachmentUpload(r) || isAttachmentDownload(r) {
			limit = attachmentLimit
		}
		ctx, cancel := context.WithTimeout(r.Context(), limit)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// buildRoutes puts an API implementation beside the bundled UI and the local
// OpenAPI document. Both normal serve mode and UI proxy mode use this exact
// route tree, so a static asset or shell change cannot work in only one mode.
func buildRoutes(apiHandler http.Handler, document *openapi.Document,
	opts serveOptions) (http.Handler, error) {
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

	root := http.NewServeMux()
	root.Handle("/api/", apiHandler)
	root.Handle("GET /openapi.json", recovery.Middleware(httputil.Gzip(document.JSONHandler())))
	root.Handle("GET /openapi.yaml", recovery.Middleware(httputil.Gzip(document.YAMLHandler())))
	root.Handle("/", recovery.Middleware(web.SPAHandler(uiHandler, staticFS, shell)))
	return root, nil
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

func runServer(ctx context.Context, logger *log.Logger, opts serveOptions, h http.Handler) error {
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
