package cli_test

import (
	"bytes"
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
	for _, name := range []string{"AWB_USER", "AWB_PASSWORD", "AWB_PROJECT", "AWB_COLOR"} {
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
	h.mustRun("project", "add", "awb", "--name", "Agent Work Board")
	return h
}

// run executes one command and returns its stdout, stderr and exit code.
func (h *harness) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	code = cli.Execute(h.t.Context(), "test", openAPI, args, &out, &errOut,
		strings.NewReader(""))
	return out.String(), errOut.String(), code
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
		{"project", "ls", "--json"},
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
	h.mustRun("project", "add", "web")
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
	h.mustRun("project", "add", "doomed")
	open := h.create("open one", "--project", "doomed")
	closed := h.create("closed one", "--project", "doomed")
	h.mustRun("close", closed)

	summary := h.mustRun("project", "rm", "doomed", "--force", "--cascade")

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
	h.mustRun("project", "add", "empty")

	summary := h.mustRun("project", "rm", "empty", "--force")
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
		"the same line project ls prints")

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
	h.mustRun("project", "add", "demo")

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
