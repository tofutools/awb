package handler

import (
	"context"

	"github.com/go-faster/jx"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/domain"
)

func (h *Handler) AddComment(ctx context.Context, req *api.CommentCreate,
	params api.AddCommentParams) (*api.Activity, error) {
	activity, err := h.backendFor(ctx).AddComment(ctx, params.ID, req.Body)
	if err != nil {
		return nil, err
	}
	response := toActivity(activity)
	return &response, nil
}

func (h *Handler) ListIssueActivity(ctx context.Context, params api.ListIssueActivityParams) (
	*api.ActivityListHeaders, error) {
	kind := domain.ActivityKind("")
	if value, ok := params.Kind.Get(); ok {
		kind = domain.ActivityKind(value)
	}
	page, err := h.backendFor(ctx).ListActivity(ctx, params.ID, kind,
		optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	return &api.ActivityListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toActivityList(page.Activity),
	}, nil
}

func toActivity(entry *domain.Activity) api.Activity {
	changes := make([]api.ActivityChange, len(entry.Changes))
	for i, change := range entry.Changes {
		changes[i] = api.ActivityChange{
			Field: change.Field, From: jx.Raw(change.From), To: jx.Raw(change.To),
		}
	}
	return api.Activity{
		ID: entry.ID, Issue: entry.Issue, Kind: api.ActivityKind(entry.Kind),
		Actor: entry.Actor, Body: entry.Body, Action: entry.Action,
		Changes: changes, CreatedAt: api.Timestamp(entry.CreatedAt),
	}
}

func toActivityList(entries []domain.Activity) []api.Activity {
	out := make([]api.Activity, len(entries))
	for i := range entries {
		out[i] = toActivity(&entries[i])
	}
	return out
}
