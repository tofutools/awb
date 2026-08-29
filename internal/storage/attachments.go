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
const attachmentColumns = `id, issue, name, content_type, size, sha256, created_at`

func scanAttachment(row rowScanner) (*domain.Attachment, error) {
	var a domain.Attachment
	err := row.Scan(&a.ID, &a.Issue, &a.Name, &a.ContentType, &a.Size, &a.Sha256, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// InsertAttachment stores an attachment's metadata, drawing a fresh salt and
// retrying on an ID collision inside the same transaction, exactly as an issue
// does.
func (t *Tx) InsertAttachment(a *domain.Attachment) error {
	const maxAttempts = 8
	a.CreatedAt = Now()

	for attempt := range maxAttempts {
		salt, err := domain.NewSalt()
		if err != nil {
			return err
		}
		a.ID = domain.MintAttachmentID(a.Name, a.CreatedAt, salt)

		_, err = t.q.ExecContext(t.ctx, `
			INSERT INTO attachments (`+attachmentColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.Issue, a.Name, a.ContentType, a.Size, a.Sha256, a.CreatedAt)
		if err == nil {
			return nil
		}
		if isUniqueViolation(err) && attempt < maxAttempts-1 {
			continue // an ID collision: draw a new salt and try again
		}
		if isCheckViolation(err) {
			return awberr.Runtimef("refusing to store an inconsistent attachment: %s", err.Error())
		}
		return awberr.Wrap(awberr.Runtime, err, "attach %s", a.Name)
	}
	return awberr.Runtimef("could not mint a free attachment id after %d attempts", maxAttempts)
}

// GetAttachment reads one attachment by its exact ID.
func (t *Tx) GetAttachment(id string) (*domain.Attachment, error) {
	a, err := scanAttachment(t.q.QueryRowContext(t.ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such attachment: %s", id)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read attachment %s", id)
	}
	return a, nil
}

// ResolveAttachmentRef turns a reference — a full attachment ID or a prefix of
// one — into exactly one ID, reporting an ambiguous one rather than guessing,
// exactly as an issue reference is resolved.
func (t *Tx) ResolveAttachmentRef(ref string) (string, error) {
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT id FROM attachments WHERE id LIKE ? || '%' ORDER BY id LIMIT 2`, ref)
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "resolve attachment %s", ref)
	}
	defer rows.Close()

	var matches []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", awberr.Wrap(awberr.Runtime, err, "resolve attachment %s", ref)
		}
		matches = append(matches, id)
	}
	if err := rows.Err(); err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "resolve attachment %s", ref)
	}

	switch len(matches) {
	case 0:
		return "", awberr.NotFoundf("no such attachment: %s", ref)
	case 1:
		return matches[0], nil
	default:
		return "", awberr.Usagef("ambiguous attachment id %q: it matches %s and at least one other",
			ref, matches[0])
	}
}

// ListAttachments returns one issue's attachments in their specified order —
// oldest first, then by id, which is domain.SortAttachments in SQL — together
// with the unpaged total.
func (t *Tx) ListAttachments(issueID string, limit, offset *int) ([]domain.Attachment, int, error) {
	var total int
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM attachments WHERE issue = ?`, issueID).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count attachments of %s", issueID)
	}

	rows, err := t.q.QueryContext(t.ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE issue = ?
		  ORDER BY created_at ASC, id ASC`+limitOffsetClause(limit, offset), issueID)
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
func (t *Tx) DeleteAttachment(id string) error {
	if _, err := t.q.ExecContext(t.ctx, `DELETE FROM attachments WHERE id = ?`, id); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete attachment %s", id)
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
