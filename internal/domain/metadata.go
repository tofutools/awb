package domain

import (
	"bytes"
	"encoding/json"
	"maps"

	"github.com/tofutools/awb/internal/awberr"
)

// MaxMetadataBytes bounds an issue's encoded metadata object. It is counted in
// bytes rather than code points, for the reason a description is: this is a
// blob nobody counts characters in.
const MaxMetadataBytes = 64 * 1024

// Metadata is the caller-owned JSON object an issue carries beside the fields
// awb itself gives meaning to. The top level is always an object; awb never
// reads what is under a key, so a value is any JSON — including a nested
// object or an array — kept whole rather than interpreted.
//
// Kept whole is a promise about the value, not about its bytes. What is stored
// is the canonical encoding, so a value comes back compacted and escaped as
// encoding/json writes it: every parser reads the same value out of both, and
// two encodings of one object are the same bytes.
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

// ParseMetadata reads an encoded metadata object — a request body's field, a
// --metadata argument, or the stored column.
//
// The top level must be an object: an array, a string, a number or null is
// refused rather than wrapped in one, because a caller who sent one of those
// did not mean an object of keys.
func ParseMetadata(encoded []byte) (Metadata, error) {
	// Checked before the decoder sees it, for the reason every other text field
	// is: a decoder replaces an invalid byte with U+FFFD, which is
	// indistinguishable from a U+FFFD the caller meant to send. A command line
	// argument is bytes rather than text, so this is where that arrives.
	if err := checkUTF8("metadata", string(encoded)); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(encoded)
	if len(trimmed) == 0 {
		return Metadata{}, nil
	}
	if trimmed[0] != '{' {
		return nil, awberr.Usagef("metadata must be a JSON object")
	}
	parsed, err := DecodeMetadata(string(trimmed))
	if err != nil {
		return nil, awberr.Usagef("metadata is not a valid JSON object: %s", err)
	}
	return ValidateMetadata(parsed)
}

// DecodeMetadata reads metadata back from the canonical form EncodeMetadata
// writes. It checks the syntax and nothing else: the size bound and the
// per-value normalization were applied before the value was stored, so a
// listing does not pay for them again on every row.
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

// ValidateMetadata normalizes a metadata object into the one form awb stores:
// each value compacted, and the whole within the size bound. Normalizing here
// rather than at the boundary is what lets two objects be compared as bytes,
// which is how an update decides it changed nothing.
func ValidateMetadata(m Metadata) (Metadata, error) {
	if len(m) == 0 {
		return Metadata{}, nil
	}
	encoded, err := json.Marshal(map[string]json.RawMessage(m))
	if err != nil {
		return nil, awberr.Usagef("metadata is not a valid JSON object: %s", err)
	}
	if len(encoded) > MaxMetadataBytes {
		return nil, awberr.Usagef("metadata is too large: maximum %d bytes", MaxMetadataBytes)
	}
	var normalized Metadata
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, awberr.Usagef("metadata is not a valid JSON object: %s", err)
	}
	return normalized, nil
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
