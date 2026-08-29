package domain_test

import (
	"encoding/json"
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

func TestCompactAttachmentLine(t *testing.T) {
	a := &domain.Attachment{
		Issue:       "awb-5c1d84",
		Name:        "release notes.md",
		ContentType: "text/markdown; charset=utf-8",
		Size:        12345,
		Sha256:      strings.Repeat("ab", 32),
		CreatedAt:   "2026-08-26T09:12:03.412Z",
	}
	assert.Equal(t,
		`awb-5c1d84 12345 `+strings.Repeat("ab", 32)+
			` "text/markdown; charset=utf-8" "release notes.md"`,
		domain.CompactAttachmentLine(a))

	// The point of the two quoted fields being last: the line is still five
	// fields when either of them holds a space, which the sniffed default
	// content type always does.
	fields := strings.Fields(domain.CompactAttachmentLine(a))
	assert.Equal(t, a.Issue, fields[0])
	assert.Equal(t, "12345", fields[1])
	assert.Equal(t, a.Sha256, fields[2])

	var contentType, name string
	rest := strings.TrimPrefix(domain.CompactAttachmentLine(a),
		a.Issue+" 12345 "+a.Sha256+" ")
	decoder := json.NewDecoder(strings.NewReader(rest))
	require.NoError(t, decoder.Decode(&contentType))
	require.NoError(t, decoder.Decode(&name))
	assert.Equal(t, a.ContentType, contentType)
	assert.Equal(t, a.Name, name)
}

// Attachments are ordered oldest first, then by name: a total order, since a
// name is unique within an issue, and one an upload extends rather than
// reshuffles.
func TestSortAttachments(t *testing.T) {
	attachments := []domain.Attachment{
		{Name: "b.txt", CreatedAt: "2026-08-26T09:12:03.412Z"},
		{Name: "a.txt", CreatedAt: "2026-08-26T09:12:03.412Z"},
		{Name: "c.txt", CreatedAt: "2026-08-26T09:12:03.411Z"},
	}
	domain.SortAttachments(attachments)
	assert.Equal(t, []string{"c.txt", "a.txt", "b.txt"},
		[]string{attachments[0].Name, attachments[1].Name, attachments[2].Name})
}

// Normalize leaves an issue with an empty array rather than a nil one, so the
// JSON encoding carries [] and never null.
func TestNormalizeGivesAnIssueAnAttachmentArray(t *testing.T) {
	issue := &domain.Issue{}
	issue.Normalize()
	require.NotNil(t, issue.Attachments)
	assert.Empty(t, issue.Attachments)
}
