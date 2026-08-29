package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

// A name is a file name and not a path. Everything refused here is refused
// rather than stripped, because a name is never somewhere to write.
func TestValidateAttachmentName(t *testing.T) {
	for _, ok := range []string{
		"notes.md", "trace 2026-01-01.log", "arkiv-Ω.txt", ".hidden", "-", "..leading",
	} {
		got, err := domain.ValidateAttachmentName(ok)
		require.NoError(t, err, ok)
		assert.Equal(t, ok, got, "stored exactly as it arrived")
	}

	for _, bad := range []string{
		"", "a/b.txt", `a\b.txt`, ".", "..", "with\nnewline", "nul\x00byte",
		strings.Repeat("x", domain.MaxAttachmentNameLen+1),
	} {
		_, err := domain.ValidateAttachmentName(bad)
		assert.Error(t, err, "%q", bad)
	}
}

func TestValidateContentType(t *testing.T) {
	for _, ok := range []string{
		"text/plain", "text/markdown; charset=utf-8", "application/octet-stream", "IMAGE/PNG",
	} {
		got, err := domain.ValidateContentType(ok)
		require.NoError(t, err, ok)
		assert.Equal(t, ok, got, "neither lower-cased nor stripped of its parameters")
	}

	for _, bad := range []string{"", "notamediatype", "text/", "text/plain; =", "text/plain\n"} {
		_, err := domain.ValidateContentType(bad)
		assert.Error(t, err, "%q", bad)
	}
}

// The default content type is sniffed from the content, never from a table on
// the machine, so the same upload is typed the same way everywhere.
func TestDetectContentType(t *testing.T) {
	assert.Equal(t, "application/octet-stream", domain.DetectContentType(nil),
		"empty content is bytes like any other")
	assert.Equal(t, "text/plain; charset=utf-8", domain.DetectContentType([]byte("hello\n")))
	assert.Equal(t, "image/png",
		domain.DetectContentType([]byte("\x89PNG\r\n\x1a\n"+strings.Repeat("\x00", 16))))
	assert.Equal(t, "application/pdf", domain.DetectContentType([]byte("%PDF-1.7\n")))
}

func TestParseAttachmentRef(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"3f2a91c40d17", "3f2a91c40d17"},
		{"3F2A91C40D17", "3f2a91c40d17"},
		{"3f2a", "3f2a"},
	} {
		got, err := domain.ParseAttachmentRef(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got)
	}

	for _, bad := range []string{"", "zz", "3f2a91c40d17a", "awb-5c1d84", " 3f2a"} {
		_, err := domain.ParseAttachmentRef(bad)
		assert.Error(t, err, "%q", bad)
	}
}

// An attachment ID is minted the way an issue ID is: from the name, the
// timestamp and a fresh salt, so two uploads of the same file are two
// attachments.
func TestMintAttachmentID(t *testing.T) {
	salt := make([]byte, domain.SaltLen)
	id := domain.MintAttachmentID("notes.md", "2026-08-26T09:12:03.412Z", salt)
	assert.Len(t, id, domain.AttachmentIDLen)
	assert.True(t, domain.IsHex(id))
	assert.Equal(t, id, domain.MintAttachmentID("notes.md", "2026-08-26T09:12:03.412Z", salt),
		"the derivation is a function of its inputs")

	other := domain.MintAttachmentID("notes.md", "2026-08-26T09:12:03.413Z", salt)
	assert.NotEqual(t, id, other)

	// The first six characters are an issue's whole hash, so the two derivations
	// stay one function cut to two lengths.
	assert.Equal(t, domain.MintHash("notes.md", "2026-08-26T09:12:03.412Z", salt),
		id[:domain.HashLen])
}

func TestCompactAttachmentLine(t *testing.T) {
	a := &domain.Attachment{
		ID:          "3f2a91c40d17",
		Issue:       "awb-5c1d84",
		Name:        "release notes.md",
		ContentType: "text/markdown; charset=utf-8",
		Size:        12345,
		Sha256:      strings.Repeat("ab", 32),
		CreatedAt:   "2026-08-26T09:12:03.412Z",
	}
	assert.Equal(t,
		`3f2a91c40d17 12345 text/markdown; charset=utf-8 `+strings.Repeat("ab", 32)+
			` "release notes.md"`,
		domain.CompactAttachmentLine(a))
}

// Attachments are ordered oldest first, then by id: a total order, and one an
// upload extends rather than reshuffles.
func TestSortAttachments(t *testing.T) {
	attachments := []domain.Attachment{
		{ID: "bbbb", CreatedAt: "2026-08-26T09:12:03.412Z"},
		{ID: "aaaa", CreatedAt: "2026-08-26T09:12:03.412Z"},
		{ID: "cccc", CreatedAt: "2026-08-26T09:12:03.411Z"},
	}
	domain.SortAttachments(attachments)
	assert.Equal(t, []string{"cccc", "aaaa", "bbbb"},
		[]string{attachments[0].ID, attachments[1].ID, attachments[2].ID})
}

// Normalize leaves an issue with an empty array rather than a nil one, so the
// JSON encoding carries [] and never null.
func TestNormalizeGivesAnIssueAnAttachmentArray(t *testing.T) {
	issue := &domain.Issue{}
	issue.Normalize()
	require.NotNil(t, issue.Attachments)
	assert.Empty(t, issue.Attachments)
}
