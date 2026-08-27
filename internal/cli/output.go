package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
)

// The three output modes of SPEC §4.1.
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

// styles carries the palette for the default mode. When colour is off every
// style is the identity, so the same rendering code produces plain text.
type styles struct {
	enabled bool

	header   lipgloss.Style
	dim      lipgloss.Style
	id       lipgloss.Style
	blocked  lipgloss.Style
	closed   lipgloss.Style
	assignee lipgloss.Style
	label    lipgloss.Style
	priority [domain.MaxPriority + 1]lipgloss.Style
}

func (s *styles) apply(style lipgloss.Style, text string) string {
	if !s.enabled {
		return text
	}
	return style.Render(text)
}

// newStyles decides whether to colour, per the chain of SPEC §4.1. In auto
// mode colour is used when stdout is a terminal.
func newStyles(mode config.ColorMode, out io.Writer) *styles {
	enabled := false
	switch mode {
	case config.ColorAlways:
		enabled = true
	case config.ColorNever:
		enabled = false
	case config.ColorAuto:
		enabled = isTerminal(out)
	}

	s := &styles{enabled: enabled}
	if !enabled {
		return s
	}

	s.header = lipgloss.NewStyle().Bold(true)
	s.dim = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	s.id = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	s.blocked = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	s.closed = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	s.assignee = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	s.label = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))

	// P0 is the most urgent, P4 the least, so the palette runs hot to cool.
	s.priority = [domain.MaxPriority + 1]lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true),
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
	return s
}

// isTerminal reports whether w is a character device, which is what "auto"
// means by a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (e *env) styles() *styles {
	mode := config.ColorAuto
	if e.cfg != nil {
		mode = e.cfg.Color
	}
	return newStyles(mode, e.stdout)
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
		return e.writeJSON(issues)
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
	st := e.styles()

	headers := []string{"ID", "P", "STATUS", "TYPE", "TITLE", "ASSIGNEE", "LABELS"}
	if withBlockers {
		headers = append(headers, "BLOCKED BY")
	}

	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(false).
		Headers(headers...)

	for i := range issues {
		issue := &issues[i]
		row := []string{
			st.apply(st.id, issue.ID),
			st.apply(st.priority[clampPriority(issue.Priority)], "P"+strconv.Itoa(issue.Priority)),
			e.renderStatus(st, issue),
			string(issue.Type),
			truncate(issue.Title, 60),
			st.apply(st.assignee, issue.Assignee),
			st.apply(st.label, strings.Join(issue.Labels, ",")),
		}
		if withBlockers {
			row = append(row, st.apply(st.blocked, strings.Join(issue.Blockers, ",")))
		}
		t.Row(row...)
	}

	_, _ = fmt.Fprintln(e.stdout, t.Render())
}

func (e *env) renderStatus(st *styles, issue *domain.Issue) string {
	status := string(issue.Status)
	if issue.Blocked {
		return st.apply(st.blocked, status+" !blocked")
	}
	if issue.Status == domain.StatusClosed {
		return st.apply(st.closed, status)
	}
	return status
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
// truncate.
func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

// printIssue renders one issue.
//
// Under --compact it prints the same single line a listing would and nothing
// else, losing the description, relations and links: --compact means the
// cheapest representation there is, and --json is what an agent uses when it
// needs the rest (SPEC §4.1).
func (e *env) printIssue(issue *domain.Issue) error {
	switch {
	case e.json:
		return e.writeJSON(issue)
	case e.compact:
		_, _ = fmt.Fprintln(e.stdout, domain.CompactLine(issue, false))
		return nil
	default:
		e.printIssueDetail(issue)
		return nil
	}
}

func (e *env) printIssueDetail(issue *domain.Issue) {
	st := e.styles()
	field := func(name, value string) {
		if value == "" {
			return
		}
		_, _ = fmt.Fprintf(e.stdout, "%s %s\n", st.apply(st.header, pad(name+":", 12)), value)
	}

	_, _ = fmt.Fprintf(e.stdout, "%s  %s\n", st.apply(st.id, issue.ID), issue.Title)
	field("Project", issue.Project)
	field("Type", string(issue.Type))
	field("Status", e.renderStatus(st, issue))
	field("Priority", "P"+strconv.Itoa(issue.Priority))
	field("Assignee", st.apply(st.assignee, issue.Assignee))
	field("Labels", st.apply(st.label, strings.Join(issue.Labels, ", ")))
	field("Closed", issue.CloseReason)
	field("Created", issue.CreatedAt)
	field("Updated", issue.UpdatedAt)

	if len(issue.Blockers) > 0 {
		field("Blocked by", st.apply(st.blocked, strings.Join(issue.Blockers, ", ")))
	}

	if issue.Description != "" {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", issue.Description)
	}

	if len(issue.Links) > 0 {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", st.apply(st.header, "Links"))
		for _, link := range issue.Links {
			if link.Text == "" {
				_, _ = fmt.Fprintf(e.stdout, "  %s\n", link.URL)
				continue
			}
			_, _ = fmt.Fprintf(e.stdout, "  %s  %s\n", link.Text, st.apply(st.dim, link.URL))
		}
	}

	if len(issue.Relations) > 0 {
		_, _ = fmt.Fprintf(e.stdout, "\n%s\n", st.apply(st.header, "Relations"))
		for _, rel := range issue.Relations {
			// Every relation reads "subject — type — other", the single
			// convention of SPEC §2.3, whichever end is being viewed.
			subject, other := issue.ID, rel.Other
			if rel.Direction == domain.DirectionIn {
				subject, other = rel.Other, issue.ID
			}
			_, _ = fmt.Fprintf(e.stdout, "  %s %s %s\n",
				st.apply(st.id, subject), rel.Type, st.apply(st.id, other))
		}
	}
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// printProjects renders a project listing. Under --compact each line is
// "<key> <active_issues> <name>", where the name is a JSON string.
func (e *env) printProjects(projects []domain.Project) error {
	switch {
	case e.json:
		if projects == nil {
			projects = []domain.Project{}
		}
		return e.writeJSON(projects)
	case e.compact:
		for i := range projects {
			_, _ = fmt.Fprintln(e.stdout, domain.CompactProjectLine(&projects[i]))
		}
		return nil
	default:
		if len(projects) == 0 {
			return nil
		}
		st := e.styles()
		t := table.New().
			Border(lipgloss.HiddenBorder()).
			BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
			BorderColumn(false).BorderRow(false).BorderHeader(false).
			Headers("KEY", "OPEN", "NAME")
		for i := range projects {
			p := &projects[i]
			t.Row(st.apply(st.id, p.Key), strconv.Itoa(p.ActiveIssues), p.Name)
		}
		_, _ = fmt.Fprintln(e.stdout, t.Render())
		return nil
	}
}

// printTree renders a dependency tree.
//
// Under --compact each node is the ordinary compact issue line prefixed by two
// spaces per level of depth, the root unindented. That prefix is the one thing
// that may precede the id, so a consumer strips the leading spaces, counts them
// to recover the depth, and parses the rest of the line as usual (SPEC §4.1).
func (e *env) printTree(tree *domain.IssueTree) error {
	switch {
	case e.json:
		return e.writeJSON(tree)
	case e.compact:
		e.walkTree(tree, 0, func(node *domain.IssueTree, depth int) {
			_, _ = fmt.Fprintln(e.stdout,
				domain.CompactTreePrefix(depth)+domain.CompactLine(&node.Issue, false))
		})
		return nil
	default:
		st := e.styles()
		e.walkTree(tree, 0, func(node *domain.IssueTree, depth int) {
			_, _ = fmt.Fprintf(e.stdout, "%s%s  %s  %s  %s\n",
				strings.Repeat("  ", depth),
				st.apply(st.id, node.ID),
				st.apply(st.priority[clampPriority(node.Priority)], "P"+strconv.Itoa(node.Priority)),
				e.renderStatus(st, &node.Issue),
				truncate(node.Title, 60))
		})
		return nil
	}
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
// under --json every one of them prints the resulting object (SPEC §4.1).
func (e *env) mutated(issue *domain.Issue) error {
	if e.json {
		return e.writeJSON(issue)
	}
	return nil
}

func (e *env) mutatedProject(project *domain.Project) error {
	if e.json {
		return e.writeJSON(project)
	}
	return nil
}
