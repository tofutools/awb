package handler

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

// The document says IssueTree is allOf Issue, and ogen flattens that into a
// struct repeating every Issue field rather than embedding one, so toTree has
// to copy a node across field by field. A field left out of that list is
// neither a compile error nor a decoding error, only a zero value on the wire:
// that is how order and closed_at were once dropped from every node while the
// response stayed schema-valid.
//
// This is what makes the list unforgettable rather than merely correct today.
// The fixture fills every field of an issue, the first check refuses one it
// left empty, and the second refuses one toTree did not carry, so a field
// added to Issue fails here until it is both exercised and copied.
func TestToTreeCarriesEveryIssueField(t *testing.T) {
	issue := domain.Issue{
		ID:             "awb-000001",
		Workspace:      "awb",
		Title:          "Everything",
		Description:    "A description.",
		CommitHash:     "01234567",
		PullRequestURL: "https://example.com/pull/1",
		Type:           domain.TypeBug,
		Status:         domain.StatusClosed,
		Priority:       1,
		Order:          1 << 20,
		Labels:         []string{"parser"},
		Assignees:      []string{"alice"},
		CreatedAt:      "2026-09-04T18:20:09.001Z",
		UpdatedAt:      "2026-09-04T18:20:09.002Z",
		ClosedAt:       "2026-09-04T18:20:09.003Z",
		Blocked:        true,
		Blockers:       []string{"awb-000002"},
		Relations: []domain.Relation{
			{Type: domain.RelBlockedBy, Other: "awb-000002", Direction: domain.DirectionOut},
			{Type: domain.RelHasParent, Other: "awb-000003", Direction: domain.DirectionOut},
		},
		Parent: "awb-000003",
		Links:  []domain.Link{{Text: "docs", URL: "https://example.com/d"}},
		Attachments: []domain.Attachment{{
			Issue: "awb-000001", Name: "shot.png", ContentType: "image/png",
			Size: 4, Sha256: "abc", CreatedAt: "2026-09-04T18:20:09.004Z",
		}},
	}

	converted := reflect.ValueOf(toIssue(&issue))
	node := reflect.ValueOf(toTree(&domain.IssueTree{Issue: issue}))

	for i := range converted.NumField() {
		name := converted.Type().Field(i).Name
		field := converted.Field(i)
		require.True(t, filled(field),
			"%s is empty: the fixture has to exercise every field of Issue", name)

		carried := node.FieldByName(name)
		require.True(t, carried.IsValid(), "api.IssueTree has no %s", name)
		assert.Equal(t, field.Interface(), carried.Interface(), "toTree drops %s", name)
	}
}

// filled reports whether a converted field holds something, treating an empty
// string or slice as nothing: a conversion returns an empty slice rather than
// a nil one, which reflect.Value.IsZero alone would accept as exercised.
func filled(v reflect.Value) bool {
	switch v.Kind() { //nolint:exhaustive // every other kind is covered by IsZero
	case reflect.String, reflect.Slice, reflect.Map:
		return v.Len() > 0
	default:
		return !v.IsZero()
	}
}
