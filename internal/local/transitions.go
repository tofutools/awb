package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// The four transitions below are the only way status or assignee ever moves.
// Keeping them out of update is what stops in_progress and an assignee from
// drifting apart and stops a claim being taken silently.

// Claim atomically sets the assignee and status to in_progress.
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

	return b.mutate(ctx, ref, ifMatch, "claimed", func(tx *storage.Tx, issue *domain.Issue) error {
		// The compare-and-set, checked inside the write lock, so two agents racing
		// for the same issue cannot both win.
		if req.ExpectAssignee != nil && issue.Assignee != *req.ExpectAssignee {
			return awberr.Conflictf("%s is assigned to %q, not to %q",
				issue.ID, issue.Assignee, *req.ExpectAssignee)
		}

		if !req.Force {
			if issue.Assignee != "" && issue.Assignee != assignee {
				return awberr.Conflictf("%s is already held by %s", issue.ID, issue.Assignee)
			}
			if issue.Status == domain.StatusClosed {
				return awberr.Conflictf("%s is closed", issue.ID)
			}
			if issue.Blocked {
				return awberr.Conflictf("%s is blocked by %v", issue.ID, issue.Blockers)
			}
		}

		fields := storage.Fields(issue)
		fields.Assignee = assignee
		fields.Status = domain.StatusInProgress
		// A forced claim on a closed issue clears the close reason along with the
		// status, since a non-closed issue never carries one.
		fields.CloseReason = ""
		return tx.UpdateIssue(issue, fields)
	})
}

// Release clears the assignee and sets status back to open.
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

	return b.mutate(ctx, ref, ifMatch, "released", func(tx *storage.Tx, issue *domain.Issue) error {
		if !req.Force {
			if issue.Status == domain.StatusClosed {
				return awberr.Conflictf("%s is closed", issue.ID)
			}
			if issue.Assignee != "" && issue.Assignee != req.Assignee {
				return awberr.Conflictf("%s is held by %s, not by you", issue.ID, issue.Assignee)
			}
		}

		fields := storage.Fields(issue)
		fields.Assignee = ""
		fields.Status = domain.StatusOpen
		// As on claim, a forced release of a closed issue clears the reason too: a
		// non-closed issue never carries one.
		fields.CloseReason = ""
		return tx.UpdateIssue(issue, fields)
	})
}

// CloseIssue sets status to closed and, when a reason is given, records it.
//
// Closing a closed issue succeeds; omitting the reason leaves the recorded one
// alone and an empty reason clears it. The assignee is left alone, since it
// records who did the work. Closing never inspects related issues.
func (b *Backend) CloseIssue(ctx context.Context, ref string, req backend.CloseRequest,
	ifMatch string) (*domain.Issue, error) {
	var reason *string
	if req.Reason != nil {
		validated, err := domain.ValidateCloseReason(*req.Reason)
		if err != nil {
			return nil, err
		}
		reason = &validated
	}

	return b.mutate(ctx, ref, ifMatch, "closed", func(tx *storage.Tx, issue *domain.Issue) error {
		fields := storage.Fields(issue)
		fields.Status = domain.StatusClosed
		if reason != nil {
			fields.CloseReason = *reason
		}
		return tx.UpdateIssue(issue, fields)
	})
}

// Reopen sets status to open, clears the close reason and clears the assignee,
// so the issue returns to the pool awb ready draws from.
//
// It acts only on a closed issue: on an issue that is not closed it succeeds
// and changes nothing, whatever its assignee, so it can never take a claim
// away from somebody who is working. Clearing the assignee of a closed issue
// is the point of the command and needs no force, the assignee there being a
// record of who did the work rather than a claim on it.
func (b *Backend) Reopen(ctx context.Context, ref, ifMatch string) (*domain.Issue, error) {
	return b.mutate(ctx, ref, ifMatch, "reopened", func(tx *storage.Tx, issue *domain.Issue) error {
		if issue.Status != domain.StatusClosed {
			return nil
		}
		fields := storage.Fields(issue)
		fields.Status = domain.StatusOpen
		fields.Assignee = ""
		fields.CloseReason = ""
		return tx.UpdateIssue(issue, fields)
	})
}
