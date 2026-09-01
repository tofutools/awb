package local_test

import (
	"fmt"
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
	_, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "shared"})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "alice only"})
	require.NoError(t, err)

	alice := root.WithUser("alice")
	shared, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: " Release ", Shared: true,
		AllProjects: false, Projects: []string{"awb", "web"}, PriorityMax: 4})
	require.NoError(t, err)
	private, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Private", AllProjects: true, PriorityMax: 4})
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
	assert.Equal(t, []string{"awb"}, visible.Projects, "view metadata cannot disclose an inaccessible key")

	board, err := bob.GetBoard(ctx, shared.ID, backend.BoardQuery{})
	require.NoError(t, err)
	require.Len(t, board.Lanes, 1)
	assert.Nil(t, board.Lanes[0].Epic)
	assert.Equal(t, "awb", board.Lanes[0].Columns[0].Issues[0].Project)
	assert.True(t, board.ProjectsOmitted)
	_, err = bob.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{}, "")
	forbidden(t, err)

	_, err = root.RemoveMember(ctx, "web", "alice")
	require.NoError(t, err)
	views, err = alice.ListBoardViews(ctx)
	require.NoError(t, err)
	for _, view := range views {
		assert.NotContains(t, view.Projects, "web", "owned view listings do not disclose revoked projects")
	}
	renamed := "Release train"
	unshared := false
	updated, err := alice.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{Name: &renamed, Shared: &unshared}, backend.ETag(shared.UpdatedAt))
	require.NoError(t, err, "an unrelated edit preserves an inaccessible stored selection")
	assert.Equal(t, renamed, updated.Name)
	assert.False(t, updated.Shared)
	assert.Equal(t, []string{"awb"}, updated.Projects, "the mutation response is scoped too")
	visibleReplacement := []string{"awb"}
	_, err = alice.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{Projects: &visibleReplacement}, backend.ETag(updated.UpdatedAt))
	require.NoError(t, err, "replacing visible selections preserves inaccessible stored selections")
	grant(t, root, ctx, "web", "alice", domain.AccessRegular)
	restored, err := alice.GetBoardView(ctx, shared.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"awb", "web"}, restored.Projects)
}

func TestBoardUsesIgnoredScopeFiltersAndIndependentPaging(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", false, false)
	for _, project := range []string{"awb", "web"} {
		grant(t, root, ctx, project, "alice", domain.AccessRegular)
	}
	for _, title := range []string{"first", "second"} {
		_, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: title, Labels: []string{"release"}})
		require.NoError(t, err)
	}
	_, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "ignored", Labels: []string{"release"}})
	require.NoError(t, err)
	alice := root.WithUser("alice")
	view, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Release", AllProjects: false,
		Projects: []string{"awb", "web"}, Labels: []string{"release"}, PriorityMax: 2})
	require.NoError(t, err)
	_, err = alice.SetProjectIgnored(ctx, "web", true)
	require.NoError(t, err)
	views, err := alice.ListBoardViews(ctx)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, []string{"awb", "web"}, views[0].Projects, "the owner can still edit an ignored selection")
	one, zero := 1, 0
	board, err := alice.GetBoard(ctx, view.ID, backend.BoardQuery{LaneLimit: &one, LaneOffset: &zero, CardLimit: &one})
	require.NoError(t, err)
	assert.Equal(t, 1, board.LaneTotal)
	assert.True(t, board.ProjectsOmitted)
	require.Len(t, board.Lanes, 1)
	open := board.Lanes[0].Columns[0]
	assert.Equal(t, domain.StatusOpen, open.Status)
	assert.Equal(t, 2, open.Total)
	assert.Len(t, open.Issues, 1)

	second, err := alice.GetBoard(ctx, view.ID, backend.BoardQuery{Projects: []string{"awb"}, Status: domain.StatusOpen,
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
	visibleEpic, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "Parser epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	child, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "Parser child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: visibleEpic.ID}}})
	require.NoError(t, err)
	loose, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "Loose work"})
	require.NoError(t, err)
	hiddenEpic, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "Secret epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "Secret child",
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
				assert.Equal(t, "awb", issue.Project)
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
	for _, query := range []backend.BoardQuery{
		{Epic: &hiddenEpic.ID},
		{Epic: &hiddenEpic.ID, LaneLimit: &zero},
		{Epic: &hiddenEpic.ID, LaneOffset: &one},
	} {
		_, err = root.WithUser("alice").GetBoard(ctx, "default", query)
		notFound(t, err, "pagination must not bypass explicit epic validation")
	}
}

func TestBoardAppliesServerSidePageBoundsWhenLimitsAreOmitted(t *testing.T) {
	root, ctx := newInstance(t)
	for i := range 51 {
		_, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: fmt.Sprintf("issue %02d", i)})
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

func TestDeletingASelectedProjectMovesTheBoardViewETag(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", false, false)
	grant(t, root, ctx, "web", "alice", domain.AccessRegular)
	alice := root.WithUser("alice")
	view, err := alice.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Web", AllProjects: false,
		Projects: []string{"web"}, PriorityMax: 4})
	require.NoError(t, err)

	_, err = root.DeleteProject(ctx, "web", false, "")
	require.NoError(t, err)
	updated, err := alice.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Empty(t, updated.Projects)
	assert.Greater(t, updated.UpdatedAt, view.UpdatedAt)

	name := "stale edit"
	_, err = alice.UpdateBoardView(ctx, view.ID, backend.BoardViewPatch{Name: &name}, backend.ETag(view.UpdatedAt))
	require.Error(t, err)
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed, "the pre-deletion ETag is stale")
}

func TestArchivedWorkspaceSelectionIsDormantAndRestored(t *testing.T) {
	root, ctx := newInstance(t)
	epic, err := root.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "Dormant epic", Type: domain.TypeEpic})
	require.NoError(t, err)
	view, err := root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Current work", AllProjects: false,
		Projects: []string{"awb"}, PriorityMax: 4})
	require.NoError(t, err)
	workspace, err := root.GetProject(ctx, "awb")
	require.NoError(t, err)
	archived, err := root.ArchiveProject(ctx, "awb", backend.ETag(workspace.UpdatedAt))
	require.NoError(t, err)

	hidden, err := root.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Empty(t, hidden.Projects, "archived workspaces are omitted from normal board metadata")
	board, err := root.GetBoard(ctx, view.ID, backend.BoardQuery{})
	require.NoError(t, err)
	assert.True(t, board.ProjectsOmitted)
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
	assert.Empty(t, updated.Projects)
	_, err = root.CreateBoardView(ctx, backend.BoardViewCreate{Name: "Archived", Projects: []string{"awb"}, PriorityMax: 4})
	notFound(t, err, "an archived workspace cannot be newly selected")

	_, err = root.RestoreProject(ctx, "awb", backend.ETag(archived.UpdatedAt))
	require.NoError(t, err)
	restored, err := root.GetBoardView(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"awb"}, restored.Projects)
}
