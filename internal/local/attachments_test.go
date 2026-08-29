package local_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	_, err = b.CreateProject(t.Context(), backend.ProjectCreate{Key: "awb"})
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
	assert.Len(t, attachment.ID, domain.AttachmentIDLen)
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

	// The order is created_at then id. Two uploads within one millisecond of
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

// Attaching does not move the issue's updated_at, exactly as adding a relation
// does not: an attachment is its own entity with its own lifecycle.
func TestAttachingDoesNotTouchTheIssue(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")

	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	read, err := b.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, issue.UpdatedAt, read.UpdatedAt)

	_, err = b.DeleteAttachment(ctx, attachment.ID)
	require.NoError(t, err)

	read, err = b.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, issue.UpdatedAt, read.UpdatedAt)
}

// Two attachments of the same bytes are two attachments sharing one stored
// file, and removing one leaves the other's content where it is.
func TestIdenticalContentIsStoredOnce(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	other := create(t, b, ctx, "Tokeniser drops a newline")

	first := attach(t, b, ctx, issue.ID, "one.txt", "same bytes")
	second := attach(t, b, ctx, other.ID, "two.txt", "same bytes")

	assert.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, first.Sha256, second.Sha256)
	assert.Equal(t, []string{first.Sha256}, blobFiles(t, dir), "one file for both")

	_, err := b.DeleteAttachment(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{second.Sha256}, blobFiles(t, dir),
		"the other attachment still holds the content")

	_, content, err := b.OpenAttachment(ctx, second.ID)
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

	deleted, err := b.DeleteAttachment(ctx, attachment.ID)
	require.NoError(t, err)
	assert.Equal(t, attachment.ID, deleted.ID, "the object as it was before deletion")
	assert.Empty(t, blobFiles(t, dir))

	_, err = b.GetAttachment(ctx, attachment.ID)
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
	_, err = b.GetAttachment(ctx, attachment.ID)
	assert.Equal(t, 3, exitOf(err))
}

// A cascading project delete does the same for every issue it takes.
func TestCascadingProjectDeleteRemovesAttachments(t *testing.T) {
	b, ctx, dir := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	_, err := b.DeleteProject(ctx, "awb", true, "")
	require.NoError(t, err)
	assert.Empty(t, blobFiles(t, dir))
}

// An attachment is addressed by a full id or an unambiguous prefix, and an
// ambiguous one is reported rather than guessed at.
func TestAttachmentReferences(t *testing.T) {
	b, ctx, _ := newBackendWithBlobs(t)
	issue := create(t, b, ctx, "Parser crashes")
	attachment := attach(t, b, ctx, issue.ID, "trace.txt", "boom\n")

	read, err := b.GetAttachment(ctx, attachment.ID[:6])
	require.NoError(t, err)
	assert.Equal(t, attachment.ID, read.ID)

	read, err = b.GetAttachment(ctx, strings.ToUpper(attachment.ID))
	require.NoError(t, err)
	assert.Equal(t, attachment.ID, read.ID)

	_, err = b.GetAttachment(ctx, "ffffffffffff")
	assert.Equal(t, 3, exitOf(err))

	_, err = b.GetAttachment(ctx, "not-hex")
	assert.Equal(t, 2, exitOf(err))
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

	_, _, err := b.OpenAttachment(ctx, attachment.ID)
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

	deleted, err := b.DeleteAttachment(ctx, attachment.ID)
	require.NoError(t, err)
	assert.Equal(t, attachment.ID, deleted.ID)

	_, err = b.GetAttachment(ctx, attachment.ID)
	assert.Equal(t, 3, exitOf(err), "the attachment is gone whatever became of its content")
	assert.Equal(t, []string{attachment.Sha256}, blobFiles(t, dir),
		"and what is left is an unreferenced file, which is the tolerated failure")
}
