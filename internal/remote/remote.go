// Package remote implements the backend over the HTTP API, so that setting
// --db to a server's URL makes every command behave identically to direct
// mode.
//
// Directory context and the CLI's identity are resolved on the client, and the
// identity is always stated explicitly — as the assignee of claim and release,
// and as the assignee parameter --mine becomes — so that a remote claim
// records exactly what the same command would record locally and a server's
// own identity never stands in for it.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// requestTimeout bounds a single API call.
//
// contentTimeout bounds one that carries an attachment's content instead. It
// is far longer because the bound has to cover moving the bytes: a file near
// the maximum over a slow link takes longer than any request describing an
// issue ever could, and a timeout that cut it short would make the size limit
// depend on the connection.
const (
	requestTimeout = 30 * time.Second
	contentTimeout = 10 * time.Minute
)

// Backend talks to an awb server.
type Backend struct {
	base     *url.URL
	user     string
	password string
	identity string
	client   *http.Client
	// contentClient carries attachment content, which needs a longer bound than
	// anything else the API answers.
	contentClient *http.Client
}

// New builds a client for the server at base, which may carry a path that the
// API paths hang under.
func New(base *url.URL, user, password, identity string) *Backend {
	return &Backend{
		base:     base,
		user:     user,
		password: password,
		identity: identity,
		client:   &http.Client{Timeout: requestTimeout},

		contentClient: &http.Client{Timeout: contentTimeout},
	}
}

// Close releases the client's idle connections.
func (b *Backend) Close() error {
	b.client.CloseIdleConnections()
	b.contentClient.CloseIdleConnections()
	return nil
}

// authenticate presents the credentials, if there are any. A client that has
// them sends them on every request, whether or not the server asks.
func (b *Backend) authenticate(req *http.Request) {
	if b.user != "" || b.password != "" {
		req.SetBasicAuth(b.user, b.password)
	}
}

// Identity is resolved on the client, never asked of the server: what a remote
// claim records must be exactly what a local one would.
func (b *Backend) Identity(_ context.Context) (string, error) {
	if b.identity == "" {
		return "", awberr.Runtimef(
			"no identity is configured: set \"identity\" in the configuration file or AWB_IDENTITY")
	}
	return b.identity, nil
}

// endpoint builds a URL under the server's base path.
//
// path is already escaped, and is appended to the base as text rather than
// assigned to url.URL.Path — which holds the *decoded* path, so putting an
// escaped one there escapes it a second time and an attachment named "release
// notes.md" is asked for as "release%2520notes.md". The base is safe to append
// to because it was validated when it was read: an http or https URL with a
// host, no userinfo, no query, no fragment and no trailing slash.
func (b *Backend) endpoint(path string, query url.Values) string {
	endpoint := b.base.String() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	return endpoint
}

// call performs one request and decodes the response into out.
//
// The status-to-exit-code mapping is inverted here so the CLI's exit codes are
// identical in both modes: 400 becomes 2, 404 becomes 3, 409 becomes 4, and
// any other failure — including a transport error or an unreachable server —
// becomes 1.
func (b *Backend) call(ctx context.Context, method, url string, body any, ifMatch string,
	out any) (http.Header, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "encode request")
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "build request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	b.authenticate(req)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, awberr.Runtimef("cannot reach %s: %s", b.base.Host, err.Error())
	}
	defer resp.Body.Close() //nolint:errcheck // the response is being discarded

	if resp.StatusCode >= 400 {
		return nil, b.apiError(resp)
	}
	if out != nil {
		if err := decodeJSON(resp.Body, out); err != nil {
			return nil, err
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.Header, nil
}

// decodeJSON reads one JSON response body.
func decodeJSON(body io.Reader, out any) error {
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "decode response")
	}
	return nil
}

// apiError turns a failure response into a classified error, printing the
// message the server sent. Wrong credentials are reported, never retried or
// prompted for.
func (b *Backend) apiError(resp *http.Response) error {
	kind := awberr.KindFromHTTPStatus(resp.StatusCode)

	var payload domain.APIError
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		remoteErr := &awberr.Error{Kind: kind, Msg: payload.Error}
		// Preserve the conditional-edit sentinel across HTTP so the one CLI
		// command shared by direct and remote mode can add actionable recovery
		// guidance without inspecting transport details or error strings.
		if resp.StatusCode == http.StatusPreconditionFailed {
			remoteErr.Err = awberr.ErrPreconditionFailed
		}
		return remoteErr
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &awberr.Error{Kind: kind, Msg: fmt.Sprintf("%s: %s", resp.Status, message)}
}

// totalCount reads the unpaged total the server reports, falling back to the
// number of rows when the header is absent.
func totalCount(header http.Header, fallback int) int {
	value := header.Get("X-Total-Count")
	if value == "" {
		return fallback
	}
	total, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return total
}
