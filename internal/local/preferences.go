package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// ListProjectPreferences is the recovery path for ignored projects. It uses
// ordinary authorization but deliberately omits only the preference boundary.
func (b *Backend) ListProjectPreferences(ctx context.Context) ([]domain.ProjectPreference, error) {
	preferences := []domain.ProjectPreference{}
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
		projects, _, err := tx.ListProjects(domain.ProjectSort{}, nil, nil)
		if err != nil {
			return err
		}
		ignored, err := tx.IgnoredProjects(caller.Name)
		if err != nil {
			return err
		}
		for _, project := range projects {
			preferences = append(preferences, domain.ProjectPreference{
				Project: project,
				Ignored: ignored[project.Key],
			})
		}
		return nil
	})
	return preferences, err
}

// SetProjectIgnored changes the current user's preference after resolving the
// project through authorization without the ignore boundary. That is what
// keeps re-enabling possible without making inaccessible projects visible.
func (b *Backend) SetProjectIgnored(ctx context.Context, key string, ignored bool) (*domain.ProjectPreference, error) {
	key, err := domain.ValidateProjectKey(key)
	if err != nil {
		return nil, err
	}
	var preference domain.ProjectPreference
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
		project, err := tx.GetProject(key)
		if err != nil {
			return err
		}
		if err := tx.SetProjectIgnored(caller.Name, key, ignored); err != nil {
			return err
		}
		preference = domain.ProjectPreference{Project: *project, Ignored: ignored}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &preference, nil
}
