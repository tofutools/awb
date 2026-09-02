package remote_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/remote"
)

func TestBoardLifecycleAndPagingUseTheRemoteWireContract(t *testing.T) {
	const (
		id   = "view-aaaaaaaaaaaaaaaaaaaaaaaa"
		view = `{"id":"view-aaaaaaaaaaaaaaaaaaaaaaaa"}`
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/board-views":
			_, _ = w.Write([]byte("[" + view + "]"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-views":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.JSONEq(t, `{"name":"Release","shared":true,"all_workspaces":false,"workspaces":["awb"],"all_epics":true,"epics":null,"include_no_epic":true,"labels":null,"assignees":null,"priority_max":4,"closed_days":30}`, string(body))
			_, _ = w.Write([]byte(view))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/board-views/"+id:
			assert.Equal(t, `"etag"`, r.Header.Get("If-Match"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.JSONEq(t, `{"name":"Release"}`, string(body))
			_, _ = w.Write([]byte(view))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/board-views/"+id:
			assert.Equal(t, `"etag"`, r.Header.Get("If-Match"))
			_, _ = w.Write([]byte(view))
		case r.Method == http.MethodGet && r.URL.Path == "/api/boards/"+id:
			assert.Equal(t, []string{"awb", "web"}, r.URL.Query()["workspace"])
			assert.Equal(t, "false", r.URL.Query().Get("all-workspaces"))
			assert.Equal(t, []string{"awb-hidden", "web-hidden"}, r.URL.Query()["hidden-epic"])
			assert.Equal(t, "false", r.URL.Query().Get("all-epics"))
			assert.Equal(t, []string{"awb-selected"}, r.URL.Query()["selected-epic"])
			assert.Equal(t, "false", r.URL.Query().Get("include-no-epic"))
			assert.Equal(t, []string{"release", "frontend"}, r.URL.Query()["label"])
			assert.Equal(t, []string{"alex", "sam"}, r.URL.Query()["assignee"])
			assert.Equal(t, "2", r.URL.Query().Get("priority-max"))
			assert.Equal(t, "7", r.URL.Query().Get("lane-limit"))
			assert.Equal(t, "3", r.URL.Query().Get("card-offset"))
			assert.Equal(t, "14", r.URL.Query().Get("closed-days"))
			assert.Equal(t, "open", r.URL.Query().Get("status"))
			assert.Equal(t, "awb-epic", r.URL.Query().Get("epic"))
			_, _ = w.Write([]byte(`{"lanes":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues/awb-123/move":
			assert.Equal(t, `"issue-etag"`, r.Header.Get("If-Match"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.JSONEq(t, `{"status":"open","epic":"awb-epic","direction":"earlier"}`, string(body))
			_, _ = w.Write([]byte(`{"id":"awb-123"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	base, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := remote.New(base, "", "", "alice")
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	views, err := client.ListBoardViews(t.Context())
	require.NoError(t, err)
	require.Len(t, views, 1)
	created, err := client.CreateBoardView(t.Context(), backend.BoardViewCreate{Name: "Release", Shared: true,
		AllWorkspaces: false, Workspaces: []string{"awb"}, AllEpics: true, IncludeNoEpic: true, PriorityMax: 4,
		ClosedDays: 30})
	require.NoError(t, err)
	name := "Release"
	_, err = client.UpdateBoardView(t.Context(), id, backend.BoardViewPatch{Name: &name}, `"etag"`)
	require.NoError(t, err)
	_, err = client.DeleteBoardView(t.Context(), id, `"etag"`)
	require.NoError(t, err)
	assert.Equal(t, id, created.ID)

	laneLimit, cardOffset, closedDays, priorityMax, epic := 7, 3, 14, 2, "awb-epic"
	allWorkspaces, allEpics, includeNoEpic := false, false, false
	_, err = client.GetBoard(t.Context(), id, backend.BoardQuery{LaneLimit: &laneLimit, CardOffset: &cardOffset,
		ClosedDays: &closedDays, Workspaces: []string{"awb", "web"}, HiddenEpics: []string{"awb-hidden", "web-hidden"},
		AllWorkspaces: &allWorkspaces, AllEpics: &allEpics, Epics: []string{"awb-selected"}, IncludeNoEpic: &includeNoEpic,
		Labels: []string{"release", "frontend"}, Assignees: []string{"alex", "sam"}, PriorityMax: &priorityMax,
		Status: domain.StatusOpen, Epic: &epic})
	require.NoError(t, err)
	_, err = client.MoveIssue(t.Context(), "awb-123", backend.IssueMove{
		Status: domain.StatusOpen, Epic: &epic, Direction: "earlier",
	}, `"issue-etag"`)
	require.NoError(t, err)
}
