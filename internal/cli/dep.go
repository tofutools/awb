package cli

import (
	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// RelationFlags are the four mutually exclusive relation flags dep add and dep
// rm share. Exactly one per invocation; giving two, or none, is a usage error.
type RelationFlags struct {
	BlockedBy      *string `long:"blocked-by" help:"the first issue cannot start until this one is closed"`
	HasParent      *string `long:"has-parent" help:"the second issue is the parent of the first"`
	DiscoveredFrom *string `long:"discovered-from" help:"the first issue was found while working on this one"`
	Related        *string `long:"related" help:"loose, symmetric association"`
}

// resolve returns the single relation the invocation names.
func (r *RelationFlags) resolve() (domain.RelationType, string, error) {
	type choice struct {
		typ   domain.RelationType
		value *string
	}
	choices := []choice{
		{domain.RelBlockedBy, r.BlockedBy},
		{domain.RelHasParent, r.HasParent},
		{domain.RelDiscoveredFrom, r.DiscoveredFrom},
		{domain.RelRelated, r.Related},
	}

	var found []choice
	for _, c := range choices {
		if c.value != nil {
			found = append(found, c)
		}
	}

	switch len(found) {
	case 1:
		return found[0].typ, *found[0].value, nil
	case 0:
		return "", "", awberr.Usagef(
			"give exactly one of --blocked-by, --has-parent, --discovered-from or --related")
	default:
		return "", "", awberr.Usagef(
			"give exactly one relation flag, not %d", len(found))
	}
}

func newDepCommand(e *env) *cobra.Command {
	return group("dep", "Manage relations between issues",
		"Every relation reads \"first id — relation — second id\", the single\n"+
			"convention of the whole tool. dep rm takes the same flag and the same two\n"+
			"ids in the same order as dep add, so removing a relation is literally the\n"+
			"add command with rm substituted.",
		newDepAddCommand(e), newDepRemoveCommand(e), newDepTreeCommand(e))
}

type depAddParams struct {
	RelationFlags
	ID    string `positional:"true" required:"true"`
	Force bool   `long:"force" optional:"true" help:"replace an existing parent"`
}

func newDepAddCommand(e *env) *cobra.Command {
	return boa.CmdT[depAddParams]{
		Use:   "add",
		Short: "Record a relation between two issues",
		Long: "Record a relation, read with the first issue as the subject.\n\n" +
			"An issue has at most one parent, so --has-parent on an issue that already\n" +
			"has a different one fails unless --force is given, which replaces it.\n" +
			"Naming the parent it already has succeeds and changes nothing.\n\n" +
			"Adding a relation that already exists succeeds and changes nothing.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *depAddParams, cmd *cobra.Command, _ []string) error {
			relType, other, err := p.resolve()
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			issue, err := be.AddRelation(cmd.Context(), p.ID,
				backend.RelationRequest{Type: relType, Other: other, Force: p.Force}, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}.ToCobra()
}

type depRemoveParams struct {
	RelationFlags
	ID string `positional:"true" required:"true"`
}

func newDepRemoveCommand(e *env) *cobra.Command {
	return boa.CmdT[depRemoveParams]{
		Use:   "rm",
		Short: "Remove a relation between two issues",
		Long: "Remove a relation, taking the same one relation flag as dep add.\n\n" +
			"Removing one that does not exist succeeds and changes nothing.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *depRemoveParams, cmd *cobra.Command, _ []string) error {
			relType, other, err := p.resolve()
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			// Under --json this prints the resulting issue — the one named first.
			issue, err := be.RemoveRelation(cmd.Context(), p.ID, relType, other, "")
			if err != nil {
				return err
			}
			return e.mutated(issue)
		},
	}.ToCobra()
}

func newDepTreeCommand(e *env) *cobra.Command {
	return idCommand("tree", "Print the subtree of children rooted at an issue",
		"Print the decomposition below an issue, to its full depth, following\n"+
			"children across project boundaries. It does not show ancestors.\n\n"+
			"It shows the whole subtree, closed children included and marked as such,\n"+
			"and accepts none of the listing filters — a tree with holes in it would\n"+
			"misrepresent the decomposition. Directory context does not apply either,\n"+
			"and --sort is not accepted, so the tree is reproducible like every other\n"+
			"output.", func(cmd *cobra.Command, id string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			tree, err := be.Tree(cmd.Context(), id)
			if err != nil {
				return err
			}
			return e.printTree(tree)
		})
}
