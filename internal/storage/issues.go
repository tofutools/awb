package storage

import (
	"database/sql"
	"errors"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// issueColumns is the stored half of an Issue, in the order scanIssue reads.
const issueColumns = `id, project, title, description, type, status, priority,
	assignee, close_reason, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanIssue(row rowScanner) (*domain.Issue, error) {
	var i domain.Issue
	err := row.Scan(&i.ID, &i.Project, &i.Title, &i.Description, &i.Type, &i.Status,
		&i.Priority, &i.Assignee, &i.CloseReason, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// GetIssue reads one issue by its exact ID, complete with its derived fields.
func (t *Tx) GetIssue(id string) (*domain.Issue, error) {
	issue, err := t.getIssueRow(id)
	if err != nil {
		return nil, err
	}
	if err := t.hydrate([]*domain.Issue{issue}); err != nil {
		return nil, err
	}
	return issue, nil
}

// getIssueRow reads the stored half of one issue by its exact ID.
//
// An issue in a project outside the transaction's scope is not found rather
// than refused, exactly as such a project itself is: a caller who is not a
// member is not told that the issue exists.
func (t *Tx) getIssueRow(id string) (*domain.Issue, error) {
	visible, args := t.visibleClause("project")
	issue, err := scanIssue(t.q.QueryRowContext(t.ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = ? AND `+visible,
		append([]any{id}, args...)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such issue: %s", id)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read issue %s", id)
	}
	return issue, nil
}

// ResolveIssueRef turns a reference — a full ID, an ID prefix, or a bare hash
// or hash prefix — into exactly one issue ID.
//
// A reference matching nothing is not found (exit 3, 404); one matching more
// than one issue is a usage error (exit 2, 400) rather than a guess.
// Uniqueness of a bare hash is a property of the data at that moment, not a
// guarantee.
//
// The scope applies here too, and it has to: an issue the caller may not see
// must not be reachable by any spelling of its reference, and a prefix must
// not be reported ambiguous because of one. So a hash matching one visible and
// one invisible issue resolves, and uniqueness is uniqueness among what the
// caller can see.
func (t *Tx) ResolveIssueRef(ref domain.IssueRef) (string, error) {
	visible, scopeArgs := t.visibleClause("project")
	var (
		rows *sql.Rows
		err  error
	)
	if ref.Project == "" {
		// A bare hash matches on the hash part of any ID, in any project.
		rows, err = t.q.QueryContext(t.ctx,
			`SELECT id FROM issues
			  WHERE substr(id, length(project) + 2) LIKE ? || '%' AND `+visible+`
			  ORDER BY id LIMIT 2`, append([]any{ref.Hash}, scopeArgs...)...)
	} else {
		rows, err = t.q.QueryContext(t.ctx,
			`SELECT id FROM issues WHERE project = ? AND id LIKE ? || '%' AND `+visible+`
			  ORDER BY id LIMIT 2`,
			append([]any{ref.Project, ref.Project + "-" + ref.Hash}, scopeArgs...)...)
	}
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "resolve issue %s", ref.Raw)
	}
	defer rows.Close()

	var matches []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", awberr.Wrap(awberr.Runtime, err, "resolve issue %s", ref.Raw)
		}
		matches = append(matches, id)
	}
	if err := rows.Err(); err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "resolve issue %s", ref.Raw)
	}

	switch len(matches) {
	case 0:
		return "", awberr.NotFoundf("no such issue: %s", ref.Raw)
	case 1:
		return matches[0], nil
	default:
		return "", awberr.Usagef("ambiguous issue id %q: it matches %s and at least one other",
			ref.Raw, matches[0])
	}
}

// hydrate fills in the derived fields for a set of issues, in a fixed number
// of queries rather than one per issue.
func (t *Tx) hydrate(issues []*domain.Issue) error {
	if len(issues) == 0 {
		return nil
	}

	byID := make(map[string]*domain.Issue, len(issues))
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
		ids = append(ids, issue.ID)
	}

	if err := t.loadLabels(ids, byID); err != nil {
		return err
	}
	if err := t.loadRelations(ids, byID); err != nil {
		return err
	}
	if err := t.loadBlockers(ids, byID); err != nil {
		return err
	}
	if err := t.loadAttachments(ids, byID); err != nil {
		return err
	}

	for _, issue := range issues {
		issue.Links = domain.ExtractLinks(issue.Description)
		issue.Normalize()
	}
	return nil
}

func (t *Tx) loadLabels(ids []string, byID map[string]*domain.Issue) error {
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT issue, label FROM issue_labels WHERE issue IN (`+placeholders(len(ids))+`)`,
		anyArgs(ids)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read labels")
	}
	defer rows.Close()

	for rows.Next() {
		var issueID, label string
		if err := rows.Scan(&issueID, &label); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read labels")
		}
		if issue := byID[issueID]; issue != nil {
			issue.Labels = append(issue.Labels, label)
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read labels")
}

// loadRelations reads every relation each issue takes part in, at either end.
//
// A relation is stored once and shown on both issues; direction identifies the
// viewed endpoint. A symmetric related pair is always direction "out", since
// both ends read the same.
func (t *Tx) loadRelations(ids []string, byID map[string]*domain.Issue) error {
	in := placeholders(len(ids))
	args := append(anyArgs(ids), anyArgs(ids)...)

	rows, err := t.q.QueryContext(t.ctx, `
		SELECT subject AS viewed, type, other AS counterpart, 'out' AS direction
		  FROM relations WHERE subject IN (`+in+`)
		UNION ALL
		SELECT other AS viewed, type, subject AS counterpart,
		       CASE WHEN type = 'related' THEN 'out' ELSE 'in' END AS direction
		  FROM relations WHERE other IN (`+in+`)`, args...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read relations")
	}
	defer rows.Close()

	for rows.Next() {
		var viewed string
		var rel domain.Relation
		if err := rows.Scan(&viewed, &rel.Type, &rel.Other, &rel.Direction); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read relations")
		}
		if issue := byID[viewed]; issue != nil {
			issue.Relations = append(issue.Relations, rel)
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read relations")
}

// loadBlockers computes the derived blocked state: an issue is blocked when it
// is itself not closed and at least one issue it is blocked-by is not closed.
//
// A closed issue is therefore never blocked and its blockers are empty,
// whatever its blocked-by relations still say, which is what makes it
// impossible for the recorded state to disagree with the dependency graph.
func (t *Tx) loadBlockers(ids []string, byID map[string]*domain.Issue) error {
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT r.subject, r.other
		  FROM relations r
		  JOIN issues subject ON subject.id = r.subject
		  JOIN issues other   ON other.id   = r.other
		 WHERE r.type = 'blocked-by'
		   AND r.subject IN (`+placeholders(len(ids))+`)
		   AND subject.status <> 'closed'
		   AND other.status   <> 'closed'`, anyArgs(ids)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read blockers")
	}
	defer rows.Close()

	for rows.Next() {
		var subject, other string
		if err := rows.Scan(&subject, &other); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read blockers")
		}
		if issue := byID[subject]; issue != nil {
			issue.Blockers = append(issue.Blockers, other)
			issue.Blocked = true
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read blockers")
}

// InsertIssue stores a new issue, drawing a fresh salt and retrying on a
// same-project ID collision inside the same transaction.
func (t *Tx) InsertIssue(issue *domain.Issue) error {
	const maxAttempts = 8
	now := Now()
	issue.CreatedAt = now
	issue.UpdatedAt = now

	for attempt := range maxAttempts {
		salt, err := domain.NewSalt()
		if err != nil {
			return err
		}
		issue.ID = domain.MakeID(issue.Project, domain.MintHash(issue.Title, now, salt))

		_, err = t.q.ExecContext(t.ctx, `
			INSERT INTO issues (`+issueColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			issue.ID, issue.Project, issue.Title, issue.Description, issue.Type,
			issue.Status, issue.Priority, issue.Assignee, issue.CloseReason,
			issue.CreatedAt, issue.UpdatedAt)
		if err == nil {
			return nil
		}
		if isUniqueViolation(err) && attempt < maxAttempts-1 {
			continue // an ID collision: draw a new salt and try again
		}
		if isCheckViolation(err) {
			return awberr.Runtimef("refusing to store an inconsistent issue: %s", err.Error())
		}
		return awberr.Wrap(awberr.Runtime, err, "create issue")
	}
	return awberr.Runtimef("could not mint a free issue id in project %s after %d attempts",
		issue.Project, maxAttempts)
}

// IssueFields are the stored fields an update may change.
type IssueFields struct {
	Title       string
	Description string
	Type        domain.Type
	Status      domain.Status
	Priority    int
	Assignee    string
	CloseReason string
}

// Fields reads the stored half of an issue.
func Fields(i *domain.Issue) IssueFields {
	return IssueFields{
		Title: i.Title, Description: i.Description, Type: i.Type, Status: i.Status,
		Priority: i.Priority, Assignee: i.Assignee, CloseReason: i.CloseReason,
	}
}

// UpdateIssue writes the stored fields of an issue, moving updated_at only
// when something actually changed. A write that changes nothing leaves the
// timestamp alone.
func (t *Tx) UpdateIssue(issue *domain.Issue, fields IssueFields) error {
	if Fields(issue) == fields {
		return nil
	}
	updated := bumpedTimestamp(issue.UpdatedAt, Now())

	_, err := t.q.ExecContext(t.ctx, `
		UPDATE issues
		   SET title = ?, description = ?, type = ?, status = ?, priority = ?,
		       assignee = ?, close_reason = ?, updated_at = ?
		 WHERE id = ?`,
		fields.Title, fields.Description, fields.Type, fields.Status, fields.Priority,
		fields.Assignee, fields.CloseReason, updated, issue.ID)
	if err != nil {
		if isCheckViolation(err) {
			return awberr.Runtimef("refusing to store an inconsistent issue: %s", err.Error())
		}
		return awberr.Wrap(awberr.Runtime, err, "update issue %s", issue.ID)
	}

	issue.Title = fields.Title
	issue.Description = fields.Description
	issue.Type = fields.Type
	issue.Status = fields.Status
	issue.Priority = fields.Priority
	issue.Assignee = fields.Assignee
	issue.CloseReason = fields.CloseReason
	issue.UpdatedAt = updated
	return nil
}

// touchIssue moves updated_at for a change that is not to a column of the
// issues table — a label being added or removed, which counts as a change to
// the issue.
func (t *Tx) touchIssue(issue *domain.Issue) error {
	updated := bumpedTimestamp(issue.UpdatedAt, Now())
	_, err := t.q.ExecContext(t.ctx,
		`UPDATE issues SET updated_at = ? WHERE id = ?`, updated, issue.ID)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update issue %s", issue.ID)
	}
	issue.UpdatedAt = updated
	return nil
}

// AddLabel adds a label to an issue. Adding one the issue already carries
// succeeds and changes nothing, timestamp included.
func (t *Tx) AddLabel(issue *domain.Issue, label string) error {
	result, err := t.q.ExecContext(t.ctx,
		`INSERT OR IGNORE INTO issue_labels (issue, label) VALUES (?, ?)`, issue.ID, label)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "add label %s", label)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "add label %s", label)
	}
	if n == 0 {
		return nil
	}
	return t.touchIssue(issue)
}

// RemoveLabel removes a label from an issue. Removing one it does not carry
// succeeds and changes nothing.
func (t *Tx) RemoveLabel(issue *domain.Issue, label string) error {
	result, err := t.q.ExecContext(t.ctx,
		`DELETE FROM issue_labels WHERE issue = ? AND label = ?`, issue.ID, label)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "remove label %s", label)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "remove label %s", label)
	}
	if n == 0 {
		return nil
	}
	return t.touchIssue(issue)
}

// DeleteIssue removes an issue and, by cascade, its labels and every relation
// it takes part in. It reports how many relations went with it, since removing
// a blocker silently makes other issues ready and orphaning children makes a
// decomposed parent's work top-level.
func (t *Tx) DeleteIssue(id string) (relationsRemoved int, err error) {
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM relations WHERE subject = ? OR other = ?`, id, id,
	).Scan(&relationsRemoved); err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "count relations of %s", id)
	}

	if _, err := t.q.ExecContext(t.ctx, `DELETE FROM issues WHERE id = ?`, id); err != nil {
		return 0, awberr.Wrap(awberr.Runtime, err, "delete issue %s", id)
	}
	return relationsRemoved, nil
}

func (t *Tx) scanFacets(query string, args []any) ([]domain.Facet, error) {
	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read facets")
	}
	defer rows.Close()

	facets := []domain.Facet{}
	for rows.Next() {
		var f domain.Facet
		if err := rows.Scan(&f.Value, &f.Count); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read facets")
		}
		facets = append(facets, f)
	}
	return facets, awberr.Wrap(awberr.Runtime, rows.Err(), "read facets")
}
