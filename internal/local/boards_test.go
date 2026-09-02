package local_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

func TestBoardViewsAreOwnedShareableAndViewerScoped(t *testing.T) {
	root, ctx := newInstance(t)
	for _, name := range []string{"alice", "bob"} {
		addUser(t, root, ctx, name, false, false)
	}
	for _, name := range []string{"alice", "bob"} {
		grant(t, root, ctx, "awb", name, domain.AccessRegular)
	}
	grant(t, root, ctx, "web", "alice", domain.AccessRegular)
	_, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "shared"})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "alice only"})
	require.NoError(t, err)

	alice := root.WithUser("alice")
	shared, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: " Release ", Shared: true,
		AllWorkspaces: false, Workspaces: []string{"awb", "web"}, PriorityMax: 4})
	require.NoError(t, err)
	private, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Private", AllWorkspaces: true, PriorityMax: 4})
	require.NoError(t, err)
	assert.Equal(t, "Release", shared.Name)

	views, err := alice.ListBoardViews(ctx)
	require.NoError(t, err)
	assert.Len(t, views, 2)
	bob := root.WithUser("bob")
	views, err = bob.ListBoardViews(ctx)
	require.NoError(t, err)
	assert.Empty(t, views, "shared links are unlisted")
	_, err = bob.GetBoardView(ctx, private.ID)
	notFound(t, err)
	name := "disclosed"
	_, err = bob.UpdateBoardView(ctx, private.ID, backend.BoardViewPatch{Name: &name}, "")
	notFound(t, err, "a private view's mutation endpoint does not disclose it")
	_, err = bob.DeleteBoardView(ctx, private.ID, "")
	notFound(t, err, "a private view's delete endpoint does not disclose it")
	visible, err := bob.GetBoardView(ctx, shared.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"awb"}, visible.Workspaces, "view metadata cannot disclose an inaccessible key")

	board, err := bob.GetBoard(ctx, shared.ID, backend.BoardQuery{})
	require.NoError(t, err)
	require.Len(t, board.Lanes, 1)
	assert.Nil(t, board.Lanes[0].Epic)
	assert.Equal(t, "awb", board.Lanes[0].Columns[0].Issues[0].Workspace)
	assert.True(t, board.WorkspacesOmitted)
	_, err = bob.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{}, "")
	forbidden(t, err)

	_, err = root.RemoveMember(ctx, "web", "alice")
	require.NoError(t, err)
	views, err = alice.ListBoardViews(ctx)
	require.NoError(t, err)
	for _, view := range views {
		assert.NotContains(t, view.Workspaces, "web", "owned view listings do not disclose revoked workspaces")
	}
	renamed := "Release train"
	unshared := false
	updated, err := alice.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{Name: &renamed, Shared: &unshared}, backend.ETag(shared.UpdatedAt))
	require.NoError(t, err, "an unrelated edit preserves an inaccessible stored selection")
	assert.Equal(t, renamed, updated.Name)
	assert.False(t, updated.Shared)
	assert.Equal(t, []string{"awb"}, updated.Workspaces, "the mutation response is scoped too")
	visibleReplacement := []string{"awb"}
	_, err = alice.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{Workspaces: &visibleReplacement}, backend.ETag(updated.UpdatedAt))
	require.NoError(t, err, "replacing visible selections preserves inaccessible stored selections")
	grant(t, root, ctx, "web", "alice", domain.AccessRegular)
	restored, err := alice.GetBoardView(ctx, shared.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"awb", "web"}, restored.Workspaces)
}

func TestBoardHidesExplicitAndExpiredClosedIssues(t *testing.T) {
	root, ctx := newInstance(t)
	visible, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "visible"})
	require.NoError(t, err)
	hidden, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "hidden"})
	require.NoError(t, err)
	closed, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "closed"})
	require.NoError(t, err)
	hiddenEpic, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "hidden epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	closedEpic, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "closed epic", Type: domain.TypeEpic})
	require.NoError(t, err)

	boardHidden := true
	hidden, err = root.UpdateIssue(ctx, hidden.ID, backend.IssuePatch{BoardHidden: &boardHidden}, "")
	require.NoError(t, err)
	assert.True(t, hidden.BoardHidden)
	_, err = root.UpdateIssue(ctx, hiddenEpic.ID, backend.IssuePatch{BoardHidden: &boardHidden}, "")
	require.NoError(t, err)
	closed, err = root.CloseIssue(ctx, closed.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)
	assert.NotEmpty(t, closed.ClosedAt)
	_, err = root.CloseIssue(ctx, closedEpic.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)

	thirty := 30
	board, err := root.GetBoard(ctx, "default", backend.BoardQuery{ClosedDays: &thirty})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal, "closed and hidden epics disappear immediately")
	assert.Equal(t, []string{visible.ID}, issueIDs(board.Lanes[0].Columns[0].Issues))
	assert.Equal(t, []string{closed.ID}, issueIDs(board.Lanes[0].Columns[2].Issues))
	board, err = root.GetBoard(ctx, "default", backend.BoardQuery{ClosedDays: &thirty, EpicClosedDays: &thirty})
	require.NoError(t, err)
	assert.Equal(t, 2, board.LaneTotal, "epic lanes have an independent retention window")
	assert.Equal(t, closedEpic.ID, board.Lanes[1].Epic.ID)

	zero := 0
	board, err = root.GetBoard(ctx, "default", backend.BoardQuery{ClosedDays: &zero})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal, "No epic is the only lane after closed and hidden epics are omitted")
	assert.Empty(t, board.Lanes[0].Columns[2].Issues)
	assert.Zero(t, board.Lanes[0].Columns[2].Total)

	view, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Recent", AllWorkspaces: true,
		AllEpics: true, IncludeNoEpic: true, PriorityMax: 4, ClosedDays: 0})
	require.NoError(t, err)
	board, err = root.GetBoard(ctx, view.ID, backend.BoardQuery{ClosedDays: &thirty})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal)
	assert.Empty(t, board.Lanes[0].Columns[2].Issues, "a saved view uses its stored setting")
	view, err = root.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{ClosedDays: &thirty}, backend.ETag(view.UpdatedAt))
	require.NoError(t, err)
	assert.Equal(t, 30, view.ClosedDays)
	noWorkspaces := false
	board, err = root.GetBoard(ctx, view.ID, backend.BoardQuery{ClosedDays: &zero, AllWorkspaces: &noWorkspaces})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal)
	assert.Equal(t, []string{closed.ID}, issueIDs(board.Lanes[0].Columns[2].Issues), "request preferences do not override a saved view")

	pinned, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Pinned", AllWorkspaces: true,
		Epics: []string{closedEpic.ID}, PriorityMax: 4, ClosedDays: 30})
	require.NoError(t, err)
	board, err = root.GetBoard(ctx, pinned.ID, backend.BoardQuery{})
	require.NoError(t, err)
	assert.Zero(t, board.LaneTotal, "a pinned closed epic disappears with the default zero-day retention")
	_, err = root.GetBoard(ctx, "default", backend.BoardQuery{Epic: &closedEpic.ID})
	notFound(t, err, "a closed epic cannot be requested directly")
	pinned, err = root.UpdateBoardView(ctx, pinned.ID, backend.BoardViewPatch{EpicClosedDays: &thirty}, backend.ETag(pinned.UpdatedAt))
	require.NoError(t, err)
	board, err = root.GetBoard(ctx, pinned.ID, backend.BoardQuery{})
	require.NoError(t, err)
	require.Len(t, board.Lanes, 1)
	assert.Equal(t, closedEpic.ID, board.Lanes[0].Epic.ID)
	board, err = root.GetBoard(ctx, "default", backend.BoardQuery{Epic: &closedEpic.ID, EpicClosedDays: &thirty})
	require.NoError(t, err, "a retained closed epic can be requested directly")
	require.Len(t, board.Lanes, 1)
}

func issueIDs(issues []domain.Issue) []string {
	ids := make([]string, len(issues))
	for i := range issues {
		ids[i] = issues[i].ID
	}
	return ids
}

func TestBoardUsesIgnoredScopeFiltersAndIndependentPaging(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", false, false)
	for _, workspace := range []string{"awb", "web"} {
		grant(t, root, ctx, workspace, "alice", domain.AccessRegular)
	}
	for _, title := range []string{"first", "second"} {
		_, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: title, Labels: []string{"release"}})
		require.NoError(t, err)
	}
	_, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "ignored", Labels: []string{"release"}})
	require.NoError(t, err)
	alice := root.WithUser("alice")
	view, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Release", AllWorkspaces: false,
		Workspaces: []string{"awb", "web"}, Labels: []string{"release"}, PriorityMax: 2})
	require.NoError(t, err)
	_, err = alice.SetWorkspaceIgnored(ctx, "web", true)
	require.NoError(t, err)
	views, err := alice.ListBoardViews(ctx)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, []string{"awb", "web"}, views[0].Workspaces, "the owner can still edit an ignored selection")
	one, zero := 1, 0
	board, err := alice.GetBoard(ctx, view.ID, backend.BoardQuery{LaneLimit: &one, LaneOffset: &zero, CardLimit: &one})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal)
	assert.True(t, board.WorkspacesOmitted)
	require.Len(t, board.Lanes, 1)
	open := board.Lanes[0].Columns[0]
	assert.Equal(t, domain.StatusOpen, open.Status)
	assert.Equal(t, 2, open.Total)
	assert.Len(t, open.Issues, 1)

	second, err := alice.GetBoard(ctx, view.ID, backend.BoardQuery{Workspaces: []string{"awb"}, Status: domain.StatusOpen,
		CardLimit: &one, CardOffset: &one})
	require.NoError(t, err)
	require.Len(t, second.Lanes, 1)
	require.Len(t, second.Lanes[0].Columns, 1)
	assert.Equal(t, 2, second.Lanes[0].Columns[0].Total)
	assert.Len(t, second.Lanes[0].Columns[0].Issues, 1)
}

func TestBoardGroupsCardsByVisibleSameWorkspaceEpics(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", false, false)
	grant(t, root, ctx, "awb", "alice", domain.AccessRegular)
	visibleEpic, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	child, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: visibleEpic.ID}}})
	require.NoError(t, err)
	loose, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Loose work"})
	require.NoError(t, err)
	hiddenEpic, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Secret epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Secret child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: hiddenEpic.ID}}})
	require.NoError(t, err)

	board, err := root.WithUser("alice").GetBoard(ctx, "default", backend.BoardQuery{})
	require.NoError(t, err)
	assert.Equal(t, 2, board.LaneTotal, "No epic plus the one authorized epic")
	require.Len(t, board.Lanes, 2)
	assert.Nil(t, board.Lanes[0].Epic)
	assert.Equal(t, loose.ID, board.Lanes[0].Columns[0].Issues[0].ID)
	require.NotNil(t, board.Lanes[1].Epic)
	assert.Equal(t, visibleEpic.ID, board.Lanes[1].Epic.ID)
	assert.Equal(t, child.ID, board.Lanes[1].Columns[0].Issues[0].ID)
	for _, lane := range board.Lanes {
		for _, column := range lane.Columns {
			for _, issue := range column.Issues {
				assert.Equal(t, "awb", issue.Workspace)
				assert.NotEqual(t, domain.TypeEpic, issue.Type, "epics are lane headers, not cards")
			}
		}
	}

	zero, one := 0, 1
	specific, err := root.WithUser("alice").GetBoard(ctx, "default", backend.BoardQuery{
		Epic: &visibleEpic.ID, LaneLimit: &one,
	})
	require.NoError(t, err)
	require.Len(t, specific.Lanes, 1)
	assert.Equal(t, visibleEpic.ID, specific.Lanes[0].Epic.ID)
	hiddenFromView, err := root.WithUser("alice").GetBoard(ctx, "default", backend.BoardQuery{HiddenEpics: []string{visibleEpic.ID}})
	require.NoError(t, err)
	assert.Equal(t, 1, hiddenFromView.LaneTotal, "the No epic lane remains while the requested epic is excluded")
	require.Len(t, hiddenFromView.Lanes, 1)
	assert.Nil(t, hiddenFromView.Lanes[0].Epic)
	for _, query := range []backend.BoardQuery{
		{Epic: &hiddenEpic.ID},
		{Epic: &hiddenEpic.ID, LaneLimit: &zero},
		{Epic: &hiddenEpic.ID, LaneOffset: &one},
		{Epic: &visibleEpic.ID, HiddenEpics: []string{visibleEpic.ID}},
	} {
		_, err = root.WithUser("alice").GetBoard(ctx, "default", query)
		notFound(t, err, "pagination must not bypass explicit epic validation")
	}
	_, err = root.WithUser("alice").GetBoard(ctx, "default", backend.BoardQuery{HiddenEpics: []string{"not-an-id"}})
	assert.Equal(t, 2, exitOf(err))
}

func TestSavedBoardSelectsAndUpdatesEpicLanes(t *testing.T) {
	root, ctx := newInstance(t)
	first, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "First epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	second, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Second epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	view, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Pinned", AllWorkspaces: true,
		AllEpics: false, Epics: []string{first.ID}, IncludeNoEpic: false, PriorityMax: 4})
	require.NoError(t, err)

	board, err := root.GetBoard(ctx, view.ID, backend.BoardQuery{})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal)
	require.Len(t, board.Lanes, 1)
	require.NotNil(t, board.Lanes[0].Epic)
	assert.Equal(t, first.ID, board.Lanes[0].Epic.ID)
	board, err = root.GetBoard(ctx, view.ID, backend.BoardQuery{HiddenEpics: []string{first.ID}})
	require.NoError(t, err)
	assert.Zero(t, board.LaneTotal, "viewer presentation state can hide a pinned lane without changing the view")
	_, err = root.GetBoard(ctx, view.ID, backend.BoardQuery{Epic: &second.ID})
	notFound(t, err, "an explicit lane cannot escape a saved view's pinned epic set")

	epics := []string{second.ID}
	includeNoEpic := true
	updated, err := root.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Epics: &epics,
		IncludeNoEpic: &includeNoEpic}, backend.ETag(view.UpdatedAt))
	require.NoError(t, err)
	assert.Equal(t, epics, updated.Epics)
	board, err = root.GetBoard(ctx, view.ID, backend.BoardQuery{})
	require.NoError(t, err)
	assert.Equal(t, 2, board.LaneTotal)
	require.Len(t, board.Lanes, 2)
	assert.Nil(t, board.Lanes[0].Epic)
	assert.Equal(t, second.ID, board.Lanes[1].Epic.ID)

	_, err = root.DeleteIssue(ctx, second.ID, "")
	require.NoError(t, err)
	afterDelete, err := root.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Empty(t, afterDelete.Epics)
	assert.Greater(t, afterDelete.UpdatedAt, updated.UpdatedAt)
	name := "stale"
	_, err = root.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Name: &name}, backend.ETag(updated.UpdatedAt))
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed)
}

func TestSavedBoardFiltersSelectedEpicsBeforePaging(t *testing.T) {
	root, ctx := newInstance(t)
	selected := []string{}
	hidden := []string{}
	visible := []string{}
	for i := range 30 {
		epic, err := root.CreateIssue(ctx, backend.IssueCreate{
			Workspace: "awb", Title: fmt.Sprintf("Epic %02d", i), Type: domain.TypeEpic,
		})
		require.NoError(t, err)
		selected = append(selected, epic.ID)
		switch {
		case i < 10:
			_, err = root.CloseIssue(ctx, epic.ID, backend.CloseRequest{}, "")
			require.NoError(t, err)
		case i < 20:
			hidden = append(hidden, epic.ID)
		default:
			visible = append(visible, epic.ID)
		}
	}
	view, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Selected", AllWorkspaces: true,
		AllEpics: false, Epics: selected, IncludeNoEpic: false, PriorityMax: 4})
	require.NoError(t, err)

	slices.Sort(visible)
	limit, offset := 3, 0
	board, err := root.GetBoard(ctx, view.ID, backend.BoardQuery{
		HiddenEpics: hidden, LaneLimit: &limit, LaneOffset: &offset,
	})
	require.NoError(t, err)
	assert.Equal(t, len(visible), board.LaneTotal)
	require.Len(t, board.Lanes, limit)
	assert.Equal(t, visible[:limit], laneEpicIDs(board.Lanes),
		"closed and viewer-hidden selections are removed before the lane page is chosen")

	offset = limit
	board, err = root.GetBoard(ctx, view.ID, backend.BoardQuery{
		HiddenEpics: hidden, LaneLimit: &limit, LaneOffset: &offset,
	})
	require.NoError(t, err)
	assert.Equal(t, visible[offset:offset+limit], laneEpicIDs(board.Lanes))
}

func laneEpicIDs(lanes []domain.BoardLane) []string {
	ids := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		if lane.Epic != nil {
			ids = append(ids, lane.Epic.ID)
		}
	}
	return ids
}

func TestDefaultBoardAcceptsTheSameScopeAndIssueFiltersAsSavedViews(t *testing.T) {
	root, ctx := newInstance(t)
	first, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "First epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	second, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Second epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	priorityOne, priorityThree := 1, 3
	matching, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Matching",
		Priority: &priorityOne, Labels: []string{"release"}, Assignees: []string{"alex"},
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: first.ID}}})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Wrong priority",
		Priority: &priorityThree, Labels: []string{"release"}, Assignees: []string{"alex"},
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: first.ID}}})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Wrong epic",
		Priority: &priorityOne, Labels: []string{"release"}, Assignees: []string{"alex"},
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: second.ID}}})
	require.NoError(t, err)

	allWorkspaces, allEpics, includeNoEpic, priorityMax := false, false, false, 2
	board, err := root.GetBoard(ctx, "default", backend.BoardQuery{AllWorkspaces: &allWorkspaces, Workspaces: []string{"awb"},
		AllEpics: &allEpics, Epics: []string{first.ID, first.ID},
		IncludeNoEpic: &includeNoEpic, Labels: []string{"release"}, Assignees: []string{"alex"}, PriorityMax: &priorityMax})
	require.NoError(t, err)
	require.Len(t, board.Lanes, 1)
	assert.Equal(t, 1, board.LaneTotal, "repeated selected epic IDs describe one lane")
	require.NotNil(t, board.Lanes[0].Epic)
	assert.Equal(t, first.ID, board.Lanes[0].Epic.ID)
	assert.Equal(t, []string{matching.ID}, issueIDs(board.Lanes[0].Columns[1].Issues))

	empty, err := root.GetBoard(ctx, "default", backend.BoardQuery{AllWorkspaces: &allWorkspaces})
	require.NoError(t, err)
	assert.Zero(t, empty.LaneTotal, "an empty selected-workspace scope must not widen to every workspace")
}

func TestChangingAPinnedEpicTypeMovesTheBoardViewVersion(t *testing.T) {
	root, ctx := newInstance(t)
	epic, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Pinned epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	view, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Pinned", AllWorkspaces: true,
		AllEpics: false, Epics: []string{epic.ID}, PriorityMax: 4})
	require.NoError(t, err)

	nextType := domain.TypeTask
	_, err = root.UpdateIssue(ctx, epic.ID, backend.IssuePatch{Type: &nextType}, backend.ETag(epic.UpdatedAt))
	require.NoError(t, err)
	updated, err := root.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Empty(t, updated.Epics)
	assert.Greater(t, updated.UpdatedAt, view.UpdatedAt)

	name := "stale"
	_, err = root.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Name: &name}, backend.ETag(view.UpdatedAt))
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed)
	_, err = root.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Name: &name}, backend.ETag(updated.UpdatedAt))
	require.NoError(t, err, "the cleaned-up view remains editable")
}

func TestBoardAppliesServerSidePageBoundsWhenLimitsAreOmitted(t *testing.T) {
	root, ctx := newInstance(t)
	for i := range 51 {
		_, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: fmt.Sprintf("issue %02d", i)})
		require.NoError(t, err)
	}
	board, err := root.GetBoard(ctx, "default", backend.BoardQuery{})
	require.NoError(t, err)
	var open domain.BoardColumn
	for _, lane := range board.Lanes {
		if lane.Epic == nil {
			open = lane.Columns[0]
		}
	}
	assert.Equal(t, 51, open.Total)
	assert.Len(t, open.Issues, 50, "an omitted card limit is still bounded")

	over := 51
	_, err = root.GetBoard(ctx, "default", backend.BoardQuery{CardLimit: &over})
	require.Error(t, err)
	assert.Equal(t, 2, exitOf(err))
}

func TestDeletingASelectedWorkspaceMovesTheBoardViewETag(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", false, false)
	grant(t, root, ctx, "web", "alice", domain.AccessRegular)
	alice := root.WithUser("alice")
	view, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Web", AllWorkspaces: false,
		Workspaces: []string{"web"}, PriorityMax: 4})
	require.NoError(t, err)

	_, err = root.DeleteWorkspace(ctx, "web", false, "")
	require.NoError(t, err)
	updated, err := alice.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Empty(t, updated.Workspaces)
	assert.Greater(t, updated.UpdatedAt, view.UpdatedAt)

	name := "stale edit"
	_, err = alice.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Name: &name}, backend.ETag(view.UpdatedAt))
	require.Error(t, err)
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed, "the pre-deletion ETag is stale")
}

func TestArchivedWorkspaceSelectionIsDormantAndRestored(t *testing.T) {
	root, ctx := newInstance(t)
	epic, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Dormant epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	view, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Current work", AllWorkspaces: false,
		Workspaces: []string{"awb"}, PriorityMax: 4})
	require.NoError(t, err)
	workspace, err := root.GetWorkspace(ctx, "awb")
	require.NoError(t, err)
	archived, err := root.ArchiveWorkspace(ctx, "awb", backend.ETag(workspace.UpdatedAt))
	require.NoError(t, err)

	hidden, err := root.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Empty(t, hidden.Workspaces, "archived workspaces are omitted from normal board metadata")
	board, err := root.GetBoard(ctx, view.ID, backend.BoardQuery{})
	require.NoError(t, err)
	assert.True(t, board.WorkspacesOmitted)
	require.Len(t, board.Lanes, 1)
	for _, column := range board.Lanes[0].Columns {
		assert.Empty(t, column.Issues)
	}
	for _, ref := range []string{"default", view.ID} {
		_, err = root.GetBoard(ctx, ref, backend.BoardQuery{Epic: &epic.ID})
		notFound(t, err, "an explicit epic cannot bypass the active-workspace board scope")
	}

	name := "Renamed while archived"
	updated, err := root.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Name: &name}, backend.ETag(view.UpdatedAt))
	require.NoError(t, err, "an unrelated edit preserves a dormant workspace selection")
	assert.Empty(t, updated.Workspaces)
	_, err = root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Archived", Workspaces: []string{"awb"}, PriorityMax: 4})
	notFound(t, err, "an archived workspace cannot be newly selected")

	_, err = root.RestoreWorkspace(ctx, "awb", backend.ETag(archived.UpdatedAt))
	require.NoError(t, err)
	restored, err := root.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"awb"}, restored.Workspaces)
}
