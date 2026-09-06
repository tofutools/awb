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

func TestParentFilterUsesAnEmptyValueForNoParent(t *testing.T) {
	none := ""
	require.NoError(t, domain.ValidateParentFilter(&domain.Filter{Parent: &none}))

	wireSentinel := "none"
	err := domain.ValidateParentFilter(&domain.Filter{Parent: &wireSentinel})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved for the HTTP API")
}

func TestIncludeParentRequiresANamedParent(t *testing.T) {
	parent := "awb-a1b2c3"
	require.NoError(t, domain.ValidateParentFilter(&domain.Filter{
		Parent: &parent, IncludeParent: true,
	}))

	none := ""
	for _, filter := range []*domain.Filter{
		{IncludeParent: true},
		{Parent: &none, IncludeParent: true},
	} {
		err := domain.ValidateParentFilter(filter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "include-parent requires a named parent")
	}
}
