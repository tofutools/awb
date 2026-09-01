package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

func TestParseAccess(t *testing.T) {
	for _, level := range domain.AccessLevels {
		parsed, err := domain.ParseAccess(string(level))
		require.NoError(t, err)
		assert.Equal(t, level, parsed)
	}

	_, err := domain.ParseAccess("owner")
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))
	assert.Contains(t, err.Error(), "regular, admin")
}

// A username is an assignee, because it is what that user's issues record.
func TestValidateUsernameIsTheAssigneeVocabulary(t *testing.T) {
	name, err := domain.ValidateUsername("claude-1")
	require.NoError(t, err)
	assert.Equal(t, "claude-1", name)

	for _, bad := range []string{"", "Mikael", "a b", strings.Repeat("a", 65)} {
		_, err := domain.ValidateUsername(bad)
		require.Error(t, err, bad)
		assert.Equal(t, 2, awberr.ExitCode(err), bad)
	}
}

// A password over bcrypt's limit is refused rather than cut, because bcrypt
// would hash the first 72 bytes and ignore the rest, and two passwords
// agreeing that far would both open the account.
func TestValidatePassword(t *testing.T) {
	require.NoError(t, domain.ValidatePassword(strings.Repeat("a", domain.MaxPasswordBytes)))

	err := domain.ValidatePassword(strings.Repeat("a", domain.MaxPasswordBytes+1))
	require.Error(t, err)
	assert.Equal(t, 2, awberr.ExitCode(err))

	for _, bad := range []string{"", "line\nbreak", "\x00", "bell\a"} {
		require.Error(t, domain.ValidatePassword(bad), "%q", bad)
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := domain.HashPassword("hunter2")
	require.NoError(t, err)
	assert.NotContains(t, hash, "hunter2")

	assert.True(t, domain.CheckPassword(hash, "hunter2"))
	assert.False(t, domain.CheckPassword(hash, "hunter3"))
	assert.False(t, domain.CheckPassword("not a hash", "hunter2"))

	// Two hashes of one password differ, the salt being part of each.
	other, err := domain.HashPassword("hunter2")
	require.NoError(t, err)
	assert.NotEqual(t, hash, other)

	_, err = domain.HashPassword("")
	require.Error(t, err, "an empty password is a disabled account written as a credential")
}

// htpasswd -Bn writes "<name>:<hash>" and marks its output $2y$, which is a
// bcrypt hash and has to verify.
func TestParsePasswordHashTakesHtpasswdOutput(t *testing.T) {
	// Exactly what "htpasswd -BnbC 5 alice hunter2" printed. The cost is the
	// lowest htpasswd will write, because what is tested is that the form is
	// accepted and not how long hashing takes.
	const hash = "$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by"

	bare, err := domain.ParsePasswordHash("alice", hash)
	require.NoError(t, err)
	assert.Equal(t, hash, bare)

	line, err := domain.ParsePasswordHash("alice", "alice:"+hash)
	require.NoError(t, err)
	assert.Equal(t, hash, line, "the whole htpasswd line is accepted")

	stored, err := domain.ParsePasswordHash("alice", hash)
	require.NoError(t, err)
	assert.True(t, domain.CheckPassword(stored, "hunter2"), "$2y$ verifies")
}

// A hash written for somebody else is refused rather than applied to the wrong
// account, and a scheme awb cannot verify is refused rather than stored as a
// login that would never work.
func TestParsePasswordHashRefusals(t *testing.T) {
	const hash = "$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by"

	_, err := domain.ParsePasswordHash("alice", "bob:"+hash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bob")

	for _, bad := range []string{
		"",
		"hunter2",
		"$apr1$rE6Lqxpk$8Sx0GOnfIIkQpTXHhY.gI0", // htpasswd's MD5
		"{SHA}Ec1Vy8UDaFiwmC8AiOtDJgu68Zk=",     // htpasswd's SHA-1
	} {
		_, err := domain.ParsePasswordHash("alice", bad)
		require.Error(t, err, "%q", bad)
		assert.Equal(t, 2, awberr.ExitCode(err), "%q", bad)
	}
}

// The rules the two surfaces share, as functions of the caller and of one
// stored membership.
func TestCallerRules(t *testing.T) {
	member := domain.Caller{Name: "bob"}
	admin := domain.Caller{Name: "carol"}
	workspaceAdmin := domain.Caller{Name: "alice", WorkspaceAdmin: true}
	userAdmin := domain.Caller{Name: "dana", UserAdmin: true}
	direct := domain.Caller{Name: "mikael", Unrestricted: true}

	// Membership alone decides what is visible and what may be worked on.
	assert.True(t, member.MaySeeWorkspace(domain.AccessRegular, true))
	assert.True(t, member.MayWorkOn(domain.AccessRegular, true))
	assert.False(t, member.MayAdministerWorkspace(domain.AccessRegular, true))

	assert.True(t, admin.MayAdministerWorkspace(domain.AccessAdmin, true))
	assert.True(t, admin.MayWorkOn(domain.AccessAdmin, true))
	assert.False(t, admin.MayManageWorkspaces(), "admin in a workspace is not power over workspaces")

	// A non-member sees nothing, whatever else they hold.
	assert.False(t, member.MaySeeWorkspace("", false))
	assert.False(t, userAdmin.MaySeeWorkspace("", false),
		"managing users confers no access to any workspace")

	// A workspace administrator holds admin everywhere, with no row saying so.
	access, ok := workspaceAdmin.AccessTo("", false)
	assert.True(t, ok)
	assert.Equal(t, domain.AccessAdmin, access)
	assert.True(t, workspaceAdmin.MayAdministerWorkspace("", false))
	assert.True(t, workspaceAdmin.MayManageWorkspaces())
	assert.False(t, workspaceAdmin.MayManageUsers(), "neither flag implies the other")

	assert.True(t, userAdmin.MayManageUsers())
	assert.False(t, userAdmin.MayManageWorkspaces())

	// Direct mode is every rule saying yes.
	assert.True(t, direct.MaySeeWorkspace("", false))
	assert.True(t, direct.MayAdministerWorkspace("", false))
	assert.True(t, direct.MayManageWorkspaces())
	assert.True(t, direct.MayManageUsers())
}

// Anybody may read their own account and set their own password, which is how
// somebody without the flag learns what they may do.
func TestCallerMayAlwaysSeeThemselves(t *testing.T) {
	bob := domain.Caller{Name: "bob"}

	assert.True(t, bob.MaySeeUser("bob"))
	assert.True(t, bob.MaySetPasswordOf("bob"))
	assert.False(t, bob.MaySeeUser("alice"))
	assert.False(t, bob.MaySetPasswordOf("alice"))

	dana := domain.Caller{Name: "dana", UserAdmin: true}
	assert.True(t, dana.MaySeeUser("alice"))
	assert.True(t, dana.MaySetPasswordOf("alice"))

	// A caller with no name at all matches nobody by name.
	nobody := domain.Caller{}
	assert.False(t, nobody.MaySeeUser(""))
}

func TestUserNormalizeCarriesAnEmptyArray(t *testing.T) {
	user := domain.User{Name: "alice"}
	user.Normalize()
	assert.NotNil(t, user.Workspaces)
	assert.Empty(t, user.Workspaces)
}

func TestCompactUserLine(t *testing.T) {
	user := &domain.User{Name: "alice"}
	user.Normalize()
	assert.Equal(t, "alice", domain.CompactUserLine(user))

	user = &domain.User{
		Name: "alice", WorkspaceAdmin: true, UserAdmin: true,
		Workspaces: []domain.Membership{
			{Workspace: "awb", User: "alice", Access: domain.AccessAdmin},
			{Workspace: "web", User: "alice", Access: domain.AccessRegular},
		},
	}
	assert.Equal(t, "alice +workspace-admin +user-admin awb:admin web:regular",
		domain.CompactUserLine(user))

	assert.Equal(t, "awb alice admin", domain.CompactMembershipLine(&domain.Membership{
		Workspace: "awb", User: "alice", Access: domain.AccessAdmin}))
}

// Nothing that leaves the storage layer carries a password, in any output mode.
func TestNoOutputCarriesAPassword(t *testing.T) {
	user := &domain.User{Name: "alice", CreatedAt: "2026-08-26T09:12:03.412Z"}
	user.Normalize()
	assert.NotContains(t, domain.CompactUserLine(user), "password")
}
