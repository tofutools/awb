package local_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/storage"
)

// newBackendWithBlobs is newBackend with the attachments directory named, so a
// test can look at the files as well as at the rows.
func newBackendWithBlobs(t *testing.T) (*local.Backend, context.Context, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	blobs := filepath.Join(dir, "attachments")
	b := local.New(db, storage.NewBlobs(blobs), "mikael")
	_, err = b.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: "awb"})
	require.NoError(t, err)
	return b, t.Context(), blobs
}

func attach(t *testing.T, b *local.Backend, ctx context.Context, issueRef, name,
	content string) *domain.Attachment {
	t.Helper()
	attachment, err := b.AddAttachment(ctx, issueRef, backend.AttachmentCreate{
		Name:    name,
		Content: strings.NewReader(content),
	})
	require.NoError(t, err)
	return attachment
}

// blobFiles is what the attachments directory holds, staging files included so
// that a leftover one would show up.
func blobFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// The metadata is derived from the content: its size, its digest, and — with
// no content type given — what the first bytes say it is.
func TestAddAttachment(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")
	assert.Equal(t, issue.ID, attachment.Issue)
	assert.Equal(t, "trace.txt", attachment.Name)
	assert.Equal(t, "text/plain; charset=utf-8", attachment.ContentType)
	assert.EqualValues(t, 5, attachment.Size)
	// The digest is the SHA-256 of the content and nothing else.
	sum := sha256.Sum256([]byte("boom\n"))
	assert.Equal(t, hex.EncodeToString(sum[:]), attachment.Sha256)
	assert.NotEmpty(t, attachment.CreatedAt)

	// The content is one file in the attachments directory, named by its digest,
	// and nothing is left staged.
	assert.Equal(t, []string{attachment.Sha256}, blobFiles(t, dir))

	stored, err := os.ReadFile(filepath.Join(dir, attachment.Sha256))
	require.NoError(t, err)
	assert.Equal(t, "boom\n", string(stored))
}

// A stated content type is stored exactly as it arrived rather than sniffed
// over.
func TestAddAttachmentWithAStatedContentType(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	attachment, err := b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name:        "notes.md",
		ContentType: "text/markdown; charset=utf-8",
		Content:     strings.NewReader("# Notes\n"),
	})
	require.NoError(t, err)
	assert.Equal(t, "text/markdown; charset=utf-8", attachment.ContentType)
}

// The issue carries its attachments as a derived array, oldest first, exactly
// as the listing does.
func TestIssueCarriesItsAttachments(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	first := attach(t, b, ctx, issue.ID, "one.txt", "1")
	second := attach(t, b, ctx, issue.ID, "two.txt", "2")

	read, err := b.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	require.Len(t, read.Attachments, 2)

	// The order is created_at then name. Two uploads within one millisecond of
	// the clock share a timestamp, so what is pinned here is that the order is
	// total and the same everywhere, not that it is the upload order.
	want := []domain.Attachment{*first, *second}
	domain.SortAttachments(want)
	assert.Equal(t, want, read.Attachments)

	page, err := b.ListAttachments(ctx, issue.ID, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, page.Total)
	assert.Equal(t, read.Attachments, page.Attachments,
		"the listing and the issue's array are the same array")
}

// An issue with none carries an empty array rather than a nil one.
func TestAnIssueWithNoAttachmentsCarriesAnEmptyArray(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	require.NotNil(t, issue.Attachments)
	assert.Empty(t, issue.Attachments)
}

// Adding and removing attachments are issue changes. Each one moves updated_at
// strictly forward, even when several versions land inside one clock tick.
func TestAttachmentsMoveIssueUpdatedAt(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	previous := issue.UpdatedAt
	for i := range 10 {
		name := fmt.Sprintf("trace-%d.txt", i)
		attachment := attach(t, b, ctx, issue.ID, name, "boom\n")
		read, err := b.GetIssue(ctx, issue.ID)
		require.NoError(t, err)
		assert.Greater(t, read.UpdatedAt, previous)
		previous = read.UpdatedAt

		_, err = b.DeleteAttachment(ctx, issue.ID, attachment.Name)
		require.NoError(t, err)
		read, err = b.GetIssue(ctx, issue.ID)
		require.NoError(t, err)
		assert.Greater(t, read.UpdatedAt, previous)
		previous = read.UpdatedAt
	}
}

func TestAttachmentMovesIssueInUpdatedOrder(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	attached := create(t, b, ctx, "attached")
	other := create(t, b, ctx, "other")

	// Two attachment changes guarantee this issue's per-row timestamp has
	// advanced past another issue created in the same clock tick.
	attachment := attach(t, b, ctx, attached.ID, "trace.txt", "boom\n")
	_, err := b.DeleteAttachment(ctx, attached.ID, attachment.Name)
	require.NoError(t, err)

	page, err := b.ListIssues(ctx, &domain.Filter{
		Sort: domain.Sort{Key: domain.SortUpdated, Desc: true},
	})
	require.NoError(t, err)
	require.Len(t, page.Issues, 2)
	assert.Equal(t, attached.ID, page.Issues[0].ID)
	assert.Equal(t, other.ID, page.Issues[1].ID)
}

// Two attachments of the same bytes are two attachments sharing one stored
// file, and removing one leaves the other's content where it is.
func TestIdenticalContentIsStoredOnce(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	other := create(t, b, ctx, "Tokeniser drops a newline")

	first := attach(t, b, ctx, issue.ID, "one.txt", "same bytes")
	second := attach(t, b, ctx, other.ID, "two.txt", "same bytes")

	assert.Equal(t, first.Sha256, second.Sha256)
	assert.Equal(t, []string{first.Sha256}, blobFiles(t, dir), "one file for both")

	_, err := b.DeleteAttachment(ctx, issue.ID, first.Name)
	require.NoError(t, err)
	assert.Equal(t, []string{second.Sha256}, blobFiles(t, dir),
		"the other attachment still holds the content")

	_, content, err := b.OpenAttachment(ctx, other.ID, second.Name)
	require.NoError(t, err)
	defer content.Close() //nolint:errcheck // read to its end below
	data, err := io.ReadAll(content)
	require.NoError(t, err)
	assert.Equal(t, "same bytes", string(data))
}

// The last attachment holding a digest takes the file with it.
func TestDeletingTheLastAttachmentRemovesTheContent(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	deleted, err := b.DeleteAttachment(ctx, issue.ID, attachment.Name)
	require.NoError(t, err)
	assert.Equal(t, attachment, deleted, "the object as it was before deletion")
	assert.Empty(t, blobFiles(t, dir))

	_, err = b.GetAttachment(ctx, issue.ID, attachment.Name)
	assert.Equal(t, 3, exitOf(err))
}

// Deleting an issue takes its attachments and their content with it.
func TestDeletingAnIssueRemovesItsAttachments(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	_, err := b.DeleteIssue(ctx, issue.ID, "")
	require.NoError(t, err)

	assert.Empty(t, blobFiles(t, dir))
	_, err = b.GetAttachment(ctx, issue.ID, attachment.Name)
	assert.Equal(t, 3, exitOf(err), "the issue is gone, so nothing holds the attachment")
}

// A cascading workspace delete does the same for every issue it takes.
func TestCascadingWorkspaceDeleteRemovesAttachments(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	_, err := b.DeleteWorkspace(ctx, "awb", true, "")
	require.NoError(t, err)
	assert.Empty(t, blobFiles(t, dir))
}

// An attachment is addressed by its issue and its name. The issue half takes
// any reference an issue takes; the name half is exact.
func TestAttachmentReferences(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	_, hash, _ := domain.SplitID(issue.ID)
	read, err := b.GetAttachment(ctx, hash, "trace.txt")
	require.NoError(t, err)
	assert.Equal(t, attachment, read, "a bare issue hash addresses it as well")

	_, err = b.GetAttachment(ctx, issue.ID, "nothing.txt")
	assert.Equal(t, 3, exitOf(err), "a name the issue does not hold is not found")

	_, err = b.GetAttachment(ctx, "awb-ffffff", "trace.txt")
	assert.Equal(t, 3, exitOf(err), "and neither is an issue that does not exist")

	// A name that could never have been stored is a usage error rather than a
	// lookup that finds nothing.
	_, err = b.GetAttachment(ctx, issue.ID, "../escape.txt")
	assert.Equal(t, 2, exitOf(err))
	_, err = b.GetAttachment(ctx, issue.ID, "")
	assert.Equal(t, 2, exitOf(err))
}

// An issue holds at most one attachment under any one name, that pair being
// what identifies one. The second is refused rather than given a name it was
// not asked to have, and nothing of it is left behind.
func TestOneNamePerIssue(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	other := create(t, b, ctx, "Tokeniser drops a newline")

	first := attach(t, b, ctx, issue.ID, "trace.txt", "the first one\n")

	_, err := b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name: "trace.txt", Content: strings.NewReader("the second one\n"),
	})
	require.Error(t, err)
	assert.Equal(t, 4, exitOf(err), "it depends on what is stored, so it is a conflict")
	assert.Contains(t, err.Error(), "trace.txt")

	// The one that was there is untouched, and the refused upload left no file.
	read, err := b.GetAttachment(ctx, issue.ID, "trace.txt")
	require.NoError(t, err)
	assert.Equal(t, first, read)
	assert.Equal(t, []string{first.Sha256}, blobFiles(t, dir))

	// Another issue may hold the same name.
	elsewhere := attach(t, b, ctx, other.ID, "trace.txt", "the first one\n")
	assert.Equal(t, first.Sha256, elsewhere.Sha256, "and share the one stored copy")
	assert.Equal(t, []string{first.Sha256}, blobFiles(t, dir))

	// Deleting the first frees the name.
	_, err = b.DeleteAttachment(ctx, issue.ID, "trace.txt")
	require.NoError(t, err)
	again := attach(t, b, ctx, issue.ID, "trace.txt", "the second one\n")
	assert.Equal(t, "trace.txt", again.Name)
}

// Content over the maximum is refused, and nothing is left behind by the
// attempt.
func TestAttachmentTooLarge(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	_, err := b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name:    "big.bin",
		Content: strings.NewReader(strings.Repeat("x", domain.MaxAttachmentBytes+1)),
	})
	require.Error(t, err)
	assert.Equal(t, 2, exitOf(err))
	assert.Empty(t, blobFiles(t, dir), "nothing staged is left behind")
}

// Content of exactly the maximum is accepted: the limit is inclusive.
func TestAttachmentOfExactlyTheMaximum(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	attachment, err := b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name:    "big.bin",
		Content: strings.NewReader(strings.Repeat("x", domain.MaxAttachmentBytes)),
	})
	require.NoError(t, err)
	assert.EqualValues(t, domain.MaxAttachmentBytes, attachment.Size)
}

// A name or a content type outside the rules is refused before anything is
// written, and attaching to an issue that does not exist is a 404 that leaves
// no file behind either.
func TestAddAttachmentRefusals(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	_, err := b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name: "../escape.txt", Content: strings.NewReader("x"),
	})
	assert.Equal(t, 2, exitOf(err))

	_, err = b.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
		Name: "ok.txt", ContentType: "nonsense", Content: strings.NewReader("x"),
	})
	assert.Equal(t, 2, exitOf(err))

	_, err = b.AddAttachment(ctx, "awb-ffffff", backend.AttachmentCreate{
		Name: "ok.txt", Content: strings.NewReader("x"),
	})
	assert.Equal(t, 3, exitOf(err))

	assert.Empty(t, blobFiles(t, dir), "a refusal writes nothing")
}

// An empty file is a file: it has a size of zero, the digest of nothing, and
// the type that says nothing is known about it.
func TestEmptyAttachment(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	attachment := attach(t, b, ctx, issue.ID, "empty.bin", "")
	assert.EqualValues(t, 0, attachment.Size)
	assert.Equal(t, domain.DefaultContentType, attachment.ContentType)
	assert.Equal(t, []string{attachment.Sha256}, blobFiles(t, dir))
}

// The row promises content the directory does not hold: a runtime failure
// naming what is missing, not a not-found, because the attachment exists.
func TestMissingContentIsARuntimeFailure(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	require.NoError(t, os.Remove(filepath.Join(dir, attachment.Sha256)))

	_, _, err := b.OpenAttachment(ctx, issue.ID, attachment.Name)
	require.Error(t, err)
	assert.Equal(t, 1, exitOf(err))
	assert.Equal(t, awberr.Runtime, awberr.KindOf(err))
}

// A failure to remove the content does not fail the deletion.
//
// The rows are already committed away by then — reporting a failure would say
// something that is not true — and what is left behind is the unreferenced
// file the design already tolerates.
func TestDeleteSucceedsWhenTheContentCannotBeRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permission this test relies on")
	}
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	// A directory that cannot be written is one nothing can be unlinked from.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	deleted, err := b.DeleteAttachment(ctx, issue.ID, attachment.Name)
	require.NoError(t, err)
	assert.Equal(t, attachment, deleted)

	_, err = b.GetAttachment(ctx, issue.ID, attachment.Name)
	assert.Equal(t, 3, exitOf(err), "the attachment is gone whatever became of its content")
	assert.Equal(t, []string{attachment.Sha256}, blobFiles(t, dir),
		"and what is left is an unreferenced file, which is the tolerated failure")
}
