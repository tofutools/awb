package storage_test

import (
	"path/filepath"
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

// seed creates a project and returns a helper that adds issues to it.
func seed(t *testing.T, db *storage.DB) func(title string, mutate ...func(*domain.Issue)) string {
	t.Helper()
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertProject("awb", "Agent Work Board", "")
	}))

	return func(title string, mutate ...func(*domain.Issue)) string {
		t.Helper()
		issue := &domain.Issue{
			Project: "awb", Title: title, Type: domain.DefaultType,
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

	assert.Equal(t, "awb", issue.Project)
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

	// The ID is <project-key>-<hash>.
	project, hash, ok := domain.SplitID(issue.ID)
	require.True(t, ok)
	assert.Equal(t, "awb", project)
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
		{Key: domain.SortID, Desc: true}, {Key: domain.SortProject},
		{Key: domain.SortStatus}, {Key: domain.SortAssignee}, {Key: domain.SortAssignee, Desc: true},
		{Key: domain.SortType}, {Key: domain.SortBlockers},
	} {
		first, _, err := listWith(t, db, &domain.Filter{Sort: sort})
		require.NoError(t, err)
		for range 3 {
			again, _, err := listWith(t, db, &domain.Filter{Sort: sort})
			require.NoError(t, err)
			assert.Equal(t, first, again, "sort %v", sort)
		}
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

func TestProjects(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	add("live")
	done := add("done")
	closeIssue(t, db, done)

	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertProject("web", "web", "")
	}))

	projects, total, err := func() ([]domain.Project, int, error) {
		var (
			ps    []domain.Project
			total int
		)
		err := db.Read(t.Context(), func(tx *storage.Tx) error {
			var err error
			ps, total, err = tx.ListProjects(domain.DefaultProjectSort, nil, nil)
			return err
		})
		return ps, total, err
	}()
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, projects, 2)
	assert.Equal(t, "awb", projects[0].Key, "ordered by key ascending")
	assert.Equal(t, "web", projects[1].Key)
	assert.Equal(t, 1, projects[0].ActiveIssues, "closed issues are not active")

	projects, _, err = func() ([]domain.Project, int, error) {
		var (
			ps    []domain.Project
			total int
		)
		err := db.Read(t.Context(), func(tx *storage.Tx) error {
			var err error
			ps, total, err = tx.ListProjects(
				domain.ProjectSort{Key: domain.ProjectSortActive, Desc: true}, nil, nil)
			return err
		})
		return ps, total, err
	}()
	require.NoError(t, err)
	assert.Equal(t, []string{"awb", "web"}, []string{projects[0].Key, projects[1].Key},
		"active count sorting happens before paging")

	// A duplicate key is a conflict.
	err = db.Write(t.Context(), func(tx *storage.Tx) error {
		return tx.InsertProject("awb", "again", "")
	})
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
}

// A project's updated_at does not move when an issue it holds changes:
// active_issues is derived, not stored.
func TestProjectUpdatedAtIgnoresIssueChurn(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)

	before := read(t, db, func(tx *storage.Tx) (*domain.Project, error) { return tx.GetProject("awb") })
	id := add("new issue")
	closeIssue(t, db, id)

	after := read(t, db, func(tx *storage.Tx) (*domain.Project, error) { return tx.GetProject("awb") })
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt)
	assert.Equal(t, 0, after.ActiveIssues)
}

func TestProjectDeletionRefusesWhileItHoldsAnyIssue(t *testing.T) {
	db := newDB(t)
	add := seed(t, db)
	id := add("even a closed one counts")
	closeIssue(t, db, id)

	count := read(t, db, func(tx *storage.Tx) (int, error) { return tx.CountIssuesInProject("awb") })
	assert.Equal(t, 1, count, "the count is wider than the active one project list shows")

	var removed int
	require.NoError(t, db.Write(t.Context(), func(tx *storage.Tx) error {
		var err error
		if removed, err = tx.DeleteProjectIssues("awb"); err != nil {
			return err
		}
		return tx.DeleteProject("awb")
	}))
	assert.Equal(t, 1, removed)

	err := db.Read(t.Context(), func(tx *storage.Tx) error {
		_, err := tx.GetProject("awb")
		return err
	})
	assert.Error(t, err)
}

// exitOf reports the exit code a failure carries, which is the
// machine-readable half of the taxonomy both surfaces share.
func exitOf(err error) int { return awberr.ExitCode(err) }
