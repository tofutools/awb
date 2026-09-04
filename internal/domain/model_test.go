package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

func TestIssueUnmarshalsRelationTitlesWithoutPuttingThemInRelationSnapshots(t *testing.T) {
	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"awb-child","relations":[{
			"type":"has-parent","other":"awb-parent",
			"other_title":"A deliberately long parent title that remains complete","direction":"out"
		}]
	}`), &issue))
	assert.Equal(t, "A deliberately long parent title that remains complete",
		issue.RelationTitle("awb-parent"))

	snapshot, err := json.Marshal(issue.Relations)
	require.NoError(t, err)
	assert.NotContains(t, string(snapshot), "other_title")
}

// parent is read back out of the relations rather than stored beside them, so
// the two cannot disagree. It names the issue this one is has-parent — the
// relation it is the subject of — and is null when there is none. A has-parent
// pointing the other way is a child, and a child is not a parent.
func TestNormalizeDerivesParentFromTheRelations(t *testing.T) {
	child := domain.Issue{Relations: []domain.Relation{
		{Type: domain.RelHasParent, Other: "awb-parent", Direction: domain.DirectionOut},
		{Type: domain.RelHasParent, Other: "awb-child", Direction: domain.DirectionIn},
		{Type: domain.RelBlockedBy, Other: "awb-blocker", Direction: domain.DirectionOut},
	}}
	child.Normalize()
	require.NotNil(t, child.Parent)
	assert.Equal(t, "awb-parent", *child.Parent)

	orphan := domain.Issue{Relations: []domain.Relation{
		{Type: domain.RelHasParent, Other: "awb-child", Direction: domain.DirectionIn},
		{Type: domain.RelRelated, Other: "awb-other", Direction: domain.DirectionOut},
	}}
	orphan.Normalize()
	assert.Nil(t, orphan.Parent)

	// A parent the caller may not see is absent from the relations, and so is
	// absent here: the field never names an issue the relations do not.
	named := "awb-parent"
	hidden := domain.Issue{Parent: &named}
	hidden.Normalize()
	assert.Nil(t, hidden.Parent)
}

// The whole issue shape encodes parent as a JSON null when there is none,
// which is the one field that is null rather than the empty string.
func TestParentEncodesAsNullWhenThereIsNone(t *testing.T) {
	var issue domain.Issue
	issue.Normalize()
	encoded, err := json.Marshal(issue)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"parent":null`)

	var decoded domain.Issue
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Nil(t, decoded.Parent)
}
