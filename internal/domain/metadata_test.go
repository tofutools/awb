package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/domain"
)

// The top level is an object and nothing else. A caller that sent an array, a
// string or null did not mean an object of keys, so none of them is wrapped in
// one.
// encodedMetadata is the stored form of one metadata object: keys sorted,
// values compacted. Comparing that string rather than the map is what pins the
// ordering down as well as the content.
func encodedMetadata(t *testing.T, metadata domain.Metadata) string {
	t.Helper()
	encoded, err := domain.EncodeMetadata(metadata)
	require.NoError(t, err)
	return encoded
}

func TestParseMetadataRequiresAnObject(t *testing.T) {
	for _, value := range []string{"[]", `"text"`, "42", "null", "true", "{", `{"a"}`} {
		_, err := domain.ParseMetadata([]byte(value))
		assertUsage(t, err, value)
	}

	// Nothing at all is an empty object rather than an error: it is what a
	// command line flag that was never given carries.
	for _, value := range []string{"", "   "} {
		got, err := domain.ParseMetadata([]byte(value))
		require.NoError(t, err, value)
		assert.Equal(t, domain.Metadata{}, got, value)
	}
}

// Invalid UTF-8 is refused rather than repaired, so nothing is stored that the
// caller did not send.
func TestParseMetadataRefusesInvalidUTF8(t *testing.T) {
	_, err := domain.ParseMetadata([]byte("{\"k\":\"\xff\"}"))
	assertUsage(t, err)
}

// A value is any JSON and awb never reads into it, so a nested object or array
// comes back exactly as it went in — only compacted, which is the form stored.
func TestParseMetadataKeepsValuesVerbatim(t *testing.T) {
	got, err := domain.ParseMetadata([]byte(`{
		"source": "github",
		"external_id": 4711,
		"nested": { "b": [1, 2, {"c": null}], "a": true },
		"absent": null
	}`))
	require.NoError(t, err)

	assert.JSONEq(t, `"github"`, string(got["source"]))
	assert.Equal(t, `{"b":[1,2,{"c":null}],"a":true}`, string(got["nested"]),
		"a value is compacted but never reordered or read into")
	assert.Equal(t, "null", string(got["absent"]),
		"a null value is a value the caller stored, not an absent key")
}

// The canonical encoding is encoding/json's, which escapes <, > and & inside a
// value. That changes the bytes and not the value: what comes back out of a
// parser is what went in, and one object always encodes to one byte sequence.
func TestMetadataValuesSurviveReEscaping(t *testing.T) {
	parsed, err := domain.ParseMetadata([]byte(`{"html":"<b>&</b>","unicode":"café ☕"}`))
	require.NoError(t, err)

	stored := encodedMetadata(t, parsed)
	assert.Equal(t, `{"html":"\u003cb\u003e\u0026\u003c/b\u003e","unicode":"café ☕"}`, stored,
		"the bytes are encoding/json's, which escapes <, > and & and leaves other runes alone")

	var readBack map[string]string
	require.NoError(t, json.Unmarshal([]byte(stored), &readBack))
	assert.Equal(t, map[string]string{"html": "<b>&</b>", "unicode": "café ☕"}, readBack)

	// And re-encoding what was stored is a fixed point, so a value does not
	// drift further with each write.
	reparsed, err := domain.ParseMetadata([]byte(stored))
	require.NoError(t, err)
	assert.Equal(t, stored, encodedMetadata(t, reparsed))
}

// The stored form has its keys sorted, because that is what a Go map encodes
// to, so two encodings of the same object are the same bytes.
func TestEncodeMetadataIsCanonical(t *testing.T) {
	first, err := domain.ParseMetadata([]byte(`{"b":1,"a":2}`))
	require.NoError(t, err)
	second, err := domain.ParseMetadata([]byte(`{"a":2,"b":1}`))
	require.NoError(t, err)

	assert.Equal(t, `{"a":2,"b":1}`, encodedMetadata(t, first))
	assert.Equal(t, encodedMetadata(t, first), encodedMetadata(t, second))
	assert.True(t, domain.EqualMetadata(first, second))

	// An unset object is the empty one on every surface, never null.
	assert.Equal(t, "{}", encodedMetadata(t, nil))

	// A value nothing validated is reported rather than stored as {}.
	_, err = domain.EncodeMetadata(domain.Metadata{"k": json.RawMessage("not json")})
	assertUsage(t, err)
	encoded, err := json.Marshal(domain.Issue{})
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"metadata":{}`)
}

// What EncodeMetadata writes, DecodeMetadata reads, and what it reads is the
// object that was written.
func TestDecodeMetadataRoundTrips(t *testing.T) {
	parsed, err := domain.ParseMetadata([]byte(`{"a":{"deep":[1]},"b":"two"}`))
	require.NoError(t, err)
	decoded, err := domain.DecodeMetadata(encodedMetadata(t, parsed))
	require.NoError(t, err)
	assert.True(t, domain.EqualMetadata(parsed, decoded))

	for _, stored := range []string{"", "{}"} {
		empty, decodeErr := domain.DecodeMetadata(stored)
		require.NoError(t, decodeErr, stored)
		assert.Empty(t, empty, stored)
	}
	for _, stored := range []string{"null", "[1]", "oops"} {
		_, decodeErr := domain.DecodeMetadata(stored)
		assert.Error(t, decodeErr, stored)
	}
}

// The merge is top-level only: a key the update names takes its value whole,
// and one it does not name survives untouched. A nested object is replaced
// rather than merged into, so the result follows from the two objects alone.
func TestMergeMetadataIsTopLevelOnly(t *testing.T) {
	stored, err := domain.ParseMetadata([]byte(`{"keep":1,"nested":{"a":1,"b":2},"replace":"old"}`))
	require.NoError(t, err)
	update, err := domain.ParseMetadata([]byte(`{"nested":{"a":9},"replace":"new","added":true}`))
	require.NoError(t, err)

	merged, err := domain.ValidateMetadata(domain.MergeMetadata(stored, update))
	require.NoError(t, err)
	assert.Equal(t, `{"added":true,"keep":1,"nested":{"a":9},"replace":"new"}`,
		encodedMetadata(t, merged))

	// Merging nothing in leaves the stored object exactly as it was, which is
	// what makes an update carrying {} a deliberate no-op.
	assert.True(t, domain.EqualMetadata(stored, domain.MergeMetadata(stored, domain.Metadata{})))
}

func TestValidateMetadataBoundsTheEncodedObject(t *testing.T) {
	fits := domain.Metadata{"k": json.RawMessage(
		`"` + strings.Repeat("x", domain.MaxMetadataBytes-10) + `"`)}
	_, err := domain.ValidateMetadata(fits)
	require.NoError(t, err)

	over := domain.Metadata{"k": json.RawMessage(
		`"` + strings.Repeat("x", domain.MaxMetadataBytes) + `"`)}
	_, err = domain.ValidateMetadata(over)
	assertUsage(t, err)
}
