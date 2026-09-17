package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"

	"github.com/tofutools/awb/internal/awberr"
)

// MaxMetadataBytes bounds an issue's encoded metadata object. It is counted in
// bytes rather than code points, for the reason a description is: this is a
// blob nobody counts characters in.
const MaxMetadataBytes = 64 * 1024

// Metadata is the caller-owned JSON object an issue carries beside the fields
// awb itself gives meaning to. The top level is always an object; awb gives no
// meaning to what is under a key, so a value is any JSON — including a nested
// object or an array — carried rather than interpreted.
//
// What is kept is the value, not the spelling. Metadata is stored in one
// canonical form: every object's keys sorted at every depth, every array's
// order kept, every number the digits the caller wrote, and no insignificant
// whitespace. Two spellings of one JSON value therefore become one byte
// sequence, which is what lets an update that re-sends what it read decide it
// changed nothing, and what keeps a caller's own key order from becoming a
// version bump.
//
// An issue that has never been given any carries an empty object. That is
// structural rather than remembered: MarshalJSON writes {} for a nil map, so
// no surface can emit null or leave the field out.
type Metadata map[string]json.RawMessage

// MarshalJSON keeps an unset metadata object an empty one on every surface.
func (m Metadata) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]json.RawMessage(m))
}

// ParseMetadata reads an encoded metadata object: a --metadata argument, or a
// value a caller spelled out rather than one awb wrote.
//
// It must be an object, and there must be one. An array, a string, a number,
// null and nothing at all are each refused rather than turned into an empty
// object, because a caller that named the value meant to give one; the way to
// say "no metadata" is to leave the argument out, which never reaches here.
func ParseMetadata(encoded []byte) (Metadata, error) {
	// Checked before the decoder sees it, for the reason every other text field
	// is: a decoder replaces an invalid byte with U+FFFD, which is
	// indistinguishable from a U+FFFD the caller meant to send. A command line
	// argument is bytes rather than text, so this is where that arrives.
	if err := checkUTF8("metadata", string(encoded)); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, awberr.Usagef("metadata must be a JSON object")
	}
	parsed, err := DecodeMetadata(string(trimmed))
	if err != nil {
		return nil, awberr.Usagef("metadata is not a valid JSON object: %s", err)
	}
	return ValidateMetadata(parsed)
}

// DecodeMetadata reads an object's keys and its values' bytes, and checks
// nothing else.
//
// That is the whole of what reading the stored column needs: what is in it was
// canonicalized and bounded before it was written, so a listing does not pay
// for either again on every row. A caller's own bytes reach it through
// ParseMetadata, which follows it with the checks.
func DecodeMetadata(encoded string) (Metadata, error) {
	if encoded == "" || encoded == "{}" {
		return Metadata{}, nil
	}
	var decoded Metadata
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, awberr.Usagef("metadata must be a JSON object")
	}
	return decoded, nil
}

// ValidateMetadata is what a write a caller asked for applies: the canonical
// form, and the bound on what that form may weigh.
//
// The two are separate because only one of them is a gate. The canonical form
// is what the column holds, so everything that writes it owes it; the size
// bound is a rule about what a caller may ask for, and a restore — a copy of
// an object some other database already holds — deliberately does not apply
// the rules, exactly as it does not apply the prose gate.
func ValidateMetadata(m Metadata) (Metadata, error) {
	normalized, err := CanonicalMetadata(m)
	if err != nil {
		return nil, err
	}
	encoded, err := EncodeMetadata(normalized)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxMetadataBytes {
		return nil, awberr.Usagef("metadata is too large: maximum %d bytes", MaxMetadataBytes)
	}
	return normalized, nil
}

// CanonicalMetadata puts a metadata object into the one form awb stores it in.
// Canonicalizing here rather than at each boundary is what lets two objects be
// compared as bytes, which is how an update decides it changed nothing.
func CanonicalMetadata(m Metadata) (Metadata, error) {
	if len(m) == 0 {
		return Metadata{}, nil
	}
	normalized := make(Metadata, len(m))
	for key, value := range m {
		canonical, err := canonicalValue(value)
		if err != nil {
			return nil, awberr.Usagef("metadata value under %q is not valid JSON: %s", key, err)
		}
		normalized[key] = canonical
	}
	return normalized, nil
}

// canonicalValue rewrites one value into the form awb stores it in: every
// object's keys sorted, every array's order kept, and no insignificant
// whitespace. It is what makes key order insignificant at every depth rather
// than only at the top, where a Go map already makes it so.
//
// Numbers are decoded as json.Number — their own digits — rather than as
// float64, so a large integer, a trailing zero or a written exponent comes
// back as the caller wrote it instead of as a float's nearest rendering.
func canonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	// One value and nothing after it. The end is found by decoding again and
	// requiring io.EOF rather than by asking More(), which answers about the
	// array or object being parsed and so reports no more after the "1" of
	// "1]" — leaving a trailing byte to be silently dropped by the re-encoding
	// below.
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, awberr.Usagef("a metadata value must be one JSON value")
	}
	return json.Marshal(value)
}

// EncodeMetadata renders metadata as it is stored: one JSON object with its
// keys sorted, which is what encoding/json does with a map.
//
// It reports rather than swallows a value that is not JSON. Every value that
// reached here through a decoder re-encodes, so the error names a Metadata
// assembled in Go from bytes nothing validated — which must not become an
// empty object in the column instead.
func EncodeMetadata(m Metadata) (string, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return "", awberr.Usagef("metadata is not a valid JSON object: %s", err)
	}
	return string(encoded), nil
}

// MergeMetadata applies an update to stored metadata at the top level only: a
// key the update names takes the update's value whole, and one it does not
// name is kept. Nothing under a key is merged into, so the result follows from
// the two objects alone rather than from how deeply they happen to nest.
func MergeMetadata(stored, update Metadata) Metadata {
	merged := make(Metadata, len(stored)+len(update))
	maps.Copy(merged, stored)
	maps.Copy(merged, update)
	return merged
}

// EqualMetadata reports whether two normalized metadata objects hold the same
// keys and the same bytes under each, which is what decides that an update
// changed nothing.
func EqualMetadata(a, b Metadata) bool {
	return maps.EqualFunc(a, b, func(x, y json.RawMessage) bool { return bytes.Equal(x, y) })
}
