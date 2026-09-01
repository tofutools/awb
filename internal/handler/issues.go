package handler

import (
	"context"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// issueResponse answers with one issue and the ETag for the version it
// describes, so a client can make another conditional edit without first
// repeating the GET.
func issueResponse(issue *domain.Issue) *api.IssueHeaders {
	return &api.IssueHeaders{
		Etag:     api.NewOptString(backend.ETag(issue.UpdatedAt)),
		Response: toIssue(issue),
	}
}

func (h *Handler) CreateIssue(ctx context.Context, req *api.IssueCreate) (
	*api.IssueCreatedHeaders, error) {
	create := backend.IssueCreate{
		Project:     string(req.Project),
		Title:       req.Title,
		Description: req.Description.Or(""),
		Type:        domain.Type(req.Type.Or("")),
		Priority:    optPriority(req.Priority),
		Assignees:   fromAssignees(req.Assignees),
		Labels:      fromLabels(req.Labels),
	}
	parents := 0
	for _, relation := range req.Relations {
		if domain.RelationType(relation.Type) == domain.RelHasParent {
			parents++
		}
		create.Relations = append(create.Relations, backend.NewRelation{
			Type:  domain.RelationType(relation.Type),
			Other: relation.Other,
		})
	}
	if parents > 1 {
		return nil, awberr.Usagef("an issue has at most one parent")
	}

	issue, err := h.backendFor(ctx).CreateIssue(ctx, create)
	if err != nil {
		return nil, err
	}

	// The two creating operations answer 201 with the new object and a Location
	// header naming it.
	return &api.IssueCreatedHeaders{
		Etag:     api.NewOptString(backend.ETag(issue.UpdatedAt)),
		Location: api.NewOptString("/api/issues/" + issue.ID),
		Response: toIssue(issue),
	}, nil
}

func (h *Handler) GetIssue(ctx context.Context, params api.GetIssueParams) (
	*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).GetIssue(ctx, params.ID)
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

// UpdateIssue carries the fields that may appear but may not change with the
// patch, so that they are compared against what is stored inside the same
// transaction that performs the write. Comparing them here would leave a
// window in which a concurrent transition could make a stale value pass.
//
// The derived and immutable fields the body may also carry are not read at
// all. They are still validated, by the generated decoder, against the schema
// the document declares for each: ignoring a value is not the same as
// accepting anything in its place.
func (h *Handler) UpdateIssue(ctx context.Context, req *api.IssuePatch,
	params api.UpdateIssueParams) (*api.IssueHeaders, error) {
	patch := backend.IssuePatch{
		Title:       optString(req.Title),
		Description: optString(req.Description),
		Type:        optType(req.Type),
		Priority:    optPriority(req.Priority),

		ExpectStatus: optStatus(req.Status),
	}
	if req.Assignees != nil {
		assignees := fromAssignees(req.Assignees)
		patch.ExpectAssignees = &assignees
	}
	// A labels array that is absent is nil; one the caller sent is not, even
	// when it is empty, and is then compared against what is stored.
	if req.Labels != nil {
		labels := fromLabels(req.Labels)
		patch.ExpectLabels = &labels
	}

	issue, err := h.backendFor(ctx).UpdateIssue(ctx, params.ID, patch, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) MoveIssue(ctx context.Context, req *api.IssueMove,
	params api.MoveIssueParams) (*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).MoveIssue(ctx, params.ID, backend.IssueMove{
		Status: domain.Status(req.Status), Epic: optString(req.Epic),
		Before: req.Before.Or(""), After: req.After.Or(""), Direction: string(req.Direction.Or("")),
	}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

// DeleteIssue answers with the object as it was immediately before deletion,
// and carries no ETag: the version it describes is gone.
func (h *Handler) DeleteIssue(ctx context.Context, params api.DeleteIssueParams) (
	*api.Issue, error) {
	deleted, err := h.backendFor(ctx).DeleteIssue(ctx, params.ID, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	issue := toIssue(&deleted.Issue)
	return &issue, nil
}

func (h *Handler) ClaimIssue(ctx context.Context, req api.OptClaimRequest,
	params api.ClaimIssueParams) (*api.IssueHeaders, error) {
	body := req.Or(api.ClaimRequest{})

	// assignee may be omitted, in which case the request's identity is used: the
	// authenticated username, or the server's fixed identity when it
	// authenticates nobody.
	be := h.backendFor(ctx)
	assignee := string(body.Assignee.Or(""))
	if assignee == "" {
		identity, err := be.Identity(ctx)
		if err != nil {
			return nil, err
		}
		assignee = identity
	}

	issue, err := be.Claim(ctx, params.ID, backend.ClaimRequest{
		Assignee: assignee,
		Force:    body.Force.Or(false),
	}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) ReleaseIssue(ctx context.Context, req api.OptReleaseRequest,
	params api.ReleaseIssueParams) (*api.IssueHeaders, error) {
	body := req.Or(api.ReleaseRequest{})

	be := h.backendFor(ctx)
	assignee := string(body.Assignee.Or(""))
	force := body.Force.Or(false)
	if assignee == "" && !force {
		identity, err := be.Identity(ctx)
		if err != nil {
			return nil, err
		}
		assignee = identity
	}

	issue, err := be.Release(ctx, params.ID,
		backend.ReleaseRequest{Assignee: assignee, Force: force}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) CloseIssue(ctx context.Context, req api.OptCloseRequest,
	params api.CloseIssueParams) (*api.IssueHeaders, error) {
	body := req.Or(api.CloseRequest{})
	issue, err := h.backendFor(ctx).CloseIssue(ctx, params.ID,
		backend.CloseRequest{Reason: optString(body.Reason)}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) ReopenIssue(ctx context.Context, params api.ReopenIssueParams) (
	*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).Reopen(ctx, params.ID, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) AddLabel(ctx context.Context, req *api.LabelRequest,
	params api.AddLabelParams) (*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).AddLabel(ctx, params.ID, string(req.Label),
		params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) RemoveLabel(ctx context.Context, params api.RemoveLabelParams) (
	*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).RemoveLabel(ctx, params.ID, string(params.Label),
		params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) AddRelation(ctx context.Context, req *api.RelationRequest,
	params api.AddRelationParams) (*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).AddRelation(ctx, params.ID, backend.RelationRequest{
		Type:  domain.RelationType(req.Type),
		Other: req.Other,
		Force: req.Force.Or(false),
	}, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

func (h *Handler) RemoveRelation(ctx context.Context, params api.RemoveRelationParams) (
	*api.IssueHeaders, error) {
	issue, err := h.backendFor(ctx).RemoveRelation(ctx, params.ID,
		domain.RelationType(params.Type), params.Other, params.IfMatch.Or(""))
	if err != nil {
		return nil, err
	}
	return issueResponse(issue), nil
}

// GetIssueTree returns the subtree whole; it is not paged, and carries no
// ETag, because an IssueTree aggregates many issues and no one version tags it.
func (h *Handler) GetIssueTree(ctx context.Context, params api.GetIssueTreeParams) (
	*api.IssueTree, error) {
	tree, err := h.backendFor(ctx).Tree(ctx, params.ID)
	if err != nil {
		return nil, err
	}
	converted := toTree(tree)
	return &converted, nil
}
