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

func TestTreeKeepsChildrenAndRelationTitlesAcrossTheRemoteBackend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/issues/awb-root/tree", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"awb-root","relations":[],"children":[{
				"id":"awb-child","relations":[{
					"type":"has-parent","other":"awb-root","other_title":"A complete root title","direction":"out"
				}],"children":[]
			}]
		}`))
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "operator", false)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	tree, err := client.Tree(t.Context(), "awb-root")
	require.NoError(t, err)
	require.Len(t, tree.Children, 1)
	assert.Equal(t, "awb-child", tree.Children[0].ID)
	assert.Equal(t, "A complete root title", tree.Children[0].RelationTitle("awb-root"))
}
