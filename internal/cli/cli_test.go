package cli_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/cli"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/openapi"
)

// openAPI is the document main embeds and hands to Execute, read from the file
// it is embedded from. It is read at load time because these tests run in a
// scratch working directory, so a relative path does not resolve once one has
// started. Only serve reads it.
var openAPI = func() *openapi.Document {
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		panic(err)
	}
	return openapi.New(raw)
}()

// harness runs awb with a scratch database and isolated configuration, the way
// a script or an agent would.
type harness struct {
	t   *testing.T
	dir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()

	// Isolate every source the configuration reads, so a test never picks up the
	// developer's own settings.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("AWB_DB", filepath.Join(root, "awb.db"))
	t.Setenv("AWB_IDENTITY", "mikael")
	for _, name := range []string{"AWB_USER", "AWB_PASSWORD", "AWB_PROJECT", "AWB_COLOR", "AWB_CONFIG_FILE"} {
		t.Setenv(name, "")
	}
	// The default table mode is coloured only when stdout is a terminal, which it
	// never is here; pinning it keeps the golden output honest anyway.
	t.Setenv("NO_COLOR", "1")

	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	t.Chdir(work)

	h := &harness{t: t, dir: work}
	h.mustRun("init")
	h.mustRun("project", "create", "awb", "--name", "Agent Work Board")
	return h
}

// root is the scratch directory holding the database and, beside it, the
// attachments directory.
func (h *harness) root() string { return filepath.Dir(h.dir) }

// run executes one command and returns its stdout, stderr and exit code.
func (h *harness) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	return h.runStdin("", args...)
}

// runStdin is run with something on stdin.
func (h *harness) runStdin(stdin string, args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	code = cli.Execute(h.t.Context(), "test", openAPI, args, &out, &errOut,
		strings.NewReader(stdin))
	return out.String(), errOut.String(), code
}

// mustRunStdin is mustRun with something on stdin.
func (h *harness) mustRunStdin(stdin string, args ...string) string {
	h.t.Helper()
	stdout, stderr, code := h.runStdin(stdin, args...)
	require.Equal(h.t, 0, code, "awb %s failed: %s", strings.Join(args, " "), stderr)
	return stdout
}

// mustRun executes a command that is expected to succeed.
func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	stdout, stderr, code := h.run(args...)
	require.Equal(h.t, 0, code, "awb %s failed: %s", strings.Join(args, " "), stderr)
	return stdout
}

// create makes an issue and returns its ID.
func (h *harness) create(args ...string) string {
	h.t.Helper()
	return strings.TrimSpace(h.mustRun(append([]string{"create"}, args...)...))
}

// Enum-like parameters advertise their complete vocabulary to Boa, which
// validates their values and supplies these alternatives to shell completion.
func TestEnumParameterCompletions(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"create type", []string{"create", "--type"}, []string{"epic", "feature", "bug", "task", "chore"}},
		{"create priority", []string{"create", "--priority"}, []string{"0", "1", "2", "3", "4"}},
		{"update type", []string{"update", "--type"}, []string{"epic", "feature", "bug", "task", "chore"}},
		{"update priority", []string{"update", "--priority"}, []string{"0", "1", "2", "3", "4"}},
		{"list status", []string{"list", "--status"}, []string{"open", "in_progress", "closed"}},
		{"list type", []string{"list", "--type"}, []string{"epic", "feature", "bug", "task", "chore"}},
		{"list priority", []string{"list", "--priority"}, []string{"0", "1", "2", "3", "4"}},
		{"list priority max", []string{"list", "--priority-max"}, []string{"0", "1", "2", "3", "4"}},
		{"list sort", []string{"list", "--sort"}, []string{"priority", "-priority", "created", "-created", "updated", "-updated", "id", "-id"}},
		{"search sort", []string{"search", "--sort"}, []string{"priority", "-priority", "created", "-created", "updated", "-updated", "id", "-id", "relevance", "-relevance"}},
		{"project access", []string{"project", "grant", "--access"}, []string{"regular", "admin"}},
		{"color", []string{"--color"}, []string{"auto", "always", "never"}},
		{"install skills harness", []string{"agent-guide", "install-skills", "--harness"}, []string{"all", "claude", "codex", "opencode", "copilot"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"__complete"}, tt.args...)
			stdout, stderr, code := h.run(append(args, "")...)
			require.Equal(t, 0, code, stderr)
			lines := strings.Fields(stdout)
			require.NotEmpty(t, lines)
			assert.Equal(t, ":0", lines[len(lines)-1])
			assert.ElementsMatch(t, tt.want, lines[:len(lines)-1])
		})
	}
}

// Dynamic alternatives use the same configured backend as the command would,
// and Boa makes flags already present on the command available to the lookup.
func TestDataBackedFilterCompletions(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "create", "web")
	h.create("Backend task", "--project", "awb", "--label", "backend", "--assignee", "alice")
	h.create("Backend bug", "--project", "awb", "--type", "bug", "--label", "crash", "--assignee", "carol")
	h.create("Ready task", "--project", "awb", "--label", "ready")
	h.create("Parser failure", "--project", "awb", "--label", "parser")
	h.create("Frontend task", "--project", "web", "--label", "frontend", "--assignee", "bob")

	complete := func(args ...string) []string {
		t.Helper()
		stdout, stderr, code := h.run(append([]string{"__complete"}, append(args, "")...)...)
		require.Equal(t, 0, code, stderr)
		lines := strings.Fields(stdout)
		require.NotEmpty(t, lines)
		assert.Equal(t, ":0", lines[len(lines)-1])
		return lines[:len(lines)-1]
	}

	assert.ElementsMatch(t, []string{"awb", "web"}, complete("list", "--project"))
	assert.Equal(t, []string{"backend", "crash", "parser", "ready"},
		complete("list", "--project", "awb", "--label"))
	assert.Equal(t, []string{"crash"},
		complete("list", "--project", "awb", "--type", "bug", "--label"))
	assert.Equal(t, []string{"bob"},
		complete("list", "--project", "web", "--assignee"))
	assert.Equal(t, []string{"parser", "ready"},
		complete("ready", "--project", "awb", "--label"))
	assert.Equal(t, []string{"parser"}, complete("search", "Parser", "--label"))
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, ".awb.yaml"), []byte("project: awb\n"), 0o600))
	assert.Equal(t, []string{"backend", "crash", "parser", "ready"}, complete("list", "--label"))
	assert.Equal(t, []string{"backend", "crash", "frontend", "parser", "ready"},
		complete("--no-context", "list", "--label"))

	otherDB := filepath.Join(h.root(), "other.db")
	h.mustRun("--db", otherDB, "init")
	h.mustRun("--db", otherDB, "project", "create", "other")
	assert.Equal(t, []string{"other"},
		complete("--db", otherDB, "list", "--project"))
	assert.Empty(t, complete("--db", "https://", "list", "--project"),
		"a completion lookup failure is silent")
}

// The end-to-end example from the README, run verbatim.
func TestWorkedExample(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, ".awb.yaml"), []byte("project: awb\n"), 0o600))

	first := h.create("Parser crashes on empty input", "--type", "bug", "--priority", "1",
		"--label", "parser")
	second := h.create("Add fuzz tests for parser", "--discovered-from", first, "--blocked-by", first)

	assert.Equal(t,
		first+" P1 open bug \"Parser crashes on empty input\" #parser\n",
		h.mustRun("ready", "--compact"))

	h.mustRun("claim", first)
	h.mustRun("close", first, "--reason", "Guard against empty token stream")

	assert.Equal(t,
		second+" P2 open task \"Add fuzz tests for parser\"\n",
		h.mustRun("ready", "--compact"),
		"closing the blocker makes the blocked issue ready, with no write to it")
}

// Mutating commands print nothing on success in the default and compact modes,
// except create, which prints the new ID, and the deleting commands and demo,
// which print a one-line summary.
func TestMutatingCommandsAreSilent(t *testing.T) {
	h := newHarness(t)
	id := h.create("t", "--project", "awb")

	for _, args := range [][]string{
		{"claim", id},
		{"release", id},
		{"close", id},
		{"reopen", id},
		{"update", id, "--priority", "0"},
		{"label", "add", id, "x"},
		{"label", "rm", id, "x"},
	} {
		stdout, stderr, code := h.run(args...)
		assert.Equal(t, 0, code, args)
		assert.Empty(t, stdout, args)
		assert.Empty(t, stderr, args)
	}

	stdout, _, code := h.run("delete", id, "--force")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Deleted "+id)
}

// Under --json every mutating command prints the resulting object.
func TestJSONMutationsPrintTheObject(t *testing.T) {
	h := newHarness(t)
	id := h.create("t", "--project", "awb")

	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("claim", id, "--json")), &issue))
	assert.Equal(t, domain.StatusInProgress, issue.Status)
	assert.Equal(t, "mikael", issue.Assignee)

	// A deleting command prints the object as it was immediately before deletion,
	// relations included.
	other := h.create("other", "--project", "awb")
	h.mustRun("dep", "add", id, "--blocked-by", other)

	var deleted domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("delete", id, "--force", "--json")), &deleted))
	assert.Equal(t, id, deleted.ID)
	assert.Len(t, deleted.Relations, 1)
}

// An empty list is success and renders as an empty table, no compact output,
// or [].
func TestEmptyListings(t *testing.T) {
	h := newHarness(t)

	assert.Empty(t, h.mustRun("list"))
	assert.Empty(t, h.mustRun("list", "--compact"))
	assert.Equal(t, "[]\n", h.mustRun("list", "--json"))
	assert.Equal(t, "[]\n", h.mustRun("ready", "--json"))
}

func TestPersistentFlagsWorkBeforeAndAfterSubcommands(t *testing.T) {
	h := newHarness(t)

	assert.Equal(t,
		h.mustRun("project", "list", "--compact"),
		h.mustRun("--compact", "project", "list"),
	)
}

// Two invocations against unchanged data produce byte-identical output.
func TestOutputIsDeterministic(t *testing.T) {
	h := newHarness(t)
	for i := range 5 {
		h.create("issue", "--project", "awb", "--label", "b", "--label", "a",
			"--priority", string(rune('0'+i%5)))
	}

	for _, args := range [][]string{
		{"list", "--json"}, {"list", "--compact"},
		{"ready", "--json"}, {"ready", "--compact"},
		{"project", "list", "--json"},
	} {
		first := h.mustRun(args...)
		for range 3 {
			assert.Equal(t, first, h.mustRun(args...), args)
		}
	}
}

// show --compact prints the same single line a listing would and nothing else.
func TestShowCompactIsOneLine(t *testing.T) {
	h := newHarness(t)
	id := h.create("A title", "--project", "awb", "--description", "See [x](https://example.com/1).")

	line := h.mustRun("show", id, "--compact")
	assert.Equal(t, id+` P2 open task "A title"`+"\n", line)

	// --json is what an agent uses when it needs the rest.
	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", id, "--json")), &issue))
	assert.Equal(t, []domain.Link{{Text: "x", URL: "https://example.com/1"}}, issue.Links)
}

// The default mode prints the description and lists the links beneath it.
func TestShowDefaultShowsLinks(t *testing.T) {
	h := newHarness(t)
	id := h.create("t", "--project", "awb", "--description", "See [CI run](https://ci.example.com/1).")

	out := h.mustRun("show", id)
	assert.Contains(t, out, "See [CI run](https://ci.example.com/1).", "the description is verbatim")
	assert.Contains(t, out, "Links")
	assert.Contains(t, out, "https://ci.example.com/1")
}

// Directory context: the local file's project filters listings and is the
// creation default, and its label is added to issues created here in addition
// to any --label given.
func TestDirectoryContext(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "create", "web")
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, ".awb.yaml"),
		[]byte("project: awb\nlabel: frontend\n"), 0o600))

	id := h.create("in context", "--label", "extra")

	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", id, "--json")), &issue))
	assert.Equal(t, "awb", issue.Project, "the context project is the creation default")
	assert.Equal(t, []string{"extra", "frontend"}, issue.Labels,
		"the context label is added, not substituted")

	// An explicit --project replaces the context one.
	elsewhere := h.create("elsewhere", "--project", "web")
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", elsewhere, "--json")), &issue))
	assert.Equal(t, "web", issue.Project)

	// Listings are scoped to the context by default.
	assert.NotContains(t, h.mustRun("list", "--compact"), elsewhere)

	// --no-context restores the view of the whole database.
	assert.Contains(t, h.mustRun("list", "--compact", "--no-context"), elsewhere)
}

// A project from the user configuration or AWB_PROJECT is the default filter,
// just like a project from directory context. An explicit project replaces it,
// and --all-projects removes it without giving up the other filters.
func TestConfiguredDefaultProjectFiltersListings(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "create", "web")
	awb := h.create("in awb", "--project", "awb")
	web := h.create("in web", "--project", "web")

	userConfig := filepath.Join(h.root(), "config.yaml")
	require.NoError(t, os.WriteFile(userConfig, []byte("project: awb\n"), 0o600))
	t.Setenv("AWB_CONFIG_FILE", userConfig)

	assert.Contains(t, h.mustRun("list", "--compact"), awb)
	assert.NotContains(t, h.mustRun("list", "--compact"), web)
	assert.Contains(t, h.mustRun("list", "--compact", "--project", "web"), web)
	assert.Contains(t, h.mustRun("list", "--compact", "--all-projects"), web)

	t.Setenv("AWB_PROJECT", "web")
	assert.NotContains(t, h.mustRun("list", "--compact"), awb)
	assert.Contains(t, h.mustRun("list", "--compact"), web)

	_, stderr, code := h.run("list", "--project", "awb", "--all-projects")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--project and --all-projects are mutually exclusive")
}

// A subdirectory is reached by the upward search.
func TestDirectoryContextFromASubdirectory(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, ".awb.yaml"), []byte("project: awb\n"), 0o600))

	deep := filepath.Join(h.dir, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o700))
	t.Chdir(deep)

	id := h.create("from deep")
	assert.Contains(t, h.mustRun("list", "--compact"), id)
}

// The compact line's optional fields are identified by prefix and appear in a
// fixed order.
func TestCompactLineFormat(t *testing.T) {
	h := newHarness(t)
	blocker := h.create("blocker", "--project", "awb")
	id := h.create("Has \"quotes\" in it", "--project", "awb", "--priority", "1",
		"--type", "bug", "--label", "z", "--label", "a",
		"--assignee", "claude-1", "--blocked-by", blocker)

	assert.Equal(t,
		id+` P1 in_progress bug "Has \"quotes\" in it" @claude-1 #a #z !blocked`+"\n",
		h.mustRun("show", id, "--compact"))

	// awb blocked adds the blockers.
	assert.Equal(t,
		id+` P1 in_progress bug "Has \"quotes\" in it" @claude-1 #a #z !blocked blocked-by:`+
			blocker+"\n",
		h.mustRun("blocked", "--compact"))
}

// dep tree --compact prefixes each node with two spaces per level of depth.
func TestDepTreeCompact(t *testing.T) {
	h := newHarness(t)
	root := h.create("root", "--project", "awb")
	child := h.create("child", "--project", "awb", "--has-parent", root)
	grandchild := h.create("grandchild", "--project", "awb", "--has-parent", child)

	out := h.mustRun("dep", "tree", root, "--compact")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)

	assert.True(t, strings.HasPrefix(lines[0], root), "the root is unindented")
	assert.Equal(t, "  "+child, lines[1][:2+len(child)])
	assert.Equal(t, "    "+grandchild, lines[2][:4+len(grandchild)])
}

// Errors go to stderr as a single line, and as {"error": "..."} under --json.
func TestErrorOutput(t *testing.T) {
	h := newHarness(t)

	stdout, stderr, code := h.run("show", "awb-ffffff")
	assert.Equal(t, 3, code)
	assert.Empty(t, stdout)
	assert.Equal(t, 1, strings.Count(stderr, "\n"), "a single line")

	stdout, stderr, code = h.run("show", "awb-ffffff", "--json")
	assert.Equal(t, 3, code)
	assert.Empty(t, stdout)

	var payload domain.APIError
	require.NoError(t, json.Unmarshal([]byte(stderr), &payload))
	assert.NotEmpty(t, payload.Error)
}

// --description-file - reads the description from stdin, and stores the bytes
// exactly as given: a description is never trimmed.
func TestDescriptionFromStdin(t *testing.T) {
	h := newHarness(t)

	var out, errOut bytes.Buffer
	code := cli.Execute(t.Context(), "test", openAPI,
		[]string{"create", "t", "--project", "awb", "--description-file", "-"},
		&out, &errOut, strings.NewReader("  body\n\n"))
	require.Equal(t, 0, code, errOut.String())

	id := strings.TrimSpace(out.String())
	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", id, "--json")), &issue))
	assert.Equal(t, "  body\n\n", issue.Description,
		"a trailing line feed from a heredoc is part of the description")
}

// A grouping command rejects a name that is not one of its subcommands, so a
// removed spelling — project ls, add and rm, renamed to list, create and
// delete — is a usage error and not a silent help page.
func TestUnknownSubcommandIsAUsageError(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"project", "ls"}, {"project", "add", "k"}, {"project", "rm", "k"},
		{"project", "wat"}, {"dep", "wat"}, {"label", "wat"}, {"wat"},
	} {
		stdout, stderr, code := h.run(args...)
		assert.Equal(t, 2, code, args)
		assert.Empty(t, stdout, args)
		assert.Contains(t, stderr, "unknown command", args)
	}
}

func TestArgumentCountErrorsAreUsageErrors(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"show"},
		{"show", "one", "two"},
		{"list", "extra"},
		{"search"},
	} {
		stdout, stderr, code := h.run(args...)
		assert.Equal(t, 2, code, args)
		assert.Empty(t, stdout, args)
		assert.NotEmpty(t, stderr, args)
	}
}

func TestRepeatableStringFlagsDoNotSplitCommas(t *testing.T) {
	h := newHarness(t)
	_, _, code := h.run("create", "t", "--project", "awb", "--label", "one,two")
	assert.Equal(t, 2, code, "one flag occurrence is one label, not a comma-separated list")
}

func TestDescriptionFlagsAreMutuallyExclusive(t *testing.T) {
	h := newHarness(t)
	_, _, code := h.run("create", "t", "--project", "awb",
		"--description", "a", "--description-file", "-")
	assert.Equal(t, 2, code)
}

// An issue is reachable by any unambiguous prefix, and by a bare hash.
func TestIssueReferences(t *testing.T) {
	h := newHarness(t)
	id := h.create("t", "--project", "awb")
	_, hash, ok := domain.SplitID(id)
	require.True(t, ok)

	for _, ref := range []string{id, hash, hash[:3], strings.ToUpper(id)} {
		out := h.mustRun("show", ref, "--compact")
		assert.True(t, strings.HasPrefix(out, id), ref)
	}
}

func TestAgentGuide(t *testing.T) {
	h := newHarness(t)

	guide := h.mustRun("agent-guide")
	assert.Contains(t, guide, "awb ready --compact")
	assert.Contains(t, guide, "Exit codes")
	assert.Contains(t, guide, "awb --help")
	assert.Contains(t, guide, "awb <group> <command> --help")

	path := filepath.Join(h.dir, "AGENTS.md")
	require.NoError(t, os.WriteFile(path, []byte("# Project\n\nText.\n"), 0o600))

	h.mustRun("agent-guide", "--write", path)
	h.mustRun("agent-guide", "--write", path)

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(written)

	assert.Equal(t, 1, strings.Count(content, "<!-- awb:begin -->"),
		"a second run replaces the block rather than appending a duplicate")
	assert.Equal(t, 1, strings.Count(content, "<!-- awb:end -->"))
	assert.True(t, strings.HasPrefix(content, "# Project\n\nText.\n\n"),
		"the block is appended after a blank line, leaving the file's own text alone")
}

func TestAgentGuideCreatesTheFile(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.dir, "new.md")

	h.mustRun("agent-guide", "--write", path)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(written), "<!-- awb:begin -->"))
}

// A file holding only one of the two markers fails rather than gaining a
// second block.
func TestAgentGuideRefusesAHalfMarkedFile(t *testing.T) {
	h := newHarness(t)

	for _, content := range []string{
		"# Doc\n\n<!-- awb:begin -->\nstale\n",
		"# Doc\n\n<!-- awb:end -->\n",
		"# Doc\n\n<!-- awb:end -->\nbackwards\n<!-- awb:begin -->\n",
	} {
		path := filepath.Join(t.TempDir(), "AGENTS.md")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		_, _, code := h.run("agent-guide", "--write", path)
		assert.Equal(t, 1, code, content)
	}
}

func TestInstallSkillsCommand(t *testing.T) {
	h := newHarness(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	agents := filepath.Join(h.dir, "AGENTS.md")
	require.NoError(t, os.WriteFile(agents, []byte("project instructions\n"), 0o600))

	out := h.mustRun("agent-guide", "install-skills", "--harness", "claude")
	path := filepath.Join(home, ".claude", "skills", "awb")
	assert.Contains(t, out, "Installed awb skill for claude at "+path)
	definition, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(definition), "name: awb")
	assert.Contains(t, string(definition), "awb ready --compact")
	projectInstructions, err := os.ReadFile(agents)
	require.NoError(t, err)
	assert.Equal(t, "project instructions\n", string(projectInstructions),
		"install-skills is independent of the agent-guide --write mechanism")

	_, _, code := h.run("agent-guide", "install-skills", "--harness", "unknown")
	assert.Equal(t, 2, code)
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, "test\n", h.mustRun("--version"))
}

// init is idempotent and is the only command that creates the database.
func TestInitIsIdempotentAndUnique(t *testing.T) {
	h := newHarness(t)
	h.mustRun("init")
	h.mustRun("init")

	missing := filepath.Join(t.TempDir(), "elsewhere.db")
	t.Setenv("AWB_DB", missing)
	_, stderr, code := h.run("list")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, missing, "the message names the path")
	assert.Contains(t, stderr, "awb init")
}

// --mine is shorthand for the configured identity; ready rejects it.
func TestMine(t *testing.T) {
	h := newHarness(t)
	mine := h.create("mine", "--project", "awb", "--assignee", "mikael")
	h.create("theirs", "--project", "awb", "--assignee", "claude-1")

	out := h.mustRun("list", "--compact", "--mine")
	assert.Contains(t, out, mine)
	assert.Equal(t, 1, strings.Count(out, "\n"))

	_, _, code := h.run("ready", "--mine")
	assert.Equal(t, 2, code)
}

// The deleting summary must not claim a number one of the two modes cannot
// know. Regression: it used to report the count of *active* issues in remote
// mode, so a project holding one open and one closed issue reported "1 issue"
// remotely and "2" directly, and a project of only closed issues reported none
// at all while deleting them.
func TestProjectRemovalSummaryIsModeIndependent(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "create", "doomed")
	open := h.create("open one", "--project", "doomed")
	closed := h.create("closed one", "--project", "doomed")
	h.mustRun("close", closed)

	summary := h.mustRun("project", "delete", "doomed", "--force", "--cascade")

	// Whatever it says, it says nothing a remote client could not also say.
	assert.Contains(t, summary, "doomed")
	assert.NotContains(t, summary, "1 issue",
		"a count here would differ between direct and remote mode")

	// Both issues really went, closed one included.
	for _, id := range []string{open, closed} {
		_, _, code := h.run("show", id)
		assert.Equal(t, 3, code, id)
	}
}

// Without --cascade the summary is the plain one.
func TestProjectRemovalWithoutCascade(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "create", "empty")

	summary := h.mustRun("project", "delete", "empty", "--force")
	assert.Equal(t, "Deleted project empty.\n", summary)
}

// awb project show prints one project in each of the three modes, and reports a
// key that is not there as not found like every other lookup.
func TestProjectShow(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "update", "awb",
		"--description", "The **board** itself.\n")
	h.create("open one", "--project", "awb")

	out := h.mustRun("project", "show", "awb")
	assert.Contains(t, out, "awb")
	assert.Contains(t, out, "Agent Work Board")
	assert.Contains(t, out, "Open:")
	// There is no window here, so the description is the source text as written.
	assert.Contains(t, out, "The **board** itself.")

	assert.Equal(t, "awb 1 \"Agent Work Board\"\n", h.mustRun("project", "show", "awb", "--compact"),
		"the same line project list prints")

	var project domain.Project
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("project", "show", "awb", "--json")), &project))
	assert.Equal(t, "awb", project.Key)
	assert.Equal(t, "The **board** itself.\n", project.Description)
	assert.Equal(t, 1, project.ActiveIssues)

	_, _, code := h.run("project", "show", "nosuch")
	assert.Equal(t, 3, code)
}

// demoIssues reads every issue of the demo project, closed ones included.
func (h *harness) demoIssues() []domain.Issue {
	h.t.Helper()
	var issues []domain.Issue
	require.NoError(h.t, json.Unmarshal(
		[]byte(h.mustRun("list", "--project", "demo", "--include-closed", "--json")), &issues))
	return issues
}

// The demo data set exercises the whole of the fixed vocabulary. That is what
// "awb demo shows off the features" means in practice, and this is the test
// that fails when a vocabulary value is added without a row for it.
func TestDemoCoversTheVocabulary(t *testing.T) {
	h := newHarness(t)
	h.mustRun("demo")
	issues := h.demoIssues()
	require.NotEmpty(t, issues)

	types := map[domain.Type]bool{}
	statuses := map[domain.Status]bool{}
	priorities := map[int]bool{}
	relations := map[domain.RelationType]bool{}
	labels := map[string]bool{}
	assignees := map[string]bool{}
	var links, structured, closeReasons, severalBlockers int

	var epic string
	for i := range issues {
		issue := &issues[i]
		types[issue.Type] = true
		statuses[issue.Status] = true
		priorities[issue.Priority] = true
		for _, rel := range issue.Relations {
			relations[rel.Type] = true
		}
		for _, label := range issue.Labels {
			labels[label] = true
		}
		if issue.Assignee != "" {
			assignees[issue.Assignee] = true
		}
		links += len(issue.Links)
		// More than links: a description that exercises the Markdown a terminal
		// and the web UI draw.
		if strings.Contains(issue.Description, "## ") && strings.Contains(issue.Description, "**") {
			structured++
		}
		if issue.CloseReason != "" {
			closeReasons++
		}
		if len(issue.Blockers) > 1 {
			severalBlockers++
		}
		if issue.Type == domain.TypeEpic {
			epic = issue.ID
		}
	}

	for _, want := range domain.Types {
		assert.True(t, types[want], "no issue of type %s", want)
	}
	for _, want := range domain.Statuses {
		assert.True(t, statuses[want], "no issue with status %s", want)
	}
	for want := domain.MinPriority; want <= domain.MaxPriority; want++ {
		assert.True(t, priorities[want], "no issue at priority %d", want)
	}
	for _, want := range domain.RelationTypes {
		assert.True(t, relations[want], "no %s relation", want)
	}

	assert.Greater(t, len(labels), 1, "more than one label, so the label facets say something")
	assert.Greater(t, len(assignees), 1, "more than one assignee")
	assert.NotZero(t, links, "a description with Markdown links, so the derived link list is not empty")
	assert.NotZero(t, structured,
		"a description written as Markdown, so the rendered description has something to show")
	assert.NotZero(t, closeReasons, "a closed issue that records why")
	assert.NotZero(t, severalBlockers, "an issue with more than one blocker")

	// Both halves of readiness have something to show.
	assert.NotEmpty(t, h.mustRun("ready", "--project", "demo", "--compact"))
	assert.NotEmpty(t, h.mustRun("blocked", "--project", "demo", "--compact"))

	// The decomposition is more than one level deep, so dep tree shows a tree
	// rather than a list. A grandchild is indented two levels, four spaces.
	require.NotEmpty(t, epic, "an epic to root the decomposition at")
	assert.Contains(t, h.mustRun("dep", "tree", epic, "--compact"), "\n    ")
}

// awb demo prints a summary line — one of the deliberate exceptions to
// "mutating commands print nothing on success" — and the project itself under
// --json.
func TestDemoOutput(t *testing.T) {
	h := newHarness(t)
	assert.Contains(t, h.mustRun("demo"), "demo")

	var project domain.Project
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("demo", "--force", "--json")), &project))
	assert.Equal(t, "demo", project.Key)
	assert.NotZero(t, project.ActiveIssues, "the count is the one after the issues were created")
}

// A second run replaces the demo project wholesale, clearing whatever else is
// in it, and touches nothing outside it.
func TestDemoReplacesTheProject(t *testing.T) {
	h := newHarness(t)
	h.mustRun("demo")
	first := h.demoIssues()

	stray := h.create("not part of the data set", "--project", "demo")
	elsewhere := h.create("in another project", "--project", "awb")

	// Without --force it refuses, and changes nothing. The refusal depends on
	// what is stored, so it is a conflict rather than a usage error.
	_, _, code := h.run("demo")
	require.Equal(t, 4, code)
	assert.Len(t, h.demoIssues(), len(first)+1, "the refusal left the stray issue alone")

	h.mustRun("demo", "--force")

	assert.Len(t, h.demoIssues(), len(first), "the same data set, not a second copy")

	_, _, code = h.run("show", stray)
	assert.Equal(t, 3, code, "an issue the data set did not create is cleared with the rest")
	h.mustRun("show", elsewhere)
}

// The refusal is about the project existing, not about what it holds: an empty
// demo project still needs --force, because the command replaces the project
// rather than its contents.
func TestDemoRefusesAnExistingEmptyProject(t *testing.T) {
	h := newHarness(t)
	h.mustRun("project", "create", "demo")

	_, _, code := h.run("demo")
	assert.Equal(t, 4, code)

	h.mustRun("demo", "--force")
}

// Replacing the demo project drops the relations its issues were on either end
// of, so an issue in another project blocked by a demo issue becomes unblocked.
// No other project is created or deleted, but that is not the same as leaving
// everything outside alone.
func TestDemoClearsRelationsIntoOtherProjects(t *testing.T) {
	h := newHarness(t)
	h.mustRun("demo")

	// Any demo issue that is not closed will do; a closed one would not block.
	var blocker string
	for _, issue := range h.demoIssues() {
		if issue.Status != domain.StatusClosed {
			blocker = issue.ID
			break
		}
	}
	require.NotEmpty(t, blocker)

	dependent := h.create("waiting on the demo", "--project", "awb", "--blocked-by", blocker)

	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", dependent, "--json")), &issue))
	require.True(t, issue.Blocked)

	h.mustRun("demo", "--force")

	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", dependent, "--json")), &issue))
	assert.False(t, issue.Blocked, "the blocker went with the project it was in")
	assert.Empty(t, issue.Relations)
}

// awb attach: the file goes in the attachments directory beside the database
// and only its metadata goes in the database.
func TestAttach(t *testing.T) {
	h := newHarness(t)
	id := strings.TrimSpace(h.mustRun("create", "Parser crashes", "--project", "awb"))

	path := filepath.Join(h.dir, "trace.txt")
	require.NoError(t, os.WriteFile(path, []byte("boom\n"), 0o600))

	// attach add prints nothing: there is no id to print, and the caller
	// already knows the issue and the name, which is the whole reference.
	assert.Empty(t, h.mustRun("attach", "add", id, path))

	// The content is one file in the attachments directory, named by its digest.
	sum := sha256.Sum256([]byte("boom\n"))
	digest := hex.EncodeToString(sum[:])
	stored, err := os.ReadFile(filepath.Join(h.root(), "attachments", digest))
	require.NoError(t, err)
	assert.Equal(t, "boom\n", string(stored))

	// The compact line is the issue, size and digest — none of which can hold a
	// space — then the content type and the name as JSON strings. The sniffed
	// content type holds a space, which is why it is quoted.
	line := h.mustRun("attach", "list", id, "--compact")
	assert.Equal(t,
		id+" 5 "+digest+" \"text/plain; charset=utf-8\" \"trace.txt\"\n", line)

	// Read back the way the format says to: the first three fields split on
	// whitespace, then two JSON strings. The content type holds a space — the
	// sniffed default always does — so a consumer that split the whole line on
	// whitespace would see six fields, which is exactly why it is quoted.
	head, quoted, found := strings.Cut(strings.TrimSuffix(line, "\n"), " \"")
	require.True(t, found)
	assert.Equal(t, []string{id, "5", digest}, strings.Fields(head))

	decoder := json.NewDecoder(strings.NewReader("\"" + quoted))
	var contentType, name string
	require.NoError(t, decoder.Decode(&contentType))
	require.NoError(t, decoder.Decode(&name))
	assert.Equal(t, "text/plain; charset=utf-8", contentType)
	assert.Equal(t, "trace.txt", name)

	// The content comes back byte for byte, to stdout and to a file.
	assert.Equal(t, "boom\n", h.mustRun("attach", "get", id, "trace.txt"))
	out := filepath.Join(h.dir, "copy.txt")
	assert.Empty(t, h.mustRun("attach", "get", id, "trace.txt", "--output", out))
	written, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "boom\n", string(written))

	// The issue carries it as a derived array.
	var issue domain.Issue
	require.NoError(t, json.Unmarshal([]byte(h.mustRun("show", id, "--json")), &issue))
	require.Len(t, issue.Attachments, 1)
	assert.Equal(t, id, issue.Attachments[0].Issue)
	assert.Equal(t, "trace.txt", issue.Attachments[0].Name)

	// Deleting it takes the file with it, nothing else holding those bytes.
	assert.Contains(t, h.mustRun("attach", "delete", id, "trace.txt", "--force"), "trace.txt")
	_, err = os.Stat(filepath.Join(h.root(), "attachments", digest))
	assert.True(t, os.IsNotExist(err), "the content went with the last attachment")
}

// An issue holds at most one attachment under any one name, that pair being
// what identifies one.
func TestAttachOneNamePerIssue(t *testing.T) {
	h := newHarness(t)
	id := strings.TrimSpace(h.mustRun("create", "Parser crashes", "--project", "awb"))
	other := strings.TrimSpace(h.mustRun("create", "Tokeniser", "--project", "awb"))

	path := filepath.Join(h.dir, "trace.txt")
	require.NoError(t, os.WriteFile(path, []byte("boom\n"), 0o600))
	h.mustRun("attach", "add", id, path)

	_, stderr, code := h.run("attach", "add", id, path)
	assert.Equal(t, 4, code, "it depends on what is stored, so it is a conflict")
	assert.Contains(t, stderr, "trace.txt")

	// --name is the way past it, and another issue may hold the same name.
	h.mustRun("attach", "add", id, path, "--name", "trace-2.txt")
	h.mustRun("attach", "add", other, path)
}

// --name and --content-type override what the file says about itself, and
// "-" reads the content from stdin, which has no name of its own.
func TestAttachNameAndContentType(t *testing.T) {
	h := newHarness(t)
	id := strings.TrimSpace(h.mustRun("create", "Parser crashes", "--project", "awb"))

	path := filepath.Join(h.dir, "trace.txt")
	require.NoError(t, os.WriteFile(path, []byte("boom\n"), 0o600))

	h.mustRun("attach", "add", id, path, "--name", "renamed.log", "--content-type", "text/plain")

	var attachment domain.Attachment
	require.NoError(t, json.Unmarshal(
		[]byte(h.mustRun("attach", "show", id, "renamed.log", "--json")), &attachment))
	assert.Equal(t, "renamed.log", attachment.Name)
	assert.Equal(t, "text/plain", attachment.ContentType)

	// Reading from stdin needs a name, stdin having none.
	_, stderr, code := h.runStdin("boom\n", "attach", "add", id, "-")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--name")

	h.mustRunStdin("piped\n", "attach", "add", id, "-", "--name", "stdin.txt")
	assert.Equal(t, "piped\n", h.mustRun("attach", "get", id, "stdin.txt"))
}

// attach delete is destructive and takes the confirmation flag every
// destructive command takes.
func TestAttachDeleteNeedsForce(t *testing.T) {
	h := newHarness(t)
	id := strings.TrimSpace(h.mustRun("create", "Parser crashes", "--project", "awb"))
	path := filepath.Join(h.dir, "trace.txt")
	require.NoError(t, os.WriteFile(path, []byte("boom\n"), 0o600))
	h.mustRun("attach", "add", id, path)

	_, stderr, code := h.run("attach", "delete", id, "trace.txt")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--force")

	h.mustRun("attach", "show", id, "trace.txt")
}

// --attachments points the content at a directory of its own, which is what a
// separately mounted filesystem is for.
func TestAttachmentsDirectory(t *testing.T) {
	h := newHarness(t)
	elsewhere := filepath.Join(h.dir, "elsewhere")

	id := strings.TrimSpace(h.mustRun("create", "Parser crashes", "--project", "awb"))
	path := filepath.Join(h.dir, "trace.txt")
	require.NoError(t, os.WriteFile(path, []byte("boom\n"), 0o600))

	h.mustRun("attach", "add", id, path, "--attachments", elsewhere)

	sum := sha256.Sum256([]byte("boom\n"))
	_, err := os.Stat(filepath.Join(elsewhere, hex.EncodeToString(sum[:])))
	require.NoError(t, err, "the content went where --attachments said")

	assert.Equal(t, "boom\n",
		h.mustRun("attach", "get", id, "trace.txt", "--attachments", elsewhere))

	// Without the flag the content is not where the default directory is, so the
	// row promises what that directory does not hold.
	_, _, code := h.run("attach", "get", id, "trace.txt")
	assert.Equal(t, 1, code)
}

// awb init creates the attachments directory, so the whole layout exists as
// soon as it has run.
func TestInitCreatesTheAttachmentsDirectory(t *testing.T) {
	h := newHarness(t)
	info, err := os.Stat(filepath.Join(h.root(), "attachments"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// serve refuses a listen address it could never bind, before it opens the
// database or the port.
func TestServeRejectsAPortThatIsNotOne(t *testing.T) {
	h := newHarness(t)

	for _, port := range []string{"0", "-1", "65536"} {
		_, stderr, code := h.run("serve", "--port", port)
		assert.Equal(t, 2, code, port)
		assert.Contains(t, stderr, "--port", port)
	}
}

// --addr carried the port before --port existed, so the old form is refused
// rather than bound as a host of that name.
func TestServeRejectsAnAddressCarryingAPort(t *testing.T) {
	h := newHarness(t)

	_, stderr, code := h.run("serve", "--addr", "0.0.0.0:7777")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--port")
}

// A flag that could never work is reported as the usage error it is, rather
// than from behind an unrelated failure to find a database.
func TestServeChecksItsFlagsBeforeItOpensAnything(t *testing.T) {
	h := newHarness(t)
	t.Setenv("AWB_DB", filepath.Join(t.TempDir(), "nothing-here.db"))

	_, stderr, code := h.run("serve", "--public-url", "//example.com/awb")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--public-url")
}

// A --public-url that cannot be a base is refused at startup rather than
// serving a UI whose every relative URL is wrong.
func TestServeRejectsAnUnusablePublicURL(t *testing.T) {
	h := newHarness(t)

	// No scheme, so the path is the whole of it; and a path with no origin,
	// which is the half that says where a browser reaches the server.
	for _, publicURL := range []string{"example.com/awb", "/awb/"} {
		_, stderr, code := h.run("serve", "--public-url", publicURL)
		assert.Equal(t, 2, code, publicURL)
		assert.Contains(t, stderr, "--public-url", publicURL)
	}
}
