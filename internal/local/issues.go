package local

import (
	"context"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// CreateIssue creates an issue with its labels and relations in one
// transaction.
func (b *Backend) CreateIssue(ctx context.Context, req backend.IssueCreate) (*domain.Issue, error) {
	issue := &domain.Issue{
		Workspace: req.Workspace,
		Type:      domain.DefaultType,
		Status:    domain.DefaultStatus,
		Priority:  domain.DefaultPriority,
	}

	var err error
	if issue.Workspace, err = domain.ValidateWorkspaceKey(req.Workspace); err != nil {
		return nil, err
	}
	if issue.Title, err = domain.ValidateTitle(req.Title); err != nil {
		return nil, err
	}
	if issue.Description, err = domain.ValidateDescription(req.Description); err != nil {
		return nil, err
	}
	if issue.CommitHash, err = domain.ValidateCommitHash(req.CommitHash); err != nil {
		return nil, err
	}
	if issue.PullRequestURL, err = domain.ValidatePullRequestURL(req.PullRequestURL); err != nil {
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

	// Creating with assignees is an atomic create-and-claim.
	if issue.Assignees, err = validateAssignees(req.Assignees); err != nil {
		return nil, err
	}
	if len(issue.Assignees) > 0 {
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

	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		workspace, err := tx.GetWorkspace(issue.Workspace)
		if err != nil {
			return err
		}
		if workspace.State == domain.WorkspaceArchived {
			return awberr.Conflictf("workspace %s is archived and cannot receive new issues", issue.Workspace)
		}
		// Create-and-claim is a claim, and holds to the same rule. There is no
		// --force here to override it: creating an issue for somebody who is not
		// a user is create-then-claim.
		if err := checkAssignees(tx, issue.Assignees...); err != nil {
			return err
		}

		if err := tx.InsertIssue(issue); err != nil {
			return err
		}
		for _, label := range labels {
			if err := tx.AddLabel(issue, label); err != nil {
				return err
			}
		}
		counterparts := relationSnapshots{}
		captureCounterpart := counterparts.capture(tx)
		for _, rel := range relations {
			other, err := resolve(tx, rel.Other)
			if err != nil {
				return err
			}
			if err := captureCounterpart(other); err != nil {
				return err
			}
			if err := addRelation(tx, issue.ID, rel.Type, other, false); err != nil {
				return err
			}
		}

		issue, err = tx.GetIssue(issue.ID)
		if err != nil {
			return err
		}
		if err := counterparts.record(tx, caller, "relation_added"); err != nil {
			return err
		}
		return recordChange(tx, caller, issue.ID, "created", nil)
	})
	if err != nil {
		return nil, err
	}
	return issue, nil
}

// movingAssignee is who a board move that starts work assigns it to: the
// caller, who has to be one of the tracker's users like any other assignee.
// The board has no --force, so an identity the directory does not know cannot
// start work from it; the command line can still claim for that name.
func movingAssignee(tx *storage.Tx, caller domain.Caller) (string, error) {
	assignee, err := domain.ValidateAssignee(caller.Name)
	if err != nil {
		return "", err
	}
	if err := checkAssignees(tx, assignee); err != nil {
		return "", err
	}
	return assignee, nil
}

func validateAssignees(assignees []string) ([]string, error) {
	validated := make([]string, 0, len(assignees))
	seen := make(map[string]bool, len(assignees))
	for _, assignee := range assignees {
		valid, err := domain.ValidateAssignee(assignee)
		if err != nil {
			return nil, err
		}
		if !seen[valid] {
			seen[valid] = true
			validated = append(validated, valid)
		}
	}
	return validated, nil
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
// awb create takes one --has-parent.
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
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		if err := checkFilterWorkspaces(tx, filter); err != nil {
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

// SuggestIssues finds visible issues by a literal ID or title fragment for
// reference editors. Closed issues are included because relations may name one.
func (b *Backend) SuggestIssues(ctx context.Context, query string, limit *int) (backend.IssuePage, error) {
	valid, err := domain.ValidateSearchTerm(query)
	if err != nil {
		return backend.IssuePage{}, err
	}
	var page backend.IssuePage
	err = b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		page.Issues, page.Total, err = tx.SuggestIssues(valid, limit)
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

// checkFilterWorkspaces reports a filter naming a workspace that is not there.
// Addressing a single entity that does not exist exits 3 even when it appears
// as a listing's --workspace, because a filter naming something that is not
// there is a mistake to report, not a listing that happens to match nothing.
func checkFilterWorkspaces(tx *storage.Tx, filter *domain.Filter) error {
	for _, key := range filter.Workspaces {
		exists, err := tx.WorkspaceExists(key)
		if err != nil {
			return err
		}
		if !exists {
			return awberr.NotFoundf("no such workspace: %s", key)
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
// succeeds and changes nothing, exactly as an empty PATCH does.
func (b *Backend) UpdateIssue(ctx context.Context, ref string, req backend.IssuePatch,
	ifMatch string) (*domain.Issue, error) {
	return b.mutate(ctx, ref, ifMatch, "updated", "", func(tx *storage.Tx, issue *domain.Issue) error {
		// Checked here, inside the write transaction, so a concurrent
		// transition cannot slip between the comparison and the write.
		if err := checkUnchanged(issue, req); err != nil {
			return err
		}

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
		if req.CommitHash != nil {
			commitHash, err := domain.ValidateCommitHash(*req.CommitHash)
			if err != nil {
				return err
			}
			fields.CommitHash = commitHash
		}
		if req.PullRequestURL != nil {
			pullRequestURL, err := domain.ValidatePullRequestURL(*req.PullRequestURL)
			if err != nil {
				return err
			}
			fields.PullRequestURL = pullRequestURL
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

// MoveIssue is the board/list drag operation. Status, optional same-workspace
// epic membership, and sparse position are committed together. Workspace and
// ID are immutable. Moving to Open clears assignments; moving to In progress
// assigns the caller, replacing a closed issue's historical assignees.
func (b *Backend) MoveIssue(ctx context.Context, ref string, req backend.IssueMove,
	ifMatch string) (*domain.Issue, error) {
	status, err := domain.ParseStatus(string(req.Status))
	if err != nil {
		return nil, err
	}
	positions := 0
	for _, set := range []bool{req.Before != "", req.After != "", req.Direction != ""} {
		if set {
			positions++
		}
	}
	if positions > 1 {
		return nil, awberr.Usagef("before, after and direction are mutually exclusive")
	}
	if req.Direction != "" && req.Direction != "earlier" && req.Direction != "later" {
		return nil, awberr.Usagef("direction must be earlier or later")
	}
	var result *domain.Issue
	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		if err := ensureIssueWritable(tx, issue); err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, issue.UpdatedAt, "the issue"); err != nil {
			return err
		}
		beforeID := ""
		afterID := ""
		if req.Before != "" {
			before, err := load(tx, req.Before)
			if err != nil {
				return err
			}
			if before.ID == issue.ID && issue.Status == status && req.Epic == nil {
				result = issue
				return nil
			}
			if before.Workspace != issue.Workspace {
				return awberr.Usagef("the order anchor must be in workspace %s", issue.Workspace)
			}
			beforeID = before.ID
		}
		if req.After != "" {
			after, err := load(tx, req.After)
			if err != nil {
				return err
			}
			if after.ID == issue.ID && issue.Status == status && req.Epic == nil {
				result = issue
				return nil
			}
			if after.Workspace != issue.Workspace {
				return awberr.Usagef("the order anchor must be in workspace %s", issue.Workspace)
			}
			afterID = after.ID
		}

		before := *issue
		before.Labels = slices.Clone(issue.Labels)
		before.Assignees = slices.Clone(issue.Assignees)
		before.Relations = slices.Clone(issue.Relations)
		originalEpic, err := tx.DirectEpic(issue.ID, issue.Workspace)
		if err != nil {
			return err
		}
		destinationEpic := originalEpic
		parentSnapshots := relationSnapshots{}
		captureParent := parentSnapshots.capture(tx)
		if req.Epic != nil {
			if *req.Epic == "" {
				if originalEpic != "" {
					if err := captureParent(originalEpic); err != nil {
						return err
					}
					if err := tx.DeleteRelation(issue.ID, domain.RelHasParent, originalEpic); err != nil {
						return err
					}
				}
				destinationEpic = ""
			} else {
				target, err := load(tx, *req.Epic)
				if err != nil {
					return err
				}
				if target.Workspace != issue.Workspace || target.Type != domain.TypeEpic {
					return awberr.Usagef("the epic must be an epic in workspace %s", issue.Workspace)
				}
				existingParent, hasParent, err := tx.ParentOf(issue.ID)
				if err != nil {
					return err
				}
				if hasParent && existingParent != target.ID && originalEpic == "" {
					return awberr.Conflictf("the issue has a non-epic parent; change that relation first")
				}
				if hasParent && existingParent != target.ID {
					if err := captureParent(existingParent); err != nil {
						return err
					}
				}
				if err := captureParent(target.ID); err != nil {
					return err
				}
				if err := addParent(tx, issue.ID, target.ID, true); err != nil {
					return err
				}
				destinationEpic = target.ID
			}
		}
		fields := storage.Fields(issue)
		switch {
		case status == issue.Status:
		case issue.Status == domain.StatusOpen && status == domain.StatusInProgress:
			if issue.Blocked {
				return awberr.Conflictf("%s is blocked by %v", issue.ID, issue.Blockers)
			}
			assignee, err := movingAssignee(tx, caller)
			if err != nil {
				return err
			}
			fields.Assignees = []string{assignee}
		case status == domain.StatusOpen:
			fields.Assignees = nil
		case status == domain.StatusClosed:
		case issue.Status == domain.StatusClosed && status == domain.StatusInProgress:
			assignee, err := movingAssignee(tx, caller)
			if err != nil {
				return err
			}
			fields.Assignees = []string{assignee}
		default:
			return awberr.Usagef("cannot move %s from %s to %s", issue.ID, issue.Status, status)
		}
		fields.Status = status
		if err := tx.UpdateIssue(issue, fields); err != nil {
			return err
		}
		var scopeStatus *domain.Status
		var scopeEpic *string
		if req.Epic != nil || status != before.Status {
			scopeStatus, scopeEpic = &status, &destinationEpic
		}
		orderChanges, err := tx.ReorderIssue(issue, beforeID, afterID, req.Direction, scopeStatus, scopeEpic)
		if err != nil {
			return err
		}
		result, err = tx.GetIssue(issue.ID)
		if err != nil {
			return err
		}
		changes := activityChanges(&before, result)
		if err := parentSnapshots.record(tx, caller, "moved"); err != nil {
			return err
		}
		for _, change := range orderChanges {
			if change.Issue == issue.ID {
				continue
			}
			if err := recordChange(tx, caller, change.Issue, "reordered", []domain.ActivityChange{{
				Field: "order", From: activityJSON(change.From), To: activityJSON(change.To),
			}}); err != nil {
				return err
			}
		}
		if len(changes) == 0 {
			return nil
		}
		return recordChange(tx, caller, issue.ID, "moved", changes)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// checkUnchanged enforces the "may appear but may not change" rule: a patch
// that genuinely tries to close an issue or rewrite its labels is refused
// rather than silently dropped.
func checkUnchanged(issue *domain.Issue, req backend.IssuePatch) error {
	if req.ExpectStatus != nil && *req.ExpectStatus != issue.Status {
		return awberr.Usagef(
			"status cannot be changed here: use claim, release, close or reopen")
	}
	if req.ExpectAssignees != nil && !slices.Equal(*req.ExpectAssignees, issue.Assignees) {
		return awberr.Usagef("assignees cannot be changed here: use claim or release")
	}
	if req.ExpectLabels != nil {
		// Compared as the sorted form, which is what a client read.
		sent := slices.Clone(*req.ExpectLabels)
		slices.Sort(sent)
		if !slices.Equal(sent, issue.Labels) {
			return awberr.Usagef("labels cannot be changed here: add and remove them one at a time")
		}
	}
	return nil
}

// DeleteIssue hard deletes an issue and its relations. It never refuses on
// account of dependents: it orphans any children and drops every relation,
// reporting how many went, since removing a blocker silently makes other
// issues ready.
func (b *Backend) DeleteIssue(ctx context.Context, ref, ifMatch string) (*backend.DeletedIssue, error) {
	var (
		deleted backend.DeletedIssue
		digests []string
	)
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		if err := ensureIssueWritable(tx, issue); err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, issue.UpdatedAt, "the issue"); err != nil {
			return err
		}

		// The digests are read before the rows go, since the cascade takes the
		// attachment rows with the issue and there would be nothing left to ask
		// afterwards.
		if digests, err = tx.DigestsOfIssue(issue.ID); err != nil {
			return err
		}

		// The object returned is the issue as it was immediately before deletion,
		// which for an issue includes the relations and the attachments that went
		// with it.
		deleted.Issue = *issue
		counterparts := relationSnapshots{}
		captureCounterpart := counterparts.capture(tx)
		for _, relation := range issue.Relations {
			if err := captureCounterpart(relation.Other); err != nil {
				return err
			}
		}
		deleted.RelationsRemoved, err = tx.DeleteIssue(issue.ID)
		if err != nil {
			return err
		}
		return counterparts.record(tx, caller, "relation_removed")
	})
	if err != nil {
		return nil, err
	}
	b.sweep(ctx, digests)
	return &deleted, nil
}

// AddLabel adds one label. Adding one the issue already carries succeeds and
// changes nothing.
func (b *Backend) AddLabel(ctx context.Context, ref, label, ifMatch string) (*domain.Issue, error) {
	valid, err := domain.ValidateLabel(label)
	if err != nil {
		return nil, err
	}
	return b.mutate(ctx, ref, ifMatch, "label_added", "", func(tx *storage.Tx, issue *domain.Issue) error {
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
	return b.mutate(ctx, ref, ifMatch, "label_removed", "", func(tx *storage.Tx, issue *domain.Issue) error {
		return tx.RemoveLabel(issue, valid)
	})
}

// Tree returns the subtree of children rooted at an issue.
func (b *Backend) Tree(ctx context.Context, ref string) (*domain.IssueTree, error) {
	var tree *domain.IssueTree
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
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

// LabelFacets lists the labels in use under a filter.
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
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		if err := checkFilterWorkspaces(tx, filter); err != nil {
			return err
		}
		if err := resolveFilterParent(tx, filter); err != nil {
			return err
		}
		facets, err := query(tx, filter)
		if err != nil {
			return err
		}
		// limit and offset page the facet rows, not the issues behind them, so count
		// is the same whatever page it appears on.
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
func (b *Backend) mutate(ctx context.Context, ref, ifMatch, action, activityBody string,
	apply func(*storage.Tx, *domain.Issue) error) (*domain.Issue, error) {
	var result *domain.Issue
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		if err := ensureIssueWritable(tx, issue); err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, issue.UpdatedAt, "the issue"); err != nil {
			return err
		}
		before := *issue
		before.Labels = slices.Clone(issue.Labels)
		before.Assignees = slices.Clone(issue.Assignees)
		before.Relations = slices.Clone(issue.Relations)
		if err := apply(tx, issue); err != nil {
			return err
		}
		result, err = tx.GetIssue(issue.ID)
		if err != nil {
			return err
		}
		changes := activityChanges(&before, result)
		if len(changes) == 0 {
			return nil
		}
		if activityBody != "" {
			return recordCloseReason(tx, caller, issue.ID, activityBody, changes)
		}
		return recordChange(tx, caller, issue.ID, action, changes)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
