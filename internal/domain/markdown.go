package domain

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/tofutools/awb/internal/awberr"
)

// The schemes a destination may carry. A link is followed by a reader, so it
// may be http, https or mailto and nothing else; an image is fetched by the
// renderer without the reader doing anything, so it may not be mailto but may
// be a data: URL of one of the raster types below.
//
// javascript:, vbscript: and data: on a link are the schemes this keeps out,
// but the list is an allow-list rather than a deny-list: a scheme nobody has
// thought about yet is refused, not passed through.
var (
	linkSchemes  = []string{"http", "https", "mailto"}
	imageSchemes = []string{"http", "https"}

	// imageDataTypes is the raster set, deliberately without image/svg+xml:
	// an SVG is a document that can carry script and CSS, which is the very
	// thing the raw-HTML rule below keeps out of a description.
	imageDataTypes = []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
)

// ValidateMarkdown is the Markdown gate every prose field passes through on
// its way in, applied by ValidateDescription, ValidateComment and
// ValidateCloseReason so that both surfaces get it from the one operation
// layer they share.
//
// What is accepted is the pinned dialect and nothing else: CommonMark plus the
// GFM set that Markdown() parses, minus two things.
//
//   - Raw HTML, inline and block alike, is refused rather than escaped. That
//     is what keeps <script>, <style>, <svg>, <math> and an event handler on a
//     hand-written tag out of stored prose, and it means no renderer
//     downstream has to be the one that catches them. An HTML comment is raw
//     HTML too, and a bare <word> in prose is raw HTML to a Markdown parser,
//     so it has to be written as `<word>` or \<word>.
//   - A link or image destination whose scheme is not in the lists above. A
//     bare URL the autolink extension picks up is a link like any other, so
//     mentioning an ftp:// address in prose means wrapping it in a code span.
//
// The check is on what a renderer will see, so a destination is normalised
// first, as far as CommonMark and a URL parser normalise it and no further —
// see splitScheme. Percent-encoding is not undone, because it survives into
// the URL rather than being removed on the way there: %6aavascript:x is a
// relative reference to every browser, not a scheme.
//
// A destination with no scheme at all — a relative path, a fragment, a
// protocol-relative //host — is accepted. It carries no scheme, so there is no
// other scheme for it to be.
//
// The gate rewrites nothing: it accepts a value or refuses it. Whether the
// caller's bytes then reach storage untouched is the field's own rule — a
// description and a comment body are stored as they arrived, a close reason
// after ValidateCloseReason has trimmed it.
func ValidateMarkdown(field, s string) error {
	if s == "" {
		return nil
	}
	source := []byte(s)
	doc := Markdown().Parser().Parse(text.NewReader(source))

	return ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var err error
		switch v := n.(type) {
		case *ast.HTMLBlock:
			err = rawHTML(field, firstSegment(v.Lines(), source))
		case *ast.RawHTML:
			err = rawHTML(field, firstSegment(v.Segments, source))
		case *ast.Image:
			err = checkDestination(field, "an image", string(v.Destination), imageSchemes, true)
		case *ast.Link:
			err = checkDestination(field, "a link", string(v.Destination), linkSchemes, false)
		case *ast.AutoLink:
			err = checkDestination(field, "a link", AutoLinkURL(v, source), linkSchemes, false)
		}
		if err != nil {
			return ast.WalkStop, err
		}
		return ast.WalkContinue, nil
	})
}

// rawHTML is the refusal, naming the markup that triggered it because a
// description is long and a stray < in it is not otherwise easy to find.
func rawHTML(field, markup string) error {
	return awberr.Usagef(
		"%s must not contain raw HTML (found %q); write it as a code span or escape the < with a backslash",
		field, abbreviate(markup))
}

// firstSegment is the source text of a node's first segment, which is where
// its markup begins.
func firstSegment(segments *text.Segments, source []byte) string {
	if segments == nil || segments.Len() == 0 {
		return ""
	}
	segment := segments.At(0)
	return strings.TrimRight(string(segment.Value(source)), "\r\n")
}

// checkDestination applies the scheme rule to one destination. kind names it
// for the error message, article and all. allowImageData additionally admits a
// data: URL whose media type is one of imageDataTypes, which only an image may
// be.
func checkDestination(field, kind, destination string, schemes []string, allowImageData bool) error {
	scheme, rest := splitScheme(destination)
	switch {
	case scheme == "":
		// Relative, fragment or protocol-relative: no scheme to refuse.
		return nil
	case slices.Contains(schemes, scheme):
		return nil
	case allowImageData && scheme == "data":
		if isAllowedImageData(rest) {
			return nil
		}
		return awberr.Usagef("%s has %s with an unsupported data URL %q: only %s are allowed",
			field, kind, abbreviate(destination), strings.Join(imageDataTypes, ", "))
	}
	allowed := strings.Join(schemes, ":, ") + ":"
	if allowImageData {
		allowed += ", and data: URLs of type " + strings.Join(imageDataTypes, ", ")
	}
	return awberr.Usagef("%s has %s with an unsupported URL scheme %q: only %s are allowed",
		field, kind, abbreviate(destination), allowed)
}

// splitScheme returns the lowercased scheme a renderer will see in destination
// and everything after the colon, or "" when the destination carries none.
//
// The destination is normalised first, exactly as far as the two layers below
// this one normalise it. CommonMark resolves backslash escapes and character
// references inside a destination, in that order, so an escape a reference
// produced stays literal. A URL parser then trims leading and trailing C0
// controls and spaces and removes every tab, line feed and carriage return
// wherever it sits, so a scheme split across a tab is still that scheme.
// Nothing beyond that is undone: a space in the middle is percent-encoded
// rather than removed, so "my file:2.txt" carries no scheme.
func splitScheme(destination string) (scheme, rest string) {
	b := util.UnescapePunctuations([]byte(destination))
	b = util.ResolveNumericReferences(b)
	b = util.ResolveEntityNames(b)

	n := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, string(b))
	n = strings.TrimFunc(n, func(r rune) bool { return r <= ' ' })

	colon := strings.IndexByte(n, ':')
	if colon <= 0 || !isSchemeToken(n[:colon]) {
		return "", n
	}
	return strings.ToLower(n[:colon]), n[colon+1:]
}

// isSchemeToken reports whether s is a URL scheme as RFC 3986 spells one: a
// letter followed by letters, digits, plus, minus and dot. A colon after
// anything else — ./notes:1, or a destination whose backslash survived
// unescaping — separates nothing, leaving a relative reference.
func isSchemeToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// isAllowedImageData reports whether rest — a data: URL with its scheme
// removed — declares one of the raster media types, optionally base64-encoded.
// A data: URL that declares nothing defaults to text/plain, so an absent media
// type is refused along with every other one.
func isAllowedImageData(rest string) bool {
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return false
	}
	mediaType := strings.ToLower(rest[:comma])
	mediaType = strings.TrimSuffix(mediaType, ";base64")
	return slices.Contains(imageDataTypes, mediaType)
}

// abbreviate shortens a destination for an error message, a data: URL being
// arbitrarily long and nothing past its media type telling the reader anything.
func abbreviate(destination string) string {
	const max = 60
	if utf8.RuneCountInString(destination) <= max {
		return destination
	}
	return string([]rune(destination)[:max]) + "…"
}
