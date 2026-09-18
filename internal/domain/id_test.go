package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// A fixed vector pins the client ID algorithm shared with the web UI.
func TestIssueHashFixedVector(t *testing.T) {
	got := domain.IssueHash("alex", "Create tickets in the UI", domain.TypeFeature,
		"Use the shared editor.")

	assert.Len(t, got, domain.HashLen)
	assert.True(t, domain.IsHex(got))
	assert.Equal(t, "20eb8c", got)
}

func TestIssueHashDependsOnEveryInput(t *testing.T) {
	base := domain.IssueHash("alex", "title", domain.TypeTask, "description")
	assert.NotEqual(t, base, domain.IssueHash("alexa", "title", domain.TypeTask, "description"))
	assert.NotEqual(t, base, domain.IssueHash("alex", "other", domain.TypeTask, "description"))
	assert.NotEqual(t, base, domain.IssueHash("alex", "title", domain.TypeBug, "description"))
	assert.NotEqual(t, base, domain.IssueHash("alex", "title", domain.TypeTask, "other"))
}

func TestSplitIDUsesTheLastHyphen(t *testing.T) {
	cases := []struct {
		id        string
		workspace string
		hash      string
		ok        bool
	}{
		{"awb-a3f9c1", "awb", "a3f9c1", true},
		{"web-ui-a3f9c1", "web-ui", "a3f9c1", true},
		{"a-b-c-d1", "a-b-c", "d1", true},
		{"nohyphen", "", "", false},
		{"-leading", "", "", false},
		{"trailing-", "", "", false},
	}
	for _, tc := range cases {
		workspace, hash, ok := domain.SplitID(tc.id)
		assert.Equal(t, tc.ok, ok, tc.id)
		assert.Equal(t, tc.workspace, workspace, tc.id)
		assert.Equal(t, tc.hash, hash, tc.id)
	}
}

func TestMakeIDRoundTrips(t *testing.T) {
	workspace, hash, ok := domain.SplitID(domain.MakeID("web-ui", "a3f9c1"))
	require.True(t, ok)
	assert.Equal(t, "web-ui", workspace)
	assert.Equal(t, "a3f9c1", hash)
}

func TestParseIssueRef(t *testing.T) {
	t.Run("full id", func(t *testing.T) {
		ref, err := domain.ParseIssueRef("awb-a3f9c1")
		require.NoError(t, err)
		assert.Equal(t, "awb", ref.Workspace)
		assert.Equal(t, "a3f9c1", ref.Hash)
	})

	t.Run("id prefix", func(t *testing.T) {
		ref, err := domain.ParseIssueRef("awb-a3f")
		require.NoError(t, err)
		assert.Equal(t, "awb", ref.Workspace)
		assert.Equal(t, "a3f", ref.Hash)
	})

	t.Run("bare hash carries no workspace", func(t *testing.T) {
		ref, err := domain.ParseIssueRef("a3f9c1")
		require.NoError(t, err)
		assert.Equal(t, "", ref.Workspace)
		assert.Equal(t, "a3f9c1", ref.Hash)
	})

	t.Run("is lower-cased so capitals resolve", func(t *testing.T) {
		ref, err := domain.ParseIssueRef("AWB-A3F9C1")
		require.NoError(t, err)
		assert.Equal(t, "awb", ref.Workspace)
		assert.Equal(t, "a3f9c1", ref.Hash)
	})

	t.Run("workspace keys may contain hyphens", func(t *testing.T) {
		ref, err := domain.ParseIssueRef("web-ui-a3f")
		require.NoError(t, err)
		assert.Equal(t, "web-ui", ref.Workspace)
		assert.Equal(t, "a3f", ref.Hash)
	})

	t.Run("rejects", func(t *testing.T) {
		for _, s := range []string{"", "   ", "awb-", "-a3f", "awb-zzz", "1bad-a3f", "awb a3f"} {
			_, err := domain.ParseIssueRef(s)
			require.Error(t, err, "%q", s)
			assert.Equal(t, awberr.Usage, awberr.KindOf(err), "%q", s)
		}
	})
}

func TestIsHex(t *testing.T) {
	assert.True(t, domain.IsHex("a3f9c1"))
	assert.True(t, domain.IsHex("0"))
	assert.False(t, domain.IsHex(""))
	assert.False(t, domain.IsHex("A3F"), "an ID is lower-cased before matching")
	assert.False(t, domain.IsHex("g1"))
}

// An identifier is lower-cased before matching and nothing else is touched, so
// a stray space is a mistake to report rather than one to paper over.
// Regression: the reference used to be trimmed.
func TestParseIssueRefDoesNotTrim(t *testing.T) {
	for _, ref := range []string{" awb-a3f9c1", "awb-a3f9c1 ", " a3f9c1 ", "\tawb-a3f9c1"} {
		_, err := domain.ParseIssueRef(ref)
		require.Error(t, err, "%q", ref)
		assert.Equal(t, awberr.Usage, awberr.KindOf(err), "%q", ref)
	}

	// The exact reference still resolves, in any case.
	ref, err := domain.ParseIssueRef("AWB-A3F9C1")
	require.NoError(t, err)
	assert.Equal(t, "awb", ref.Workspace)
	assert.Equal(t, "a3f9c1", ref.Hash)
}
