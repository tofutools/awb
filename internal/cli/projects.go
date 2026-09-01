package cli

import (
	"fmt"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// summarise prints the one-line human-readable summary the deleting commands
// and awb demo give. It is the one output with no guaranteed format; a script
// uses --json, which returns the object.
func (e *env) summarise(format string, args ...any) error {
	_, _ = fmt.Fprintf(e.stdout, format, args...)
	return e.stdout.Err()
}

func newProjectCommand(e *env) *cobra.Command {
	return group("project", "Manage projects",
		"A project is the top-level organising unit; every issue belongs to exactly one.",
		newProjectCreateCommand(e),
		newProjectUpdateCommand(e),
		newProjectDescriptionCommand(e),
		newProjectShowCommand(e),
		newProjectListCommand(e),
		newProjectDeleteCommand(e),
		newProjectGrantCommand(e),
		newProjectRevokeCommand(e),
		newProjectMembersCommand(e),
	)
}

type projectCreateParams struct {
	DescriptionFlags
	Key  string `positional:"true" required:"true"`
	Name string `long:"name" optional:"true" help:"human-readable name; defaults to the key"`
}

func newProjectCreateCommand(e *env) *cobra.Command {
	return boa.CmdT[projectCreateParams]{
		Use:   "create",
		Short: "Create a project",
		Long: "Create a project.\n\n" +
			"The key is lowercase ASCII letters, digits and hyphens, starting with a\n" +
			"letter, at most 16 characters. It becomes the issue ID prefix and is\n" +
			"immutable. --name defaults to the key.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *projectCreateParams, cmd *cobra.Command) error {
			return describe("project")(ctx, &p.DescriptionFlags, cmd)
		},
		RunFuncE: func(p *projectCreateParams, cmd *cobra.Command, _ []string) error {
			req := backend.ProjectCreate{Key: p.Key, Name: p.Name}
			if description, err := p.value(e); err != nil {
				return err
			} else if description != nil {
				req.Description = *description
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			project, err := be.CreateProject(cmd.Context(), req)
			if err != nil {
				return err
			}
			return e.mutatedProject(project)
		},
	}.ToCobra()
}

type projectUpdateParams struct {
	DescriptionFlags
	Key   string  `positional:"true" required:"true"`
	Name  *string `long:"name" help:"human-readable name; \"\" restores the key"`
	Force bool    `long:"force" optional:"true" help:"replace the description without a fetched-version precondition"`
}

func newProjectUpdateCommand(e *env) *cobra.Command {
	return boa.CmdT[projectUpdateParams]{
		Use:   "update",
		Short: "Change a project's name or description",
		Long: "Change a project's name or description. The key itself is immutable.\n\n" +
			"A description file must first be fetched with awb project description get,\n" +
			"whose receipt prevents overwriting a concurrent edit. --force deliberately\n" +
			"replaces a description without that precondition. --name \"\" restores the\n" +
			"key as the name.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *projectUpdateParams, cmd *cobra.Command) error {
			return describe("project")(ctx, &p.DescriptionFlags, cmd)
		},
		RunFuncE: func(p *projectUpdateParams, cmd *cobra.Command, _ []string) error {
			patch := backend.ProjectPatch{Name: p.Name}
			description, ifMatch, err := p.valueForUpdate(e, "project", p.Key, p.Force)
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
			project, err := be.UpdateProject(cmd.Context(), p.Key, patch, ifMatch)
			if err != nil {
				file := ""
				if p.File != nil {
					file = *p.File
				}
				return descriptionPreconditionError(err, "project", p.Key, file)
			}
			return e.mutatedProject(project)
		},
	}.ToCobra()
}

func newProjectShowCommand(e *env) *cobra.Command {
	type params struct {
		Key string `positional:"true" required:"true"`
	}
	return boa.CmdT[params]{
		Use:   "show",
		Short: "Print one project in full",
		Long: "Print a project with its description and its count of issues that are not\n" +
			"closed.\n\n" +
			"On a terminal the description is drawn as the Markdown it is. Under\n" +
			"--compact this prints the same single line project list would and nothing\n" +
			"else; --json is what a script uses when it needs the rest.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *params, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			project, err := be.GetProject(cmd.Context(), p.Key)
			if err != nil {
				return err
			}
			return e.printProject(project)
		},
	}.ToCobra()
}

// projectListParams is the picker flag plus the ordering and the window.
// Project ordering is its own vocabulary because "active" is a derived count,
// not a stored column.
type projectListParams struct {
	InteractiveFlags
	Sort   string `long:"sort" optional:"true"`
	Limit  *int   `long:"limit" optional:"true" help:"cap the number of results; zero returns none"`
	Offset *int   `long:"offset" optional:"true" help:"skip this many results"`
}

func newProjectListCommand(e *env) *cobra.Command {
	return boa.CmdT[projectListParams]{
		Use:         "list",
		Short:       "List projects with counts of issues that are not closed",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *projectListParams, _ *cobra.Command) error {
			// Offered and validated from the parser's own vocabulary, as --sort on
			// the issue listings is.
			sortParam := boa.GetParamT(ctx, &p.Sort)
			sortParam.SetAlternatives(domain.ProjectSortAlternatives())
			sortParam.SetDescription(domain.ProjectSortHelp())
			return nil
		},
		RunFuncE: func(p *projectListParams, cmd *cobra.Command, _ []string) error {
			out, err := e.interactively(p.Interactive)
			if err != nil {
				return err
			}

			sort := domain.DefaultProjectSort
			if p.Sort != "" {
				if sort, err = domain.ParseProjectSort(p.Sort); err != nil {
					return err
				}
			}
			if err := checkPaging(p.Limit, p.Offset); err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListProjects(cmd.Context(), "", sort, p.Limit, p.Offset)
			if err != nil {
				return err
			}
			if out != nil {
				return e.pickProject(cmd.Context(), be, out, page.Projects)
			}
			return e.printProjects(page.Projects)
		},
	}.ToCobra()
}

type projectDeleteParams struct {
	Key     string `positional:"true" required:"true"`
	Force   bool   `long:"force" optional:"true" help:"confirm the deletion"`
	Cascade bool   `long:"cascade" optional:"true" help:"also delete the issues the project holds"`
}

func newProjectDeleteCommand(e *env) *cobra.Command {
	return boa.CmdT[projectDeleteParams]{
		Use:   "delete",
		Short: "Delete a project",
		Long: "Delete a project.\n\n" +
			"It refuses while the project holds any issue at all — closed ones included,\n" +
			"so the refusal is wider than the count project list shows and --force alone\n" +
			"can never destroy closed history — unless --cascade is also given, which\n" +
			"deletes those issues and their relations, including relations to issues in\n" +
			"other projects, which may unblock work elsewhere.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *projectDeleteParams, cmd *cobra.Command, _ []string) error {
			if !p.Force {
				return awberr.Usagef("awb project delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteProject(cmd.Context(), p.Key, p.Cascade, "")
			if err != nil {
				return err
			}
			if e.json {
				return e.writeProjectJSON(&deleted.Project)
			}
			if p.Cascade {
				return e.summarise("Deleted project %s and the issues it held.\n",
					deleted.Project.Key)
			}
			return e.summarise("Deleted project %s.\n", deleted.Project.Key)
		},
	}.ToCobra()
}
