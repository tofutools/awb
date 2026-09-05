package domain

import (
	"net"
	"strings"
)

// IsLoopbackHost reports whether a host names this machine without consulting
// DNS. localhost is the one hostname with that guaranteed meaning; every other
// name is treated as remote so a changing or attacker-controlled resolution
// cannot turn cleartext transport into a trusted one.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
