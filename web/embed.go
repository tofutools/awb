// Package web embeds the compiled frontend assets into the binary so the
// deployed artifact is a single executable. The TypeScript sources in web/ts/
// are compiled to web/static/ by tsc (see build.sh) before this is built.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/mikaelstaldal/go-server-common/httputil"
)

//go:embed all:static
var Static embed.FS

// StaticFS is the sub-filesystem actually served, with the "static/" prefix
// stripped.
func StaticFS() (fs.FS, error) { return fs.Sub(Static, "static") }

// rootBase is the <base href> the compiled shell carries, and the tag every
// relative URL in the UI — its assets, its import map and its API calls —
// resolves against.
const rootBase = `<base href="/">`

// Shell returns index.html with its <base href> pointed at basePath, which is
// "/" unless a reverse proxy publishes the server under a path.
//
// Every URL the UI uses is relative, so that one tag is the whole of what makes
// it work under a path: without it a page loaded from
// https://example.com/awb/ would ask for https://example.com/app.js. The tag
// has to be there — a shell that quietly lost it would serve correctly at the
// root and fail only once deployed behind a proxy, which is why its absence is
// an error rather than a no-op.
func Shell(basePath string) ([]byte, error) {
	html, err := fs.ReadFile(Static, "static/index.html")
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(html, []byte(rootBase)) {
		return nil, fmt.Errorf("index.html carries no %s", rootBase)
	}
	if basePath == "/" {
		return html, nil
	}
	return bytes.Replace(html, []byte(rootBase), []byte(`<base href="`+basePath+`">`), 1), nil
}

// SPAHandler serves a real file when one exists and falls back to the UI shell
// otherwise, so a deep link into the client-side router works.
//
// The shell comes from memory rather than from the embedded filesystem because
// Shell rewrites its <base href> at startup; the file server never answers "/"
// at all. Its caching mirrors what StaticHandler gives every other asset, and
// like that handler it answers the 304 before the compressor, so an empty body
// is never gzip-encoded.
func SPAHandler(files http.Handler, staticFS fs.FS, shell []byte) http.Handler {
	sum := sha256.Sum256(shell)
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	shellBody := httputil.Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(shell)
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if _, err := fs.Stat(staticFS, name); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		w.Header().Add("Vary", "Accept-Encoding")
		if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		shellBody.ServeHTTP(w, r)
	})
}
