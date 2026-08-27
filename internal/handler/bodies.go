package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tofutools/awb/internal/awberr"
)

var (
	// errUnsupportedMediaType is a request carrying a body without a JSON content
	// type, one of the statuses with no exit code behind it (415).
	errUnsupportedMediaType = errors.New("request body must be application/json")
	// errBodyTooLarge is a body over the transport cap (413).
	errBodyTooLarge = errors.New("request body is too large")
)

// readBody reads the whole request body, turning the transport cap into the
// status that describes it.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		// http.MaxBytesHandler surfaces the cap as a read error.
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, errBodyTooLarge
		}
		return nil, awberr.Wrap(awberr.Runtime, err, "read request body")
	}
	return raw, nil
}

// checkContentType requires a JSON content type of a request that carries a
// body.
//
// A missing header is not a JSON content type either: the rule is about what
// the body claims to be, and a body claiming nothing claims nothing useful. It
// is checked only once a body is known to be present, so a request that sends
// none is never asked to describe it.
func checkContentType(r *http.Request) error {
	mediaType, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if strings.TrimSpace(mediaType) != "application/json" {
		return errUnsupportedMediaType
	}
	return nil
}

// Two different questions are asked about a request body, and they must not be
// confused.
//
// *Was a body carried?* That is about bytes on the wire: a body of whitespace
// is still a body, and still has to declare what it is. It decides whether a
// content type is required and whether an endpoint that takes no body has been
// sent one.
//
// *Does the body hold a JSON value?* That is about content, and only whitespace
// holds nothing. It decides whether a required body is missing.
//
// Deciding presence by the trimmed length would let a whitespace body slip past
// both the content-type rule and the no-body rule.

func bodyWasCarried(raw []byte) bool { return len(raw) > 0 }

func holdsNoValue(raw []byte) bool { return len(bytes.TrimSpace(raw)) == 0 }

// decodeBody reads a required JSON request body into out, applying the API's
// strict body rules.
//
// Any unrecognised field name is rejected. A field present with a JSON null is
// a type error, because no field of an Issue is ever null — an unset string is
// "" — so there is no third state to encode. A body that is well-formed JSON
// but wrong, and one that is not well-formed JSON at all, are both 400: those
// are what the client asked for rather than how it asked.
func decodeBody(r *http.Request, out any) error {
	raw, err := readBody(r)
	if err != nil {
		return err
	}
	if bodyWasCarried(raw) {
		if err := checkContentType(r); err != nil {
			return err
		}
	}
	if holdsNoValue(raw) {
		return awberr.Usagef("a request body is required")
	}
	return decodeRaw(raw, out)
}

// decodeOptionalBody is decodeBody for an endpoint whose body may be omitted
// entirely, such as claim. A body that was carried must still declare what it
// is; one that holds no value is the same as none at all, and the defaults
// apply.
func decodeOptionalBody(r *http.Request, out any) error {
	raw, err := readBody(r)
	if err != nil {
		return err
	}
	if !bodyWasCarried(raw) {
		return nil
	}
	if err := checkContentType(r); err != nil {
		return err
	}
	if holdsNoValue(raw) {
		return nil
	}
	return decodeRaw(raw, out)
}

// rejectBody refuses a body on an endpoint that takes none. Ignoring one would
// let a client believe it had said something the server never read, so what is
// refused is any body at all rather than any body that says something.
func rejectBody(r *http.Request) error {
	raw, err := readBody(r)
	if err != nil {
		return err
	}
	if bodyWasCarried(raw) {
		return awberr.Usagef("this endpoint takes no request body")
	}
	return nil
}

func decodeRaw(raw []byte, out any) error {
	if err := checkJSONText(raw); err != nil {
		return err
	}
	if err := checkNoNulls(raw); err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return awberr.Usagef("invalid request body: %s", err.Error())
	}
	if dec.More() {
		return awberr.Usagef("invalid request body: it holds more than one JSON value")
	}
	return nil
}

// checkJSONText applies the UTF-8 half of the input rules to a request body.
//
// A byte sequence that is not well-formed UTF-8 is rejected rather than
// repaired, so nothing is stored that the caller did not send. A JSON escape
// denoting an unpaired surrogate, such as "\ud800", is rejected for the same
// reason: it is not a character, and a decoder that quietly turned it into
// U+FFFD would store what the caller did not send.
func checkJSONText(raw []byte) error {
	if !utf8.Valid(raw) {
		return awberr.Usagef("request body is not valid UTF-8")
	}
	return checkSurrogateEscapes(raw)
}

// checkSurrogateEscapes scans for \uXXXX escapes denoting surrogates and
// requires every high one to be followed immediately by a matching low one.
//
// encoding/json silently replaces an unpaired surrogate with U+FFFD, which
// would be indistinguishable from a literal U+FFFD the caller meant to send,
// so the check has to happen on the raw bytes before decoding.
func checkSurrogateEscapes(raw []byte) error {
	unpaired := func(value int) error {
		return awberr.Usagef(
			"request body holds an unpaired surrogate escape \\u%04X, which is not a character", value)
	}

	for i := 0; i+6 <= len(raw); i++ {
		if raw[i] != '\\' || raw[i+1] != 'u' {
			continue
		}
		// A backslash that is itself escaped does not begin an escape.
		if countPrecedingBackslashes(raw, i)%2 == 1 {
			continue
		}
		value, ok := parseHex4(raw[i+2 : i+6])
		if !ok {
			// A malformed escape is the decoder's business to report.
			continue
		}
		if !utf16.IsSurrogate(rune(value)) {
			i += 5
			continue
		}
		// A low surrogate reached first has nothing in front of it.
		if value >= 0xDC00 {
			return unpaired(value)
		}
		// A high surrogate must be followed immediately by a low one.
		next := i + 6
		if next+6 > len(raw) || raw[next] != '\\' || raw[next+1] != 'u' {
			return unpaired(value)
		}
		low, ok := parseHex4(raw[next+2 : next+6])
		if !ok || low < 0xDC00 || low > 0xDFFF {
			return unpaired(value)
		}
		i = next + 5
	}
	return nil
}

func countPrecedingBackslashes(raw []byte, i int) int {
	count := 0
	for j := i - 1; j >= 0 && raw[j] == '\\'; j-- {
		count++
	}
	return count
}

func parseHex4(b []byte) (int, bool) {
	if len(b) != 4 {
		return 0, false
	}
	value := 0
	for _, c := range b {
		var digit int
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c >= 'a' && c <= 'f':
			digit = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = int(c-'A') + 10
		default:
			return 0, false
		}
		value = value*16 + digit
	}
	return value, true
}

// checkNoNulls rejects any JSON null anywhere in the body: everything these
// bodies say turns on a field being present or absent, and null is neither, so
// a field present with a null is a type error answered 400, exactly as a
// number would be where a string belongs.
func checkNoNulls(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return awberr.Usagef("invalid request body: %s", err.Error())
	}
	if hasNull(value) {
		return awberr.Usagef(
			"request body holds a null: clear a value with \"\" and leave it alone by omitting it")
	}
	return nil
}

func hasNull(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range v {
			if hasNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasNull(item) {
				return true
			}
		}
	}
	return false
}
