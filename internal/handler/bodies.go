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

// bodyWasCarried reports whether the request carried a body at all. That is a
// question about bytes on the wire: a body of whitespace is still a body, and
// still has to declare what it is. A length of -1 is a chunked body, whose
// length is not known until it is read and which is therefore carrying one.
func bodyWasCarried(r *http.Request) bool { return r.ContentLength != 0 }

// claimsJSON reports whether the request declares a JSON body.
func claimsJSON(r *http.Request) bool {
	mediaType, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	return strings.TrimSpace(mediaType) == "application/json"
}

// rejectBody refuses a body on an operation that declares none. Ignoring one
// would let a client believe it had said something the server never read, so
// what is refused is any body at all rather than any body that says something.
func rejectBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		// http.MaxBytesHandler surfaces the transport cap as a read error, and
		// NewError turns that into the status that describes it.
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return awberr.Wrap(awberr.Runtime, err, "read request body")
	}
	if len(raw) > 0 {
		return awberr.Usagef("this endpoint takes no request body")
	}
	return nil
}

// holdsNoValue reports whether the body holds no JSON value at all, which only
// whitespace does. That is a different question from whether a body was
// carried: a body of whitespace is one, and still has to declare what it is.
func holdsNoValue(raw []byte) bool { return len(bytes.TrimSpace(raw)) == 0 }

// holdsNull reports whether a JSON null appears anywhere in the body.
func holdsNull(raw []byte) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return hasNull(value)
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

// checkText applies the UTF-8 half of the input rules to a request body.
//
// A byte sequence that is not well-formed UTF-8 is rejected rather than
// repaired, so nothing is stored that the caller did not send. A JSON escape
// denoting an unpaired surrogate, such as "\ud800", is rejected for the same
// reason: it is not a character, and a decoder that quietly turned it into
// U+FFFD would store what the caller did not send.
func checkText(raw []byte) error {
	if !utf8.Valid(raw) {
		return awberr.Usagef("request body is not valid UTF-8")
	}
	return checkSurrogateEscapes(raw)
}

// checkSurrogateEscapes scans for \uXXXX escapes denoting surrogates and
// requires every high one to be followed immediately by a matching low one.
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
