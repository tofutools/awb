package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// CreateIssue creates an issue with its labels and relations in one transaction
// (SPEC §4.3).
func (b *Backend) CreateIssue(ctx context.Context, req backend.IssueCreate) (*domain.Issue, error) {
	issue := &domain.Issue{
		Project:  req.Project,
		Type:     domain.DefaultType,
		Status:   domain.DefaultStatus,
		Priority: domain.DefaultPriority,
	}

	var err error
	if issue.Project, err = domain.ValidateProjectKey(req.Project); err != nil {
		return nil, err
	}
	if issue.Title, err = domain.ValidateTitle(req.Title); err != nil {
		return nil, err
	}
	if issue.Description, err = domain.ValidateDescription(req.Description); err != nil {
		return nil, err
	}
	if req.Type != "" {
		if issue.Type, err = domain.ParseType(string(req.Type)); err != nil {
			return nil, err
		}
	}
	if req.Priority != nil {
		if issue.Priority, err = domain.ParsePriority(*req.Priority); err != nil {
			return nil, err
		}
	}

	// Creating with an assignee is an atomic create-and-claim: the assignee
	// also sets status to in_progress, so a new issue is never open and
	// assigned at once (SPEC §2.2, §4.3).
	if req.Assignee != "" {
		if issue.Assignee, err = domain.ValidateAssignee(req.Assignee); err != nil {
			return nil, err
		}
		issue.Status = domain.StatusInProgress
	}

	labels, err := validateLabels(req.Labels)
	if err != nil {
		return nil, err
	}

	relations, err := validateNewRelations(req.Relations)
	if err != nil {
		return nil, err
	}

	err = b.write(ctx, func(tx *storage.Tx) error {
		exists, err := tx.ProjectExists(issue.Project)
		if err != nil {
			return err
		}
		if !exists {
			return awberr.NotFoundf("no such project: %s", issue.Project)
		}

		if err := tx.InsertIssue(issue); err != nil {
			return err
		}
		for _, label := range labels {
			if err := tx.AddLabel(issue, label); err != nil {
				return err
			}
		}
		for _, rel := range relations {
			other, err := resolve(tx, rel.Other)
			if err != nil {
				return err
			}
			if err := addRelation(tx, issue.ID, rel.Type, other, false); err != nil {
				return err
			}
		}

		issue, err = tx.GetIssue(issue.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return issue, nil
}

func validateLabels(labels []string) ([]string, error) {
	validated := make([]string, 0, len(labels))
	for _, label := range labels {
		valid, err := domain.ValidateLabel(label)
		if err != nil {
			return nil, err
		}
		validated = append(validated, valid)
	}
	return validated, nil
}

// validateNewRelations checks the relation vocabulary and the one-parent rule
// that applies before anything is stored: an issue has at most one parent, and
// awb create takes one --has-parent (SPEC §6.4).
func validateNewRelations(relations []backend.NewRelation) ([]backend.NewRelation, error) {
	parents := 0
	for _, rel := range relations {
		if _, err := domain.ParseRelationType(string(rel.Type)); err != nil {
			return nil, err
		}
		if rel.Type == domain.RelHasParent {
			parents++
			if parents > 1 {
				return nil, awberr.Usagef("an issue has at most one parent")
			}
		}
	}
	return relations, nil
}

// GetIssue reads one issue.
func (b *Backend) GetIssue(ctx context.Context, ref string) (*domain.Issue, error) {
	return b.readIssue(ctx, ref)
}

// ListIssues runs a listing, which is also how ready, blocked and search are
// served: they are the same query with the filter fixed differently.
func (b *Backend) ListIssues(ctx context.Context, filter *domain.Filter) (backend.IssuePage, error) {
	var page backend.IssuePage
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		if err := checkFilterProjects(tx, filter); err != nil {
			return err
		}
		if err := resolveFilterParent(tx, filter); err != nil {
			return err
		}
		var err error
		page.Issues, page.Total, err = tx.ListIssues(filter)
		return err
	})
	if err != nil {
		return backend.IssuePage{}, err
	}
	if page.Issues == nil {
		page.Issues = []domain.Issue{}
	}
	return page, nil
}

// checkFilterProjects reports a filter naming a project that is not there.
// SPEC §4.1: addressing a single entity that does not exist exits 3 even when
// it appears as a listing's --project, because a filter naming something that
// is not there is a mistake to report, not a listing that happens to match
// nothing.
func checkFilterProjects(tx *storage.Tx, filter *domain.Filter) error {
	for _, key := range filter.Projects {
		exists, err := tx.ProjectExists(key)
		if err != nil {
			return err
		}
		if !exists {
			return awberr.NotFoundf("no such project: %s", key)
		}
	}
	return nil
}

// resolveFilterParent turns --parent's reference into a stored ID, reporting
// one that names no issue for the same reason.
func resolveFilterParent(tx *storage.Tx, filter *domain.Filter) error {
	if filter.Parent == "" {
		return nil
	}
	id, err := resolve(tx, filter.Parent)
	if err != nil {
		return err
	}
	filter.Parent = id
	return nil
}

// UpdateIssue changes the fields awb update may change. Giving no field at all
// succeeds and changes nothing, exactly as an empty PATCH does (SPEC §4.3).
func (b *Backend) UpdateIssue(ctx context.Context, ref string, req backend.IssuePatch,
	ifMatch string) (*domain.Issue, error) {
	return b.mutate(ctx, ref, ifMatch, func(tx *storage.Tx, issue *domain.Issue) error {
		fields := storage.Fields(issue)

		if req.Title != nil {
			title, err := domain.ValidateTitle(*req.Title)
			if err != nil {
				return err
			}
			fields.Title = title
		}
		if req.Description != nil {
			description, err := domain.ValidateDescription(*req.Description)
			if err != nil {
				return err
			}
			fields.Description = description
		}
		if req.Type != nil {
			issueType, err := domain.ParseType(string(*req.Type))
			if err != nil {
				return err
			}
			fields.Type = issueType
		}
		if req.Priority != nil {
			priority, err := domain.ParsePriority(*req.Priority)
			if err != nil {
				return err
			}
			fields.Priority = priority
		}

		return tx.UpdateIssue(issue, fields)
	})
}

// DeleteIssue hard deletes an issue and its relations. It never refuses on
// account of dependents: it orphans any children and drops every relation,
// reporting how many went, since removing a blocker silently makes other issues
// ready (SPEC §4.3).
func (b *Backend) DeleteIssue(ctx context.Context, ref, ifMatch string) (*backend.DeletedIssue, error) {
	var deleted backend.DeletedIssue
	err := b.write(ctx, func(tx *storage.Tx) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, issue.UpdatedAt, "the issue"); err != nil {
			return err
		}

		// The object returned is the issue as it was immediately before
		// deletion, which for an issue includes the relations that went with it
		// (SPEC §4.1).
		deleted.Issue = *issue
		deleted.RelationsRemoved, err = tx.DeleteIssue(issue.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &deleted, nil
}

// AddLabel adds one label. Adding one the issue already carries succeeds and
// changes nothing (SPEC §4.3).
func (b *Backend) AddLabel(ctx context.Context, ref, label, ifMatch string) (*domain.Issue, error) {
	valid, err := domain.ValidateLabel(label)
	if err != nil {
		return nil, err
	}
	return b.mutate(ctx, ref, ifMatch, func(tx *storage.Tx, issue *domain.Issue) error {
		return tx.AddLabel(issue, valid)
	})
}

// RemoveLabel removes one label. Removing one it does not carry succeeds and
// changes nothing.
func (b *Backend) RemoveLabel(ctx context.Context, ref, label, ifMatch string) (*domain.Issue, error) {
	valid, err := domain.ValidateLabel(label)
	if err != nil {
		return nil, err
	}
	return b.mutate(ctx, ref, ifMatch, func(tx *storage.Tx, issue *domain.Issue) error {
		return tx.RemoveLabel(issue, valid)
	})
}

// Tree returns the subtree of children rooted at an issue (SPEC §4.4).
func (b *Backend) Tree(ctx context.Context, ref string) (*domain.IssueTree, error) {
	var tree *domain.IssueTree
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		id, err := resolve(tx, ref)
		if err != nil {
			return err
		}
		tree, err = tx.Tree(id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

// LabelFacets lists the labels in use under a filter (SPEC §6.2).
func (b *Backend) LabelFacets(ctx context.Context, filter *domain.Filter) (backend.FacetPage, error) {
	return b.facets(ctx, filter, (*storage.Tx).LabelFacets)
}

// AssigneeFacets lists the assignees in use under a filter.
func (b *Backend) AssigneeFacets(ctx context.Context, filter *domain.Filter) (backend.FacetPage, error) {
	return b.facets(ctx, filter, (*storage.Tx).AssigneeFacets)
}

func (b *Backend) facets(ctx context.Context, filter *domain.Filter,
	query func(*storage.Tx, *domain.Filter) ([]domain.Facet, error)) (backend.FacetPage, error) {
	var page backend.FacetPage
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		if err := checkFilterProjects(tx, filter); err != nil {
			return err
		}
		if err := resolveFilterParent(tx, filter); err != nil {
			return err
		}
		facets, err := query(tx, filter)
		if err != nil {
			return err
		}
		// limit and offset page the facet rows, not the issues behind them, so
		// count is the same whatever page it appears on (SPEC §6.2).
		page.Facets, page.Total = storage.PageFacets(facets, filter.Limit, filter.Offset)
		return nil
	})
	if err != nil {
		return backend.FacetPage{}, err
	}
	return page, nil
}

// mutate is the shape every single-issue mutation shares: resolve, check the
// precondition, apply, and re-read so the returned object carries the derived
// fields as they are after the change.
func (b *Backend) mutate(ctx context.Context, ref, ifMatch string,
	apply func(*storage.Tx, *domain.Issue) error) (*domain.Issue, error) {
	var result *domain.Issue
	err := b.write(ctx, func(tx *storage.Tx) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, issue.UpdatedAt, "the issue"); err != nil {
			return err
		}
		if err := apply(tx, issue); err != nil {
			return err
		}
		result, err = tx.GetIssue(issue.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
