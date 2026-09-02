package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/domain"
)

// accepted is Markdown the gate lets through unchanged.
var accepted = []string{
	"",
	"plain prose",
	"# Heading\n\n- a\n- b\n\n> quote\n\n    indented code\n",
	"**bold** _italic_ ~~struck~~ `code` and a\n\n| a | b |\n|---|---|\n| 1 | 2 |\n",
	"- [ ] todo\n- [x] done\n",

	// Every allowed link scheme, written every way a link can be written.
	"[a](https://example.com/x)",
	"[a](http://example.com/x)",
	"[a](mailto:someone@example.com)",
	"<https://example.com/x>",
	"<mailto:someone@example.com>",
	"someone@example.com",       // GFM autolink, which becomes mailto:
	"visit www.example.com now", // GFM autolink, which becomes http://
	"[a][ref]\n\n[ref]: https://example.com/x",
	"[HTTPS://EXAMPLE.COM/X](HTTPS://EXAMPLE.COM/X)", // the scheme is case-insensitive

	// No scheme at all: relative, fragment, protocol-relative, a colon after
	// something that is not a scheme, and nothing.
	"[a](./notes.md) [b](#section) [c](//example.com/x) [d](./notes:1.md) [e]()",
	"![a](./shot.png)",

	// Images: http, https and the four raster data types, base64 or not.
	"![a](https://example.com/x.png)",
	"![a](data:image/png;base64,iVBORw0KGgo=)",
	"![a](data:image/jpeg;base64,AAAA)",
	"![a](data:image/gif;base64,AAAA)",
	"![a](data:image/webp;base64,AAAA)",
	"![a](DATA:IMAGE/PNG;BASE64,AAAA)",
	"![a](data:image/png,%89PNG)",

	// A disallowed scheme is only a destination rule, never a text rule, and a
	// < that begins no tag is prose.
	"the javascript: scheme, and `<script>` in a code span",
	"```html\n<script>alert(1)</script>\n```\n",
	"    <script>alert(1)</script>\n", // an indented code block
	"a \\<span> escaped, and 3 < 5 > 2 unescaped, and <3",
	"C:\\Users\\x, git@github.com:tofutools/awb.git and key: value",
	"the file:///etc/passwd path and ssh://host/path, neither of them autolinked",
}

// rejectedLinks are destinations no link may carry.
var rejectedLinks = []string{
	"javascript:alert(1)",
	"JaVaScRiPt:alert(1)",
	"vbscript:msgbox(1)",
	"data:text/html;base64,PHNjcmlwdD4=",
	"data:image/png;base64,AAAA", // allowed on an image, never on a link
	"file:///etc/passwd",
	"ftp://example.com/x",
	"notes:1.md", // scheme-shaped, so it is a scheme, and not one of the three
	"tel:+15551234",
	"about:blank",
}

// rejectedImages are destinations no image may carry.
var rejectedImages = []string{
	"javascript:alert(1)",
	"mailto:someone@example.com", // allowed on a link, never on an image
	"data:image/svg+xml;base64,PHN2Zz4=",
	"data:text/html,<script>alert(1)</script>",
	"data:image/png", // no comma, so no media type
	"data:,AAAA",     // the default media type is text/plain
	"data:base64,AAAA",
	"data:image/png;charset=utf-8;base64,AAAA",
}

func TestValidateMarkdownAccepts(t *testing.T) {
	for _, s := range accepted {
		assert.NoError(t, domain.ValidateMarkdown("description", s), "%q", s)
	}
}

// A bare URL the GFM autolink extension picks up is a link, so it meets the
// link rule like any other. That is deliberate rather than incidental: what it
// renders as is a destination the reader can click.
func TestValidateMarkdownAppliesTheLinkRuleToAutolinkedProse(t *testing.T) {
	assertUsage(t, domain.ValidateMarkdown("description", "see ftp://example.com/x for the archive"))

	// Wrapping it takes it out of the rule, because then it is no longer a link.
	require.NoError(t, domain.ValidateMarkdown("description", "see `ftp://example.com/x` for the archive"))
}

func TestValidateMarkdownRejectsRawHTML(t *testing.T) {
	// Raw HTML is refused rather than escaped, which is what keeps script,
	// style, SVG and MathML out: every one of them is raw HTML to the parser.
	for _, s := range []string{
		"<script>alert(1)</script>",
		"<style>body{display:none}</style>",
		"<svg onload=alert(1)></svg>",
		"<math><mtext></mtext></math>",
		"<div>block</div>",
		"<p>\n\n**still a block**\n\n</p>",
		"inline <b>bold</b> html",
		"inline <img src=x onerror=alert(1)>",
		"an <a href=\"https://example.com\">anchor</a> written by hand",
		"<!-- a comment -->",
		"prose with a bare <placeholder> in it",
		"| a |\n|---|\n| <b>x</b> |\n",
		"- <span>in a list</span>\n",
	} {
		err := domain.ValidateMarkdown("description", s)
		assertUsage(t, err, "%q", s)
		assert.Contains(t, err.Error(), "raw HTML", "%q", s)
	}
}

func TestValidateMarkdownRejectsLinkSchemes(t *testing.T) {
	// Every syntax that produces a link destination goes through the same gate.
	forms := []func(string) string{
		func(d string) string { return "[a](" + d + ")" },
		func(d string) string { return "[a](<" + d + ">)" },
		func(d string) string { return "[a][ref]\n\n[ref]: " + d },
		func(d string) string { return "<" + d + ">" },
		func(d string) string { return "[![alt](https://example.com/i.png)](" + d + ")" },
	}
	for _, destination := range rejectedLinks {
		for _, form := range forms {
			s := form(destination)
			err := domain.ValidateMarkdown("description", s)
			assertUsage(t, err, "%q", s)
			assert.Contains(t, err.Error(), "link", "%q", s)
		}
	}
}

func TestValidateMarkdownRejectsImageSchemes(t *testing.T) {
	for _, destination := range rejectedImages {
		for _, s := range []string{
			"![a](" + destination + ")",
			"![a][ref]\n\n[ref]: " + destination,
			"[link](https://example.com/x) ![a](" + destination + ")",
		} {
			err := domain.ValidateMarkdown("description", s)
			assertUsage(t, err, "%q", s)
			assert.Contains(t, err.Error(), "image", "%q", s)
		}
	}
}

func TestValidateMarkdownSeesThroughMarkdownEscaping(t *testing.T) {
	// CommonMark resolves backslash escapes and character references inside a
	// destination, so the scheme is read after resolving them; a URL parser
	// ignores whitespace and control characters within a destination, so those
	// go too. Every one of these is javascript: by the time a renderer has it.
	for _, s := range []string{
		"[a](&#106;avascript:alert&#40;1&#41;)",
		"[a](&#x6a;avascript:alert(1))",
		"[a](javascript\\:alert(1))",
		"[a](<java\tscript:alert(1)>)",
		"[a](  javascript:alert(1)  )",
		"[a](java&NewLine;script:alert(1))",
	} {
		assertUsage(t, domain.ValidateMarkdown("description", s), "%q", s)
	}

	// Percent-encoding is not undone: it survives into the URL rather than
	// being removed on the way there, so %6a is a literal path character and
	// the destination is a relative reference to every browser.
	require.NoError(t, domain.ValidateMarkdown("description", "[a](%6aavascript:alert(1))"))

	// Nor is a backslash the escaping left in place: \s is a literal backslash
	// and an s, so the colon that follows separates nothing.
	require.NoError(t, domain.ValidateMarkdown("description", "[a](java\\script:alert(1))"))
}

func TestValidateMarkdownNamesTheField(t *testing.T) {
	err := domain.ValidateMarkdown("close reason", "<b>no</b>")
	assertUsage(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "close reason "), err.Error())

	// The refusal names the markup, which is the only way to find a stray < in
	// 64 KiB of prose.
	err = domain.ValidateMarkdown("description", strings.Repeat("prose. ", 200)+"a bare <placeholder> here")
	assertUsage(t, err)
	assert.Contains(t, err.Error(), `"<placeholder>"`, err.Error())

	// A long data URL is abbreviated rather than echoed whole.
	err = domain.ValidateMarkdown("description", "![a](data:text/html;base64,"+strings.Repeat("A", 500)+")")
	assertUsage(t, err)
	assert.Less(t, len(err.Error()), 200, err.Error())
}

// The gate is part of every prose field's validation, not something a caller
// opts into.
func TestProseFieldsApplyTheMarkdownGate(t *testing.T) {
	_, err := domain.ValidateDescription("<script>alert(1)</script>")
	assertUsage(t, err)

	_, err = domain.ValidateComment("[a](javascript:alert(1))")
	assertUsage(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "comment "), err.Error())

	// A close reason becomes the body of a Markdown comment, so it passes too.
	_, err = domain.ValidateCloseReason("done, see [why](javascript:alert(1))")
	assertUsage(t, err)

	got, err := domain.ValidateCloseReason("  done, see [why](https://example.com/x)  ")
	require.NoError(t, err)
	assert.Equal(t, "done, see [why](https://example.com/x)", got)
}
