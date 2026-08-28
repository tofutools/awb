package cli

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
)

// drawn renders a description the way a terminal of the given width and the
// given colour setting would see it.
func drawn(mode config.ColorMode, width int, description string) string {
	return newTheme(mode, width > 0, width).markdown(description)
}

// A description is Markdown only when there is a window to draw it in. Piped or
// redirected it is the source text, byte for byte, because that is what a
// captured log and anything reading one wants.
func TestMarkdownIsDrawnOnlyToAWindow(t *testing.T) {
	source := "# Heading\n\nSome **bold** text.\n"

	assert.Equal(t, source, drawn(config.ColorAuto, 0, source))
	assert.Equal(t, source, drawn(config.ColorAlways, 0, source),
		"colour is asked for; a window either is there or is not")

	assert.Equal(t, "Heading\n\nSome bold text.", drawn(config.ColorNever, 60, source))
}

// Emphasis, headings and links are drawn as the terminal's own marking, so the
// markers that asked for them are gone from the text.
func TestMarkdownDrawsEmphasisRatherThanItsMarkers(t *testing.T) {
	out := drawn(config.ColorAlways, 60,
		"# Operator guide\n\nHow to **install** and *configure* it.\n")

	assert.NotContains(t, out, "#")
	assert.NotContains(t, out, "**")
	assert.NotContains(t, out, "*configure*")

	assert.Contains(t, out, "\x1b[1mOperator", "the heading is bold")
	assert.Contains(t, out, "\x1b[1minstall\x1b[m", "two markers are bold")
	assert.Contains(t, out, "\x1b[3mconfigure\x1b[m", "one marker is italic")
}

// A link is one the terminal can open, which OSC 8 is how to say. A terminal
// that does not understand the sequence ignores it and shows the text, and awb
// show lists the destinations as plain text either way.
func TestMarkdownLinksAreClickable(t *testing.T) {
	const url = "https://example.com/widgets/style"
	out := drawn(config.ColorAlways, 60, "Follow the [style guide]("+url+").\n")

	assert.Contains(t, out, "\x1b]8;;"+url+"\x07", "the link opens")
	assert.Contains(t, out, "style guide")
	assert.Contains(t, out, "\x1b]8;;\x07", "and is closed again")
	assert.NotContains(t, out, "("+url+")", "the destination is not written out as text")

	// A GFM autolink carries the destination the link list reports, mailto: and
	// all, so the two surfaces agree on what the dialect yields.
	mail := drawn(config.ColorAlways, 60, "Ask alice@example.com about it.\n")
	assert.Contains(t, mail, "\x1b]8;;mailto:alice@example.com\x07")
	assert.Contains(t, mail, "alice@example.com\x1b[m\x1b]8;;\x07")
}

// Colour off means no escape sequence at all, hyperlinks included: the drawing
// stays, the marking goes. That is the same split the rest of the default mode
// makes between what is asked for and what the window is.
func TestMarkdownWithoutColourEmitsNoEscapes(t *testing.T) {
	out := drawn(config.ColorNever, 60,
		"# Guide\n\nRun **awb** and see [the guide](https://example.com/g).\n")

	assert.NotContains(t, out, "\x1b")
	assert.Contains(t, out, "Guide")
	assert.Contains(t, out, "Run awb and see the guide.")
}

// Prose is folded to the window and structure is drawn around it: a list is
// marked and indented, a quotation ruled, code left as it was written.
func TestMarkdownDrawsBlocks(t *testing.T) {
	out := drawn(config.ColorNever, 40,
		"1. First\n2. Second\n   - nested\n\n- [ ] todo\n- [x] done\n\n"+
			"> quoted\n\n```\nawb ready --compact\n```\n\n---\n")
	got := strings.Split(out, "\n")

	assert.Contains(t, got, "1. First")
	assert.Contains(t, got, "2. Second")
	assert.Contains(t, got, "   • nested", "a nested list sits under its item")
	assert.Contains(t, got, "• [ ] todo")
	assert.Contains(t, got, "• [x] done")
	assert.Contains(t, got, "│ quoted")
	assert.Contains(t, got, "  awb ready --compact")
	assert.Contains(t, got, strings.Repeat("─", 40))
}

// Nothing drawn is wider than the window, and no line of it carries trailing
// whitespace, which would be noise in a captured file.
func TestMarkdownFitsTheWindow(t *testing.T) {
	source := "# A heading long enough that it has to be folded somewhere\n\n" +
		"A paragraph of prose that is a good deal wider than any of these windows, " +
		"with a [link](https://example.com/some/rather/long/destination) in it.\n\n" +
		"> - a list inside a quotation, itself long enough to need folding\n\n" +
		"| Command | What it does |\n| --- | --- |\n" +
		"| awb ready | Open, unblocked, unassigned issues, highest priority first |\n"

	for _, width := range []int{40, 60, 100} {
		for _, mode := range []config.ColorMode{config.ColorNever, config.ColorAlways} {
			for _, line := range strings.Split(drawn(mode, width, source), "\n") {
				assert.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, line)
				assert.Equal(t, strings.TrimRight(line, " "), line, "width %d: %q", width, line)
			}
		}
	}
}

// Folding rewraps prose; it never drops any of it, whatever markup the prose
// carries.
func TestMarkdownKeepsEveryWord(t *testing.T) {
	out := drawn(config.ColorNever, 30,
		"How to **install**, *configure* and back the service up, in that order, "+
			"following the [documentation style guide](https://example.com/style).\n")

	assert.Equal(t,
		strings.Fields("How to install, configure and back the service up, in that order, "+
			"following the documentation style guide."),
		strings.Fields(out))
}

// The renderer drops exactly what domain.ExtractLinks drops, so the two never
// disagree about what a description says: raw HTML is markup rather than text,
// and an image contributes nothing at all, alt text included.
func TestMarkdownDropsWhatTheLinksDo(t *testing.T) {
	assert.Empty(t, drawn(config.ColorNever, 60, "<p>raw</p>\n"))
	assert.Empty(t, drawn(config.ColorNever, 60, "![alt text](picture.png)\n"))
	// The space each side of an image is prose and stays; only the image goes.
	assert.Equal(t, []string{"before", "after"},
		strings.Fields(drawn(config.ColorNever, 60, "before ![alt](p.png) after\n")))
}

// A description that draws as nothing prints nothing, the blank line above it
// included.
func TestEmptyDescriptionPrintsNothing(t *testing.T) {
	issue := &domain.Issue{ID: "demo-bff7dc", Title: "Search", Status: domain.StatusOpen}
	for _, description := range []string{"", "   \n", "<p>raw</p>\n"} {
		issue.Description = description
		out := render(60, func(e *env) { e.printIssueDetail(issue) })
		assert.NotContains(t, out, "\n\n", "%q", description)
	}
}

// A table is drawn as the aligned columns the rest of the default mode uses.
func TestMarkdownDrawsTables(t *testing.T) {
	out := drawn(config.ColorNever, 60,
		"| Key | Name |\n| --- | --- |\n| demo | DEMO |\n| awb | Agent Work Board |\n")
	got := strings.Split(out, "\n")

	require.Len(t, got, 3)
	assert.Equal(t, "Key   Name", got[0])
	assert.Equal(t, "demo  DEMO", got[1])
	assert.Equal(t, "awb   Agent Work Board", got[2])
}

// A table is fitted to the window rather than allowed to overflow it, because
// it is read across: the widest column gives way first, and a cell cut short
// still says which column it is in.
func TestNarrowTableIsCutRatherThanOverflowed(t *testing.T) {
	source := "| Key | What it holds |\n| --- | --- |\n" +
		"| demo | A sample project for trying awb out, replaced wholesale each run |\n"

	wide := strings.Split(drawn(config.ColorNever, 100, source), "\n")
	assert.Equal(t, "demo  A sample project for trying awb out, replaced wholesale each run",
		wide[1], "with room to spare nothing is cut")

	for _, width := range []int{20, 40, 60} {
		got := strings.Split(drawn(config.ColorNever, width, source), "\n")
		require.Len(t, got, 2)
		for _, line := range got {
			assert.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, line)
		}
		assert.Contains(t, got[1], "demo", "width %d keeps the narrow column whole", width)
		assert.Contains(t, got[1], "\u2026", "width %d cuts the wide one", width)
	}
}

// A link destination is never shown, so nothing would reveal that it carried a
// byte the terminal reads as a control — and one of those would end the
// sequence the hyperlink is written as and leave the rest of the destination
// driving the terminal. They are percent-encoded; everything else is passed
// through as written.
func TestHyperlinkDestinationsCannotDriveTheTerminal(t *testing.T) {
	assert.Equal(t, "https://example.com/a?b=c&d=%C3%A9",
		safeURL("https://example.com/a?b=c&d=%C3%A9"))
	assert.Equal(t, "https://example.com/\u00a3", safeURL("https://example.com/\u00a3"),
		"a C1 lead byte before an ordinary character is not a control")

	assert.Equal(t, "https://x/%07", safeURL("https://x/\a"), "BEL ends an OSC 8 sequence")
	assert.Equal(t, "https://x/%1B]0;pwned%07", safeURL("https://x/\x1b]0;pwned\a"))
	assert.Equal(t, "https://x/%7F%00", safeURL("https://x/\x7f\x00"))
	assert.Equal(t, "https://x/%C2%9C", safeURL("https://x/\u009c"),
		"the C1 string terminator, which also ends the sequence")

	// And the renderer uses it, so no description puts one on the wire.
	out := drawn(config.ColorAlways, 60, "See [it](https://x/\x1b]0;pwned\a).\n")
	assert.NotContains(t, out, "\x1b]0;pwned")
	assert.Contains(t, out, "\x1b]8;;https://x/%1B]0;pwned%07\x07")
}

// A project's description is drawn the same way an issue's is, which is the
// point of the two going through one renderer.
func TestProjectDetailDrawsItsDescription(t *testing.T) {
	project := &domain.Project{Key: "demo", Name: "DEMO", ActiveIssues: 7,
		Description: "A sample project for trying **awb** out.\n"}

	out := render(60, func(e *env) { e.printProjectDetail(project) })
	assert.Contains(t, out, "demo  DEMO")
	assert.Contains(t, out, "Open:")
	assert.Contains(t, out, "A sample project for trying awb out.")
	assert.NotContains(t, out, "**")
}
