package storage

import (
	"strings"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// conditions accumulates a WHERE clause and its arguments.
type conditions struct {
	clauses []string
	args    []any
}

func (c *conditions) add(clause string, args ...any) {
	c.clauses = append(c.clauses, clause)
	c.args = append(c.args, args...)
}

// addIn adds a repeated filter, whose values are ORed.
func (c *conditions) addIn(column string, values []any) {
	if len(values) == 0 {
		return
	}
	c.add(column+" IN ("+placeholders(len(values))+")", values...)
}

func (c *conditions) where() string {
	if len(c.clauses) == 0 {
		return "1 = 1"
	}
	return strings.Join(c.clauses, " AND ")
}

// selection turns a filter's selection half into SQL. It is deliberately
// separate from the ordering and paging half, because the facet endpoints of
// SPEC §6.2 honour the selection parameters and page the facet rows rather than
// the issues behind them.
//
// Repeated values of one filter are ORed; different filters are ANDed.
func selection(f *domain.Filter) *conditions {
	c := &conditions{}

	c.addIn("i.status", anyArgs(f.EffectiveStatuses()))
	c.addIn("i.type", anyArgs(f.Types))
	c.addIn("i.priority", anyArgs(f.Priorities))
	c.addIn("i.project", anyArgs(f.Projects))

	if f.PriorityMax != nil {
		// Inclusive, and reading as urgency rather than as a number: because 0
		// is the highest priority, a maximum of 1 means P0 and P1.
		c.add("i.priority <= ?", *f.PriorityMax)
	}
	if f.Unassigned {
		c.add("i.assignee = ''")
	} else {
		c.addIn("i.assignee", anyArgs(f.Assignees))
	}
	// The derived blocked state of SPEC §2.2: an issue is blocked when it is
	// itself not closed and at least one issue it is blocked-by is not closed.
	// Expressing it here rather than after the fact keeps ready and blocked
	// pageable, with an accurate unpaged total.
	const blockedBy = `EXISTS (SELECT 1 FROM relations r
	                             JOIN issues b ON b.id = r.other
	                            WHERE r.subject = i.id
	                              AND r.type = 'blocked-by'
	                              AND b.status <> 'closed')`
	switch f.Readiness {
	case domain.ReadinessReady:
		c.add(`i.status <> 'closed' AND NOT ` + blockedBy)
	case domain.ReadinessBlocked:
		c.add(`i.status <> 'closed' AND ` + blockedBy)
	case domain.ReadinessAny:
	}

	if f.Parent != "" {
		c.add(`i.id IN (SELECT subject FROM relations
		                 WHERE type = 'has-parent' AND other = ?)`, f.Parent)
	}
	if len(f.Labels) > 0 {
		// Repeated --label is ORed, like every other repeated filter, so all of
		// the values go into one IN clause rather than one EXISTS each.
		c.add(`i.id IN (SELECT issue FROM issue_labels
		                 WHERE label IN (`+placeholders(len(f.Labels))+`))`,
			anyArgs(f.Labels)...)
	}

	return c
}

// orderBy renders the ordering of SPEC §4.3. Every sort ends with id ascending
// as a final tiebreak, so the order is total and two invocations against
// unchanged data agree. The "-" prefix reverses the named key only: the
// created_at and id tiebreaks stay ascending whatever it says.
func orderBy(sort domain.Sort) string {
	direction := "ASC"
	if sort.Desc {
		direction = "DESC"
	}

	switch sort.Key {
	case domain.SortPriority:
		// priority inserts created_at ascending before the tiebreak — oldest
		// first within a priority — so --sort priority is the default order.
		return " ORDER BY i.priority " + direction + ", i.created_at ASC, i.id ASC"
	case domain.SortCreated:
		return " ORDER BY i.created_at " + direction + ", i.id ASC"
	case domain.SortUpdated:
		return " ORDER BY i.updated_at " + direction + ", i.id ASC"
	case domain.SortID:
		return " ORDER BY i.id " + direction
	case domain.SortRelevance:
		// bm25's better matches are its more negative values, so ascending is
		// best match first, which is what a bare "relevance" means. "-relevance"
		// is worst match first.
		if sort.Desc {
			return " ORDER BY relevance DESC, i.id ASC"
		}
		return " ORDER BY relevance ASC, i.id ASC"
	default:
		return " ORDER BY i.priority ASC, i.created_at ASC, i.id ASC"
	}
}

// ListIssues returns the issues a filter selects, ordered and paged, together
// with the unpaged total for X-Total-Count (SPEC §6.2).
func (t *Tx) ListIssues(f *domain.Filter) (issues []domain.Issue, total int, err error) {
	if len(f.Terms) > 0 {
		return t.searchIssues(f)
	}

	c := selection(f)
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM issues i WHERE `+c.where(), c.args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count issues")
	}

	query := `SELECT ` + issueColumns + ` FROM issues i WHERE ` + c.where() +
		orderBy(f.Sort) + limitOffsetClause(f.Limit, f.Offset)

	issues, err = t.queryIssues(query, c.args)
	return issues, total, err
}

// searchIssues is ListIssues over the full text index.
//
// Each term is wrapped in double quotes, with any double quote inside it
// doubled, before it reaches the query, so no operator, wildcard or column
// prefix is passed through and no user or agent input can produce a query
// syntax error. An issue matches when the title and description together
// contain all of the terms (SPEC §4.3).
func (t *Tx) searchIssues(f *domain.Filter) (issues []domain.Issue, total int, err error) {
	match := ftsQuery(f.Terms)

	c := selection(f)
	c.clauses = append([]string{`i.rowid IN (SELECT rowid FROM issues_fts WHERE issues_fts MATCH ?)`},
		c.clauses...)
	c.args = append([]any{match}, c.args...)

	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM issues i WHERE `+c.where(), c.args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count search results")
	}

	// Relevance is FTS5's bm25 with the title weighted ten times the
	// description, so a term in a title outranks the same term buried in a
	// description. Fixing the function and the weights here is what makes
	// --sort relevance mean one thing rather than whatever a driver happens to
	// do.
	query := `
		SELECT ` + issueColumns + `
		  FROM issues i
		  JOIN (SELECT rowid, bm25(issues_fts, 10.0, 1.0) AS relevance
		          FROM issues_fts WHERE issues_fts MATCH ?) m ON m.rowid = i.rowid
		 WHERE ` + c.where() + orderBy(f.Sort) + limitOffsetClause(f.Limit, f.Offset)

	args := append([]any{match}, c.args...)
	issues, err = t.queryIssues(query, args)
	return issues, total, err
}

// ftsQuery builds an FTS5 MATCH expression from literal terms. Doubling an
// inner double quote is how FTS5 escapes one inside a quoted string, so a term
// containing a quote stays a literal rather than closing the string early.
func ftsQuery(terms []string) string {
	quoted := make([]string, len(terms))
	for i, term := range terms {
		quoted[i] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " ")
}

func (t *Tx) queryIssues(query string, args []any) ([]domain.Issue, error) {
	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list issues")
	}
	defer rows.Close()

	var pointers []*domain.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "list issues")
		}
		pointers = append(pointers, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "list issues")
	}

	if err := t.hydrate(pointers); err != nil {
		return nil, err
	}

	issues := make([]domain.Issue, len(pointers))
	for i, p := range pointers {
		issues[i] = *p
	}
	return issues, nil
}

// LabelFacets returns the distinct labels in use under a filter, with the
// number of matching issues carrying each, sorted by value ascending (SPEC
// §6.2). A value with a count of zero is not listed at all: "in use" means in
// use under the filters in force.
func (t *Tx) LabelFacets(f *domain.Filter) ([]domain.Facet, error) {
	c := selection(f)
	return t.scanFacets(`
		SELECT l.label, count(*)
		  FROM issue_labels l
		  JOIN issues i ON i.id = l.issue
		 WHERE `+c.where()+`
		 GROUP BY l.label
		 ORDER BY l.label ASC`, c.args)
}

// AssigneeFacets is LabelFacets for assignees. There is no row for the empty
// assignee: unassigned is a filter, not a value (SPEC §6.2).
func (t *Tx) AssigneeFacets(f *domain.Filter) ([]domain.Facet, error) {
	c := selection(f)
	c.add("i.assignee <> ''")
	return t.scanFacets(`
		SELECT i.assignee, count(*)
		  FROM issues i
		 WHERE `+c.where()+`
		 GROUP BY i.assignee
		 ORDER BY i.assignee ASC`, c.args)
}

// page applies limit and offset to already-ordered facet rows. The parameters
// that shape a listing rather than select it belong to the facet rows here, not
// to the issues behind them, so count is the same whatever page it appears on.
func page[T any](rows []T, limit, offset *int) []T {
	start := 0
	if offset != nil && *offset > 0 {
		start = min(*offset, len(rows))
	}
	end := len(rows)
	if limit != nil {
		end = min(start+max(*limit, 0), len(rows))
	}
	return rows[start:end]
}

// PageFacets pages facet rows and reports the unpaged total.
func PageFacets(facets []domain.Facet, limit, offset *int) ([]domain.Facet, int) {
	return page(facets, limit, offset), len(facets)
}
