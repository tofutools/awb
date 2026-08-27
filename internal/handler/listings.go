package handler

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// listOptions says which parameters an endpoint accepts, mirroring the
// per-command rules of the CLI. A parameter an endpoint rejects is a 400,
// exactly as the corresponding flag is a usage error.
type listOptions struct {
	status    bool
	assignee  bool
	relevance bool
	// sortable is false for the facet endpoints, where the row order is fixed at
	// value ascending.
	sortable bool
	// terms is true only for search.
	terms bool
}

// parseFilter turns query parameters into a domain filter. They carry the same
// names as the corresponding CLI filter flags, in the same kebab-case
// spelling, and a repeatable filter is repeated rather than comma-separated.
func parseFilter(r *http.Request, opts listOptions) (*domain.Filter, error) {
	query := r.URL.Query()
	filter := &domain.Filter{}

	if err := rejectUnaccepted(query, opts); err != nil {
		return nil, err
	}

	for _, value := range query["status"] {
		status, err := domain.ParseStatus(value)
		if err != nil {
			return nil, err
		}
		filter.Statuses = append(filter.Statuses, status)
	}
	includeClosed, err := boolParam(query, "include-closed")
	if err != nil {
		return nil, err
	}
	filter.IncludeClosed = includeClosed

	for _, value := range query["type"] {
		issueType, err := domain.ParseType(value)
		if err != nil {
			return nil, err
		}
		filter.Types = append(filter.Types, issueType)
	}
	for _, value := range query["priority"] {
		number, err := strconv.Atoi(value)
		if err != nil {
			return nil, awberr.Usagef("invalid priority %q", value)
		}
		priority, err := domain.ParsePriority(number)
		if err != nil {
			return nil, err
		}
		filter.Priorities = append(filter.Priorities, priority)
	}
	if value := query.Get("priority-max"); value != "" {
		number, err := strconv.Atoi(value)
		if err != nil {
			return nil, awberr.Usagef("invalid priority-max %q", value)
		}
		priority, err := domain.ParsePriority(number)
		if err != nil {
			return nil, err
		}
		filter.PriorityMax = &priority
	}

	for _, value := range query["label"] {
		label, err := domain.ValidateLabel(value)
		if err != nil {
			return nil, err
		}
		filter.Labels = append(filter.Labels, label)
	}
	for _, value := range query["assignee"] {
		assignee, err := domain.ValidateAssignee(value)
		if err != nil {
			return nil, err
		}
		filter.Assignees = append(filter.Assignees, assignee)
	}
	unassigned, err := boolParam(query, "unassigned")
	if err != nil {
		return nil, err
	}
	filter.Unassigned = unassigned
	if len(filter.Assignees) > 0 && filter.Unassigned {
		return nil, awberr.Usagef("assignee and unassigned are mutually exclusive")
	}

	for _, value := range query["project"] {
		key, err := domain.ValidateProjectKey(value)
		if err != nil {
			return nil, err
		}
		filter.Projects = append(filter.Projects, key)
	}
	filter.Parent = query.Get("parent")

	if filter.Limit, err = intParam(query, "limit"); err != nil {
		return nil, err
	}
	if filter.Offset, err = intParam(query, "offset"); err != nil {
		return nil, err
	}

	filter.Sort = domain.DefaultSort
	if opts.relevance {
		filter.Sort = domain.DefaultSearchSort
	}
	if value := query.Get("sort"); value != "" {
		if filter.Sort, err = domain.ParseSort(value, opts.relevance); err != nil {
			return nil, err
		}
	}

	if opts.terms {
		// q is repeated once per positional argument of awb search, each value one
		// literal term that may itself contain spaces. A request with no q is a 400.
		terms := query["q"]
		if len(terms) == 0 {
			return nil, awberr.Usagef("at least one q parameter is required")
		}
		for _, term := range terms {
			valid, err := domain.ValidateSearchTerm(term)
			if err != nil {
				return nil, err
			}
			filter.Terms = append(filter.Terms, valid)
		}
	}

	return filter, nil
}

// rejectUnaccepted reports a parameter the endpoint does not take.
func rejectUnaccepted(query url.Values, opts listOptions) error {
	reject := func(names ...string) error {
		for _, name := range names {
			if query.Has(name) {
				return awberr.Usagef("this endpoint does not accept the %s parameter", name)
			}
		}
		return nil
	}

	if !opts.status {
		if err := reject("status", "include-closed"); err != nil {
			return err
		}
	}
	if !opts.assignee {
		if err := reject("assignee", "unassigned"); err != nil {
			return err
		}
	}
	if !opts.sortable {
		if err := reject("sort"); err != nil {
			return err
		}
	}
	if !opts.terms {
		if err := reject("q"); err != nil {
			return err
		}
	}
	return nil
}

// boolParam reads a boolean parameter, which is written name=true or
// name=false.
func boolParam(query url.Values, name string) (bool, error) {
	value := query.Get(name)
	switch value {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, awberr.Usagef("invalid %s %q: must be true or false", name, value)
	}
}

// intParam reads limit or offset, which must be non-negative integers.
func intParam(query url.Values, name string) (*int, error) {
	value := query.Get(name)
	if value == "" {
		return nil, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return nil, awberr.Usagef("invalid %s %q: must be a non-negative integer", name, value)
	}
	if number < 0 {
		return nil, awberr.Usagef("invalid %s %d: must be a non-negative integer", name, number)
	}
	return &number, nil
}

func (h *Handler) listIssues(w http.ResponseWriter, r *http.Request) {
	h.serveListing(w, r, listOptions{status: true, assignee: true, sortable: true}, nil)
}

// listReady fixes the status set and the assignee filter for itself, and
// therefore rejects status, include-closed, assignee and unassigned.
func (h *Handler) listReady(w http.ResponseWriter, r *http.Request) {
	h.serveListing(w, r, listOptions{sortable: true}, func(f *domain.Filter) {
		f.Statuses = []domain.Status{domain.StatusOpen}
		f.Unassigned = true
		f.Readiness = domain.ReadinessReady
	})
}

// listBlocked fixes the status set to the two that are not closed.
func (h *Handler) listBlocked(w http.ResponseWriter, r *http.Request) {
	h.serveListing(w, r, listOptions{assignee: true, sortable: true}, func(f *domain.Filter) {
		f.Statuses = domain.NotClosedStatuses
		f.Readiness = domain.ReadinessBlocked
	})
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	h.serveListing(w, r,
		listOptions{status: true, assignee: true, relevance: true, sortable: true, terms: true}, nil)
}

func (h *Handler) serveListing(w http.ResponseWriter, r *http.Request, opts listOptions,
	fix func(*domain.Filter)) {
	filter, err := parseFilter(r, opts)
	if err != nil {
		writeError(w, err)
		return
	}
	if fix != nil {
		fix(filter)
	}

	page, err := h.backendFor(r).ListIssues(r.Context(), filter)
	if err != nil {
		writeError(w, err)
		return
	}
	writeList(w, page.Issues, page.Total)
}

// The two facet endpoints honour the selection parameters of GET /api/issues,
// the facet's own included, so ?label=parser lists the labels that co-occur
// with parser and a UI can narrow progressively. sort is not accepted at all,
// the row order being fixed at value ascending.
//
// limit and offset page the facet rows rather than the issues behind them, so
// count is the same whatever page it appears on.
func (h *Handler) labelFacets(w http.ResponseWriter, r *http.Request) {
	be := h.backendFor(r)
	h.serveFacets(w, r, be.LabelFacets)
}

func (h *Handler) assigneeFacets(w http.ResponseWriter, r *http.Request) {
	be := h.backendFor(r)
	h.serveFacets(w, r, be.AssigneeFacets)
}

func (h *Handler) serveFacets(w http.ResponseWriter, r *http.Request,
	query func(context.Context, *domain.Filter) (backend.FacetPage, error)) {
	filter, err := parseFilter(r, listOptions{status: true, assignee: true})
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := query(r.Context(), filter)
	if err != nil {
		writeError(w, err)
		return
	}
	writeList(w, page.Facets, page.Total)
}

func (h *Handler) identity(w http.ResponseWriter, r *http.Request) {
	identity, err := h.backendFor(r).Identity(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, domain.Identity{Identity: identity})
}
