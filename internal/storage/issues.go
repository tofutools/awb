package storage

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// issueColumns is the stored half of an Issue, in the order scanIssue reads.
const issueColumns = `id, workspace, title, description, commit_hash, pull_request_url, type, status, priority, board_order,
	board_hidden, created_at, updated_at, closed_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanIssue(row rowScanner) (*domain.Issue, error) {
	var i domain.Issue
	err := row.Scan(&i.ID, &i.Workspace, &i.Title, &i.Description, &i.CommitHash, &i.PullRequestURL, &i.Type, &i.Status,
		&i.Priority, &i.Order, &i.BoardHidden, &i.CreatedAt, &i.UpdatedAt, &i.ClosedAt)
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

// IssueWorkspaceState reads only the lifecycle guard for an already-resolved
// endpoint. Relation maintenance may address an existing counterpart outside
// the caller's scope (the visible relation already names it), so this check is
// intentionally independent of presentation scope.
func (t *Tx) IssueWorkspaceState(id string) (domain.WorkspaceState, error) {
	var state domain.WorkspaceState
	err := t.q.QueryRowContext(t.ctx, `SELECT p.state FROM issues i
		JOIN workspaces p ON p.key = i.workspace WHERE i.id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", awberr.NotFoundf("no such issue: %s", id)
	}
	return state, awberr.Wrap(awberr.Runtime, err, "read workspace state of issue %s", id)
}

// getIssueRow reads the stored half of one issue by its exact ID.
//
// An issue in a workspace outside the transaction's scope is not found rather
// than refused, exactly as such a workspace itself is: a caller who is not a
// member is not told that the issue exists.
func (t *Tx) getIssueRow(id string) (*domain.Issue, error) {
	visible, args := t.visibleClause("issues.workspace")
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
	visible, scopeArgs := t.visibleClause("issues.workspace")
	var (
		rows *sql.Rows
		err  error
	)
	if ref.Workspace == "" {
		// A bare hash matches on the hash part of any ID, in any workspace.
		rows, err = t.q.QueryContext(t.ctx,
			`SELECT id FROM issues
			  WHERE substr(id, length(workspace) + 2) LIKE ? || '%' AND `+visible+`
			  ORDER BY id LIMIT 2`, append([]any{ref.Hash}, scopeArgs...)...)
	} else {
		rows, err = t.q.QueryContext(t.ctx,
			`SELECT id FROM issues WHERE workspace = ? AND id LIKE ? || '%' AND `+visible+`
			  ORDER BY id LIMIT 2`,
			append([]any{ref.Workspace, ref.Workspace + "-" + ref.Hash}, scopeArgs...)...)
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

	if err := t.loadAssignees(ids, byID); err != nil {
		return err
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

func (t *Tx) loadAssignees(ids []string, byID map[string]*domain.Issue) error {
	rows, err := t.q.QueryContext(t.ctx, `SELECT issue, assignee FROM issue_assignees
		WHERE issue IN (`+placeholders(len(ids))+`) ORDER BY issue, position`, anyArgs(ids)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read issue assignees")
	}
	defer rows.Close()
	for rows.Next() {
		var issue, assignee string
		if err := rows.Scan(&issue, &assignee); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read issue assignees")
		}
		byID[issue].Assignees = append(byID[issue].Assignees, assignee)
	}
	return rows.Err()
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
	visibleTitleOut, visibleTitleOutArgs := t.visibleClause("counterpart.workspace")
	visibleOut, visibleOutArgs := t.notIgnoredClause("counterpart.workspace")
	visibleTitleIn, visibleTitleInArgs := t.visibleClause("counterpart.workspace")
	visibleIn, visibleInArgs := t.notIgnoredClause("counterpart.workspace")
	args := append(visibleTitleOutArgs, anyArgs(ids)...)
	args = append(args, visibleOutArgs...)
	args = append(args, visibleTitleInArgs...)
	args = append(args, anyArgs(ids)...)
	args = append(args, visibleInArgs...)

	rows, err := t.q.QueryContext(t.ctx, `
		SELECT r.subject AS viewed, r.type, r.other AS counterpart, 'out' AS direction,
		       CASE WHEN `+visibleTitleOut+` THEN counterpart.title ELSE '' END AS counterpart_title
		  FROM relations r JOIN issues counterpart ON counterpart.id = r.other
		 WHERE r.subject IN (`+in+`) AND `+visibleOut+`
		UNION ALL
		SELECT r.other AS viewed, r.type, r.subject AS counterpart,
		       CASE WHEN r.type = 'related' THEN 'out' ELSE 'in' END AS direction,
		       CASE WHEN `+visibleTitleIn+` THEN counterpart.title ELSE '' END AS counterpart_title
		  FROM relations r JOIN issues counterpart ON counterpart.id = r.subject
		 WHERE r.other IN (`+in+`) AND `+visibleIn, args...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read relations")
	}
	defer rows.Close()

	for rows.Next() {
		var viewed, counterpartTitle string
		var rel domain.Relation
		if err := rows.Scan(&viewed, &rel.Type, &rel.Other, &rel.Direction, &counterpartTitle); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read relations")
		}
		if issue := byID[viewed]; issue != nil {
			issue.Relations = append(issue.Relations, rel)
			if counterpartTitle != "" {
				issue.SetRelationTitle(rel.Other, counterpartTitle)
			}
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
	notIgnored, ignoredArgs := t.notIgnoredClause("other.workspace")
	args := append(ignoredArgs, anyArgs(ids)...)
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT r.subject, r.other, `+notIgnored+` AS show_name
		  FROM relations r
		  JOIN issues subject ON subject.id = r.subject
		  JOIN issues other   ON other.id   = r.other
		 WHERE r.type = 'blocked-by'
		   AND r.subject IN (`+placeholders(len(ids))+`)
		   AND subject.status <> 'closed'
		   AND other.status   <> 'closed'`, args...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read blockers")
	}
	defer rows.Close()

	for rows.Next() {
		var subject, other string
		var showName bool
		if err := rows.Scan(&subject, &other, &showName); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read blockers")
		}
		if issue := byID[subject]; issue != nil {
			if showName {
				issue.Blockers = append(issue.Blockers, other)
			}
			issue.Blocked = true
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read blockers")
}

// InsertIssue stores a new issue, drawing a fresh salt and retrying on a
// same-workspace ID collision inside the same transaction.
func (t *Tx) InsertIssue(issue *domain.Issue) error {
	const maxAttempts = 8
	now := Now()
	issue.CreatedAt = now
	issue.UpdatedAt = now
	issue.ClosedAt = ""
	assignees := issue.Assignees
	if err := validateAssignment(issue.Status, assignees); err != nil {
		return err
	}

	for attempt := range maxAttempts {
		salt, err := domain.NewSalt()
		if err != nil {
			return err
		}
		issue.ID = domain.MakeID(issue.Workspace, domain.MintHash(issue.Title, now, salt))

		_, err = t.q.ExecContext(t.ctx, `
			INSERT INTO issues (`+issueColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			issue.ID, issue.Workspace, issue.Title, issue.Description, issue.CommitHash, issue.PullRequestURL, issue.Type,
			issue.Status, issue.Priority, issue.Order, issue.BoardHidden,
			issue.CreatedAt, issue.UpdatedAt, issue.ClosedAt)
		if err == nil {
			for position, assignee := range assignees {
				if _, err := t.q.ExecContext(t.ctx,
					`INSERT INTO issue_assignees (issue, assignee, position) VALUES (?, ?, ?)`,
					issue.ID, assignee, position); err != nil {
					return awberr.Wrap(awberr.Runtime, err, "assign issue %s", issue.ID)
				}
			}
			issue.Assignees = slices.Clone(assignees)
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
	return awberr.Runtimef("could not mint a free issue id in workspace %s after %d attempts",
		issue.Workspace, maxAttempts)
}

// IssueFields are the stored fields an update may change.
type IssueFields struct {
	Title          string
	Description    string
	CommitHash     string
	PullRequestURL string
	Type           domain.Type
	Status         domain.Status
	Priority       int
	Order          int
	BoardHidden    bool
	Assignees      []string
}

// Fields reads the stored half of an issue.
func Fields(i *domain.Issue) IssueFields {
	return IssueFields{
		Title: i.Title, Description: i.Description, CommitHash: i.CommitHash, PullRequestURL: i.PullRequestURL, Type: i.Type, Status: i.Status,
		Priority: i.Priority, Order: i.Order, BoardHidden: i.BoardHidden,
		Assignees: slices.Clone(i.Assignees),
	}
}

// UpdateIssue writes the stored fields of an issue, moving updated_at only
// when something actually changed. A write that changes nothing leaves the
// timestamp alone.
func (t *Tx) UpdateIssue(issue *domain.Issue, fields IssueFields) error {
	if err := validateAssignment(fields.Status, fields.Assignees); err != nil {
		return err
	}
	before := Fields(issue)
	if before.Title == fields.Title && before.Description == fields.Description && before.CommitHash == fields.CommitHash && before.PullRequestURL == fields.PullRequestURL &&
		before.Type == fields.Type && before.Status == fields.Status &&
		before.Priority == fields.Priority && before.Order == fields.Order &&
		before.BoardHidden == fields.BoardHidden &&
		slices.Equal(before.Assignees, fields.Assignees) {
		return nil
	}
	updated := bumpedTimestamp(issue.UpdatedAt, Now())
	closedAt := issue.ClosedAt
	if before.Status != fields.Status {
		if fields.Status == domain.StatusClosed {
			closedAt = updated
		} else {
			closedAt = ""
		}
	}
	if before.Type == domain.TypeEpic && fields.Type != domain.TypeEpic {
		if err := t.removeBoardViewEpicSelections(issue.ID); err != nil {
			return err
		}
	}

	_, err := t.q.ExecContext(t.ctx, `
		UPDATE issues
		   SET title = ?, description = ?, commit_hash = ?, pull_request_url = ?, type = ?, status = ?, priority = ?, board_order = ?,
		       board_hidden = ?, updated_at = ?, closed_at = ?
		 WHERE id = ?`,
		fields.Title, fields.Description, fields.CommitHash, fields.PullRequestURL, fields.Type, fields.Status, fields.Priority, fields.Order,
		fields.BoardHidden, updated, closedAt, issue.ID)
	if err != nil {
		if isCheckViolation(err) {
			return awberr.Runtimef("refusing to store an inconsistent issue: %s", err.Error())
		}
		return awberr.Wrap(awberr.Runtime, err, "update issue %s", issue.ID)
	}
	if _, err := t.q.ExecContext(t.ctx, `DELETE FROM issue_assignees WHERE issue = ?`, issue.ID); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update assignees for %s", issue.ID)
	}
	for position, assignee := range fields.Assignees {
		if _, err := t.q.ExecContext(t.ctx,
			`INSERT INTO issue_assignees (issue, assignee, position) VALUES (?, ?, ?)`,
			issue.ID, assignee, position); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "update assignees for %s", issue.ID)
		}
	}

	issue.Title = fields.Title
	issue.Description = fields.Description
	issue.CommitHash = fields.CommitHash
	issue.PullRequestURL = fields.PullRequestURL
	issue.Type = fields.Type
	issue.Status = fields.Status
	issue.Priority = fields.Priority
	issue.Order = fields.Order
	issue.BoardHidden = fields.BoardHidden
	issue.ClosedAt = closedAt
	issue.Assignees = slices.Clone(fields.Assignees)
	issue.UpdatedAt = updated
	return nil
}

// OrderChange is one sparse-rank write made while placing an issue. Most moves
// contain only the dragged issue; an automatic anchor is the one ordinary case
// that also becomes ranked.
type OrderChange struct {
	Issue    string
	From, To int
}

// ReorderIssue places issue relative to an anchor in its immutable workspace.
// A board additionally supplies destination status and epic so ranks cannot
// cross cells; a regular list leaves both nil and orders within the workspace.
func (t *Tx) ReorderIssue(issue *domain.Issue, beforeID, afterID, direction string,
	status *domain.Status, epic *string) ([]OrderChange, error) {
	visible, args := t.visibleClause("workspace")
	where := "workspace = ? AND " + visible
	args = append([]any{issue.Workspace}, args...)
	if status != nil {
		where += " AND status = ?"
		args = append(args, *status)
	}
	if epic != nil {
		const directEpic = `EXISTS (SELECT 1 FROM relations er JOIN issues parent ON parent.id = er.other
			WHERE er.subject = issues.id AND er.type = 'has-parent'
			  AND parent.type = 'epic' AND parent.workspace = issues.workspace`
		if *epic == "" {
			where += " AND NOT " + directEpic + ")"
		} else {
			where += " AND " + directEpic + " AND parent.id = ?)"
			args = append(args, *epic)
		}
	}
	rows, err := t.q.QueryContext(t.ctx, `SELECT id, board_order, updated_at FROM issues
		WHERE `+where+
		` ORDER BY (board_order = 0) ASC, board_order ASC, priority ASC, updated_at DESC, id ASC`,
		args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read issue order")
	}
	type ordered struct {
		id      string
		order   int
		updated string
	}
	var orderedRows []ordered
	for rows.Next() {
		var row ordered
		if err := rows.Scan(&row.id, &row.order, &row.updated); err != nil {
			rows.Close()
			return nil, err
		}
		orderedRows = append(orderedRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, awberr.Wrap(awberr.Runtime, err, "read issue order")
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if direction != "" {
		position := -1
		for i := range orderedRows {
			if orderedRows[i].id == issue.ID {
				position = i
				break
			}
		}
		if position < 0 {
			return nil, awberr.Usagef("the issue is not visible in its ordering scope")
		}
		switch direction {
		case "earlier":
			if position == 0 {
				return nil, nil
			}
			beforeID = orderedRows[position-1].id
		case "later":
			if position == len(orderedRows)-1 {
				return nil, nil
			}
			afterID = orderedRows[position+1].id
		default:
			return nil, awberr.Usagef("direction must be earlier or later")
		}
	}
	var current []ordered
	for _, row := range orderedRows {
		if row.id != issue.ID {
			current = append(current, row)
		}
	}
	var changes []OrderChange
	write := func(row *ordered, want int) error {
		if row.order == want {
			return nil
		}
		updated := bumpedTimestamp(row.updated, Now())
		if _, err := t.q.ExecContext(t.ctx, `UPDATE issues SET board_order = ?, updated_at = ? WHERE id = ?`,
			want, updated, row.id); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "reorder issue %s", row.id)
		}
		changes = append(changes, OrderChange{Issue: row.id, From: row.order, To: want})
		row.order, row.updated = want, updated
		if row.id == issue.ID {
			issue.Order, issue.UpdatedAt = want, updated
		}
		return nil
	}

	const step = 1 << 20
	ranked := make([]ordered, 0, len(current))
	maxOrder := 0
	var anchor *ordered
	anchorID := beforeID
	if anchorID == "" {
		anchorID = afterID
	}
	for i := range current {
		if current[i].order > 0 {
			ranked = append(ranked, current[i])
			if current[i].order > maxOrder {
				maxOrder = current[i].order
			}
		}
		if current[i].id == anchorID {
			copy := current[i]
			anchor = &copy
		}
	}
	moving := ordered{id: issue.ID, order: issue.Order, updated: issue.UpdatedAt}
	if anchorID == "" {
		if err := write(&moving, maxOrder+step); err != nil {
			return nil, err
		}
		return changes, nil
	}
	if anchor == nil {
		return nil, awberr.Usagef("the order anchor is not visible")
	}
	if anchor.order == 0 {
		if direction != "" && issue.Order == 0 {
			// Ranked rows sort before every automatic row. To swap two rows in
			// the automatic tail without pulling that pair to its front, freeze
			// only the automatic prefix through the pair in its desired order.
			desired := slices.Clone(orderedRows)
			position := -1
			for i := range desired {
				if desired[i].id == issue.ID {
					position = i
					break
				}
			}
			target := position - 1
			if direction == "later" {
				target = position + 1
			}
			desired[position], desired[target] = desired[target], desired[position]
			nextOrder := maxOrder
			through := max(position, target)
			for i := range through + 1 {
				if desired[i].order > 0 {
					continue
				}
				nextOrder += step
				if err := write(&desired[i], nextOrder); err != nil {
					return nil, err
				}
			}
			return changes, nil
		}
		if beforeID != "" {
			if err := write(&moving, maxOrder+step); err != nil {
				return nil, err
			}
			if err := write(anchor, maxOrder+2*step); err != nil {
				return nil, err
			}
		} else {
			if err := write(anchor, maxOrder+step); err != nil {
				return nil, err
			}
			if err := write(&moving, maxOrder+2*step); err != nil {
				return nil, err
			}
		}
		return changes, nil
	}

	insert := -1
	for i := range ranked {
		if ranked[i].id == anchor.id {
			insert = i
			break
		}
	}
	if insert < 0 {
		return nil, awberr.Usagef("the order anchor is not visible")
	}
	if afterID != "" {
		insert++
	}
	previous, next := 0, 0
	if insert > 0 {
		previous = ranked[insert-1].order
	}
	if insert < len(ranked) {
		next = ranked[insert].order
	}
	want := previous + step
	if next > previous+1 {
		want = previous + (next-previous)/2
	}
	if next == 0 || next > previous+1 {
		if err := write(&moving, want); err != nil {
			return nil, err
		}
		return changes, nil
	}

	// The gap is exhausted. Re-space only the already-ranked visible sequence;
	// this is rare because each ordinary gap starts with over a million slots.
	ranked = append(ranked, ordered{})
	copy(ranked[insert+1:], ranked[insert:])
	ranked[insert] = moving
	for i := range ranked {
		if err := write(&ranked[i], (i+1)*step); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

func validateAssignment(status domain.Status, assignees []string) error {
	switch {
	case status == domain.StatusOpen && len(assignees) > 0:
		return awberr.Runtimef("refusing to store an inconsistent issue: open issue has assignees")
	case status == domain.StatusInProgress && len(assignees) == 0:
		return awberr.Runtimef("refusing to store an inconsistent issue: in_progress issue has no assignees")
	default:
		return nil
	}
}

// TouchIssue moves updated_at for issue activity that is not a change to a
// column of the issues table: a label or attachment being added or removed,
// or a comment being posted.
func (t *Tx) TouchIssue(issue *domain.Issue) error {
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
	return t.TouchIssue(issue)
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
	return t.TouchIssue(issue)
}

// DeleteIssue removes an issue and, by cascade, its labels and every relation
// it takes part in. It reports how many relations went with it, since removing
// a blocker silently makes other issues ready and orphaning children makes a
// decomposed parent's work top-level.
func (t *Tx) DeleteIssue(id string) (relationsRemoved int, err error) {
	if err := t.bumpBoardViewsSelectingEpic(id); err != nil {
		return 0, err
	}
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
