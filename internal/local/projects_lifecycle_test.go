package local_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

func TestArchivedProjectIsRetainedReadOnlyAndRestorable(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "retained")
	_, err := b.CreateProject(ctx, backend.ProjectCreate{Key: "web"})
	require.NoError(t, err)
	other, err := b.CreateIssue(ctx, backend.IssueCreate{Project: "web", Title: "linked"})
	require.NoError(t, err)
	_, err = b.AddRelation(ctx, other.ID, backend.RelationRequest{Type: domain.RelRelated, Other: issue.ID}, "")
	require.NoError(t, err)
	before, err := b.GetProject(ctx, "awb")
	require.NoError(t, err)

	archived, err := b.ArchiveProject(ctx, "awb", backend.ETag(before.UpdatedAt))
	require.NoError(t, err)
	assert.Equal(t, domain.ProjectArchived, archived.State)
	assert.NotEmpty(t, archived.ArchivedAt)
	assert.Equal(t, "mikael", archived.ArchivedBy)

	active, err := b.ListProjects(ctx, "", domain.DefaultProjectSort, nil, nil)
	require.NoError(t, err)
	require.Len(t, active.Projects, 1)
	assert.Equal(t, "web", active.Projects[0].Key)
	history, err := b.ListProjectsByState(ctx, "", domain.ProjectsArchived, domain.DefaultProjectSort, nil, nil)
	require.NoError(t, err)
	require.Len(t, history.Projects, 1)

	got, err := b.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, issue.ID, got.ID, "stable direct reads remain available")
	_, err = b.AddComment(ctx, issue.ID, "must not change")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
	_, err = b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{Name: "note.txt", Content: strings.NewReader("x")})
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
	_, err = b.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "must not exist"})
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
	_, err = b.DeleteProject(ctx, "awb", true, "")
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err))
	_, err = b.DeleteProject(ctx, "web", true, "")
	require.Error(t, err, "cascade cannot rewrite an archived counterpart's historical graph")
	assert.Equal(t, 4, exitOf(err))

	// The fresh version makes a repeated archive idempotent; stale state is
	// still refused before idempotence can conceal a concurrent transition.
	_, err = b.ArchiveProject(ctx, "awb", backend.ETag(archived.UpdatedAt))
	require.NoError(t, err)
	_, err = b.ArchiveProject(ctx, "awb", backend.ETag(before.UpdatedAt))
	require.Error(t, err)
	assert.ErrorIs(t, err, awberr.ErrPreconditionFailed)

	restored, err := b.RestoreProject(ctx, "awb", backend.ETag(archived.UpdatedAt))
	require.NoError(t, err)
	assert.Equal(t, domain.ProjectActive, restored.State)
	assert.Empty(t, restored.ArchivedAt)
	_, err = b.AddComment(ctx, issue.ID, "work resumes")
	require.NoError(t, err)

	audit, err := b.ListProjectActivity(ctx, "awb", nil, nil)
	require.NoError(t, err)
	require.Len(t, audit.Activity, 2)
	assert.Equal(t, "restored", audit.Activity[0].Action)
	assert.Equal(t, "archived", audit.Activity[1].Action)
}
