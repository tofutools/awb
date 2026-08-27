package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

func assertConflict(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	require.Error(t, err, msgAndArgs...)
	assert.Equal(t, awberr.Conflict, awberr.KindOf(err), msgAndArgs...)
}

func set(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// A symmetric related pair is stored with the byte-wise smaller ID as subject,
// so adding it from either end is the same edge.
func TestCanonicalRelation(t *testing.T) {
	a, b := domain.CanonicalRelation(domain.RelRelated, "awb-b", "awb-a")
	assert.Equal(t, "awb-a", a)
	assert.Equal(t, "awb-b", b)

	a, b = domain.CanonicalRelation(domain.RelRelated, "awb-a", "awb-b")
	assert.Equal(t, "awb-a", a)
	assert.Equal(t, "awb-b", b)

	// Comparison is over the whole ID string, so the project part counts.
	a, b = domain.CanonicalRelation(domain.RelRelated, "zzz-a", "aaa-z")
	assert.Equal(t, "aaa-z", a)
	assert.Equal(t, "zzz-a", b)

	// Directed relations are stored exactly as written.
	for _, rt := range []domain.RelationType{
		domain.RelBlockedBy, domain.RelHasParent, domain.RelDiscoveredFrom,
	} {
		a, b = domain.CanonicalRelation(rt, "awb-b", "awb-a")
		assert.Equal(t, "awb-b", a, string(rt))
		assert.Equal(t, "awb-a", b, string(rt))
	}
}

func TestCheckSelfRelation(t *testing.T) {
	for _, rt := range domain.RelationTypes {
		assertConflict(t, domain.CheckSelfRelation(rt, "awb-a", "awb-a"), string(rt))
		assert.NoError(t, domain.CheckSelfRelation(rt, "awb-a", "awb-b"), string(rt))
	}
}

func TestCheckCycle(t *testing.T) {
	// The three directed graphs are each checked, separately.
	for _, rt := range []domain.RelationType{
		domain.RelBlockedBy, domain.RelHasParent, domain.RelDiscoveredFrom,
	} {
		assertConflict(t, domain.CheckCycle(rt, "awb-a", "awb-b", true), string(rt))
		assert.NoError(t, domain.CheckCycle(rt, "awb-a", "awb-b", false), string(rt))
	}

	// related has no direction to run in a circle, so it is unconstrained.
	assert.NoError(t, domain.CheckCycle(domain.RelRelated, "awb-a", "awb-b", true))
}

// An issue may not be blocked-by any ancestor or descendant in the has-parent
// graph: a child waiting for its own parent, or a parent for its own child,
// describes work that cannot sensibly be ordered.
func TestCheckBlockedByDecomposition(t *testing.T) {
	relatives := set("awb-parent", "awb-grandparent", "awb-child", "awb-grandchild")

	for _, other := range []string{"awb-parent", "awb-grandparent", "awb-child", "awb-grandchild"} {
		assertConflict(t, domain.CheckBlockedByDecomposition("awb-me", other, relatives), other)
	}
	assert.NoError(t, domain.CheckBlockedByDecomposition("awb-me", "awb-unrelated", relatives))
	assert.NoError(t, domain.CheckBlockedByDecomposition("awb-me", "awb-x", set()))
}

// Adding or replacing a has-parent edge moves a whole subtree under a new
// chain of ancestors, and the edge that ends up violating the rule is some
// *existing* blocked-by edge, neither of whose endpoints is an endpoint of the
// edge being added.
func TestCheckParentDecomposition(t *testing.T) {
	subtree := set("awb-child", "awb-gchild")
	chain := set("awb-parent", "awb-gparent")

	t.Run("refuses an existing edge from the subtree into the chain", func(t *testing.T) {
		edges := []domain.BlockedByEdge{{Subject: "awb-gchild", Other: "awb-gparent"}}
		assertConflict(t, domain.CheckParentDecomposition(subtree, chain, edges))
	})

	t.Run("refuses it in the other direction too", func(t *testing.T) {
		edges := []domain.BlockedByEdge{{Subject: "awb-gparent", Other: "awb-gchild"}}
		assertConflict(t, domain.CheckParentDecomposition(subtree, chain, edges))
	})

	t.Run("allows edges with an end outside both sets", func(t *testing.T) {
		edges := []domain.BlockedByEdge{
			{Subject: "awb-gchild", Other: "awb-outside"},
			{Subject: "awb-outside", Other: "awb-gparent"},
			{Subject: "awb-x", Other: "awb-y"},
		}
		assert.NoError(t, domain.CheckParentDecomposition(subtree, chain, edges))
	})

	t.Run("allows edges within the subtree or within the chain", func(t *testing.T) {
		edges := []domain.BlockedByEdge{
			{Subject: "awb-child", Other: "awb-gchild"},
			{Subject: "awb-parent", Other: "awb-gparent"},
		}
		// Both ends inside one set is not this rule's business: whether an issue may
		// be blocked-by a sibling is not what this rule forbids.
		assert.NoError(t, domain.CheckParentDecomposition(subtree, chain, edges))
	})

	t.Run("no edges at all is fine", func(t *testing.T) {
		assert.NoError(t, domain.CheckParentDecomposition(subtree, chain, nil))
	})
}

func TestRelationTypeProperties(t *testing.T) {
	assert.True(t, domain.RelBlockedBy.Acyclic())
	assert.True(t, domain.RelHasParent.Acyclic())
	assert.True(t, domain.RelDiscoveredFrom.Acyclic())
	assert.False(t, domain.RelRelated.Acyclic())

	assert.True(t, domain.RelRelated.Symmetric())
	assert.False(t, domain.RelBlockedBy.Symmetric())
	assert.False(t, domain.RelHasParent.Symmetric())
	assert.False(t, domain.RelDiscoveredFrom.Symmetric())
}
