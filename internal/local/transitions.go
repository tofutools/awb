package local

import (
	"context"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// Status and assignees change through these transitions and the move operation.
// Keeping them out of update is what stops in_progress and the assignment set
// from drifting apart and stops a claim being taken silently.

// checkAssignees refuses a name that is not a user, which is what keeps the
// assignee vocabulary and the user directory one vocabulary.
//
// It is not a foreign key, and deliberately not: an assignee is stored as the
// text it always was, so deleting an account leaves the record of who did the
// work exactly as it stands. The rule is applied where an assignment is made
// instead, inside the same transaction as the write.
//
// A database holding no user at all has no directory to check against, and
// keeps the version 1 behaviour of taking any assignee. Adding the first user
// turns the rule on, whether or not that user has a password — an account that
// exists only to be assigned work is exactly what a passwordless one is for.
func checkAssignees(tx *storage.Tx, assignees ...string) error {
	if len(assignees) == 0 {
		return nil
	}
	any, err := tx.AnyUsers()
	if err != nil || !any {
		return err
	}
	for _, assignee := range assignees {
		exists, err := tx.UserExists(assignee)
		if err != nil {
			return err
		}
		if !exists {
			return awberr.Usagef("no such user: %s", assignee)
		}
	}
	return nil
}

// Claim atomically adds an assignee and sets status to in_progress.
//
// Claiming an issue you already hold succeeds; if it is already in_progress
// nothing changes. Another claimant joins without replacing anyone. A blocked
// or closed issue conflicts, and --force overrides those two refusals. Backlog
// is explicitly activated by claiming; parked ancestors still exclude it from ready.
//
// The assignee has to be a user; see checkAssignees. --force overrides that
// too, but only on the command line over the database file, where the check is
// a convenience rather than a control. Through the API it stands whatever the
// request asks for.
func (b *Backend) Claim(ctx context.Context, ref string, req backend.ClaimRequest,
	ifMatch string) (*domain.Issue, error) {
	assignee, err := domain.ValidateAssignee(req.Assignee)
	if err != nil {
		return nil, err
	}
	return b.mutate(ctx, ref, ifMatch, "claimed", "", func(tx *storage.Tx, issue *domain.Issue) error {
		if !req.Force {
			if issue.Status == domain.StatusClosed {
				return awberr.Conflictf("%s is closed", issue.ID)
			}
			if issue.Blocked {
				return awberr.Conflictf("%s is blocked by %v", issue.ID, issue.Blockers)
			}
		}
		if b.served || !req.Force {
			if err := checkAssignees(tx, assignee); err != nil {
				return err
			}
		}

		fields := storage.Fields(issue)
		if req.Force && issue.Status == domain.StatusClosed {
			// A closed issue's assignees are a historical record. Reclaiming it starts
			// a new active assignment rather than reviving everyone who completed it.
			fields.Assignees = nil
		}
		if !slices.Contains(fields.Assignees, assignee) {
			fields.Assignees = append(fields.Assignees, assignee)
		}
		fields.Status = domain.StatusInProgress
		return tx.UpdateIssue(issue, fields)
	})
}

// Release removes one named assignee. The issue returns to open only when no
// assignees remain; --force clears every assignee.
//
// The name is not checked against the user directory: releasing is how an
// assignment that names nobody is taken off an issue, so refusing it because
// the name is not a user would leave it stuck there.
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
		if issue.Status == domain.StatusBacklog {
			return nil // Releasing unassigned parked work does not activate it.
		}
		if len(fields.Assignees) == 0 {
			fields.Status = domain.StatusOpen
		} else {
			fields.Status = domain.StatusInProgress
		}
		return tx.UpdateIssue(issue, fields)
	})
}

// CloseIssue sets status to closed and records a non-empty reason as a typed
// comment on that same transition. Closing a closed issue succeeds and changes
// nothing, so a reason can never become detached from the act of closing. The
// assignees are left alone, since they record who did the work.
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

// Reopen sets status to open and clears the assignees, so the issue returns to
// the pool awb ready draws from. Its close-reason comment remains in history.
//
// It acts on closed or backlog issues: on an issue that is not closed it succeeds
// and changes nothing, whatever its assignee, so it can never take a claim
// away from somebody who is working. Clearing the assignee of a closed issue
// is the point of the command and needs no force, the assignee there being a
// record of who did the work rather than a claim on it.
func (b *Backend) Reopen(ctx context.Context, ref, ifMatch string) (*domain.Issue, error) {
	return b.mutate(ctx, ref, ifMatch, "reopened", "", func(tx *storage.Tx, issue *domain.Issue) error {
		if issue.Status != domain.StatusClosed && issue.Status != domain.StatusBacklog {
			return nil
		}
		fields := storage.Fields(issue)
		fields.Status = domain.StatusOpen
		fields.Assignees = nil
		return tx.UpdateIssue(issue, fields)
	})
}
