package local

import (
	"context"
	"slices"
	"sort"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// AddRelation records a relation, read with the addressed issue as the
// subject.
func (b *Backend) AddRelation(ctx context.Context, ref string, req backend.RelationRequest,
	ifMatch string) (*domain.Issue, error) {
	relType, err := domain.ParseRelationType(string(req.Type))
	if err != nil {
		return nil, err
	}

	return b.mutateRelation(ctx, ref, ifMatch, "relation_added",
		func(tx *storage.Tx, issue *domain.Issue, capture func(string) error) error {
			other, err := resolve(tx, req.Other)
			if err != nil {
				return err
			}
			if err := capture(other); err != nil {
				return err
			}
			if relType == domain.RelHasParent {
				existing, hasParent, err := tx.ParentOf(issue.ID)
				if err != nil {
					return err
				}
				if hasParent {
					if err := capture(existing); err != nil {
						return err
					}
				}
			}
			return addRelation(tx, issue.ID, relType, other, req.Force)
		})
}

// addRelation validates and stores one edge. It is shared with issue creation,
// where the relation flags of awb create read the same way.
//
// Every check reads the graph inside the same BEGIN IMMEDIATE transaction that
// performs the write, so none of them can be overtaken by a concurrent commit.
func addRelation(tx *storage.Tx, subject string, relType domain.RelationType,
	other string, force bool) error {
	if err := domain.CheckSelfRelation(relType, subject, other); err != nil {
		return err
	}

	// A symmetric related pair is stored once, canonically, so adding it from
	// either end is the same edge.
	storedSubject, storedOther := domain.CanonicalRelation(relType, subject, other)

	// Adding a relation that already exists succeeds and changes nothing. For
	// has-parent this is also the "naming the parent it already has" case, which
	// needs no --force.
	exists, err := tx.RelationExists(storedSubject, relType, storedOther)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	if relType == domain.RelHasParent {
		return addParent(tx, subject, other, force)
	}

	if relType.Acyclic() {
		// Adding "subject relType other" closes a loop exactly when other already
		// reaches subject by following that same relation.
		reaches, err := tx.Reaches(relType, other, subject)
		if err != nil {
			return err
		}
		if err := domain.CheckCycle(relType, subject, other, reaches); err != nil {
			return err
		}
	}

	if relType == domain.RelBlockedBy {
		// Adding a blocked-by edge moves one edge into a fixed decomposition, so it
		// is enough to test the other endpoint against the subject's ancestor and
		// descendant sets.
		relatives, err := tx.Ancestors(subject)
		if err != nil {
			return err
		}
		descendants, err := tx.Descendants(subject)
		if err != nil {
			return err
		}
		for id := range descendants {
			relatives[id] = struct{}{}
		}
		if err := domain.CheckBlockedByDecomposition(subject, other, relatives); err != nil {
			return err
		}
	}

	return tx.InsertRelation(storedSubject, relType, storedOther)
}

// addParent sets or replaces a parent.
//
// An issue has at most one parent; naming a different one fails unless force
// is given, which replaces it.
//
// The parent it replaces is read unscoped, and may therefore be an issue in a
// project the caller has no access to. That is deliberate on both counts: the
// edge is stored on the child, which is the caller's to change — deleting the
// child outright would remove it too — and the child's own relations already
// name that parent, so the refusal below reveals nothing the caller cannot
// read. Reading it scoped would instead leave an issue whose parent could
// neither be seen nor replaced.
func addParent(tx *storage.Tx, child, parent string, force bool) error {
	existing, hasParent, err := tx.ParentOf(child)
	if err != nil {
		return err
	}
	if hasParent && existing != parent && !force {
		return awberr.Conflictf("%s already has parent %s; use --force to replace it", child, existing)
	}

	reaches, err := tx.Reaches(domain.RelHasParent, parent, child)
	if err != nil {
		return err
	}
	if err := domain.CheckCycle(domain.RelHasParent, child, parent, reaches); err != nil {
		return err
	}

	// Replacing the edge first means the two sets below are computed from the
	// graph as it would be after the change, which is what the rule requires —
	// and what makes the cheaper blocked-by test correct. The whole thing is
	// inside one transaction, so a refusal rolls the removal back.
	if hasParent {
		if err := tx.DeleteRelation(child, domain.RelHasParent, existing); err != nil {
			return err
		}
	}
	if err := tx.InsertRelation(child, domain.RelHasParent, parent); err != nil {
		return err
	}

	// Adding or replacing a has-parent edge moves a whole subtree under a new
	// chain of ancestors, and the edge that ends up violating the rule is then
	// some existing blocked-by edge, neither of whose endpoints is an endpoint of
	// the edge being added. So the check is over the subtree rooted at the child
	// and the new parent's ancestor chain, the child and the new parent included.
	subtree, err := tx.Subtree(child)
	if err != nil {
		return err
	}
	chain, err := tx.AncestorChainIncluding(parent)
	if err != nil {
		return err
	}
	edges, err := tx.BlockedByEdges()
	if err != nil {
		return err
	}
	return domain.CheckParentDecomposition(subtree, chain, edges)
}

// RemoveRelation removes a relation, taking the same two ids in the same order
// as adding one. Removing one that does not exist succeeds and changes
// nothing.
func (b *Backend) RemoveRelation(ctx context.Context, ref string, relType domain.RelationType,
	other string, ifMatch string) (*domain.Issue, error) {
	parsed, err := domain.ParseRelationType(string(relType))
	if err != nil {
		return nil, err
	}

	return b.mutateRelation(ctx, ref, ifMatch, "relation_removed",
		func(tx *storage.Tx, issue *domain.Issue, capture func(string) error) error {
			otherID, err := resolve(tx, other)
			if err != nil {
				return err
			}
			if err := capture(otherID); err != nil {
				return err
			}
			// Removal works in either order for a symmetric relation, because both ends
			// canonicalise to the same stored edge.
			subject, counterpart := domain.CanonicalRelation(parsed, issue.ID, otherID)
			return tx.DeleteRelation(subject, parsed, counterpart)
		})
}

// mutateRelation records the graph change on every endpoint whose issue view
// changed. Relation rows are stored once but displayed from both ends, so a
// one-sided activity event would leave the counterpart's timeline incomplete.
func (b *Backend) mutateRelation(ctx context.Context, ref, ifMatch, action string,
	apply func(*storage.Tx, *domain.Issue, func(string) error) error) (*domain.Issue, error) {
	var result *domain.Issue
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		if err := ensureIssueWritable(tx, issue); err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, issue.UpdatedAt, "the issue"); err != nil {
			return err
		}

		before := relationSnapshots{}
		capture := before.capture(tx)
		if err := capture(issue.ID); err != nil {
			return err
		}
		if err := apply(tx, issue, capture); err != nil {
			return err
		}
		result, err = tx.GetIssue(issue.ID)
		if err != nil {
			return err
		}

		return before.record(tx, caller, action)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type relationSnapshots map[string][]domain.Relation

// capture returns a callback that remembers an endpoint's relation view once,
// before the mutation starts changing graph rows.
func (s relationSnapshots) capture(tx *storage.Tx) func(string) error {
	return func(id string) error {
		if _, ok := s[id]; ok {
			return nil
		}
		state, err := tx.IssueProjectState(id)
		if err != nil {
			return err
		}
		if state == domain.ProjectArchived {
			return awberr.Conflictf("the relation touches archived read-only workspace work")
		}
		relations, err := tx.IssueRelations(id)
		if err != nil {
			return err
		}
		s[id] = relations
		return nil
	}
}

// record compares every captured endpoint with the graph after the mutation
// and appends one activity row for each issue whose visible relations changed.
func (s relationSnapshots) record(tx *storage.Tx, caller domain.Caller, action string) error {
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		after, err := tx.IssueRelations(id)
		if err != nil {
			return err
		}
		if slices.Equal(s[id], after) {
			continue
		}
		changes := []domain.ActivityChange{{
			Field: "relations", From: activityJSON(s[id]), To: activityJSON(after),
		}}
		if err := recordChange(tx, caller, id, action, changes); err != nil {
			return err
		}
	}
	return nil
}
