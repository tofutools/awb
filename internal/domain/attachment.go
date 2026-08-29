package domain

import (
	"cmp"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tofutools/awb/internal/awberr"
)

// The attachment limits.
//
// MaxAttachmentBytes is the size of one attachment's content. It is a rule of
// the domain rather than of the transport, so the two modes refuse the same
// file: the command line checks it while streaming the file to disk and the
// server caps the request body at the same number.
const (
	MaxAttachmentBytes   = 32 << 20
	MaxAttachmentNameLen = 255
	MaxContentTypeLen    = 255
	DefaultContentType   = "application/octet-stream"
	sha256HexLen         = 64
)

// Attachment is one file attached to an issue. It is immutable: there is no
// operation that changes any field of one, which is why it carries no entity
// tag and takes no conditional edit — there is no second version to guard
// against.
//
// It is identified by the issue it belongs to and its name, which is what a
// label is identified by too. It carries no identifier of its own: a synthetic
// one would be a second name for something that already has one, and nobody
// would ever type it. That is why a name is unique within an issue.
//
// The content itself is not in the database. It lives as a file in the
// attachments directory, named by Sha256, and the row here is the metadata.
type Attachment struct {
	Issue       string `json:"issue"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Sha256      string `json:"sha256"`
	CreatedAt   string `json:"created_at"`
}

// SortAttachments puts attachments into their specified order — oldest first,
// then by name — so two invocations against unchanged data produce
// byte-identical output. Sorting by name alone would reshuffle the list
// whenever a file is added, which is the one thing an upload should not do.
//
// The timestamp has millisecond resolution and is not forced upward the way an
// issue's updated_at is, so two uploads within one millisecond share it and
// the name decides between them. A name is unique within an issue, so that is
// a total order.
func SortAttachments(attachments []Attachment) {
	slices.SortFunc(attachments, func(a, b Attachment) int {
		if c := cmp.Compare(a.CreatedAt, b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
}

// ValidateAttachmentName applies the input rules to an attachment's name.
//
// It is a file name and not a path: a separator, a control character, and the
// two names that mean a directory are all refused rather than stripped, so a
// name can never be read as somewhere to write. Like every other value it is
// stored exactly as it arrived, and it is never used to build a path — the
// content is stored under its own hash.
func ValidateAttachmentName(s string) (string, error) {
	if err := checkUTF8("attachment name", s); err != nil {
		return "", err
	}
	if s == "" {
		return "", awberr.Usagef("attachment name must not be empty")
	}
	if err := checkNoControls("attachment name", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("attachment name", s, MaxAttachmentNameLen); err != nil {
		return "", err
	}
	if strings.ContainsAny(s, `/\`) {
		return "", awberr.Usagef("invalid attachment name %q: it is a file name, not a path", s)
	}
	if s == "." || s == ".." {
		return "", awberr.Usagef("invalid attachment name %q: it names a directory", s)
	}
	return s, nil
}

// ValidateContentType applies the input rules to an attachment's content type.
// It must be a media type as RFC 2045 spells one — a type and a subtype, with
// optional parameters — and is stored exactly as it arrived rather than being
// lower-cased or stripped of those parameters.
//
// The parser is asked as well as the shape, because it is what decides whether
// the parameters are well formed; the shape is checked separately because the
// parser accepts a bare type with no subtype, which is not a media type
// anything can act on.
func ValidateContentType(s string) (string, error) {
	invalid := func(reason string) error {
		return awberr.Usagef("invalid content type %q: %s", s, reason)
	}

	if err := checkUTF8("content type", s); err != nil {
		return "", err
	}
	if s == "" {
		return "", awberr.Usagef("content type must not be empty")
	}
	if err := checkNoControls("content type", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("content type", s, MaxContentTypeLen); err != nil {
		return "", err
	}
	parsed, _, err := mime.ParseMediaType(s)
	if err != nil {
		return "", invalid(err.Error())
	}
	mainType, subType, found := strings.Cut(parsed, "/")
	if !found || mainType == "" || subType == "" {
		return "", invalid("expected a type and a subtype, as in text/plain")
	}
	return s, nil
}

// DetectContentType is what an attachment gets when the caller states no
// content type: the type sniffed from the first bytes of the content.
//
// It is derived from the content rather than from the name's extension,
// because an extension table is a file on the machine and would make the same
// upload get different answers on two of them. It lives here, in the layer
// both surfaces share, so a file uploaded through the API and the same file
// uploaded through the command line are typed identically.
func DetectContentType(head []byte) string {
	if len(head) == 0 {
		return DefaultContentType
	}
	return http.DetectContentType(head)
}

// CompactAttachmentLine renders an attachment as the compact one-line form:
//
//	awb-5c1d84 12345 9f86d0…b0f00a "text/markdown; charset=utf-8" "notes.md"
//
// The five fields are the issue, the size in bytes, the content's SHA-256, the
// content type and the name. The first three cannot contain a space; the last
// two are JSON strings, including their surrounding double quotes and JSON
// escaping, and are last for that reason. So a line is read by splitting the
// first three fields on whitespace and decoding the two JSON strings that
// follow.
//
// The line begins with the issue, as an issue's own compact line begins with
// its id, so that a line says what it is about away from the command that
// produced it. Together with the name it is the whole of what addresses the
// attachment.
//
// The content type is quoted rather than written bare because it may carry
// parameters — the sniffed default is "text/plain; charset=utf-8" — and a bare
// one would put a space in the middle of a positional field and turn a
// five-field line into six.
func CompactAttachmentLine(a *Attachment) string {
	return a.Issue + " " + strconv.FormatInt(a.Size, 10) + " " + a.Sha256 + " " +
		jsonString(a.ContentType) + " " + jsonString(a.Name)
}
