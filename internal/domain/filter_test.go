package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

func TestIssueSortAcceptsWebListingColumns(t *testing.T) {
	for _, value := range []string{
		"workspace", "status", "assignee", "type", "blockers",
		"-workspace", "-status", "-assignee", "-type", "-blockers",
	} {
		sort, err := domain.ParseSort(value, false)
		require.NoError(t, err, value)
		assert.Equal(t, value[0] == '-', sort.Desc, value)
	}
}

func TestWorkspaceSortVocabulary(t *testing.T) {
	sort, err := domain.ParseWorkspaceSort("-active")
	require.NoError(t, err)
	assert.Equal(t, domain.WorkspaceSortActive, sort.Key)
	assert.True(t, sort.Desc)

	_, err = domain.ParseWorkspaceSort("name")
	assert.Error(t, err)
}
