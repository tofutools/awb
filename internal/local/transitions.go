package local

import (
	"context"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// The four transitions below are the only way status or assignee ever moves.
// Keeping them out of update is what stops in_progress and an assignee from
// drifting apart and stops a claim being taken silently.

// Claim atomically adds an assignee and sets status to in_progress.
//
// Claiming an issue you already hold succeeds; if it is already in_progress
// nothing changes. It fails with a conflict if the issue is assigned to
// someone else, blocked, or closed, and --force overrides all three.
func (b *Backend) Claim(ctx context.Context, ref string, req backend.ClaimRequest,
	ifMatch string) (*domain.Issue, error) {
	assignee, err := domain.ValidateAssignee(req.Assignee)
	if err != nil {
		return nil, err
	}
	if req.ExpectAssignee != nil && *req.ExpectAssignee != "" {
		if _, err := domain.ValidateAssignee(*req.ExpectAssignee); err != nil {
			return nil, err
		}
	}

	return b.mutate(ctx, ref, ifMatch, "claimed", "", func(tx *storage.Tx, issue *domain.Issue) error {
		// The compare-and-set, checked inside the write lock, so two agents racing
		// for the same issue cannot both win.
		if req.ExpectAssignee != nil && issue.Assignee != *req.ExpectAssignee {
			return awberr.Conflictf("%s is assigned to %q, not to %q",
				issue.ID, issue.Assignee, *req.ExpectAssignee)
		}

		if !req.Force {
			if issue.Status == domain.StatusClosed {
				return awberr.Conflictf("%s is closed", issue.ID)
			}
			if issue.Blocked {
				return awberr.Conflictf("%s is blocked by %v", issue.ID, issue.Blockers)
			}
		}

		fields := storage.Fields(issue)
		if !slices.Contains(fields.Assignees, assignee) {
			fields.Assignees = append(fields.Assignees, assignee)
		}
		if fields.Assignee == "" {
			fields.Assignee = assignee
		}
		fields.Status = domain.StatusInProgress
		return tx.UpdateIssue(issue, fields)
	})
}

// Release removes the caller from the assignees. The issue returns to open
// only when no assignees remain; --force clears every assignee.
//
// Releasing an issue that is already open and unassigned succeeds and changes
// nothing. It fails on a closed issue, or on one assigned to someone else,
// unless forced.
func (b *Backend) Release(ctx context.Context, ref string, req backend.ReleaseRequest,
	ifMatch string) (*domain.Issue, error) {
	// The assignee serves only the "assigned to someone else" refusal, so it may
	// be omitted when that refusal is being overridden anyway.
	if req.Assignee != "" {
		if _, err := domain.ValidateAssignee(req.Assignee); err != nil {
			return nil, err
		}
	} else if !req.Force {
		return nil, awberr.Runtimef(
			"no identity is configured: set \"identity\" in the configuration file or AWB_IDENTITY, or use --force")
	}

	return b.mutate(ctx, ref, ifMatch, "released", "", func(tx *storage.Tx, issue *domain.Issue) error {
		if !req.Force {
			if issue.Status == domain.StatusClosed {
				return awberr.Conflictf("%s is closed", issue.ID)
			}
			if len(issue.Assignees) > 0 && !slices.Contains(issue.Assignees, req.Assignee) {
				return awberr.Conflictf("%s is held by %v, not by you", issue.ID, issue.Assignees)
			}
		}

		fields := storage.Fields(issue)
		if req.Force {
			fields.Assignees = nil
		} else {
			fields.Assignees = slices.DeleteFunc(fields.Assignees,
				func(a string) bool { return a == req.Assignee })
		}
		if len(fields.Assignees) == 0 {
			fields.Assignee = ""
			fields.Status = domain.StatusOpen
		} else {
			fields.Assignee = fields.Assignees[0]
			fields.Status = domain.StatusInProgress
		}
		return tx.UpdateIssue(issue, fields)
	})
}

// CloseIssue sets status to closed and records a non-empty reason as a typed
// comment on that same transition. Closing a closed issue succeeds and changes
// nothing, so a reason can never become detached from the act of closing. The
// assignee is left alone, since it records who did the work.
func (b *Backend) CloseIssue(ctx context.Context, ref string, req backend.CloseRequest,
	ifMatch string) (*domain.Issue, error) {
	reason := ""
	if req.Reason != nil {
		validated, err := domain.ValidateCloseReason(*req.Reason)
		if err != nil {
			return nil, err
		}
		reason = validated
	}

	return b.mutate(ctx, ref, ifMatch, "closed", reason, func(tx *storage.Tx, issue *domain.Issue) error {
		fields := storage.Fields(issue)
		fields.Status = domain.StatusClosed
		return tx.UpdateIssue(issue, fields)
	})
}

// Reopen sets status to open and clears the assignee, so the issue returns to
// the pool awb ready draws from. Its close-reason comment remains in history.
//
// It acts only on a closed issue: on an issue that is not closed it succeeds
// and changes nothing, whatever its assignee, so it can never take a claim
// away from somebody who is working. Clearing the assignee of a closed issue
// is the point of the command and needs no force, the assignee there being a
// record of who did the work rather than a claim on it.
func (b *Backend) Reopen(ctx context.Context, ref, ifMatch string) (*domain.Issue, error) {
	return b.mutate(ctx, ref, ifMatch, "reopened", "", func(tx *storage.Tx, issue *domain.Issue) error {
		if issue.Status != domain.StatusClosed {
			return nil
		}
		fields := storage.Fields(issue)
		fields.Status = domain.StatusOpen
		fields.Assignee = ""
		fields.Assignees = nil
		return tx.UpdateIssue(issue, fields)
	})
}
