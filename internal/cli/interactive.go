package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// The interactive listing.
//
// -i draws the listing a command would have printed on the full screen, lets
// the reader move through it, and prints the entry they choose exactly as the
// matching show command would. It is the one interactive thing awb does, and
// the only one that needs a terminal: it draws on standard output and reads
// keys from standard input, so it refuses without one on both rather than
// falling back to something else. Everything an agent or a script reads is
// unchanged, because neither has a terminal to run it in.

// InteractiveFlags is -i, which the three list commands offer.
//
// It is declared before the filters so that -i belongs to the picker on every
// command that has one, rather than to --include-closed on the listings that
// take that.
type InteractiveFlags struct {
	Interactive bool `long:"interactive" short:"i" optional:"true" help:"scroll the listing and show the entry chosen; needs a terminal"`
}

// interactively returns the terminal to show this invocation's listing on, and
// nil when it is not to show one at all.
//
// Asking for it where it cannot happen is a usage error rather than a silent
// fall back to a printed listing: a caller who asked for a screen to scroll
// asked for something this invocation has no way to give. --json and --compact
// are what a script and an agent read, and neither is a screen either.
//
// It returns the terminal itself and not merely permission to draw on one,
// because a file descriptor is what raw mode is set on and the window size is
// read from: the one place that decides there is a terminal is the one that
// hands it over.
func (e *env) interactively(on bool) (term.File, error) {
	if !on {
		return nil, nil
	}
	if e.json || e.compact {
		return nil, awberr.Usagef("--interactive, --json and --compact are mutually exclusive")
	}
	out, ok := e.stdout.w.(term.File)
	if !ok || !term.IsTerminal(out.Fd()) || !isTerminal(e.stdin) {
		return nil, awberr.Usagef(
			"--interactive needs a terminal on both standard input and standard output")
	}
	return out, nil
}

// isTerminal reports whether a reader is a terminal, by the same rule window
// applies to a writer.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// screen is the terminal the picker draws on, keeping the first write it
// fails.
//
// Bubble Tea throws away the errors its renderer's writes return, and it wants
// the file rather than the errWriter around stdout, because a plain writer is
// nothing it can put in raw mode or ask the size of. So the file is what it is
// given, wrapped in the same rule the rest of the output follows: a failure to
// write is a runtime failure and does not pass as success. Everything else a
// terminal is asked for reaches the file untouched.
type screen struct {
	term.File
	err error
}

func (s *screen) Write(p []byte) (int, error) {
	n, err := s.File.Write(p)
	if err != nil && s.err == nil {
		s.err = err
	}
	return n, err
}

// pickIssue scrolls an issue listing and prints the issue chosen.
//
// A row carries only what a listing shows, so the issue is read again: what
// show prints is the whole of it, relations and derived state included, which
// is the point of choosing one.
func (e *env) pickIssue(ctx context.Context, be backend.Backend, out term.File,
	issues []domain.Issue, withBlockers bool) error {
	if len(issues) == 0 {
		return nil
	}
	t := e.theme()
	row, err := e.pick(ctx, out, t, e.issueCols(t, issues, withBlockers), len(issues))
	if err != nil || row == noSelection {
		return err
	}
	issue, err := be.GetIssue(ctx, issues[row].ID)
	if err != nil {
		return err
	}
	return e.printIssue(issue)
}

// pickWorkspace scrolls a workspace listing and prints the workspace chosen.
func (e *env) pickWorkspace(ctx context.Context, be backend.Backend, out term.File,
	workspaces []domain.Workspace) error {
	if len(workspaces) == 0 {
		return nil
	}
	t := e.theme()
	row, err := e.pick(ctx, out, t, e.workspaceCols(t, workspaces), len(workspaces))
	if err != nil || row == noSelection {
		return err
	}
	workspace, err := be.GetWorkspace(ctx, workspaces[row].Key)
	if err != nil {
		return err
	}
	return e.printWorkspace(workspace)
}

// pickUser scrolls a user listing and prints the user chosen.
func (e *env) pickUser(ctx context.Context, be backend.Backend, out term.File,
	users []domain.User) error {
	if len(users) == 0 {
		return nil
	}
	t := e.theme()
	row, err := e.pick(ctx, out, t, userCols(t, users), len(users))
	if err != nil || row == noSelection {
		return err
	}
	user, err := be.GetUser(ctx, users[row].Name)
	if err != nil {
		return err
	}
	return e.printUser(user)
}

// pick shows a listing on the full screen and returns the row the reader
// chose, or noSelection when they left without choosing one.
//
// The alternate screen is what makes choosing and showing one thing: the
// listing occupies the window while it is being read and is gone when it has
// been, so what remains on the terminal afterwards is what the show command
// printed and nothing else.
func (e *env) pick(ctx context.Context, out term.File, t *theme, cols []col,
	rows int) (int, error) {
	drawn := &screen{File: out}
	row, err := runPicker(&picker{t: t, cols: cols, rows: rows, chosen: noSelection},
		tea.WithContext(ctx), tea.WithInput(e.stdin), tea.WithOutput(drawn))
	if err == nil && drawn.err != nil {
		err = awberr.Wrap(awberr.Runtime, drawn.err, "draw the listing")
	}
	return row, err
}

// runPicker is the program itself, apart from what it is attached to, so that
// a test can attach it to something other than a terminal.
func runPicker(p *picker, opts ...tea.ProgramOption) (int, error) {
	final, err := tea.NewProgram(p, opts...).Run()
	if err != nil {
		// Being interrupted is how somebody leaves without choosing, not a
		// failure to report.
		if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
			return noSelection, nil
		}
		return noSelection, awberr.Wrap(awberr.Runtime, err, "show the listing")
	}
	return final.(*picker).chosen, nil
}

// pickerChrome is what the rows share the window with: the box's top and
// bottom borders, the headings and the rule under them, and the line of help
// beneath it all.
const pickerChrome = 5

// picker is the listing on the full screen: the whole of it in cols, the part
// of it the window has room for, and where in it the reader is.
type picker struct {
	t    *theme
	cols []col
	rows int

	// fitted is cols laid out to the window, which is done once per window and
	// not once per keystroke: fitting the visible rows alone would let a column
	// change width, or be given up altogether, as the reader scrolled past a
	// long one.
	fitted []col

	cursor int
	top    int
	height int

	// chosen is the row the reader settled on, and noSelection until they do.
	chosen int
}

func (p *picker) Init() tea.Cmd { return nil }

func (p *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.resize(msg.Width, msg.Height)
	case tea.KeyPressMsg:
		return p, p.key(msg.String())
	}
	return p, nil
}

func (p *picker) View() tea.View {
	v := tea.NewView(p.render())
	v.AltScreen = true
	return v
}

// resize lays the listing out again for a window of this size.
func (p *picker) resize(width, height int) {
	p.t.width = max(width, minimumWidth)
	p.height = height
	p.fitted = fill(p.t.fit(cloneCols(p.cols)))
	p.scroll()
}

// key applies one keystroke. Both the arrow keys and their vi equivalents move
// the cursor, because a listing is read by whoever is at the terminal.
func (p *picker) key(key string) tea.Cmd {
	switch key {
	case "up", "k", "ctrl+p":
		p.move(-1)
	case "down", "j", "ctrl+n":
		p.move(1)
	case "pgup", "ctrl+b":
		p.move(-p.visible())
	case "pgdown", "ctrl+f", " ":
		p.move(p.visible())
	case "home", "g":
		p.move(-p.rows)
	case "end", "G":
		p.move(p.rows)
	case "enter":
		p.chosen = p.cursor
		return tea.Quit
	case "q", "esc", "ctrl+c":
		return tea.Quit
	}
	return nil
}

// move takes the cursor as far as it can go in the direction asked for, and
// brings the window with it.
func (p *picker) move(by int) {
	p.cursor = max(0, min(p.cursor+by, p.rows-1))
	p.scroll()
}

// visible is how many rows the window has room for, and never fewer than one:
// a window too short for the box still shows the row the reader is on.
func (p *picker) visible() int {
	return max(1, p.height-pickerChrome)
}

// scroll moves the window the least it can to hold the cursor.
func (p *picker) scroll() {
	visible := p.visible()
	p.top = max(0, min(p.top, p.rows-visible))
	p.top = min(p.top, p.cursor)
	p.top = max(p.top, p.cursor-visible+1)
}

func (p *picker) render() string {
	if p.fitted == nil {
		// Nothing has said how big the window is yet.
		return ""
	}
	end := min(p.top+p.visible(), p.rows)
	listing := p.t.renderListing(rowWindow(p.fitted, p.top, end), p.cursor-p.top)
	return listing + "\n" + p.t.apply(p.t.dim, p.helpLine())
}

func (p *picker) helpLine() string {
	return fmt.Sprintf(" %d/%d   ↑/↓ move   enter show   q quit", p.cursor+1, p.rows)
}

// fill pads every cell out to the width of the widest in its column, so that
// what a column is worth is decided by the whole listing rather than by the
// rows that happen to be on the screen.
//
// A table is laid out to the content it is given, and the picker gives it one
// window of rows at a time. Fitting alone is not enough, because fitting only
// cuts what is too wide and leaves everything else at its own length: without
// this the box is as wide as the longest title currently visible, and it grows
// and shrinks under the reader as they scroll past a long one.
func fill(cols []col) []col {
	for i := range cols {
		width := lipgloss.Width(cols[i].header)
		for _, cell := range cols[i].cells {
			width = max(width, lipgloss.Width(cell))
		}
		for j, cell := range cols[i].cells {
			cols[i].cells[j] = padCell(cell, width, cols[i].right)
		}
	}
	return cols
}

// padCell fills a cell out to a width, on whichever side leaves what it says
// where its column wants it. The width is what the terminal shows and not what
// the string holds, because a cell may carry the escapes that make an
// identifier clickable.
func padCell(cell string, width int, right bool) string {
	fill := strings.Repeat(" ", max(0, width-lipgloss.Width(cell)))
	if right {
		return fill + cell
	}
	return cell + fill
}

// cloneCols copies the cells, because fit cuts them where they stand and the
// picker lays the same listing out again every time the window changes size.
func cloneCols(cols []col) []col {
	out := slices.Clone(cols)
	for i := range out {
		out[i].cells = slices.Clone(cols[i].cells)
	}
	return out
}

// rowWindow is the columns cut down to the rows the window is showing. The
// cells are shared rather than copied, since nothing writes to them once they
// are fitted, and each colour follows the row it came from.
func rowWindow(cols []col, from, to int) []col {
	out := slices.Clone(cols)
	for i := range out {
		out[i].cells = cols[i].cells[from:to]
		if paint := cols[i].paint; paint != nil {
			out[i].paint = func(row int) lipgloss.Style { return paint(row + from) }
		}
	}
	return out
}
