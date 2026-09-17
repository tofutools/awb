package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tofutools/awb/internal/domain"
)

// encodedMetadata is the stored form of one metadata object: keys sorted at
// every depth, values compacted. Comparing that string rather than the map is
// what pins the ordering down as well as the content.
func encodedMetadata(t *testing.T, metadata domain.Metadata) string {
	t.Helper()
	encoded, err := domain.EncodeMetadata(metadata)
	require.NoError(t, err)
	return encoded
}

// The top level is an object and nothing else. A caller that spelled out an
// array, a string, null — or nothing at all — named a value and did not supply
// an object of keys, so none of them is wrapped in one. Saying "no metadata" is
// leaving the argument out, which never reaches here.
func TestParseMetadataRequiresAnObject(t *testing.T) {
	for _, value := range []string{"[]", `"text"`, "42", "null", "true", "{", `{"a"}`, "", "   "} {
		_, err := domain.ParseMetadata([]byte(value))
		assertUsage(t, err, value)
	}
}

// Invalid UTF-8 is refused rather than repaired, so nothing is stored that the
// caller did not send.
func TestParseMetadataRefusesInvalidUTF8(t *testing.T) {
	_, err := domain.ParseMetadata([]byte("{\"k\":\"\xff\"}"))
	assertUsage(t, err)
}

// A value is any JSON, carried rather than interpreted, and stored in one
// canonical form: objects sorted at every depth, arrays left in their order,
// numbers the digits the caller wrote.
func TestParseMetadataCanonicalizesValues(t *testing.T) {
	got, err := domain.ParseMetadata([]byte(`{
		"source": "github",
		"external_id": 4711,
		"nested": { "b": [1, 2, {"d": null, "c": 3}], "a": true },
		"absent": null
	}`))
	require.NoError(t, err)

	assert.Equal(t, `"github"`, string(got["source"]))
	assert.Equal(t, `{"a":true,"b":[1,2,{"c":3,"d":null}]}`, string(got["nested"]),
		"every object's keys are sorted, at every depth; an array keeps its order")
	assert.Equal(t, "null", string(got["absent"]),
		"a null value is a value the caller stored, not an absent key")
}

// Canonicalizing must not round a number through float64: a value awb gives no
// meaning to has to come back as the caller wrote it.
func TestParseMetadataKeepsNumbersAsWritten(t *testing.T) {
	got, err := domain.ParseMetadata([]byte(
		`{"big":9007199254740993,"zero":1.0,"exp":1e3,"neg":-0.00000001}`))
	require.NoError(t, err)

	assert.Equal(t, "9007199254740993", string(got["big"]), "an integer past float64's range")
	assert.Equal(t, "1.0", string(got["zero"]))
	assert.Equal(t, "1e3", string(got["exp"]))
	assert.Equal(t, "-0.00000001", string(got["neg"]))
}

// Two spellings of one value are one stored object, so re-sending what was read
// with the keys in another order is not a change. Array order is meaning rather
// than spelling, so reordering one is.
func TestMetadataEqualityIgnoresKeyOrderAtEveryDepth(t *testing.T) {
	first, err := domain.ParseMetadata([]byte(`{"a":{"x":1,"y":[{"p":1,"q":2}]},"b":2}`))
	require.NoError(t, err)
	second, err := domain.ParseMetadata([]byte(`{"b":2,"a":{"y":[{"q":2,"p":1}],"x":1}}`))
	require.NoError(t, err)

	assert.True(t, domain.EqualMetadata(first, second))
	assert.Equal(t, encodedMetadata(t, first), encodedMetadata(t, second))

	swapped, err := domain.ParseMetadata([]byte(`{"a":{"x":1,"y":[{"q":2,"p":1},{"z":0}]},"b":2}`))
	require.NoError(t, err)
	assert.False(t, domain.EqualMetadata(first, swapped))
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
