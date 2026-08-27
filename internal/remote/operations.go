package remote

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// The API's request bodies. They are declared here rather than reused from the
// backend package because the wire names are part of the API contract and must
// not follow a Go field rename.
//
// Every field turns on presence or absence, and null is neither: a description
// or a close reason is cleared with "" and left alone by omission.

type issueCreateBody struct {
	Project     string         `json:"project"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Type        domain.Type    `json:"type,omitempty"`
	Priority    *int           `json:"priority,omitempty"`
	Assignee    string         `json:"assignee,omitempty"`
	Labels      []string       `json:"labels,omitempty"`
	Relations   []relationBody `json:"relations,omitempty"`
}

type relationBody struct {
	Type  domain.RelationType `json:"type"`
	Other string              `json:"other"`
	Force bool                `json:"force,omitempty"`
}

type issuePatchBody struct {
	Title       *string      `json:"title,omitempty"`
	Description *string      `json:"description,omitempty"`
	Type        *domain.Type `json:"type,omitempty"`
	Priority    *int         `json:"priority,omitempty"`
}

type claimBody struct {
	Assignee       string  `json:"assignee,omitempty"`
	ExpectAssignee *string `json:"expect_assignee,omitempty"`
	Force          bool    `json:"force,omitempty"`
}

type releaseBody struct {
	Assignee string `json:"assignee,omitempty"`
	Force    bool   `json:"force,omitempty"`
}

type closeBody struct {
	Reason *string `json:"reason,omitempty"`
}

type labelBody struct {
	Label string `json:"label"`
}

type projectCreateBody struct {
	Key         string `json:"key"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type projectPatchBody struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type identityBody struct {
	Identity string `json:"identity"`
}

func (b *Backend) CreateIssue(ctx context.Context, req backend.IssueCreate) (*domain.Issue, error) {
	body := issueCreateBody{
		Project:     req.Project,
		Title:       req.Title,
		Description: req.Description,
		Type:        req.Type,
		Priority:    req.Priority,
		Assignee:    req.Assignee,
		Labels:      req.Labels,
	}
	for _, rel := range req.Relations {
		body.Relations = append(body.Relations, relationBody{Type: rel.Type, Other: rel.Other})
	}

	var issue domain.Issue
	_, err := b.call(ctx, http.MethodPost, b.endpoint("/api/issues", nil), body, "", &issue)
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func (b *Backend) GetIssue(ctx context.Context, ref string) (*domain.Issue, error) {
	var issue domain.Issue
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/issues/"+url.PathEscape(ref), nil),
		nil, "", &issue)
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// ListIssues sends the filter as query parameters, which carry the same names
// as the corresponding CLI flags in the same kebab-case spelling.
//
// ready and blocked are their own endpoints, so the readiness selector picks
// the path rather than becoming a parameter.
func (b *Backend) ListIssues(ctx context.Context, filter *domain.Filter) (backend.IssuePage, error) {
	path := "/api/issues"
	switch filter.Readiness {
	case domain.ReadinessReady:
		path = "/api/ready"
	case domain.ReadinessBlocked:
		path = "/api/blocked"
	case domain.ReadinessAny:
		if len(filter.Terms) > 0 {
			path = "/api/search"
		}
	}

	issues := []domain.Issue{}
	header, err := b.call(ctx, http.MethodGet, b.endpoint(path, filterQuery(filter, path)),
		nil, "", &issues)
	if err != nil {
		return backend.IssuePage{}, err
	}
	return backend.IssuePage{Issues: issues, Total: totalCount(header, len(issues))}, nil
}

// filterQuery renders a filter as query parameters. A repeatable filter is
// repeated rather than comma-separated, exactly as on the command line.
//
// The endpoints that fix a status set or an assignee filter for themselves
// reject those parameters, so they are not sent to them.
func filterQuery(filter *domain.Filter, path string) url.Values {
	query := url.Values{}

	fixesStatus := path == "/api/ready" || path == "/api/blocked"
	fixesAssignee := path == "/api/ready"

	if !fixesStatus {
		for _, status := range filter.Statuses {
			query.Add("status", string(status))
		}
		if filter.IncludeClosed {
			query.Set("include-closed", "true")
		}
	}
	for _, t := range filter.Types {
		query.Add("type", string(t))
	}
	for _, p := range filter.Priorities {
		query.Add("priority", strconv.Itoa(p))
	}
	if filter.PriorityMax != nil {
		query.Set("priority-max", strconv.Itoa(*filter.PriorityMax))
	}
	for _, label := range filter.Labels {
		query.Add("label", label)
	}
	if !fixesAssignee {
		// --mine never reaches the server: it is sent as assignee=<identity>.
		for _, assignee := range filter.Assignees {
			query.Add("assignee", assignee)
		}
		if filter.Unassigned {
			query.Set("unassigned", "true")
		}
	}
	for _, project := range filter.Projects {
		query.Add("project", project)
	}
	if filter.Parent != "" {
		query.Set("parent", filter.Parent)
	}
	if filter.Limit != nil {
		query.Set("limit", strconv.Itoa(*filter.Limit))
	}
	if filter.Offset != nil {
		query.Set("offset", strconv.Itoa(*filter.Offset))
	}
	if filter.Sort.Key != "" {
		value := string(filter.Sort.Key)
		if filter.Sort.Desc {
			value = "-" + value
		}
		query.Set("sort", value)
	}
	// Each term is one q, and a value may itself contain spaces.
	for _, term := range filter.Terms {
		query.Add("q", term)
	}
	return query
}

func (b *Backend) UpdateIssue(ctx context.Context, ref string, req backend.IssuePatch,
	ifMatch string) (*domain.Issue, error) {
	body := issuePatchBody{
		Title:       req.Title,
		Description: req.Description,
		Type:        req.Type,
		Priority:    req.Priority,
	}
	return b.issueCall(ctx, http.MethodPatch, "/api/issues/"+url.PathEscape(ref), body, ifMatch)
}

func (b *Backend) DeleteIssue(ctx context.Context, ref, ifMatch string) (*backend.DeletedIssue, error) {
	issue, err := b.issueCall(ctx, http.MethodDelete, "/api/issues/"+url.PathEscape(ref), nil, ifMatch)
	if err != nil {
		return nil, err
	}
	// The server does not report a relation count; the deleted object carries the
	// relations that went with it, which is what --json prints.
	return &backend.DeletedIssue{Issue: *issue, RelationsRemoved: len(issue.Relations)}, nil
}

func (b *Backend) Claim(ctx context.Context, ref string, req backend.ClaimRequest,
	ifMatch string) (*domain.Issue, error) {
	body := claimBody{Assignee: req.Assignee, ExpectAssignee: req.ExpectAssignee, Force: req.Force}
	return b.issueCall(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/claim", body, ifMatch)
}

func (b *Backend) Release(ctx context.Context, ref string, req backend.ReleaseRequest,
	ifMatch string) (*domain.Issue, error) {
	body := releaseBody{Assignee: req.Assignee, Force: req.Force}
	return b.issueCall(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/release", body, ifMatch)
}

func (b *Backend) CloseIssue(ctx context.Context, ref string, req backend.CloseRequest,
	ifMatch string) (*domain.Issue, error) {
	return b.issueCall(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/close",
		closeBody{Reason: req.Reason}, ifMatch)
}

func (b *Backend) Reopen(ctx context.Context, ref, ifMatch string) (*domain.Issue, error) {
	return b.issueCall(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/reopen", nil, ifMatch)
}

func (b *Backend) AddLabel(ctx context.Context, ref, label, ifMatch string) (*domain.Issue, error) {
	return b.issueCall(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/labels",
		labelBody{Label: label}, ifMatch)
}

// RemoveLabel sends the label as a query parameter rather than a path segment,
// because a label may contain a slash.
func (b *Backend) RemoveLabel(ctx context.Context, ref, label, ifMatch string) (*domain.Issue, error) {
	target := b.endpoint("/api/issues/"+url.PathEscape(ref)+"/labels", url.Values{"label": {label}})
	var issue domain.Issue
	if _, err := b.call(ctx, http.MethodDelete, target, nil, ifMatch, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

func (b *Backend) AddRelation(ctx context.Context, ref string, req backend.RelationRequest,
	ifMatch string) (*domain.Issue, error) {
	body := relationBody{Type: req.Type, Other: req.Other, Force: req.Force}
	return b.issueCall(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/relations", body, ifMatch)
}

func (b *Backend) RemoveRelation(ctx context.Context, ref string, relType domain.RelationType,
	other string, ifMatch string) (*domain.Issue, error) {
	path := "/api/issues/" + url.PathEscape(ref) + "/relations/" +
		url.PathEscape(string(relType)) + "/" + url.PathEscape(other)
	return b.issueCall(ctx, http.MethodDelete, path, nil, ifMatch)
}

func (b *Backend) Tree(ctx context.Context, ref string) (*domain.IssueTree, error) {
	var tree domain.IssueTree
	_, err := b.call(ctx, http.MethodGet,
		b.endpoint("/api/issues/"+url.PathEscape(ref)+"/tree", nil), nil, "", &tree)
	if err != nil {
		return nil, err
	}
	return &tree, nil
}

func (b *Backend) CreateProject(ctx context.Context, req backend.ProjectCreate) (*domain.Project, error) {
	body := projectCreateBody{Key: req.Key, Name: req.Name, Description: req.Description}
	var project domain.Project
	_, err := b.call(ctx, http.MethodPost, b.endpoint("/api/projects", nil), body, "", &project)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (b *Backend) GetProject(ctx context.Context, key string) (*domain.Project, error) {
	var project domain.Project
	_, err := b.call(ctx, http.MethodGet, b.endpoint("/api/projects/"+url.PathEscape(key), nil),
		nil, "", &project)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (b *Backend) ListProjects(ctx context.Context, limit, offset *int) (backend.ProjectPage, error) {
	query := url.Values{}
	if limit != nil {
		query.Set("limit", strconv.Itoa(*limit))
	}
	if offset != nil {
		query.Set("offset", strconv.Itoa(*offset))
	}

	projects := []domain.Project{}
	header, err := b.call(ctx, http.MethodGet, b.endpoint("/api/projects", query), nil, "", &projects)
	if err != nil {
		return backend.ProjectPage{}, err
	}
	return backend.ProjectPage{Projects: projects, Total: totalCount(header, len(projects))}, nil
}

func (b *Backend) UpdateProject(ctx context.Context, key string, req backend.ProjectPatch,
	ifMatch string) (*domain.Project, error) {
	body := projectPatchBody{Name: req.Name, Description: req.Description}
	var project domain.Project
	_, err := b.call(ctx, http.MethodPatch, b.endpoint("/api/projects/"+url.PathEscape(key), nil),
		body, ifMatch, &project)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// DeleteProject sends --cascade as a boolean query parameter. There is no
// force parameter anywhere: the HTTP method is the confirmation that --force
// supplies on the command line.
func (b *Backend) DeleteProject(ctx context.Context, key string, cascade bool,
	ifMatch string) (*backend.DeletedProject, error) {
	query := url.Values{}
	if cascade {
		query.Set("cascade", "true")
	}

	var project domain.Project
	_, err := b.call(ctx, http.MethodDelete,
		b.endpoint("/api/projects/"+url.PathEscape(key), query), nil, ifMatch, &project)
	if err != nil {
		return nil, err
	}
	// The count of deleted issues is not on the wire; the project's own
	// active-issue count is the closest thing the response carries.
	return &backend.DeletedProject{Project: project, IssuesRemoved: project.ActiveIssues}, nil
}

func (b *Backend) LabelFacets(ctx context.Context, filter *domain.Filter) (backend.FacetPage, error) {
	return b.facets(ctx, "/api/labels", filter)
}

func (b *Backend) AssigneeFacets(ctx context.Context, filter *domain.Filter) (backend.FacetPage, error) {
	return b.facets(ctx, "/api/assignees", filter)
}

func (b *Backend) facets(ctx context.Context, path string, filter *domain.Filter) (backend.FacetPage, error) {
	query := filterQuery(filter, path)
	// sort is not accepted on a facet endpoint, the row order being fixed at
	// value ascending.
	query.Del("sort")

	facets := []domain.Facet{}
	header, err := b.call(ctx, http.MethodGet, b.endpoint(path, query), nil, "", &facets)
	if err != nil {
		return backend.FacetPage{}, err
	}
	return backend.FacetPage{Facets: facets, Total: totalCount(header, len(facets))}, nil
}

// ServerIdentity asks the server who it thinks the caller is. The CLI does not
// use it — it resolves its own identity locally — but it is what a browser UI
// has no other way to learn.
func (b *Backend) ServerIdentity(ctx context.Context) (string, error) {
	var body identityBody
	if _, err := b.call(ctx, http.MethodGet, b.endpoint("/api/identity", nil), nil, "", &body); err != nil {
		return "", err
	}
	return body.Identity, nil
}

func (b *Backend) issueCall(ctx context.Context, method, path string, body any,
	ifMatch string) (*domain.Issue, error) {
	var issue domain.Issue
	if _, err := b.call(ctx, method, b.endpoint(path, nil), body, ifMatch, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

var _ backend.Backend = (*Backend)(nil)
