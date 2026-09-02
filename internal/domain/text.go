package domain

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tofutools/awb/internal/awberr"
)

// The length maxima. Everything but a description is counted in Unicode code
// points, after trimming for the two fields that are trimmed; a description,
// the one field meant to hold prose, is bounded in bytes instead, because that
// is the size that matters for a blob nobody counts characters in.
const (
	MaxTitleLen          = 500
	MaxCloseReasonLen    = 500
	MaxWorkspaceNameLen  = 500
	MaxUserFullNameLen   = 500
	MaxSearchTermLen     = 500
	MaxLabelLen          = 64
	MaxAssigneeLen       = 64
	MaxWorkspaceKeyLen   = 16
	MaxBoardViewNameLen  = 100
	MaxCommitHashLen     = 128
	MinCommitHashLen     = 8
	MaxPullRequestURLLen = 1000

	// MaxDescriptionBytes is 64 KiB of UTF-8.
	MaxDescriptionBytes = 64 * 1024
)

// ValidateCommitHash accepts an optional hexadecimal commit identifier. It is
// stored exactly as supplied, including letter case.
func ValidateCommitHash(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) < MinCommitHashLen || len(s) > MaxCommitHashLen {
		return "", awberr.Usagef("commit hash must be between %d and %d hexadecimal characters", MinCommitHashLen, MaxCommitHashLen)
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return "", awberr.Usagef("commit hash must contain only hexadecimal characters")
		}
	}
	return s, nil
}

// ValidatePullRequestURL accepts an optional absolute HTTP(S) URL. The value
// is stored verbatim rather than normalized.
func ValidatePullRequestURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if err := checkUTF8("pull request URL", s); err != nil {
		return "", err
	}
	if utf8.RuneCountInString(s) > MaxPullRequestURLLen {
		return "", awberr.Usagef("pull request URL is too long: maximum %d characters", MaxPullRequestURLLen)
	}
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return "", awberr.Usagef("pull request URL must not contain whitespace")
	}
	parsed, err := url.Parse(s)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", awberr.Usagef("pull request URL must be an absolute http or https URL")
	}
	return s, nil
}

// checkUTF8 rejects a byte sequence that is not well-formed UTF-8. It is never
// repaired and an invalid byte is never replaced with U+FFFD, so nothing is
// stored that the caller did not send. A command line argument is bytes rather
// than text on POSIX, so it is checked exactly as a JSON request body is.
func checkUTF8(field, s string) error {
	if !utf8.ValidString(s) {
		return awberr.Usagef("%s is not valid UTF-8", field)
	}
	return nil
}

// ValidateBoardViewName applies the title-like rules to a saved board view's
// display name. It is trimmed, required and kept deliberately short because it
// appears in a compact selector rather than as prose.
func ValidateBoardViewName(s string) (string, error) {
	if err := checkUTF8("board view name", s); err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", awberr.Usagef("board view name must not be empty")
	}
	if err := checkNoControls("board view name", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("board view name", s, MaxBoardViewNameLen); err != nil {
		return "", err
	}
	return s, nil
}

// checkNoControls rejects any rune in general category Cc — the C0 range, DEL
// and the C1 range. NUL is refused everywhere, being Cc itself. This is also
// what rejects a title or a close reason containing a line break, since LF and
// CR are Cc.
func checkNoControls(field, s string) error {
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) {
			return awberr.Usagef("%s must not contain control characters (found U+%04X)", field, r)
		}
	}
	return nil
}

// checkNoControlsAllowingWhitespace is checkNoControls with the one exception
// granted to a description: tab, line feed and carriage return, those being
// what Markdown text is made of.
func checkNoControlsAllowingWhitespace(field, s string) error {
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if unicode.Is(unicode.Cc, r) {
			return awberr.Usagef("%s must not contain control characters (found U+%04X)", field, r)
		}
	}
	return nil
}

// checkMaxRunes bounds a value in Unicode code points.
func checkMaxRunes(field, s string, max int) error {
	if n := utf8.RuneCountInString(s); n > max {
		return awberr.Usagef("%s is too long: %d characters, maximum %d", field, n, max)
	}
	return nil
}

// ValidateTitle applies the input rules to an issue title: leading and
// trailing whitespace is trimmed, and a title that is empty after trimming, or
// that contains a line break, is rejected. It returns the trimmed value, which
// is what gets stored.
func ValidateTitle(s string) (string, error) {
	if err := checkUTF8("title", s); err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", awberr.Usagef("title must not be empty")
	}
	if err := checkNoControls("title", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("title", s, MaxTitleLen); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateCloseReason applies the input rules to a close reason. It is trimmed
// and rejects a line break as a title does, but unlike a title it may be
// empty: a value that is empty after trimming clears it. A non-empty reason
// becomes the body of a Markdown comment, so it passes the Markdown gate too.
func ValidateCloseReason(s string) (string, error) {
	if err := checkUTF8("close reason", s); err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	if err := checkNoControls("close reason", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("close reason", s, MaxCloseReasonLen); err != nil {
		return "", err
	}
	if err := ValidateMarkdown("close reason", s); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateDescription applies the input rules to a description. It is never
// trimmed, so a trailing line feed from a heredoc or an editor is part of it,
// and it is bounded in bytes rather than in characters. Being Markdown, it
// also passes the Markdown gate: raw HTML and a link or image destination
// carrying an unsupported scheme are refused here rather than left to whatever
// renders it.
func ValidateDescription(s string) (string, error) {
	if err := checkUTF8("description", s); err != nil {
		return "", err
	}
	if err := checkNoControlsAllowingWhitespace("description", s); err != nil {
		return "", err
	}
	if len(s) > MaxDescriptionBytes {
		return "", awberr.Usagef("description is too long: %d bytes, maximum %d",
			len(s), MaxDescriptionBytes)
	}
	if err := ValidateMarkdown("description", s); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateComment applies the prose gate to a Markdown comment. A comment has
// the same byte and control-character bounds as a description, but unlike a
// description it must contain something besides whitespace. The original
// bytes are still returned and stored unchanged.
func ValidateComment(s string) (string, error) {
	if _, err := ValidateDescription(s); err != nil {
		return "", awberr.Usagef("comment %s", strings.TrimPrefix(err.Error(), "description "))
	}
	if strings.TrimSpace(s) == "" {
		return "", awberr.Usagef("comment must not be empty")
	}
	return s, nil
}

// ValidateWorkspaceName applies the input rules to a workspace name. An empty name
// is accepted here and means "restore the key as the name", which is what
// --name "" and a PATCH carrying "" do; the caller substitutes the key.
func ValidateWorkspaceName(s string) (string, error) {
	if err := checkUTF8("workspace name", s); err != nil {
		return "", err
	}
	// Not trimmed: only a title and a close reason are. Everything else is
	// stored byte for byte as it arrived, so a name whose spacing the caller
	// meant is the name they get back.
	if err := checkNoControls("workspace name", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("workspace name", s, MaxWorkspaceNameLen); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateUserFullName applies the input rules to a user's descriptive name.
// It is optional, so empty clears it, and is otherwise stored byte for byte as
// it arrived, like a workspace name.
func ValidateUserFullName(s string) (string, error) {
	if err := checkUTF8("full name", s); err != nil {
		return "", err
	}
	if err := checkNoControls("full name", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("full name", s, MaxUserFullNameLen); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateWorkspaceKey applies the key vocabulary: lowercase ASCII letters,
// digits and hyphens, starting with a letter, at most 16 characters. The key
// is refused rather than normalised.
func ValidateWorkspaceKey(s string) (string, error) {
	if err := checkUTF8("workspace key", s); err != nil {
		return "", err
	}
	if s == "" {
		return "", awberr.Usagef("workspace key must not be empty")
	}
	if len(s) > MaxWorkspaceKeyLen {
		return "", awberr.Usagef("invalid workspace key %q: at most %d characters", s, MaxWorkspaceKeyLen)
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return "", awberr.Usagef(
				"invalid workspace key %q: lowercase ASCII letters, digits and hyphens, starting with a letter", s)
		}
	}
	return s, nil
}

// isLabelRune reports whether r is in the character set labels and assignees
// share: lowercase ASCII letters, digits, hyphens, underscores, dots and
// slashes.
func isLabelRune(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= '0' && r <= '9' ||
		r == '-' || r == '_' || r == '.' || r == '/'
}

// validateLabelLike applies the shared vocabulary of labels and assignees. A
// value outside it is rejected rather than normalised, so "claim --as Mikael"
// is a usage error.
func validateLabelLike(field, s string, max int) (string, error) {
	if err := checkUTF8(field, s); err != nil {
		return "", err
	}
	if s == "" {
		return "", awberr.Usagef("%s must not be empty", field)
	}
	if err := checkMaxRunes(field, s, max); err != nil {
		return "", err
	}
	for _, r := range s {
		if !isLabelRune(r) {
			return "", awberr.Usagef(
				"invalid %s %q: lowercase ASCII letters, digits, hyphens, underscores, dots and slashes only",
				field, s)
		}
	}
	return s, nil
}

// ValidateLabel applies the label vocabulary.
func ValidateLabel(s string) (string, error) { return validateLabelLike("label", s, MaxLabelLen) }

// ValidateAssignee applies the assignee vocabulary, which is the same
// character set as a label.
func ValidateAssignee(s string) (string, error) {
	return validateLabelLike("assignee", s, MaxAssigneeLen)
}

// ValidateSearchTerm applies the input rules to one search term. A term the
// FTS5 unicode61 tokenizer would reduce to nothing — "-" or "," — is a usage
// error: dropping it silently would widen the search without saying so, and
// passing it through would either error or match everything depending on the
// driver.
func ValidateSearchTerm(s string) (string, error) {
	if err := checkUTF8("search term", s); err != nil {
		return "", err
	}
	if err := checkNoControls("search term", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("search term", s, MaxSearchTermLen); err != nil {
		return "", err
	}
	if !hasToken(s) {
		return "", awberr.Usagef("search term %q contains nothing to search for", s)
	}
	return s, nil
}

// hasToken reports whether s holds anything the unicode61 tokenizer would
// keep. That tokenizer splits on non-alphanumeric characters, so a term needs
// at least one letter or digit to produce a token at all.
func hasToken(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// FoldToAssignee reduces s to the assignee vocabulary by lower-casing it and
// dropping every character outside that set, so an OS username like "Mikael"
// or a Windows "DOMAIN\\user" still yields a usable identity. It returns ""
// when nothing is left.
//
// This is for values the user never typed. A value given on the command line
// or in a configuration file is still rejected rather than folded.
func FoldToAssignee(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if isLabelRune(r) {
			b.WriteRune(r)
		}
	}
	folded := b.String()
	if utf8.RuneCountInString(folded) > MaxAssigneeLen {
		folded = string([]rune(folded)[:MaxAssigneeLen])
	}
	return folded
}
