package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tofutools/awb/internal/domain"
)

func TestLoopbackHosts(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "127.0.0.2", "::1", "localhost", "LOCALHOST"} {
		assert.True(t, domain.IsLoopbackHost(host), host)
	}
	for _, host := range []string{"", "0.0.0.0", "192.0.2.10", "::", "example.com", "localhost."} {
		assert.False(t, domain.IsLoopbackHost(host), host)
	}
}
