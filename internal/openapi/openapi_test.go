package openapi_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/openapi"
)

// The document as it is on disk. main embeds this same file; a test reads it,
// so that what is checked here is the source the code is generated from rather
// than a copy of it.
func read(t *testing.T) *openapi.Document {
	t.Helper()
	raw, err := os.ReadFile("../../openapi.yaml")
	require.NoError(t, err)
	return openapi.New(raw)
}

// document parses the OpenAPI document.
func document(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(read(t).YAML(), &doc))
	return doc
}

// schema walks to components.schemas.<name>.
func schema(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	components, ok := doc["components"].(map[string]any)
	require.True(t, ok, "components")
	schemas, ok := components["schemas"].(map[string]any)
	require.True(t, ok, "components.schemas")
	value, ok := schemas[name].(map[string]any)
	require.True(t, ok, "components.schemas.%s", name)
	return value
}

func enumOf(t *testing.T, doc map[string]any, name string) []string {
	t.Helper()
	raw, ok := schema(t, doc, name)["enum"].([]any)
	require.True(t, ok, "%s.enum", name)

	values := make([]string, len(raw))
	for i, v := range raw {
		values[i], ok = v.(string)
		require.True(t, ok, "%s.enum[%d]", name, i)
	}
	return values
}

func TestDocumentParses(t *testing.T) {
	doc := document(t)
	assert.Equal(t, "3.1.0", doc["openapi"])

	info, ok := doc["info"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, info["version"], "info.version tracks the API, not the binary")

	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, paths)
}

func TestJSONIsTheSameDocument(t *testing.T) {
	doc := read(t)
	encoded, err := doc.JSON()
	require.NoError(t, err)

	var fromYAML any
	require.NoError(t, yaml.Unmarshal(doc.YAML(), &fromYAML))

	// Both sides are compared after a JSON round trip, so that YAML's integer
	// types and JSON's float64 do not stand in for a real difference.
	reencoded, err := json.Marshal(fromYAML)
	require.NoError(t, err)

	var normalisedYAML, normalisedJSON any
	require.NoError(t, json.Unmarshal(reencoded, &normalisedYAML))
	require.NoError(t, json.Unmarshal(encoded, &normalisedJSON))

	assert.Equal(t, normalisedYAML, normalisedJSON,
		"/openapi.json and /openapi.yaml are one document")
}

// The declared vocabulary must be exactly the one the Go code enforces, so a
// generated client validates what the CLI validates.
func TestEnumsMatchTheGoVocabulary(t *testing.T) {
	doc := document(t)

	types := make([]string, len(domain.Types))
	for i, v := range domain.Types {
		types[i] = string(v)
	}
	assert.Equal(t, types, enumOf(t, doc, "Type"))

	statuses := make([]string, len(domain.Statuses))
	for i, v := range domain.Statuses {
		statuses[i] = string(v)
	}
	assert.Equal(t, statuses, enumOf(t, doc, "Status"))

	relations := make([]string, len(domain.RelationTypes))
	for i, v := range domain.RelationTypes {
		relations[i] = string(v)
	}
	assert.Equal(t, relations, enumOf(t, doc, "RelationType"))

	assert.Equal(t,
		[]string{string(domain.DirectionOut), string(domain.DirectionIn)},
		enumOf(t, doc, "Direction"))
}

// number reads a YAML scalar as an int, whatever integer type the parser
// chose.
func number(t *testing.T, value any) int {
	t.Helper()
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case uint64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("not a number: %T", value)
		return 0
	}
}

func TestPriorityRangeMatches(t *testing.T) {
	priority := schema(t, document(t), "Priority")
	assert.Equal(t, domain.MinPriority, number(t, priority["minimum"]))
	assert.Equal(t, domain.MaxPriority, number(t, priority["maximum"]))
}

func TestLengthMaximaMatch(t *testing.T) {
	doc := document(t)
	cases := map[string]int{
		"Label":        domain.MaxLabelLen,
		"Assignee":     domain.MaxAssigneeLen,
		"ProjectKey":   domain.MaxProjectKeyLen,
		"UserFullName": domain.MaxUserFullNameLen,
	}
	for name, want := range cases {
		assert.Equal(t, want, number(t, schema(t, doc, name)["maxLength"]), name)
	}
}

// A default on one of the shared vocabulary schemas is inherited by every
// field that references it, IssuePatch's included, where the generated decoder
// would then fill in a type or a priority the caller did not send and quietly
// rewrite the issue. The creation defaults are stated in IssueCreate's prose
// instead; this is the assertion that keeps one from being written as a schema
// default again.
func TestTheVocabularySchemasCarryNoDefault(t *testing.T) {
	doc := document(t)
	for _, name := range []string{"Type", "Status", "Priority", "Timestamp"} {
		assert.NotContains(t, schema(t, doc, name), "default", name)
	}
}

// Every field of the Issue shape must be declared and required: every field is
// always present, so a consumer needs no absence handling.
func TestIssueSchemaCoversEveryField(t *testing.T) {
	doc := document(t)
	issue := schema(t, doc, "Issue")

	properties, ok := issue["properties"].(map[string]any)
	require.True(t, ok)
	rawRequired, ok := issue["required"].([]any)
	require.True(t, ok)

	required := make(map[string]bool, len(rawRequired))
	for _, name := range rawRequired {
		required[name.(string)] = true
	}

	for _, field := range []string{
		"id", "project", "title", "description", "type", "status", "priority",
		"labels", "assignees", "created_at", "updated_at",
		"blocked", "blockers", "relations", "links",
	} {
		assert.Contains(t, properties, field)
		assert.True(t, required[field], "%s must be required: every field is always present", field)
	}

	assert.Equal(t, false, issue["additionalProperties"],
		"there is one Issue shape and nothing else belongs in it")
}

func TestProjectSchemaCoversEveryField(t *testing.T) {
	project := schema(t, document(t), "Project")
	properties, ok := project["properties"].(map[string]any)
	require.True(t, ok)

	for _, field := range []string{
		"key", "name", "description", "active_issues", "created_at", "updated_at",
	} {
		assert.Contains(t, properties, field)
	}
}

func TestUserSchemasAlwaysCarryTheFullName(t *testing.T) {
	doc := document(t)
	for _, name := range []string{"User", "UserDirectoryEntry"} {
		user := schema(t, doc, name)
		properties, ok := user["properties"].(map[string]any)
		require.True(t, ok)
		assert.Contains(t, properties, "full_name", name)

		required, ok := user["required"].([]any)
		require.True(t, ok)
		assert.Contains(t, required, "full_name", name)
	}
}

// The endpoints the CLI has no counterpart for still have to be declared.
func TestPathsCoverTheWholeAPI(t *testing.T) {
	paths, ok := document(t)["paths"].(map[string]any)
	require.True(t, ok)

	for _, path := range []string{
		"/api/issues", "/api/issues/suggestions", "/api/issues/{id}", "/api/issues/{id}/claim",
		"/api/issues/{id}/release", "/api/issues/{id}/close", "/api/issues/{id}/reopen",
		"/api/issues/{id}/labels", "/api/issues/{id}/relations",
		"/api/issues/{id}/relations/{type}/{other}", "/api/issues/{id}/tree",
		"/api/ready", "/api/blocked", "/api/search",
		"/api/projects", "/api/projects/{key}",
		"/api/labels", "/api/assignees", "/api/identity",
	} {
		assert.Contains(t, paths, path)
	}
}

// The Basic scheme is declared and applied, and declared optional because a
// server whose database holds no user accepts requests carrying none.
func TestSecurityIsDeclaredAndOptional(t *testing.T) {
	doc := document(t)

	components, ok := doc["components"].(map[string]any)
	require.True(t, ok)
	schemes, ok := components["securitySchemes"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, schemes, "basicAuth")

	security, ok := doc["security"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, security)

	empty, ok := security[0].(map[string]any)
	require.True(t, ok, "the first entry declares the scheme optional")
	assert.Empty(t, empty)
}

// Every operation declares the default error response. It is what the
// generated server turns into one NewError method mapping awb's error taxonomy
// onto statuses; an operation without one would need its own error handling.
func TestEveryOperationDeclaresTheDefaultError(t *testing.T) {
	paths, ok := document(t)["paths"].(map[string]any)
	require.True(t, ok)

	for path, raw := range paths {
		item, ok := raw.(map[string]any)
		require.True(t, ok, path)
		for method, rawOperation := range item {
			if method == "parameters" {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			require.True(t, ok, "%s %s", method, path)
			responses, ok := operation["responses"].(map[string]any)
			require.True(t, ok, "%s %s", method, path)
			assert.Contains(t, responses, "default", "%s %s", method, path)
		}
	}
}

// Operations reads what each operation accepts, which is what the request
// strictness rules are enforced from. The listings are the interesting ones:
// the endpoints that fix a status set or an assignee filter for themselves
// declare neither, and the facet endpoints declare no sort.
func TestOperations(t *testing.T) {
	operations, err := read(t).Operations()
	require.NoError(t, err)
	require.Len(t, operations, 44)

	names := func(id string) []string {
		operation, ok := operations[id]
		require.True(t, ok, id)
		out := make([]string, 0, len(operation.QueryParameters))
		for name := range operation.QueryParameters {
			out = append(out, name)
		}
		return out
	}

	assert.ElementsMatch(t, []string{
		"status", "include-closed", "type", "priority", "priority-max", "label",
		"assignee", "unassigned", "project", "parent", "sort", "limit", "offset",
	}, names("listIssues"))
	assert.ElementsMatch(t, []string{
		"type", "priority", "priority-max", "label", "project", "parent", "sort",
		"limit", "offset",
	}, names("listReady"))
	assert.ElementsMatch(t, []string{
		"type", "priority", "priority-max", "label", "assignee", "unassigned",
		"project", "parent", "sort", "limit", "offset",
	}, names("listBlocked"))
	assert.ElementsMatch(t, []string{
		"q", "status", "include-closed", "type", "priority", "priority-max", "label",
		"assignee", "unassigned", "project", "parent", "limit", "offset",
	}, names("listLabels"))
	assert.ElementsMatch(t, names("listLabels"), names("listAssignees"))
	assert.ElementsMatch(t, []string{"label"}, names("removeLabel"))
	assert.ElementsMatch(t, []string{"cascade"}, names("deleteProject"))
	assert.ElementsMatch(t, []string{"name", "content-type"}, names("addAttachment"))
	assert.ElementsMatch(t, []string{"limit", "offset"}, names("listAttachments"))
	assert.ElementsMatch(t, []string{"kind", "limit", "offset"}, names("listIssueActivity"))
	assert.ElementsMatch(t, []string{"q", "limit"}, names("searchNavigation"))
	assert.Empty(t, names("listProjectPreferences"))
	assert.Empty(t, names("setProjectIgnored"))
	assert.Empty(t, names("getIssue"))

	assert.True(t, operations["createIssue"].TakesBody)
	assert.True(t, operations["addComment"].DeclaresJSONBody())
	assert.True(t, operations["claimIssue"].TakesBody, "an optional body is still a body")
	assert.False(t, operations["reopenIssue"].TakesBody)
	assert.False(t, operations["deleteIssue"].TakesBody)
	assert.False(t, operations["listIssues"].TakesBody)

	// The media types are what the content-type rule is applied from. Every
	// operation with a body declares JSON but the attachment upload, whose body
	// is a file.
	assert.True(t, operations["createIssue"].DeclaresJSONBody())
	assert.True(t, operations["addAttachment"].TakesBody)
	assert.False(t, operations["addAttachment"].DeclaresJSONBody())
	assert.True(t, operations["addAttachment"].AcceptsBodyType("application/octet-stream"))
	assert.True(t, operations["addAttachment"].AcceptsBodyType("Application/Octet-Stream"),
		"a media type is case-insensitive")

	// What follows the first ";" is parameters rather than the type, and the
	// comparison reduces the header itself so that no caller has to.
	assert.True(t, operations["createIssue"].AcceptsBodyType("application/json; charset=utf-8"))
	assert.True(t, operations["createIssue"].AcceptsBodyType("Application/JSON;charset=UTF-8"))
	assert.True(t, operations["addAttachment"].AcceptsBodyType("application/octet-stream; x=1"))
	assert.False(t, operations["createIssue"].AcceptsBodyType("application/jsonx"),
		"only the whole media type matches, not a prefix of one")
	assert.False(t, operations["createIssue"].AcceptsBodyType(""),
		"a body claiming nothing claims nothing useful")
	assert.False(t, operations["getIssue"].AcceptsBodyType("application/json"),
		"an operation that declares no body accepts none")
}
