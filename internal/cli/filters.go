package cli

import (
	"context"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// FilterFlags are the filters list, ready, blocked and search share.
//
// Repeated values of one filter are ORed; different filters are ANDed. No
// Status, type, priority, label, assignee and project repeat; every other
// filter may occur once.
type FilterFlags struct {
	Statuses      []string `long:"status" collection:"array" optional:"true" alts:"open,in_progress,closed" help:"select this status; repeatable (open, in_progress, closed)"`
	IncludeClosed bool     `long:"include-closed" optional:"true" help:"widen the status set to include closed issues"`
	Types         []string `long:"type" collection:"array" optional:"true" alts:"epic,feature,bug,task,chore" help:"select this type; repeatable (epic, feature, bug, task, chore)"`
	Priorities    []int    `long:"priority" optional:"true" alts:"0,1,2,3,4" help:"select this priority exactly; repeatable (0 highest to 4 lowest)"`
	PriorityMax   *int     `long:"priority-max" alts:"0,1,2,3,4" help:"select issues at least this urgent, inclusive (0 highest)"`
	Labels        []string `long:"label" collection:"array" optional:"true" help:"select this label; repeatable"`
	Assignees     []string `long:"assignee" collection:"array" optional:"true" help:"select this assignee; repeatable"`
	Mine          bool     `long:"mine" optional:"true" help:"shorthand for --assignee <your identity>"`
	Unassigned    bool     `long:"unassigned" optional:"true" help:"select unassigned issues"`
	Projects      []string `long:"project" collection:"array" optional:"true" help:"select this project; repeatable"`
	Parent        string   `long:"parent" optional:"true" help:"select the direct children of this issue"`
	Limit         *int     `long:"limit" help:"cap the number of results; zero returns none"`
	Sort          string   `long:"sort" optional:"true" alts:"priority,-priority,created,-created,updated,-updated,id,-id"`
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

func filterInit(e *env, opts filterOptions, fix func(*domain.Filter)) func(
	*boa.HookContext, *FilterFlags, *cobra.Command) error {
	return func(ctx *boa.HookContext, f *FilterFlags, _ *cobra.Command) error {
		boa.GetParamT(ctx, &f.Projects).SetAlternativesFunc(e.completeProjects)
		boa.GetParamT(ctx, &f.Labels).SetAlternativesFunc(
			func(cmd *cobra.Command, _ []string, _ string) []string {
				e.prepareCompletion(cmd)
				filter, err := f.build(e, cmd, opts)
				if err != nil {
					return nil
				}
				if fix != nil {
					fix(filter)
				}
				// A repeated label is an OR, so the facet must be computed without
				// its own dimension or the first label would hide alternatives.
				filter.Labels = nil
				filter.Limit = nil
				return e.queryCompletion(cmd,
					func(ctx context.Context, be backend.Backend) ([]string, error) {
						page, err := be.LabelFacets(ctx, filter)
						return facetValues(page.Facets), err
					})
			})
		sortHelp := "priority, created, updated or id, optionally prefixed with \"-\""
		if opts.relevance {
			sortHelp = "relevance, priority, created, updated or id, optionally prefixed with \"-\""
		}
		boa.GetParamT(ctx, &f.Sort).SetDescription(sortHelp)
		if !opts.status {
			boa.GetParamT(ctx, &f.Statuses).SetIgnored(true)
			boa.GetParamT(ctx, &f.IncludeClosed).SetIgnored(true)
		}
		if !opts.assignee {
			boa.GetParamT(ctx, &f.Assignees).SetIgnored(true)
			boa.GetParamT(ctx, &f.Mine).SetIgnored(true)
			boa.GetParamT(ctx, &f.Unassigned).SetIgnored(true)
		} else {
			boa.GetParamT(ctx, &f.Assignees).SetAlternativesFunc(
				func(cmd *cobra.Command, _ []string, _ string) []string {
					e.prepareCompletion(cmd)
					filter, err := f.build(e, cmd, opts)
					if err != nil {
						return nil
					}
					if fix != nil {
						fix(filter)
					}
					filter.Assignees = nil
					filter.Unassigned = false
					filter.Limit = nil
					return e.queryCompletion(cmd,
						func(ctx context.Context, be backend.Backend) ([]string, error) {
							page, err := be.AssigneeFacets(ctx, filter)
							return facetValues(page.Facets), err
						})
				})
		}
		return nil
	}
}

// filterPostCreate adds search's two extra sort values after Boa has read the
// shared struct tag. Its completion function consults this metadata when it is
// invoked, so other listing commands retain only the common sort vocabulary.
func filterPostCreate(opts filterOptions) func(*boa.HookContext, *FilterFlags, *cobra.Command) error {
	return func(ctx *boa.HookContext, f *FilterFlags, _ *cobra.Command) error {
		if opts.relevance {
			boa.GetParamT(ctx, &f.Sort).SetAlternatives([]string{
				"priority", "-priority", "created", "-created", "updated", "-updated",
				"id", "-id", "relevance", "-relevance",
			})
		}
		return nil
	}
}

// build turns the flags into a domain filter, applying directory context and
// the per-command rules.
func (f *FilterFlags) build(e *env, cmd *cobra.Command, opts filterOptions) (*domain.Filter, error) {
	cfg, err := e.config()
	if err != nil {
		return nil, err
	}

	filter := &domain.Filter{IncludeClosed: f.IncludeClosed}

	for _, s := range f.Statuses {
		status, err := domain.ParseStatus(s)
		if err != nil {
			return nil, err
		}
		filter.Statuses = append(filter.Statuses, status)
	}
	for _, s := range f.Types {
		issueType, err := domain.ParseType(s)
		if err != nil {
			return nil, err
		}
		filter.Types = append(filter.Types, issueType)
	}
	for _, p := range f.Priorities {
		priority, err := domain.ParsePriority(p)
		if err != nil {
			return nil, err
		}
		filter.Priorities = append(filter.Priorities, priority)
	}
	if f.PriorityMax != nil {
		priority, err := domain.ParsePriority(*f.PriorityMax)
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

	if f.Limit != nil {
		if *f.Limit < 0 {
			return nil, awberr.Usagef("--limit must not be negative")
		}
		limit := *f.Limit
		filter.Limit = &limit
	}

	filter.Parent = f.Parent

	sort := domain.DefaultSort
	if opts.relevance {
		sort = domain.DefaultSearchSort
	}
	if f.Sort != "" {
		if sort, err = domain.ParseSort(f.Sort, opts.relevance); err != nil {
			return nil, err
		}
	}
	filter.Sort = sort
	return filter, nil
}

// buildAssignee applies the mutually exclusive assignee filters. --mine
// resolves to the configured identity.
func (f *FilterFlags) buildAssignee(e *env, cmd *cobra.Command, opts filterOptions,
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
	case f.Mine:
		identity, err := e.identity()
		if err != nil {
			return err
		}
		filter.Assignees = []string{identity}
	case f.Unassigned:
		filter.Unassigned = true
	default:
		for _, a := range f.Assignees {
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
func (f *FilterFlags) buildScope(contextProject, contextLabel string, cmd *cobra.Command,
	filter *domain.Filter) error {
	if cmd.Flags().Changed("project") {
		for _, p := range f.Projects {
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
		for _, l := range f.Labels {
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
