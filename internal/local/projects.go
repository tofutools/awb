package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// CreateProject creates a project. The name defaults to the key and is never
// empty.
func (b *Backend) CreateProject(ctx context.Context, req backend.ProjectCreate) (*domain.Project, error) {
	key, err := domain.ValidateProjectKey(req.Key)
	if err != nil {
		return nil, err
	}
	name, err := domain.ValidateProjectName(req.Name)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = key
	}
	description, err := domain.ValidateDescription(req.Description)
	if err != nil {
		return nil, err
	}

	var project *domain.Project
	err = b.write(ctx, func(tx *storage.Tx) error {
		if err := tx.InsertProject(key, name, description); err != nil {
			return err
		}
		project, err = tx.GetProject(key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return project, nil
}

// GetProject reads one project.
func (b *Backend) GetProject(ctx context.Context, key string) (*domain.Project, error) {
	if _, err := domain.ValidateProjectKey(key); err != nil {
		return nil, err
	}
	var project *domain.Project
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		var err error
		project, err = tx.GetProject(key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return project, nil
}

// ListProjects lists projects ordered by key ascending, with counts of issues
// that are not closed.
func (b *Backend) ListProjects(ctx context.Context, limit, offset *int) (backend.ProjectPage, error) {
	var page backend.ProjectPage
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		var err error
		page.Projects, page.Total, err = tx.ListProjects(limit, offset)
		return err
	})
	if err != nil {
		return backend.ProjectPage{}, err
	}
	if page.Projects == nil {
		page.Projects = []domain.Project{}
	}
	return page, nil
}

// UpdateProject changes a project's name or description. The key itself is
// immutable. An empty name restores the key as the name.
func (b *Backend) UpdateProject(ctx context.Context, key string, req backend.ProjectPatch,
	ifMatch string) (*domain.Project, error) {
	if _, err := domain.ValidateProjectKey(key); err != nil {
		return nil, err
	}

	var project *domain.Project
	err := b.write(ctx, func(tx *storage.Tx) error {
		existing, err := tx.GetProject(key)
		if err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, existing.UpdatedAt, "the project"); err != nil {
			return err
		}

		name := existing.Name
		if req.Name != nil {
			if name, err = domain.ValidateProjectName(*req.Name); err != nil {
				return err
			}
			if name == "" {
				name = key
			}
		}
		description := existing.Description
		if req.Description != nil {
			if description, err = domain.ValidateDescription(*req.Description); err != nil {
				return err
			}
		}

		if err := tx.UpdateProject(existing, name, description); err != nil {
			return err
		}
		project, err = tx.GetProject(key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return project, nil
}

// DeleteProject deletes a project.
//
// It refuses while the project holds any issue at all — closed ones included,
// so the refusal is wider than the count project list shows and confirmation
// alone can never destroy closed history — unless cascade is given, which
// deletes those issues and their relations, including relations to issues in
// other projects, which may unblock work elsewhere.
func (b *Backend) DeleteProject(ctx context.Context, key string, cascade bool,
	ifMatch string) (*backend.DeletedProject, error) {
	if _, err := domain.ValidateProjectKey(key); err != nil {
		return nil, err
	}

	var (
		deleted backend.DeletedProject
		digests []string
	)
	err := b.write(ctx, func(tx *storage.Tx) error {
		project, err := tx.GetProject(key)
		if err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, project.UpdatedAt, "the project"); err != nil {
			return err
		}

		held, err := tx.CountIssuesInProject(key)
		if err != nil {
			return err
		}
		if held > 0 && !cascade {
			return awberr.Conflictf(
				"project %s still holds %d issue(s), closed ones included; use --cascade to delete them too",
				key, held)
		}

		deleted.Project = *project
		if cascade {
			// Read before the rows go: the cascade takes the attachment rows with
			// the issues, and there would be nothing left to ask afterwards.
			if digests, err = tx.DigestsOfProject(key); err != nil {
				return err
			}
			if _, err := tx.DeleteProjectIssues(key); err != nil {
				return err
			}
		}
		return tx.DeleteProject(key)
	})
	if err != nil {
		return nil, err
	}
	// Once the rows are gone, and never before: see sweep.
	b.sweep(ctx, digests)
	return &deleted, nil
}
