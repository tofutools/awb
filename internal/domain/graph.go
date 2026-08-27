package domain

import "github.com/tofutools/awb/internal/awberr"

// The relation graph rules of SPEC §2.3.
//
// Traversing the graph is the storage layer's job — it does it in SQL, inside
// the same BEGIN IMMEDIATE transaction that performs the write, so no
// concurrent commit can slip between the check and the change. What lives here
// is the rules themselves, stated over the sets storage gathers, so that both
// surfaces enforce one definition of a legal graph.

// CanonicalRelation orders the two ends of a relation for storage. A symmetric
// related pair is stored with the smaller ID — comparing the whole ID string
// byte for byte — as subject, so adding it from either end is the same edge,
// removal works in either order, and both views use direction "out". A directed
// relation is stored as written.
func CanonicalRelation(t RelationType, subject, other string) (string, string) {
	if t.Symmetric() && other < subject {
		return other, subject
	}
	return subject, other
}

// CheckSelfRelation refuses a relation from an issue to itself, which SPEC §2.3
// treats as a cycle and gives exit code 4.
func CheckSelfRelation(t RelationType, subject, other string) error {
	if subject == other {
		return awberr.Conflictf("%s cannot be %s itself", subject, t)
	}
	return nil
}

// CheckCycle refuses an edge that would close a loop. The blocked-by,
// has-parent and discovered-from graphs must each remain acyclic and are
// checked separately: work cannot depend on itself, decomposition cannot nest
// inside itself, and an issue cannot be, however indirectly, its own origin.
//
// Adding "subject t other" closes a loop exactly when other already reaches
// subject by following t, which is what otherReachesSubject reports. Only
// related is unconstrained, having no direction to run in a circle.
func CheckCycle(t RelationType, subject, other string, otherReachesSubject bool) error {
	if !t.Acyclic() || !otherReachesSubject {
		return nil
	}
	switch t {
	case RelBlockedBy:
		return awberr.Conflictf("%s blocked-by %s would create a dependency cycle", subject, other)
	case RelHasParent:
		return awberr.Conflictf("%s has-parent %s would nest the decomposition inside itself", subject, other)
	case RelDiscoveredFrom:
		return awberr.Conflictf("%s discovered-from %s would make the issue its own origin", subject, other)
	default:
		return awberr.Conflictf("%s %s %s would create a cycle", subject, t, other)
	}
}

// CheckBlockedByDecomposition refuses a blocked-by edge that inverts
// decomposition (SPEC §2.3): an issue may not be blocked-by any ancestor or
// descendant in the has-parent graph, because a child waiting for its own
// parent, or a parent for its own child, describes work that cannot sensibly be
// ordered.
//
// Adding a blocked-by edge moves one edge into a fixed decomposition, so it is
// enough to test the other endpoint for membership in the subject's ancestor
// and descendant sets. relatives holds both, the subject excluded — a
// self-relation is CheckSelfRelation's refusal, not this one.
func CheckBlockedByDecomposition(subject, other string, relatives map[string]struct{}) error {
	if _, related := relatives[other]; related {
		return awberr.Conflictf(
			"%s blocked-by %s is not allowed: they are parent and child in the same decomposition",
			subject, other)
	}
	return nil
}

// BlockedByEdge is one stored "Subject blocked-by Other" edge.
type BlockedByEdge struct {
	Subject string
	Other   string
}

// CheckParentDecomposition refuses a has-parent edge that would invert
// decomposition (SPEC §2.3).
//
// Adding or replacing a has-parent edge moves a whole subtree under a new chain
// of ancestors, and the edge that ends up violating the rule is then some
// *existing* blocked-by edge, neither of whose endpoints is an endpoint of the
// edge being added. So the check is over the subtree rooted at the child and
// the new parent's ancestor chain — the child and the new parent included — and
// the edge is refused when any blocked-by edge has one end in that subtree and
// the other in that chain.
//
// Both sets are computed from the graph as it would be after the change, which
// is also what makes the cheaper CheckBlockedByDecomposition test correct.
func CheckParentDecomposition(subtree, chain map[string]struct{}, edges []BlockedByEdge) error {
	for _, e := range edges {
		_, subjectInSubtree := subtree[e.Subject]
		_, otherInSubtree := subtree[e.Other]
		_, subjectInChain := chain[e.Subject]
		_, otherInChain := chain[e.Other]

		if (subjectInSubtree && otherInChain) || (otherInSubtree && subjectInChain) {
			return awberr.Conflictf(
				"this parent is not allowed: %s blocked-by %s would then be between parent and child in one decomposition",
				e.Subject, e.Other)
		}
	}
	return nil
}
