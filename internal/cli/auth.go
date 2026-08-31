package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/mikaelstaldal/go-server-common/auth"

	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// authenticator is awb serve's HTTP basic authentication, checked against the
// users table rather than against a file.
//
// Whether it authenticates at all is a property of the database and is asked
// on every request: a database holding no user is an open server, exactly as
// version 1 was, and one holding at least one requires credentials. Asking per
// request rather than at startup is what makes adding the first user close the
// door immediately, and it costs one indexed lookup.
//
// The door only opens the other way at a restart. Once this server has seen a
// user, deleting the last one leaves it answering nothing rather than serving
// everybody: a request that arrives a moment after the deletion is one the
// operator has no reason to expect will be answered, and turning a server that
// authenticates into an open one, without anybody saying so, is exactly the
// accident that awb serve refuses to be started into.
//
// Everything the server answers sits behind it: the API, the OpenAPI document
// and the web UI alike. The one thing that does not is a CORS preflight, which
// carries no credentials because the specification forbids it and no data
// because it is a question about the request that follows; see Middleware.
type authenticator struct {
	db    *storage.DB
	realm string
	// hadUsers latches the moment this server sees a user, and is never
	// cleared. It is read and written from every request in flight.
	hadUsers atomic.Bool
}

// newAuthenticator builds one over a database that was found holding users, or
// not, at startup — which is the latch's starting position, so that a server
// whose last user is deleted before its first request is locked rather than
// open.
func newAuthenticator(db *storage.DB, realm string, hadUsers bool) *authenticator {
	a := &authenticator{db: db, realm: realm}
	a.hadUsers.Store(hadUsers)
	return a
}

// authState is what the database says about a request before any credentials
// in it are looked at.
type authState int

const (
	// authOpen: no user has ever been seen. Everything is let through, and the
	// server's fixed identity stands in for the caller.
	authOpen authState = iota
	// authRequired: the database holds a user, so credentials decide.
	authRequired
	// authLocked: users were seen and are all gone. Nothing is let through
	// until one is added again.
	authLocked
)

// dummyHash is what a password is compared against when the username is not
// one the database holds, so that an unknown user costs bcrypt work rather
// than returning at once — which, with the two answers being byte-identical,
// is what stops the response from saying which of the two it was.
//
// It equalises against a hash awb wrote itself, both being at bcrypt's default
// cost. It cannot equalise against one imported at another cost: the cost is
// part of the hash, so a wrong password for an account whose hash was written
// by "htpasswd -Bn" at its own default is measurably cheaper than an unknown
// username. That is a property of the imported credential rather than of this
// comparison — the same lower cost is what makes it a weaker password — and it
// is why the schema for one says so.
//
// It is derived once, from bytes nobody has, rather than being a constant: a
// constant would be a hash in the source that some deployment might one day
// have a password for.
var dummyHash = sync.OnceValue(func() string {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// A password can then still be compared against something; what is lost
		// is only the constant-time property, and failing to serve at all would
		// be the worse answer.
		return "$2a$10$" + base64.RawStdEncoding.EncodeToString(make([]byte, 40))[:53]
	}
	hash, err := domain.HashPassword(base64.RawStdEncoding.EncodeToString(secret)[:32])
	if err != nil {
		return "$2a$10$" + base64.RawStdEncoding.EncodeToString(make([]byte, 40))[:53]
	}
	return hash
})

// check reports whether credentials open an account, and whether the database
// requires any at all.
//
// Both questions are answered inside one read transaction, so a request cannot
// see a database that holds no user and a user's hash at the same time.
func (a *authenticator) check(ctx context.Context, username, password string) (
	state authState, ok bool, err error) {
	err = a.db.Read(ctx, func(tx *storage.Tx) error {
		any, err := tx.AnyUsers()
		if err != nil {
			return err
		}
		if !any {
			state = authOpen
			if a.hadUsers.Load() {
				state = authLocked
			}
			return nil
		}
		a.hadUsers.Store(true)
		state = authRequired

		hash, found, err := tx.PasswordHash(username)
		if err != nil {
			return err
		}
		if !found {
			// Compared anyway, against a hash nothing matches, so that an
			// unknown username costs bcrypt work rather than returning at
			// once; see dummyHash for what that does and does not equalise.
			domain.CheckPassword(dummyHash(), password)
			return nil
		}
		ok = domain.CheckPassword(hash, password)
		return nil
	})
	return state, ok, err
}

// Middleware requires credentials whenever the database holds a user, and lets
// everything through when it holds none.
//
// A request that passes reaches the rest of the server with its authenticated
// username in the request context, which is what gives it its identity and its
// permissions. A request that passed because there was nobody to authenticate
// carries none, and the server's fixed identity stands in for it.
//
// The client address of every 401 is logged, so an external tool such as
// fail2ban can watch for brute-force attempts.
func (a *authenticator) Middleware(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A browser never puts credentials on a preflight — the CORS
			// specification forbids it — so requiring them here would refuse
			// every cross-origin request that needs one, and --cors-origin
			// would mean nothing on a server that authenticates. The preflight
			// carries no data and returns none: it is answered with headers
			// saying what the request that follows may be, and that request is
			// authenticated like any other.
			if isPreflight(r) {
				next.ServeHTTP(w, r)
				return
			}

			username, password, given := r.BasicAuth()
			state, ok, err := a.check(r.Context(), username, password)
			switch {
			case err != nil:
				// The database could not say. That is this server's failure and
				// not the caller's, and it must not read as "come in".
				logger.Printf("cannot authenticate %s %s: %s", r.Method, r.URL.Path, err.Error())
				writeAuthError(w, http.StatusInternalServerError,
					"cannot check credentials right now")
				return
			case state == authOpen:
				next.ServeHTTP(w, r)
				return
			case state == authLocked:
				// No credentials can open a server with no accounts, so this is
				// not a challenge: it is the server saying it cannot serve
				// anybody until one exists. Adding a user makes the next
				// request work, with no restart, exactly as the first one did.
				logger.Printf("refusing %s %s from %s: the last user was deleted",
					r.Method, r.URL.Path, r.RemoteAddr)
				writeAuthError(w, http.StatusServiceUnavailable,
					"no users: this server answers nothing until a user is added")
				return
			case !given || !ok:
				logger.Printf("authentication failed: %s %s from %s",
					r.Method, r.URL.Path, r.RemoteAddr)
				w.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", a.realm))
				writeAuthError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUsername(r.Context(), username)))
		})
	}
}

// isPreflight recognises a CORS preflight: an OPTIONS request asking what a
// later request may do. The header is what tells one apart from an ordinary
// OPTIONS, and a browser always sends it.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// writeAuthError answers in the API's own error shape, so a refusal from in
// front of the router looks like every other refusal.
func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.APIError{Error: message})
}
