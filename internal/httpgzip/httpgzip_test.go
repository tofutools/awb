package httpgzip_test

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/httpgzip"
)

// echo answers with the request's path, so a response can be checked against
// the request that asked for it.
func echo() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, strings.Repeat(r.URL.Path, 100))
	})
}

func call(t *testing.T, h http.Handler, path string, acceptGzip bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptGzip {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A client that asks for gzip gets a gzip body, and it decodes to what the
// handler wrote.
func TestCompressesWhenAsked(t *testing.T) {
	rec := call(t, httpgzip.Gzip(echo()), "/hello", true)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
	assert.Empty(t, rec.Header().Get("Content-Length"),
		"a compressed body is not the length the handler would have written")
	assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"),
		"the handler's own headers reach the response unchanged")

	reader, err := gzip.NewReader(rec.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, strings.Repeat("/hello", 100), string(body))
}

// A client that does not ask is left alone, headers and all.
func TestPassesThroughWhenNotAsked(t *testing.T) {
	rec := call(t, httpgzip.Gzip(echo()), "/hello", false)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, strings.Repeat("/hello", 100), rec.Body.String())
}

// A handler that writes no body still produces a valid, empty gzip stream: the
// header and the trailer are written when the compressor is closed, which
// happens whether or not anything went through it. A reader that got the
// header and nothing else would fail with an unexpected EOF.
func TestEmptyBodyIsStillValidGzip(t *testing.T) {
	handler := httpgzip.Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := call(t, handler, "/nothing", true)

	require.Equal(t, http.StatusOK, rec.Code)
	reader, err := gzip.NewReader(rec.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Empty(t, body)
}

// Compressors are reused between responses rather than made for each one.
//
// What is pinned is the shape of the cost, not a figure: a gzip.Writer carries
// about 800 kB whatever it is about to compress, so a middleware that made one
// per response would allocate hundreds of megabytes here and one that reuses
// them allocates a fraction of that. The threshold is a tenth of what making
// one per response would cost, so only a real regression reaches it.
func TestCompressorsAreReused(t *testing.T) {
	const (
		requests           = 300
		perFreshCompressor = 800 << 10
		allowed            = requests * perFreshCompressor / 10
	)

	handler := httpgzip.Gzip(echo())
	before := totalAlloc()
	for i := range requests {
		rec := call(t, handler, fmt.Sprintf("/issue-%d", i), true)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	allocated := totalAlloc() - before

	assert.Less(t, allocated, uint64(allowed),
		"%d compressed responses allocated %d bytes: the compressors are not being reused",
		requests, allocated)
}

// Concurrent responses do not share a compressor, so no body arrives holding
// another response's bytes.
func TestConcurrentResponsesStaySeparate(t *testing.T) {
	handler := httpgzip.Gzip(echo())

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("/issue-%d", i)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			reader, err := gzip.NewReader(rec.Body)
			if !assert.NoError(t, err) {
				return
			}
			body, err := io.ReadAll(reader)
			assert.NoError(t, err)
			assert.NoError(t, reader.Close())
			assert.Equal(t, strings.Repeat(path, 100), string(body))
		}()
	}
	wg.Wait()
}

func totalAlloc() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.TotalAlloc
}
