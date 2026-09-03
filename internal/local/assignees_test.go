package local_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
)

// assignee creates an account with no password, which is what a name that is
// an assignee and nothing else looks like.
func assignee(t *testing.T, b *local.Backend, ctx context.Context, name string) {
	t.Helper()
	_, err := b.CreateUser(ctx, backend.UserCreate{Name: name})
	require.NoError(t, err)
}

// A database that holds no user has no directory to check a name against and
// takes any assignee, which is what version 1 did and what a tracker nobody
// has put names into still is. The first account turns the rule on, with or
// without a password.
func TestAnAssigneeIsAUserOnceTheDatabaseHoldsOne(t *testing.T) {
	b, ctx := newBackend(t)

	free := create(t, b, ctx, "free text", func(r *backend.IssueCreate) {
		r.Assignees = []string{"nobody"}
	})
	assert.Equal(t, []string{"nobody"}, free.Assignees)

	assignee(t, b, ctx, "alice")

	_, err := b.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "create-and-claim", Assignees: []string{"nobody"}})
	require.Error(t, err, "create-and-claim is a claim and holds to the same rule")
	assert.Equal(t, 2, exitOf(err))

	issue := create(t, b, ctx, "t")
	_, err = b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "nobody"}, "")
	require.Error(t, err)
	assert.Equal(t, 2, exitOf(err))
	assert.Contains(t, err.Error(), "no such user: nobody")

	claimed, err := b.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "alice"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"alice"}, claimed.Assignees)

	// The rule is not a foreign key: deleting the account leaves the record of
	// who holds the work exactly as it stands.
	_, err = b.DeleteUser(ctx, "alice", "")
	require.NoError(t, err)
	held, err := b.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"alice"}, held.Assignees)
}

// --force overrides the rule on the database file, where whoever can open it
// can write any assignee into it anyway and the check is a convenience. It
// does not override it through a server, however that server authenticates.
func TestForceRecordsANameThatIsNoUserOnlyOnTheFile(t *testing.T) {
	b, ctx := newBackend(t)
	assignee(t, b, ctx, "alice")
	_, err := b.CreateUser(ctx, backend.UserCreate{
		Name: "admin", Password: "hunter2", WorkspaceAdmin: true, UserAdmin: true})
	require.NoError(t, err)

	onTheFile := create(t, b, ctx, "on the file")
	forced, err := b.Claim(ctx, onTheFile.ID,
		backend.ClaimRequest{Assignee: "nobody", Force: true}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"nobody"}, forced.Assignees)

	served := create(t, b, ctx, "served")
	for name, be := range map[string]*local.Backend{
		"an authenticated request": b.WithUser("admin"),
		"an open server":           b.WithoutUserPreferences(),
	} {
		_, err := be.Claim(ctx, served.ID,
			backend.ClaimRequest{Assignee: "nobody", Force: true}, "")
		require.Error(t, err, name)
		assert.Equal(t, 2, exitOf(err), name)
	}

	// Force still overrides what it always overrode, for a name that is a user.
	blocked := create(t, b, ctx, "blocked", func(r *backend.IssueCreate) {
		r.Relations = []backend.NewRelation{{Type: domain.RelBlockedBy, Other: served.ID}}
	})
	claimed, err := b.WithUser("admin").Claim(ctx, blocked.ID,
		backend.ClaimRequest{Assignee: "alice", Force: true}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"alice"}, claimed.Assignees)
}

// Releasing is how an assignment naming somebody the directory does not know
// is taken off an issue, so the name it removes is not checked against it.
func TestReleaseRemovesANameThatIsNoUser(t *testing.T) {
	b, ctx := newBackend(t)
	issue := create(t, b, ctx, "t", func(r *backend.IssueCreate) {
		r.Assignees = []string{"nobody", "alice"}
	})
	assignee(t, b, ctx, "alice")

	released, err := b.Release(ctx, issue.ID, backend.ReleaseRequest{Assignee: "nobody"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"alice"}, released.Assignees)
	assert.Equal(t, domain.StatusInProgress, released.Status)
}

// The board's move assigns the caller, and the caller is an assignee like any
// other. There is no force on that path: an identity the directory does not
// know claims from the command line instead.
func TestBoardMoveAssignsOnlyAnIdentityThatIsAUser(t *testing.T) {
	b, ctx := newBackend(t)
	assignee(t, b, ctx, "alice")
	issue := create(t, b, ctx, "t")

	_, err := b.MoveIssue(ctx, issue.ID, backend.IssueMove{Status: domain.StatusInProgress}, "")
	require.Error(t, err, "mikael is not one of this tracker's users")
	assert.Equal(t, 2, exitOf(err))

	moved, err := b.WithIdentity("alice").MoveIssue(ctx, issue.ID,
		backend.IssueMove{Status: domain.StatusInProgress}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"alice"}, moved.Assignees)
}
