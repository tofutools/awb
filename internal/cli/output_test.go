package cli

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
)

// These tests are inside the package because the thing under test is what the
// default mode does with a terminal, and a test has none: the terminal is a
// field on env, so a test states one rather than owning one.

// sample is a listing wide enough that every column cannot fit any ordinary
// window, so what the layout gives up is visible.
func sample() []domain.Issue {
	return []domain.Issue{
		{ID: "demo-eeec94", Priority: 0, Status: domain.StatusOpen, Type: "epic",
			Title:  "Ship the 1.0 release of the widget catalogue",
			Labels: []string{"release"}},
		{ID: "demo-992e3c", Priority: 0, Status: domain.StatusInProgress, Type: "bug",
			Title:    "Catalogue page crashes on an empty result set",
			Assignee: "bob", Labels: []string{"catalogue", "frontend"}},
		{ID: "demo-bff7dc", Priority: 2, Status: domain.StatusOpen, Type: "feature",
			Title: "Search the catalogue by name and tag", Blocked: true,
			Blockers: []string{"demo-bbd9d3"},
			Labels:   []string{"catalogue", "frontend", "search"}},
	}
}

// render prints a listing to a window of the given width, or to no window at
// all when width is zero. Colour is off throughout: what these tests are about
// is the layout, and an escape sequence in a golden string reads as noise.
func render(width int, print func(*env)) string {
	var buf bytes.Buffer
	e := &env{
		stdout: &errWriter{w: &buf}, boxed: width > 0, width: width,
		cfg: &config.Config{Color: config.ColorNever},
	}
	print(e)
	return buf.String()
}

func renderRemote(mode config.ColorMode, boxed bool, print func(*env)) string {
	var buf bytes.Buffer
	base, err := url.Parse("https://example.com/awb")
	if err != nil {
		panic(err)
	}
	e := &env{
		stdout: &errWriter{w: &buf}, boxed: boxed, width: 140,
		cfg: &config.Config{Color: mode, RemoteURL: base},
	}
	print(e)
	return buf.String()
}

func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// Without a terminal a listing is plain aligned columns, two spaces apart, and
// no line carries trailing whitespace. Nothing should parse this, but a human
// reading a captured log has to be able to tell one column from the next.
func TestListingWithoutATerminalIsPlainColumns(t *testing.T) {
	out := render(0, func(e *env) { e.printIssueTable(sample(), false) })

	for _, line := range lines(out) {
		assert.NotContains(t, line, "│", "no border is drawn without a terminal")
		assert.Equal(t, strings.TrimRight(line, " "), line, "no trailing whitespace")
	}

	head := lines(out)[0]
	for _, header := range []string{"ID", "P", "STATUS", "TYPE", "TITLE", "ASSIGNEE", "LABELS"} {
		assert.Contains(t, head, header)
	}
	// Every column is separated from the next, which is what was wrong before.
	assert.NotContains(t, out, "demo-eeec94P0")
	assert.Contains(t, lines(out)[1], "demo-eeec94  P0  open")
}

// On a terminal the same listing is a box, and no line of it is wider than the
// window.
func TestListingFitsTheWindow(t *testing.T) {
	for _, width := range []int{60, 80, 100, 140, 200} {
		out := render(width, func(e *env) { e.printIssueTable(sample(), false) })
		for _, line := range lines(out) {
			assert.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, line)
		}
		assert.Contains(t, out, "╭", "width %d is boxed", width)
		// Whatever else goes, what identifies a row and what a reader scans for
		// stays.
		for _, header := range []string{"ID", "P", "STATUS", "TITLE"} {
			assert.Contains(t, lines(out)[1], header, "width %d keeps %s", width, header)
		}
		assert.Contains(t, out, "demo-eeec94", "width %d keeps the ids whole", width)
	}
}

// The server URL used by remote mode is also the base of the bundled web UI,
// so identifiers in an interactive listing can lead straight to the entity
// they name. OSC 8 changes no visible width and terminals that do not implement
// it still show the identifier itself.
func TestRemoteListingIdentifiersAreClickable(t *testing.T) {
	issues := renderRemote(config.ColorAlways, true, func(e *env) {
		e.printIssueTable(sample()[2:], true)
	})
	assert.Contains(t, issues,
		"\x1b]8;;https://example.com/awb/#/issues/demo-bff7dc\x07demo-bff7dc")
	assert.Contains(t, issues,
		"\x1b]8;;https://example.com/awb/#/issues/demo-bbd9d3\x07demo-bbd9d3",
		"blocker IDs lead to their issues too")
	assert.Contains(t, issues, "\x1b]8;;\x07", "each hyperlink is closed")

	projects := renderRemote(config.ColorAlways, true, func(e *env) {
		require.NoError(t, e.printProjects([]domain.Project{{Key: "demo", Name: "Demo"}}))
	})
	assert.Contains(t, projects,
		"\x1b]8;;https://example.com/awb/#/issues?project=demo\x07demo",
		"the web UI represents a project as its filtered issue listing")
}

// JSON cannot carry an OSC 8 sequence, so it names the same web destinations
// explicitly. Both fields are part of the CLI shape even in direct mode; an
// empty value says that the local database has no associated web address.
func TestJSONIncludesWebLinks(t *testing.T) {
	issue := domain.Issue{ID: "demo-eeec94", Project: "demo", Title: "Release"}
	issues := renderRemote(config.ColorNever, false, func(e *env) {
		e.json = true
		require.NoError(t, e.printIssues([]domain.Issue{issue}, false))
	})
	var issueList []map[string]any
	require.NoError(t, json.Unmarshal([]byte(issues), &issueList))
	require.Len(t, issueList, 1)
	assert.Equal(t, "https://example.com/awb/#/issues/demo-eeec94", issueList[0]["issue_link"])
	assert.Equal(t, "https://example.com/awb/#/issues?project=demo", issueList[0]["project_link"])

	projects := renderRemote(config.ColorNever, false, func(e *env) {
		e.json = true
		require.NoError(t, e.printProjects([]domain.Project{{Key: "demo", Name: "Demo"}}))
	})
	var projectList []map[string]any
	require.NoError(t, json.Unmarshal([]byte(projects), &projectList))
	require.Len(t, projectList, 1)
	assert.Equal(t, "https://example.com/awb/#/issues?project=demo", projectList[0]["project_link"])

	local := render(0, func(e *env) {
		e.json = true
		require.NoError(t, e.printIssue(&issue))
	})
	var localIssue map[string]any
	require.NoError(t, json.Unmarshal([]byte(local), &localIssue))
	assert.Equal(t, "", localIssue["issue_link"])
	assert.Equal(t, "", localIssue["project_link"])
}

func TestRemoteDetailIdentifiersAreClickable(t *testing.T) {
	issue := domain.Issue{
		ID: "demo-eeec94", Project: "demo", Title: "Release", Status: domain.StatusOpen,
		Blockers:  []string{"demo-bff7dc"},
		Relations: []domain.Relation{{Type: domain.RelRelated, Other: "demo-bbd9d3"}},
	}
	out := renderRemote(config.ColorAlways, true, func(e *env) { e.printIssueDetail(&issue) })
	for _, destination := range []string{
		"#/issues/demo-eeec94", "#/issues?project=demo", "#/issues/demo-bff7dc", "#/issues/demo-bbd9d3",
	} {
		assert.Contains(t, out, "https://example.com/awb/"+destination)
	}

	project := renderRemote(config.ColorAlways, true, func(e *env) {
		e.printProjectDetail(&domain.Project{Key: "demo", Name: "Demo"})
	})
	assert.Contains(t, project, "https://example.com/awb/#/issues?project=demo")
}

func TestRemoteStatusLinksToTheWebUI(t *testing.T) {
	out := renderRemote(config.ColorAlways, true, func(e *env) {
		require.NoError(t, e.printStatus(&statusReport{
			Connection: statusConnection{
				Mode: "remote", Server: "https://example.com/awb",
				UI: "https://example.com/awb/#/projects",
			},
			Configuration: statusConfiguration{Color: config.ColorAlways},
			Projects:      []statusProject{{Key: "demo", Name: "Demo", Open: 2, Total: 2}},
		}))
	})
	assert.Contains(t, out,
		"\x1b]8;;https://example.com/awb/#/projects\x07https://example.com/awb/#/projects",
		"the visible full UI URL opens the project index")
	assert.Contains(t, out,
		"\x1b]8;;https://example.com/awb/#/issues?project=demo\x07demo",
		"the project key opens its filtered issue listing")
	assert.Contains(t, out,
		"\x1b]8;;https://example.com/awb/#/issues?project=demo\x07Demo",
		"the project name opens the same listing")

	plain := ansi.Strip(out)
	var header, row string
	for _, line := range lines(plain) {
		if strings.Contains(line, "KEY") && strings.Contains(line, "IN PROGRESS") {
			header = line
		}
		if strings.Contains(line, "demo") && strings.Contains(line, "Demo") {
			row = line
		}
	}
	require.NotEmpty(t, header)
	require.NotEmpty(t, row)
	assert.Equal(t, strings.Index(header, "NAME"), strings.Index(row, "Demo"))
	assert.Equal(t, strings.Index(header, "OPEN")+len("OPEN"), strings.Index(row, "2")+len("2"),
		"OSC 8 bytes must not change visible column alignment")
}

// A local database has no associated web address, redirected output is not an
// interactive terminal, and --color never promises no terminal escapes. None
// of those cases acquires an OSC 8 sequence.
func TestListingHyperlinksNeedARemoteInteractiveColouredTerminal(t *testing.T) {
	local := renderRemote(config.ColorAlways, true, func(e *env) {
		e.cfg.RemoteURL = nil
		e.printIssueTable(sample()[:1], false)
	})
	redirected := renderRemote(config.ColorAlways, false, func(e *env) {
		e.printIssueTable(sample()[:1], false)
	})
	plain := renderRemote(config.ColorNever, true, func(e *env) {
		e.printIssueTable(sample()[:1], false)
	})

	for name, out := range map[string]string{
		"local": local, "redirected": redirected, "colour disabled": plain,
	} {
		assert.NotContains(t, out, "\x1b]8;", name)
		assert.Contains(t, out, "demo-eeec94", name)
	}
}

// A wide window is not filled for the sake of it: the box is only ever shrunk
// to fit, never stretched to the edge of the screen.
func TestListingIsNotStretchedToTheWindow(t *testing.T) {
	out := render(200, func(e *env) { e.printIssueTable(sample(), false) })
	assert.Less(t, lipgloss.Width(lines(out)[0]), 200)
	// With that much room nothing has to be given up or cut.
	assert.Contains(t, out, "LABELS")
	assert.Contains(t, out, "Ship the 1.0 release of the widget catalogue")
}

// The columns a reader can do without are given up rightmost first, and only
// once cutting the rest to their floors is not enough.
func TestNarrowListingGivesUpColumnsFromTheRight(t *testing.T) {
	wide := lines(render(140, func(e *env) { e.printIssueTable(sample(), false) }))[1]
	assert.Contains(t, wide, "LABELS")
	assert.Contains(t, wide, "ASSIGNEE")

	// Labels are the first to go, and the assignee and the type still fit.
	narrower := lines(render(90, func(e *env) { e.printIssueTable(sample(), false) }))[1]
	assert.NotContains(t, narrower, "LABELS")
	assert.Contains(t, narrower, "ASSIGNEE")
	assert.Contains(t, narrower, "TYPE")

	// Narrower still and only what identifies a row, and what a reader scans
	// for, is left.
	narrowest := lines(render(70, func(e *env) { e.printIssueTable(sample(), false) }))[1]
	assert.NotContains(t, narrowest, "ASSIGNEE")
	assert.NotContains(t, narrowest, "TYPE")
	assert.Contains(t, narrowest, "TITLE")
}

// awb blocked exists to name the blockers, so that column is never the one
// given up to make room.
func TestBlockersAreNeverGivenUp(t *testing.T) {
	for _, width := range []int{60, 100} {
		out := render(width, func(e *env) { e.printIssueTable(sample()[2:], true) })
		assert.Contains(t, lines(out)[1], "BLOCKED BY", "width %d", width)
	}
	// It can still be cut to fit, but only once every column that could go has.
	assert.Contains(t, render(100, func(e *env) { e.printIssueTable(sample()[2:], true) }),
		"demo-bbd9d3")
}

// An empty listing prints nothing at all, box included.
func TestEmptyListingPrintsNothing(t *testing.T) {
	assert.Empty(t, render(100, func(e *env) { e.printIssueTable(nil, false) }))
	assert.Empty(t, render(0, func(e *env) { e.printIssueTable(nil, false) }))
}

// A description is folded to the window when there is one, and left exactly as
// it was written when there is not.
func TestDescriptionIsFoldedOnlyToAWindow(t *testing.T) {
	long := "A single search box over the index, with the tag filters beside it, " +
		"and the scope fixed by the release checklist."
	issue := &domain.Issue{ID: "demo-bff7dc", Title: "Search", Description: long,
		Status: domain.StatusOpen}

	verbatim := render(0, func(e *env) { e.printIssueDetail(issue) })
	assert.Contains(t, verbatim, long)

	folded := render(60, func(e *env) { e.printIssueDetail(issue) })
	assert.NotContains(t, folded, long)
	for _, line := range lines(folded) {
		assert.LessOrEqual(t, lipgloss.Width(line), 60, "%q", line)
	}
	// Folding rewraps the prose; it does not drop any of it.
	assert.Equal(t, strings.Fields(long), fieldsOfBody(folded))
}

// fieldsOfBody returns the words of the description block of a rendered issue,
// which is everything after the blank line that follows the fields.
func fieldsOfBody(out string) []string {
	_, body, found := strings.Cut(out, "\n\n")
	if !found {
		return nil
	}
	return strings.Fields(body)
}

// A field with no value prints no line, and colour does not change that: a
// style around nothing is nothing. Regression: an unassigned issue printed an
// empty Assignee line whenever colour was on, because the escape sequences that
// would have coloured the value were themselves the value.
func TestFieldsWithNoValuePrintNothing(t *testing.T) {
	issue := &domain.Issue{ID: "demo-bff7dc", Title: "Search", Status: domain.StatusOpen}

	for _, mode := range []config.ColorMode{config.ColorNever, config.ColorAlways} {
		assert.Empty(t, newTheme(mode, true, 100).apply(lipgloss.NewStyle().Bold(true), ""))

		var buf bytes.Buffer
		e := &env{stdout: &errWriter{w: &buf}, boxed: true, width: 100,
			cfg: &config.Config{Color: mode}}
		e.printIssueDetail(issue)

		for _, absent := range []string{"Assignee", "Labels", "Closed", "Blocked by"} {
			assert.NotContains(t, buf.String(), absent, "colour mode %v", mode)
		}
		assert.Contains(t, buf.String(), "Status", "the fields that do have a value are still printed")
	}
}

// The sections of awb show line their columns up, so two links or two relations
// can be read down the page.
func TestDetailSectionsAreAligned(t *testing.T) {
	issue := &domain.Issue{
		ID: "demo-bff7dc", Title: "Search", Status: domain.StatusOpen,
		Links: []domain.Link{
			{Text: "checklist", URL: "https://example.com/a"},
			{Text: "much longer text", URL: "https://example.com/b"},
		},
		Relations: []domain.Relation{
			{Other: "demo-bbd9d3", Type: domain.RelBlockedBy, Direction: domain.DirectionOut},
			{Other: "demo-eeec94", Type: domain.RelHasParent, Direction: domain.DirectionOut},
		},
	}
	out := render(100, func(e *env) { e.printIssueDetail(issue) })

	first := strings.Index(out, "https://example.com/a")
	second := strings.Index(out, "https://example.com/b")
	require.Positive(t, first)
	require.Positive(t, second)
	assert.Equal(t, columnOf(out, first), columnOf(out, second),
		"the urls of two links start in the same column")

	firstRel := strings.Index(out, "blocked-by")
	secondRel := strings.Index(out, "has-parent")
	require.Positive(t, firstRel)
	require.Positive(t, secondRel)
	assert.Equal(t, columnOf(out, firstRel), columnOf(out, secondRel),
		"the types of two relations start in the same column")
}

// columnOf gives the column an offset into a rendered block falls in.
func columnOf(s string, offset int) int {
	return offset - strings.LastIndex(s[:offset], "\n") - 1
}

// A tree is drawn with connectors whether or not there is a terminal: they are
// the shape of the graph rather than decoration, and need no width.
func TestTreeIsDrawnWithConnectorsEitherWay(t *testing.T) {
	root := &domain.IssueTree{
		Issue: domain.Issue{ID: "demo-eeec94", Status: domain.StatusOpen, Title: "Ship"},
		Children: []domain.IssueTree{
			{Issue: domain.Issue{ID: "demo-54674f", Status: domain.StatusClosed, Title: "Design"}},
			{
				Issue: domain.Issue{ID: "demo-d0c372", Status: domain.StatusInProgress, Title: "Browse"},
				Children: []domain.IssueTree{
					{Issue: domain.Issue{ID: "demo-4202a3", Status: domain.StatusOpen, Title: "Thumbnails"}},
				},
			},
		},
	}

	for _, width := range []int{0, 100} {
		out := render(width, func(e *env) { require.NoError(t, e.printTree(root)) })
		assert.Contains(t, out, "├── demo-54674f", "width %d", width)
		assert.Contains(t, out, "└── demo-d0c372", "width %d", width)
		assert.Contains(t, out, "    └── demo-4202a3", "the grandchild is a level deeper")
	}
}

// A cell is cut to what a terminal shows rather than to bytes or runes, so a
// double-width character costs two columns and an escape sequence costs none.
func TestTruncateCountsWhatTheTerminalShows(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "ab…", truncate("abcdef", 3))
	assert.Equal(t, "日…", truncate("日本語", 4))
	assert.Equal(t, "\x1b[31mab…\x1b[0m", truncate("\x1b[31mabcdef\x1b[0m", 3))
}

// Colour and the box are decided separately, because they answer different
// questions: colour is asked for, and a window either is there or is not. A
// pipe told to colour gets colour and still gets plain columns.
func TestColourAndBoxesAreDecidedSeparately(t *testing.T) {
	drawn := func(mode config.ColorMode, boxed bool) string {
		var buf bytes.Buffer
		e := &env{stdout: &errWriter{w: &buf}, boxed: boxed, width: 100,
			cfg: &config.Config{Color: mode}}
		e.printIssueTable(sample(), false)
		return buf.String()
	}

	assert.Contains(t, drawn(config.ColorAlways, false), "\x1b[")
	assert.NotContains(t, drawn(config.ColorAlways, false), "│")

	// Colour must not smuggle trailing whitespace past the trimming: lipgloss
	// pads a cell outside the escape sequence that colours it, so the padding is
	// still the end of the line and is still cut.
	for _, line := range lines(drawn(config.ColorAlways, false)) {
		assert.Equal(t, strings.TrimRight(line, " "), line, "%q", line)
	}
	assert.NotContains(t, drawn(config.ColorNever, true), "\x1b[")
	assert.Contains(t, drawn(config.ColorNever, true), "│")

	// In auto mode colour follows the terminal, as it always has.
	assert.NotContains(t, drawn(config.ColorAuto, false), "\x1b[")
	assert.Contains(t, drawn(config.ColorAuto, true), "\x1b[")
}

// A heading is never cut, however narrow the window: a heading with its end
// missing says nothing about what the column below it holds.
func TestHeadingsAreNeverCut(t *testing.T) {
	for _, width := range []int{40, 50, 60, 80} {
		head := lines(render(width, func(e *env) { e.printIssueTable(sample(), true) }))[1]
		for _, header := range strings.Fields(strings.ReplaceAll(head, "│", " ")) {
			assert.NotContains(t, header, "…", "width %d: %q", width, head)
		}
		assert.Contains(t, head, "BLOCKED BY", "width %d", width)
	}
}
