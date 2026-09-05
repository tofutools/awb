package cli

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	authConcurrency  = 4
	authPeerCapacity = 1024
	authFailureBurst = 8
	authCooldown     = time.Second
)

type authResult int

const (
	authUnchecked authResult = iota
	authFailed
	authSucceeded
)

type authPeer struct {
	active   int
	failures int
	retryAt  time.Time
}

// authGuard bounds database reads and bcrypt work together, without queuing
// requests. Eight failures from a socket peer within a second impose a one
// second cooldown. Rejected requests do not extend it. Successful checks clear
// previous failures. Neither usernames nor forwarded headers choose a bucket.
// The peer table has a hard cap: when all entries are live, new peers are
// refused until one expires, rather than evicting a peer's failure history.
// The zero value is ready for use.
type authGuard struct {
	mu     sync.Mutex
	active int
	peers  map[string]*authPeer
}

func authPeerKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap().String()
	}
	// Real HTTP socket addresses are IPs. Keep malformed synthetic addresses
	// in one bucket too, rather than retaining arbitrary strings.
	return "unknown"
}

func (g *authGuard) begin(peer string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= authConcurrency {
		return false
	}
	p := g.peers[peer]
	if p == nil {
		if len(g.peers) >= authPeerCapacity {
			for key, entry := range g.peers {
				if entry.active == 0 && !now.Before(entry.retryAt) {
					delete(g.peers, key)
				}
			}
			if len(g.peers) >= authPeerCapacity {
				return false
			}
		}
		if g.peers == nil {
			g.peers = make(map[string]*authPeer)
		}
		p = &authPeer{}
		g.peers[peer] = p
	}
	if !now.Before(p.retryAt) {
		p.failures = 0
	}
	if p.failures >= authFailureBurst {
		return false
	}
	g.active++
	p.active++
	return true
}

func (g *authGuard) finish(peer string, result authResult, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.peers[peer]
	g.active--
	p.active--
	switch result {
	case authFailed:
		if !now.Before(p.retryAt) {
			p.failures = 0
			p.retryAt = now.Add(authCooldown)
		}
		p.failures++
		if p.failures >= authFailureBurst {
			p.retryAt = now.Add(authCooldown)
		}
	case authSucceeded:
		p.failures = 0
		p.retryAt = time.Time{}
	}
	if p.active == 0 && p.failures == 0 {
		delete(g.peers, peer)
	}
}
