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
	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		// A project's existence is a project administrator's to decide. There is
		// nothing to hide behind a 404 here: the caller named a key that is not
		// there yet either way.
		if !caller.MayManageProjects() {
			return awberr.Forbiddenf("only a project administrator may create a project")
		}
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
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
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
func (b *Backend) ListProjects(ctx context.Context, filter string, sort domain.ProjectSort,
	limit, offset *int) (backend.ProjectPage, error) {
	return b.ListProjectsByState(ctx, filter, domain.ProjectsActive, sort, limit, offset)
}

func (b *Backend) ListProjectsByState(ctx context.Context, filter string, state domain.ProjectStateFilter, sort domain.ProjectSort,
	limit, offset *int) (backend.ProjectPage, error) {
	var page backend.ProjectPage
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		var err error
		page.Projects, page.Total, err = tx.ListProjectsByState(filter, state, sort, limit, offset)
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
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		// Read first, so a project the caller cannot see is not found rather
		// than refused: the refusal below is for one they can see and may not
		// change.
		existing, err := tx.GetProject(key)
		if err != nil {
			return err
		}
		if !caller.MayManageProjects() {
			return awberr.Forbiddenf("only a project administrator may change project %s", key)
		}
		if existing.State == domain.ProjectArchived {
			return awberr.Conflictf("project %s is archived; restore it before changing it", key)
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

func (b *Backend) setProjectState(ctx context.Context, key string, state domain.ProjectState,
	ifMatch string) (*domain.Project, error) {
	if _, err := domain.ValidateProjectKey(key); err != nil {
		return nil, err
	}
	var project *domain.Project
	err := b.writeIncludingIgnored(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		existing, err := tx.GetProject(key)
		if err != nil {
			return err
		}
		if !caller.MayManageProjects() {
			return awberr.Forbiddenf("only a project administrator may change project %s lifecycle", key)
		}
		if err := checkIfMatch(ifMatch, existing.UpdatedAt, "the project"); err != nil {
			return err
		}
		if _, err := tx.SetProjectState(existing, state, caller.Name); err != nil {
			return err
		}
		project, err = tx.GetProject(key)
		return err
	})
	return project, err
}

func (b *Backend) ArchiveProject(ctx context.Context, key, ifMatch string) (*domain.Project, error) {
	return b.setProjectState(ctx, key, domain.ProjectArchived, ifMatch)
}

func (b *Backend) RestoreProject(ctx context.Context, key, ifMatch string) (*domain.Project, error) {
	return b.setProjectState(ctx, key, domain.ProjectActive, ifMatch)
}

func (b *Backend) ListProjectActivity(ctx context.Context, key string, limit, offset *int) (backend.ProjectActivityPage, error) {
	if _, err := domain.ValidateProjectKey(key); err != nil {
		return backend.ProjectActivityPage{}, err
	}
	var page backend.ProjectActivityPage
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		if _, err := tx.GetProject(key); err != nil {
			return err
		}
		var err error
		page.Activity, page.Total, err = tx.ListProjectActivity(key, limit, offset)
		return err
	})
	return page, err
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
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		project, err := tx.GetProject(key)
		if err != nil {
			return err
		}
		if !caller.MayManageProjects() {
			return awberr.Forbiddenf("only a project administrator may delete project %s", key)
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
