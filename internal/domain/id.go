package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/tofutools/awb/internal/awberr"
)

// HashLen is the number of hexadecimal characters in an issue ID's hash part.
// It is fixed at six, which is about 16 million values per project — few
// enough that collision handling is required, which is why MintHash draws a
// fresh salt and the insert retries.
const HashLen = 6

// SaltLen is the number of random bytes mixed into a hash.
const SaltLen = 16

// MintHash derives the hash part of an issue ID from the issue's content and a
// random salt, following the Beads hash-ID scheme:
//
//  1. concatenate the title's UTF-8 byte length as an unsigned 64-bit
//     big-endian integer, the title's UTF-8 bytes, the 24 ASCII bytes of
//     createdAt in the exact form YYYY-MM-DDTHH:MM:SS.sssZ, and the 16 raw salt
//     bytes;
//  2. take the SHA-256 digest of that sequence;
//  3. keep the first six characters of its lowercase hexadecimal encoding.
//
// Length-prefixing the only variable-length field makes the framing
// unambiguous without reserving a character that titles could otherwise use.
// The title and timestamp make IDs independently mintable without a counter;
// the salt distinguishes otherwise identical creations.
//
// IDs are not content-addressed and must not be reconstructed.
func MintHash(title, createdAt string, salt []byte) string {
	buf := make([]byte, 0, 8+len(title)+len(createdAt)+len(salt))
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(title)))
	buf = append(buf, title...)
	buf = append(buf, createdAt...)
	buf = append(buf, salt...)

	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])[:HashLen]
}

// NewSalt draws SaltLen bytes from crypto/rand. math/rand is deliberately not
// used.
func NewSalt() ([]byte, error) {
	salt := make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "generate salt")
	}
	return salt, nil
}

// MakeID joins a project key and a hash into an issue ID.
func MakeID(projectKey, hash string) string { return projectKey + "-" + hash }

// SplitID separates an issue ID into its project key and hash. Because a
// project key may itself contain hyphens, an ID is split on its *last* hyphen.
func SplitID(id string) (projectKey, hash string, ok bool) {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// IsHex reports whether s is non-empty and made only of lowercase hexadecimal
// digits, which is what an issue ID's hash part and any prefix of one look
// like.
func IsHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isHexLetter := r >= 'a' && r <= 'f'
		if !isDigit && !isHexLetter {
			return false
		}
	}
	return true
}

// IssueRef is a reference to one issue as the caller wrote it: a full ID, an
// unambiguous ID prefix, or a bare hash or hash prefix. Any non-empty prefix
// is allowed.
type IssueRef struct {
	// Project is the project key when the reference carried one, and "" when it
	// is a bare hash that has to be matched across the whole database.
	Project string
	// Hash is the hash or hash prefix to match.
	Hash string
	// Raw is what the caller wrote, for error messages.
	Raw string
}

// ParseIssueRef reads an issue reference. The argument is lower-cased before
// matching, so an ID typed in capitals resolves.
func ParseIssueRef(s string) (IssueRef, error) {
	raw := s
	// Lower-cased before matching, so an ID typed in capitals resolves — and
	// nothing else is touched, so a stray space is a mistake to report rather
	// than one to paper over.
	s = strings.ToLower(s)
	if s == "" {
		return IssueRef{}, awberr.Usagef("issue id must not be empty")
	}

	// A bare hash or hash prefix carries no project.
	if IsHex(s) {
		return IssueRef{Hash: s, Raw: raw}, nil
	}

	project, hash, ok := SplitID(s)
	if !ok || !IsHex(hash) {
		return IssueRef{}, awberr.Usagef(
			"invalid issue id %q: expected <project>-<hash> or a bare hash", raw)
	}
	if _, err := ValidateProjectKey(project); err != nil {
		return IssueRef{}, awberr.Usagef("invalid issue id %q: %s", raw, err.Error())
	}
	return IssueRef{Project: project, Hash: hash, Raw: raw}, nil
}
