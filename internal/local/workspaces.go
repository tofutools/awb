package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// CreateWorkspace creates a workspace. The name defaults to the key and is never
// empty.
func (b *Backend) CreateWorkspace(ctx context.Context, req backend.WorkspaceCreate) (*domain.Workspace, error) {
	key, err := domain.ValidateWorkspaceKey(req.Key)
	if err != nil {
		return nil, err
	}
	name, err := domain.ValidateWorkspaceName(req.Name)
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

	var workspace *domain.Workspace
	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		// A workspace's existence is a workspace administrator's to decide. There is
		// nothing to hide behind a 404 here: the caller named a key that is not
		// there yet either way.
		if !caller.MayManageWorkspaces() {
			return awberr.Forbiddenf("only a workspace administrator may create a workspace")
		}
		if err := tx.InsertWorkspace(key, name, description); err != nil {
			return err
		}
		workspace, err = tx.GetWorkspace(key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return workspace, nil
}

// GetWorkspace reads one workspace.
func (b *Backend) GetWorkspace(ctx context.Context, key string) (*domain.Workspace, error) {
	if _, err := domain.ValidateWorkspaceKey(key); err != nil {
		return nil, err
	}
	var workspace *domain.Workspace
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		var err error
		workspace, err = tx.GetWorkspace(key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return workspace, nil
}

// ListWorkspaces lists workspaces ordered by key ascending, with counts of issues
// that are not closed.
func (b *Backend) ListWorkspaces(ctx context.Context, filter string, sort domain.WorkspaceSort,
	limit, offset *int) (backend.WorkspacePage, error) {
	return b.ListWorkspacesByState(ctx, filter, domain.WorkspacesActive, sort, limit, offset)
}

func (b *Backend) ListWorkspacesByState(ctx context.Context, filter string, state domain.WorkspaceStateFilter, sort domain.WorkspaceSort,
	limit, offset *int) (backend.WorkspacePage, error) {
	var page backend.WorkspacePage
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		var err error
		page.Workspaces, page.Total, err = tx.ListWorkspacesByState(filter, state, sort, limit, offset)
		return err
	})
	if err != nil {
		return backend.WorkspacePage{}, err
	}
	if page.Workspaces == nil {
		page.Workspaces = []domain.Workspace{}
	}
	return page, nil
}

// UpdateWorkspace changes a workspace's name or description. The key itself is
// immutable. An empty name restores the key as the name.
func (b *Backend) UpdateWorkspace(ctx context.Context, key string, req backend.WorkspacePatch,
	ifMatch string) (*domain.Workspace, error) {
	if _, err := domain.ValidateWorkspaceKey(key); err != nil {
		return nil, err
	}

	var workspace *domain.Workspace
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		// Read first, so a workspace the caller cannot see is not found rather
		// than refused: the refusal below is for one they can see and may not
		// change.
		existing, err := tx.GetWorkspace(key)
		if err != nil {
			return err
		}
		if !caller.MayManageWorkspaces() {
			return awberr.Forbiddenf("only a workspace administrator may change workspace %s", key)
		}
		if existing.State == domain.WorkspaceArchived {
			return awberr.Conflictf("workspace %s is archived; restore it before changing it", key)
		}
		if err := checkIfMatch(ifMatch, existing.UpdatedAt, "the workspace"); err != nil {
			return err
		}

		name := existing.Name
		if req.Name != nil {
			if name, err = domain.ValidateWorkspaceName(*req.Name); err != nil {
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

		if err := tx.UpdateWorkspace(existing, name, description); err != nil {
			return err
		}
		workspace, err = tx.GetWorkspace(key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return workspace, nil
}

func (b *Backend) setWorkspaceState(ctx context.Context, key string, state domain.WorkspaceState,
	ifMatch string) (*domain.Workspace, error) {
	if _, err := domain.ValidateWorkspaceKey(key); err != nil {
		return nil, err
	}
	var workspace *domain.Workspace
	err := b.writeIncludingIgnored(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		existing, err := tx.GetWorkspace(key)
		if err != nil {
			return err
		}
		if !caller.MayManageWorkspaces() {
			return awberr.Forbiddenf("only a workspace administrator may change workspace %s lifecycle", key)
		}
		if err := checkIfMatch(ifMatch, existing.UpdatedAt, "the workspace"); err != nil {
			return err
		}
		if _, err := tx.SetWorkspaceState(existing, state, caller.Name); err != nil {
			return err
		}
		workspace, err = tx.GetWorkspace(key)
		return err
	})
	return workspace, err
}

func (b *Backend) ArchiveWorkspace(ctx context.Context, key, ifMatch string) (*domain.Workspace, error) {
	return b.setWorkspaceState(ctx, key, domain.WorkspaceArchived, ifMatch)
}

func (b *Backend) RestoreWorkspace(ctx context.Context, key, ifMatch string) (*domain.Workspace, error) {
	return b.setWorkspaceState(ctx, key, domain.WorkspaceActive, ifMatch)
}

func (b *Backend) ListWorkspaceActivity(ctx context.Context, key string, limit, offset *int) (backend.WorkspaceActivityPage, error) {
	if _, err := domain.ValidateWorkspaceKey(key); err != nil {
		return backend.WorkspaceActivityPage{}, err
	}
	var page backend.WorkspaceActivityPage
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		if _, err := tx.GetWorkspace(key); err != nil {
			return err
		}
		var err error
		page.Activity, page.Total, err = tx.ListWorkspaceActivity(key, limit, offset)
		return err
	})
	return page, err
}

// DeleteWorkspace deletes a workspace.
//
// It refuses while the workspace holds any issue at all — closed ones included,
// so the refusal is wider than the count workspace list shows and confirmation
// alone can never destroy closed history — unless cascade is given, which
// deletes those issues and their relations, including relations to issues in
// other workspaces, which may unblock work elsewhere.
func (b *Backend) DeleteWorkspace(ctx context.Context, key string, cascade bool,
	ifMatch string) (*backend.DeletedWorkspace, error) {
	if _, err := domain.ValidateWorkspaceKey(key); err != nil {
		return nil, err
	}

	var (
		deleted backend.DeletedWorkspace
		digests []string
	)
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		workspace, err := tx.GetWorkspace(key)
		if err != nil {
			return err
		}
		if !caller.MayManageWorkspaces() {
			return awberr.Forbiddenf("only a workspace administrator may delete workspace %s", key)
		}
		if workspace.State == domain.WorkspaceArchived {
			return awberr.Conflictf("workspace %s is archived and read-only; restore it before deleting it", key)
		}
		if err := checkIfMatch(ifMatch, workspace.UpdatedAt, "the workspace"); err != nil {
			return err
		}

		held, err := tx.CountIssuesInWorkspace(key)
		if err != nil {
			return err
		}
		if held > 0 && !cascade {
			return awberr.Conflictf(
				"workspace %s still holds %d issue(s), closed ones included; use --cascade to delete them too",
				key, held)
		}
		if cascade {
			touchesArchived, err := tx.WorkspaceRelationsTouchArchived(key)
			if err != nil {
				return err
			}
			if touchesArchived {
				return awberr.Conflictf("workspace %s has relations touching archived read-only work", key)
			}
		}

		deleted.Workspace = *workspace
		if cascade {
			// Read before the rows go: the cascade takes the attachment rows with
			// the issues, and there would be nothing left to ask afterwards.
			if digests, err = tx.DigestsOfWorkspace(key); err != nil {
				return err
			}
			if _, err := tx.DeleteWorkspaceIssues(key); err != nil {
				return err
			}
		}
		return tx.DeleteWorkspace(key)
	})
	if err != nil {
		return nil, err
	}
	// Once the rows are gone, and never before: see sweep.
	b.sweep(ctx, digests)
	return &deleted, nil
}
