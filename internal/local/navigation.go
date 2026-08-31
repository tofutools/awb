package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// SearchNavigation returns small, independently capped sets of visible records
// for interactive navigation. All three reads share one authorized snapshot.
func (b *Backend) SearchNavigation(ctx context.Context, query string, limit int) (backend.NavigationResults, error) {
	query, err := domain.ValidateSearchTerm(query)
	if err != nil {
		return backend.NavigationResults{}, err
	}
	if limit < 1 || limit > 20 {
		return backend.NavigationResults{}, awberr.Usagef("navigation result limit must be between 1 and 20")
	}
	var results backend.NavigationResults
	err = b.read(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if results.Issues, err = tx.SearchIssuesForNavigation(query, limit); err != nil {
			return err
		}
		if results.Projects, err = tx.SearchProjectsForNavigation(query, limit); err != nil {
			return err
		}
		if caller.MayManageUsers() {
			results.Users, err = tx.SearchUsersForNavigation(query, limit)
		} else {
			results.Users, err = tx.SearchVisibleUsersForNavigation(caller.Name, query, limit)
		}
		return err
	})
	return results, err
}
