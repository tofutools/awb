package cli

import (
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// filterFlags are the filters list, ready, blocked and search share.
//
// Repeated values of one filter are ORed; different filters are ANDed. No
// filter accepts comma-separated lists. Only status, type, priority, label,
// assignee and project repeat; every other filter may occur once.
type filterFlags struct {
	statuses      []string
	includeClosed bool
	types         []string
	priorities    []int
	priorityMax   int
	labels        []string
	assignees     []string
	mine          bool
	unassigned    bool
	projects      []string
	parent        string
	limit         int
	sort          string
}

// filterOptions says which of the flags a particular command accepts. A flag a
// command rejects this way is a usage error, exit code 2.
type filterOptions struct {
	// status is false for ready and blocked, which fix the status set themselves.
	status bool
	// assignee is false for ready, which fixes the assignee filter to unassigned:
	// "what should nobody-in-particular pick up next" is the question it exists
	// to answer.
	assignee bool
	// relevance is true only for search.
	relevance bool
}

func (f *filterFlags) register(cmd *cobra.Command, opts filterOptions) {
	flags := cmd.Flags()

	if opts.status {
		flags.StringArrayVar(&f.statuses, "status", nil,
			"select this status; repeatable (open, in_progress, closed)")
		flags.BoolVar(&f.includeClosed, "include-closed", false,
			"widen the status set to include closed issues")
	}
	flags.StringArrayVar(&f.types, "type", nil,
		"select this type; repeatable (epic, feature, bug, task, chore)")
	flags.IntSliceVar(&f.priorities, "priority", nil,
		"select this priority exactly; repeatable (0 highest to 4 lowest)")
	flags.IntVar(&f.priorityMax, "priority-max", -1,
		"select issues at least this urgent, inclusive (0 highest)")
	flags.StringArrayVar(&f.labels, "label", nil, "select this label; repeatable")
	if opts.assignee {
		flags.StringArrayVar(&f.assignees, "assignee", nil, "select this assignee; repeatable")
		flags.BoolVar(&f.mine, "mine", false, "shorthand for --assignee <your identity>")
		flags.BoolVar(&f.unassigned, "unassigned", false, "select unassigned issues")
	}
	flags.StringArrayVar(&f.projects, "project", nil, "select this project; repeatable")
	flags.StringVar(&f.parent, "parent", "", "select the direct children of this issue")
	flags.IntVar(&f.limit, "limit", -1, "cap the number of results; zero returns none")

	sortHelp := "priority, created, updated or id, optionally prefixed with \"-\""
	if opts.relevance {
		sortHelp = "relevance, priority, created, updated or id, optionally prefixed with \"-\""
	}
	flags.StringVar(&f.sort, "sort", "", sortHelp)
}

// build turns the flags into a domain filter, applying directory context and
// the per-command rules.
func (f *filterFlags) build(e *env, cmd *cobra.Command, opts filterOptions) (*domain.Filter, error) {
	cfg, err := e.config()
	if err != nil {
		return nil, err
	}

	filter := &domain.Filter{IncludeClosed: f.includeClosed}

	for _, s := range f.statuses {
		status, err := domain.ParseStatus(s)
		if err != nil {
			return nil, err
		}
		filter.Statuses = append(filter.Statuses, status)
	}
	for _, s := range f.types {
		issueType, err := domain.ParseType(s)
		if err != nil {
			return nil, err
		}
		filter.Types = append(filter.Types, issueType)
	}
	for _, p := range f.priorities {
		priority, err := domain.ParsePriority(p)
		if err != nil {
			return nil, err
		}
		filter.Priorities = append(filter.Priorities, priority)
	}
	if cmd.Flags().Changed("priority-max") {
		priority, err := domain.ParsePriority(f.priorityMax)
		if err != nil {
			return nil, err
		}
		filter.PriorityMax = &priority
	}

	if err := f.buildAssignee(e, cmd, opts, filter); err != nil {
		return nil, err
	}
	if err := f.buildScope(cfg.ContextProject, cfg.ContextLabel, cmd, filter); err != nil {
		return nil, err
	}

	if cmd.Flags().Changed("limit") {
		if f.limit < 0 {
			return nil, awberr.Usagef("--limit must not be negative")
		}
		limit := f.limit
		filter.Limit = &limit
	}

	filter.Parent = f.parent

	sort := domain.DefaultSort
	if opts.relevance {
		sort = domain.DefaultSearchSort
	}
	if f.sort != "" {
		if sort, err = domain.ParseSort(f.sort, opts.relevance); err != nil {
			return nil, err
		}
	}
	filter.Sort = sort

	return filter, nil
}

// buildAssignee applies the mutually exclusive assignee filters. --mine
// resolves to the configured identity.
func (f *filterFlags) buildAssignee(e *env, cmd *cobra.Command, opts filterOptions,
	filter *domain.Filter) error {
	if !opts.assignee {
		return nil
	}

	given := 0
	for _, name := range []string{"mine", "assignee", "unassigned"} {
		if cmd.Flags().Changed(name) {
			given++
		}
	}
	if given > 1 {
		return awberr.Usagef("--mine, --assignee and --unassigned are mutually exclusive")
	}

	switch {
	case f.mine:
		identity, err := e.identity()
		if err != nil {
			return err
		}
		filter.Assignees = []string{identity}
	case f.unassigned:
		filter.Unassigned = true
	default:
		for _, a := range f.assignees {
			assignee, err := domain.ValidateAssignee(a)
			if err != nil {
				return err
			}
			filter.Assignees = append(filter.Assignees, assignee)
		}
	}
	return nil
}

// buildScope applies --project and --label, and the directory context they
// default to.
//
// The context project is a default for --project, and an explicit --project
// replaces it: an issue belongs to exactly one project, so intersecting the
// two could only ever yield nothing, and the explicit flag is what the person
// running the command means. The context label works the same way.
func (f *filterFlags) buildScope(contextProject, contextLabel string, cmd *cobra.Command,
	filter *domain.Filter) error {
	if cmd.Flags().Changed("project") {
		for _, p := range f.projects {
			key, err := domain.ValidateProjectKey(p)
			if err != nil {
				return err
			}
			filter.Projects = append(filter.Projects, key)
		}
	} else if contextProject != "" {
		filter.Projects = []string{contextProject}
	}

	if cmd.Flags().Changed("label") {
		for _, l := range f.labels {
			label, err := domain.ValidateLabel(l)
			if err != nil {
				return err
			}
			filter.Labels = append(filter.Labels, label)
		}
	} else if contextLabel != "" {
		filter.Labels = []string{contextLabel}
	}
	return nil
}
