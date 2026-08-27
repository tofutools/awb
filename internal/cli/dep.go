package cli

import (
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// relationFlags are the four mutually exclusive relation flags dep add and
// dep rm share. Exactly one per invocation; giving two, or none, is a usage
// error (SPEC §4.4).
type relationFlags struct {
	blockedBy      string
	hasParent      string
	discoveredFrom string
	related        string
}

func (r *relationFlags) register(cmd *cobra.Command, verb string) {
	cmd.Flags().StringVar(&r.blockedBy, "blocked-by", "",
		"the first issue cannot start until this one is closed")
	cmd.Flags().StringVar(&r.hasParent, "has-parent", "",
		"the second issue is the parent of the first")
	cmd.Flags().StringVar(&r.discoveredFrom, "discovered-from", "",
		"the first issue was found while working on this one")
	cmd.Flags().StringVar(&r.related, "related", "", "loose, symmetric association")
	_ = verb
}

// resolve returns the single relation the invocation names.
func (r *relationFlags) resolve(cmd *cobra.Command) (domain.RelationType, string, error) {
	type choice struct {
		flag  string
		typ   domain.RelationType
		value string
	}
	choices := []choice{
		{"blocked-by", domain.RelBlockedBy, r.blockedBy},
		{"has-parent", domain.RelHasParent, r.hasParent},
		{"discovered-from", domain.RelDiscoveredFrom, r.discoveredFrom},
		{"related", domain.RelRelated, r.related},
	}

	var found []choice
	for _, c := range choices {
		if cmd.Flags().Changed(c.flag) {
			found = append(found, c)
		}
	}

	switch len(found) {
	case 1:
		return found[0].typ, found[0].value, nil
	case 0:
		return "", "", awberr.Usagef(
			"give exactly one of --blocked-by, --has-parent, --discovered-from or --related")
	default:
		return "", "", awberr.Usagef(
			"give exactly one relation flag, not %d", len(found))
	}
}

func newDepCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dep",
		Short: "Manage relations between issues",
		Long: "Every relation reads \"first id — relation — second id\", the single\n" +
			"convention of the whole tool. dep rm takes the same flag and the same two\n" +
			"ids in the same order as dep add, so removing a relation is literally the\n" +
			"add command with rm substituted.",
	}
	cmd.AddCommand(newDepAddCommand(e), newDepRemoveCommand(e), newDepTreeCommand(e))
	return cmd
}

func newDepAddCommand(e *env) *cobra.Command {
	var (
		rels  relationFlags
		force bool
	)

	cmd := &cobra.Command{
		Use:   "add <id> --blocked-by|--has-parent|--discovered-from|--related <id>",
		Short: "Record a relation between two issues",
		Long: "Record a relation, read with the first issue as the subject.\n\n" +
			"An issue has at most one parent, so --has-parent on an issue that already\n" +
			"has a different one fails unless --force is given, which replaces it.\n" +
			"Naming the parent it already has succeeds and changes nothing.\n\n" +
			"Adding a relation that already exists succeeds and changes nothing.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			relType, other, err := rels.resolve(cmd)
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.AddRelation(cmd.Context(), args[0],
				backend.RelationRequest{Type: relType, Other: other, Force: force}, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}

	rels.register(cmd, "add")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing parent")
	return cmd
}

func newDepRemoveCommand(e *env) *cobra.Command {
	var rels relationFlags

	cmd := &cobra.Command{
		Use:   "rm <id> --blocked-by|--has-parent|--discovered-from|--related <id>",
		Short: "Remove a relation between two issues",
		Long: "Remove a relation, taking the same one relation flag as dep add.\n\n" +
			"Removing one that does not exist succeeds and changes nothing.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			relType, other, err := rels.resolve(cmd)
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			// Under --json this prints the resulting issue — the one named
			// first (SPEC §4.1).
			issue, err := be.RemoveRelation(cmd.Context(), args[0], relType, other, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}

	rels.register(cmd, "rm")
	return cmd
}

func newDepTreeCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "tree <id>",
		Short: "Print the subtree of children rooted at an issue",
		Long: "Print the decomposition below an issue, to its full depth, following\n" +
			"children across project boundaries. It does not show ancestors.\n\n" +
			"It shows the whole subtree, closed children included and marked as such,\n" +
			"and accepts none of the listing filters — a tree with holes in it would\n" +
			"misrepresent the decomposition. Directory context does not apply either,\n" +
			"and --sort is not accepted, so the tree is reproducible like every other\n" +
			"output.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			tree, err := be.Tree(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return e.printTree(tree)
		},
	}
}
