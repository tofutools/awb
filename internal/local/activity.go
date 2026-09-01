package local

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// AddComment appends Markdown prose to an issue as the current caller and
// moves the issue's updated_at in the same transaction.
func (b *Backend) AddComment(ctx context.Context, ref, body string) (*domain.Activity, error) {
	validated, err := domain.ValidateComment(body)
	if err != nil {
		return nil, err
	}
	actor, err := b.Identity(ctx)
	if err != nil {
		return nil, err
	}
	var activity domain.Activity
	err = b.write(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		activity = domain.Activity{
			Issue: issue.ID, Kind: domain.ActivityKindComment,
			Actor: actor, Body: validated,
		}
		if err := tx.InsertActivity(&activity); err != nil {
			return err
		}
		return tx.TouchIssue(issue)
	})
	if err != nil {
		return nil, err
	}
	return &activity, nil
}

// ListActivity reads an issue timeline. Resolving the issue first is both the
// not-found behavior and the authorization check; the activity table is never
// queried without that scoped read.
func (b *Backend) ListActivity(ctx context.Context, ref string, kind domain.ActivityKind,
	limit, offset *int) (backend.ActivityPage, error) {
	if kind != "" && kind != domain.ActivityKindComment && kind != domain.ActivityKindChange {
		return backend.ActivityPage{}, awberr.Usagef("invalid activity kind %q", kind)
	}
	var page backend.ActivityPage
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		issue, err := load(tx, ref)
		if err != nil {
			return err
		}
		page.Activity, page.Total, err = tx.ListActivity(issue.ID, kind, limit, offset)
		if err != nil {
			return err
		}
		if !b.userPreferences {
			return nil
		}
		return redactIgnoredRelationActivity(tx, b.identity, page.Activity)
	})
	if err != nil {
		return backend.ActivityPage{}, err
	}
	return page, nil
}

// redactIgnoredRelationActivity applies today's preference boundary to the
// relation snapshots stored in yesterday's activity. The activity row belongs
// to a visible issue, but either endpoint snapshot may name an issue in a
// project the caller later ignored; returning that immutable JSON verbatim
// would make the hidden connection and ID visible again.
func redactIgnoredRelationActivity(tx *storage.Tx, user string, entries []domain.Activity) error {
	ignored, err := tx.IgnoredProjects(user)
	if err != nil || len(ignored) == 0 {
		return err
	}
	for i := range entries {
		for j := range entries[i].Changes {
			change := &entries[i].Changes[j]
			if change.Field != "relations" {
				continue
			}
			for _, value := range []*json.RawMessage{&change.From, &change.To} {
				var relations []domain.Relation
				if err := json.Unmarshal(*value, &relations); err != nil {
					return awberr.Wrap(awberr.Runtime, err,
						"decode relation activity %d", entries[i].ID)
				}
				kept := relations[:0]
				for _, relation := range relations {
					project, err := tx.IssueProject(relation.Other)
					if err != nil {
						return err
					}
					if project == "" {
						project, _, _ = domain.SplitID(relation.Other)
					}
					if !ignored[project] {
						kept = append(kept, relation)
					}
				}
				relations = kept
				*value = activityJSON(relations)
			}
		}
	}
	return nil
}

// activityChanges compares the stored and separately-mutated parts of an
// issue. The JSON text preserves each value's type while keeping one compact
// schema for every field.
func activityChanges(before, after *domain.Issue) []domain.ActivityChange {
	changes := []domain.ActivityChange{}
	add := func(field string, from, to any) {
		if reflect.DeepEqual(from, to) {
			return
		}
		changes = append(changes, domain.ActivityChange{
			Field: field, From: activityJSON(from), To: activityJSON(to),
		})
	}
	add("title", before.Title, after.Title)
	add("description", before.Description, after.Description)
	add("type", before.Type, after.Type)
	add("project", before.Project, after.Project)
	add("status", before.Status, after.Status)
	add("priority", before.Priority, after.Priority)
	add("order", before.Order, after.Order)
	if !slices.Equal(before.Assignees, after.Assignees) {
		add("assignees", before.Assignees, after.Assignees)
	}
	if !slices.Equal(before.Labels, after.Labels) {
		add("labels", before.Labels, after.Labels)
	}
	if !slices.Equal(before.Relations, after.Relations) {
		add("relations", before.Relations, after.Relations)
	}
	return changes
}

func activityJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		// Every value passed here is a domain scalar or slice and always encodes.
		return json.RawMessage("null")
	}
	return encoded
}

func recordChange(tx *storage.Tx, caller domain.Caller, issue, action string,
	changes []domain.ActivityChange) error {
	return tx.InsertActivity(&domain.Activity{
		Issue: issue, Kind: domain.ActivityKindChange, Actor: caller.Name,
		Action: action, Changes: changes,
	})
}

func recordCloseReason(tx *storage.Tx, caller domain.Caller, issue, body string,
	changes []domain.ActivityChange) error {
	return tx.InsertActivity(&domain.Activity{
		Issue: issue, Kind: domain.ActivityKindComment, Actor: caller.Name,
		Body: body, Action: "closed", Changes: changes,
	})
}
