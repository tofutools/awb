package storage_test

import (
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
	"github.com/tofutools/awb/internal/storage"
)

// The store is a flat directory of files named by the SHA-256 of what is in
// them, and nothing is created until something is stored.
func TestBlobsStageAndPlace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attachments")
	blobs := storage.NewBlobs(dir)
	assert.Equal(t, dir, blobs.Dir())

	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "naming the directory does not create it")

	staged, err := blobs.Stage(strings.NewReader("boom\n"), 1024)
	require.NoError(t, err)
	assert.EqualValues(t, 5, staged.Size)
	sum := sha256.Sum256([]byte("boom\n"))
	assert.Equal(t, hex.EncodeToString(sum[:]), staged.Sha256)
	assert.Equal(t, "boom\n", string(staged.Head))

	// The staging file is in the store's own directory, so the rename that names
	// it never crosses a filesystem boundary.
	assert.Equal(t, dir, filepath.Dir(staged.Path))

	require.NoError(t, blobs.Place(staged))
	assert.Equal(t, []string{staged.Sha256}, entries(t, dir))

	content, err := blobs.Open(staged.Sha256)
	require.NoError(t, err)
	defer content.Close() //nolint:errcheck // read to its end below
	data, err := io.ReadAll(content)
	require.NoError(t, err)
	assert.Equal(t, "boom\n", string(data))
}

// Placing the same content twice is not a failure: the name is the digest, so
// whatever is there holds the same bytes.
func TestBlobsPlaceIsIdempotent(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))

	first, err := blobs.Stage(strings.NewReader("same"), 1024)
	require.NoError(t, err)
	require.NoError(t, blobs.Place(first))

	second, err := blobs.Stage(strings.NewReader("same"), 1024)
	require.NoError(t, err)
	require.Equal(t, first.Sha256, second.Sha256)
	require.NoError(t, blobs.Place(second))

	assert.Equal(t, []string{first.Sha256}, entries(t, blobs.Dir()))
}

// Content over the limit is refused rather than truncated, and the staging
// file goes with the refusal. A file of exactly the limit is accepted.
func TestBlobsRefuseContentOverTheLimit(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))

	_, err := blobs.Stage(strings.NewReader(strings.Repeat("x", 11)), 10)
	require.Error(t, err)
	assert.Equal(t, awberr.Usage, awberr.KindOf(err))
	assert.Empty(t, entries(t, blobs.Dir()), "nothing staged is left behind")

	staged, err := blobs.Stage(strings.NewReader(strings.Repeat("x", 10)), 10)
	require.NoError(t, err)
	assert.EqualValues(t, 10, staged.Size)
}

// Discard removes what an upload did not go on to store.
func TestBlobsDiscard(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))

	staged, err := blobs.Stage(strings.NewReader("boom\n"), 1024)
	require.NoError(t, err)
	blobs.Discard(staged)
	assert.Empty(t, entries(t, blobs.Dir()))
}

// Removing content that is already gone is not a failure: what the caller
// asked for is that it not be there.
func TestBlobsRemove(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))

	staged, err := blobs.Stage(strings.NewReader("boom\n"), 1024)
	require.NoError(t, err)
	require.NoError(t, blobs.Place(staged))

	require.NoError(t, blobs.Remove(staged.Sha256))
	require.NoError(t, blobs.Remove(staged.Sha256))
	assert.Empty(t, entries(t, blobs.Dir()))
}

// A digest with no file behind it is a runtime failure naming the directory:
// the row promised content this directory does not hold.
func TestBlobsOpenMissing(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))
	require.NoError(t, blobs.Create())

	_, err := blobs.Open(strings.Repeat("a", 64))
	require.Error(t, err)
	assert.Equal(t, awberr.Runtime, awberr.KindOf(err))
	assert.Contains(t, err.Error(), blobs.Dir())
}

// Create is what awb init calls, and is idempotent.
func TestBlobsCreateIsIdempotent(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))
	require.NoError(t, blobs.Create())
	require.NoError(t, blobs.Create())

	info, err := os.Stat(blobs.Dir())
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// Only the first bytes are kept for sniffing, however long the content is.
func TestBlobsHeadIsBounded(t *testing.T) {
	blobs := storage.NewBlobs(filepath.Join(t.TempDir(), "attachments"))

	staged, err := blobs.Stage(strings.NewReader(strings.Repeat("x", 4096)), 1<<20)
	require.NoError(t, err)
	assert.Len(t, staged.Head, 512)
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	found, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	names := make([]string, 0, len(found))
	for _, entry := range found {
		names = append(names, entry.Name())
	}
	return names
}
