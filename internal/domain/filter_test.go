package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

func TestIssueSortAcceptsWebListingColumns(t *testing.T) {
	for _, value := range []string{
		"project", "status", "assignee", "type", "blockers",
		"-project", "-status", "-assignee", "-type", "-blockers",
	} {
		sort, err := domain.ParseSort(value, false)
		require.NoError(t, err, value)
		assert.Equal(t, value[0] == '-', sort.Desc, value)
	}
}

func TestProjectSortVocabulary(t *testing.T) {
	sort, err := domain.ParseProjectSort("-active")
	require.NoError(t, err)
	assert.Equal(t, domain.ProjectSortActive, sort.Key)
	assert.True(t, sort.Desc)

	_, err = domain.ParseProjectSort("name")
	assert.Error(t, err)
}
