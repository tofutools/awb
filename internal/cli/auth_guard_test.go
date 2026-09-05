package cli

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/backend"
)

func TestAuthGuardConcurrentFlood(t *testing.T) {
	var g authGuard
	now := time.Now()
	var attempted sync.WaitGroup
	var finished sync.WaitGroup
	release := make(chan struct{})
	admitted := make(chan string, 100)
	for i := range 100 {
		attempted.Add(1)
		finished.Add(1)
		go func() {
			defer finished.Done()
			peer := fmt.Sprint(i)
			ok := g.begin(peer, now)
			if ok {
				admitted <- peer
			}
			attempted.Done()
			if ok {
				<-release
				g.finish(peer, true, now)
			}
		}()
	}
	attempted.Wait()
	assert.Len(t, admitted, authConcurrency)
	close(release)
	finished.Wait()
	require.True(t, g.begin("healthy", now))
	g.finish("healthy", false, now)
	assert.Zero(t, g.active)
}

func TestAuthGuardCooldownAndRecovery(t *testing.T) {
	var g authGuard
	now := time.Now()
	for range authFailureBurst {
		require.True(t, g.begin("peer", now))
		g.finish("peer", true, now)
	}
	for range 100 {
		assert.False(t, g.begin("peer", now.Add(authCooldown/2)))
	}
	require.True(t, g.begin("other", now))
	g.finish("other", false, now)
	require.True(t, g.begin("peer", now.Add(authCooldown)))
	g.finish("peer", false, now.Add(authCooldown))
	assert.Empty(t, g.peers)
	// Successful checks reset a partial failure burst immediately.
	for range authFailureBurst - 1 {
		require.True(t, g.begin("peer", now))
		g.finish("peer", true, now)
	}
	require.True(t, g.begin("peer", now))
	g.finish("peer", false, now)
	assert.Empty(t, g.peers)
}

func TestAuthGuardBoundsPeerMemoryWithoutEvictingFailures(t *testing.T) {
	var g authGuard
	now := time.Now()
	for i := range authPeerCapacity {
		peer := fmt.Sprint(i)
		require.True(t, g.begin(peer, now))
		g.finish(peer, true, now)
	}
	for i := range 100 {
		assert.False(t, g.begin(fmt.Sprintf("new-%d", i), now))
	}
	assert.Len(t, g.peers, authPeerCapacity)
	require.True(t, g.begin("new", now.Add(authCooldown)))
	g.finish("new", false, now.Add(authCooldown))
	assert.Empty(t, g.peers)
}

func TestAuthPeerKey(t *testing.T) {
	for _, address := range []string{"192.0.2.1:123", "192.0.2.1:456", "[::ffff:192.0.2.1]:789"} {
		assert.Equal(t, "192.0.2.1", authPeerKey(address))
	}
	assert.Equal(t, "2001:db8::1", authPeerKey("[2001:db8::1]:123"))
	assert.Equal(t, "unknown", authPeerKey("malformed"))
}

func TestAuthAdmissionRefusalSkipsDatabase(t *testing.T) {
	// No database: reaching check would panic. Saturation must refuse before it.
	a := &authenticator{realm: "awb"}
	for i := range authConcurrency {
		require.True(t, a.guard.begin(fmt.Sprint(i), time.Now()))
	}
	h := a.Middleware(log.New(io.Discard, "", 0))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached route") }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("alice", "correct")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, `Basic realm="awb"`, w.Header().Get("WWW-Authenticate"))
	assert.JSONEq(t, `{"error":"unauthorized"}`, w.Body.String())
}

func TestAuthFailedRequestsThrottleAndRecover(t *testing.T) {
	_, be := newServeHandlerOn(t, serveOptions{addr: "127.0.0.1", port: 7777})
	_, err := be.CreateUser(t.Context(), backend.UserCreate{Name: "alice", Password: "hunter2"})
	require.NoError(t, err)
	a := &authenticator{db: be.DB(), realm: "awb"}
	h := a.Middleware(log.New(io.Discard, "", 0))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := func(user, password, address string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = address
		req.Header.Set("X-Forwarded-For", address)
		req.Header.Set("Forwarded", "for="+address)
		req.SetBasicAuth(user, password)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	// Seed seven failures without spending most of the window in bcrypt on slow CI.
	now := time.Now()
	for range authFailureBurst - 1 {
		require.True(t, a.guard.begin("192.0.2.1", now))
		a.guard.finish("192.0.2.1", true, now)
	}
	failed := request("missing", "wrong", "192.0.2.1:123")
	require.Equal(t, http.StatusUnauthorized, failed.Code)
	// Changing port, username and forwarding headers cannot escape the cooldown.
	blocked := request("alice", "hunter2", "192.0.2.1:456")
	assert.Equal(t, failed.Code, blocked.Code)
	assert.Equal(t, failed.Header(), blocked.Header())
	assert.Equal(t, failed.Body.String(), blocked.Body.String())
	assert.Equal(t, http.StatusNoContent, request("alice", "hunter2", "192.0.2.2:123").Code)
	time.Sleep(authCooldown)
	assert.Equal(t, http.StatusNoContent, request("alice", "hunter2", "192.0.2.1:789").Code)
	assert.Empty(t, a.guard.peers)
}
