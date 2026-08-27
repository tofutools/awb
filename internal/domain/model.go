package domain

import (
	"cmp"
	"slices"
	"time"
)

// TimeFormat is the timestamp form SPEC §2.2 fixes: UTC, RFC 3339 with
// millisecond precision, e.g. 2026-08-26T09:12:03.412Z. It is exactly 24 ASCII
// bytes, which is what the hash derivation of §8 relies on.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// FormatTime renders t in TimeFormat, in UTC.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// ParseTime reads a timestamp in TimeFormat.
func ParseTime(s string) (time.Time, error) { return time.Parse(TimeFormat, s) }

// Link is a Markdown link found in a description (SPEC §2.4). It is derived and
// read-only.
type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// Relation is one end of a directed link between two issues (SPEC §4.6).
// Direction says which end this issue is: out when it is the subject — the one
// named first — and in when Other is. A directed relation always keeps the
// reading of §2.3, so "A blocked-by B" appears on A as {blocked-by, B, out} and
// on B as {blocked-by, A, in}, and both still read "A blocked-by B".
type Relation struct {
	Type      RelationType `json:"type"`
	Other     string       `json:"other"`
	Direction Direction    `json:"direction"`
}

// Issue is the one issue shape both surfaces return, always complete (SPEC
// §4.6). Every field is always present: an unset string is "", never null or
// absent, so consumers need no absence handling.
//
// Blocked, Blockers, Relations and Links are derived and read-only; they cannot
// be written through update or PATCH.
type Issue struct {
	ID          string     `json:"id"`
	Project     string     `json:"project"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        Type       `json:"type"`
	Status      Status     `json:"status"`
	Priority    int        `json:"priority"`
	Labels      []string   `json:"labels"`
	Assignee    string     `json:"assignee"`
	CloseReason string     `json:"close_reason"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
	Blocked     bool       `json:"blocked"`
	Blockers    []string   `json:"blockers"`
	Relations   []Relation `json:"relations"`
	Links       []Link     `json:"links"`
}

// IssueTree is one Issue extended with its children, recursively (SPEC §4.6).
// It is what dep tree and GET /api/issues/{id}/tree return. No other surface
// carries children at all.
type IssueTree struct {
	Issue
	Children []IssueTree `json:"children"`
}

// Project is the top-level organising unit (SPEC §4.6). ActiveIssues counts the
// issues that are not closed; it is derived and read-only, as are the two
// timestamps.
type Project struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ActiveIssues int    `json:"active_issues"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Facet is a distinct value in use with the number of issues carrying it,
// which GET /api/labels and GET /api/assignees return (SPEC §6.2).
type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Identity is what GET /api/identity returns (SPEC §6.2).
type Identity struct {
	Identity string `json:"identity"`
}

// APIError is the one error shape both surfaces use (SPEC §4.6).
type APIError struct {
	Error string `json:"error"`
}

// Ready reports whether an issue is ready to be picked up (SPEC §2.5): it is
// open and it is not blocked. Only blocked-by drives readiness, so a decomposed
// issue is ready alongside its children.
func (i *Issue) Ready() bool { return i.Status == StatusOpen && !i.Blocked }

// SortRelations puts relations into the order SPEC §4.6 fixes — by type, then
// other, then direction — so two invocations against unchanged data produce
// byte-identical output.
func SortRelations(rels []Relation) {
	slices.SortFunc(rels, func(a, b Relation) int {
		if c := cmp.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Other, b.Other); c != 0 {
			return c
		}
		return cmp.Compare(a.Direction, b.Direction)
	})
}

// Normalize puts an issue's derived arrays into their specified order and
// replaces any nil slice with an empty one, so the JSON encoding carries [] and
// never null.
func (i *Issue) Normalize() {
	slices.Sort(i.Labels)
	slices.Sort(i.Blockers)
	SortRelations(i.Relations)
	if i.Labels == nil {
		i.Labels = []string{}
	}
	if i.Blockers == nil {
		i.Blockers = []string{}
	}
	if i.Relations == nil {
		i.Relations = []Relation{}
	}
	if i.Links == nil {
		i.Links = []Link{}
	}
}
