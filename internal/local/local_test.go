package local_test

import (
	"context"
	"fmt"
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
	assert.Zero(t, issue.Order)
	assert.Empty(t, issue.Assignees)
	assert.True(t, issue.Ready())
}

func TestMoveIssueAcrossBoardAndSparseReorder(t *testing.T) {
	b, ctx := newBackend(t)
	_, err := b.CreateProject(ctx, backend.ProjectCreate{Key: "web"})
	require.NoError(t, err)
	create(t, b, ctx, "first")
	create(t, b, ctx, "second")
	create(t, b, ctx, "third")
	page, err := b.ListIssues(ctx, &domain.Filter{Sort: domain.DefaultSort})
	require.NoError(t, err)
	first, second, third := &page.Issues[0], &page.Issues[1], &page.Issues[2]

	// The first manual move ranks the dragged issue without touching unrelated
	// automatic rows.
	third, err = b.MoveIssue(ctx, third.ID, backend.IssueMove{
		Status: domain.StatusOpen,
	}, "")
	require.NoError(t, err)
	assert.Positive(t, third.Order)
	secondAfter, err := b.GetIssue(ctx, second.ID)
	require.NoError(t, err)
	assert.Zero(t, secondAfter.Order)
	firstAutomatic, err := b.GetIssue(ctx, first.ID)
	require.NoError(t, err)
	assert.Zero(t, firstAutomatic.Order)

	// A move between ranked neighbors chooses the sparse gap and leaves its
	// neighbor's representation/ETag unchanged.
	thirdTag := third.UpdatedAt
	first, err = b.MoveIssue(ctx, first.ID, backend.IssueMove{
		Status: domain.StatusOpen, Before: third.ID,
	}, "")
	require.NoError(t, err)
	assert.Less(t, first.Order, third.Order)
	thirdAgain, err := b.GetIssue(ctx, third.ID)
	require.NoError(t, err)
	assert.Equal(t, thirdTag, thirdAgain.UpdatedAt)

	second, err = b.MoveIssue(ctx, second.ID, backend.IssueMove{
		Status: domain.StatusOpen, After: first.ID,
	}, "")
	require.NoError(t, err)
	assert.Greater(t, second.Order, first.Order)
	assert.Less(t, second.Order, third.Order)

	automatic := create(t, b, ctx, "automatic anchor")
	third, err = b.MoveIssue(ctx, third.ID, backend.IssueMove{
		Status: domain.StatusOpen, Before: automatic.ID,
	}, "")
	require.NoError(t, err)
	automatic, err = b.GetIssue(ctx, automatic.ID)
	require.NoError(t, err)
	assert.Greater(t, automatic.Order, third.Order)
	activity, err := b.ListActivity(ctx, automatic.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "reordered", activity.Activity[0].Action,
		"ranking an explicit automatic anchor records its stored-field mutation")

	_, err = b.MoveIssue(ctx, second.ID, backend.IssueMove{
		Status: domain.StatusOpen, Before: first.ID, After: third.ID,
	}, "")
	assert.Equal(t, 2, exitOf(err), "the two relative anchors are mutually exclusive")

	// Epic swimlane and status change together without changing workspace or ID.
	epicOne := create(t, b, ctx, "Epic one", func(req *backend.IssueCreate) { req.Type = domain.TypeEpic })
	epicTwo := create(t, b, ctx, "Epic two", func(req *backend.IssueCreate) { req.Type = domain.TypeEpic })
	epicTwoID := epicTwo.ID
	moved, err := b.MoveIssue(ctx, first.ID, backend.IssueMove{
		Status: domain.StatusInProgress, Epic: &epicTwoID,
	}, backend.ETag(first.UpdatedAt))
	require.NoError(t, err)
	assert.Equal(t, first.ID, moved.ID)
	assert.Equal(t, "awb", moved.Project)
	assert.Equal(t, domain.StatusInProgress, moved.Status)
	assert.Equal(t, []string{"mikael"}, moved.Assignees)
	assert.Contains(t, moved.Relations, domain.Relation{Type: domain.RelHasParent, Other: epicTwo.ID, Direction: domain.DirectionOut})

	noEpic := ""
	moved, err = b.MoveIssue(ctx, moved.ID, backend.IssueMove{Status: domain.StatusInProgress, Epic: &noEpic}, "")
	require.NoError(t, err)
	assert.NotContains(t, moved.Relations, domain.Relation{Type: domain.RelHasParent, Other: epicTwo.ID, Direction: domain.DirectionOut})
	epicOneID := epicOne.ID
	moved, err = b.MoveIssue(ctx, moved.ID, backend.IssueMove{Status: domain.StatusInProgress, Epic: &epicOneID}, "")
	require.NoError(t, err)
	assert.Contains(t, moved.Relations, domain.Relation{Type: domain.RelHasParent, Other: epicOne.ID, Direction: domain.DirectionOut})

	webIssue, err := b.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "other workspace"})
	require.NoError(t, err)
	_, err = b.MoveIssue(ctx, second.ID, backend.IssueMove{Status: domain.StatusOpen, Before: webIssue.ID}, "")
	assert.Equal(t, 2, exitOf(err), "manual order cannot cross workspace boundaries")
}

func TestMoveIssueRecordsAnAutomaticAnchorWhenDraggedRankIsUnchanged(t *testing.T) {
	b, ctx := newBackend(t)
	dragged := create(t, b, ctx, "ranked")
	dragged, err := b.MoveIssue(ctx, dragged.ID, backend.IssueMove{
		Status: domain.StatusOpen,
	}, "")
	require.NoError(t, err)
	draggedOrder := dragged.Order
	automatic := create(t, b, ctx, "automatic anchor")

	dragged, err = b.MoveIssue(ctx, dragged.ID, backend.IssueMove{
		Status: domain.StatusOpen, Before: automatic.ID,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, draggedOrder, dragged.Order,
		"placing the last ranked issue before an automatic anchor keeps its rank")

	automatic, err = b.GetIssue(ctx, automatic.ID)
	require.NoError(t, err)
	assert.Greater(t, automatic.Order, dragged.Order)
	activity, err := b.ListActivity(ctx, automatic.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, activity.Activity)
	assert.Equal(t, "reordered", activity.Activity[0].Action,
		"the anchor mutation is recorded even when the dragged issue is unchanged")
}

func TestMoveDirectionUsesTheTransactionTimeWorkspaceOrder(t *testing.T) {
	b, ctx := newBackend(t)
	for i := range 26 {
		create(t, b, ctx, fmt.Sprintf("paged %02d", i))
	}
	limit, firstOffset, secondOffset := 25, 0, 25
	firstPage, err := b.ListIssues(ctx, &domain.Filter{Sort: domain.DefaultSort, Limit: &limit, Offset: &firstOffset})
	require.NoError(t, err)
	secondPage, err := b.ListIssues(ctx, &domain.Filter{Sort: domain.DefaultSort, Limit: &limit, Offset: &secondOffset})
	require.NoError(t, err)
	require.Len(t, firstPage.Issues, 25)
	require.Len(t, secondPage.Issues, 1)
	anchor := firstPage.Issues[24]
	moving := secondPage.Issues[0]

	moved, err := b.MoveIssue(ctx, moving.ID, backend.IssueMove{
		Status: domain.StatusOpen, Direction: "earlier",
	}, "")
	require.NoError(t, err)
	assert.Positive(t, moved.Order)
	anchorAfter, err := b.GetIssue(ctx, anchor.ID)
	require.NoError(t, err)
	assert.Less(t, moved.Order, anchorAfter.Order,
		"direction resolves the neighbor outside the caller's visible offset page")

	_, err = b.MoveIssue(ctx, moving.ID, backend.IssueMove{
		Status: domain.StatusOpen, Direction: "earlier", Before: anchor.ID,
	}, "")
	assert.Equal(t, 2, exitOf(err))
}

func TestMoveIssueGuardsEpicMembershipAndCellOrderingAtomically(t *testing.T) {
	b, ctx := newBackend(t)
	epicOne := create(t, b, ctx, "Epic one", func(req *backend.IssueCreate) { req.Type = domain.TypeEpic })
	epicTwo := create(t, b, ctx, "Epic two", func(req *backend.IssueCreate) { req.Type = domain.TypeEpic })
	child := create(t, b, ctx, "Child", func(req *backend.IssueCreate) {
		req.Relations = []backend.NewRelation{{Type: domain.RelHasParent, Other: epicTwo.ID}}
	})
	peer := create(t, b, ctx, "Peer", func(req *backend.IssueCreate) {
		req.Relations = []backend.NewRelation{{Type: domain.RelHasParent, Other: epicTwo.ID}}
	})

	stale := backend.ETag(child.UpdatedAt)
	changed := "Changed child"
	child, err := b.UpdateIssue(ctx, child.ID, backend.IssuePatch{Title: &changed}, "")
	require.NoError(t, err)
	epicOneID := epicOne.ID
	_, err = b.MoveIssue(ctx, child.ID, backend.IssueMove{
		Status: domain.StatusOpen, Epic: &epicOneID,
	}, stale)
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed)
	child, err = b.GetIssue(ctx, child.ID)
	require.NoError(t, err)
	assert.Contains(t, child.Relations, domain.Relation{Type: domain.RelHasParent, Other: epicTwo.ID, Direction: domain.DirectionOut})

	// The target anchor belongs to the old epic cell. Changing membership and
	// ordering fails as one transaction, leaving the old relation intact.
	_, err = b.MoveIssue(ctx, child.ID, backend.IssueMove{
		Status: domain.StatusOpen, Epic: &epicOneID, Before: peer.ID,
	}, backend.ETag(child.UpdatedAt))
	assert.Equal(t, 2, exitOf(err))
	child, err = b.GetIssue(ctx, child.ID)
	require.NoError(t, err)
	assert.Contains(t, child.Relations, domain.Relation{Type: domain.RelHasParent, Other: epicTwo.ID, Direction: domain.DirectionOut})

	feature := create(t, b, ctx, "Feature parent", func(req *backend.IssueCreate) { req.Type = domain.TypeFeature })
	nested := create(t, b, ctx, "Nested", func(req *backend.IssueCreate) {
		req.Relations = []backend.NewRelation{{Type: domain.RelHasParent, Other: feature.ID}}
	})
	_, err = b.MoveIssue(ctx, nested.ID, backend.IssueMove{Status: domain.StatusOpen, Epic: &epicOneID}, "")
	assert.Equal(t, 4, exitOf(err), "a board move never silently replaces a non-epic decomposition parent")
}

// Creating with an assignee is an atomic create-and-claim, so a new issue is
// never open and assigned at once.
func TestCreateWithAssigneeIsAClaim(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignees = []string{"claude-1"} })

	assert.Equal(t, domain.StatusInProgress, issue.Status)
	assert.Equal(t, []string{"claude-1"}, issue.Assignees)
	assert.False(t, issue.Ready())
}

func TestCreateWithSeveralAssigneesIsAClaim(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) {
		r.Assignees = []string{"claude-1", "claude-2", "claude-1"}
	})

	assert.Equal(t, domain.StatusInProgress, issue.Status)
	assert.Equal(t, []string{"claude-1", "claude-2"}, issue.Assignees)
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
	assert.Equal(t, []string{"claude-1"}, claimed.Assignees)

	// Claiming an issue you already hold succeeds and changes nothing.
	again, err := b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-1"}, "")
	require.NoError(t, err)
	assert.Equal(t, claimed.UpdatedAt, again.UpdatedAt)

	// Somebody else joins without replacing the first assignee.
	joined, err := b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "claude-2"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-1", "claude-2"}, joined.Assignees)
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

func TestForcedClaimOfClosedIssueStartsANewAssignmentSet(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "done", func(r *backend.IssueCreate) {
		r.Assignees = []string{"alice", "bob"}
	})
	_, err := b.CloseIssue(ctx, issue.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)

	reclaimed, err := b.Claim(ctx, issue.ID,
		backend.ClaimRequest{Assignee: "carol", Force: true}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"carol"}, reclaimed.Assignees)
}

func TestRelease(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignees = []string{"mikael"} })

	released, err := b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "mikael"}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusOpen, released.Status)
	assert.Empty(t, released.Assignees)

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
	assert.Empty(t, forced.Assignees)
}

func TestReleaseOneOfSeveralAssignees(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) {
		r.Assignees = []string{"mikael", "claude-1"}
	})

	released, err := b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "mikael"}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, released.Status)
	assert.Equal(t, []string{"claude-1"}, released.Assignees)

	released, err = b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "claude-1"}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusOpen, released.Status)
	assert.Empty(t, released.Assignees)
}

func TestCloseAndReopen(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignees = []string{"mikael"} })

	reason := "Guard against empty token stream"
	closed, err := b.CloseIssue(ctx, issue.ID, backend.CloseRequest{Reason: &reason}, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusClosed, closed.Status)
	assert.Equal(t, []string{"mikael"}, closed.Assignees, "assignees record who did the work")

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
	assert.Empty(t, reopened.Assignees)
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
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) { r.Assignees = []string{"claude-1"} })

	unchanged, err := b.Reopen(ctx, issue.ID, "")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, unchanged.Status)
	assert.Equal(t, []string{"claude-1"}, unchanged.Assignees)
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
	held := create(t, b, ctx, "held", func(r *backend.IssueCreate) { r.Assignees = []string{"claude-1"} })

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

// The conditional-edit precondition. A caller which sends the tag it read gets
// compare-and-set semantics; omitting it gets last-write-wins.
func TestIfMatch(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t")
	tag := backend.ETag(issue.UpdatedAt)

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
		backend.ETag(updated.UpdatedAt))
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
	tag := backend.ETag(issue.UpdatedAt)

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
