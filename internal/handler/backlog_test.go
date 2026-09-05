package handler_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/remote"
)

// Exercise the same contract through direct mode and the real generated HTTP server.
func TestBacklogWorkflowAndBoardParity(t *testing.T) {
	for _, mode := range []string{"direct", "remote"} {
		t.Run(mode, func(t *testing.T) {
			a := newAPI(t)
			var be backend.Backend = a.be
			if mode == "remote" {
				u, err := url.Parse(a.server.URL)
				require.NoError(t, err)
				be = remote.New(u, "", "", "mikael")
				t.Cleanup(func() { _ = be.Close() })
			}
			ctx := t.Context()
			create := func(title string, typ domain.Type, backlog bool, parent string) *domain.Issue {
				req := backend.IssueCreate{Workspace: "awb", Title: title, Type: typ, Backlog: backlog}
				if parent != "" {
					req.Relations = []backend.NewRelation{{Type: domain.RelHasParent, Other: parent}}
				}
				issue, err := be.CreateIssue(ctx, req)
				require.NoError(t, err)
				return issue
			}
			epic := create("Future", domain.TypeEpic, true, "")
			child := create("Child", domain.TypeTask, false, epic.ID)
			grandchild := create("Grandchild", domain.TypeBug, false, child.ID)
			parked := create("Parked", domain.TypeFeature, true, "")
			active := create("Active", domain.TypeChore, false, "")
			_, err := be.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "ambiguous", Backlog: true, Assignees: []string{"mikael"}})
			require.Error(t, err)
			rows, err := be.ListIssues(ctx, &domain.Filter{Statuses: []domain.Status{domain.StatusBacklog}})
			require.NoError(t, err)
			assert.Len(t, rows.Issues, 2)
			ready := func() []string {
				rows, err := be.ListIssues(ctx, &domain.Filter{Statuses: []domain.Status{domain.StatusOpen}, Readiness: domain.ReadinessReady, Unassigned: true})
				require.NoError(t, err)
				ids := []string{}
				for _, i := range rows.Issues {
					ids = append(ids, i.ID)
				}
				return ids
			}
			assert.Equal(t, []string{active.ID}, ready())
			board, err := be.GetBoard(ctx, "default", backend.BoardQuery{})
			require.NoError(t, err)
			require.Len(t, board.Lanes, 1)
			require.Len(t, board.Lanes[0].Columns, 3)
			assert.Equal(t, 1, board.Lanes[0].Columns[0].Total)
			_, err = be.GetBoard(ctx, "default", backend.BoardQuery{Epic: &epic.ID})
			require.Error(t, err)
			board, err = be.GetBoard(ctx, "default", backend.BoardQuery{IncludeBacklog: true})
			require.NoError(t, err)
			require.Len(t, board.Lanes, 2)
			assert.Len(t, board.Lanes[0].Columns, 4)
			ids := []string{}
			for _, lane := range board.Lanes {
				for _, column := range lane.Columns {
					for _, issue := range column.Issues {
						ids = append(ids, issue.ID)
					}
				}
			}
			assert.ElementsMatch(t, []string{child.ID, grandchild.ID, parked.ID, active.ID}, ids)
			issue, err := be.Release(ctx, parked.ID, backend.ReleaseRequest{Assignee: "mikael"}, "")
			require.NoError(t, err)
			assert.Equal(t, domain.StatusBacklog, issue.Status)
			issue, err = be.Claim(ctx, parked.ID, backend.ClaimRequest{Assignee: "mikael"}, "")
			require.NoError(t, err)
			assert.Equal(t, domain.StatusInProgress, issue.Status)
			issue, err = be.MoveIssue(ctx, parked.ID, backend.IssueMove{Status: domain.StatusBacklog}, "")
			require.NoError(t, err)
			assert.Empty(t, issue.Assignees)
			issue, err = be.CloseIssue(ctx, parked.ID, backend.CloseRequest{}, "")
			require.NoError(t, err)
			assert.NotEmpty(t, issue.ClosedAt)
			issue, err = be.MoveIssue(ctx, parked.ID, backend.IssueMove{Status: domain.StatusBacklog}, "")
			require.NoError(t, err)
			assert.Empty(t, issue.ClosedAt)
			issue, err = be.Reopen(ctx, epic.ID, "")
			require.NoError(t, err)
			assert.Equal(t, domain.StatusOpen, issue.Status)
			assert.Contains(t, ready(), grandchild.ID)
			issue, err = be.MoveIssue(ctx, epic.ID, backend.IssueMove{Status: domain.StatusBacklog}, "")
			require.NoError(t, err)
			assert.Equal(t, domain.StatusBacklog, issue.Status)
			assert.NotContains(t, ready(), child.ID)
		})
	}
}
