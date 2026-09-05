package storage_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Init(t.Context(), filepath.Join(t.TempDir(), "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seed creates a workspace and returns a helper that adds issues to it.
func seed(t *testing.T, db *storage.DB) func(title string, mutate ...func(*domain.Issue)) string {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertWorkspace("awb", "Agent Work Board", "")
	}))

	return func(title string, mutate ...func(*domain.Issue)) string {
		t.Helper()
		issue := &domain.Issue{
			Workspace: "awb", Title: title, Type: domain.DefaultType,
			Status: domain.DefaultStatus, Priority: domain.DefaultPriority,
		}
		for _, m := range mutate {
			m(issue)
		}
		require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
			return tx.InsertIssue(issue)
		}))
		return issue.ID
	}
}

func read[T any](t *testing.T, db *storage.DB, fn func(*storage.Tx) (T, error)) T {
	t.Helper()
	var result T
	require.NoError(t, db.Read(t.Context(), func(tx *storage.Tx) error {
		var err error
		result, err = fn(tx)
		return err
	}))
	return result
}

func TestInsertAndGetIssue(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	id := add("Parser crashes on empty input", func(i *domain.Issue) {
		i.Type = domain.TypeBug
		i.Priority = 1
		i.Description = "See [CI](https://ci.example.com/1)."
	})

	issue := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(id) })

	assert.Equal(t, "awb", issue.Workspace)
	assert.Equal(t, "Parser crashes on empty input", issue.Title)
	assert.Equal(t, domain.TypeBug, issue.Type)
	assert.Equal(t, domain.StatusOpen, issue.Status)
	assert.Equal(t, 1, issue.Priority)
	assert.Empty(t, issue.Assignees)
	assert.False(t, issue.Blocked)

	// Derived fields are always present and never null.
	assert.Equal(t, []string{}, issue.Labels)
	assert.Equal(t, []string{}, issue.Blockers)
	assert.Equal(t, []domain.Relation{}, issue.Relations)
	assert.Equal(t, []domain.Link{{Text: "CI", URL: "https://ci.example.com/1"}}, issue.Links)

	// The ID is <workspace-key>-<hash>.
	workspace, hash, ok := domain.SplitID(issue.ID)
	require.True(t, ok)
	assert.Equal(t, "awb", workspace)
	assert.Len(t, hash, domain.HashLen)
}

func TestGetIssueNotFound(t *testing.T) {
	db := newDB(t)
	seed(t, db)

	err := db.Read(t.Context(), func(tx *storage.Tx) error {
		_, err := tx.GetIssue("awb-nope01")
		return err
	})
	require.Error(t, err)
	assert.Equal(t, 3, exitOf(err))
}

// Storage rejects a status and assignment set that disagree, whichever caller
// constructs the write.
func TestStatusAssigneeInvariantIsEnforcedByStorage(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	id := add("t")

	cases := []struct {
		name   string
		fields storage.IssueFields
		ok     bool
	}{
		{"open with no assignee", fields(domain.StatusOpen), true},
		{"open with an assignee", fields(domain.StatusOpen, "claude-1"), false},
		{"in_progress with an assignee", fields(domain.StatusInProgress, "claude-1"), true},
		{"in_progress with none", fields(domain.StatusInProgress), false},
		{"closed with an assignee", fields(domain.StatusClosed, "claude-1"), true},
		{"closed with none", fields(domain.StatusClosed), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Write(t.Context(), func(tx *storage.Tx) error {
				issue, err := tx.GetIssue(id)
				if err != nil {
					return err
				}
				return tx.UpdateIssue(issue, tc.fields)
			})
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func fields(status domain.Status, assignees ...string) storage.IssueFields {
	return storage.IssueFields{
		Title: "t", Type: domain.TypeTask, Priority: 2,
		Status: status, Assignees: assignees,
	}
}

// updated_at moves only when a stored field actually changes, and is strictly
// increasing per issue whatever the clock's resolution is.
func TestUpdatedAtOnlyMovesOnRealChange(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	id := add("t")

	before := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(id) })

	// A write that changes nothing leaves the timestamp alone.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue(id)
		if err != nil {
			return err
		}
		return tx.UpdateIssue(issue, storage.Fields(issue))
	}))
	unchanged := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(id) })
	assert.Equal(t, before.UpdatedAt, unchanged.UpdatedAt)

	// A real change moves it, and every successive version is strictly greater
	// even when several writes land inside one clock tick.
	previous := before.UpdatedAt
	for i := range 20 {
		require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
			issue, err := tx.GetIssue(id)
			if err != nil {
				return err
			}
			f := storage.Fields(issue)
			f.Priority = i % 5
			if f.Priority == issue.Priority {
				f.Priority = (i + 1) % 5
			}
			return tx.UpdateIssue(issue, f)
		}))
		current := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(id) })
		assert.Greater(t, current.UpdatedAt, previous,
			"two versions of one issue can never carry the same timestamp")
		previous = current.UpdatedAt
	}
}

// Adding or removing a label counts as a change to the issue; adding or
// removing a relation does not touch either endpoint.
func TestLabelsMoveUpdatedAtButRelationsDoNot(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	a := add("a")
	b := add("b")

	start := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue(a)
		if err != nil {
			return err
		}
		return tx.AddLabel(issue, "parser")
	}))
	labelled := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })
	assert.Greater(t, labelled.UpdatedAt, start.UpdatedAt)
	assert.Equal(t, []string{"parser"}, labelled.Labels)

	// Adding a label the issue already carries changes nothing.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue(a)
		if err != nil {
			return err
		}
		return tx.AddLabel(issue, "parser")
	}))
	again := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })
	assert.Equal(t, labelled.UpdatedAt, again.UpdatedAt)

	// A relation does not move either endpoint.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertRelation(a, domain.RelBlockedBy, b)
	}))
	related := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })
	assert.Equal(t, labelled.UpdatedAt, related.UpdatedAt)

	otherEnd := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(b) })
	assert.Equal(t, otherEnd.CreatedAt, otherEnd.UpdatedAt)

	// Removing a label the issue does not carry changes nothing either.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue(a)
		if err != nil {
			return err
		}
		return tx.RemoveLabel(issue, "absent")
	}))
	final := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })
	assert.Equal(t, related.UpdatedAt, final.UpdatedAt)
}

// A relation is stored once and shown on both issues; direction identifies the
// viewed endpoint, and a symmetric related pair is "out" at both ends.
func TestRelationsAreShownOnBothEnds(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	a := add("a")
	b := add("b")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertRelation(a, domain.RelBlockedBy, b); err != nil {
			return err
		}
		subject, other := domain.CanonicalRelation(domain.RelRelated, a, b)
		return tx.InsertRelation(subject, domain.RelRelated, other)
	}))

	issueA := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })
	issueB := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(b) })

	assert.Contains(t, issueA.Relations,
		domain.Relation{Type: domain.RelBlockedBy, Other: b, Direction: domain.DirectionOut})
	assert.Contains(t, issueB.Relations,
		domain.Relation{Type: domain.RelBlockedBy, Other: a, Direction: domain.DirectionIn})

	assert.Contains(t, issueA.Relations,
		domain.Relation{Type: domain.RelRelated, Other: b, Direction: domain.DirectionOut})
	assert.Contains(t, issueB.Relations,
		domain.Relation{Type: domain.RelRelated, Other: a, Direction: domain.DirectionOut},
		"a symmetric relation is always out")
	assert.Equal(t, "b", issueA.RelationTitle(b))
	assert.Equal(t, "a", issueB.RelationTitle(a))
}

// blocked is derived, so the recorded state can never disagree with the
// dependency graph.
func TestBlockedIsDerived(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	blocked := add("blocked")
	blocker := add("blocker")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertRelation(blocked, domain.RelBlockedBy, blocker)
	}))

	issue := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(blocked) })
	assert.True(t, issue.Blocked)
	assert.Equal(t, []string{blocker}, issue.Blockers)
	assert.False(t, issue.Ready())

	// Closing the blocker unblocks it, with no write to the blocked issue.
	closeIssue(t, db, blocker)

	issue = read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(blocked) })
	assert.False(t, issue.Blocked)
	assert.Equal(t, []string{}, issue.Blockers)
	assert.True(t, issue.Ready())
}

// A closed issue is never blocked and its blockers are empty, whatever its
// blocked-by relations still say.
func TestClosedIssueIsNeverBlocked(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	blocked := add("blocked")
	blocker := add("blocker")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertRelation(blocked, domain.RelBlockedBy, blocker)
	}))
	closeIssue(t, db, blocked)

	issue := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(blocked) })
	assert.False(t, issue.Blocked)
	assert.Equal(t, []string{}, issue.Blockers)
	assert.Len(t, issue.Relations, 1, "the relation itself is still there")
}

func closeIssue(t *testing.T, db *storage.DB, id string) {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue(id)
		if err != nil {
			return err
		}
		f := storage.Fields(issue)
		f.Status = domain.StatusClosed
		return tx.UpdateIssue(issue, f)
	}))
}

func TestOneParentPerIssue(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	child := add("child")
	parent := add("parent")
	other := add("other")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertRelation(child, domain.RelHasParent, parent)
	}))

	// Re-adding the parent it already has is idempotent.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertRelation(child, domain.RelHasParent, parent)
	}))

	err := db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertRelation(child, domain.RelHasParent, other)
	})
	require.Error(t, err, "the unique index refuses a second parent")
	assert.Equal(t, 4, exitOf(err))

	got := read(t, db, func(tx *storage.Tx) (string, error) {
		found, ok, err := tx.ParentOf(child)
		if err != nil || !ok {
			return "", err
		}
		return found, nil
	})
	assert.Equal(t, parent, got, "the first parent stands")
}

func TestReachesWalksEachGraphSeparately(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	a, b, c := add("a"), add("b"), add("c")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertRelation(a, domain.RelBlockedBy, b); err != nil {
			return err
		}
		return tx.InsertRelation(b, domain.RelBlockedBy, c)
	}))

	reaches := func(rt domain.RelationType, from, to string) bool {
		return read(t, db, func(tx *storage.Tx) (bool, error) { return tx.Reaches(rt, from, to) })
	}

	assert.True(t, reaches(domain.RelBlockedBy, a, c), "transitively")
	assert.False(t, reaches(domain.RelBlockedBy, c, a), "the graph is directed")
	assert.False(t, reaches(domain.RelHasParent, a, c), "a different graph is walked separately")
}

func TestAncestorsAndDescendants(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	gparent, parent, child, gchild := add("gp"), add("p"), add("c"), add("gc")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		for _, e := range [][2]string{{parent, gparent}, {child, parent}, {gchild, child}} {
			if err := tx.InsertRelation(e[0], domain.RelHasParent, e[1]); err != nil {
				return err
			}
		}
		return nil
	}))

	ancestors := read(t, db, func(tx *storage.Tx) (map[string]struct{}, error) {
		return tx.Ancestors(child)
	})
	assert.Equal(t, map[string]struct{}{parent: {}, gparent: {}}, ancestors)

	descendants := read(t, db, func(tx *storage.Tx) (map[string]struct{}, error) {
		return tx.Descendants(parent)
	})
	assert.Equal(t, map[string]struct{}{child: {}, gchild: {}}, descendants)

	subtree := read(t, db, func(tx *storage.Tx) (map[string]struct{}, error) {
		return tx.Subtree(parent)
	})
	assert.Equal(t, map[string]struct{}{parent: {}, child: {}, gchild: {}}, subtree)

	chain := read(t, db, func(tx *storage.Tx) (map[string]struct{}, error) {
		return tx.AncestorChainIncluding(child)
	})
	assert.Equal(t, map[string]struct{}{child: {}, parent: {}, gparent: {}}, chain)
}

func TestTree(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	root := add("root")
	high := add("high", func(i *domain.Issue) { i.Priority = 0 })
	low := add("low", func(i *domain.Issue) { i.Priority = 4 })
	leaf := add("leaf")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		for _, e := range [][2]string{{high, root}, {low, root}, {leaf, high}} {
			if err := tx.InsertRelation(e[0], domain.RelHasParent, e[1]); err != nil {
				return err
			}
		}
		return nil
	}))

	tree := read(t, db, func(tx *storage.Tx) (*domain.IssueTree, error) { return tx.Tree(root) })

	assert.Equal(t, root, tree.ID)
	require.Len(t, tree.Children, 2)
	assert.Equal(t, high, tree.Children[0].ID, "siblings are ordered by priority ascending")
	assert.Equal(t, low, tree.Children[1].ID)

	require.Len(t, tree.Children[0].Children, 1)
	assert.Equal(t, leaf, tree.Children[0].Children[0].ID)
	assert.Equal(t, []domain.IssueTree{}, tree.Children[1].Children, "a leaf carries an empty array")

	// Every node is hydrated, not just the root.
	assert.Equal(t, []string{}, tree.Children[0].Children[0].Labels)
	assert.NotEmpty(t, tree.Children[0].Children[0].CreatedAt)
}

func TestDeleteIssueReportsRelationsRemoved(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	target, a, b := add("target"), add("a"), add("b")

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertRelation(target, domain.RelBlockedBy, a); err != nil {
			return err
		}
		return tx.InsertRelation(b, domain.RelHasParent, target)
	}))

	var removed int
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		var err error
		removed, err = tx.DeleteIssue(target)
		return err
	}))
	assert.Equal(t, 2, removed)

	// The orphaned child survives, its work now top-level.
	orphan := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(b) })
	assert.Equal(t, []domain.Relation{}, orphan.Relations)

	// Removing a blocker silently makes other issues ready.
	unblocked := read(t, db, func(tx *storage.Tx) (*domain.Issue, error) { return tx.GetIssue(a) })
	assert.Equal(t, []domain.Relation{}, unblocked.Relations)
}

func TestResolveIssueRef(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	id := add("t")
	_, hash, _ := domain.SplitID(id)

	resolve := func(s string) (string, error) {
		ref, err := domain.ParseIssueRef(s)
		if err != nil {
			return "", err
		}
		var resolved string
		err = db.Read(t.Context(), func(tx *storage.Tx) error {
			resolved, err = tx.ResolveIssueRef(ref)
			return err
		})
		return resolved, err
	}

	for _, ref := range []string{id, hash, hash[:2], "awb-" + hash[:2], "AWB-" + hash} {
		got, err := resolve(ref)
		require.NoError(t, err, ref)
		assert.Equal(t, id, got, ref)
	}

	_, err := resolve("awb-ffffff")
	require.Error(t, err)
	assert.Equal(t, 3, exitOf(err), "a reference matching nothing is not found")
}

func TestResolveIssueRefAmbiguous(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)

	// Mint issues until two share a first hash character.
	byPrefix := map[string]string{}
	var collide string
	for range 200 {
		id := add("t")
		_, hash, _ := domain.SplitID(id)
		if _, dup := byPrefix[hash[:1]]; dup {
			collide = hash[:1]
			break
		}
		byPrefix[hash[:1]] = id
	}
	require.NotEmpty(t, collide, "expected two hashes to share a first character")

	ref, err := domain.ParseIssueRef(collide)
	require.NoError(t, err)
	err = db.Read(t.Context(), func(tx *storage.Tx) error {
		_, err := tx.ResolveIssueRef(ref)
		return err
	})
	require.Error(t, err)
	assert.Equal(t, 2, exitOf(err), "an ambiguous reference is a usage error, not a guess")
}

func TestSearch(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	titled := add("Parser crashes on empty input")
	described := add("Something else", func(i *domain.Issue) {
		i.Description = "the parser is involved somehow"
	})
	add("Unrelated work")

	search := func(terms ...string) []domain.Issue {
		f := &domain.Filter{Terms: terms, Sort: domain.DefaultSearchSort}
		issues, _, err := listWith(t, db, f)
		require.NoError(t, err)
		return issues
	}

	// Matching is by whole token, case- and diacritic-insensitive.
	results := search("parser")
	require.Len(t, results, 2)
	assert.Equal(t, titled, results[0].ID,
		"a term in a title outranks the same term buried in a description")
	assert.Equal(t, described, results[1].ID)

	assert.Empty(t, search("pars"), "no prefix matching")
	assert.Empty(t, search("parsers"), "no stemming")

	// An issue matches when title and description together contain all terms.
	assert.Len(t, search("parser", "crashes"), 1)
	assert.Empty(t, search("parser", "nonexistent"))
}

func TestListingFilterAppliesBeforeIssuePagingTotalsAndFacets(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	for range 12 {
		add("ordinary work")
	}
	wanted := add("MÜLLER Needle in the final unfiltered page")
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		issue, err := tx.GetIssue(wanted)
		if err != nil {
			return err
		}
		return tx.AddLabel(issue, "frontend")
	}))

	limit, offset := 10, 0
	filter := &domain.Filter{
		ListingFilter: "müller needle FRONT",
		Limit:         &limit,
		Offset:        &offset,
		Sort:          domain.DefaultSort,
	}
	issues, total, err := listWith(t, db, filter)
	require.NoError(t, err)
	require.Len(t, issues, 1, "a match beyond the first unfiltered page remains discoverable")
	assert.Equal(t, wanted, issues[0].ID)
	assert.Equal(t, 1, total, "the total is filtered before paging")

	facets := read(t, db, func(tx *storage.Tx) ([]domain.Facet, error) {
		return tx.LabelFacets(filter)
	})
	assert.Equal(t, []domain.Facet{{Value: "frontend", Count: 1}}, facets)

	filter.ListingFilter = "missing"
	issues, total, err = listWith(t, db, filter)
	require.NoError(t, err)
	assert.Empty(t, issues)
	assert.Zero(t, total)

	filter.ListingFilter = ""
	_, total, err = listWith(t, db, filter)
	require.NoError(t, err)
	assert.Equal(t, 13, total, "clearing restores the complete result set")
}

func TestEpicFilterAppliesBeforePagingAndSelectsNoEpic(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	epic := add("Release epic", func(i *domain.Issue) { i.Type = domain.TypeEpic })
	first := add("first member")
	second := add("second member")
	unrelated := add("unrelated")
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertRelation(first, domain.RelHasParent, epic); err != nil {
			return err
		}
		return tx.InsertRelation(second, domain.RelHasParent, epic)
	}))

	limit, offset := 1, 1
	filter := &domain.Filter{Epic: &epic, Limit: &limit, Offset: &offset, Sort: domain.Sort{Key: domain.SortID}}
	issues, total, err := listWith(t, db, filter)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, 2, total, "epic membership is counted before paging")
	assert.Contains(t, []string{first, second}, issues[0].ID)

	noEpic := ""
	filter = &domain.Filter{Epic: &noEpic, Sort: domain.Sort{Key: domain.SortID}}
	issues, total, err = listWith(t, db, filter)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.ElementsMatch(t, []string{epic, unrelated}, []string{issues[0].ID, issues[1].ID})
}

func TestListingFilterMatchesChildrenByParentID(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	parent := add("parent")
	firstChild := add("first child")
	secondChild := add("second child")
	add("unrelated")
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertRelation(firstChild, domain.RelHasParent, parent); err != nil {
			return err
		}
		return tx.InsertRelation(secondChild, domain.RelHasParent, parent)
	}))

	filter := &domain.Filter{ListingFilter: parent[1:], Sort: domain.DefaultSort}
	issues, total, err := listWith(t, db, filter)
	require.NoError(t, err)
	require.Len(t, issues, 3)

	assert.ElementsMatch(t, []string{parent, firstChild, secondChild},
		[]string{issues[0].ID, issues[1].ID, issues[2].ID})
	assert.Equal(t, 3, total)
}

func TestSuggestIssuesByIDAndTitle(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	prefix := add("Parser crashes")
	contains := add("Repair the parser")
	closed := add("Parser work already shipped")
	closeIssue(t, db, closed)
	add("Unrelated")

	read := func(query string) ([]domain.Issue, int) {
		var issues []domain.Issue
		var total int
		err := db.Read(t.Context(), func(tx *storage.Tx) error {
			var err error
			issues, total, err = tx.SuggestIssues(query, nil)
			return err
		})
		require.NoError(t, err)
		return issues, total
	}

	issues, total := read("parser")
	require.Len(t, issues, 3)
	assert.Equal(t, 3, total)
	assert.ElementsMatch(t, []string{prefix, closed}, []string{issues[0].ID, issues[1].ID},
		"title prefixes sort first, including a closed relation target")
	assert.Equal(t, contains, issues[2].ID, "a contained match sorts after title prefixes")

	issues, total = read(prefix[:len(prefix)-2])
	require.Len(t, issues, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, prefix, issues[0].ID)
}

// No operator, wildcard or column prefix is passed through, so no input can
// produce a query syntax error.
func TestSearchTermsAreLiteral(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	add("a AND b OR c")

	for _, term := range []string{"AND", "OR", "NOT", "*", `"`, "a*", "title:x", "(", "NEAR"} {
		f := &domain.Filter{Terms: []string{term}, Sort: domain.DefaultSearchSort}
		_, _, err := listWith(t, db, f)
		assert.NoError(t, err, "term %q must not reach FTS5 as syntax", term)
	}
}

func listWith(t *testing.T, db *storage.DB, f *domain.Filter) ([]domain.Issue, int, error) {
	t.Helper()
	var (
		issues []domain.Issue
		total  int
	)
	err := db.Read(t.Context(), func(tx *storage.Tx) error {
		var err error
		issues, total, err = tx.ListIssues(f)
		return err
	})
	return issues, total, err
}

func TestListFiltersAndDefaults(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	open1 := add("open one", func(i *domain.Issue) { i.Priority = 1 })
	open2 := add("open two", func(i *domain.Issue) { i.Priority = 3; i.Type = domain.TypeBug })
	done := add("done")
	closeIssue(t, db, done)

	// By default closed issues are hidden.
	issues, total, err := listWith(t, db, &domain.Filter{Sort: domain.DefaultSort})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, issues, 2)
	assert.Equal(t, open1, issues[0].ID, "priority ascending is the default order")
	assert.Equal(t, open2, issues[1].ID)

	// --include-closed widens the set.
	issues, _, err = listWith(t, db, &domain.Filter{IncludeClosed: true, Sort: domain.DefaultSort})
	require.NoError(t, err)
	assert.Len(t, issues, 3)

	// --status closed selects only closed issues.
	issues, _, err = listWith(t, db, &domain.Filter{
		Statuses: []domain.Status{domain.StatusClosed}, Sort: domain.DefaultSort})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, done, issues[0].ID)

	// --type
	issues, _, err = listWith(t, db, &domain.Filter{
		Types: []domain.Type{domain.TypeBug}, Sort: domain.DefaultSort})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, open2, issues[0].ID)

	// --priority-max reads as urgency: 1 means P0 and P1.
	one := 1
	issues, _, err = listWith(t, db, &domain.Filter{PriorityMax: &one, Sort: domain.DefaultSort})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, open1, issues[0].ID)
}

func TestListPagingKeepsTheUnpagedTotal(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	for i := range 5 {
		add("issue", func(is *domain.Issue) { is.Priority = i % 5 })
	}

	limit, offset := 2, 1
	issues, total, err := listWith(t, db,
		&domain.Filter{Limit: &limit, Offset: &offset, Sort: domain.DefaultSort})
	require.NoError(t, err)
	assert.Len(t, issues, 2)
	assert.Equal(t, 5, total)

	zero := 0
	issues, total, err = listWith(t, db, &domain.Filter{Limit: &zero, Sort: domain.DefaultSort})
	require.NoError(t, err)
	assert.Empty(t, issues, "a limit of zero returns no rows")
	assert.Equal(t, 5, total, "while preserving the unpaged total")
}

func TestAssigneeSortingPagesByTheVisibleAssigneeList(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	add("unassigned")
	anna := add("anna", func(i *domain.Issue) {
		i.Status = domain.StatusInProgress
		i.Assignees = []string{"anna"}
	})
	team := add("team", func(i *domain.Issue) {
		i.Status = domain.StatusInProgress
		i.Assignees = []string{"zoe", "mikael"}
	})

	limit := 2
	issues, _, err := listWith(t, db, &domain.Filter{
		Limit: &limit,
		Sort:  domain.Sort{Key: domain.SortAssignee},
	})
	require.NoError(t, err)
	require.Len(t, issues, 2)
	assert.Equal(t, []string{anna, team}, []string{issues[0].ID, issues[1].ID})
	assert.Equal(t, []string{"anna"}, issues[0].Assignees)
	assert.Equal(t, []string{"zoe", "mikael"}, issues[1].Assignees)
}

func TestBlockerSortingPagesByTheVisibleBlockers(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	firstBlocker := add("first blocker")
	lastBlocker := add("last blocker")
	if firstBlocker > lastBlocker {
		firstBlocker, lastBlocker = lastBlocker, firstBlocker
	}
	closed := add("closed subject")
	open := add("open subject")
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertRelation(closed, domain.RelBlockedBy, firstBlocker); err != nil {
			return err
		}
		return tx.InsertRelation(open, domain.RelBlockedBy, lastBlocker)
	}))
	closeIssue(t, db, closed)

	limit := 1
	issues, _, err := listWith(t, db, &domain.Filter{
		IncludeClosed: true,
		Limit:         &limit,
		Sort:          domain.Sort{Key: domain.SortBlockers},
	})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, open, issues[0].ID,
		"a closed subject's historical blocker is not part of its visible sort value")
}

// The assignee sort key joins the assignee list in assignment order, not in
// alphabetical order, so an issue's place in the listing follows the list the
// listing shows. The two issues below are chosen so that joining their
// assignees by value rather than by position would swap them.
func TestAssigneeSortingJoinsInAssignmentOrder(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	inProgress := func(assignees ...string) func(*domain.Issue) {
		return func(i *domain.Issue) {
			i.Status = domain.StatusInProgress
			i.Assignees = assignees
		}
	}
	// "b z" sorts before "c a"; joined by value instead, "a c" would sort before
	// "b z" and the two would come back the other way round.
	bz := add("bz", inProgress("b", "z"))
	ca := add("ca", inProgress("c", "a"))

	issues, _, err := listWith(t, db, &domain.Filter{Sort: domain.Sort{Key: domain.SortAssignee}})
	require.NoError(t, err)
	require.Len(t, issues, 2)
	assert.Equal(t, []string{bz, ca}, []string{issues[0].ID, issues[1].ID})
	assert.Equal(t, []string{"b", "z"}, issues[0].Assignees, "and the visible list is the joined one")
}

// The blocker sort key joins the blocker ids ascending, whatever order the
// relations were written in, so it agrees with the ascending list the listing
// shows.
func TestBlockerSortingJoinsAscending(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	blockers := []string{add("blocker one"), add("blocker two"), add("blocker three")}
	slices.Sort(blockers)
	low, middle, high := blockers[0], blockers[1], blockers[2]

	two := add("blocked by two")
	one := add("blocked by one")
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		// Written high first, so a join that followed insertion order would put
		// the high id at the front.
		if err := tx.InsertRelation(two, domain.RelBlockedBy, high); err != nil {
			return err
		}
		if err := tx.InsertRelation(two, domain.RelBlockedBy, low); err != nil {
			return err
		}
		return tx.InsertRelation(one, domain.RelBlockedBy, middle)
	}))

	issues, _, err := listWith(t, db, &domain.Filter{Sort: domain.Sort{Key: domain.SortBlockers}})
	require.NoError(t, err)
	require.Len(t, issues, 5)
	// Joined ascending, the two-blocker issue's key starts at the low id and it
	// comes first; joined in insertion order it would start at the high id and
	// come second.
	assert.Equal(t, []string{two, one}, []string{issues[0].ID, issues[1].ID})
	assert.Equal(t, []string{low, high}, issues[0].Blockers, "and the visible list is the joined one")
}

// Each of the three workspace orderings, in both directions. The descending form
// reverses the named key only: after a derived key the p.key tiebreak stays
// ascending, exactly as the issue listings' id tiebreak does.
func TestWorkspaceListOrderings(t *testing.T) {
	db := newDB(t)
	// Keys, active counts and update times are each deliberately in a different
	// order, so no two orderings agree by accident.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		for _, key := range []string{"beta", "alpha", "gamma"} {
			if err := tx.InsertWorkspace(key, key, ""); err != nil {
				return err
			}
		}
		return nil
	}))
	// gamma two open issues, alpha one, beta none.
	for _, key := range []string{"gamma", "gamma", "alpha"} {
		require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
			return tx.InsertIssue(&domain.Issue{
				Workspace: key, Title: "t", Type: domain.DefaultType,
				Status: domain.DefaultStatus, Priority: domain.DefaultPriority,
			})
		}))
	}
	// Updated once, twice and three times, so updated_at runs beta, gamma, alpha
	// whether or not the writes land in one millisecond: an update that does not
	// advance the clock still forces the row's timestamp a millisecond upward.
	updates := map[string]int{"beta": 1, "gamma": 2, "alpha": 3}
	for _, key := range []string{"beta", "gamma", "alpha"} {
		for i := range updates[key] {
			require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
				workspace, err := tx.GetWorkspace(key)
				if err != nil {
					return err
				}
				// A rename that changes nothing is a no-op, so each one differs.
				return tx.UpdateWorkspace(workspace, fmt.Sprintf("%s %d", key, i), "")
			}))
		}
	}

	keys := func(sort domain.WorkspaceSort) []string {
		t.Helper()
		workspaces := read(t, db, func(tx *storage.Tx) ([]domain.Workspace, error) {
			workspaces, _, err := tx.ListWorkspaces("", sort, nil, nil)
			return workspaces, err
		})
		found := make([]string, len(workspaces))
		for i, workspace := range workspaces {
			found[i] = workspace.Key
		}
		return found
	}

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, keys(domain.DefaultWorkspaceSort),
		"the default, which an absent key also gives")
	assert.Equal(t, []string{"alpha", "beta", "gamma"},
		keys(domain.WorkspaceSort{Key: domain.WorkspaceSortByKey}))
	assert.Equal(t, []string{"gamma", "beta", "alpha"},
		keys(domain.WorkspaceSort{Key: domain.WorkspaceSortByKey, Desc: true}),
		"-key is descending, not the ascending default")

	assert.Equal(t, []string{"beta", "alpha", "gamma"},
		keys(domain.WorkspaceSort{Key: domain.WorkspaceSortActive}), "0, 1, 2 open issues")
	assert.Equal(t, []string{"gamma", "alpha", "beta"},
		keys(domain.WorkspaceSort{Key: domain.WorkspaceSortActive, Desc: true}))

	assert.Equal(t, []string{"beta", "gamma", "alpha"},
		keys(domain.WorkspaceSort{Key: domain.WorkspaceSortUpdated}), "least recently touched first")
	assert.Equal(t, []string{"alpha", "gamma", "beta"},
		keys(domain.WorkspaceSort{Key: domain.WorkspaceSortUpdated, Desc: true}))
}

// status and type order by what their values mean, not by how they are spelled.
// Sorting the stored text would put closed first and bug before epic.
func TestStatusAndTypeSortByTheVocabulary(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)

	// Created in an order that is neither the vocabulary's nor the alphabet's,
	// so passing means the ordering did the work.
	for _, issueType := range []domain.Type{
		domain.TypeChore, domain.TypeBug, domain.TypeEpic,
		domain.TypeTask, domain.TypeFeature,
	} {
		add(string(issueType), func(i *domain.Issue) { i.Type = issueType })
	}
	types := func(sort domain.Sort) []domain.Type {
		t.Helper()
		issues, _, err := listWith(t, db, &domain.Filter{IncludeClosed: true, Sort: sort})
		require.NoError(t, err)
		found := make([]domain.Type, len(issues))
		for i := range issues {
			found[i] = issues[i].Type
		}
		return found
	}
	assert.Equal(t, domain.Types, types(domain.Sort{Key: domain.SortType}),
		"epic, feature, bug, task, chore — not the alphabet's bug, chore, epic, feature, task")
	reversed := slices.Clone(domain.Types)
	slices.Reverse(reversed)
	assert.Equal(t, reversed, types(domain.Sort{Key: domain.SortType, Desc: true}))

	// One issue per status, on a second workspace so the type set above is not in
	// the way. Every status is reachable only through its own transition.
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertWorkspace("st", "statuses", "")
	}))
	statusIssue := func(title string, mutate func(*domain.Issue)) {
		t.Helper()
		issue := &domain.Issue{
			Workspace: "st", Title: title, Type: domain.DefaultType,
			Status: domain.DefaultStatus, Priority: domain.DefaultPriority,
		}
		mutate(issue)
		require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
			return tx.InsertIssue(issue)
		}))
		if issue.Status == domain.StatusClosed {
			closeIssue(t, db, issue.ID)
		}
	}
	statusIssue("closed", func(i *domain.Issue) { i.Status = domain.StatusClosed })
	statusIssue("open", func(*domain.Issue) {})
	statusIssue("in progress", func(i *domain.Issue) {
		i.Status = domain.StatusInProgress
		i.Assignees = []string{"mikael"}
	})

	statuses := func(sort domain.Sort) []domain.Status {
		t.Helper()
		issues, _, err := listWith(t, db, &domain.Filter{
			IncludeClosed: true, Workspaces: []string{"st"}, Sort: sort})
		require.NoError(t, err)
		found := make([]domain.Status, len(issues))
		for i := range issues {
			found[i] = issues[i].Status
		}
		return found
	}
	assert.Equal(t, domain.Statuses, statuses(domain.Sort{Key: domain.SortStatus}),
		"open, in_progress, closed — not the alphabet's closed, in_progress, open")
	reversedStatuses := slices.Clone(domain.Statuses)
	slices.Reverse(reversedStatuses)
	assert.Equal(t, reversedStatuses, statuses(domain.Sort{Key: domain.SortStatus, Desc: true}))
}

// Two invocations against unchanged data must agree, so every order is total.
func TestListOrderIsTotal(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	for range 20 {
		add("same title")
	}

	for _, sort := range []domain.Sort{
		{Key: domain.SortPriority}, {Key: domain.SortPriority, Desc: true},
		{Key: domain.SortCreated}, {Key: domain.SortUpdated}, {Key: domain.SortID},
		{Key: domain.SortID, Desc: true}, {Key: domain.SortWorkspace},
		{Key: domain.SortStatus}, {Key: domain.SortAssignee}, {Key: domain.SortAssignee, Desc: true},
		{Key: domain.SortType}, {Key: domain.SortBlockers},
	} {
		first, _, err := listWith(t, db, &domain.Filter{Sort: sort})
		require.NoError(t, err)
		// The contract is equality across two invocations; the cases above,
		// rather than repeated sampling, provide the sort coverage.
		again, _, err := listWith(t, db, &domain.Filter{Sort: sort})
		require.NoError(t, err)
		assert.Equal(t, first, again, "sort %v", sort)
	}
}

func TestFacets(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)

	label := func(id string, labels ...string) {
		require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
			issue, err := tx.GetIssue(id)
			if err != nil {
				return err
			}
			for _, l := range labels {
				if err := tx.AddLabel(issue, l); err != nil {
					return err
				}
			}
			return nil
		}))
	}

	a := add("Parser failure")
	b := add("Unrelated work")
	closed := add("c")
	label(a, "parser", "frontend")
	label(b, "parser")
	label(closed, "archived")
	closeIssue(t, db, closed)

	facets := read(t, db, func(tx *storage.Tx) ([]domain.Facet, error) {
		return tx.LabelFacets(&domain.Filter{})
	})
	assert.Equal(t, []domain.Facet{
		{Value: "frontend", Count: 1},
		{Value: "parser", Count: 2},
	}, facets, "sorted by value; a closed issue's label is not in use by default")

	// The facet's own filter applies too, so a UI can narrow progressively.
	narrowed := read(t, db, func(tx *storage.Tx) ([]domain.Facet, error) {
		return tx.LabelFacets(&domain.Filter{Labels: []string{"frontend"}})
	})
	assert.Equal(t, []domain.Facet{
		{Value: "frontend", Count: 1},
		{Value: "parser", Count: 1},
	}, narrowed)

	searched := read(t, db, func(tx *storage.Tx) ([]domain.Facet, error) {
		return tx.LabelFacets(&domain.Filter{Terms: []string{"Parser"}})
	})
	assert.Equal(t, []domain.Facet{
		{Value: "frontend", Count: 1},
		{Value: "parser", Count: 1},
	}, searched)
}

func TestAssigneeFacetsHaveNoEmptyRow(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	assigned := add("a", func(i *domain.Issue) {
		i.Assignees = []string{"claude-1", "claude-2"}
		i.Status = domain.StatusInProgress
	})
	add("b")

	facets := read(t, db, func(tx *storage.Tx) ([]domain.Facet, error) {
		return tx.AssigneeFacets(&domain.Filter{})
	})
	assert.Equal(t, []domain.Facet{
		{Value: "claude-1", Count: 1},
		{Value: "claude-2", Count: 1},
	}, facets)

	issues := read(t, db, func(tx *storage.Tx) ([]domain.Issue, error) {
		rows, _, err := tx.ListIssues(&domain.Filter{Assignees: []string{"claude-2"}})
		return rows, err
	})
	require.Len(t, issues, 1)
	assert.Equal(t, assigned, issues[0].ID)
	assert.NotEmpty(t, assigned)
}

func TestWorkspaces(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	add("live")
	done := add("done")
	closeIssue(t, db, done)

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertWorkspace("web", "web", "")
	}))

	workspaces, total, err := func() ([]domain.Workspace, int, error) {
		var (
			ps    []domain.Workspace
			total int
		)
		err := db.Read(t.Context(), func(tx *storage.Tx) error {
			var err error
			ps, total, err = tx.ListWorkspaces("", domain.DefaultWorkspaceSort, nil, nil)
			return err
		})
		return ps, total, err
	}()
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, workspaces, 2)
	assert.Equal(t, "awb", workspaces[0].Key, "ordered by key ascending")
	assert.Equal(t, "web", workspaces[1].Key)
	assert.Equal(t, 1, workspaces[0].ActiveIssues, "closed issues are not active")

	workspaces, _, err = func() ([]domain.Workspace, int, error) {
		var (
			ps    []domain.Workspace
			total int
		)
		err := db.Read(t.Context(), func(tx *storage.Tx) error {
			var err error
			ps, total, err = tx.ListWorkspaces("",
				domain.WorkspaceSort{Key: domain.WorkspaceSortActive, Desc: true}, nil, nil)
			return err
		})
		return ps, total, err
	}()
	require.NoError(t, err)
	assert.Equal(t, []string{"awb", "web"}, []string{workspaces[0].Key, workspaces[1].Key},
		"active count sorting happens before paging")

	// A duplicate key is a conflict.
	err = db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertWorkspace("awb", "again", "")
	})
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
}

func TestWorkspaceListingFilterAppliesBeforePagingAndCounting(t *testing.T) {
	db := newDB(t)
	seed(t, db)
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		if err := tx.InsertWorkspace("cli", "Command tools", "remote clients"); err != nil {
			return err
		}
		return tx.InsertWorkspace("web", "Web console", "MÜLLER Agent issue tracking")
	}))

	limit, offset := 1, 0
	var workspaces []domain.Workspace
	var total int
	require.NoError(t, db.Read(t.Context(), func(tx *storage.Tx) error {
		var err error
		workspaces, total, err = tx.ListWorkspaces("müller agent TRACK", domain.DefaultWorkspaceSort, &limit, &offset)
		return err
	}))
	require.Len(t, workspaces, 1)
	assert.Equal(t, "web", workspaces[0].Key)
	assert.Equal(t, 1, total)
}

// A workspace's updated_at does not move when an issue it holds changes:
// active_issues is derived, not stored.
func TestWorkspaceUpdatedAtIgnoresIssueChurn(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)

	before := read(t, db, func(tx *storage.Tx) (*domain.Workspace, error) { return tx.GetWorkspace("awb") })
	id := add("new issue")
	closeIssue(t, db, id)

	after := read(t, db, func(tx *storage.Tx) (*domain.Workspace, error) { return tx.GetWorkspace("awb") })
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt)
	assert.Equal(t, 0, after.ActiveIssues)
}

func TestWorkspaceDeletionRefusesWhileItHoldsAnyIssue(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	id := add("even a closed one counts")
	closeIssue(t, db, id)

	count := read(t, db, func(tx *storage.Tx) (int, error) { return tx.CountIssuesInWorkspace("awb") })
	assert.Equal(t, 1, count, "the count is wider than the active one workspace list shows")

	var removed int
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		var err error
		if removed, err = tx.DeleteWorkspaceIssues("awb"); err != nil {
			return err
		}
		return tx.DeleteWorkspace("awb")
	}))
	assert.Equal(t, 1, removed)

	err := db.Read(t.Context(), func(tx *storage.Tx) error {
		_, err := tx.GetWorkspace("awb")
		return err
	})
	assert.Error(t, err)
}

// exitOf reports the exit code a failure carries, which is the
// machine-readable half of the taxonomy both surfaces share.
func exitOf(err error) int { return awberr.ExitCode(err) }
