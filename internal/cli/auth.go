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

	"github.com/mikaelstaldal/go-server-common/auth"

	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// authenticator is awb serve's HTTP basic authentication, checked against the
// users table rather than against a file.
//
// Whether it authenticates at all is a property of the database and is asked
// on every request: a database holding no user with a password is an open
// server, exactly as version 1 was, and one holding at least one requires
// credentials. Asking per request rather than at startup is what makes giving
// the first user a password close the door immediately, and it costs one
// indexed lookup.
//
// A user without a password does not count, and cannot: they exist so that the
// tracker knows who its assignees are, and an account nobody can log in as is
// not what a server would be authenticating.
//
// It does not open the other way. Deleting the last user who could log in
// leaves the server answering nothing until another exists, rather than
// reverting to the open server: a deletion says who may no longer act, and
// reading it as "everybody may act" turns an administrative mistake into an
// unguarded database that nobody was told about.
//
// That the database has had such users is a stored fact rather than something
// this server remembers, because the users are added and deleted by a command
// line holding the file and a server learns of either only by looking. A server
// that looked before the first was added and again after the last was deleted
// would see the same empty table twice, and nothing it held in memory could
// tell the two apart; see schemaV6.
//
// Everything the server answers sits behind it: the API, the OpenAPI document
// and the web UI alike, and, once it is locked, a CORS preflight too; see
// Middleware.
type authenticator struct {
	db    *storage.DB
	realm string
}

// authState is what the database says about a request before any credentials
// in it are looked at.
type authState int

const (
	// authOpen: this database has never held a user with a password. Everything
	// is let through, and the server's fixed identity stands in for the caller.
	authOpen authState = iota
	// authRequired: the database holds a user with a password, so credentials
	// decide.
	authRequired
	// authLocked: it has held users with passwords and holds none now. Nothing
	// is let through until one exists again.
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

// check reports whether credentials open an account, and what the database
// asks of a request in the first place.
//
// Every question is answered inside one read transaction, so a request cannot
// see a database that holds no user and a user's hash at the same time, nor
// one that has never had users and the record of one at the same time.
func (a *authenticator) check(ctx context.Context, username, password string) (
	state authState, ok bool, err error) {
	err = a.db.Read(ctx, func(tx *storage.Tx) error {
		any, err := tx.AnyUsersWithPassword()
		if err != nil {
			return err
		}
		if !any {
			existed, err := tx.UsersWithPasswordHaveExisted()
			if err != nil {
				return err
			}
			state = authOpen
			if existed {
				state = authLocked
			}
			return nil
		}
		state = authRequired

		hash, found, err := tx.PasswordHash(username)
		if err != nil {
			return err
		}
		if !found {
			// Compared anyway, against a hash nothing matches, so that an
			// unknown username — or one belonging to an account with no
			// password, which PasswordHash reports as the same thing — costs
			// bcrypt work rather than returning at once; see dummyHash for
			// what that does and does not equalise.
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
			username, password, given := r.BasicAuth()
			state, ok, err := a.check(r.Context(), username, password)

			// A browser never puts credentials on a preflight — the CORS
			// specification forbids it — so requiring them here would refuse
			// every cross-origin request that needs one, and --cors-origin
			// would mean nothing on a server that authenticates. The preflight
			// carries no data and returns none: it is answered with headers
			// saying what the request that follows may be, and that request is
			// authenticated like any other.
			//
			// A locked server is the exception to the exception. There is no
			// request that could follow one successfully, so answering the
			// question would be describing a door that does not open, and
			// "unreachable" would not be true of every request.
			if err == nil && state != authLocked && isPreflight(r) {
				next.ServeHTTP(w, r)
				return
			}

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
				// No credentials can open a server with no account that has one,
				// so this is not a challenge: it is the server saying it cannot
				// serve anybody until one exists. Adding a password makes the
				// next request work, with no restart, exactly as the first one
				// did.
				logger.Printf("refusing %s %s from %s: this database has no user with a password",
					r.Method, r.URL.Path, r.RemoteAddr)
				writeAuthError(w, http.StatusServiceUnavailable,
					"no users: this server answers nothing until a user with a password is added")
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
