package cli

import (
	"io"
	"os"
	"slices"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// DescriptionFlags are --description and --description-file, which are
// mutually exclusive. Both replace the description outright, taking the bytes
// exactly as given: a description is never trimmed, so a trailing line feed
// from a heredoc or an editor is part of it.
type DescriptionFlags struct {
	Text *string `long:"description"`
	File *string `long:"description-file" help:"read the description from this file, or from stdin with \"-\""`
}

func describe(what string) func(*boa.HookContext, *DescriptionFlags, *cobra.Command) error {
	return func(ctx *boa.HookContext, d *DescriptionFlags, _ *cobra.Command) error {
		boa.GetParamT(ctx, &d.Text).SetDescription("markdown description of the " + what)
		return nil
	}
}

// value returns the description, or nil when neither flag was given.
func (d *DescriptionFlags) value(e *env) (*string, error) {
	switch {
	case d.Text != nil && d.File != nil:
		return nil, awberr.Usagef("--description and --description-file are mutually exclusive")
	case d.Text != nil:
		return d.Text, nil
	case d.File != nil:
		data, err := d.read(e)
		if err != nil {
			return nil, err
		}
		text := string(data)
		return &text, nil
	default:
		return nil, nil
	}
}

func (d *DescriptionFlags) read(e *env) ([]byte, error) {
	// This is the only use of stdin, and is not the bulk import that is out of
	// scope.
	if *d.File == "-" {
		data, err := io.ReadAll(e.stdin)
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read the description from stdin")
		}
		return data, nil
	}
	data, err := os.ReadFile(*d.File)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read %s", *d.File)
	}
	return data, nil
}

type createParams struct {
	DescriptionFlags
	Title          string   `positional:"true" required:"true"`
	Type           string   `long:"type" default:"task" optional:"true" alts:"epic,feature,bug,task,chore" help:"epic, feature, bug, task or chore"`
	Priority       int      `long:"priority" default:"2" optional:"true" alts:"0,1,2,3,4" help:"0 (highest) to 4 (lowest)"`
	Labels         []string `long:"label" collection:"array" optional:"true" help:"add this label; repeatable"`
	Assignee       string   `long:"assignee" optional:"true" help:"create and claim in one step"`
	Project        string   `long:"project" optional:"true" help:"the project to create the issue in"`
	HasParent      string   `long:"has-parent" optional:"true" help:"the new issue is part of decomposing this one"`
	BlockedBy      []string `long:"blocked-by" collection:"array" optional:"true" help:"the new issue cannot start until this one is closed; repeatable"`
	DiscoveredFrom []string `long:"discovered-from" collection:"array" optional:"true" help:"the new issue was found while working on this one; repeatable"`
	Related        []string `long:"related" collection:"array" optional:"true" help:"loose association; repeatable"`
}

func newCreateCommand(e *env) *cobra.Command {
	return boa.CmdT[createParams]{
		Use:   "create",
		Short: "Create an issue, with its labels and relations, in one transaction",
		Long: "Create an issue and print its ID.\n\n" +
			"The relation flags read \"the new issue — relation — the named issue\",\n" +
			"the single convention of the whole tool.\n\n" +
			"Creating with an assignee is an atomic create-and-claim: --assignee also\n" +
			"sets the status to in_progress, so a new issue is never open and assigned\n" +
			"at once.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *createParams, cmd *cobra.Command) error {
			if err := describe("issue")(ctx, &p.DescriptionFlags, cmd); err != nil {
				return err
			}
			return nil
		},
		RunFuncE: func(p *createParams, cmd *cobra.Command, _ []string) error {
			cfg, err := e.config()
			if err != nil {
				return err
			}

			// The project is resolved as --project, else AWB_PROJECT, else the local
			// file, else the user file.
			target := cfg.DefaultProject
			if cmd.Flags().Changed("project") {
				target = p.Project
			}
			if target == "" {
				return awberr.Usagef(
					"no project: give --project, set AWB_PROJECT, or put \"project\" in %s",
					".awb.yaml or the user configuration file")
			}

			req := backend.IssueCreate{
				Project:  target,
				Title:    p.Title,
				Assignee: p.Assignee,
				Type:     domain.Type(p.Type),
			}
			if !cmd.Flags().Changed("type") {
				req.Type = ""
			}
			if cmd.Flags().Changed("priority") {
				req.Priority = &p.Priority
			}
			if description, err := p.value(e); err != nil {
				return err
			} else if description != nil {
				req.Description = *description
			}

			// The context label is not a default but a value: a new issue carries it in
			// addition to any --label given, so an issue created here stays visible
			// here whatever else it is labelled.
			req.Labels = slices.Clone(p.Labels)
			if cfg.ContextLabel != "" && !slices.Contains(req.Labels, cfg.ContextLabel) {
				req.Labels = append(req.Labels, cfg.ContextLabel)
			}

			req.Relations = collectRelations(p.HasParent, p.BlockedBy, p.DiscoveredFrom, p.Related)

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.CreateIssue(cmd.Context(), req)
			if err != nil {
				return err
			}

			if e.json {
				return e.writeIssueJSON(issue)
			}
			// create is one of the exceptions to "mutating commands print nothing on
			// success": it prints the new ID.
			_, err = io.WriteString(e.stdout, issue.ID+"\n")
			return err
		},
	}.ToCobra()
}

func collectRelations(hasParent string, blockedBy, discoveredFrom, related []string) []backend.NewRelation {
	var relations []backend.NewRelation
	if hasParent != "" {
		relations = append(relations, backend.NewRelation{Type: domain.RelHasParent, Other: hasParent})
	}
	for _, id := range blockedBy {
		relations = append(relations, backend.NewRelation{Type: domain.RelBlockedBy, Other: id})
	}
	for _, id := range discoveredFrom {
		relations = append(relations, backend.NewRelation{Type: domain.RelDiscoveredFrom, Other: id})
	}
	for _, id := range related {
		relations = append(relations, backend.NewRelation{Type: domain.RelRelated, Other: id})
	}
	return relations
}

func newShowCommand(e *env) *cobra.Command {
	return idCommand("show", "Print one issue in full",
		"Print an issue with its relations, derived blocked state and the Markdown\n"+
			"links found in its description.\n\n"+
			"Under --compact this prints the same single line a listing would and\n"+
			"nothing else; --json is what an agent uses when it needs the rest.",
		func(cmd *cobra.Command, id string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.GetIssue(cmd.Context(), id)
			if err != nil {
				return err
			}
			return e.printIssue(issue)
		})
}

// listingParams is a listing's filters, with the flag that says to show them
// on the full screen rather than print them.
type listingParams struct {
	InteractiveFlags
	FilterFlags
}

// listing builds the three list-like commands without search terms, which
// differ only in the filters
// they accept and the ones they fix for themselves.
func listing(e *env, use, short, long string, opts filterOptions,
	fix func(*domain.Filter), withBlockers bool) *cobra.Command {
	cmd := boa.CmdT[listingParams]{
		Use:         use,
		Short:       short,
		Long:        long,
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *listingParams, cmd *cobra.Command) error {
			return filterInit(e, opts, fix)(ctx, &p.FilterFlags, cmd)
		},
		PostCreateFuncCtx: func(ctx *boa.HookContext, p *listingParams, cmd *cobra.Command) error {
			return filterPostCreate(opts)(ctx, &p.FilterFlags, cmd)
		},
		RunFuncE: func(p *listingParams, cmd *cobra.Command, _ []string) error {
			return runListing(e, cmd, &p.FilterFlags, p.Interactive, opts, fix, withBlockers, nil)
		},
	}.ToCobra()
	return cmd
}

func newListCommand(e *env) *cobra.Command {
	return listing(e, "list", "List issues",
		"List issues. By default closed ones are hidden; --include-closed widens\n"+
			"whatever status set is in force.",
		filterOptions{status: true, assignee: true}, nil, false)
}

func newReadyCommand(e *env) *cobra.Command {
	return listing(e, "ready", "List ready issues, highest priority first",
		"An issue is ready when it is open and not blocked.\n\n"+
			"ready lists only unassigned issues, because \"what should\n"+
			"nobody-in-particular pick up next\" is the question it exists to answer.\n"+
			"It therefore takes no assignee filter: --mine, --assignee and --unassigned\n"+
			"are usage errors on it, and \"which issues do I hold\" is awb list --mine.\n\n"+
			"This is the primary agent entry point.",
		filterOptions{}, func(f *domain.Filter) {
			// ready fixes the status set and the assignee filter for itself.
			f.Statuses = []domain.Status{domain.StatusOpen}
			f.Unassigned = true
			f.Readiness = domain.ReadinessReady
		}, false)
}

func newBlockedCommand(e *env) *cobra.Command {
	return listing(e, "blocked", "List issues that are not closed and are blocked",
		"List blocked issues, each with the ids of the issues blocking it.",
		filterOptions{assignee: true}, func(f *domain.Filter) {
			// blocked fixes the status set to the two that are not closed.
			f.Statuses = domain.NotClosedStatuses
			f.Readiness = domain.ReadinessBlocked
		}, true)
}

type searchParams struct {
	InteractiveFlags
	FilterFlags
	Terms []string `positional:"true" required:"true"`
}

func newSearchCommand(e *env) *cobra.Command {
	opts := filterOptions{status: true, assignee: true, relevance: true}
	return boa.CmdT[searchParams]{
		Use:   "search",
		Short: "Full text search over title and description",
		Long: "Search titles and descriptions. Each argument is a literal term: no\n" +
			"operator, wildcard or column prefix is passed through, so no input can\n" +
			"produce a query syntax error. An issue matches when its title and\n" +
			"description together contain all of the terms.\n\n" +
			"Matching is by whole token and is case- and diacritic-insensitive, with no\n" +
			"stemming and no prefix matching: \"parser\" finds \"Parser\" and \"parser,\"\n" +
			"but neither \"pars\" nor \"parsers\" finds \"parser\". Widen the terms rather\n" +
			"than the syntax.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *searchParams, cmd *cobra.Command) error {
			return filterInit(e, opts, nil)(ctx, &p.FilterFlags, cmd)
		},
		PostCreateFuncCtx: func(ctx *boa.HookContext, p *searchParams, cmd *cobra.Command) error {
			return filterPostCreate(opts)(ctx, &p.FilterFlags, cmd)
		},
		RunFuncE: func(p *searchParams, cmd *cobra.Command, _ []string) error {
			return runListing(e, cmd, &p.FilterFlags, p.Interactive, opts, nil, false, p.Terms)
		},
	}.ToCobra()
}

func runListing(e *env, cmd *cobra.Command, flags *FilterFlags, interactive bool,
	opts filterOptions, fix func(*domain.Filter), withBlockers bool, terms []string) error {
	// Whether the listing can be shown at all is settled before it is asked
	// for, so a refusal costs nothing and says only what is wrong with the
	// invocation.
	out, err := e.interactively(interactive)
	if err != nil {
		return err
	}

	filter, err := flags.build(e, cmd, opts)
	if err != nil {
		return err
	}
	if fix != nil {
		fix(filter)
	}
	for _, term := range terms {
		valid, err := domain.ValidateSearchTerm(term)
		if err != nil {
			return err
		}
		filter.Terms = append(filter.Terms, valid)
	}

	be, err := e.backend(cmd.Context())
	if err != nil {
		return err
	}
	page, err := be.ListIssues(cmd.Context(), filter)
	if err != nil {
		return err
	}
	if out != nil {
		return e.pickIssue(cmd.Context(), be, out, page.Issues, withBlockers)
	}
	return e.printIssues(page.Issues, withBlockers)
}

type updateParams struct {
	DescriptionFlags
	ID       string  `positional:"true" required:"true"`
	Title    *string `long:"title" help:"new title"`
	Type     *string `long:"type" alts:"epic,feature,bug,task,chore" help:"epic, feature, bug, task or chore"`
	Priority *int    `long:"priority" alts:"0,1,2,3,4" help:"0 (highest) to 4 (lowest)"`
	Force    bool    `long:"force" optional:"true" help:"replace the description without a fetched-version precondition"`
}

func newUpdateCommand(e *env) *cobra.Command {
	return boa.CmdT[updateParams]{
		Use:   "update",
		Short: "Change an issue's fields",
		Long: "Change the title, description, type or priority.\n\n" +
			"A description file must first be fetched with awb description get, whose\n" +
			"receipt prevents overwriting a concurrent edit. --force deliberately\n" +
			"replaces a description without that precondition.\n\n" +
			"update cannot change the status or the assignee: claim, release, close and\n" +
			"reopen are the only transitions of either, which keeps in_progress and an\n" +
			"assignee from drifting apart and keeps a claim from being taken silently.\n\n" +
			"Giving no field flag at all succeeds and changes nothing.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *updateParams, cmd *cobra.Command) error {
			return describe("issue")(ctx, &p.DescriptionFlags, cmd)
		},
		RunFuncE: func(p *updateParams, cmd *cobra.Command, _ []string) error {
			var patch backend.IssuePatch
			if p.Title != nil {
				patch.Title = p.Title
			}
			if p.Type != nil {
				t := domain.Type(*p.Type)
				patch.Type = &t
			}
			if p.Priority != nil {
				patch.Priority = p.Priority
			}
			description, ifMatch, err := p.valueForUpdate(e, "issue", p.ID, p.Force)
			if err != nil {
				return err
			}
			if p.Force && description == nil {
				return awberr.Usagef("--force only applies when replacing a description")
			}
			patch.Description = description

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.UpdateIssue(cmd.Context(), p.ID, patch, ifMatch)
			if err != nil {
				file := ""
				if p.File != nil {
					file = *p.File
				}
				return descriptionPreconditionError(err, "issue", p.ID, file)
			}
			return e.mutated(issue)
		},
	}.ToCobra()
}

func newLabelCommand(e *env) *cobra.Command {
	run := func(add bool) func(*cobra.Command, string, string) error {
		return func(cmd *cobra.Command, id, label string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			var issue *domain.Issue
			if add {
				issue, err = be.AddLabel(cmd.Context(), id, label, "")
			} else {
				issue, err = be.RemoveLabel(cmd.Context(), id, label, "")
			}
			if err != nil {
				return err
			}
			return e.mutated(issue)
		}
	}

	return group("label", "Add or remove a label",
		"Labels are managed one per invocation, so the command matches the API\n"+
			"endpoint one for one and neither surface has to define a partial failure.",
		labelCommand("add", "Add a label; adding one the issue already carries changes nothing", run(true)),
		labelCommand("rm", "Remove a label; removing one it does not carry changes nothing", run(false)))
}

type labelParams struct {
	ID    string `positional:"true" required:"true"`
	Label string `positional:"true" required:"true"`
}

func labelCommand(use, short string, run func(*cobra.Command, string, string) error) *cobra.Command {
	return boa.CmdT[labelParams]{
		Use: use, Short: short, ParamEnrich: boaParams,
		RunFuncE: func(p *labelParams, cmd *cobra.Command, _ []string) error {
			return run(cmd, p.ID, p.Label)
		},
	}.ToCobra()
}

type claimParams struct {
	ID    string  `positional:"true" required:"true"`
	As    *string `long:"as" help:"claim for this name instead of your identity"`
	Force bool    `long:"force" optional:"true" help:"override a held, blocked or closed issue"`
}

func newClaimCommand(e *env) *cobra.Command {
	return boa.CmdT[claimParams]{
		Use:   "claim",
		Short: "Atomically set the assignee and status to in_progress",
		Long: "Claim an issue.\n\n" +
			"Claiming one you already hold succeeds. It fails if the issue is assigned\n" +
			"to somebody else, blocked, or closed; --force overrides all three. A close\n" +
			"reason remains in the issue's activity history.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *claimParams, cmd *cobra.Command, _ []string) error {
			assignee := ""
			if p.As == nil {
				// The CLI resolves its identity locally and always states it explicitly,
				// so a remote claim records exactly what the same command would record
				// locally.
				var err error
				if assignee, err = e.identity(); err != nil {
					return err
				}
			} else {
				assignee = *p.As
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.Claim(cmd.Context(), p.ID,
				backend.ClaimRequest{Assignee: assignee, Force: p.Force}, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}.ToCobra()
}

type forceParams struct {
	ID    string `positional:"true" required:"true"`
	Force bool   `long:"force" optional:"true"`
}

func newReleaseCommand(e *env) *cobra.Command {
	return boa.CmdT[forceParams]{
		Use:   "release",
		Short: "Clear the assignee and set the status back to open",
		Long: "Release an issue.\n\n" +
			"Releasing one that is already open and unassigned succeeds. It fails on a\n" +
			"closed issue, or on one assigned to somebody else, unless --force.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *forceParams, _ *cobra.Command) error {
			boa.GetParamT(ctx, &p.Force).SetDescription("release a closed issue, or somebody else's")
			return nil
		},
		RunFuncE: func(p *forceParams, cmd *cobra.Command, _ []string) error {
			req := backend.ReleaseRequest{Force: p.Force}
			if !p.Force {
				// The identity serves only the "assigned to someone else" refusal, so it
				// is needed only when that refusal applies.
				identity, err := e.identity()
				if err != nil {
					return err
				}
				req.Assignee = identity
			} else if cfg, err := e.config(); err == nil {
				req.Assignee = cfg.Identity
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.Release(cmd.Context(), p.ID, req, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}.ToCobra()
}

type closeParams struct {
	ID     string  `positional:"true" required:"true"`
	Reason *string `long:"reason" help:"record why it was closed"`
}

func newCloseCommand(e *env) *cobra.Command {
	return boa.CmdT[closeParams]{
		Use:   "close",
		Short: "Set the status to closed",
		Long: "Close an issue.\n\n" +
			"A non-empty --reason is recorded as a typed comment on the closing\n" +
			"transition. Closing a closed issue succeeds and changes nothing. The\n" +
			"assignee is left alone, since it records who did the work.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *closeParams, cmd *cobra.Command, _ []string) error {
			req := backend.CloseRequest{Reason: p.Reason}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.CloseIssue(cmd.Context(), p.ID, req, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}.ToCobra()
}

func newReopenCommand(e *env) *cobra.Command {
	return idCommand("reopen",
		"Set the status to open and clear the assignee",
		"Reopen a closed issue, returning it to the pool awb ready draws from.\n\n"+
			"Its historical close-reason comment remains in the activity stream.\n\n"+
			"It acts only on a closed issue: on one that is not closed it succeeds and\n"+
			"changes nothing, whatever its assignee, so it can never take a claim away\n"+
			"from somebody who is working.", func(cmd *cobra.Command, id string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.Reopen(cmd.Context(), id, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		})
}

func newDeleteCommand(e *env) *cobra.Command {
	return boa.CmdT[forceParams]{
		Use:   "delete",
		Short: "Hard delete an issue and its relations",
		Long: "Delete an issue. This is not recoverable.\n\n" +
			"It never refuses on account of dependents and has no --cascade: it orphans\n" +
			"any children and drops every relation, and reports how many went, since\n" +
			"removing a blocker silently makes other issues ready and orphaning children\n" +
			"makes a decomposed parent's work top-level.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *forceParams, _ *cobra.Command) error {
			boa.GetParamT(ctx, &p.Force).SetDescription("confirm the deletion")
			return nil
		},
		RunFuncE: func(p *forceParams, cmd *cobra.Command, _ []string) error {
			// A missing --force depends on the arguments alone and not on anything the
			// database holds, so it is a usage error.
			if !p.Force {
				return awberr.Usagef("awb delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteIssue(cmd.Context(), p.ID, "")
			if err != nil {
				return err
			}
			if e.json {
				// A deleting command prints the object as it was immediately before
				// deletion, relations included.
				return e.writeIssueJSON(&deleted.Issue)
			}
			return e.summarise("Deleted %s and %d relation(s).\n",
				deleted.Issue.ID, deleted.RelationsRemoved)
		},
	}.ToCobra()
}
