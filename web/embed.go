// Package web embeds the compiled frontend assets into the binary so the
// deployed artifact is a single executable. The TypeScript sources in web/ts/
// are compiled to web/static/ by tsc (see build.sh) before this is built.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:static
var Static embed.FS

// StaticFS is the sub-filesystem actually served, with the "static/" prefix
// stripped.
func StaticFS() (fs.FS, error) { return fs.Sub(Static, "static") }

// SPAHandler serves a real file when one exists and falls back to the UI shell
// otherwise, so a deep link into the client-side router works.
func SPAHandler(files http.Handler, staticFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if _, err := fs.Stat(staticFS, name); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
