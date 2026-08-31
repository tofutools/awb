package local_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	visible, err := bob.GetBoardView(ctx, shared.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"awb"}, visible.Projects, "view metadata cannot disclose an inaccessible key")

	board, err := bob.GetBoard(ctx, shared.ID, backend.BoardQuery{})
	require.NoError(t, err)
	require.Len(t, board.Lanes, 1)
	assert.Equal(t, "awb", board.Lanes[0].Project.Key)
	assert.True(t, board.ProjectsOmitted)
	_, err = bob.UpdateBoardView(ctx, shared.ID, backend.BoardViewPatch{}, "")
	forbidden(t, err)
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
