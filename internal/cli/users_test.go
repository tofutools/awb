package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
)

// The bootstrap: the first user is created in direct mode, on the file, which
// is the only place that needs no permission to do it.
func TestUserAddReadsThePasswordFromStdin(t *testing.T) {
	h := newHarness(t)

	out := h.mustRunStdin("hunter2\n", "user", "add", "alice", "--compact")
	assert.Equal(t, "alice\n", out)

	// A password is never an argument, so there is no flag to give it as.
	_, stderr, code := h.run("user", "add", "bob", "--password", "hunter2")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "unknown flag")
}

func TestUserFullName(t *testing.T) {
	h := newHarness(t)

	// Compact output stays compatible: free text is available in JSON and the
	// human-readable views, but does not introduce a new compact token shape.
	out := h.mustRunStdin("hunter2\n", "user", "add", "alice", "--full-name", "Alice Andersson", "--compact")
	assert.Equal(t, "alice\n", out)
	assert.Contains(t, h.mustRun("user", "show", "alice"), "Alice Andersson")
	assert.Contains(t, h.mustRun("user", "list"), "Alice Andersson")

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("user", "show", "alice", "--json")), &user))
	assert.Equal(t, "Alice Andersson", user.FullName)

	h.mustRun("user", "update", "alice", "--full-name", "Alice Berg")
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("user", "show", "alice", "--json")), &user))
	assert.Equal(t, "Alice Berg", user.FullName)

	h.mustRun("user", "update", "alice", "--full-name", "")
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("user", "show", "alice", "--json")), &user))
	assert.Empty(t, user.FullName)
}

// A password reaches the database as a hash and comes back out as nothing at
// all: no output mode has a field it could appear in.
func TestAUserNeverPrintsAPassword(t *testing.T) {
	h := newHarness(t)
	h.mustRunStdin("hunter2\n", "user", "add", "alice")

	for _, args := range [][]string{
		{"user", "show", "alice"},
		{"user", "show", "alice", "--json"},
		{"user", "show", "alice", "--compact"},
		{"user", "list"},
		{"user", "list", "--json"},
	} {
		out := h.mustRun(args...)
		assert.NotContains(t, out, "hunter2", strings.Join(args, " "))
		assert.NotContains(t, out, "password", strings.Join(args, " "))
		assert.NotContains(t, out, "$2", strings.Join(args, " "))
	}
}

// A bcrypt hash computed elsewhere sets the password without the plaintext
// ever reaching awb. This is exactly what "htpasswd -Bn alice" prints.
func TestUserAddTakesAPreComputedHash(t *testing.T) {
	h := newHarness(t)
	const line = "alice:$2y$05$jRQBcZwqnz6rOegEld5p7ODNrLSH7xsVELVgmt0NTTmZBnaiCU2by"

	out := h.mustRun("user", "add", "alice", "--password-hash", line, "--compact")
	assert.Equal(t, "alice\n", out)

	// The bare hash is accepted too, and a hash written for somebody else is
	// not applied to the wrong account.
	_, hash, _ := strings.Cut(line, ":")
	h.mustRun("user", "add", "bob", "--password-hash", hash)

	_, stderr, code := h.run("user", "add", "carol", "--password-hash", line)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "alice")

	// And nothing that is not a bcrypt hash gets in.
	_, stderr, code = h.run("user", "add", "dana", "--password-hash", "hunter2")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "htpasswd")
}

func TestUserFlagsAndMembership(t *testing.T) {
	h := newHarness(t)
	h.mustRunStdin("hunter2\n", "user", "add", "alice", "--workspace-admin", "--user-admin")
	h.mustRunStdin("hunter2\n", "user", "add", "bob")

	assert.Equal(t, "alice +workspace-admin +user-admin\nbob\n",
		h.mustRun("user", "list", "--compact"))

	h.mustRun("workspace", "grant", "awb", "bob", "--access", "admin")
	assert.Equal(t, "bob awb:admin\n", h.mustRun("user", "show", "bob", "--compact"))
	assert.Equal(t, "awb bob admin\n", h.mustRun("workspace", "members", "awb", "--compact"))

	// Granting again replaces the level rather than adding a second row.
	h.mustRun("workspace", "grant", "awb", "bob")
	assert.Equal(t, "awb bob regular\n", h.mustRun("workspace", "members", "awb", "--compact"))

	h.mustRun("workspace", "revoke", "awb", "bob")
	assert.Empty(t, h.mustRun("workspace", "members", "awb", "--compact"))

	_, _, code := h.run("workspace", "grant", "awb", "bob", "--access", "owner")
	assert.Equal(t, 2, code, "the access vocabulary is fixed")
}

func TestUserUpdate(t *testing.T) {
	h := newHarness(t)
	h.mustRunStdin("hunter2\n", "user", "add", "alice")

	out := h.mustRun("user", "update", "alice", "--user-admin", "--compact")
	assert.Equal(t, "alice +user-admin\n", out)

	out = h.mustRun("user", "update", "alice", "--user-admin=false", "--compact")
	assert.Equal(t, "alice\n", out)

	// --password reads the new one from stdin, exactly as user add does.
	h.mustRunStdin("hunter3\n", "user", "update", "alice", "--password")

	_, _, code := h.runStdin("hunter3\n", "user", "update", "alice",
		"--password", "--password-hash", "x")
	assert.Equal(t, 2, code, "two ways of stating one credential")
}

// Without a name it prints your own account, which is how a user learns what
// they are permitted to do.
func TestUserShowDefaultsToTheCaller(t *testing.T) {
	h := newHarness(t)
	h.mustRunStdin("hunter2\n", "user", "add", "mikael")
	h.mustRun("workspace", "grant", "awb", "mikael", "--access", "admin")

	assert.Equal(t, "mikael awb:admin\n", h.mustRun("user", "show", "--compact"))

	_, _, code := h.run("user", "show", "alice", "bob")
	assert.Equal(t, 2, code)
}

func TestUserDeleteNeedsForce(t *testing.T) {
	h := newHarness(t)
	h.mustRunStdin("hunter2\n", "user", "add", "alice")

	_, stderr, code := h.run("user", "delete", "alice")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--force")

	out := h.mustRun("user", "delete", "alice", "--force")
	assert.Equal(t, "Deleted user alice.\n", out)

	_, _, code = h.run("user", "show", "alice")
	assert.Equal(t, 3, code)
}

// The --json shape is the stable one, and is the same object the API returns.
func TestUserJSON(t *testing.T) {
	h := newHarness(t)
	h.mustRunStdin("hunter2\n", "user", "add", "alice", "--workspace-admin")
	h.mustRun("workspace", "grant", "awb", "alice", "--access", "admin")

	var user domain.User
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("user", "show", "alice", "--json")), &user))
	assert.Equal(t, "alice", user.Name)
	assert.Empty(t, user.FullName)
	assert.True(t, user.WorkspaceAdmin)
	assert.False(t, user.UserAdmin)
	assert.NotEmpty(t, user.CreatedAt)
	require.Len(t, user.Workspaces, 1)
	assert.Equal(t, domain.Membership{
		Workspace: "awb", User: "alice", Access: domain.AccessAdmin}, user.Workspaces[0])

	// An empty listing is [] and never null, and a workspace that is not there
	// is reported rather than answered with one.
	h.mustRun("workspace", "create", "web")
	assert.Equal(t, "[]\n", h.mustRun("workspace", "members", "web", "--json"))

	_, _, code := h.run("workspace", "members", "nosuch", "--json")
	assert.Equal(t, 3, code)
}

// A username is an assignee, which is what makes an account name usable as one.
func TestUsernameVocabulary(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"Alice", "a b", ""} {
		_, _, code := h.runStdin("hunter2\n", "user", "add", name)
		assert.Equal(t, 2, code, name)
	}

	h.mustRunStdin("hunter2\n", "user", "add", "claude-1")
	id := h.create("t", "--workspace", "awb", "--assignee", "claude-1")
	assert.Contains(t, h.mustRun("show", id, "--compact"), "@claude-1")
}

// An unknown subcommand is reported rather than swallowed, at every level.
func TestUnknownUserSubcommand(t *testing.T) {
	h := newHarness(t)
	_, stderr, code := h.run("user", "grant")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "unknown command")
}
