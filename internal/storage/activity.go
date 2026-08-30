package storage

import (
	"encoding/json"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

const activityColumns = `id, issue, kind, actor, body, action, changes, created_at`

func scanActivity(row rowScanner) (*domain.Activity, error) {
	var (
		a       domain.Activity
		changes string
	)
	if err := row.Scan(&a.ID, &a.Issue, &a.Kind, &a.Actor, &a.Body, &a.Action,
		&changes, &a.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(changes), &a.Changes); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "decode activity %d changes", a.ID)
	}
	a.Normalize()
	return &a, nil
}

// InsertActivity appends one entry and fills its database-assigned id and
// creation time. It is called inside the transaction of the action it records.
func (t *Tx) InsertActivity(a *domain.Activity) error {
	a.Normalize()
	encoded, err := json.Marshal(a.Changes)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "encode activity changes")
	}
	a.CreatedAt = Now()
	result, err := t.q.ExecContext(t.ctx, `
		INSERT INTO issue_activity (issue, kind, actor, body, action, changes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Issue, a.Kind, a.Actor, a.Body, a.Action, string(encoded), a.CreatedAt)
	if err != nil {
		if isCheckViolation(err) {
			return awberr.Runtimef("refusing to store inconsistent activity: %s", err.Error())
		}
		return awberr.Wrap(awberr.Runtime, err, "record activity for %s", a.Issue)
	}
	a.ID, err = result.LastInsertId()
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read activity id")
	}
	a.Normalize()
	return nil
}

// ListActivity returns an issue's newest activity first. An empty kind means
// both kinds. The id is the final tiebreak, so the order is total.
func (t *Tx) ListActivity(issue string, kind domain.ActivityKind, limit, offset *int) (
	[]domain.Activity, int, error) {
	where := `issue = ?`
	args := []any{issue}
	if kind != "" {
		where += ` AND kind = ?`
		args = append(args, kind)
	}

	var total int
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM issue_activity WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count activity of %s", issue)
	}
	rows, err := t.q.QueryContext(t.ctx,
		`SELECT `+activityColumns+` FROM issue_activity WHERE `+where+
			` ORDER BY created_at DESC, id DESC`+limitOffsetClause(limit, offset), args...)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list activity of %s", issue)
	}
	defer rows.Close()

	entries := []domain.Activity{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list activity of %s", issue)
		}
		entries = append(entries, *a)
	}
	return entries, total, awberr.Wrap(awberr.Runtime, rows.Err(), "list activity of %s", issue)
}
