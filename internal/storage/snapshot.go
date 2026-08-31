package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// Snapshot is the stored state the existing read API exposes. Users and
// memberships are deliberately absent: a server never exposes password hashes,
// and a database with no users is an unauthenticated local server.
type Snapshot struct {
	Projects        []domain.Project
	Issues          []domain.Issue
	Attachments     []domain.Attachment
	Activity        []domain.Activity
	ProjectActivity []domain.ProjectActivity
}

// RestoreSnapshot writes an API snapshot into a freshly initialized database,
// preserving every stored field exactly. It uses one transaction so a failed
// restore never leaves a partially populated database.
//
// This is intentionally not expressed through the ordinary create operations:
// those mint new issue IDs and timestamps and apply transition defaults, while
// a dump must remain a faithful local copy of what the server returned.
func (d *DB) RestoreSnapshot(ctx context.Context, snapshot Snapshot) error {
	return d.Write(ctx, func(tx *Tx) error {
		var populated int
		if err := tx.q.QueryRowContext(ctx,
			`SELECT (SELECT count(*) FROM projects) + (SELECT count(*) FROM users)`).Scan(&populated); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "inspect dump database")
		}
		if populated != 0 {
			return awberr.Runtimef("refusing to restore a dump into a populated database")
		}

		for i := range snapshot.Projects {
			p := &snapshot.Projects[i]
			state := p.State
			if state == "" {
				state = domain.ProjectActive
			}
			if _, err := tx.q.ExecContext(ctx, `
				INSERT INTO projects (key, name, description, state, archived_at, archived_by, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				p.Key, p.Name, p.Description, state, p.ArchivedAt, p.ArchivedBy, p.CreatedAt, p.UpdatedAt); err != nil {
				return restoreError(err, "project %s", p.Key)
			}
		}

		issueIDs := make(map[string]struct{}, len(snapshot.Issues))
		for i := range snapshot.Issues {
			issue := &snapshot.Issues[i]
			if err := validateAssignment(issue.Status, issue.Assignees); err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO issues (`+issueColumns+`)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				issue.ID, issue.Project, issue.Title, issue.Description, issue.Type,
				issue.Status, issue.Priority, issue.CreatedAt, issue.UpdatedAt); err != nil {
				return restoreError(err, "issue %s", issue.ID)
			}
			issueIDs[issue.ID] = struct{}{}
			for position, assignee := range issue.Assignees {
				if _, err := tx.q.ExecContext(ctx,
					`INSERT INTO issue_assignees (issue, assignee, position) VALUES (?, ?, ?)`,
					issue.ID, assignee, position); err != nil {
					return restoreError(err, "assignee %q of %s", assignee, issue.ID)
				}
			}

			for _, label := range issue.Labels {
				if _, err := tx.q.ExecContext(ctx,
					`INSERT INTO issue_labels (issue, label) VALUES (?, ?)`, issue.ID, label); err != nil {
					return restoreError(err, "label %q of %s", label, issue.ID)
				}
			}
		}

		// A relation returned on a visible issue may name an issue in a project the
		// caller cannot see. Such an edge cannot exist in the self-contained visible
		// subset, so only restore edges whose two endpoints were dumped.
		type edge struct {
			subject string
			type_   domain.RelationType
			other   string
		}
		edges := make(map[edge]struct{})
		for i := range snapshot.Issues {
			issue := &snapshot.Issues[i]
			for _, relation := range issue.Relations {
				if relation.Direction != domain.DirectionOut {
					continue
				}
				if _, visible := issueIDs[relation.Other]; !visible {
					continue
				}
				subject, other := domain.CanonicalRelation(relation.Type, issue.ID, relation.Other)
				edges[edge{subject: subject, type_: relation.Type, other: other}] = struct{}{}
			}
		}
		for relation := range edges {
			if _, err := tx.q.ExecContext(ctx,
				`INSERT INTO relations (subject, type, other) VALUES (?, ?, ?)`,
				relation.subject, relation.type_, relation.other); err != nil {
				return restoreError(err, "relation %s %s %s",
					relation.subject, relation.type_, relation.other)
			}
		}

		for i := range snapshot.Attachments {
			a := &snapshot.Attachments[i]
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO attachments (`+attachmentColumns+`)
				VALUES (?, ?, ?, ?, ?, ?)`,
				a.Issue, a.Name, a.ContentType, a.Size, a.Sha256, a.CreatedAt); err != nil {
				return restoreError(err, "attachment %q of %s", a.Name, a.Issue)
			}
		}

		for i := range snapshot.Activity {
			a := &snapshot.Activity[i]
			changes, err := json.Marshal(a.Changes)
			if err != nil {
				return restoreError(err, "activity %d", a.ID)
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO issue_activity (`+activityColumns+`)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				a.ID, a.Issue, a.Kind, a.Actor, a.Body, a.Action,
				string(changes), a.CreatedAt); err != nil {
				return restoreError(err, "activity %d of %s", a.ID, a.Issue)
			}
		}

		for i := range snapshot.ProjectActivity {
			a := &snapshot.ProjectActivity[i]
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO project_activity
				(id, project, action, actor, created_at) VALUES (?, ?, ?, ?, ?)`,
				a.ID, a.Project, a.Action, a.Actor, a.CreatedAt); err != nil {
				return restoreError(err, "project activity %d of %s", a.ID, a.Project)
			}
		}

		return nil
	})
}

func restoreError(err error, format string, args ...any) error {
	return awberr.Wrap(awberr.Runtime, err, "restore %s", fmt.Sprintf(format, args...))
}
