package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// ListWorkspacePreferences is the recovery path for ignored workspaces. It uses
// ordinary authorization but deliberately omits only the preference boundary.
func (b *Backend) ListWorkspacePreferences(ctx context.Context) ([]domain.WorkspacePreference, error) {
	if !b.userPreferences {
		return nil, awberr.NotFoundf("no such user: %s", b.identity)
	}
	preferences := []domain.WorkspacePreference{}
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		exists, err := tx.UserExists(caller.Name)
		if err != nil {
			return err
		}
		if !exists {
			return awberr.NotFoundf("no such user: %s", caller.Name)
		}
		workspaces, _, err := tx.ListWorkspacesByState("", domain.WorkspacesAll, domain.WorkspaceSort{}, nil, nil)
		if err != nil {
			return err
		}
		ignored, err := tx.IgnoredWorkspaces(caller.Name)
		if err != nil {
			return err
		}
		for _, workspace := range workspaces {
			preferences = append(preferences, domain.WorkspacePreference{
				Workspace: workspace,
				Ignored:   ignored[workspace.Key],
			})
		}
		return nil
	})
	return preferences, err
}

// SetWorkspaceIgnored changes the current user's preference after resolving the
// workspace through authorization without the ignore boundary. That is what
// keeps re-enabling possible without making inaccessible workspaces visible.
func (b *Backend) SetWorkspaceIgnored(ctx context.Context, key string, ignored bool) (*domain.WorkspacePreference, error) {
	key, err := domain.ValidateWorkspaceKey(key)
	if err != nil {
		return nil, err
	}
	if !b.userPreferences {
		return nil, awberr.NotFoundf("no such user: %s", b.identity)
	}
	var preference domain.WorkspacePreference
	err = b.db.Write(ctx, func(tx *storage.Tx) error {
		caller, err := b.authorize(tx, true)
		if err != nil {
			return err
		}
		exists, err := tx.UserExists(caller.Name)
		if err != nil {
			return err
		}
		if !exists {
			return awberr.NotFoundf("no such user: %s", caller.Name)
		}
		workspace, err := tx.GetWorkspace(key)
		if err != nil {
			return err
		}
		if err := tx.SetWorkspaceIgnored(caller.Name, key, ignored); err != nil {
			return err
		}
		preference = domain.WorkspacePreference{Workspace: *workspace, Ignored: ignored}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &preference, nil
}
