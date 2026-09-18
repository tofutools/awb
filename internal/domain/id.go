package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/tofutools/awb/internal/awberr"
)

// HashLen is the number of hexadecimal characters in an issue ID's hash part.
// It is fixed at six, which is about 16 million values per workspace.
const HashLen = 6

// BoardViewIDBytes makes view IDs long enough to be safely shared as
// unguessable URLs without a workspace prefix or a collision-retry loop.
const BoardViewIDBytes = 12

// IssueHash derives the hash part of a client-created issue ID. The clients
// concatenate the creating identity, title, effective type and description,
// hash the UTF-8 bytes with SHA-256 and retain the first six lowercase hex
// characters. The server validates the resulting ID's shape and workspace,
// but deliberately does not require clients to use this algorithm.
func IssueHash(identity, title string, typ Type, description string) string {
	sum := sha256.Sum256([]byte(identity + title + string(typ) + description))
	return hex.EncodeToString(sum[:])[:HashLen]
}

// NewBoardViewID mints the stable, opaque identifier of a saved board view.
func NewBoardViewID() (string, error) {
	raw := make([]byte, BoardViewIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "generate board view id")
	}
	return "view-" + hex.EncodeToString(raw), nil
}

// ValidateBoardViewID refuses path values that are not IDs awb can mint.
func ValidateBoardViewID(s string) (string, error) {
	const prefix = "view-"
	if !strings.HasPrefix(s, prefix) || len(s) != len(prefix)+BoardViewIDBytes*2 || !IsHex(s[len(prefix):]) {
		return "", awberr.Usagef("invalid board view id %q", s)
	}
	return s, nil
}

// MakeID joins a workspace key and a hash into an issue ID.
func MakeID(workspaceKey, hash string) string { return workspaceKey + "-" + hash }

// SplitID separates an issue ID into its workspace key and hash. Because a
// workspace key may itself contain hyphens, an ID is split on its *last* hyphen.
func SplitID(id string) (workspaceKey, hash string, ok bool) {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// ValidateIssueID refuses abbreviated references where a persisted foreign key
// requires one immutable issue ID.
func ValidateIssueID(s string) (string, error) {
	workspace, hash, ok := SplitID(s)
	if !ok || len(hash) != HashLen || !IsHex(hash) {
		return "", awberr.Usagef("invalid issue id %q", s)
	}
	if _, err := ValidateWorkspaceKey(workspace); err != nil {
		return "", awberr.Usagef("invalid issue id %q: %s", s, err.Error())
	}
	return s, nil
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
	// Workspace is the workspace key when the reference carried one, and "" when it
	// is a bare hash that has to be matched across the whole database.
	Workspace string
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

	// A bare hash or hash prefix carries no workspace.
	if IsHex(s) {
		return IssueRef{Hash: s, Raw: raw}, nil
	}

	workspace, hash, ok := SplitID(s)
	if !ok || !IsHex(hash) {
		return IssueRef{}, awberr.Usagef(
			"invalid issue id %q: expected <workspace>-<hash> or a bare hash", raw)
	}
	if _, err := ValidateWorkspaceKey(workspace); err != nil {
		return IssueRef{}, awberr.Usagef("invalid issue id %q: %s", raw, err.Error())
	}
	return IssueRef{Workspace: workspace, Hash: hash, Raw: raw}, nil
}
