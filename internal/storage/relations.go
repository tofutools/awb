package storage

import (
	"database/sql"
	"errors"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// RelationExists reports whether this exact stored edge is present. The caller
// canonicalises a symmetric relation first, so adding one from either end
// finds the same edge.
func (t *Tx) RelationExists(subject string, relType domain.RelationType, other string) (bool, error) {
	var one int
	err := t.q.QueryRowContext(t.ctx,
		`SELECT 1 FROM relations WHERE subject = ? AND type = ? AND other = ?`,
		subject, relType, other).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read relation")
	}
	return true, nil
}

// ParentOf returns the issue's parent, and whether it has one.
func (t *Tx) ParentOf(id string) (string, bool, error) {
	var parent string
	err := t.q.QueryRowContext(t.ctx,
		`SELECT other FROM relations WHERE subject = ? AND type = 'has-parent'`, id).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, awberr.Wrap(awberr.Runtime, err, "read parent of %s", id)
	}
	return parent, true, nil
}

// IssueRelations returns every relation visible from one endpoint. It is
// deliberately unscoped, like the graph-rule queries below: relation writes
// change both endpoints, and their activity rows must be recorded together
// even when an old parent is outside the caller's visibility scope.
//
// Callers must not return this snapshot to a user. It exists only to compare
// the graph immediately before and after a write in the same transaction.
func (t *Tx) IssueRelations(id string) ([]domain.Relation, error) {
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT type, other, 'out' AS direction
		  FROM relations WHERE subject = ?
		UNION ALL
		SELECT type, subject,
		       CASE WHEN type = 'related' THEN 'out' ELSE 'in' END AS direction
		  FROM relations WHERE other = ?`, id, id)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read relations for %s", id)
	}
	defer rows.Close()

	relations := []domain.Relation{}
	for rows.Next() {
		var relation domain.Relation
		if err := rows.Scan(&relation.Type, &relation.Other, &relation.Direction); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read relations for %s", id)
		}
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read relations for %s", id)
	}
	domain.SortRelations(relations)
	return relations, nil
}

// InsertRelation stores an edge. Adding one that already exists succeeds and
// changes nothing; adding or removing a relation does not change either
// endpoint's updated_at.
//
// The idempotent re-add is done by looking first rather than with INSERT OR
// IGNORE, because the relations table carries a second unique index — at most
// one has-parent per subject — and IGNORE would swallow a genuine
// second-parent conflict along with the harmless duplicate. The caller checks
// for an existing different parent and reports it better; this is the backstop
// that keeps the storage layer from silently dropping the write.
func (t *Tx) InsertRelation(subject string, relType domain.RelationType, other string) error {
	exists, err := t.RelationExists(subject, relType, other)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = t.q.ExecContext(t.ctx,
		`INSERT INTO relations (subject, type, other) VALUES (?, ?, ?)`,
		subject, relType, other)
	if err != nil {
		if isUniqueViolation(err) && relType == domain.RelHasParent {
			return awberr.Conflictf("%s already has a different parent", subject)
		}
		if isUniqueViolation(err) {
			return nil // raced with an identical insert; the edge is there
		}
		return awberr.Wrap(awberr.Runtime, err, "add relation")
	}
	return nil
}

// DeleteRelation removes an edge. Removing one that does not exist succeeds
// and changes nothing.
func (t *Tx) DeleteRelation(subject string, relType domain.RelationType, other string) error {
	_, err := t.q.ExecContext(t.ctx,
		`DELETE FROM relations WHERE subject = ? AND type = ? AND other = ?`,
		subject, relType, other)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "remove relation")
	}
	return nil
}

// Reaches reports whether "from" reaches "to" by following relType edges from
// subject to other, which is what decides whether adding "to relType from"
// would close a cycle.
//
// Each relation type is a graph of its own and is walked separately.
//
// This and the four walks below are deliberately unscoped, as is
// BlockedByEdges. They answer the graph rules, and a rule answered over half a
// graph is not the rule: a caller who cannot see part of a cycle could
// otherwise close one. What follows is that a relation can be refused on
// account of an edge the caller cannot see, which the API document states.
func (t *Tx) Reaches(relType domain.RelationType, from, to string) (bool, error) {
	var one int
	err := t.q.QueryRowContext(t.ctx, `
		WITH RECURSIVE reachable(id) AS (
			SELECT ?
			UNION
			SELECT r.other FROM relations r
			  JOIN reachable ON reachable.id = r.subject
			 WHERE r.type = ?
		)
		SELECT 1 FROM reachable WHERE id = ? LIMIT 1`, from, relType, to).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "walk the %s graph", relType)
	}
	return true, nil
}

// Ancestors returns the issue's ancestors in the has-parent graph, the issue
// itself excluded.
func (t *Tx) Ancestors(id string) (map[string]struct{}, error) {
	return t.walk(`
		WITH RECURSIVE chain(id) AS (
			SELECT ?
			UNION
			SELECT r.other FROM relations r
			  JOIN chain ON chain.id = r.subject
			 WHERE r.type = 'has-parent'
		)
		SELECT id FROM chain WHERE id <> ?`, id, id)
}

// Descendants returns the issue's descendants in the has-parent graph, the
// issue itself excluded.
func (t *Tx) Descendants(id string) (map[string]struct{}, error) {
	return t.walk(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT r.subject FROM relations r
			  JOIN subtree ON subtree.id = r.other
			 WHERE r.type = 'has-parent'
		)
		SELECT id FROM subtree WHERE id <> ?`, id, id)
}

// Subtree returns the issue and everything below it in the has-parent graph.
func (t *Tx) Subtree(id string) (map[string]struct{}, error) {
	return t.walk(`
		WITH RECURSIVE subtree(id) AS (
			SELECT ?
			UNION
			SELECT r.subject FROM relations r
			  JOIN subtree ON subtree.id = r.other
			 WHERE r.type = 'has-parent'
		)
		SELECT id FROM subtree`, id)
}

// AncestorChainIncluding returns the issue and all of its ancestors in the
// has-parent graph.
func (t *Tx) AncestorChainIncluding(id string) (map[string]struct{}, error) {
	return t.walk(`
		WITH RECURSIVE chain(id) AS (
			SELECT ?
			UNION
			SELECT r.other FROM relations r
			  JOIN chain ON chain.id = r.subject
			 WHERE r.type = 'has-parent'
		)
		SELECT id FROM chain`, id)
}

func (t *Tx) walk(query string, args ...any) (map[string]struct{}, error) {
	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "walk the decomposition")
	}
	defer rows.Close()

	found := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "walk the decomposition")
		}
		found[id] = struct{}{}
	}
	return found, awberr.Wrap(awberr.Runtime, rows.Err(), "walk the decomposition")
}

// BlockedByEdges returns every stored blocked-by edge. It is read when a
// has-parent edge is added or replaced, where the edge that ends up violating
// the decomposition rule is some existing blocked-by edge neither of whose
// endpoints is an endpoint of the edge being added.
//
// It is ordered because the refusal names the offending edge: when more than
// one violates the rule, the same one is named every time.
func (t *Tx) BlockedByEdges() ([]domain.BlockedByEdge, error) {
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT subject, other FROM relations WHERE type = 'blocked-by'
		  ORDER BY subject ASC, other ASC`)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read the dependency graph")
	}
	defer rows.Close()

	var edges []domain.BlockedByEdge
	for rows.Next() {
		var e domain.BlockedByEdge
		if err := rows.Scan(&e.Subject, &e.Other); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read the dependency graph")
		}
		edges = append(edges, e)
	}
	return edges, awberr.Wrap(awberr.Runtime, rows.Err(), "read the dependency graph")
}

// Children returns the direct children of an issue, ordered as siblings are
// ordered everywhere: priority ascending, then created_at, then id.
//
// The walk crosses project boundaries, so the scope applies: a child in a
// project the caller is not a member of is not listed, and neither is the
// subtree below it. A tree is therefore what the caller can see of a
// decomposition, not a claim that it is the whole of one.
func (t *Tx) Children(id string) ([]*domain.Issue, error) {
	visible, scopeArgs := t.visibleClause("issues.project")
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT `+issueColumns+`
		  FROM issues
		 WHERE id IN (SELECT subject FROM relations WHERE type = 'has-parent' AND other = ?)
		   AND `+visible+`
		 ORDER BY priority ASC, created_at ASC, id ASC`, append([]any{id}, scopeArgs...)...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read children of %s", id)
	}
	defer rows.Close()

	var children []*domain.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read children of %s", id)
		}
		children = append(children, issue)
	}
	return children, awberr.Wrap(awberr.Runtime, rows.Err(), "read children of %s", id)
}

// Tree returns the subtree of children rooted at an issue, to its full depth,
// following children across project boundaries and showing closed children
// too. It does not show ancestors.
//
// The whole subtree is gathered first and hydrated in one pass, so the derived
// fields of every node are filled in before any of them is copied into the
// tree.
func (t *Tx) Tree(id string) (*domain.IssueTree, error) {
	root, err := t.getIssueRow(id)
	if err != nil {
		return nil, err
	}

	var all []*domain.Issue
	childrenOf := make(map[string][]*domain.Issue)
	seen := make(map[string]struct{})

	// The has-parent graph is acyclic by construction — the unique index gives
	// each issue at most one parent and the cycle check guards the rest — but
	// seen makes the walk terminate regardless.
	var gather func(*domain.Issue) error
	gather = func(issue *domain.Issue) error {
		if _, repeat := seen[issue.ID]; repeat {
			return nil
		}
		seen[issue.ID] = struct{}{}
		all = append(all, issue)

		children, err := t.Children(issue.ID)
		if err != nil {
			return err
		}
		childrenOf[issue.ID] = children
		for _, child := range children {
			if err := gather(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := gather(root); err != nil {
		return nil, err
	}

	if err := t.hydrate(all); err != nil {
		return nil, err
	}

	built := make(map[string]struct{})
	var build func(*domain.Issue) domain.IssueTree
	build = func(issue *domain.Issue) domain.IssueTree {
		node := domain.IssueTree{Issue: *issue, Children: []domain.IssueTree{}}
		if _, repeat := built[issue.ID]; repeat {
			return node
		}
		built[issue.ID] = struct{}{}
		for _, child := range childrenOf[issue.ID] {
			node.Children = append(node.Children, build(child))
		}
		return node
	}

	tree := build(root)
	return &tree, nil
}
