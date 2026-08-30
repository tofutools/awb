package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
)

// These tests are inside the package for the same reason the output ones are:
// the picker is what the default mode does with a whole terminal, and a test
// has none, so it states the window rather than owning one.

// many is a listing longer than any window a test gives it, so what the picker
// scrolls is visible.
//
// Every row's title is a different length, and no window holds every length,
// so a layout decided by the visible rows rather than by the whole listing
// shows up as a box that changes width.
func many(n int) []domain.Issue {
	issues := make([]domain.Issue, n)
	for i := range issues {
		issues[i] = domain.Issue{
			ID:       "demo-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Priority: i % (domain.MaxPriority + 1), Status: domain.StatusOpen,
			Type: "task", Title: "Issue " + strings.Repeat("x", i),
		}
	}
	return issues
}

// newPicker is a picker over a listing, in a window of the given size.
func newPicker(t *testing.T, issues []domain.Issue, mode config.ColorMode,
	width, height int) *picker {
	t.Helper()
	e := &env{stdout: &errWriter{w: &strings.Builder{}}, boxed: true, width: width,
		cfg: &config.Config{Color: mode}}
	theme := e.theme()
	p := &picker{t: theme, cols: e.issueCols(theme, issues, false),
		rows: len(issues), chosen: noSelection}
	model, _ := p.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return model.(*picker)
}

func press(t *testing.T, p *picker, keys ...tea.KeyPressMsg) (*picker, bool) {
	t.Helper()
	quit := false
	for _, key := range keys {
		model, cmd := p.Update(key)
		p = model.(*picker)
		if cmd != nil {
			// Quit is the only command the picker ever returns.
			quit = true
		}
	}
	return p, quit
}

func key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

// Enter takes the row the cursor is on, and nothing else does.
func TestPickerChoosesTheRowTheCursorIsOn(t *testing.T) {
	p := newPicker(t, many(20), config.ColorAuto, 100, 24)
	assert.Equal(t, noSelection, p.chosen, "nothing is chosen until it is")

	p, quit := press(t, p, key(tea.KeyDown, ""), key(tea.KeyDown, ""), key(tea.KeyEnter, ""))
	assert.True(t, quit, "choosing leaves the picker")
	assert.Equal(t, 2, p.chosen)
}

// Leaving without choosing chooses nothing, which is what the caller then
// prints nothing for.
func TestPickerLeavesWithoutChoosing(t *testing.T) {
	for _, leave := range []tea.KeyPressMsg{
		key('q', "q"), key(tea.KeyEscape, ""), {Code: 'c', Mod: tea.ModCtrl},
	} {
		p := newPicker(t, many(20), config.ColorAuto, 100, 24)
		p, quit := press(t, p, key(tea.KeyDown, ""), leave)
		assert.True(t, quit, "%s leaves the picker", leave)
		assert.Equal(t, noSelection, p.chosen, "%s chooses nothing", leave)
	}
}

// The cursor stops at both ends rather than wrapping, so holding a key down
// lands somewhere predictable.
func TestPickerCursorStopsAtBothEnds(t *testing.T) {
	p := newPicker(t, many(20), config.ColorAuto, 100, 24)

	p, _ = press(t, p, key(tea.KeyUp, ""), key(tea.KeyUp, ""))
	assert.Equal(t, 0, p.cursor)

	for range 40 {
		p, _ = press(t, p, key(tea.KeyDown, ""))
	}
	assert.Equal(t, 19, p.cursor)
}

// The window follows the cursor by as little as it can, so a listing longer
// than the terminal reads as one list rather than as pages.
func TestPickerScrollsToKeepTheCursorInTheWindow(t *testing.T) {
	// Ten lines of window, five of which the box and the help line take.
	p := newPicker(t, many(30), config.ColorAuto, 100, 10)
	require.Equal(t, 5, p.visible())

	for range 4 {
		p, _ = press(t, p, key(tea.KeyDown, ""))
	}
	assert.Equal(t, 0, p.top, "the window does not move while the cursor is in it")

	p, _ = press(t, p, key(tea.KeyDown, ""))
	assert.Equal(t, 1, p.top, "and then moves by one")
	assert.Equal(t, 5, p.cursor)

	p, _ = press(t, p, key(tea.KeyUp, ""), key(tea.KeyUp, ""))
	assert.Equal(t, 1, p.top, "coming back up it stays until the cursor leaves it")
	p, _ = press(t, p, key(tea.KeyUp, ""), key(tea.KeyUp, ""))
	assert.Equal(t, 1, p.cursor)
	assert.Equal(t, 1, p.top)

	// The end is the end: the last row is the last one shown.
	p, _ = press(t, p, key(tea.KeyEnd, ""))
	assert.Equal(t, 29, p.cursor)
	assert.Equal(t, 25, p.top)

	p, _ = press(t, p, key(tea.KeyHome, ""))
	assert.Equal(t, 0, p.cursor)
	assert.Equal(t, 0, p.top)
}

// A window too short for the box still shows the row the reader is on, rather
// than nothing at all.
func TestPickerAlwaysShowsARow(t *testing.T) {
	p := newPicker(t, many(30), config.ColorAuto, 100, 3)
	assert.Equal(t, 1, p.visible())
	p, _ = press(t, p, key(tea.KeyDown, ""), key(tea.KeyDown, ""))
	assert.Equal(t, 2, p.top)
	assert.Contains(t, p.render(), p.cols[0].cells[2])
}

// The picker shows only what the window has room for, and moves through the
// whole listing one row at a time.
func TestPickerShowsTheWindowAndNothingElse(t *testing.T) {
	issues := many(30)
	p := newPicker(t, issues, config.ColorAuto, 100, 10)

	assert.Contains(t, p.render(), issues[0].ID)
	assert.Contains(t, p.render(), issues[4].ID)
	assert.NotContains(t, p.render(), issues[5].ID)

	p, _ = press(t, p, key(tea.KeyEnd, ""))
	assert.NotContains(t, p.render(), issues[24].ID)
	assert.Contains(t, p.render(), issues[25].ID)
	assert.Contains(t, p.render(), issues[29].ID)
}

// The columns are laid out once for the window and not once for the rows in
// it, so nothing shifts sideways as the reader scrolls.
//
// A table is laid out to the content it is given and the picker gives it one
// window at a time, so every cell is padded to its column: without that the
// box was as wide as the longest title currently on the screen and grew and
// shrank under the reader.
func TestPickerColumnsHoldStillWhileItScrolls(t *testing.T) {
	for _, width := range []int{60, 100, 200} {
		p := newPicker(t, many(40), config.ColorAuto, width, 12)
		want := lines(p.render())
		for range 39 {
			p, _ = press(t, p, key(tea.KeyDown, ""))
			got := lines(p.render())
			assert.Equal(t, want[0], got[0],
				"width %d: the box is the same at row %d", width, p.cursor)
			assert.Equal(t, want[1], got[1],
				"width %d: so are the headings at row %d", width, p.cursor)
			// Every line of the box, the line of help under it aside.
			for i, line := range got[:len(got)-1] {
				assert.Equal(t, lipgloss.Width(want[0]), lipgloss.Width(line),
					"width %d: every line is the same width at row %d, line %d",
					width, p.cursor, i)
			}
		}
	}
}

// Resizing lays the same listing out again, and never compounds the last
// window's cuts on top of this one's.
func TestPickerLaysOutAgainWhenTheWindowChanges(t *testing.T) {
	p := newPicker(t, many(30), config.ColorAuto, 60, 24)
	narrow := lipgloss.Width(lines(p.render())[0])
	assert.LessOrEqual(t, narrow, 60)

	model, _ := p.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	p = model.(*picker)
	assert.Greater(t, lipgloss.Width(lines(p.render())[0]), narrow,
		"a wider window shows more")

	model, _ = p.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	p = model.(*picker)
	assert.Equal(t, narrow, lipgloss.Width(lines(p.render())[0]),
		"and going back gives exactly what it gave before")
}

// The cursor is position and not colour, so --color never keeps it: without it
// there would be no way to tell which row enter would take.
func TestPickerCursorSurvivesColourBeingOff(t *testing.T) {
	p := newPicker(t, many(6), config.ColorNever, 100, 24)
	p, _ = press(t, p, key(tea.KeyDown, ""))

	rows := lines(p.render())
	// Two borders and the headings and their rule come before the rows.
	assert.NotContains(t, rows[3], "\x1b[", "the row above the cursor is plain")
	assert.Contains(t, rows[4], "\x1b[7m", "the row the cursor is on is not")
	assert.NotContains(t, rows[5], "\x1b[", "nor is the one below it")
	assert.NotContains(t, rows[1], "\x1b[", "and the headings are never the cursor")
}

// The whole program, run as it is run for real, only attached to a pipe and a
// buffer rather than to a terminal: keys go in, the alternate screen and the
// listing come out, and the row chosen is the one enter was pressed on.
func TestPickerRunsAsAProgram(t *testing.T) {
	issues := many(30)
	e := &env{stdout: &errWriter{w: &strings.Builder{}}, boxed: true, width: 100,
		cfg: &config.Config{Color: config.ColorAlways}}
	theme := e.theme()

	var screen strings.Builder
	// Down twice, then enter.
	keys := strings.NewReader("\x1b[B\x1b[B\r")

	chosen, err := runPicker(
		&picker{t: theme, cols: e.issueCols(theme, issues, false),
			rows: len(issues), chosen: noSelection},
		tea.WithInput(keys), tea.WithOutput(&screen), tea.WithWindowSize(100, 12))
	require.NoError(t, err)
	assert.Equal(t, 2, chosen)

	drawn := screen.String()
	assert.Contains(t, drawn, issues[0].ID, "the listing was drawn")
	assert.Contains(t, drawn, "enter show", "under the line of help")
}

// brokenScreen is a terminal that will not take output, which is what a closed
// pipe or a full disk looks like from here. It embeds a real file so that
// everything about it other than writing still answers.
type brokenScreen struct {
	term.File
}

func (b *brokenScreen) Write([]byte) (int, error) {
	return 0, errors.New("no room on the device")
}

// A picker that could not draw is a runtime failure like any other write that
// did not happen, and must not leave a successful command behind. Bubble Tea
// throws away the errors its renderer's writes return, so the terminal it is
// handed keeps them instead.
func TestPickerReportsATerminalItCannotDrawOn(t *testing.T) {
	e := &env{stdout: &errWriter{w: &strings.Builder{}}, stdin: strings.NewReader("q"),
		boxed: true, width: 100, cfg: &config.Config{Color: config.ColorAlways}}
	theme := e.theme()

	row, err := e.pick(t.Context(), &brokenScreen{File: os.Stdout}, theme,
		e.issueCols(theme, many(30), false), 30)
	assert.Equal(t, noSelection, row)
	require.Error(t, err, "a terminal that could not be drawn on is not a success")
	assert.Equal(t, awberr.Runtime, awberr.KindOf(err))
}

// The terminal keeps the first failure and not the last, so what is reported
// is what went wrong rather than whatever followed from it.
func TestScreenKeepsTheFirstFailure(t *testing.T) {
	s := &screen{File: &brokenScreen{File: os.Stdout}}
	_, first := s.Write([]byte("one"))
	_, second := s.Write([]byte("two"))
	require.Error(t, first)
	require.Error(t, second)
	assert.Same(t, first, s.err)
}
