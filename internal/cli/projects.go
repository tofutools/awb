package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
)

// summarise prints the one-line human-readable summary the deleting commands
// and awb demo give. It is the one output with no guaranteed format; a script
// uses --json, which returns the object.
func (e *env) summarise(format string, args ...any) error {
	_, _ = fmt.Fprintf(e.stdout, format, args...)
	return e.stdout.Err()
}

func newProjectCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
		Long:  "A project is the top-level organising unit; every issue belongs to exactly one.",
	}
	cmd.AddCommand(
		newProjectCreateCommand(e),
		newProjectUpdateCommand(e),
		newProjectShowCommand(e),
		newProjectListCommand(e),
		newProjectDeleteCommand(e),
	)
	return grouping(cmd)
}

func newProjectCreateCommand(e *env) *cobra.Command {
	var (
		desc descriptionFlags
		name string
	)

	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a project",
		Long: "Create a project.\n\n" +
			"The key is lowercase ASCII letters, digits and hyphens, starting with a\n" +
			"letter, at most 16 characters. It becomes the issue ID prefix and is\n" +
			"immutable. --name defaults to the key.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := backend.ProjectCreate{Key: args[0], Name: name}
			if description, err := desc.value(e, cmd); err != nil {
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
	}

	cmd.Flags().StringVar(&name, "name", "", "human-readable name; defaults to the key")
	desc.register(cmd, "project")
	return cmd
}

func newProjectUpdateCommand(e *env) *cobra.Command {
	var (
		desc descriptionFlags
		name string
	)

	cmd := &cobra.Command{
		Use:   "update <key>",
		Short: "Change a project's name or description",
		Long: "Change a project's name or description. The key itself is immutable.\n\n" +
			"--name \"\" restores the key as the name.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var patch backend.ProjectPatch
			if cmd.Flags().Changed("name") {
				patch.Name = &name
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
			project, err := be.UpdateProject(cmd.Context(), args[0], patch, "")
			if err != nil {
				return err
			}
			return e.mutatedProject(project)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "human-readable name; \"\" restores the key")
	desc.register(cmd, "project")
	return cmd
}

func newProjectShowCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "show <key>",
		Short: "Print one project in full",
		Long: "Print a project with its description and its count of issues that are not\n" +
			"closed.\n\n" +
			"On a terminal the description is drawn as the Markdown it is. Under\n" +
			"--compact this prints the same single line project list would and nothing\n" +
			"else; --json is what a script uses when it needs the rest.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			project, err := be.GetProject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return e.printProject(project)
		},
	}
}

func newProjectListCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects with counts of issues that are not closed",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListProjects(cmd.Context(), nil, nil)
			if err != nil {
				return err
			}
			return e.printProjects(page.Projects)
		},
	}
}

func newProjectDeleteCommand(e *env) *cobra.Command {
	var (
		force   bool
		cascade bool
	)

	cmd := &cobra.Command{
		Use:   "delete <key> --force",
		Short: "Delete a project",
		Long: "Delete a project.\n\n" +
			"It refuses while the project holds any issue at all — closed ones included,\n" +
			"so the refusal is wider than the count project list shows and --force alone\n" +
			"can never destroy closed history — unless --cascade is also given, which\n" +
			"deletes those issues and their relations, including relations to issues in\n" +
			"other projects, which may unblock work elsewhere.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !force {
				return awberr.Usagef("awb project delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteProject(cmd.Context(), args[0], cascade, "")
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(&deleted.Project)
			}
			if cascade {
				return e.summarise("Deleted project %s and the issues it held.\n",
					deleted.Project.Key)
			}
			return e.summarise("Deleted project %s.\n", deleted.Project.Key)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "confirm the deletion")
	cmd.Flags().BoolVar(&cascade, "cascade", false, "also delete the issues the project holds")
	return cmd
}
