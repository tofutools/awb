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
