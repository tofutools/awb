package local_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/storage"
)

// newInstance is a database with two workspaces and no users: direct mode, where
// nothing is authorized. root is the unrestricted backend a CLI on the file
// gets, and is what the fixtures are built with.
func newInstance(t *testing.T) (root *local.Backend, ctx context.Context) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Init(t.Context(), filepath.Join(dir, "awb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	root = local.New(db, storage.NewBlobs(filepath.Join(dir, "attachments")), "mikael")
	for _, key := range []string{"awb", "web"} {
		_, err := root.CreateWorkspace(t.Context(), backend.WorkspaceCreate{Key: key})
		require.NoError(t, err)
	}
	return root, t.Context()
}

// addUser creates an account through the unrestricted backend, which is how a
// real instance is bootstrapped.
func addUser(t *testing.T, root *local.Backend, ctx context.Context, name string,
	workspaceAdmin, userAdmin bool) {
	t.Helper()
	_, err := root.CreateUser(ctx, backend.UserCreate{
		Name: name, Password: "hunter2",
		WorkspaceAdmin: workspaceAdmin, UserAdmin: userAdmin,
	})
	require.NoError(t, err)
}

func grant(t *testing.T, root *local.Backend, ctx context.Context,
	workspace, user string, access domain.Access) {
	t.Helper()
	_, err := root.SetMember(ctx, workspace, user, access)
	require.NoError(t, err)
}

// forbidden and notFound name the two refusals the model turns on: a caller who
// may see a thing and may not change it, and one who is not told it is there.
func forbidden(t *testing.T, err error, because ...any) {
	t.Helper()
	require.Error(t, err, because...)
	assert.Equal(t, awberr.Forbidden, awberr.KindOf(err), err)
	assert.Equal(t, 5, awberr.ExitCode(err))
}

func notFound(t *testing.T, err error, because ...any) {
	t.Helper()
	require.Error(t, err, because...)
	assert.Equal(t, awberr.NotFound, awberr.KindOf(err), err)
	assert.Equal(t, 3, awberr.ExitCode(err))
}

// Direct mode applies no authorization at all, whoever the users table says
// may do what. The CLI on a database file can already read and write every
// byte of it.
func TestDirectModeIsUnauthorized(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)

	// bob is a member of nothing and holds no flag, and the unrestricted
	// backend acting as bob does everything anyway.
	direct := local.New(root.DB(), storage.NewBlobs(t.TempDir()), "bob")

	page, err := direct.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Len(t, page.Workspaces, 2)

	_, err = direct.CreateWorkspace(ctx, backend.WorkspaceCreate{Key: "third"})
	require.NoError(t, err)
	_, err = direct.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)
	_, err = direct.CreateUser(ctx, backend.UserCreate{Name: "carol", Password: "hunter2"})
	require.NoError(t, err)
	_, err = direct.SetMember(ctx, "awb", "carol", domain.AccessAdmin)
	require.NoError(t, err)
}

// A user works in the workspaces they are a member of and sees nothing else:
// every listing, and the unpaged total with it.
func TestVisibilityIsMembership(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	inAwb, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)
	inWeb, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Button drifts"})
	require.NoError(t, err)

	bob := root.WithUser("bob")

	workspaces, err := bob.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	require.Len(t, workspaces.Workspaces, 1)
	assert.Equal(t, "awb", workspaces.Workspaces[0].Key)
	assert.Equal(t, 1, workspaces.Total, "the unpaged total counts what the caller may see")

	issues, err := bob.ListIssues(ctx, &domain.Filter{})
	require.NoError(t, err)
	require.Len(t, issues.Issues, 1)
	assert.Equal(t, inAwb.ID, issues.Issues[0].ID)
	assert.Equal(t, 1, issues.Total)

	hiddenMatch, err := bob.ListIssues(ctx, &domain.Filter{ListingFilter: "Button drifts"})
	require.NoError(t, err)
	assert.Empty(t, hiddenMatch.Issues)
	assert.Zero(t, hiddenMatch.Total, "the text filter narrows the authorized selection")

	hiddenWorkspace, err := bob.ListWorkspaces(ctx, "web", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, hiddenWorkspace.Workspaces)
	assert.Zero(t, hiddenWorkspace.Total)

	// The visible one is readable and the invisible one is not there at all.
	_, err = bob.GetIssue(ctx, inAwb.ID)
	require.NoError(t, err)
	_, err = bob.GetIssue(ctx, inWeb.ID)
	notFound(t, err)
	_, err = bob.GetWorkspace(ctx, "web")
	notFound(t, err)
}

func TestIgnoredWorkspacesAreScopedAndRecoverable(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	grant(t, root, ctx, "web", "bob", domain.AccessRegular)

	visible, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Visible parser"})
	require.NoError(t, err)
	hidden, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Hidden parser"})
	require.NoError(t, err)
	_, err = root.AddLabel(ctx, hidden.ID, "hidden-label", "")
	require.NoError(t, err)
	_, err = root.AddRelation(ctx, visible.ID, backend.RelationRequest{
		Type: domain.RelBlockedBy, Other: hidden.ID,
	}, "")
	require.NoError(t, err)

	bob := root.WithUser("bob")
	preference, err := bob.SetWorkspaceIgnored(ctx, "web", true)
	require.NoError(t, err)
	assert.True(t, preference.Ignored)

	preferences, err := bob.ListWorkspacePreferences(ctx)
	require.NoError(t, err)
	require.Len(t, preferences, 2, "the recovery path retains the ignored workspace")
	assert.Equal(t, "web", preferences[1].Workspace.Key)
	assert.True(t, preferences[1].Ignored)

	workspaces, err := bob.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, workspaces.Total)
	assert.Equal(t, "awb", workspaces.Workspaces[0].Key)
	account, err := bob.GetUser(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, account.Workspaces, 1)
	assert.Equal(t, "awb", account.Workspaces[0].Workspace)
	issues, err := bob.ListIssues(ctx, &domain.Filter{Terms: []string{"parser"}})
	require.NoError(t, err)
	require.Len(t, issues.Issues, 1)
	assert.Equal(t, visible.ID, issues.Issues[0].ID)
	assert.True(t, issues.Issues[0].Blocked, "the complete graph still drives readiness")
	assert.Empty(t, issues.Issues[0].Blockers, "an ignored connection is not presented")
	assert.Empty(t, issues.Issues[0].Relations)
	activity, err := bob.ListActivity(ctx, visible.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	encodedActivity, err := json.Marshal(activity.Activity)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedActivity), hidden.ID,
		"historical relation snapshots must obey the current ignore boundary")

	labels, err := bob.LabelFacets(ctx, &domain.Filter{})
	require.NoError(t, err)
	assert.Empty(t, labels.Facets)
	navigation, err := bob.SearchNavigation(ctx, "hidden", 6)
	require.NoError(t, err)
	assert.Empty(t, navigation.Issues)
	assert.Empty(t, navigation.Workspaces)
	_, err = bob.GetWorkspace(ctx, "web")
	notFound(t, err)
	_, err = bob.GetIssue(ctx, hidden.ID)
	notFound(t, err)
	_, err = bob.AddRelation(ctx, visible.ID, backend.RelationRequest{
		Type: domain.RelRelated, Other: hidden.ID,
	}, "")
	notFound(t, err)

	preference, err = bob.SetWorkspaceIgnored(ctx, "web", false)
	require.NoError(t, err)
	assert.False(t, preference.Ignored)
	_, err = bob.GetIssue(ctx, hidden.ID)
	require.NoError(t, err)
	activity, err = bob.ListActivity(ctx, visible.ID, domain.ActivityKindChange, nil, nil)
	require.NoError(t, err)
	encodedActivity, err = json.Marshal(activity.Activity)
	require.NoError(t, err)
	assert.Contains(t, string(encodedActivity), hidden.ID,
		"re-enabling restores the historical relation snapshot")
}

func TestBoardMoveCannotSeeOrSelectAnInaccessibleEpic(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	issue, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Visible work"})
	require.NoError(t, err)
	secret, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Secret epic", Type: domain.TypeEpic})
	require.NoError(t, err)

	secretID := secret.ID
	_, err = root.WithUser("bob").MoveIssue(ctx, issue.ID, backend.IssueMove{
		Status: domain.StatusOpen, Epic: &secretID,
	}, "")
	notFound(t, err)
	unchanged, err := root.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Empty(t, unchanged.Relations)
	assert.Zero(t, unchanged.Order)
}

// Ignoring a workspace is a presentation preference, not an authorization
// change. Its dedicated membership administration path therefore retains the
// ordinary membership boundary while bypassing only that preference.
func TestIgnoredWorkspaceMembershipRemainsAdministrable(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", false, false)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "web", "bob", domain.AccessAdmin)

	bob := root.WithUser("bob")
	_, err := bob.SetWorkspaceIgnored(ctx, "web", true)
	require.NoError(t, err)
	_, err = bob.GetWorkspace(ctx, "web")
	notFound(t, err, "ordinary workspace browsing still respects the preference")

	page, err := bob.ListMembers(ctx, "web", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Members, 1)
	assert.Equal(t, "bob", page.Members[0].User)

	_, err = bob.SetMember(ctx, "web", "alice", domain.AccessRegular)
	require.NoError(t, err)
	removed, err := bob.RemoveMember(ctx, "web", "alice")
	require.NoError(t, err)
	assert.Equal(t, domain.AccessRegular, removed.Access)

	_, err = bob.ListMembers(ctx, "awb", nil, nil)
	notFound(t, err, "the recovery path does not reveal an unauthorized workspace")
}

func TestDirectModeUsesAStoredIdentityPreference(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "web", "bob", domain.AccessRegular)
	bob := root.WithUser("bob")
	_, err := bob.SetWorkspaceIgnored(ctx, "web", true)
	require.NoError(t, err)

	directBob := local.New(root.DB(), storage.NewBlobs(t.TempDir()), "bob")
	_, err = directBob.GetWorkspace(ctx, "web")
	notFound(t, err)

	// An identity without an account has no per-user preference and remains
	// unrestricted, as direct mode was before preferences existed.
	directOperator := local.New(root.DB(), storage.NewBlobs(t.TempDir()), "operator")
	_, err = directOperator.GetWorkspace(ctx, "web")
	require.NoError(t, err)
}

func TestUserAdministratorDirectoryOmitsIgnoredWorkspaceAssociations(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "admin", false, true)
	addUser(t, root, ctx, "bob", false, false)
	for _, user := range []string{"admin", "bob"} {
		grant(t, root, ctx, "awb", user, domain.AccessRegular)
		grant(t, root, ctx, "web", user, domain.AccessRegular)
	}
	admin := root.WithUser("admin")
	_, err := admin.SetWorkspaceIgnored(ctx, "web", true)
	require.NoError(t, err)

	page, err := admin.ListUsers(ctx, "", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Users, 2, "user administration still sees the complete account directory")
	for _, user := range page.Users {
		require.Len(t, user.Workspaces, 1)
		assert.Equal(t, "awb", user.Workspaces[0].Workspace)
	}

	page, err = admin.ListUsers(ctx, "web", nil, nil)
	require.NoError(t, err)
	assert.Empty(t, page.Users)
	assert.Zero(t, page.Total, "an ignored membership cannot make an account match")

	page, err = admin.ListUsers(ctx, "awb regular", nil, nil)
	require.NoError(t, err)
	assert.Len(t, page.Users, 2)
}

func TestUserListingFilterIsUnicodeCaseInsensitive(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "admin", false, true)
	addUser(t, root, ctx, "bob", false, false)
	fullName := "Bob MÜLLER"
	_, err := root.UpdateUser(ctx, "bob", backend.UserPatch{FullName: &fullName}, "")
	require.NoError(t, err)

	page, err := root.WithUser("admin").ListUsers(ctx, "müller", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Users, 1)
	assert.Equal(t, "bob", page.Users[0].Name)
	assert.Equal(t, 1, page.Total)
}

func TestUserListingFilterCannotMatchUnauthorizedWorkspaces(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	addUser(t, root, ctx, "carol", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	grant(t, root, ctx, "awb", "carol", domain.AccessRegular)
	grant(t, root, ctx, "web", "carol", domain.AccessAdmin)

	page, err := root.WithUser("bob").ListUsers(ctx, "web", nil, nil)
	require.NoError(t, err)
	assert.Empty(t, page.Users)
	assert.Zero(t, page.Total, "a hidden membership cannot make a visible user match")

	page, err = root.WithUser("bob").ListUsers(ctx, "carol awb regular", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Users, 1)
	assert.Equal(t, "carol", page.Users[0].Name)
}

func TestActivityUsesTheIssueWorkspaceScope(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	visible, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Visible"})
	require.NoError(t, err)
	hidden, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Hidden"})
	require.NoError(t, err)

	bob := root.WithUser("bob")
	comment, err := bob.AddComment(ctx, visible.ID, "I can see this.")
	require.NoError(t, err)
	assert.Equal(t, "bob", comment.Actor)
	page, err := bob.ListActivity(ctx, visible.ID, "", nil, nil)
	require.NoError(t, err)
	assert.Len(t, page.Activity, 2)

	_, err = bob.AddComment(ctx, hidden.ID, "I cannot see this.")
	notFound(t, err)
	_, err = bob.ListActivity(ctx, hidden.ID, "", nil, nil)
	notFound(t, err)
}

// A workspace a caller cannot see is answered "no such workspace" and never
// "forbidden": it is not theirs to know about.
func TestAnInvisibleWorkspaceIsNotFoundEverywhere(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	bob := root.WithUser("bob")

	_, err := bob.GetWorkspace(ctx, "web")
	notFound(t, err)

	// A listing filtered to it reports the workspace rather than matching nothing.
	_, err = bob.ListIssues(ctx, &domain.Filter{Workspaces: []string{"web"}})
	notFound(t, err)

	// And creating an issue in it is the same answer.
	_, err = bob.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Button drifts"})
	notFound(t, err)

	_, err = bob.ListMembers(ctx, "web", nil, nil)
	notFound(t, err)
}

// An issue the caller may not see must not be reachable by any spelling of its
// reference, and must not make a prefix of a visible one ambiguous.
func TestReferencesDoNotResolveOutsideTheScope(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	hidden, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Button drifts"})
	require.NoError(t, err)
	bob := root.WithUser("bob")

	// The full ID, the workspace-qualified prefix and the bare hash all say the
	// same thing.
	hash := hidden.ID[len("web-"):]
	for _, ref := range []string{hidden.ID, hidden.ID[:len("web-")+3], hash, hash[:3]} {
		_, err := bob.GetIssue(ctx, ref)
		notFound(t, err, ref)
	}

	// And the unrestricted backend still finds it, so what changed is the
	// scope and not the data.
	_, err = root.GetIssue(ctx, hidden.ID)
	require.NoError(t, err)
}

// Membership is the whole of the question for an issue: a member works with
// the issues of their workspace, and admin adds nothing there.
func TestAMemberWorksWithTheIssuesOfTheirWorkspace(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	bob := root.WithUser("bob")

	issue, err := bob.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)

	title := "Parser crashes on empty input"
	_, err = bob.UpdateIssue(ctx, issue.ID, backend.IssuePatch{Title: &title}, "")
	require.NoError(t, err)
	_, err = bob.Claim(ctx, issue.ID, backend.ClaimRequest{Assignee: "bob"}, "")
	require.NoError(t, err)
	_, err = bob.AddLabel(ctx, issue.ID, "parser", "")
	require.NoError(t, err)
	_, err = bob.CloseIssue(ctx, issue.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)
	_, err = bob.DeleteIssue(ctx, issue.ID, "")
	require.NoError(t, err)
}

// A workspace's own existence is the workspace_admin flag's, not its members'.
func TestOnlyAWorkspaceAdministratorManagesWorkspaces(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "carol", false, false)
	addUser(t, root, ctx, "alice", true, false)
	grant(t, root, ctx, "awb", "carol", domain.AccessAdmin)

	carol := root.WithUser("carol")
	name := "Renamed"

	_, err := carol.CreateWorkspace(ctx, backend.WorkspaceCreate{Key: "third"})
	forbidden(t, err)
	// She can see awb, so changing it is refused rather than hidden.
	_, err = carol.UpdateWorkspace(ctx, "awb", backend.WorkspacePatch{Name: &name}, "")
	forbidden(t, err)
	_, err = carol.DeleteWorkspace(ctx, "awb", false, "")
	forbidden(t, err)

	// One she cannot see is not found instead, the refusal never being the
	// thing that reveals it.
	_, err = carol.UpdateWorkspace(ctx, "web", backend.WorkspacePatch{Name: &name}, "")
	notFound(t, err)

	alice := root.WithUser("alice")
	_, err = alice.CreateWorkspace(ctx, backend.WorkspaceCreate{Key: "third"})
	require.NoError(t, err)
	_, err = alice.UpdateWorkspace(ctx, "awb", backend.WorkspacePatch{Name: &name}, "")
	require.NoError(t, err)
}

// The workspace_admin flag holds admin access in every workspace, with no row
// saying so — including the ones nobody has been given access to.
func TestAWorkspaceAdministratorSeesEverything(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", true, false)
	_, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Button drifts"})
	require.NoError(t, err)

	alice := root.WithUser("alice")
	workspaces, err := alice.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Len(t, workspaces.Workspaces, 2)

	issues, err := alice.ListIssues(ctx, &domain.Filter{})
	require.NoError(t, err)
	assert.Len(t, issues.Issues, 1)

	// And she may run the membership of a workspace she holds no row in.
	_, err = alice.SetMember(ctx, "web", "alice", domain.AccessRegular)
	require.NoError(t, err)
}

// Adding and removing a workspace's users is what admin adds over regular.
func TestOnlyAWorkspacesAdministratorChangesItsMembership(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	addUser(t, root, ctx, "carol", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	grant(t, root, ctx, "awb", "carol", domain.AccessAdmin)

	_, err := root.WithUser("bob").SetMember(ctx, "awb", "bob", domain.AccessAdmin)
	forbidden(t, err, "a regular member cannot promote themselves")

	carol := root.WithUser("carol")
	_, err = carol.AddMember(ctx, "awb", "bob", domain.AccessAdmin)
	assert.Equal(t, awberr.Conflict, awberr.KindOf(err), err)
	members, err := carol.ListMembers(ctx, "awb", nil, nil)
	require.NoError(t, err)
	for _, member := range members.Members {
		if member.User == "bob" {
			assert.Equal(t, domain.AccessRegular, member.Access,
				"a stale create must not replace the concurrent grant")
		}
	}

	membership, err := carol.SetMember(ctx, "awb", "bob", domain.AccessAdmin)
	require.NoError(t, err)
	assert.Equal(t, domain.AccessAdmin, membership.Access)

	// Granting the access already held changes nothing and still succeeds.
	_, err = carol.SetMember(ctx, "awb", "bob", domain.AccessAdmin)
	require.NoError(t, err)

	removed, err := carol.RemoveMember(ctx, "awb", "bob")
	require.NoError(t, err)
	assert.Equal(t, domain.AccessAdmin, removed.Access, "the access as it was before")

	// Withdrawing access nobody holds is not found.
	_, err = carol.RemoveMember(ctx, "awb", "bob")
	notFound(t, err)

	// And a membership must name an account that exists.
	_, err = carol.SetMember(ctx, "awb", "nobody", domain.AccessRegular)
	notFound(t, err)
}

// The last stored workspace administrator may leave. A global workspace
// administrator or direct database mode is the documented recovery path, and
// the check is intentionally not a second, partial notion of administration.
func TestTheLastStoredWorkspaceAdministratorMayRemoveThemselves(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "carol", false, false)
	grant(t, root, ctx, "awb", "carol", domain.AccessAdmin)

	carol := root.WithUser("carol")
	removed, err := carol.RemoveMember(ctx, "awb", "carol")
	require.NoError(t, err)
	assert.Equal(t, domain.AccessAdmin, removed.Access)

	_, err = carol.ListMembers(ctx, "awb", nil, nil)
	notFound(t, err, "the removal takes effect for the next operation")
}

// Revoking access makes the workspace and its issues vanish for that user, and
// leaves their issues exactly as they were.
func TestRevokingAccessHidesTheWorkspaceAndKeepsTheWork(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	bob := root.WithUser("bob")
	issue, err := bob.CreateIssue(ctx,
		backend.IssueCreate{Workspace: "awb", Title: "Parser crashes", Assignees: []string{"bob"}})
	require.NoError(t, err)

	_, err = root.RemoveMember(ctx, "awb", "bob")
	require.NoError(t, err)

	_, err = bob.GetIssue(ctx, issue.ID)
	notFound(t, err)

	kept, err := root.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, kept.Assignees, "the record of who did the work outlives the access")
}

// Managing users is the user_admin flag's, and neither flag implies the other.
func TestOnlyAUserAdministratorManagesUsers(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	addUser(t, root, ctx, "alice", true, false)
	addUser(t, root, ctx, "dana", false, true)

	bob, alice, dana := root.WithUser("bob"), root.WithUser("alice"), root.WithUser("dana")

	for _, be := range []*local.Backend{bob, alice} {
		_, err := be.CreateUser(ctx, backend.UserCreate{Name: "eve", Password: "hunter2"})
		forbidden(t, err)
		_, err = be.GetUser(ctx, "dana")
		forbidden(t, err)
		_, err = be.GetUser(ctx, "nobody")
		forbidden(t, err, "the authorization check precedes lookup and does not disclose existence")
		fullName := "Dana Doe"
		_, err = be.UpdateUser(ctx, "dana", backend.UserPatch{FullName: &fullName}, "")
		forbidden(t, err)
		_, err = be.DeleteUser(ctx, "dana", "")
		forbidden(t, err)
	}

	for name, be := range map[string]*local.Backend{"bob": bob, "alice": alice} {
		users, err := be.ListUsers(ctx, "", nil, nil)
		require.NoError(t, err)
		require.Len(t, users.Users, 1, "unrelated dormant accounts stay outside the directory")
		assert.Equal(t, name, users.Users[0].Name)
	}

	_, err := dana.CreateUser(ctx, backend.UserCreate{Name: "eve", Password: "hunter2"})
	require.NoError(t, err)
	users, err := dana.ListUsers(ctx, "", nil, nil)
	require.NoError(t, err)
	assert.Len(t, users.Users, 4)

	// Managing users confers no access to any workspace.
	workspaces, err := dana.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, workspaces.Workspaces)
}

// A member's user directory includes collaborators and retained work history,
// without exposing memberships in workspaces they cannot see.
func TestMembersListUsersFromVisibleWorkspaces(t *testing.T) {
	root, ctx := newInstance(t)
	for _, name := range []string{"alice", "bob", "carol", "dana", "erin"} {
		addUser(t, root, ctx, name, false, name == "dana")
	}
	grant(t, root, ctx, "awb", "alice", domain.AccessRegular)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	grant(t, root, ctx, "web", "bob", domain.AccessAdmin)

	// Carol's old assignment remains visible even though she has no current
	// access to awb, as does Erin through the same multi-assignee issue. Carol's
	// unrelated web membership must not come with it.
	issue, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "Parser crashes", Assignees: []string{"carol", "erin"},
	})
	require.NoError(t, err)
	_, err = root.CloseIssue(ctx, issue.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)
	grant(t, root, ctx, "web", "carol", domain.AccessRegular)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "web", Title: "Private redesign", Assignees: []string{"carol"},
	})
	require.NoError(t, err)

	page, err := root.WithUser("alice").ListUsers(ctx, "", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Users, 4)
	assert.Equal(t, 4, page.Total)
	assert.Equal(t, []string{"alice", "bob", "carol", "erin"}, []string{
		page.Users[0].Name, page.Users[1].Name, page.Users[2].Name, page.Users[3].Name,
	})
	require.Len(t, page.Users[1].Workspaces, 1)
	assert.Equal(t, "awb", page.Users[1].Workspaces[0].Workspace)
	assert.Empty(t, page.Users[2].Workspaces, "carol's hidden web membership is not disclosed")
	assert.Equal(t, []string{"awb"}, page.Users[2].ActivityWorkspaces,
		"a closed issue remains activity history without disclosing hidden activity")
	assert.Equal(t, []string{"awb"}, page.Users[3].ActivityWorkspaces)

	// A user administrator retains the complete management listing.
	all, err := root.WithUser("dana").ListUsers(ctx, "", nil, nil)
	require.NoError(t, err)
	assert.Len(t, all.Users, 5)
	assert.Equal(t, 5, all.Total)
}

// Anybody may read and edit their own profile and password, and nobody may
// grant themselves anything by doing so.
func TestAUserMayChangeTheirOwnProfileAndPassword(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	bob := root.WithUser("bob")

	self, err := bob.GetUser(ctx, "bob")
	require.NoError(t, err)
	assert.False(t, self.UserAdmin)
	require.Len(t, self.Workspaces, 1, "which is how they learn what they may do")
	assert.Equal(t, domain.AccessRegular, self.Workspaces[0].Access)

	changed := "hunter3"
	fullName := "Bob Builder"
	updated, err := bob.UpdateUser(ctx, "bob", backend.UserPatch{
		FullName: &fullName, Password: &changed,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, fullName, updated.FullName)
	assert.Greater(t, updated.UpdatedAt, self.UpdatedAt, "a password change moves updated_at")

	// But not the flags, and not anybody else's password.
	yes := true
	_, err = bob.UpdateUser(ctx, "bob", backend.UserPatch{UserAdmin: &yes}, "")
	forbidden(t, err)
	_, err = bob.UpdateUser(ctx, "alice", backend.UserPatch{Password: &changed}, "")
	forbidden(t, err)
	_, err = bob.UpdateUser(ctx, "alice", backend.UserPatch{FullName: &fullName}, "")
	forbidden(t, err)
}

// The two ways of stating a credential are two ways of stating one credential.
func TestAPasswordOrAHashButNeverBoth(t *testing.T) {
	root, ctx := newInstance(t)

	_, err := root.CreateUser(ctx, backend.UserCreate{Name: "alice"})
	require.Error(t, err, "an account nobody can log in to")
	assert.Equal(t, 2, awberr.ExitCode(err))

	_, err = root.CreateUser(ctx, backend.UserCreate{
		Name: "alice", Password: "hunter2",
		PasswordHash: "$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by",
	})
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))

	// A hash alone is enough, and is stored as given.
	user, err := root.CreateUser(ctx, backend.UserCreate{
		Name:         "alice",
		PasswordHash: "alice:$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by",
	})
	require.NoError(t, err)
	assert.Equal(t, "alice", user.Name)
}

// Deleting a user takes their memberships and leaves their issues.
func TestDeletingAUserTakesTheirMemberships(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	issue, err := root.CreateIssue(ctx,
		backend.IssueCreate{Workspace: "awb", Title: "Parser crashes", Assignees: []string{"bob"}})
	require.NoError(t, err)

	deleted, err := root.DeleteUser(ctx, "bob", "")
	require.NoError(t, err)
	require.Len(t, deleted.User.Workspaces, 1, "the memberships as they were")

	members, err := root.ListMembers(ctx, "awb", nil, nil)
	require.NoError(t, err)
	assert.Empty(t, members.Members)

	kept, err := root.GetIssue(ctx, issue.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, kept.Assignees)
}

// An account deleted between authentication and the operation cannot act.
func TestAnUnknownCallerIsRefused(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)

	ghost := root.WithUser("nobody")
	_, err := ghost.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	forbidden(t, err)
}

// A user object never carries a password, in any direction.
func TestAUserNeverCarriesAPassword(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)

	user, err := root.GetUser(ctx, "bob")
	require.NoError(t, err)
	assert.NotNil(t, user.Workspaces)
	assert.Empty(t, user.Workspaces)
	assert.NotEmpty(t, user.CreatedAt)
	assert.Equal(t, user.CreatedAt, user.UpdatedAt)
}

// A tree crosses workspaces, so it shows what the caller can see of a
// decomposition rather than claiming to be the whole of one.
func TestATreeStopsAtTheScope(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	parent, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Epic"})
	require.NoError(t, err)
	visible, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "Visible child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: parent.ID}},
	})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "web", Title: "Hidden child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: parent.ID}},
	})
	require.NoError(t, err)

	whole, err := root.Tree(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, whole.Children, 2)

	scoped, err := root.WithUser("bob").Tree(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, scoped.Children, 1)
	assert.Equal(t, visible.ID, scoped.Children[0].ID)
}

// The facet endpoints run the same selection as a listing, so they are scoped
// with it: a label used only in a workspace the caller cannot see is not in use
// as far as they are concerned.
func TestFacetsAreScoped(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	_, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "Parser crashes", Labels: []string{"parser"}, Assignees: []string{"bob"}})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "web", Title: "Button drifts", Labels: []string{"css"}, Assignees: []string{"carol"}})
	require.NoError(t, err)

	bob := root.WithUser("bob")

	labels, err := bob.LabelFacets(ctx, &domain.Filter{})
	require.NoError(t, err)
	require.Len(t, labels.Facets, 1)
	assert.Equal(t, "parser", labels.Facets[0].Value)

	assignees, err := bob.AssigneeFacets(ctx, &domain.Filter{})
	require.NoError(t, err)
	require.Len(t, assignees.Facets, 1)
	assert.Equal(t, "bob", assignees.Facets[0].Value)
}

// Search runs the same selection too, index and all.
func TestSearchIsScoped(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	_, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Parser drifts"})
	require.NoError(t, err)

	all, err := root.ListIssues(ctx, &domain.Filter{Terms: []string{"parser"}})
	require.NoError(t, err)
	assert.Equal(t, 2, all.Total)

	scoped, err := root.WithUser("bob").ListIssues(ctx, &domain.Filter{Terms: []string{"parser"}})
	require.NoError(t, err)
	require.Len(t, scoped.Issues, 1)
	assert.Equal(t, 1, scoped.Total)
	assert.Equal(t, "awb", scoped.Issues[0].Workspace)
}

func TestIssueSuggestionsAreScoped(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	visible, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Parser crashes"})
	require.NoError(t, err)
	_, err = root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Parser drifts"})
	require.NoError(t, err)

	page, err := root.WithUser("bob").SuggestIssues(ctx, "parser", nil)
	require.NoError(t, err)
	require.Len(t, page.Issues, 1)
	assert.Equal(t, 1, page.Total)
	assert.Equal(t, visible.ID, page.Issues[0].ID)
}

// Attachments hang off an issue, so they are scoped with it.
func TestAttachmentsAreScoped(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	hidden, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Button drifts"})
	require.NoError(t, err)
	_, err = root.AddAttachment(ctx, hidden.ID, backend.AttachmentCreate{
		Name: "trace.txt", Content: strings.NewReader("boom")})
	require.NoError(t, err)

	bob := root.WithUser("bob")
	_, err = bob.ListAttachments(ctx, hidden.ID, nil, nil)
	notFound(t, err)
	_, err = bob.GetAttachment(ctx, hidden.ID, "trace.txt")
	notFound(t, err)
	_, err = bob.DeleteAttachment(ctx, hidden.ID, "trace.txt")
	notFound(t, err)
	_, err = bob.AddAttachment(ctx, hidden.ID, backend.AttachmentCreate{
		Name: "other.txt", Content: strings.NewReader("boom")})
	notFound(t, err)
}

// A member of a workspace may read who else works on it.
func TestAMemberSeesTheMemberList(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	addUser(t, root, ctx, "carol", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)
	grant(t, root, ctx, "awb", "carol", domain.AccessAdmin)

	page, err := root.WithUser("bob").ListMembers(ctx, "awb", nil, nil)
	require.NoError(t, err)
	require.Len(t, page.Members, 2)
	assert.Equal(t, 2, page.Total)
	assert.Equal(t, "bob", page.Members[0].User, "ordered by username ascending")
	assert.Equal(t, "carol", page.Members[1].User)
	assert.Equal(t, "awb", page.Members[0].Workspace)
}

// The permissions are read inside the operation's own transaction, so a change
// to them is in force for the very next operation rather than for the next
// connection.
func TestPermissionsAreReadPerOperation(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	bob := root.WithUser("bob")

	workspaces, err := bob.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, workspaces.Workspaces)

	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	workspaces, err = bob.ListWorkspaces(ctx, "", domain.DefaultWorkspaceSort, nil, nil)
	require.NoError(t, err)
	assert.Len(t, workspaces.Workspaces, 1, "the same backend, without reopening anything")
}

func TestOnlyWorkspaceAdministratorsManageLifecycle(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	addUser(t, root, ctx, "operator", true, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessAdmin)

	_, err := root.WithUser("bob").ArchiveWorkspace(ctx, "awb", "")
	forbidden(t, err)
	archived, err := root.WithUser("operator").ArchiveWorkspace(ctx, "awb", "")
	require.NoError(t, err)
	assert.Equal(t, domain.WorkspaceArchived, archived.State)
	_, err = root.WithUser("bob").RestoreWorkspace(ctx, "awb", "")
	forbidden(t, err)
}

// An empty patch is still a read of the account, and answers with it, so a
// caller who may not read one may not reach it by asking to change nothing.
func TestAnEmptyPatchIsNotAWayToReadAnAccount(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "alice", true, true)
	addUser(t, root, ctx, "bob", false, false)

	_, err := root.WithUser("bob").UpdateUser(ctx, "alice", backend.UserPatch{}, "")
	forbidden(t, err)

	// Their own is theirs to ask about.
	_, err = root.WithUser("bob").UpdateUser(ctx, "bob", backend.UserPatch{}, "")
	require.NoError(t, err)
}

// The graph is deliberately not scoped, and this is what that means.
//
// A visible issue's relations and blockers may name issues the caller cannot
// fetch, and its blocked state is computed over all of them, because readiness
// has to be true: an issue held up by work you cannot see is still held up. A
// name is all that is exposed.
func TestTheGraphIsNotScopedAndReadinessIsTrue(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	blocker, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Button drifts"})
	require.NoError(t, err)
	blocked, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "Parser crashes",
		Relations: []backend.NewRelation{{Type: domain.RelBlockedBy, Other: blocker.ID}},
	})
	require.NoError(t, err)

	bob := root.WithUser("bob")
	seen, err := bob.GetIssue(ctx, blocked.ID)
	require.NoError(t, err)

	assert.True(t, seen.Blocked, "held up by work bob cannot see, and still held up")
	assert.Equal(t, []string{blocker.ID}, seen.Blockers)
	require.Len(t, seen.Relations, 1)
	assert.Equal(t, blocker.ID, seen.Relations[0].Other)

	// A name is all of it: the issue behind it is not there for bob.
	_, err = bob.GetIssue(ctx, blocker.ID)
	notFound(t, err)

	// And the listing that decides what to pick up agrees with the flag.
	ready, err := bob.ListIssues(ctx, &domain.Filter{Readiness: domain.ReadinessReady})
	require.NoError(t, err)
	assert.Empty(t, ready.Issues)

	stuck, err := bob.ListIssues(ctx, &domain.Filter{Readiness: domain.ReadinessBlocked})
	require.NoError(t, err)
	require.Len(t, stuck.Issues, 1)
	assert.Equal(t, blocked.ID, stuck.Issues[0].ID)

	// Closing the invisible blocker makes bob's issue ready, with no write to
	// it — which only works because the state is computed over the whole graph.
	_, err = root.CloseIssue(ctx, blocker.ID, backend.CloseRequest{}, "")
	require.NoError(t, err)
	ready, err = bob.ListIssues(ctx, &domain.Filter{Readiness: domain.ReadinessReady})
	require.NoError(t, err)
	require.Len(t, ready.Issues, 1)
}

// The relation rules are global too: a rule answered over half a graph is not
// the rule, so a caller cannot close a cycle through issues they cannot see.
func TestTheRelationRulesSeeTheWholeGraph(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	// mine -> hidden -> other, all by blocked-by, with the middle one out of
	// bob's sight.
	other, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Far end"})
	require.NoError(t, err)
	hidden, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "web", Title: "Middle",
		Relations: []backend.NewRelation{{Type: domain.RelBlockedBy, Other: other.ID}},
	})
	require.NoError(t, err)
	mine, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "Near end",
		Relations: []backend.NewRelation{{Type: domain.RelBlockedBy, Other: hidden.ID}},
	})
	require.NoError(t, err)

	// "other blocked-by mine" would close the loop through the issue bob
	// cannot see, and is refused rather than stored.
	_, err = root.WithUser("bob").AddRelation(ctx, other.ID,
		backend.RelationRequest{Type: domain.RelBlockedBy, Other: mine.ID}, "")
	require.Error(t, err)
	assert.Equal(t, 4, awberr.ExitCode(err), "a cycle is a conflict, not a refusal of access")
}

// An issue's parent may be one the caller cannot see — the child is theirs and
// its own relations already name it — and replacing it is changing their own
// issue, exactly as deleting the child would.
func TestReparentingAnIssueWhoseParentIsInvisible(t *testing.T) {
	root, ctx := newInstance(t)
	addUser(t, root, ctx, "bob", false, false)
	grant(t, root, ctx, "awb", "bob", domain.AccessRegular)

	hiddenParent, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "web", Title: "Epic"})
	require.NoError(t, err)
	visibleParent, err := root.CreateIssue(ctx, backend.IssueCreate{Workspace: "awb", Title: "Other epic"})
	require.NoError(t, err)
	child, err := root.CreateIssue(ctx, backend.IssueCreate{
		Workspace: "awb", Title: "Child",
		Relations: []backend.NewRelation{{Type: domain.RelHasParent, Other: hiddenParent.ID}},
	})
	require.NoError(t, err)

	bob := root.WithUser("bob")

	// Without force it is the ordinary "already has a parent" conflict, which
	// names an id bob's own issue already carries in its relations.
	_, err = bob.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: visibleParent.ID}, "")
	require.Error(t, err)
	assert.Equal(t, 4, awberr.ExitCode(err))

	updated, err := bob.AddRelation(ctx, child.ID,
		backend.RelationRequest{Type: domain.RelHasParent, Other: visibleParent.ID, Force: true}, "")
	require.NoError(t, err)
	require.Len(t, updated.Relations, 1)
	assert.Equal(t, visibleParent.ID, updated.Relations[0].Other)
}
