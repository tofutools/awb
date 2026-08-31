package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// selection is every filtering parameter a listing operation can declare,
// gathered from the parameters of whichever one is being served. Each
// operation declares its own subset — /api/ready fixes the status set and the
// assignee filter for itself, the facet operations fix the row order — and the
// generated code gives each its own parameters type, so this is where the six
// of them meet and become one filter.
//
// A parameter an operation does not declare cannot appear here at all: the
// request is refused before it arrives (see Handler.Middleware).
type selection struct {
	statuses      []api.Status
	includeClosed bool
	types         []api.Type
	priorities    []api.Priority
	priorityMax   api.OptPriority
	labels        []api.Label
	assignees     []api.Assignee
	unassigned    bool
	projects      []api.ProjectKey
	parent        api.OptString
	sort          string
	limit         api.OptInt
	offset        api.OptInt
	terms         []string
	listingFilter string
}

// filter turns the selection into a domain filter. The values arrive already
// checked against the vocabulary the document publishes, so what is left is
// the one rule a schema cannot state — that assignee and unassigned exclude
// each other — and the rules that live in the domain layer.
func (s selection) filter(relevance bool) (*domain.Filter, error) {
	filter := &domain.Filter{
		IncludeClosed: s.includeClosed,
		Unassigned:    s.unassigned,
		Parent:        s.parent.Or(""),
		ListingFilter: s.listingFilter,
	}

	for _, status := range s.statuses {
		filter.Statuses = append(filter.Statuses, domain.Status(status))
	}
	for _, issueType := range s.types {
		filter.Types = append(filter.Types, domain.Type(issueType))
	}
	for _, priority := range s.priorities {
		filter.Priorities = append(filter.Priorities, int(priority))
	}
	if priority, ok := s.priorityMax.Get(); ok {
		number := int(priority)
		filter.PriorityMax = &number
	}
	for _, label := range s.labels {
		valid, err := domain.ValidateLabel(string(label))
		if err != nil {
			return nil, err
		}
		filter.Labels = append(filter.Labels, valid)
	}
	for _, assignee := range s.assignees {
		valid, err := domain.ValidateAssignee(string(assignee))
		if err != nil {
			return nil, err
		}
		filter.Assignees = append(filter.Assignees, valid)
	}
	if len(filter.Assignees) > 0 && filter.Unassigned {
		return nil, awberr.Usagef("assignee and unassigned are mutually exclusive")
	}
	for _, project := range s.projects {
		valid, err := domain.ValidateProjectKey(string(project))
		if err != nil {
			return nil, err
		}
		filter.Projects = append(filter.Projects, valid)
	}

	if limit, ok := s.limit.Get(); ok {
		filter.Limit = &limit
	}
	if offset, ok := s.offset.Get(); ok {
		filter.Offset = &offset
	}

	filter.Sort = domain.DefaultSort
	if relevance {
		filter.Sort = domain.DefaultSearchSort
	}
	if s.sort != "" {
		var err error
		if filter.Sort, err = domain.ParseSort(s.sort, relevance); err != nil {
			return nil, err
		}
	}

	// Each q is one literal term that may itself contain spaces. A term the
	// tokenizer reduces to nothing is a 400, as a request with no q at all is.
	for _, term := range s.terms {
		valid, err := domain.ValidateSearchTerm(term)
		if err != nil {
			return nil, err
		}
		filter.Terms = append(filter.Terms, valid)
	}

	return filter, nil
}

func (h *Handler) ListIssues(ctx context.Context, params api.ListIssuesParams) (
	*api.IssueListHeaders, error) {
	filter, err := selection{
		statuses:      params.Status,
		includeClosed: params.IncludeClosed.Or(false),
		types:         params.Type,
		priorities:    params.Priority,
		priorityMax:   params.PriorityMax,
		labels:        params.Label,
		assignees:     params.Assignee,
		unassigned:    params.Unassigned.Or(false),
		projects:      params.Project,
		parent:        params.Parent,
		sort:          string(params.Sort.Or("")),
		limit:         params.Limit,
		offset:        params.Offset,
		listingFilter: params.Filter.Or(""),
	}.filter(false)
	if err != nil {
		return nil, err
	}
	return h.listIssues(ctx, filter)
}

// ListReady lists the issues that are open and not blocked, and only the
// unassigned ones: it fixes the status set and the assignee filter for itself,
// which is why it declares neither.
func (h *Handler) ListReady(ctx context.Context, params api.ListReadyParams) (
	*api.IssueListHeaders, error) {
	filter, err := selection{
		types:         params.Type,
		priorities:    params.Priority,
		priorityMax:   params.PriorityMax,
		labels:        params.Label,
		projects:      params.Project,
		parent:        params.Parent,
		sort:          string(params.Sort.Or("")),
		limit:         params.Limit,
		offset:        params.Offset,
		listingFilter: params.Filter.Or(""),
	}.filter(false)
	if err != nil {
		return nil, err
	}
	filter.Statuses = []domain.Status{domain.StatusOpen}
	filter.Unassigned = true
	filter.Readiness = domain.ReadinessReady
	return h.listIssues(ctx, filter)
}

// ListBlocked fixes the status set to the two that are not closed.
func (h *Handler) ListBlocked(ctx context.Context, params api.ListBlockedParams) (
	*api.IssueListHeaders, error) {
	filter, err := selection{
		types:         params.Type,
		priorities:    params.Priority,
		priorityMax:   params.PriorityMax,
		labels:        params.Label,
		assignees:     params.Assignee,
		unassigned:    params.Unassigned.Or(false),
		projects:      params.Project,
		parent:        params.Parent,
		sort:          string(params.Sort.Or("")),
		limit:         params.Limit,
		offset:        params.Offset,
		listingFilter: params.Filter.Or(""),
	}.filter(false)
	if err != nil {
		return nil, err
	}
	filter.Statuses = domain.NotClosedStatuses
	filter.Readiness = domain.ReadinessBlocked
	return h.listIssues(ctx, filter)
}

func (h *Handler) SearchIssues(ctx context.Context, params api.SearchIssuesParams) (
	*api.IssueListHeaders, error) {
	filter, err := selection{
		statuses:      params.Status,
		includeClosed: params.IncludeClosed.Or(false),
		types:         params.Type,
		priorities:    params.Priority,
		priorityMax:   params.PriorityMax,
		labels:        params.Label,
		assignees:     params.Assignee,
		unassigned:    params.Unassigned.Or(false),
		projects:      params.Project,
		parent:        params.Parent,
		sort:          string(params.Sort.Or("")),
		limit:         params.Limit,
		offset:        params.Offset,
		terms:         params.Q,
		listingFilter: params.Filter.Or(""),
	}.filter(true)
	if err != nil {
		return nil, err
	}
	return h.listIssues(ctx, filter)
}

func (h *Handler) SuggestIssues(ctx context.Context, params api.SuggestIssuesParams) (
	*api.IssueListHeaders, error) {
	query, err := domain.ValidateSearchTerm(params.Q)
	if err != nil {
		return nil, err
	}
	page, err := h.backendFor(ctx).SuggestIssues(ctx, query, optInt(params.Limit))
	if err != nil {
		return nil, err
	}
	return &api.IssueListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toIssues(page.Issues),
	}, nil
}

// listIssues answers with the matching issues and the unpaged total that
// X-Total-Count carries, so a UI can show "1–50 of 214" and page through
// without loading everything.
func (h *Handler) listIssues(ctx context.Context, filter *domain.Filter) (
	*api.IssueListHeaders, error) {
	page, err := h.backendFor(ctx).ListIssues(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &api.IssueListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toIssues(page.Issues),
	}, nil
}

// The two facet operations honour the selection parameters of GET /api/issues
// and optional search terms, the facet's own included, so ?label=parser lists
// the labels that co-occur with parser and a UI can narrow progressively.
//
// limit and offset page the facet rows rather than the issues behind them, so
// count is the same whatever page it appears on, and neither declares sort:
// the row order is fixed at value ascending.

func (h *Handler) ListLabels(ctx context.Context, params api.ListLabelsParams) (
	*api.FacetListHeaders, error) {
	filter, err := selection{
		terms:         params.Q,
		statuses:      params.Status,
		includeClosed: params.IncludeClosed.Or(false),
		types:         params.Type,
		priorities:    params.Priority,
		priorityMax:   params.PriorityMax,
		labels:        params.Label,
		assignees:     params.Assignee,
		unassigned:    params.Unassigned.Or(false),
		projects:      params.Project,
		parent:        params.Parent,
		limit:         params.Limit,
		offset:        params.Offset,
		listingFilter: params.Filter.Or(""),
	}.filter(false)
	if err != nil {
		return nil, err
	}
	return facets(ctx, filter, h.backendFor(ctx).LabelFacets)
}

func (h *Handler) ListAssignees(ctx context.Context, params api.ListAssigneesParams) (
	*api.FacetListHeaders, error) {
	filter, err := selection{
		terms:         params.Q,
		statuses:      params.Status,
		includeClosed: params.IncludeClosed.Or(false),
		types:         params.Type,
		priorities:    params.Priority,
		priorityMax:   params.PriorityMax,
		labels:        params.Label,
		assignees:     params.Assignee,
		unassigned:    params.Unassigned.Or(false),
		projects:      params.Project,
		parent:        params.Parent,
		limit:         params.Limit,
		offset:        params.Offset,
		listingFilter: params.Filter.Or(""),
	}.filter(false)
	if err != nil {
		return nil, err
	}
	return facets(ctx, filter, h.backendFor(ctx).AssigneeFacets)
}

func facets(ctx context.Context, filter *domain.Filter,
	query func(context.Context, *domain.Filter) (backend.FacetPage, error)) (
	*api.FacetListHeaders, error) {
	page, err := query(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &api.FacetListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toFacets(page.Facets),
	}, nil
}

func (h *Handler) GetIdentity(ctx context.Context) (*api.Identity, error) {
	identity, err := h.backendFor(ctx).Identity(ctx)
	if err != nil {
		return nil, err
	}
	return &api.Identity{Identity: identity}, nil
}
