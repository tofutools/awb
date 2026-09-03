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
