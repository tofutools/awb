package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tofutools/awb/internal/awberr"
)

// The length maxima of SPEC §2.6. Everything but a description is counted in
// Unicode code points after trimming; a description, the one field meant to
// hold prose, is bounded in bytes instead, because that is the size that
// matters for a blob nobody counts characters in.
const (
	MaxTitleLen       = 500
	MaxCloseReasonLen = 500
	MaxProjectNameLen = 500
	MaxSearchTermLen  = 500
	MaxLabelLen       = 64
	MaxAssigneeLen    = 64
	MaxProjectKeyLen  = 16

	// MaxDescriptionBytes is 64 KiB of UTF-8.
	MaxDescriptionBytes = 64 * 1024
)

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
// SPEC §2.6 grants a description: tab, line feed and carriage return, those
// being what Markdown text is made of.
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

// ValidateTitle applies SPEC §2.2 and §2.6 to an issue title: leading and
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

// ValidateCloseReason applies SPEC §2.2 to a close reason. It is trimmed and
// rejects a line break as a title does, but unlike a title it may be empty: a
// value that is empty after trimming clears it.
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
	return s, nil
}

// ValidateDescription applies SPEC §2.6 to a description. It is never trimmed,
// so a trailing line feed from a heredoc or an editor is part of it, and it is
// bounded in bytes rather than in characters.
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
	return s, nil
}

// ValidateProjectName applies SPEC §2.1 and §2.6 to a project name. An empty
// name is accepted here and means "restore the key as the name", which is what
// --name "" and a PATCH carrying "" do; the caller substitutes the key.
func ValidateProjectName(s string) (string, error) {
	if err := checkUTF8("project name", s); err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	if err := checkNoControls("project name", s); err != nil {
		return "", err
	}
	if err := checkMaxRunes("project name", s, MaxProjectNameLen); err != nil {
		return "", err
	}
	return s, nil
}

// ValidateProjectKey applies SPEC §2.1: lowercase ASCII letters, digits and
// hyphens, starting with a letter, at most 16 characters. The key is refused
// rather than normalised.
func ValidateProjectKey(s string) (string, error) {
	if err := checkUTF8("project key", s); err != nil {
		return "", err
	}
	if s == "" {
		return "", awberr.Usagef("project key must not be empty")
	}
	if len(s) > MaxProjectKeyLen {
		return "", awberr.Usagef("invalid project key %q: at most %d characters", s, MaxProjectKeyLen)
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return "", awberr.Usagef(
				"invalid project key %q: lowercase ASCII letters, digits and hyphens, starting with a letter", s)
		}
	}
	return s, nil
}

// isLabelRune reports whether r is in the character set SPEC §2.2 gives labels
// and assignees: lowercase ASCII letters, digits, hyphens, underscores, dots
// and slashes.
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

// ValidateLabel applies SPEC §2.2 to a label.
func ValidateLabel(s string) (string, error) { return validateLabelLike("label", s, MaxLabelLen) }

// ValidateAssignee applies SPEC §2.2 to an assignee, which has the same
// character set as a label.
func ValidateAssignee(s string) (string, error) {
	return validateLabelLike("assignee", s, MaxAssigneeLen)
}

// ValidateSearchTerm applies SPEC §2.6 and §4.3 to one search term. A term the
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

// hasToken reports whether s holds anything the unicode61 tokenizer would keep.
// That tokenizer splits on non-alphanumeric characters, so a term needs at
// least one letter or digit to produce a token at all.
func hasToken(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// FoldToAssignee reduces s to the assignee vocabulary of SPEC §2.2 by
// lower-casing it and dropping every character outside that set, so an OS
// username like "Mikael" or a Windows "DOMAIN\\user" still yields a usable
// identity (SPEC §7.1). It returns "" when nothing is left.
//
// This is for values the user never typed. A value given on the command line or
// in a configuration file is still rejected rather than folded.
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
