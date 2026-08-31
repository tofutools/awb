package local_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
)

func TestSearchNavigationMatchesNamesAndIdentifiersBySubstring(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "Keyboard Command Palette")
	_, err := b.CreateProject(ctx, backend.ProjectCreate{Key: "client-ui", Name: "Browser Client"})
	require.NoError(t, err)
	_, err = b.CreateUser(ctx, backend.UserCreate{
		Name: "palette-owner", FullName: "Palette Owner", Password: "safe password",
	})
	require.NoError(t, err)

	byTitle, err := b.SearchNavigation(ctx, "command pal", 6)
	require.NoError(t, err)
	require.Len(t, byTitle.Issues, 1)
	assert.Equal(t, issue.ID, byTitle.Issues[0].ID)

	byID, err := b.SearchNavigation(ctx, issue.ID[4:9], 6)
	require.NoError(t, err)
	require.Len(t, byID.Issues, 1)
	assert.Equal(t, issue.ID, byID.Issues[0].ID)

	_, err = b.CloseIssue(ctx, issue.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)
	closed, err := b.SearchNavigation(ctx, "command pal", 6)
	require.NoError(t, err)
	require.Len(t, closed.Issues, 1)
	assert.Equal(t, issue.ID, closed.Issues[0].ID, "closed issues remain directly navigable")

	byProjectName, err := b.SearchNavigation(ctx, "browser", 6)
	require.NoError(t, err)
	require.Len(t, byProjectName.Projects, 1)
	assert.Equal(t, "client-ui", byProjectName.Projects[0].Key)

	byProjectKey, err := b.SearchNavigation(ctx, "ent-u", 6)
	require.NoError(t, err)
	require.Len(t, byProjectKey.Projects, 1)
	assert.Equal(t, "client-ui", byProjectKey.Projects[0].Key)

	byUser, err := b.SearchNavigation(ctx, "ETTE-OWN", 6)
	require.NoError(t, err)
	require.Len(t, byUser.Users, 1)
	assert.Equal(t, "palette-owner", byUser.Users[0].Name)

	byFullName, err := b.SearchNavigation(ctx, "LETTE OW", 6)
	require.NoError(t, err)
	require.Len(t, byFullName.Users, 1)
	assert.Equal(t, "Palette Owner", byFullName.Users[0].FullName)
}

func TestSearchNavigationCapsEveryKind(t *testing.T) {
	b, ctx := newBackend(t)
	for _, title := range []string{"palette one", "palette two", "palette three"} {
		create(t, b, ctx, title)
	}

	results, err := b.SearchNavigation(ctx, "palette", 2)
	require.NoError(t, err)
	assert.Len(t, results.Issues, 2)
}

func TestSearchNavigationRanksAnExactUsernameInsideTheCap(t *testing.T) {
	b, ctx := newBackend(t)
	_, err := b.CreateUser(ctx, backend.UserCreate{
		Name: "target", FullName: "Selected Person", Password: "safe password",
	})
	require.NoError(t, err)
	for _, name := range []string{
		"a-target-1", "a-target-2", "a-target-3", "a-target-4",
		"a-target-5", "a-target-6", "a-target-7",
	} {
		_, err = b.CreateUser(ctx, backend.UserCreate{Name: name, Password: "safe password"})
		require.NoError(t, err)
	}

	results, err := b.SearchNavigation(ctx, "target", 6)
	require.NoError(t, err)
	require.NotEmpty(t, results.Users)
	assert.Equal(t, "target", results.Users[0].Name,
		"following a selected palette result must find the exact user within the same cap")
}
