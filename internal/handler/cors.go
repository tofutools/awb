package handler

import (
	"net/http"
	"slices"
	"strings"
)

// NoStore marks every /api/ response uncacheable.
//
// No response here is cacheable in the first place, so the ETag has no second
// job to be wrong about. It is a validator for conditional edits and nothing
// should treat it as a cache validator.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// CORS allows the named origins to call the API from a browser.
//
// The default is to allow none, because any page in the user's browser could
// otherwise reach the API — unauthenticated it needs nothing at all, and
// authenticated it would ride credentials the browser has already stored.
//
// For an allowed origin the server answers preflight OPTIONS requests, permits
// the methods and request headers the API uses, allows credentials so that a
// UI there can authenticate at all, and exposes ETag, X-Total-Count and
// Location — without which a cross-origin UI could use neither the optimistic
// concurrency nor the paging.
//
// The generated encoders set the same header themselves, listing the ones the
// response they are encoding actually carries, and so replace this on every
// response an operation produced. What is set here therefore covers the
// responses no operation produced: the ones written before routing, the
// OpenAPI document and the bundled UI.
func CORS(allowedOrigins []string, next http.Handler) http.Handler {
	// "*" is deliberately not accepted, so a separately hosted web UI is opt-in
	// rather than any page in the browser being able to read the database.
	allowed := slices.Clone(allowedOrigins)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(allowed, origin) {
			header := w.Header()
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Access-Control-Allow-Credentials", "true")
			header.Add("Vary", "Origin")
			header.Set("Access-Control-Expose-Headers", "ETag, X-Total-Count, Location")

			if r.Method == http.MethodOptions {
				header.Set("Access-Control-Allow-Methods",
					strings.Join([]string{
						http.MethodGet, http.MethodPost, http.MethodPatch,
						http.MethodDelete, http.MethodOptions,
					}, ", "))
				header.Set("Access-Control-Allow-Headers", "Content-Type, If-Match, Authorization")
				header.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
