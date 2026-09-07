package storage

import (
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// issueSummaryColumns is deliberately narrower than issueColumns: collection
// views do not read descriptions, integration metadata, ordering, or lifecycle
// timestamps they never return.
const issueSummaryColumns = `i.id, i.workspace, i.title, i.type, i.status, i.priority, i.updated_at`

func scanIssueSummary(row rowScanner) (*domain.IssueSummary, error) {
	var issue domain.IssueSummary
	err := row.Scan(&issue.ID, &issue.Workspace, &issue.Title, &issue.Type,
		&issue.Status, &issue.Priority, &issue.UpdatedAt)
	return &issue, err
}

// ListIssueSummaries is ListIssues' selection, ordering and paging with the
// lightweight representation used by collection views.
func (t *Tx) ListIssueSummaries(f *domain.Filter) (issues []domain.IssueSummary, total int, err error) {
	match := ""
	c := t.selection(f)
	if len(f.Terms) > 0 {
		match = ftsQuery(f.Terms)
		c.clauses = append([]string{`i.rowid IN (SELECT rowid FROM issues_fts WHERE issues_fts MATCH ?)`}, c.clauses...)
		c.args = append([]any{match}, c.args...)
	}
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM issues i WHERE `+c.where(), c.args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count issue summaries")
	}

	query := `SELECT ` + issueSummaryColumns + ` FROM issues i WHERE ` + c.where()
	args := c.args
	if match != "" {
		c = t.selection(f)
		query = `SELECT ` + issueSummaryColumns + `
			FROM issues i
			JOIN (SELECT rowid, bm25(issues_fts, 10.0, 1.0) AS relevance
			        FROM issues_fts WHERE issues_fts MATCH ?) m ON m.rowid = i.rowid
			WHERE ` + c.where()
		args = append([]any{match}, c.args...)
	}
	issues, err = t.queryIssueSummaries(query+orderBy(f.Sort)+limitOffsetClause(f.Limit, f.Offset), args)
	return issues, total, err
}

func (t *Tx) queryIssueSummaries(query string, args []any) ([]domain.IssueSummary, error) {
	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list issue summaries")
	}
	defer rows.Close()

	var pointers []*domain.IssueSummary
	for rows.Next() {
		issue, err := scanIssueSummary(rows)
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "list issue summaries")
		}
		pointers = append(pointers, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list issue summaries")
	}
	if err := t.hydrateIssueSummaries(pointers); err != nil {
		return nil, err
	}

	issues := make([]domain.IssueSummary, len(pointers))
	for i, issue := range pointers {
		issues[i] = *issue
	}
	return issues, nil
}

func (t *Tx) hydrateIssueSummaries(issues []*domain.IssueSummary) error {
	if len(issues) == 0 {
		return nil
	}
	byID := make(map[string]*domain.IssueSummary, len(issues))
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
		ids = append(ids, issue.ID)
	}
	if err := t.loadSummaryAssignees(ids, byID); err != nil {
		return err
	}
	if err := t.loadSummaryLabels(ids, byID); err != nil {
		return err
	}
	if err := t.loadSummaryParents(ids, byID); err != nil {
		return err
	}
	if err := t.loadSummaryBlockers(ids, byID); err != nil {
		return err
	}
	for _, issue := range issues {
		issue.Normalize()
	}
	return nil
}

func (t *Tx) loadSummaryAssignees(ids []string, byID map[string]*domain.IssueSummary) error {
	rows, err := t.q.QueryContext(t.ctx, `SELECT issue, assignee FROM issue_assignees
		WHERE issue IN (`+placeholders(len(ids))+`) ORDER BY issue, position`, anyArgs(ids)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read issue summary assignees")
	}
	defer rows.Close()
	for rows.Next() {
		var id, assignee string
		if err := rows.Scan(&id, &assignee); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read issue summary assignees")
		}
		byID[id].Assignees = append(byID[id].Assignees, assignee)
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read issue summary assignees")
}

func (t *Tx) loadSummaryLabels(ids []string, byID map[string]*domain.IssueSummary) error {
	rows, err := t.q.QueryContext(t.ctx, `SELECT issue, label FROM issue_labels
		WHERE issue IN (`+placeholders(len(ids))+`)`, anyArgs(ids)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read issue summary labels")
	}
	defer rows.Close()
	for rows.Next() {
		var id, label string
		if err := rows.Scan(&id, &label); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read issue summary labels")
		}
		byID[id].Labels = append(byID[id].Labels, label)
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read issue summary labels")
}

func (t *Tx) loadSummaryParents(ids []string, byID map[string]*domain.IssueSummary) error {
	visibleTitle, visibleArgs := t.visibleClause("parent.workspace")
	notIgnored, ignoredArgs := t.notIgnoredClause("parent.workspace")
	args := append(visibleArgs, anyArgs(ids)...)
	args = append(args, ignoredArgs...)
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT r.subject, r.other,
		       CASE WHEN `+visibleTitle+` THEN parent.title ELSE '' END
		  FROM relations r JOIN issues parent ON parent.id = r.other
		 WHERE r.subject IN (`+placeholders(len(ids))+`)
		   AND r.type = 'has-parent' AND `+notIgnored, args...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read issue summary parents")
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, title string
		if err := rows.Scan(&id, &parent, &title); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read issue summary parents")
		}
		if issue := byID[id]; issue != nil {
			issue.Parent = parent
			issue.ParentTitle = title
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read issue summary parents")
}

func (t *Tx) loadSummaryBlockers(ids []string, byID map[string]*domain.IssueSummary) error {
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
		return awberr.Wrap(awberr.Runtime, err, "read issue summary blockers")
	}
	defer rows.Close()
	for rows.Next() {
		var id, blocker string
		var showName bool
		if err := rows.Scan(&id, &blocker, &showName); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read issue summary blockers")
		}
		if issue := byID[id]; issue != nil {
			if showName {
				issue.Blockers = append(issue.Blockers, blocker)
			}
			issue.Blocked = true
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read issue summary blockers")
}
