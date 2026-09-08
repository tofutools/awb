// Package httpgzip compresses a response with a pooled compressor.
//
// It exists because a compressor is expensive to make and cheap to reuse. A
// gzip.Writer carries about 800 kB of hash tables and window whatever the body
// it is about to compress, and a browser asks for gzip on every request it
// sends, so a server that makes one per response allocates that much to send a
// two-hundred-byte answer. Pooling them bounds the cost at one compressor per
// response in flight rather than one per response, which is what
// TestCompressorsAreReused pins down.
//
// Nothing else about it differs from the middleware it stands in for: the same
// header is set and the same one removed, the body is the same bytes, and a
// client that does not ask for gzip is passed through untouched. That includes
// what neither of them does, which is notice a response that may carry no body
// at all: a 204 or a 304 compressed by either would be answered with a
// Content-Encoding it does not use and a gzip stream net/http refuses to
// write. Both of awb's are answered in front of it — the CORS preflight by the
// middleware outside the router, the not-modified by the two handlers that
// serve the UI — which is why the compressor never sees one.
//
// It is deliberately a local copy of go-server-common's httputil.Gzip, against
// the rule that cross-cutting middleware comes from there, and it is
// temporary. The fix belongs upstream, where it would also reach the static
// assets that httputil.StaticHandler compresses through its own copy of that
// middleware and this package cannot see. When it has moved there, awb calls
// httputil.Gzip again and this package goes away.
package httpgzip

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// writers holds compressors between responses. gzip.NewWriter compresses at
// the default level, which is the level the middleware this replaces used;
// levels 1 to 3 are not the cheaper option they sound like, being a different
// encoder that allocates half as much again.
var writers = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// responseWriter sends what a handler writes through the compressor. Only
// Write is intercepted: the header and the status belong to the response
// underneath and are written to it directly.
type responseWriter struct {
	http.ResponseWriter
	writer io.Writer
}

func (g *responseWriter) Write(b []byte) (int, error) { return g.writer.Write(b) }

// Gzip returns a middleware that compresses the response body when the client
// says it can decompress one.
func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		compressor, _ := writers.Get().(*gzip.Writer)
		compressor.Reset(w)
		defer func() {
			// Closing is what writes the trailer, so it happens before the
			// handler's response is finished with. The second reset is what
			// keeps a compressor waiting in the pool from holding the response
			// it last wrote to, and the request behind it, alive.
			_ = compressor.Close()
			compressor.Reset(io.Discard)
			writers.Put(compressor)
		}()

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		next.ServeHTTP(&responseWriter{ResponseWriter: w, writer: compressor}, r)
	})
}
