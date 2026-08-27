package cli

import (
	"io"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// descriptionFlags are --description and --description-file, which are mutually
// exclusive. Both replace the description outright, taking the bytes exactly as
// given: a description is never trimmed, so a trailing line feed from a heredoc
// or an editor is part of it (SPEC §4.3).
type descriptionFlags struct {
	text string
	file string
}

func (d *descriptionFlags) register(cmd *cobra.Command, what string) {
	cmd.Flags().StringVar(&d.text, "description", "", "markdown description of the "+what)
	cmd.Flags().StringVar(&d.file, "description-file", "",
		"read the description from this file, or from stdin with \"-\"")
}

// value returns the description, or nil when neither flag was given.
func (d *descriptionFlags) value(e *env, cmd *cobra.Command) (*string, error) {
	hasText := cmd.Flags().Changed("description")
	hasFile := cmd.Flags().Changed("description-file")

	switch {
	case hasText && hasFile:
		return nil, awberr.Usagef("--description and --description-file are mutually exclusive")
	case hasText:
		text := d.text
		return &text, nil
	case hasFile:
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

func (d *descriptionFlags) read(e *env) ([]byte, error) {
	// This is the only use of stdin, and is not the bulk import excluded by
	// SPEC §1.2.
	if d.file == "-" {
		data, err := io.ReadAll(e.stdin)
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read the description from stdin")
		}
		return data, nil
	}
	data, err := os.ReadFile(d.file)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read %s", d.file)
	}
	return data, nil
}

func newCreateCommand(e *env) *cobra.Command {
	var (
		desc           descriptionFlags
		issueType      string
		priority       int
		labels         []string
		assignee       string
		project        string
		hasParent      string
		blockedBy      []string
		discoveredFrom []string
		relatedTo      []string
	)

	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create an issue, with its labels and relations, in one transaction",
		Long: "Create an issue and print its ID.\n\n" +
			"The relation flags read \"the new issue — relation — the named issue\",\n" +
			"the single convention of the whole tool.\n\n" +
			"Creating with an assignee is an atomic create-and-claim: --assignee also\n" +
			"sets the status to in_progress, so a new issue is never open and assigned\n" +
			"at once.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := e.config()
			if err != nil {
				return err
			}

			// The project is resolved as --project, else AWB_PROJECT, else the
			// local file, else the user file (SPEC §5).
			target := cfg.CreateProject
			if cmd.Flags().Changed("project") {
				target = project
			}
			if target == "" {
				return awberr.Usagef(
					"no project: give --project, set AWB_PROJECT, or put \"project\" in %s",
					".awb.yaml or the user configuration file")
			}

			req := backend.IssueCreate{
				Project:  target,
				Title:    args[0],
				Assignee: assignee,
				Type:     domain.Type(issueType),
			}
			if !cmd.Flags().Changed("type") {
				req.Type = ""
			}
			if cmd.Flags().Changed("priority") {
				req.Priority = &priority
			}
			if description, err := desc.value(e, cmd); err != nil {
				return err
			} else if description != nil {
				req.Description = *description
			}

			// The context label is not a default but a value: a new issue
			// carries it in addition to any --label given, so an issue created
			// here stays visible here whatever else it is labelled (SPEC §5).
			req.Labels = slices.Clone(labels)
			if cfg.ContextLabel != "" && !slices.Contains(req.Labels, cfg.ContextLabel) {
				req.Labels = append(req.Labels, cfg.ContextLabel)
			}

			req.Relations = collectRelations(hasParent, blockedBy, discoveredFrom, relatedTo)

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.CreateIssue(cmd.Context(), req)
			if err != nil {
				return err
			}

			if e.json {
				return e.writeJSON(issue)
			}
			// create is one of the two exceptions to "mutating commands print
			// nothing on success": it prints the new ID (SPEC §4.1).
			_, err = io.WriteString(e.stdout, issue.ID+"\n")
			return err
		},
	}

	cmd.Flags().StringVar(&issueType, "type", string(domain.DefaultType),
		"epic, feature, bug, task or chore")
	cmd.Flags().IntVar(&priority, "priority", domain.DefaultPriority, "0 (highest) to 4 (lowest)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "add this label; repeatable")
	cmd.Flags().StringVar(&assignee, "assignee", "", "create and claim in one step")
	cmd.Flags().StringVar(&project, "project", "", "the project to create the issue in")
	cmd.Flags().StringVar(&hasParent, "has-parent", "", "the new issue is part of decomposing this one")
	cmd.Flags().StringArrayVar(&blockedBy, "blocked-by", nil,
		"the new issue cannot start until this one is closed; repeatable")
	cmd.Flags().StringArrayVar(&discoveredFrom, "discovered-from", nil,
		"the new issue was found while working on this one; repeatable")
	cmd.Flags().StringArrayVar(&relatedTo, "related", nil, "loose association; repeatable")
	desc.register(cmd, "issue")

	return cmd
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
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Print one issue in full",
		Long: "Print an issue with its relations, derived blocked state and the Markdown\n" +
			"links found in its description.\n\n" +
			"Under --compact this prints the same single line a listing would and\n" +
			"nothing else; --json is what an agent uses when it needs the rest.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.GetIssue(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return e.printIssue(issue)
		},
	}
}

// listing builds the four list-like commands, which differ only in the filters
// they accept and the ones they fix for themselves.
func listing(e *env, use, short, long string, opts filterOptions,
	fix func(*domain.Filter), withBlockers bool, args cobra.PositionalArgs) *cobra.Command {
	var flags filterFlags

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  args,
		RunE: func(cmd *cobra.Command, positional []string) error {
			filter, err := flags.build(e, cmd, opts)
			if err != nil {
				return err
			}
			if fix != nil {
				fix(filter)
			}
			for _, term := range positional {
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
			return e.printIssues(page.Issues, withBlockers)
		},
	}
	flags.register(cmd, opts)
	return cmd
}

func newListCommand(e *env) *cobra.Command {
	return listing(e, "list", "List issues",
		"List issues. By default closed ones are hidden; --include-closed widens\n"+
			"whatever status set is in force.",
		filterOptions{status: true, assignee: true}, nil, false, noArgs)
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
		}, false, noArgs)
}

func newBlockedCommand(e *env) *cobra.Command {
	return listing(e, "blocked", "List issues that are not closed and are blocked",
		"List blocked issues, each with the ids of the issues blocking it.",
		filterOptions{assignee: true}, func(f *domain.Filter) {
			// blocked fixes the status set to the two that are not closed.
			f.Statuses = domain.NotClosedStatuses
			f.Readiness = domain.ReadinessBlocked
		}, true, noArgs)
}

func newSearchCommand(e *env) *cobra.Command {
	return listing(e, "search <terms>...", "Full text search over title and description",
		"Search titles and descriptions. Each argument is a literal term: no\n"+
			"operator, wildcard or column prefix is passed through, so no input can\n"+
			"produce a query syntax error. An issue matches when its title and\n"+
			"description together contain all of the terms.\n\n"+
			"Matching is by whole token and is case- and diacritic-insensitive, with no\n"+
			"stemming and no prefix matching: \"parser\" finds \"Parser\" and \"parser,\"\n"+
			"but neither \"pars\" nor \"parsers\" finds \"parser\". Widen the terms rather\n"+
			"than the syntax.",
		filterOptions{status: true, assignee: true, relevance: true}, nil, false, minArgs(1))
}

func newUpdateCommand(e *env) *cobra.Command {
	var (
		desc      descriptionFlags
		title     string
		issueType string
		priority  int
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Change an issue's fields",
		Long: "Change the title, description, type or priority.\n\n" +
			"update cannot change the status or the assignee: claim, release, close and\n" +
			"reopen are the only transitions of either, which keeps in_progress and an\n" +
			"assignee from drifting apart and keeps a claim from being taken silently.\n\n" +
			"Giving no field flag at all succeeds and changes nothing.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var patch backend.IssuePatch
			if cmd.Flags().Changed("title") {
				patch.Title = &title
			}
			if cmd.Flags().Changed("type") {
				t := domain.Type(issueType)
				patch.Type = &t
			}
			if cmd.Flags().Changed("priority") {
				patch.Priority = &priority
			}
			description, err := desc.value(e, cmd)
			if err != nil {
				return err
			}
			patch.Description = description

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.UpdateIssue(cmd.Context(), args[0], patch, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&issueType, "type", "", "epic, feature, bug, task or chore")
	cmd.Flags().IntVar(&priority, "priority", domain.DefaultPriority, "0 (highest) to 4 (lowest)")
	desc.register(cmd, "issue")
	return cmd
}

func newLabelCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label",
		Short: "Add or remove a label",
		Long: "Labels are managed one per invocation, so the command matches the API\n" +
			"endpoint one for one and neither surface has to define a partial failure.",
	}

	run := func(add bool) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			var issue *domain.Issue
			if add {
				issue, err = be.AddLabel(cmd.Context(), args[0], args[1], "")
			} else {
				issue, err = be.RemoveLabel(cmd.Context(), args[0], args[1], "")
			}
			if err != nil {
				return err
			}
			return e.mutated(issue)
		}
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "add <id> <label>",
		Short: "Add a label; adding one the issue already carries changes nothing",
		Args:  exactArgs(2),
		RunE:  run(true),
	}, &cobra.Command{
		Use:   "rm <id> <label>",
		Short: "Remove a label; removing one it does not carry changes nothing",
		Args:  exactArgs(2),
		RunE:  run(false),
	})
	return cmd
}

func newClaimCommand(e *env) *cobra.Command {
	var (
		as    string
		force bool
	)

	cmd := &cobra.Command{
		Use:   "claim <id>",
		Short: "Atomically set the assignee and status to in_progress",
		Long: "Claim an issue.\n\n" +
			"Claiming one you already hold succeeds. It fails if the issue is assigned\n" +
			"to somebody else, blocked, or closed; --force overrides all three, and a\n" +
			"forced claim on a closed issue clears the close reason along with the\n" +
			"status.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			assignee := as
			if !cmd.Flags().Changed("as") {
				// The CLI resolves its identity locally and always states it
				// explicitly, so a remote claim records exactly what the same
				// command would record locally (SPEC §6).
				var err error
				if assignee, err = e.identity(); err != nil {
					return err
				}
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.Claim(cmd.Context(), args[0],
				backend.ClaimRequest{Assignee: assignee, Force: force}, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}

	cmd.Flags().StringVar(&as, "as", "", "claim for this name instead of your identity")
	cmd.Flags().BoolVar(&force, "force", false, "override a held, blocked or closed issue")
	return cmd
}

func newReleaseCommand(e *env) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "release <id>",
		Short: "Clear the assignee and set the status back to open",
		Long: "Release an issue.\n\n" +
			"Releasing one that is already open and unassigned succeeds. It fails on a\n" +
			"closed issue, or on one assigned to somebody else, unless --force.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := backend.ReleaseRequest{Force: force}
			if !force {
				// The identity serves only the "assigned to someone else"
				// refusal, so it is needed only when that refusal applies.
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
			issue, err := be.Release(cmd.Context(), args[0], req, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "release a closed issue, or somebody else's")
	return cmd
}

func newCloseCommand(e *env) *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "close <id>",
		Short: "Set the status to closed",
		Long: "Close an issue.\n\n" +
			"Closing a closed issue succeeds; omitting --reason leaves the recorded\n" +
			"reason alone and --reason \"\" clears it. The assignee is left alone, since\n" +
			"it records who did the work.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req backend.CloseRequest
			if cmd.Flags().Changed("reason") {
				req.Reason = &reason
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.CloseIssue(cmd.Context(), args[0], req, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "record why it was closed")
	return cmd
}

func newReopenCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id>",
		Short: "Set the status to open, clearing the close reason and the assignee",
		Long: "Reopen a closed issue, returning it to the pool awb ready draws from.\n\n" +
			"It acts only on a closed issue: on one that is not closed it succeeds and\n" +
			"changes nothing, whatever its assignee, so it can never take a claim away\n" +
			"from somebody who is working.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.Reopen(cmd.Context(), args[0], "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}
}

func newDeleteCommand(e *env) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <id> --force",
		Short: "Hard delete an issue and its relations",
		Long: "Delete an issue. This is not recoverable.\n\n" +
			"It never refuses on account of dependents and has no --cascade: it orphans\n" +
			"any children and drops every relation, and reports how many went, since\n" +
			"removing a blocker silently makes other issues ready and orphaning children\n" +
			"makes a decomposed parent's work top-level.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A missing --force depends on the arguments alone and not on
			// anything the database holds, so it is a usage error (SPEC §4.1).
			if !force {
				return awberr.Usagef("awb delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteIssue(cmd.Context(), args[0], "")
			if err != nil {
				return err
			}
			if e.json {
				// A deleting command prints the object as it was immediately
				// before deletion, relations included.
				return e.writeJSON(&deleted.Issue)
			}
			return e.summarise("Deleted %s and %d relation(s).\n",
				deleted.Issue.ID, deleted.RelationsRemoved)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "confirm the deletion")
	return cmd
}
