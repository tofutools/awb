package local_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

func TestIssueActivityRecordsCommentsAndChanges(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "old title")

	created, err := b.ListActivity(ctx, issue.ID, "", nil, nil)
	require.NoError(t, err)
	require.Len(t, created.Activity, 1)
	assert.Equal(t, domain.ActivityKindChange, created.Activity[0].Kind)
	assert.Equal(t, "created", created.Activity[0].Action)
	assert.Equal(t, "mikael", created.Activity[0].Actor)

	body := "A **Markdown** comment.\n"
	comment, err := b.AddComment(ctx, issue.ID, body)
	require.NoError(t, err)
	assert.Equal(t, body, comment.Body, "comment bytes round-trip unchanged")
	assert.Equal(t, "mikael", comment.Actor)
	commented, err := b.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Greater(t, commented.UpdatedAt, issue.UpdatedAt,
		"a comment counts as new issue activity")

	title := "new title"
	_, err = b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title}, "")
	require.NoError(t, err)

	page, err := b.ListActivity(ctx, issue.ID, "", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Activity, 3)
	assert.Equal(t, "updated", page.Activity[0].Action)
	assert.Equal(t, domain.ActivityKindComment, page.Activity[1].Kind)
	require.Equal(t, []domain.ActivityChange{{
		Field: "title", From: json.RawMessage(`"old title"`), To: json.RawMessage(`"new title"`),
	}}, page.Activity[0].Changes)
	assert.Greater(t, page.Activity[0].ID, page.Activity[1].ID)
}

func TestCommentMovesIssueInUpdatedOrder(t *testing.T) {
	b, ctx := newBackend(t)
	commented := create(t, b, ctx, "commented")
	other := create(t, b, ctx, "other")

	// A second comment guarantees this issue's per-row timestamp has advanced
	// past another issue created in the same clock tick.
	_, err := b.AddComment(ctx, commented.ID, "first")
	require.NoError(t, err)
	_, err = b.AddComment(ctx, commented.ID, "second")
	require.NoError(t, err)

	page, err := b.ListIssues(ctx, &domain.Filter{
		Sort: domain.Sort{Key: domain.SortUpdated, Desc: true},
	})
	require.NoError(t, err)
	require.Len(t, page.Issues, 2)
	assert.Equal(t, commented.ID, page.Issues[0].ID)
	assert.Equal(t, other.ID, page.Issues[1].ID)
}

func TestNoOpAndFailedMutationsProduceNoActivity(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "title")

	title := issue.Title
	_, err := b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title}, "")
	require.NoError(t, err)
	badPriority := 9
	_, err = b.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Priority: &badPriority}, "")
	require.Error(t, err)
	_, err = b.AddComment(ctx, issue.ID, " \n\t")
	require.Error(t, err)

	page, err := b.ListActivity(ctx, issue.ID, "", nil, nil)
	require.NoError(t, err)
	assert.Len(t, page.Activity, 1, "only the creation event remains")
}

func TestActivityKindAndPaging(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "title")
	_, err := b.AddComment(ctx, issue.ID, "first")
	require.NoError(t, err)
	_, err = b.AddComment(ctx, issue.ID, "second")
	require.NoError(t, err)

	limit, offset := 1, 1
	page, err := b.ListActivity(ctx, issue.ID, domain.ActivityKindComment, &limit, &offset)
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	require.Len(t, page.Activity, 1)
	assert.Equal(t, "first", page.Activity[0].Body)
}

func TestAttachmentActivityNamesWhatChanged(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "title")
	_, err := b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name: "evidence.txt", Content: strings.NewReader("evidence"),
	})
	require.NoError(t, err)

	page, err := b.ListActivity(ctx, issue.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Activity, 2)
	assert.Equal(t, "attachment_added", page.Activity[0].Action)
	require.Len(t, page.Activity[0].Changes, 1)
	assert.Equal(t, "attachment", page.Activity[0].Changes[0].Field)
	assert.Contains(t, string(page.Activity[0].Changes[0].To), `"name":"evidence.txt"`)
}

func TestRelationActivityIsRecordedOnEveryChangedEndpoint(t *testing.T) {
	b, ctx := newBackend(t)
	child := create(t, b, ctx, "child")
	firstParent := create(t, b, ctx, "first parent")
	secondParent := create(t, b, ctx, "second parent")

	_, err := b.AddRelation(ctx, child.ID, backend.RelationRequest{
		Type: domain.RelHasParent, Other: firstParent.ID,
	}, "")
	require.NoError(t, err)

	childActivity, err := b.ListActivity(ctx, child.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	firstActivity, err := b.ListActivity(ctx, firstParent.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "relation_added", childActivity.Activity[0].Action)
	assert.Equal(t, "relation_added", firstActivity.Activity[0].Action)

	_, err = b.AddRelation(ctx, child.ID, backend.RelationRequest{
		Type: domain.RelHasParent, Other: secondParent.ID, Force: true,
	}, "")
	require.NoError(t, err)

	firstActivity, err = b.ListActivity(ctx, firstParent.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	secondActivity, err := b.ListActivity(ctx, secondParent.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "relation_added", firstActivity.Activity[0].Action,
		"the replaced parent records losing the child")
	assert.Equal(t, "relation_added", secondActivity.Activity[0].Action,
		"the new parent records gaining the child")
	assert.Contains(t, string(firstActivity.Activity[0].Changes[0].From), child.ID)
	assert.NotContains(t, string(firstActivity.Activity[0].Changes[0].To), child.ID)

	_, err = b.RemoveRelation(ctx, child.ID, domain.RelHasParent, secondParent.ID, "")
	require.NoError(t, err)
	childActivity, err = b.ListActivity(ctx, child.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	secondActivity, err = b.ListActivity(ctx, secondParent.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "relation_removed", childActivity.Activity[0].Action)
	assert.Equal(t, "relation_removed", secondActivity.Activity[0].Action)
}

func TestCreateAndDeleteRecordRelationActivityOnSurvivingEndpoints(t *testing.T) {
	b, ctx := newBackend(t)
	parent := create(t, b, ctx, "parent")

	child, err := b.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: parent.ID}},
	})
	require.NoError(t, err)
	parentActivity, err := b.ListActivity(ctx, parent.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "relation_added", parentActivity.Activity[0].Action)
	assert.Contains(t, string(parentActivity.Activity[0].Changes[0].To), child.ID)

	_, err = b.DeleteIssue(ctx, child.ID, "")
	require.NoError(t, err)
	parentActivity, err = b.ListActivity(ctx, parent.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "relation_removed", parentActivity.Activity[0].Action)
	assert.Contains(t, string(parentActivity.Activity[0].Changes[0].From), child.ID)
	assert.NotContains(t, string(parentActivity.Activity[0].Changes[0].To), child.ID)
}
