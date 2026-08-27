package api_test

import (
	"encoding/json"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/domain"
)

// document parses the embedded OpenAPI document.
func document(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(api.YAML(), &doc))
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

// The document is embedded by a compiler directive, and a directive that stops
// being one — a stray space after the slashes — disables it silently, leaving an
// empty byte slice rather than an error. This is the assertion that makes that
// loud.
func TestDocumentIsEmbedded(t *testing.T) {
	require.NotEmpty(t, api.YAML(), "the OpenAPI document is not embedded")
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
	encoded, err := api.JSON()
	require.NoError(t, err)

	var fromYAML any
	require.NoError(t, yaml.Unmarshal(api.YAML(), &fromYAML))

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
	assert.Equal(t, domain.DefaultPriority, number(t, priority["default"]))
}

func TestLengthMaximaMatch(t *testing.T) {
	doc := document(t)
	cases := map[string]int{
		"Label":      domain.MaxLabelLen,
		"Assignee":   domain.MaxAssigneeLen,
		"ProjectKey": domain.MaxProjectKeyLen,
	}
	for name, want := range cases {
		assert.Equal(t, want, number(t, schema(t, doc, name)["maxLength"]), name)
	}
}

func TestDefaultsMatch(t *testing.T) {
	doc := document(t)
	assert.Equal(t, string(domain.DefaultType), schema(t, doc, "Type")["default"])
	assert.Equal(t, string(domain.DefaultStatus), schema(t, doc, "Status")["default"])
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
		"labels", "assignee", "close_reason", "created_at", "updated_at",
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

// The endpoints the CLI has no counterpart for still have to be declared.
func TestPathsCoverTheWholeAPI(t *testing.T) {
	paths, ok := document(t)["paths"].(map[string]any)
	require.True(t, ok)

	for _, path := range []string{
		"/api/issues", "/api/issues/{id}", "/api/issues/{id}/claim",
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
// server started without --basic-auth-file accepts requests carrying none.
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
