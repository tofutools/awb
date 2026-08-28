package cli

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/tofutools/awb/internal/domain"
)

// A description is Markdown, and on a terminal it is drawn as Markdown:
// emphasis as emphasis, a heading as a heading, a link as one the terminal can
// open.
//
// This is the default output mode only, and only when there is a window. Under
// --json and --compact, and when stdout is a pipe or a file, the description is
// the source text exactly as it was written — the same rule the rest of the
// default mode follows, and what a captured log or a script wants.
//
// The dialect is the one internal/domain pins, so the links a description
// yields, the web UI and this all read the same document.
//
// Rendering is best-effort by nature: what a terminal makes of a hyperlink, of
// italics or of a strikethrough is the terminal's own business, and one that
// understands none of them shows the text and nothing else. That is why nothing
// here is the only way to reach anything: awb show still lists the links it
// found in the description, with their destinations, as plain text.

const (
	// The narrowest prose is ever folded to. Indentation subtracts from the
	// window — a nested list inside a quotation costs several levels of it — and
	// below this width folding produces a column of syllables, so the text is
	// allowed to overflow instead.
	minimumProseWidth = 20

	bulletMarker = "• "
	quoteMarker  = "│ "
	codeIndent   = "  "
)

// markdown draws a description for the window it is being printed to, and
// returns "" when the document holds nothing that is drawn.
func (t *theme) markdown(description string) string {
	if !t.boxed {
		return description
	}
	source := []byte(description)
	r := &markdownRenderer{t: t, source: source}
	doc := domain.Markdown().Parser().Parse(text.NewReader(source))
	return strings.Join(r.blocks(doc, t.width), "\n")
}

// markdownRenderer draws one document. It holds the source because goldmark's
// nodes carry positions into it rather than text.
type markdownRenderer struct {
	t      *theme
	source []byte
}

// blocks draws the block children of a node, one blank line between each, and
// returns no lines at all for a node whose children all draw as nothing.
func (r *markdownRenderer) blocks(n ast.Node, width int) []string {
	return r.children(n, width, true)
}

// children draws the block children of a node. They are separated by a blank
// line everywhere but inside the item of a tight list, which is what tight
// means: a list written without blank lines reads without them too, and a list
// nested in one follows its marker straight away.
func (r *markdownRenderer) children(n ast.Node, width int, separated bool) []string {
	var out []string
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		lines := r.block(child, width)
		if len(lines) == 0 {
			continue
		}
		if separated && len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	return out
}

func (r *markdownRenderer) block(n ast.Node, width int) []string {
	switch v := n.(type) {
	case *ast.Heading:
		// A heading is bold and carries no other marking. Its level is structure
		// a terminal has no good way to show, and the reader has the nesting of
		// the text itself.
		return fold(r.inline(n, r.t.header), width)
	case *ast.List:
		return r.list(v, width)
	case *ast.Blockquote:
		return r.quote(v, width)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return r.code(n)
	case *ast.ThematicBreak:
		return []string{r.t.apply(r.t.dim, strings.Repeat("─", max(width, minimumProseWidth)))}
	case *east.Table:
		return r.table(v, width)
	case *ast.HTMLBlock:
		// Raw HTML is markup rather than text, which is what it is to
		// domain.ExtractLinks too.
		return nil
	default:
		// A paragraph, the text block of a tight list item, and whatever the
		// dialect grows later: prose, folded to the width it is given.
		return fold(r.inline(n, lipgloss.NewStyle()), width)
	}
}

// fold folds one run of prose to the width it is given.
func fold(s string, width int) []string {
	if s == "" {
		return nil
	}
	return strings.Split(lipgloss.Wrap(s, max(width, minimumProseWidth), ""), "\n")
}

// list draws a list, its items marked and its continuation lines indented to
// sit under the first. A loose list — one written with blank lines between its
// items — reads that way too.
func (r *markdownRenderer) list(n *ast.List, width int) []string {
	var out []string
	number := n.Start
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		marker := bulletMarker
		if n.IsOrdered() {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		indent := strings.Repeat(" ", lipgloss.Width(marker))

		lines := r.children(item, width-lipgloss.Width(marker), !n.IsTight)
		if len(lines) == 0 {
			// An item whose whole content is drawn as nothing still holds its
			// place, because the numbering of the ones after it depends on it.
			lines = []string{""}
		}
		for i, line := range lines {
			switch {
			case i == 0 && line == "":
				out = append(out, r.t.apply(r.t.dim, strings.TrimRight(marker, " ")))
			case i == 0:
				out = append(out, r.t.apply(r.t.dim, marker)+line)
			case line == "":
				out = append(out, "")
			default:
				out = append(out, indent+line)
			}
		}
		if !n.IsTight && item.NextSibling() != nil {
			out = append(out, "")
		}
	}
	return out
}

// quote draws a quotation with a rule down its left edge.
func (r *markdownRenderer) quote(n ast.Node, width int) []string {
	marked := r.t.apply(r.t.dim, quoteMarker)
	blank := r.t.apply(r.t.dim, strings.TrimRight(quoteMarker, " "))

	inner := r.blocks(n, width-lipgloss.Width(quoteMarker))
	out := make([]string, len(inner))
	for i, line := range inner {
		if line == "" {
			out[i] = blank
			continue
		}
		out[i] = marked + line
	}
	return out
}

// code draws a code block as it was written: folding code changes what it says,
// so a line wider than the window overflows rather than being broken.
func (r *markdownRenderer) code(n ast.Node) []string {
	segments := n.Lines()
	out := make([]string, 0, segments.Len())
	for i := range segments.Len() {
		segment := segments.At(i)
		line := strings.TrimRight(string(segment.Value(r.source)), "\r\n")
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, codeIndent+r.t.apply(r.t.code, line))
	}
	return out
}

// table draws a GFM table as the aligned columns every other section of the
// default mode uses, its heading row bold, and fitted to the window the way a
// listing is. The column alignment the table declares is not honoured: what the
// reader is after is which cell is which.
func (r *markdownRenderer) table(n *east.Table, width int) []string {
	var rows [][]string
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		base := lipgloss.NewStyle()
		if _, heading := row.(*east.TableHeader); heading {
			base = r.t.header
		}
		var cells []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, r.inline(cell, base))
		}
		rows = append(rows, cells)
	}
	fitColumns(rows, max(width, minimumProseWidth))

	tbl := section()
	for _, cells := range rows {
		tbl.Row(cells...)
	}
	rendered := strings.TrimRight(trimRight(tbl.Render()), "\n")
	if rendered == "" {
		return nil
	}
	return strings.Split(rendered, "\n")
}

// fitColumns cuts a drawn table down to the window, taking a column from
// whichever is widest until the whole fits. A table is read across rather than
// down, so a cell cut short still says which column it is in, where a table
// wider than the window says nothing at all: this is the one block that is
// fitted rather than allowed to overflow, code being the other way about
// because folding code changes what it says.
//
// The two spaces between one column and the next are what hold them apart, so
// they are the part that cannot be given up.
func fitColumns(rows [][]string, width int) {
	columns := 0
	for _, cells := range rows {
		columns = max(columns, len(cells))
	}
	if columns == 0 {
		return
	}

	widths := make([]int, columns)
	for _, cells := range rows {
		for i, cell := range cells {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}

	room := width - 2*(columns-1)
	for {
		used, widest := 0, 0
		for i, w := range widths {
			used += w
			if w > widths[widest] {
				widest = i
			}
		}
		// Nothing left to give: a column is never cut below the one character
		// and the mark that says the rest was cut.
		if used <= room || widths[widest] <= 1 {
			break
		}
		widths[widest]--
	}

	for _, cells := range rows {
		for i, cell := range cells {
			cells[i] = truncate(cell, widths[i])
		}
	}
}

// inline draws the inline children of a node.
//
// The style is carried down rather than nested, because a nested style ends
// with a reset and that reset would end the style around it too: a bold word
// inside a heading has to be drawn as one style, not as one inside another.
func (r *markdownRenderer) inline(n ast.Node, base lipgloss.Style) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch v := child.(type) {
		case *ast.Text:
			b.WriteString(r.t.apply(base, string(v.Segment.Value(r.source))))
			// A hard break is a line the writer asked for; a soft one is a fold
			// in the source, and folding here is this renderer's own business.
			switch {
			case v.HardLineBreak():
				b.WriteByte('\n')
			case v.SoftLineBreak():
				b.WriteByte(' ')
			}
		case *ast.String:
			b.WriteString(r.t.apply(base, string(v.Value)))
		case *ast.Emphasis:
			// One marker is italic, two are bold, and goldmark nests the two for
			// the three-marker form.
			style := base.Italic(true)
			if v.Level >= 2 {
				style = base.Bold(true)
			}
			b.WriteString(r.inline(v, style))
		case *east.Strikethrough:
			b.WriteString(r.inline(v, base.Strikethrough(true)))
		case *ast.CodeSpan:
			b.WriteString(r.inline(v, base.Inherit(r.t.code)))
		case *ast.Link:
			b.WriteString(r.inline(v, r.hyperlink(base, string(v.Destination))))
		case *ast.AutoLink:
			// The same destination domain.ExtractLinks reports, mailto: and all.
			url := domain.AutoLinkURL(v, r.source)
			b.WriteString(r.t.apply(r.hyperlink(base, url), string(v.Label(r.source))))
		case *east.TaskCheckBox:
			b.WriteString(r.t.apply(base, taskBox(v.IsChecked)))
		case *ast.Image:
			// Images are ignored, alt text included, exactly as they are to
			// domain.ExtractLinks.
		case *ast.RawHTML:
			// markup, not text
		default:
			b.WriteString(r.inline(child, base))
		}
	}
	return b.String()
}

// hyperlink marks a run of text as a link the terminal can open, which it does
// with OSC 8. A terminal that does not understand the sequence ignores it and
// shows the text, so nothing is lost but the click.
func (r *markdownRenderer) hyperlink(base lipgloss.Style, url string) lipgloss.Style {
	if url == "" {
		return base
	}
	return base.Inherit(r.t.link).Hyperlink(safeURL(url))
}

// safeURL percent-encodes the bytes a terminal reads as a control: the C0
// range and DEL, and the two-byte form of a C1 control. A destination goes into
// the sequence the hyperlink is written as, and one of those bytes would end
// that sequence early and leave the rest of the destination driving the
// terminal. Everything else is passed through byte for byte, so the link opens
// what the writer wrote.
//
// This is the one place a description reaches the terminal without being shown,
// which is why the guard is here rather than left to the input rules: those
// already refuse every one of these bytes in a description, but a database
// written by something other than awb, or reached over --db, has not been
// through them. Text that is shown is a different matter and is printed as
// stored, exactly as a title or a label is.
func safeURL(url string) string {
	var b strings.Builder
	for i := 0; i < len(url); i++ {
		c := url[i]
		switch {
		// A C0 control or DEL. Neither is ever a byte of a multi-byte UTF-8
		// sequence, so testing bytes rather than runes is exact and works on a
		// destination that is not well-formed UTF-8 either.
		case c < 0x20 || c == 0x7f:
			writePercent(&b, c)
		// The UTF-8 form of a C1 control, which an eight-bit terminal reads as
		// one. 0xC2 leads U+0080 to U+00BF and only the first 32 are controls,
		// so £ and its neighbours are left alone.
		case c == 0xc2 && i+1 < len(url) && url[i+1] >= 0x80 && url[i+1] <= 0x9f:
			writePercent(&b, c)
			writePercent(&b, url[i+1])
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func writePercent(b *strings.Builder, c byte) {
	const hex = "0123456789ABCDEF"
	b.WriteByte('%')
	b.WriteByte(hex[c>>4])
	b.WriteByte(hex[c&0x0f])
}

// taskBox draws a GFM task list checkbox. It carries its own trailing space:
// goldmark's checkbox node has consumed the one written after it.
func taskBox(checked bool) string {
	if checked {
		return "[x] "
	}
	return "[ ] "
}
