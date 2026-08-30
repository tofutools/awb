package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/openapi"
	"github.com/tofutools/awb/internal/storage"
)

// dump is entirely a client operation: an existing server only sees its
// ordinary paginated reads and attachment downloads. The restored database
// preserves the stored shapes rather than recreating them through mutations,
// which would mint new IDs and timestamps.
func TestDumpDownloadsAnExistingServerIntoLocalFiles(t *testing.T) {
	handler, source := newServeHandlerOn(t, serveOptions{
		addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb",
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	ctx := t.Context()
	_, err := source.CreateProject(ctx, backend.ProjectCreate{
		Key: "awb", Name: "Agent Work Board", Description: "The tracker",
	})
	require.NoError(t, err)
	_, err = source.CreateProject(ctx, backend.ProjectCreate{Key: "web", Name: "Web"})
	require.NoError(t, err)

	created := make([]*domain.Issue, 101)
	for i := range created {
		project := "awb"
		if i%2 != 0 {
			project = "web"
		}
		created[i], err = source.CreateIssue(ctx, backend.IssueCreate{
			Project: project, Title: fmt.Sprintf("Issue %03d", i),
			Description: "See [the design](https://example.com/design).",
			Type:        domain.TypeTask, Labels: []string{"dump"},
		})
		require.NoError(t, err)
	}
	reason := "verified"
	_, err = source.CloseIssue(ctx, created[1].ID, backend.CloseRequest{Reason: &reason}, "")
	require.NoError(t, err)
	_, err = source.Claim(ctx, created[2].ID, backend.ClaimRequest{Assignee: "alice"}, "")
	require.NoError(t, err)
	_, err = source.AddRelation(ctx, created[0].ID, backend.RelationRequest{
		Type: domain.RelBlockedBy, Other: created[1].ID,
	}, "")
	require.NoError(t, err)
	attachmentContent := "the bytes in the attachment\n"
	attachment, err := source.AddAttachment(ctx, created[0].ID, backend.AttachmentCreate{
		Name: "notes.txt", ContentType: "text/plain; charset=utf-8",
		Content: strings.NewReader(attachmentContent),
	})
	require.NoError(t, err)

	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("AWB_DB", server.URL)
	t.Setenv("AWB_IDENTITY", "mikael")
	for _, name := range []string{"AWB_USER", "AWB_PASSWORD", "AWB_PROJECT", "AWB_CONFIG_FILE"} {
		t.Setenv(name, "")
	}
	outputDB := filepath.Join(root, "copy", "awb.db")
	outputAttachments := filepath.Join(root, "copy", "attachments")
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	code := Execute(ctx, "test", openapi.New(raw), []string{
		"dump", "--output-db", outputDB, "--output-attachments", outputAttachments,
	}, &stdout, &stderr, strings.NewReader(""))
	require.Equal(t, 0, code, stderr.String())
	assert.Empty(t, stdout.String())

	db, err := storage.Open(ctx, outputDB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	restored := local.New(db, storage.NewBlobs(outputAttachments), "local")

	wantProjects, err := source.ListProjects(ctx, nil, nil)
	require.NoError(t, err)
	gotProjects, err := restored.ListProjects(ctx, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, wantProjects, gotProjects)

	filter := &domain.Filter{IncludeClosed: true, Sort: domain.Sort{Key: domain.SortID}}
	wantIssues, err := source.ListIssues(ctx, filter)
	require.NoError(t, err)
	gotIssues, err := restored.ListIssues(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, wantIssues, gotIssues)

	gotAttachment, content, err := restored.OpenAttachment(ctx, attachment.Issue, attachment.Name)
	require.NoError(t, err)
	defer content.Close() //nolint:errcheck
	gotContent, err := io.ReadAll(content)
	require.NoError(t, err)
	assert.Equal(t, attachment, gotAttachment)
	assert.Equal(t, attachmentContent, string(gotContent))

	users, err := restored.ListUsers(ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, users.Users)
}

func TestDumpOverwritePublishesOnlyACompletedReplacement(t *testing.T) {
	handler, source := newServeHandlerOn(t, serveOptions{
		addr: "127.0.0.1", port: 7777, basicAuthRealm: "awb",
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	ctx := t.Context()
	_, err := source.CreateProject(ctx, backend.ProjectCreate{Key: "awb"})
	require.NoError(t, err)
	first, err := source.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "First"})
	require.NoError(t, err)
	_, err = source.AddAttachment(ctx, first.ID, backend.AttachmentCreate{
		Name: "first.txt", Content: strings.NewReader("first"),
	})
	require.NoError(t, err)

	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("AWB_DB", server.URL)
	t.Setenv("AWB_IDENTITY", "mikael")
	for _, name := range []string{"AWB_USER", "AWB_PASSWORD", "AWB_PROJECT", "AWB_CONFIG_FILE"} {
		t.Setenv(name, "")
	}
	outputDB := filepath.Join(root, "copy", "awb.db")
	outputAttachments := filepath.Join(root, "copy", "attachments")
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	run := func(extra ...string) (string, int) {
		t.Helper()
		args := []string{"dump", "--output-db", outputDB,
			"--output-attachments", outputAttachments}
		args = append(args, extra...)
		var stdout, stderr bytes.Buffer
		code := Execute(ctx, "test", openapi.New(raw), args,
			&stdout, &stderr, strings.NewReader(""))
		return stderr.String(), code
	}

	errOut, code := run()
	require.Equal(t, 0, code, errOut)
	second, err := source.CreateIssue(ctx, backend.IssueCreate{Project: "awb", Title: "Second"})
	require.NoError(t, err)
	secondAttachment, err := source.AddAttachment(ctx, second.ID, backend.AttachmentCreate{
		Name: "second.txt", Content: strings.NewReader("second"),
	})
	require.NoError(t, err)

	errOut, code = run("--force")
	require.Equal(t, 0, code, errOut)
	db, err := storage.Open(ctx, outputDB)
	require.NoError(t, err)
	restored := local.New(db, storage.NewBlobs(outputAttachments), "local")
	page, err := restored.ListIssues(ctx, &domain.Filter{IncludeClosed: true})
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	require.NoError(t, restored.Close())
	secondBytes, err := os.ReadFile(filepath.Join(outputAttachments, secondAttachment.Sha256))
	require.NoError(t, err)
	assert.Equal(t, "second", string(secondBytes))

	// Once the server is unavailable, staging fails. The successfully published
	// pair remains byte-for-byte unchanged rather than being removed first.
	databaseBefore, err := os.ReadFile(outputDB)
	require.NoError(t, err)
	server.Close()
	errOut, code = run("--force")
	assert.Equal(t, 1, code, errOut)
	databaseAfter, err := os.ReadFile(outputDB)
	require.NoError(t, err)
	assert.Equal(t, databaseBefore, databaseAfter)
	secondBytesAfter, err := os.ReadFile(filepath.Join(outputAttachments, secondAttachment.Sha256))
	require.NoError(t, err)
	assert.Equal(t, secondBytes, secondBytesAfter)
}

func TestDumpOverwriteRefusesLocalSourceOverlap(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	sourceDB := filepath.Join(sourceDir, "awb.db")
	sourceAttachments := filepath.Join(sourceDir, "attachments")
	db, err := storage.Init(t.Context(), sourceDB)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, storage.NewBlobs(sourceAttachments).Create())

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("AWB_DB", sourceDB)
	t.Setenv("AWB_ATTACHMENTS", sourceAttachments)
	t.Setenv("AWB_IDENTITY", "mikael")
	t.Setenv("AWB_CONFIG_FILE", "")
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	run := func(outputDB, outputAttachments string) (string, int) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Execute(t.Context(), "test", openapi.New(raw), []string{
			"dump", "--force", "--output-db", outputDB,
			"--output-attachments", outputAttachments,
		}, &stdout, &stderr, strings.NewReader(""))
		return stderr.String(), code
	}

	for _, tt := range []struct {
		name        string
		attachments string
	}{
		{name: "source attachment directory", attachments: sourceAttachments},
		{name: "ancestor of source database", attachments: sourceDir},
	} {
		t.Run(tt.name, func(t *testing.T) {
			errOut, code := run(filepath.Join(root, tt.name+".db"), tt.attachments)
			assert.Equal(t, 2, code)
			assert.Contains(t, errOut, "must not overlap the local database or attachment directory")
			_, err := os.Stat(sourceDB)
			assert.NoError(t, err)
			info, err := os.Stat(sourceAttachments)
			require.NoError(t, err)
			assert.True(t, info.IsDir())
		})
	}
}

func TestPublishDumpRechecksSQLiteSidecars(t *testing.T) {
	root := t.TempDir()
	stagedDB := filepath.Join(root, "staged.db")
	stagedAttachments := filepath.Join(root, "staged-attachments")
	outputDB := filepath.Join(root, "output.db")
	outputAttachments := filepath.Join(root, "output-attachments")
	require.NoError(t, os.WriteFile(stagedDB, []byte("new"), 0o600))
	require.NoError(t, os.Mkdir(stagedAttachments, 0o700))
	require.NoError(t, os.WriteFile(outputDB, []byte("old"), 0o600))
	require.NoError(t, os.Mkdir(outputAttachments, 0o700))
	// This models a server opening the destination after overwrite staging began.
	require.NoError(t, os.WriteFile(outputDB+"-wal", []byte("active"), 0o600))

	err := publishDump(stagedDB, stagedAttachments, outputDB, outputAttachments)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop the local server first")
	contents, err := os.ReadFile(outputDB)
	require.NoError(t, err)
	assert.Equal(t, "old", string(contents))
	_, err = os.Stat(stagedDB)
	assert.NoError(t, err, "publication stopped before moving the completed replacement")
}

func TestDumpRefusesExistingOutputsAndUnimplementedUsers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("AWB_DB", "https://awb.example.com")
	t.Setenv("AWB_CONFIG_FILE", "")
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)

	run := func(args ...string) (string, int) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Execute(t.Context(), "test", openapi.New(raw), args,
			&stdout, &stderr, strings.NewReader(""))
		return stderr.String(), code
	}

	db := filepath.Join(root, "dump.db")
	attachments := filepath.Join(root, "attachments")
	errOut, code := run("dump", "--overwrite", "--output-db", db,
		"--output-attachments", attachments)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "unknown flag")

	errOut, code = run("dump", "--output-db", db, "--output-attachments", attachments,
		"--include-users")
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "--include-users is not implemented")

	require.NoError(t, os.WriteFile(db, []byte("mine"), 0o600))
	errOut, code = run("dump", "--output-db", db, "--output-attachments", attachments)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "output already exists")
	contents, err := os.ReadFile(db)
	require.NoError(t, err)
	assert.Equal(t, "mine", string(contents))
	require.NoError(t, os.WriteFile(db+"-wal", []byte("active"), 0o600))
	errOut, code = run("dump", "--output-db", db, "--output-attachments", attachments,
		"--force")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "stop the local server first")
	require.NoError(t, os.Remove(db+"-wal"))

	physical := filepath.Join(root, "physical")
	require.NoError(t, os.Mkdir(physical, 0o700))
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(physical, alias))
	errOut, code = run("dump", "--force", "--output-db", filepath.Join(physical, "dump.db"),
		"--output-attachments", alias)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "must not contain one another")
	_, err = os.Stat(filepath.Join(physical, "dump.db"))
	assert.ErrorIs(t, err, os.ErrNotExist)

	nestedDB := filepath.Join(root, "nested.db")
	errOut, code = run("dump", "--output-db", nestedDB,
		"--output-attachments", filepath.Join(nestedDB, "attachments"))
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "must not contain one another")
	_, err = os.Lstat(nestedDB)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
