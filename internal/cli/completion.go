package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

const completionTimeout = 2 * time.Second

// prepareCompletion carries the persistent flags Cobra has already parsed
// into the normal configuration path. Root parameter validation does not run
// for Cobra's hidden completion command, while a child parameter's dynamic
// alternatives function does.
func (e *env) prepareCompletion(cmd *cobra.Command) {
	if e.cfg != nil {
		return
	}
	if flag := cmd.Flag("db"); flag != nil && flag.Changed {
		value := flag.Value.String()
		e.flags.DB = &value
	}
	if flag := cmd.Flag("no-context"); flag != nil && flag.Changed {
		e.flags.NoContext = flag.Value.String() == "true"
	}
}

// queryCompletion runs an advisory backend lookup with a short deadline. A
// completion must never turn an unavailable server or invalid configuration
// into a shell error or a long pause.
func (e *env) queryCompletion(cmd *cobra.Command,
	query func(context.Context, backend.Backend) ([]string, error)) []string {
	e.prepareCompletion(cmd)
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	be, err := e.backend(ctx)
	if err != nil {
		return nil
	}
	values, err := query(ctx, be)
	if err != nil {
		return nil
	}
	return values
}

// completeProjects lists the project keys visible through the selected local
// or remote backend. A completion lookup is advisory: unavailable storage,
// credentials or a server produce no suggestions rather than a shell error.
func (e *env) completeProjects(cmd *cobra.Command, _ []string, _ string) []string {
	return e.queryCompletion(cmd, func(ctx context.Context, be backend.Backend) ([]string, error) {
		page, err := be.ListProjects(ctx, nil, nil)
		if err != nil {
			return nil, err
		}
		values := make([]string, len(page.Projects))
		for i, project := range page.Projects {
			values[i] = project.Key
		}
		return values, nil
	})
}

func facetValues(facets []domain.Facet) []string {
	values := make([]string, len(facets))
	for i, facet := range facets {
		values[i] = facet.Value
	}
	return values
}
