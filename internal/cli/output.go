package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
)

// The three output modes.
//
// The default mode is an aligned, coloured table for humans. Its columns,
// widths and truncation are deliberately unspecified and are not a
// compatibility surface: nothing should parse it, and it may change between
// versions. --compact and --json are what a script or an agent reads.

// writeJSON prints one object or one array per invocation, indented for jq and
// for a human reading it directly.
func (e *env) writeJSON(value any) error {
	enc := json.NewEncoder(e.stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// issueJSON and projectJSON are the CLI's JSON presentation shapes. The API
// returns the domain objects themselves, but a remote CLI also knows the web
// address that presents each object and includes it as navigation metadata.
// The fields remain present and empty in direct mode, where no web address
// exists to name.
type issueJSON struct {
	*domain.Issue
	IssueLink   string `json:"issue_link"`
	ProjectLink string `json:"project_link"`
}

type projectJSON struct {
	*domain.Project
	ProjectLink string `json:"project_link"`
}

type issueTreeJSON struct {
	issueJSON
	Children []issueTreeJSON `json:"children"`
}

func (e *env) issueJSON(issue *domain.Issue) issueJSON {
	return issueJSON{
		Issue:       issue,
		IssueLink:   e.issueURL(issue.ID),
		ProjectLink: e.projectURL(issue.Project),
	}
}

func (e *env) projectJSON(project *domain.Project) projectJSON {
	return projectJSON{Project: project, ProjectLink: e.projectURL(project.Key)}
}

func (e *env) issueTreeJSON(node *domain.IssueTree) issueTreeJSON {
	children := make([]issueTreeJSON, len(node.Children))
	for i := range node.Children {
		children[i] = e.issueTreeJSON(&node.Children[i])
	}
	return issueTreeJSON{issueJSON: e.issueJSON(&node.Issue), Children: children}
}

func (e *env) writeIssueJSON(issue *domain.Issue) error {
	return e.writeJSON(e.issueJSON(issue))
}

func (e *env) writeProjectJSON(project *domain.Project) error {
	return e.writeJSON(e.projectJSON(project))
}

const (
	// What to assume a terminal is when it will not say how wide it is.
	assumedWidth = 100
	// A window narrower than this leaves a listing no room whatever is done to
	// it, so below this it is simply allowed to overflow.
	minimumWidth = 40
	// What a title is cut to when there is no window to fit it to.
	unboxedTitleWidth = 60
)

// theme carries every decision the default mode makes about how to draw. There
// are two switches, each decided once per invocation:
//
//   - colour follows the colour chain, so --color always still colours a pipe.
//   - boxed follows stdout alone: a box drawn around a listing, and fitting
//     that listing to the window, only mean something when there is a window to
//     draw in. A pipe or a file gets the same content as plain aligned columns
//     at its natural width, which is what a human reading a captured log wants.
//
// When colour is off every style is the identity and when boxed is off every
// border is blank, so one set of rendering code produces both.
type theme struct {
	color bool
	boxed bool
	// width is the window in columns, and is meaningful only when boxed.
	width int

	header   lipgloss.Style
	dim      lipgloss.Style
	id       lipgloss.Style
	blocked  lipgloss.Style
	closed   lipgloss.Style
	assignee lipgloss.Style
	label    lipgloss.Style
	border   lipgloss.Style
	link     lipgloss.Style
	code     lipgloss.Style
	priority [domain.MaxPriority + 1]lipgloss.Style
}

// apply draws text in a style, or returns it unchanged when colour is off.
//
// A style around nothing is nothing. Rendering "" would otherwise produce the
// escape sequence that starts the style and the one that ends it, with no
// character between them — a value that is empty to look at but not empty to
// test, which is how an issue with no assignee came to print an Assignee line
// with nothing after it whenever colour was on.
func (t *theme) apply(style lipgloss.Style, text string) string {
	if !t.color || text == "" {
		return text
	}
	return style.Render(text)
}

// newTheme decides how to draw, per the colour chain and per what Execute found
// stdout to be. In auto mode colour too follows the terminal.
func newTheme(mode config.ColorMode, boxed bool, width int) *theme {
	t := &theme{boxed: boxed, width: width}
	switch mode {
	case config.ColorAlways:
		t.color = true
	case config.ColorNever:
		t.color = false
	case config.ColorAuto:
		t.color = boxed
	}
	if !t.color {
		return t
	}

	t.header = lipgloss.NewStyle().Bold(true)
	t.dim = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.id = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	t.blocked = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	t.closed = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.assignee = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	t.label = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	t.border = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	// The two colours a rendered description adds: a link, and code that is not
	// to be read as prose.
	t.link = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	t.code = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	// P0 is the most urgent, P4 the least, so the palette runs hot to cool.
	t.priority = [domain.MaxPriority + 1]lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
	return t
}

// window reports whether w is a terminal and how many columns it has. A writer
// that is not one — a pipe, a file, a test's buffer — has no width, and the
// default mode then lays out to whatever width its content needs.
//
// A terminal is one the operating system says is a terminal, not merely a
// character device: /dev/null is a character device and is nobody's window.
func window(w io.Writer) (isTerminal bool, width int) {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return false, 0
	}
	width, _, err := term.GetSize(f.Fd())
	if err != nil || width <= 0 {
		return true, assumedWidth
	}
	return true, max(width, minimumWidth)
}

func (e *env) theme() *theme {
	mode := config.ColorAuto
	if e.cfg != nil {
		mode = e.cfg.Color
	}
	return newTheme(mode, e.boxed, e.width)
}

// A listing is one column at a time, because fitting it to the window is a
// decision about columns: which ones the reader can be asked to do without, and
// which ones can be cut without becoming meaningless.
type col struct {
	header string
	cells  []string

	// floor is the narrowest width at which this column still says something.
	// Zero means the column is short and bounded already — an id, a priority, a
	// status — and is never cut, because half of one says nothing at all.
	floor int
	// expendable marks a column that is given up whole when cutting the others
	// to their floors is still not enough. The rightmost one goes first.
	expendable bool
	// right right-aligns the cells, which is what a column of counts wants.
	right bool
	// paint gives the colour of one row's cell.
	paint func(row int) lipgloss.Style
}

// The narrowest width at which each free-text column still tells the reader
// something. A column goes below its floor only once every column that could be
// given up has been, and never below its own heading: a heading with its end
// cut off says nothing about what the column holds.
const (
	titleFloor    = 24
	labelsFloor   = 10
	blockersFloor = 13
	nameFloor     = 12
	typeFloor     = 12
	adminFloor    = 8
)

// always paints every row of a column the same.
func always(style lipgloss.Style) func(int) lipgloss.Style {
	return func(int) lipgloss.Style { return style }
}

// fit lays the columns out to the window: expendable columns are given up,
// rightmost first, while what remains cannot be shown even at its floors, and
// then the columns that can be cut give up whatever room is still needed.
//
// Without a window there is nothing to fit to, so every column is kept whole.
func (t *theme) fit(cols []col) []col {
	if !t.boxed {
		return cols
	}
	widths := t.solve(cols)
	for !t.fits(cols, widths) {
		last := -1
		for i, c := range cols {
			if c.expendable {
				last = i
			}
		}
		if last < 0 {
			break
		}
		cols = append(cols[:last:last], cols[last+1:]...)
		widths = t.solve(cols)
	}
	// Nothing is left to give up and it still will not fit, so the floors give
	// way too: a title cut short says more than a box wider than the window.
	t.shrink(cols, widths, hardFloor)
	for i := range cols {
		for j, cell := range cols[i].cells {
			cols[i].cells[j] = truncate(cell, widths[i])
		}
	}
	return cols
}

// solve gives every column its natural width, less whatever the columns that
// can be cut have to give up for the whole to fit the window without any of
// them falling below the width it stops saying anything at.
func (t *theme) solve(cols []col) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = lipgloss.Width(c.header)
		for _, cell := range c.cells {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	t.shrink(cols, widths, func(c col) int { return c.floor })
	return widths
}

// shrink takes a column off whichever has the most room above the floor it is
// given, one at a time, until the whole fits the window or nothing can give any
// more. Sharing the shortfall out this way keeps a column that has to be wide
// to be worth reading wide, at the expense of one that does not.
func (t *theme) shrink(cols []col, widths []int, floor func(col) int) {
	for !t.fits(cols, widths) {
		giving, slack := -1, 0
		for i, c := range cols {
			if f := floor(c); f > 0 && widths[i]-f > slack {
				giving, slack = i, widths[i]-f
			}
		}
		if giving < 0 {
			return
		}
		widths[giving]--
	}
}

// hardFloor is the narrowest a column that can be cut at all is ever made: its
// whole heading, and never less than room for a character and the mark that
// says the rest was cut. A column that is never cut has no hard floor either.
func hardFloor(c col) int {
	if c.floor == 0 {
		return 0
	}
	return max(4, lipgloss.Width(c.header))
}

// fits reports whether the columns at these widths fit the window, counting the
// frame: every cell is padded a space each side, and a border rule stands
// between each pair of columns and at both ends.
func (t *theme) fits(cols []col, widths []int) bool {
	total := 3*len(cols) + 1
	for _, w := range widths {
		total += w
	}
	return total <= t.width
}

// writeListing draws a listing: a rounded box with a rule under the headings on
// a terminal, and the same columns two spaces apart with no border anywhere
// else.
func (e *env) writeListing(t *theme, cols []col) {
	cols = t.fit(cols)

	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.header
	}

	tbl := table.New().Headers(headers...).Wrap(false).
		StyleFunc(func(row, c int) lipgloss.Style {
			s := lipgloss.NewStyle()
			if t.boxed {
				// Inside the box the rules do half the separating, so a space
				// each side is enough; without them the gap has to read as one.
				s = s.Padding(0, 1)
			} else {
				s = s.PaddingRight(2)
			}
			if cols[c].right {
				s = s.Align(lipgloss.Right)
			}
			if !t.color {
				return s
			}
			if row == table.HeaderRow {
				return s.Bold(true)
			}
			if cols[c].paint != nil && cols[c].cells[row] != "" {
				return s.Inherit(cols[c].paint(row))
			}
			return s
		})

	if t.boxed {
		tbl.Border(lipgloss.RoundedBorder()).BorderStyle(t.border).BorderRow(false)
	} else {
		tbl.Border(lipgloss.HiddenBorder()).
			BorderTop(false).BorderBottom(false).
			BorderLeft(false).BorderRight(false).
			BorderColumn(false).BorderRow(false).BorderHeader(false)
	}

	for row := range cols[0].cells {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = c.cells[row]
		}
		tbl.Row(cells...)
	}

	_, _ = fmt.Fprintln(e.stdout, trimRight(tbl.Render()))
}

// section returns the borderless, heading-less table that lines up the columns
// of one section of awb show. It never boxes: a box around three words would be
// noise, whatever it is being printed to.
func section() *table.Table {
	return table.New().Wrap(false).
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(false).
		StyleFunc(func(int, int) lipgloss.Style { return lipgloss.NewStyle().PaddingRight(2) })
}

// writeSection prints a section's table under its heading, indented to sit
// beneath it.
func (e *env) writeSection(tbl *table.Table) {
	rendered := strings.TrimRight(trimRight(tbl.Render()), "\n")
	for _, line := range strings.Split(rendered, "\n") {
		_, _ = fmt.Fprintf(e.stdout, "  %s\n", line)
	}
}

// trimRight drops the padding a blank border leaves at the end of every line,
// which would otherwise be trailing whitespace in a captured file.
func trimRight(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// printIssues renders a listing in whichever mode is in force.
//
// withBlockers is set by awb blocked, which shows the ids of the blockers as
// well.
func (e *env) printIssues(issues []domain.Issue, withBlockers bool) error {
	switch {
	case e.json:
		// An empty list renders as [], never as null.
		if issues == nil {
			issues = []domain.Issue{}
		}
		out := make([]issueJSON, len(issues))
		for i := range issues {
			out[i] = e.issueJSON(&issues[i])
		}
		return e.writeJSON(out)
	case e.compact:
		for i := range issues {
			_, _ = fmt.Fprintln(e.stdout, domain.CompactLine(&issues[i], withBlockers))
		}
		return nil
	default:
		e.printIssueTable(issues, withBlockers)
		return nil
	}
}

func (e *env) printIssueTable(issues []domain.Issue, withBlockers bool) {
	if len(issues) == 0 {
		return
	}
	t := e.theme()

	cells := func(text func(*domain.Issue) string) []string {
		out := make([]string, len(issues))
		for i := range issues {
			out[i] = text(&issues[i])
		}
		return out
	}

	cols := []col{
		{header: "ID", paint: always(t.id),
			cells: cells(func(i *domain.Issue) string { return e.issueLink(t, i.ID) })},
		{header: "P", paint: func(row int) lipgloss.Style {
			return t.priority[clampPriority(issues[row].Priority)]
		},
			cells: cells(func(i *domain.Issue) string { return "P" + strconv.Itoa(i.Priority) })},
		{header: "STATUS", paint: func(row int) lipgloss.Style { return t.statusStyle(&issues[row]) },
			cells: cells(statusText)},
		{header: "TYPE", expendable: true,
			cells: cells(func(i *domain.Issue) string { return string(i.Type) })},
		{header: "TITLE", floor: titleFloor,
			cells: cells(func(i *domain.Issue) string { return t.listTitle(i.Title) })},
		{header: "ASSIGNEE", expendable: true, paint: always(t.assignee),
			cells: cells(func(i *domain.Issue) string { return i.Assignee })},
		{header: "LABELS", floor: labelsFloor, expendable: true, paint: always(t.label),
			cells: cells(func(i *domain.Issue) string { return strings.Join(i.Labels, ",") })},
	}
	if withBlockers {
		// awb blocked exists to show this column, so it is never given up.
		cols = append(cols, col{header: "BLOCKED BY", floor: blockersFloor, paint: always(t.blocked),
			cells: cells(func(i *domain.Issue) string { return e.issueLinks(t, i.Blockers, ",") })})
	}

	e.writeListing(t, cols)
}

// entityLink makes an identifier open the bundled web UI when this invocation
// is talking to an awb server. A local database has no web address to name, and
// a pipe is not an interactive terminal, so both keep the identifier as plain
// text. Hyperlinks follow the same escape-sequence switch as the rest of the
// human output: --color never means no terminal escapes at all.
func (e *env) entityLink(t *theme, text, route string) string {
	if !t.boxed || !t.color {
		return text
	}
	destination := e.entityURL(route)
	if destination == "" {
		return text
	}
	return lipgloss.NewStyle().Hyperlink(safeURL(destination)).Render(text)
}

func (e *env) entityURL(route string) string {
	if e.cfg == nil || e.cfg.RemoteURL == nil {
		return ""
	}
	return e.cfg.RemoteURL.String() + "/#" + route
}

func (e *env) issueURL(id string) string {
	return e.entityURL("/issues/" + url.PathEscape(id))
}

func (e *env) projectURL(key string) string {
	query := url.Values{"project": []string{key}}.Encode()
	return e.entityURL("/issues?" + query)
}

func (e *env) issueLink(t *theme, id string) string {
	return e.entityLink(t, id, "/issues/"+url.PathEscape(id))
}

func (e *env) issueLinks(t *theme, ids []string, separator string) string {
	linked := make([]string, len(ids))
	for i, id := range ids {
		linked[i] = e.issueLink(t, id)
	}
	return strings.Join(linked, separator)
}

func (e *env) projectLink(t *theme, key string) string {
	return e.entityLink(t, key, "/issues?"+url.Values{"project": []string{key}}.Encode())
}

// listTitle gives a title whichever width treatment the layout can offer. With
// a window to fit, the listing decides what has to go, and a title can take the
// whole window when there is room for it; without one there is nothing to fit
// to, so a long title is cut at a fixed width to keep the columns aligned.
func (t *theme) listTitle(title string) string {
	if t.boxed {
		return title
	}
	return truncate(title, unboxedTitleWidth)
}

// statusText is the status as a listing shows it. The blocked marker is the one
// thing the column says that the stored status does not.
func statusText(issue *domain.Issue) string {
	if issue.Blocked {
		return string(issue.Status) + " !blocked"
	}
	return string(issue.Status)
}

func (t *theme) statusStyle(issue *domain.Issue) lipgloss.Style {
	switch {
	case issue.Blocked:
		return t.blocked
	case issue.Status == domain.StatusClosed:
		return t.closed
	default:
		return lipgloss.NewStyle()
	}
}

func (e *env) renderStatus(t *theme, issue *domain.Issue) string {
	return t.apply(t.statusStyle(issue), statusText(issue))
}

func clampPriority(p int) int {
	if p < domain.MinPriority {
		return domain.MinPriority
	}
	if p > domain.MaxPriority {
		return domain.MaxPriority
	}
	return p
}

// truncate shortens a cell for the default mode, which is explicitly allowed to
// truncate. It counts what the terminal shows rather than bytes or runes, so a
// cell that is already coloured, or holds a double-width character, still comes
// out the width it was asked for.
func truncate(s string, width int) string {
	return ansi.Truncate(s, width, "…")
}

// printIssue renders one issue.
//
// Under --compact it prints the same single line a listing would and nothing
// else, losing the description, relations and links: --compact means the
// cheapest representation there is, and --json is what an agent uses when it
// needs the rest.
func (e *env) printIssue(issue *domain.Issue) error {
	switch {
	case e.json:
		return e.writeIssueJSON(issue)
	case e.compact:
		_, _ = fmt.Fprintln(e.stdout, domain.CompactLine(issue, false))
		return nil
	default:
		e.printIssueDetail(issue)
		return nil
	}
}

func (e *env) printIssueDetail(issue *domain.Issue) {
	t := e.theme()

	e.writeHeading(t, e.issueLink(t, issue.ID), issue.Title)
	e.field(t, "Project", e.projectLink(t, issue.Project))
	e.field(t, "Type", string(issue.Type))
	e.field(t, "Status", e.renderStatus(t, issue))
	e.field(t, "Priority", "P"+strconv.Itoa(issue.Priority))
	e.field(t, "Assignee", t.apply(t.assignee, issue.Assignee))
	e.field(t, "Labels", t.apply(t.label, strings.Join(issue.Labels, ", ")))
	e.field(t, "Created", issue.CreatedAt)
	e.field(t, "Updated", issue.UpdatedAt)

	if len(issue.Blockers) > 0 {
		e.field(t, "Blocked by", t.apply(t.blocked, e.issueLinks(t, issue.Blockers, ", ")))
	}

	e.writeDescription(t, issue.Description)

	if len(issue.Links) > 0 {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", t.apply(t.header, "Links"))
		tbl := section()
		for _, link := range issue.Links {
			tbl.Row(link.Text, t.apply(t.dim, link.URL))
		}
		e.writeSection(tbl)
	}

	if len(issue.Attachments) > 0 {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", t.apply(t.header, "Attachments"))
		tbl := section()
		for i := range issue.Attachments {
			a := &issue.Attachments[i]
			tbl.Row(a.Name, t.apply(t.dim, humanSize(a.Size)), t.apply(t.dim, a.ContentType))
		}
		e.writeSection(tbl)
	}

	if len(issue.Relations) > 0 {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", t.apply(t.header, "Relations"))
		tbl := section()
		for _, rel := range issue.Relations {
			// Every relation reads "subject — type — other", the single convention
			// everywhere, whichever end is being viewed.
			subject, other := issue.ID, rel.Other
			if rel.Direction == domain.DirectionIn {
				subject, other = rel.Other, issue.ID
			}
			tbl.Row(t.apply(t.id, e.issueLink(t, subject)), string(rel.Type),
				t.apply(t.id, e.issueLink(t, other)))
		}
		e.writeSection(tbl)
	}
}

// writeHeading prints the two-part heading a detail view opens with, and the
// rule that separates it from the fields.
//
// The rule is as long as the heading and no longer. Like the connectors of a
// tree it needs no window to be drawn to, only the heading it underlines, so it
// is drawn either way.
func (e *env) writeHeading(t *theme, id, title string) {
	heading := t.apply(t.id, id) + "  " + t.apply(t.header, title)
	length := lipgloss.Width(heading)
	if t.boxed {
		length = min(length, t.width)
	}
	_, _ = fmt.Fprintln(e.stdout, heading)
	_, _ = fmt.Fprintln(e.stdout, t.apply(t.dim, strings.Repeat("─", length)))
}

// field prints one line of a detail view, and prints nothing at all where there
// is no value.
func (e *env) field(t *theme, name, value string) {
	if value == "" {
		return
	}
	_, _ = fmt.Fprintf(e.stdout, "%s %s\n", t.apply(t.header, pad(name+":", 12)), value)
}

// writeDescription prints a description below the fields, drawn as Markdown
// when there is a terminal to draw it for. A document that draws as nothing —
// an empty description, or one holding only what the renderer drops — prints
// nothing, blank line included.
func (e *env) writeDescription(t *theme, description string) {
	if body := t.markdown(description); body != "" {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", body)
	}
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// printProjects renders a project listing. Under --compact each line is "<key>
// <active_issues> <name>", where the name is a JSON string.
func (e *env) printProjects(projects []domain.Project) error {
	switch {
	case e.json:
		if projects == nil {
			projects = []domain.Project{}
		}
		out := make([]projectJSON, len(projects))
		for i := range projects {
			out[i] = e.projectJSON(&projects[i])
		}
		return e.writeJSON(out)
	case e.compact:
		for i := range projects {
			_, _ = fmt.Fprintln(e.stdout, domain.CompactProjectLine(&projects[i]))
		}
		return nil
	default:
		if len(projects) == 0 {
			return nil
		}
		t := e.theme()
		keys := make([]string, len(projects))
		counts := make([]string, len(projects))
		names := make([]string, len(projects))
		for i := range projects {
			keys[i] = e.projectLink(t, projects[i].Key)
			counts[i] = strconv.Itoa(projects[i].ActiveIssues)
			names[i] = projects[i].Name
		}
		e.writeListing(t, []col{
			{header: "KEY", cells: keys, paint: always(t.id)},
			{header: "OPEN", cells: counts, right: true},
			{header: "NAME", cells: names, floor: nameFloor},
		})
		return nil
	}
}

// printProject renders one project.
//
// Under --compact it prints the same single line a project listing would, and
// so loses the description; --json is what a script reads when it wants the
// rest.
func (e *env) printProject(project *domain.Project) error {
	switch {
	case e.json:
		return e.writeProjectJSON(project)
	case e.compact:
		_, _ = fmt.Fprintln(e.stdout, domain.CompactProjectLine(project))
		return nil
	default:
		e.printProjectDetail(project)
		return nil
	}
}

func (e *env) printProjectDetail(project *domain.Project) {
	t := e.theme()

	e.writeHeading(t, e.projectLink(t, project.Key), project.Name)
	// The same count project list shows, under the heading it uses there.
	e.field(t, "Open", strconv.Itoa(project.ActiveIssues))
	e.field(t, "Created", project.CreatedAt)
	e.field(t, "Updated", project.UpdatedAt)

	e.writeDescription(t, project.Description)
}

// printUsers renders a user listing. Under --compact each line is the compact
// user line.
func (e *env) printUsers(users []domain.User) error {
	switch {
	case e.json:
		if users == nil {
			users = []domain.User{}
		}
		return e.writeJSON(users)
	case e.compact:
		for i := range users {
			_, _ = fmt.Fprintln(e.stdout, domain.CompactUserLine(&users[i]))
		}
		return nil
	default:
		if len(users) == 0 {
			return nil
		}
		t := e.theme()
		names := make([]string, len(users))
		flags := make([]string, len(users))
		projects := make([]string, len(users))
		for i := range users {
			names[i] = users[i].Name
			flags[i] = adminFlags(&users[i])
			projects[i] = memberships(&users[i])
		}
		e.writeListing(t, []col{
			{header: "NAME", cells: names, paint: always(t.id)},
			{header: "ADMIN", cells: flags, floor: adminFloor},
			{header: "PROJECTS", cells: projects, floor: labelsFloor},
		})
		return nil
	}
}

// adminFlags is the two flags as one cell, and "-" when neither is set, so the
// column never reads as a blank that might be a missing value.
func adminFlags(user *domain.User) string {
	switch {
	case user.ProjectAdmin && user.UserAdmin:
		return "projects, users"
	case user.ProjectAdmin:
		return "projects"
	case user.UserAdmin:
		return "users"
	default:
		return "-"
	}
}

// memberships is a user's projects as one cell, each with its access level.
func memberships(user *domain.User) string {
	parts := make([]string, len(user.Projects))
	for i, m := range user.Projects {
		parts[i] = m.Project + ":" + string(m.Access)
	}
	return strings.Join(parts, " ")
}

// printUser renders one user.
//
// Under --compact it prints the same single line a user listing would; --json
// is what a script reads. Neither carries a password: nothing that leaves the
// storage layer does.
func (e *env) printUser(user *domain.User) error {
	switch {
	case e.json:
		return e.writeJSON(user)
	case e.compact:
		_, _ = fmt.Fprintln(e.stdout, domain.CompactUserLine(user))
		return nil
	default:
		t := e.theme()
		e.writeHeading(t, user.Name, adminTitle(user))
		e.field(t, "Created", user.CreatedAt)
		e.field(t, "Updated", user.UpdatedAt)
		e.field(t, "Projects", memberships(user))
		return nil
	}
}

// adminTitle is what the two flags make somebody, in words, and never empty:
// an account holding neither is a user, which is a thing to be.
func adminTitle(user *domain.User) string {
	switch {
	case user.ProjectAdmin && user.UserAdmin:
		return "project and user administrator"
	case user.ProjectAdmin:
		return "project administrator"
	case user.UserAdmin:
		return "user administrator"
	default:
		return "user"
	}
}

// printMemberships renders a project's member listing.
func (e *env) printMemberships(members []domain.Membership) error {
	switch {
	case e.json:
		if members == nil {
			members = []domain.Membership{}
		}
		return e.writeJSON(members)
	case e.compact:
		for i := range members {
			_, _ = fmt.Fprintln(e.stdout, domain.CompactMembershipLine(&members[i]))
		}
		return nil
	default:
		if len(members) == 0 {
			return nil
		}
		t := e.theme()
		users := make([]string, len(members))
		access := make([]string, len(members))
		for i, m := range members {
			users[i] = m.User
			access[i] = string(m.Access)
		}
		e.writeListing(t, []col{
			{header: "USER", cells: users, paint: always(t.id)},
			{header: "ACCESS", cells: access},
		})
		return nil
	}
}

// mutatedUser is what the commands that change a user print.
func (e *env) mutatedUser(user *domain.User) error {
	if e.json {
		return e.writeJSON(user)
	}
	return e.printUser(user)
}

// mutatedMembership is what grant and revoke print.
func (e *env) mutatedMembership(membership *domain.Membership) error {
	if e.json {
		return e.writeJSON(membership)
	}
	_, _ = fmt.Fprintln(e.stdout, domain.CompactMembershipLine(membership))
	return e.stdout.Err()
}

// printTree renders a dependency tree.
//
// Under --compact each node is the ordinary compact issue line prefixed by two
// spaces per level of depth, the root unindented. That prefix is the one thing
// that may precede the id, so a consumer strips the leading spaces, counts
// them to recover the depth, and parses the rest of the line as usual.
func (e *env) printTree(root *domain.IssueTree) error {
	switch {
	case e.json:
		return e.writeJSON(e.issueTreeJSON(root))
	case e.compact:
		e.walkTree(root, 0, func(node *domain.IssueTree, depth int) {
			_, _ = fmt.Fprintln(e.stdout,
				domain.CompactTreePrefix(depth)+domain.CompactLine(&node.Issue, false))
		})
		return nil
	default:
		t := e.theme()
		// The connectors are drawn whether or not stdout is a terminal. Unlike a
		// box they are not decoration: they are the shape of the graph, which is
		// the whole point of the command, and they need no window to be drawn to.
		// Setting a style replaces lipgloss's default one, which is where the
		// space between a connector and what it points at comes from, so the
		// padding is restated here and both styles are set either way: with
		// colour off they are the identity and the layout is the same.
		drawn := e.issueTree(t, root)
		drawn.EnumeratorStyle(t.dim.PaddingRight(1)).IndenterStyle(t.dim.PaddingRight(1))
		_, _ = fmt.Fprintln(e.stdout, drawn.String())
		return nil
	}
}

// issueTree mirrors one node and its children into a lipgloss tree. A child
// tree draws itself with the root's renderer only for as long as it has none of
// its own, so nothing below the root may be styled.
func (e *env) issueTree(t *theme, node *domain.IssueTree) *tree.Tree {
	drawn := tree.Root(e.treeNode(t, node))
	for i := range node.Children {
		drawn.Child(e.issueTree(t, &node.Children[i]))
	}
	return drawn
}

func (e *env) treeNode(t *theme, node *domain.IssueTree) string {
	return fmt.Sprintf("%s  %s  %s  %s",
		t.apply(t.id, e.issueLink(t, node.ID)),
		t.apply(t.priority[clampPriority(node.Priority)], "P"+strconv.Itoa(node.Priority)),
		e.renderStatus(t, &node.Issue),
		truncate(node.Title, unboxedTitleWidth))
}

func (e *env) walkTree(node *domain.IssueTree, depth int, visit func(*domain.IssueTree, int)) {
	visit(node, depth)
	for i := range node.Children {
		e.walkTree(&node.Children[i], depth+1, visit)
	}
}

// mutated reports the result of a mutating command.
//
// Mutating commands print nothing on success in the default and compact modes;
// under --json every one of them prints the resulting object.
func (e *env) mutated(issue *domain.Issue) error {
	if e.json {
		return e.writeIssueJSON(issue)
	}
	return nil
}

func (e *env) attached(attachment *domain.Attachment) error {
	if e.json {
		return e.writeJSON(attachment)
	}
	return nil
}

func (e *env) mutatedProject(project *domain.Project) error {
	if e.json {
		return e.writeProjectJSON(project)
	}
	return nil
}

// printAttachments renders an attachment listing in whichever mode is in
// force.
func (e *env) printAttachments(attachments []domain.Attachment) error {
	switch {
	case e.json:
		// An empty list renders as [], never as null.
		if attachments == nil {
			attachments = []domain.Attachment{}
		}
		return e.writeJSON(attachments)
	case e.compact:
		for i := range attachments {
			_, _ = fmt.Fprintln(e.stdout, domain.CompactAttachmentLine(&attachments[i]))
		}
		return nil
	default:
		e.printAttachmentTable(attachments)
		return nil
	}
}

func (e *env) printAttachmentTable(attachments []domain.Attachment) {
	if len(attachments) == 0 {
		return
	}
	t := e.theme()

	cells := func(text func(*domain.Attachment) string) []string {
		out := make([]string, len(attachments))
		for i := range attachments {
			out[i] = text(&attachments[i])
		}
		return out
	}

	// The name comes first because it is what addresses the attachment, which
	// is the job the id column used to do in an issue listing.
	e.writeListing(t, []col{
		{header: "NAME", floor: nameFloor, paint: always(t.id),
			cells: cells(func(a *domain.Attachment) string { return a.Name })},
		{header: "SIZE", right: true,
			cells: cells(func(a *domain.Attachment) string { return humanSize(a.Size) })},
		{header: "TYPE", floor: typeFloor, expendable: true,
			cells: cells(func(a *domain.Attachment) string { return a.ContentType })},
	})
}

// printAttachment renders one attachment's metadata.
//
// Under --compact it prints the same single line a listing would; there is
// nothing else to lose, an attachment's metadata being that line and its
// digest and timestamp.
func (e *env) printAttachment(attachment *domain.Attachment) error {
	switch {
	case e.json:
		return e.writeJSON(attachment)
	case e.compact:
		_, _ = fmt.Fprintln(e.stdout, domain.CompactAttachmentLine(attachment))
		return nil
	default:
		t := e.theme()
		// The heading is the pair that identifies it, in the order the command
		// takes them.
		e.writeHeading(t, attachment.Issue, attachment.Name)
		e.field(t, "Type", attachment.ContentType)
		e.field(t, "Size", fmt.Sprintf("%s (%d bytes)", humanSize(attachment.Size), attachment.Size))
		e.field(t, "SHA-256", t.apply(t.dim, attachment.Sha256))
		e.field(t, "Created", attachment.CreatedAt)
		return nil
	}
}

// humanSize is a size for the default mode, which is for humans and is
// explicitly not a compatibility surface. --json and --compact carry the exact
// number of bytes.
func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return strconv.FormatInt(size, 10) + " B"
	}
	value, exponent := float64(size)/unit, 0
	for value >= unit && exponent < 3 {
		value /= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", value, "KMGT"[exponent])
}
