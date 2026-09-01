package cli

import (
	"fmt"
	"io"

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

func newWorkspaceCommand(e *env) *cobra.Command {
	cmd := group("workspace", "Manage workspaces",
		"A workspace is the top-level organising unit. Every issue belongs immutably to exactly one; the workspace key is its stable ID prefix.",
		newWorkspaceCreateCommand(e),
		newWorkspaceUpdateCommand(e),
		newWorkspaceDescriptionCommand(e),
		newWorkspaceShowCommand(e),
		newWorkspaceListCommand(e),
		newWorkspaceArchiveCommand(e),
		newWorkspaceRestoreCommand(e),
		newWorkspaceActivityCommand(e),
		newWorkspaceDeleteCommand(e),
		newWorkspaceGrantCommand(e),
		newWorkspaceRevokeCommand(e),
		newWorkspaceMembersCommand(e),
	)
	return cmd
}

func newWorkspaceActivityCommand(e *env) *cobra.Command {
	type params struct {
		Key    string `positional:"true" required:"true"`
		Limit  *int   `long:"limit" optional:"true" help:"cap the entries returned"`
		Offset *int   `long:"offset" optional:"true" help:"skip this many entries"`
	}
	return boa.CmdT[params]{
		Use: "activity", Short: "List a workspace's archive and restore history", ParamEnrich: boaParams,
		RunFuncE: func(p *params, cmd *cobra.Command, _ []string) error {
			if err := checkPaging(p.Limit, p.Offset); err != nil {
				return err
			}
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListWorkspaceActivity(cmd.Context(), p.Key, p.Limit, p.Offset)
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(page.Activity)
			}
			for i := range page.Activity {
				if _, err := io.WriteString(e.stdout, domain.CompactWorkspaceActivityLine(&page.Activity[i])+"\n"); err != nil {
					return err
				}
			}
			return nil
		},
	}.ToCobra()
}

type workspaceCreateParams struct {
	DescriptionFlags
	Key  string `positional:"true" required:"true"`
	Name string `long:"name" optional:"true" help:"human-readable name; defaults to the key"`
}

func newWorkspaceCreateCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceCreateParams]{
		Use:   "create",
		Short: "Create a workspace",
		Long: "Create a workspace.\n\n" +
			"The key is lowercase ASCII letters, digits and hyphens, starting with a\n" +
			"letter, at most 16 characters. It becomes the immutable issue ID prefix;\n" +
			"issues cannot move to another workspace. --name defaults to the key.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *workspaceCreateParams, cmd *cobra.Command) error {
			return describe("workspace")(ctx, &p.DescriptionFlags, cmd)
		},
		RunFuncE: func(p *workspaceCreateParams, cmd *cobra.Command, _ []string) error {
			req := backend.WorkspaceCreate{Key: p.Key, Name: p.Name}
			if description, err := p.value(e); err != nil {
				return err
			} else if description != nil {
				req.Description = *description
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			workspace, err := be.CreateWorkspace(cmd.Context(), req)
			if err != nil {
				return err
			}
			return e.mutatedWorkspace(workspace)
		},
	}.ToCobra()
}

type workspaceUpdateParams struct {
	DescriptionFlags
	Key   string  `positional:"true" required:"true"`
	Name  *string `long:"name" help:"human-readable name; \"\" restores the key"`
	Force bool    `long:"force" optional:"true" help:"replace the description without a fetched-version precondition"`
}

func newWorkspaceUpdateCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceUpdateParams]{
		Use:   "update",
		Short: "Change a workspace's name or description",
		Long: "Change a workspace's name or description. Its key and issue boundary are immutable.\n\n" +
			"A description file must first be fetched with awb workspace description get,\n" +
			"whose receipt prevents overwriting a concurrent edit. --force deliberately\n" +
			"replaces a description without that precondition. --name \"\" restores the\n" +
			"key as the name.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *workspaceUpdateParams, cmd *cobra.Command) error {
			return describe("workspace")(ctx, &p.DescriptionFlags, cmd)
		},
		RunFuncE: func(p *workspaceUpdateParams, cmd *cobra.Command, _ []string) error {
			patch := backend.WorkspacePatch{Name: p.Name}
			description, ifMatch, err := p.valueForUpdate(e, "workspace", p.Key, p.Force)
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
			workspace, err := be.UpdateWorkspace(cmd.Context(), p.Key, patch, ifMatch)
			if err != nil {
				file := ""
				if p.File != nil {
					file = *p.File
				}
				return descriptionPreconditionError(err, "workspace", p.Key, file)
			}
			return e.mutatedWorkspace(workspace)
		},
	}.ToCobra()
}

func newWorkspaceShowCommand(e *env) *cobra.Command {
	type params struct {
		Key string `positional:"true" required:"true"`
	}
	return boa.CmdT[params]{
		Use:   "show",
		Short: "Print one workspace in full",
		Long: "Print a workspace with its description and its count of issues that are not\n" +
			"closed.\n\n" +
			"On a terminal the description is drawn as the Markdown it is. Under\n" +
			"--compact this prints the same single line workspace list would and nothing\n" +
			"else; --json is what a script uses when it needs the rest.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *params, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			workspace, err := be.GetWorkspace(cmd.Context(), p.Key)
			if err != nil {
				return err
			}
			return e.printWorkspace(workspace)
		},
	}.ToCobra()
}

// workspaceListParams is the picker flag, the state selection, the ordering and
// the window. Workspace ordering is its own vocabulary because "active" is a
// derived count, not a stored column.
type workspaceListParams struct {
	InteractiveFlags
	Archived bool   `long:"archived" optional:"true" help:"list archived workspaces instead of active workspaces"`
	All      bool   `long:"all" optional:"true" help:"list active and archived workspaces"`
	Sort     string `long:"sort" optional:"true"`
	Limit    *int   `long:"limit" optional:"true" help:"cap the number of results; zero returns none"`
	Offset   *int   `long:"offset" optional:"true" help:"skip this many results"`
}

func newWorkspaceListCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceListParams]{
		Use:         "list",
		Short:       "List workspaces with counts of issues that are not closed",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *workspaceListParams, _ *cobra.Command) error {
			// Offered and validated from the parser's own vocabulary, as --sort on
			// the issue listings is.
			sortParam := boa.GetParamT(ctx, &p.Sort)
			sortParam.SetAlternatives(domain.WorkspaceSortAlternatives())
			sortParam.SetDescription(domain.WorkspaceSortHelp())
			return nil
		},
		RunFuncE: func(p *workspaceListParams, cmd *cobra.Command, _ []string) error {
			if p.Archived && p.All {
				return awberr.Usagef("--archived and --all are mutually exclusive")
			}
			out, err := e.interactively(p.Interactive)
			if err != nil {
				return err
			}

			sort := domain.DefaultWorkspaceSort
			if p.Sort != "" {
				if sort, err = domain.ParseWorkspaceSort(p.Sort); err != nil {
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
			state := domain.WorkspacesActive
			if p.Archived {
				state = domain.WorkspacesArchived
			} else if p.All {
				state = domain.WorkspacesAll
			}
			page, err := be.ListWorkspacesByState(cmd.Context(), "", state, sort, p.Limit, p.Offset)
			if err != nil {
				return err
			}
			if out != nil {
				return e.pickWorkspace(cmd.Context(), be, out, page.Workspaces)
			}
			return e.printWorkspaces(page.Workspaces)
		},
	}.ToCobra()
}

func newWorkspaceArchiveCommand(e *env) *cobra.Command {
	type params struct {
		Key string `positional:"true" required:"true"`
	}
	return boa.CmdT[params]{
		Use:   "archive",
		Short: "Archive a workspace as retained read-only history",
		Long: "Archive a workspace without deleting or transferring anything. Its stable URLs,\n" +
			"workspace-prefixed issue IDs and history remain\n" +
			"readable, while normal listings and target pickers omit it and work mutations\n" +
			"are refused. Repeating the command is idempotent.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *params, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			workspace, err := be.ArchiveWorkspace(cmd.Context(), p.Key, "")
			if err != nil {
				return err
			}
			return e.mutatedWorkspace(workspace)
		},
	}.ToCobra()
}

func newWorkspaceRestoreCommand(e *env) *cobra.Command {
	type params struct {
		Key string `positional:"true" required:"true"`
	}
	return boa.CmdT[params]{
		Use:         "restore",
		Short:       "Restore an archived workspace",
		Long:        "Restore the same workspace boundary, including all retained records and stable URLs.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *params, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			workspace, err := be.RestoreWorkspace(cmd.Context(), p.Key, "")
			if err != nil {
				return err
			}
			return e.mutatedWorkspace(workspace)
		},
	}.ToCobra()
}

type workspaceDeleteParams struct {
	Key     string `positional:"true" required:"true"`
	Force   bool   `long:"force" optional:"true" help:"confirm the deletion"`
	Cascade bool   `long:"cascade" optional:"true" help:"also delete the issues the workspace holds"`
}

func newWorkspaceDeleteCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceDeleteParams]{
		Use:   "delete",
		Short: "Delete a workspace",
		Long: "Delete a workspace.\n\n" +
			"It refuses while the workspace holds any issue at all — closed ones included,\n" +
			"so the refusal is wider than the count workspace list shows and --force alone\n" +
			"can never destroy closed history — unless --cascade is also given, which\n" +
			"deletes those issues and their relations, including relations to issues in\n" +
			"other workspaces, which may unblock work elsewhere.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *workspaceDeleteParams, cmd *cobra.Command, _ []string) error {
			if !p.Force {
				return awberr.Usagef("awb workspace delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteWorkspace(cmd.Context(), p.Key, p.Cascade, "")
			if err != nil {
				return err
			}
			if e.json {
				return e.writeWorkspaceJSON(&deleted.Workspace)
			}
			if p.Cascade {
				return e.summarise("Deleted workspace %s and the issues it held.\n",
					deleted.Workspace.Key)
			}
			return e.summarise("Deleted workspace %s.\n", deleted.Workspace.Key)
		},
	}.ToCobra()
}
