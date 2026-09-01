package remote_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/remote"
)

func TestRemoteReadsEffectiveUserAdministrationFromIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/identity", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"identity":"fixed-name","may_manage_users":true}`))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "configured-name")
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	identity, err := client.AuthenticatedIdentity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "fixed-name", identity)
	allowed, err := client.MayManageUsers(t.Context())
	require.NoError(t, err)
	assert.True(t, allowed)
}
