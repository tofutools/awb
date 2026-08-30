package local_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/storage"
)

func newBackend(t *testing.T) (*local.Backend, context.Context) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	b := local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	_, err = b.CreateProject(t.Context(), backend.ProjectCreate{Key: "awb"})
	require.NoError(t, err)
	return b, t.Context()
}

func create(t *testing.T, b *local.Backend, ctx context.Context, title string,
	mutate ...func(*backend.IssueCreate)) *domain.Issue {
	t.Helper()
	req := backend.IssueCreate{Project: "awb", Title: title}
	for _, m := range mutate {
		m(&req)
	}
	issue, err := b.CreateIssue(ctx, req)
	require.NoError(t, err)
	return issue
}

func exitOf(err error) int { return awberr.ExitCode(err) }

func TestCreateIssueDefaults(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "  Parser crashes  ")

	assert.Equal(t, "Parser crashes", issue.Title, "the title is trimmed")
	assert.Equal(t, domain.TypeTask, issue.Type)
	assert.Equal(t, domain.StatusOpen, issue.Status)
	assert.Equal(t, 2, issue.Priority)
	assert.Empty(t, issue.Assignee)
	assert.True(t, issue.Ready())
}

// Creating with an assignee is an atomic create-and-claim, so a new issue is
// never open and assigned at once.
func TestCreateWithAssigneeIsAClaim(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignee = "claude-1" })

	assert.Equal(t, domain.StatusInProgress, issue.Status)
	assert.Equal(t, "claude-1", issue.Assignee)
	assert.False(t, issue.Ready())
}

func TestCreateWithLabelsAndRelationsInOneTransaction(t *testing.T) {
	b, ctx := newBackend(t)
	origin := create(t, b, ctx, "origin")

	issue := create(t, b, ctx, "derived", func(r *backend.IssueCreate) {
		r.Labels = []string{"parser", "a"}
		r.Relations = []backend.NewRelation{
			{Type: domain.RelDiscoveredFrom, Other: origin.ID},
			{Type: domain.RelBlockedBy, Other: origin.ID},
		}
	})

	assert.Equal(t, []string{"a", "parser"}, issue.Labels, "labels come back sorted")
	assert.Equal(t, []domain.Relation{
		{Type: domain.RelBlockedBy, Other: origin.ID, Direction: domain.DirectionOut},
		{Type: domain.RelDiscoveredFrom, Other: origin.ID, Direction: domain.DirectionOut},
	}, issue.Relations, "relations come back sorted by type, then other, then direction")
	assert.True(t, issue.Blocked)
}

func TestCreateRejectsUnknownProject(t *testing.T) {
	b, ctx := newBackend(t)
	_, err := b.CreateIssue(ctx, backend.IssueCreate{Project: "nosuch", Title: "t"})
	require.Error(t, err)
	assert.Equal(t, 3, exitOf(err))
}

func TestCreateRejectsTwoParents(t *testing.T) {
	b, ctx := newBackend(t)
	a := create(t, b, ctx, "a")
	c := create(t, b, ctx, "c")

	_, err := b.CreateIssue(ctx, backend.IssueCreate{
		Project: "awb", Title: "t",
		Relations: []backend.NewRelation{
			{Type: domain.RelHasParent, Other: a.ID},
			{Type: domain.RelHasParent, Other: c.ID},
		},
	})
	require.Error(t, err)
	assert.Equal(t, 2, exitOf(err))
}

// update cannot change status or assignee; the transitions are the only way.
func TestUpdate(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")

	title, priority := "new title", 0
	updated, err := b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title, Priority: &priority}, "")
	require.NoError(t, err)
	assert.Equal(t, "new title", updated.Title)
	assert.Equal(t, 0, updated.Priority)
	assert.Greater(t, updated.UpdatedAt, issue.UpdatedAt)

	// Giving no field at all succeeds and changes nothing.
	same, err := b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{}, "")
	require.NoError(t, err)
	assert.Equal(t, updated.UpdatedAt, same.UpdatedAt)

	// An empty description clears it.
	empty := ""
	cleared, err := b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Description: &empty}, "")
	require.NoError(t, err)
	assert.Empty(t, cleared.Description)
}

func TestClaim(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")

	claimed, err := b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-1"}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, claimed.Status)
	assert.Equal(t, "claude-1", claimed.Assignee)

	// Claiming an issue you already hold succeeds and changes nothing.
	again, err := b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-1"}, "")
	require.NoError(t, err)
	assert.Equal(t, claimed.UpdatedAt, again.UpdatedAt)

	// Somebody else's claim is refused.
	_, err = b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-2"}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))

	// --force takes it.
	forced, err := b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-2", Force: true}, "")
	require.NoError(t, err)
	assert.Equal(t, "claude-2", forced.Assignee)
}

// The compare-and-set: two agents racing for the same issue cannot both win.
func TestClaimCompareAndSet(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")

	unassigned := ""
	first, err := b.Claim(ctx, issue.ID,
		backend.ClaimRequest{Assignee: "claude-1", ExpectAssignee: &unassigned}, "")
	require.NoError(t, err)
	assert.Equal(t, "claude-1", first.Assignee)

	// The second agent expected it to still be unassigned, and it is not.
	_, err = b.Claim(ctx, issue.ID,
		backend.ClaimRequest{Assignee: "claude-2", ExpectAssignee: &unassigned}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
}

func TestClaimRefusesBlockedAndClosed(t *testing.T) {
	b, ctx := newBackend(t)
	blocker := create(t, b, ctx, "blocker")
	blocked := create(t, b, ctx, "blocked", func(r *backend.IssueCreate) {
		r.Relations = []backend.NewRelation{{Type: domain.RelBlockedBy, Other: blocker.ID}}
	})

	_, err := b.Claim(ctx, blocked.ID, backend.ClaimRequest{Assignee: "claude-1"}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))

	forced, err := b.Claim(ctx, blocked.ID, backend.ClaimRequest{Assignee: "claude-1", Force: true}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, forced.Status)

	// A forced claim on a closed issue leaves its historical close-reason
	// comment in the activity stream.
	done := create(t, b, ctx, "done")
	reason := "fixed"
	_, err = b.CloseIssue(ctx, done.ID, backend.CloseRequest{Reason: &reason}, "")
	require.NoError(t, err)

	_, err = b.Claim(ctx, done.ID, backend.ClaimRequest{Assignee: "claude-1"}, "")
	require.Error(t, err)

	reclaimed, err := b.Claim(ctx, done.ID, backend.ClaimRequest{Assignee: "claude-1", Force: true}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, reclaimed.Status)
	activity, err := b.ListActivity(ctx, done.ID, domain.ActivityKindComment, nil, nil)
	require.NoError(t, err)
	require.Len(t, activity.Activity, 1)
	assert.Equal(t, "closed", activity.Activity[0].Action)
	assert.Equal(t, reason, activity.Activity[0].Body)
}

func TestRelease(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignee = "mikael" })

	released, err := b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "mikael"}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusOpen, released.Status)
	assert.Empty(t, released.Assignee)

	// Releasing one that is already open and unassigned changes nothing.
	again, err := b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "mikael"}, "")
	require.NoError(t, err)
	assert.Equal(t, released.UpdatedAt, again.UpdatedAt)

	// Somebody else's is refused without force.
	_, err = b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-1"}, "")
	require.NoError(t, err)
	_, err = b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "mikael"}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))

	forced, err := b.Release(ctx, issue.ID, backend.ReleaseRequest{Force: true}, "")
	require.NoError(t, err)
	assert.Empty(t, forced.Assignee)
}

func TestCloseAndReopen(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignee = "mikael" })

	reason := "Guard against empty token stream"
	closed, err := b.CloseIssue(ctx, issue.ID, backend.CloseRequest{Reason: &reason}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusClosed, closed.Status)
	assert.Equal(t, "mikael", closed.Assignee, "the assignee records who did the work")

	activity, err := b.ListActivity(ctx, issue.ID, "", nil, nil)
	require.NoError(t, err)
	require.Len(t, activity.Activity, 2, "creation and closing are both recorded")
	closeReason := activity.Activity[0]
	assert.Equal(t, domain.ActivityKindComment, closeReason.Kind)
	assert.Equal(t, "closed", closeReason.Action)
	assert.Equal(t, reason, closeReason.Body)
	require.Len(t, closeReason.Changes, 1)
	assert.Equal(t, "status", closeReason.Changes[0].Field)

	// Closing a closed issue is a no-op, so a reason can never become detached
	// from a real close transition.
	again, err := b.CloseIssue(ctx, issue.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)
	assert.Equal(t, closed.UpdatedAt, again.UpdatedAt)

	detached := "not attached"
	unchanged, err := b.CloseIssue(ctx, issue.ID, backend.CloseRequest{Reason: &detached}, "")
	require.NoError(t, err)
	assert.Equal(t, closed.UpdatedAt, unchanged.UpdatedAt)
	activity, err = b.ListActivity(ctx, issue.ID, "", nil, nil)
	require.NoError(t, err)
	assert.Len(t, activity.Activity, 2)

	// Reopening returns it to the pool while retaining the close reason in the
	// immutable history.
	reopened, err := b.Reopen(ctx, issue.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusOpen, reopened.Status)
	assert.Empty(t, reopened.Assignee)
	assert.True(t, reopened.Ready())
	comments, err := b.ListActivity(ctx, issue.ID, domain.ActivityKindComment, nil, nil)
	require.NoError(t, err)
	require.Len(t, comments.Activity, 1)
	assert.Equal(t, reason, comments.Activity[0].Body)
}

// Reopen acts only on a closed issue, so it can never take a claim away from
// somebody who is working.
func TestReopenLeavesLiveWorkAlone(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignee = "claude-1" })

	unchanged, err := b.Reopen(ctx, issue.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, unchanged.Status)
	assert.Equal(t, "claude-1", unchanged.Assignee)
	assert.Equal(t, issue.UpdatedAt, unchanged.UpdatedAt)
}

func TestCyclesAreRefused(t *testing.T) {
	b, ctx := newBackend(t)
	a := create(t, b, ctx, "a")
	c := create(t, b, ctx, "c")
	d := create(t, b, ctx, "d")

	for _, relType := range []domain.RelationType{
		domain.RelBlockedBy, domain.RelHasParent, domain.RelDiscoveredFrom,
	} {
		t.Run(string(relType), func(t *testing.T) {
			b, ctx := newBackend(t)
			x := create(t, b, ctx, "x")
			y := create(t, b, ctx, "y")
			z := create(t, b, ctx, "z")

			_, err := b.AddRelation(ctx, x.ID, backend.RelationRequest{Type: relType, Other: y.ID}, "")
			require.NoError(t, err)
			_, err = b.AddRelation(ctx, y.ID, backend.RelationRequest{Type: relType, Other: z.ID}, "")
			require.NoError(t, err)

			// Closing the loop is refused.
			_, err = b.AddRelation(ctx, z.ID, backend.RelationRequest{Type: relType, Other: x.ID}, "")
			require.Error(t, err)
			assert.Equal(t, 4, exitOf(err))

			// A relation to itself is refused too.
			_, err = b.AddRelation(ctx, x.ID, backend.RelationRequest{Type: relType, Other: x.ID}, "")
			require.Error(t, err)
			assert.Equal(t, 4, exitOf(err))
		})
	}

	// related has no direction to run in a circle, so it is unconstrained.
	_, err := b.AddRelation(ctx, a.ID, backend.RelationRequest{Type: domain.RelRelated, Other: c.ID}, "")
	require.NoError(t, err)
	_, err = b.AddRelation(ctx, c.ID, backend.RelationRequest{Type: domain.RelRelated, Other: d.ID}, "")
	require.NoError(t, err)
	_, err = b.AddRelation(ctx, d.ID, backend.RelationRequest{Type: domain.RelRelated, Other: a.ID}, "")
	assert.NoError(t, err)
}

// A symmetric related pair is one stored edge, so adding it from either end is
// idempotent and removal works in either order.
func TestRelatedIsSymmetric(t *testing.T) {
	b, ctx := newBackend(t)
	a := create(t, b, ctx, "a")
	c := create(t, b, ctx, "c")

	_, err := b.AddRelation(ctx, a.ID, backend.RelationRequest{Type: domain.RelRelated, Other: c.ID}, "")
	require.NoError(t, err)
	_, err = b.AddRelation(ctx, c.ID, backend.RelationRequest{Type: domain.RelRelated, Other: a.ID}, "")
	require.NoError(t, err)

	issue, err := b.GetIssue(ctx, a.ID)
	require.NoError(t, err)
	assert.Len(t, issue.Relations, 1, "adding from either end is the same edge")
	assert.Equal(t, domain.DirectionOut, issue.Relations[0].Direction)

	other, err := b.GetIssue(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.DirectionOut, other.Relations[0].Direction)

	// Removal works in either order.
	_, err = b.RemoveRelation(ctx, c.ID, domain.RelRelated, a.ID, "")
	require.NoError(t, err)
	issue, err = b.GetIssue(ctx, a.ID)
	require.NoError(t, err)
	assert.Empty(t, issue.Relations)
}

func TestParentReplacement(t *testing.T) {
	b, ctx := newBackend(t)
	child := create(t, b, ctx, "child")
	first := create(t, b, ctx, "first")
	second := create(t, b, ctx, "second")

	_, err := b.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: first.ID}, "")
	require.NoError(t, err)

	// Naming the parent it already has is the ordinary idempotent re-add.
	_, err = b.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: first.ID}, "")
	require.NoError(t, err)

	// A different one is refused without force.
	_, err = b.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: second.ID}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))

	// Force replaces it.
	_, err = b.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: second.ID, Force: true}, "")
	require.NoError(t, err)

	issue, err := b.GetIssue(ctx, child.ID)
	require.NoError(t, err)
	require.Len(t, issue.Relations, 1)
	assert.Equal(t, second.ID, issue.Relations[0].Other)
}

// An issue may not be blocked-by any ancestor or descendant in the has-parent
// graph: that inverts decomposition.
func TestBlockedByCannotInvertDecomposition(t *testing.T) {
	b, ctx := newBackend(t)
	gparent := create(t, b, ctx, "gparent")
	parent := create(t, b, ctx, "parent")
	child := create(t, b, ctx, "child")

	for _, e := range [][2]string{{parent.ID, gparent.ID}, {child.ID, parent.ID}} {
		_, err := b.AddRelation(ctx, e[0],
			backend.RelationRequest{Type: domain.RelHasParent, Other: e[1]}, "")
		require.NoError(t, err)
	}

	// A child waiting for its own parent, or for any ancestor.
	for _, other := range []string{parent.ID, gparent.ID} {
		_, err := b.AddRelation(ctx, child.ID,
			backend.RelationRequest{Type: domain.RelBlockedBy, Other: other}, "")
		require.Error(t, err, other)
		assert.Equal(t, 4, exitOf(err))
	}

	// And a parent waiting for its own descendant.
	_, err := b.AddRelation(ctx, gparent.ID,
		backend.RelationRequest{Type: domain.RelBlockedBy, Other: child.ID}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
}

// The asymmetric half of the rule: moving a subtree under a new chain of
// ancestors is refused when some *existing* blocked-by edge would then run
// between an ancestor and a descendant of one decomposition — even though
// neither endpoint of that edge is an endpoint of the has-parent edge being
// added.
func TestParentEdgeRefusedByAnExistingBlockedByEdge(t *testing.T) {
	b, ctx := newBackend(t)
	blocker := create(t, b, ctx, "blocker")
	newParent := create(t, b, ctx, "new parent")
	child := create(t, b, ctx, "child")
	grandchild := create(t, b, ctx, "grandchild")

	// blocker is above newParent, so it will become an ancestor of anything moved
	// under newParent.
	_, err := b.AddRelation(ctx, newParent.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: blocker.ID}, "")
	require.NoError(t, err)

	// grandchild sits inside the subtree about to move...
	_, err = b.AddRelation(ctx, grandchild.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: child.ID}, "")
	require.NoError(t, err)

	// ...and is legitimately blocked-by blocker today, the two being unrelated in
	// the decomposition as it currently stands.
	_, err = b.AddRelation(ctx, grandchild.ID,
		backend.RelationRequest{Type: domain.RelBlockedBy, Other: blocker.ID}, "")
	require.NoError(t, err)

	// Moving child under newParent would put that existing edge between an
	// ancestor and a descendant of one decomposition, so it is refused.
	_, err = b.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: newParent.ID}, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))

	// The refusal rolled back cleanly: child still has no parent of its own. Its
	// incoming has-parent edge, from grandchild, is a different thing and stays.
	issue, err := b.GetIssue(ctx, child.ID)
	require.NoError(t, err)
	assert.Contains(t, issue.Relations,
		domain.Relation{Type: domain.RelHasParent, Other: grandchild.ID, Direction: domain.DirectionIn})
	for _, rel := range issue.Relations {
		if rel.Type == domain.RelHasParent {
			assert.Equal(t, domain.DirectionIn, rel.Direction,
				"a refused parent edge must not be left behind")
		}
	}

	// A move that does not create such a pairing still works.
	unrelated := create(t, b, ctx, "unrelated")
	_, err = b.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: unrelated.ID}, "")
	assert.NoError(t, err)
}

func TestReadyAndBlockedListings(t *testing.T) {
	b, ctx := newBackend(t)
	blocker := create(t, b, ctx, "blocker", func(r *backend.IssueCreate) { p := 1; r.Priority = &p })
	blocked := create(t, b, ctx, "blocked", func(r *backend.IssueCreate) {
		r.Relations = []backend.NewRelation{{Type: domain.RelBlockedBy, Other: blocker.ID}}
	})
	held := create(t, b, ctx, "held", func(r *backend.IssueCreate) { r.Assignee = "claude-1" })

	ready, err := b.ListIssues(ctx, &domain.Filter{
		Readiness:  domain.ReadinessReady,
		Statuses:   []domain.Status{domain.StatusOpen},
		Unassigned: true,
		Sort:       domain.DefaultSort,
	})
	require.NoError(t, err)
	require.Len(t, ready.Issues, 1)
	assert.Equal(t, blocker.ID, ready.Issues[0].ID,
		"ready lists only unassigned, unblocked, open issues")

	blockedPage, err := b.ListIssues(ctx, &domain.Filter{
		Readiness: domain.ReadinessBlocked, Sort: domain.DefaultSort,
	})
	require.NoError(t, err)
	require.Len(t, blockedPage.Issues, 1)
	assert.Equal(t, blocked.ID, blockedPage.Issues[0].ID)
	assert.Equal(t, []string{blocker.ID}, blockedPage.Issues[0].Blockers)

	// Closing the blocker makes the blocked issue ready, with no write to it.
	_, err = b.CloseIssue(ctx, blocker.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)

	ready, err = b.ListIssues(ctx, &domain.Filter{
		Readiness:  domain.ReadinessReady,
		Statuses:   []domain.Status{domain.StatusOpen},
		Unassigned: true,
		Sort:       domain.DefaultSort,
	})
	require.NoError(t, err)
	require.Len(t, ready.Issues, 1)
	assert.Equal(t, blocked.ID, ready.Issues[0].ID)
	assert.NotEqual(t, held.ID, ready.Issues[0].ID)
}

// A filter naming a project that is not there is a mistake to report, not a
// listing that happens to match nothing.
func TestFilterNamingAMissingProjectIsNotFound(t *testing.T) {
	b, ctx := newBackend(t)

	_, err := b.ListIssues(ctx, &domain.Filter{Projects: []string{"nosuch"}, Sort: domain.DefaultSort})
	require.Error(t, err)
	assert.Equal(t, 3, exitOf(err))

	_, err = b.ListIssues(ctx, &domain.Filter{Parent: "awb-ffffff", Sort: domain.DefaultSort})
	require.Error(t, err)
	assert.Equal(t, 3, exitOf(err))
}

func TestDeleteIssue(t *testing.T) {
	b, ctx := newBackend(t)
	target := create(t, b, ctx, "target")
	child := create(t, b, ctx, "child", func(r *backend.IssueCreate) {
		r.Relations = []backend.NewRelation{{Type: domain.RelHasParent, Other: target.ID}}
	})

	deleted, err := b.DeleteIssue(ctx, target.ID, "")
	require.NoError(t, err)
	assert.Equal(t, target.ID, deleted.Issue.ID)
	assert.Equal(t, 1, deleted.RelationsRemoved)

	_, err = b.GetIssue(ctx, target.ID)
	require.Error(t, err)
	assert.Equal(t, 3, exitOf(err))

	// The child is orphaned, not deleted.
	orphan, err := b.GetIssue(ctx, child.ID)
	require.NoError(t, err)
	assert.Empty(t, orphan.Relations)
}

func TestProjectLifecycle(t *testing.T) {
	b, ctx := newBackend(t)

	created, err := b.CreateProject(ctx, backend.ProjectCreate{Key: "web"})
	require.NoError(t, err)
	assert.Equal(t, "web", created.Name, "the name defaults to the key")

	name := "Web UI"
	updated, err := b.UpdateProject(ctx, "web", backend.ProjectPatch{Name: &name}, "")
	require.NoError(t, err)
	assert.Equal(t, "Web UI", updated.Name)

	// An empty name restores the key.
	empty := ""
	restored, err := b.UpdateProject(ctx, "web", backend.ProjectPatch{Name: &empty}, "")
	require.NoError(t, err)
	assert.Equal(t, "web", restored.Name)

	// A duplicate key conflicts.
	_, err = b.CreateProject(ctx, backend.ProjectCreate{Key: "web"})
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
}

// project delete refuses while the project holds any issue at all, closed ones
// included, so confirmation alone can never destroy closed history.
func TestProjectDeletionAndCascade(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")
	_, err := b.CloseIssue(ctx, issue.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)

	_, err = b.DeleteProject(ctx, "awb", false, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))

	deleted, err := b.DeleteProject(ctx, "awb", true, "")
	require.NoError(t, err)
	assert.Equal(t, "awb", deleted.Project.Key)

	// The issues went with it, closed ones included.
	page, err := b.ListIssues(ctx, &domain.Filter{IncludeClosed: true, Sort: domain.DefaultSort})
	require.NoError(t, err)
	assert.Empty(t, page.Issues)
}

// The conditional-edit precondition. The CLI never sends one and gets
// last-write-wins; a UI sends the tag it read.
func TestIfMatch(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")
	tag := local.ETag(issue.UpdatedAt)

	title := "renamed"
	updated, err := b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title}, tag)
	require.NoError(t, err)

	// The stale tag no longer matches.
	title2 := "renamed again"
	_, err = b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title2}, tag)
	require.Error(t, err)
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed)

	// The fresh one does.
	_, err = b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title2},
		local.ETag(updated.UpdatedAt))
	assert.NoError(t, err)

	// No precondition is last-write-wins.
	_, err = b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title}, "")
	assert.NoError(t, err)
}

// A relation added meanwhile does not move updated_at, so a conditional edit
// does not fail on it — which is the whole point of the tag covering the
// issue's own stored fields alone.
func TestETagIsNotInvalidatedByRelations(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")
	other := create(t, b, ctx, "other")
	tag := local.ETag(issue.UpdatedAt)

	_, err := b.AddRelation(ctx, issue.ID,
		backend.RelationRequest{Type: domain.RelBlockedBy, Other: other.ID}, "")
	require.NoError(t, err)

	title := "renamed"
	_, err = b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title}, tag)
	assert.NoError(t, err, "the tag guards the issue's own stored fields")
}

func TestReferencesResolveEverywhere(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")
	_, hash, _ := domain.SplitID(issue.ID)

	for _, ref := range []string{issue.ID, hash, hash[:3], "AWB-" + hash} {
		got, err := b.GetIssue(ctx, ref)
		require.NoError(t, err, ref)
		assert.Equal(t, issue.ID, got.ID, ref)
	}
}

func TestTree(t *testing.T) {
	b, ctx := newBackend(t)
	root := create(t, b, ctx, "root")
	child := create(t, b, ctx, "child", func(r *backend.IssueCreate) {
		r.Relations = []backend.NewRelation{{Type: domain.RelHasParent, Other: root.ID}}
	})

	tree, err := b.Tree(ctx, root.ID)
	require.NoError(t, err)
	assert.Equal(t, root.ID, tree.ID)
	require.Len(t, tree.Children, 1)
	assert.Equal(t, child.ID, tree.Children[0].ID)
	assert.Equal(t, []domain.IssueTree{}, tree.Children[0].Children)
}

func TestIdentity(t *testing.T) {
	b, ctx := newBackend(t)
	identity, err := b.Identity(ctx)
	require.NoError(t, err)
	assert.Equal(t, "mikael", identity)
}
