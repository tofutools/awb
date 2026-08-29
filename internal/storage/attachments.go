package storage

import (
	"database/sql"
	"errors"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// attachmentColumns is the stored shape of an Attachment, in the order
// scanAttachment reads. An attachment has no derived field: what is stored is
// the whole of it.
const attachmentColumns = `issue, name, content_type, size, sha256, created_at`

func scanAttachment(row rowScanner) (*domain.Attachment, error) {
	var a domain.Attachment
	err := row.Scan(&a.Issue, &a.Name, &a.ContentType, &a.Size, &a.Sha256, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// InsertAttachment stores an attachment's metadata.
//
// An issue cannot hold two attachments under one name, that pair being what
// identifies one, so a second is refused rather than being given a name it was
// not asked to have. It is a conflict rather than a usage error because
// whether it is one depends on what is stored.
func (t *Tx) InsertAttachment(a *domain.Attachment) error {
	a.CreatedAt = Now()

	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO attachments (`+attachmentColumns+`)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.Issue, a.Name, a.ContentType, a.Size, a.Sha256, a.CreatedAt)
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err):
		return awberr.Conflictf("%s already has an attachment named %q", a.Issue, a.Name)
	case isCheckViolation(err):
		return awberr.Runtimef("refusing to store an inconsistent attachment: %s", err.Error())
	default:
		return awberr.Wrap(awberr.Runtime, err, "attach %s", a.Name)
	}
}

// GetAttachment reads one attachment by the issue it belongs to and its name.
func (t *Tx) GetAttachment(issueID, name string) (*domain.Attachment, error) {
	a, err := scanAttachment(t.q.QueryRowContext(t.ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE issue = ? AND name = ?`,
		issueID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("%s has no attachment named %q", issueID, name)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read attachment %q of %s", name, issueID)
	}
	return a, nil
}

// ListAttachments returns one issue's attachments in their specified order —
// oldest first, then by name, which is domain.SortAttachments in SQL —
// together with the unpaged total.
func (t *Tx) ListAttachments(issueID string, limit, offset *int) ([]domain.Attachment, int, error) {
	var total int
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM attachments WHERE issue = ?`, issueID).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count attachments of %s", issueID)
	}

	rows, err := t.q.QueryContext(t.ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE issue = ?
		  ORDER BY created_at ASC, name ASC`+limitOffsetClause(limit, offset), issueID)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list attachments of %s", issueID)
	}
	defer rows.Close()

	attachments := []domain.Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list attachments of %s", issueID)
		}
		attachments = append(attachments, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list attachments of %s", issueID)
	}
	return attachments, total, nil
}

// loadAttachments fills in the attachments of a set of issues, in one query
// rather than one per issue.
func (t *Tx) loadAttachments(ids []string, byID map[string]*domain.Issue) error {
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE issue IN (`+placeholders(len(ids))+`)`,
		anyArgs(ids)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read attachments")
	}
	defer rows.Close()

	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read attachments")
		}
		if issue := byID[a.Issue]; issue != nil {
			issue.Attachments = append(issue.Attachments, *a)
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read attachments")
}

// DeleteAttachment removes one attachment's row.
func (t *Tx) DeleteAttachment(issueID, name string) error {
	_, err := t.q.ExecContext(t.ctx,
		`DELETE FROM attachments WHERE issue = ? AND name = ?`, issueID, name)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete attachment %q of %s", name, issueID)
	}
	return nil
}

// DigestsOfIssue is the distinct content digests one issue's attachments name.
// It is read before the issue is deleted, so that the content those rows were
// keeping alive can be considered afterwards.
func (t *Tx) DigestsOfIssue(issueID string) ([]string, error) {
	return t.digests(`SELECT DISTINCT sha256 FROM attachments WHERE issue = ?`, issueID)
}

// DigestsOfProject is DigestsOfIssue for every issue a project holds, which is
// what a cascading project delete needs.
func (t *Tx) DigestsOfProject(key string) ([]string, error) {
	return t.digests(`SELECT DISTINCT a.sha256 FROM attachments a
	                    JOIN issues i ON i.id = a.issue
	                   WHERE i.project = ?`, key)
}

func (t *Tx) digests(query string, arg any) ([]string, error) {
	rows, err := t.q.QueryContext(t.ctx, query, arg)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read attachment digests")
	}
	defer rows.Close()

	var sums []string
	for rows.Next() {
		var sum string
		if err := rows.Scan(&sum); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read attachment digests")
		}
		sums = append(sums, sum)
	}
	return sums, awberr.Wrap(awberr.Runtime, rows.Err(), "read attachment digests")
}

// DigestIsUnreferenced reports whether no attachment row names this digest any
// more, which is when the file holding it may go. Two attachments of the same
// bytes share one file, so deleting one of them must not take the other's
// content with it.
func (t *Tx) DigestIsUnreferenced(sum string) (bool, error) {
	var n int
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM attachments WHERE sha256 = ?`, sum).Scan(&n); err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "count attachments holding %s", sum)
	}
	return n == 0, nil
}
