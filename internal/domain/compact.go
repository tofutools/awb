package domain

import (
	"encoding/json"
	"strconv"
	"strings"
)

// CompactLine renders an issue as the compact one-line form, designed to
// consume as little agent context as possible:
//
//	awb-5c1d84 P1 in_progress bug "Tokeniser drops the trailing newline" @claude-1 #tokeniser !blocked
//
// The line begins with five mandatory positional fields — id, P<priority>,
// status, type and the title. The title is encoded as a JSON string, including
// the surrounding double quotes and JSON escaping; it is the only field that
// may contain literal spaces after decoding.
//
// Any further fields are optional and identified by their prefix rather than
// their position, and appear in this fixed order when present: @<assignee>,
// one #<label> per label in sorted order, !blocked and, when withBlockers is
// set, one blocked-by:<id> per entry of Blockers, in sorted order. That last
// group is for awb blocked, which is the listing whose point is the blockers.
//
// The character restrictions on labels and assignees keep those tokens free of
// spaces, so a line is parseable by splitting on whitespace outside the quoted
// title.
func CompactLine(issue *Issue, withBlockers bool) string {
	var b strings.Builder

	b.WriteString(issue.ID)
	b.WriteString(" P")
	b.WriteString(strconv.Itoa(issue.Priority))
	b.WriteByte(' ')
	b.WriteString(string(issue.Status))
	b.WriteByte(' ')
	b.WriteString(string(issue.Type))
	b.WriteByte(' ')
	b.WriteString(jsonString(issue.Title))

	if issue.Assignee != "" {
		b.WriteString(" @")
		b.WriteString(issue.Assignee)
	}
	for _, label := range issue.Labels {
		b.WriteString(" #")
		b.WriteString(label)
	}
	if issue.Blocked {
		b.WriteString(" !blocked")
	}
	if withBlockers {
		for _, blocker := range issue.Blockers {
			b.WriteString(" blocked-by:")
			b.WriteString(blocker)
		}
	}

	return b.String()
}

// CompactProjectLine renders a project as "<key> <active_issues> <name>",
// where the name is a JSON string.
func CompactProjectLine(p *Project) string {
	return p.Key + " " + strconv.Itoa(p.ActiveIssues) + " " + jsonString(p.Name)
}

// CompactActivityLine renders one timeline entry as a stable single line.
// Comment bodies and change arrays are JSON so embedded whitespace and line
// breaks never make one entry span multiple lines.
func CompactActivityLine(a *Activity) string {
	var b strings.Builder
	b.WriteString(strconv.FormatInt(a.ID, 10))
	b.WriteByte(' ')
	b.WriteString(a.CreatedAt)
	b.WriteByte(' ')
	b.WriteString(string(a.Kind))
	if a.Actor != "" {
		b.WriteString(" @")
		b.WriteString(a.Actor)
	}
	if a.Kind == ActivityKindComment {
		if a.Action != "" {
			b.WriteByte(' ')
			b.WriteString(a.Action)
		}
		b.WriteByte(' ')
		b.WriteString(jsonString(a.Body))
	} else {
		b.WriteByte(' ')
		b.WriteString(a.Action)
		if len(a.Changes) > 0 {
			encoded, _ := json.Marshal(a.Changes)
			b.WriteByte(' ')
			b.Write(encoded)
		}
	}
	return b.String()
}

// CompactUserLine renders a user as the compact one-line form:
//
//	alice +project-admin awb:admin web:regular
//
// The line begins with the one mandatory field, the username. Any further
// field is optional and identified by its shape rather than its position, and
// they appear in this fixed order when present: "+project-admin",
// "+user-admin", and one "<project>:<access>" per membership, in project
// order. A project key cannot contain a colon and neither can an access level,
// so a membership token splits on its only one.
//
// A password is not in it, and there is no field it could go in: nothing that
// leaves the storage layer carries one.
func CompactUserLine(u *User) string {
	var b strings.Builder

	b.WriteString(u.Name)
	if u.ProjectAdmin {
		b.WriteString(" +project-admin")
	}
	if u.UserAdmin {
		b.WriteString(" +user-admin")
	}
	for _, m := range u.Projects {
		b.WriteByte(' ')
		b.WriteString(m.Project)
		b.WriteByte(':')
		b.WriteString(string(m.Access))
	}

	return b.String()
}

// CompactMembershipLine renders one membership as "<project> <user> <access>",
// all three of which are drawn from character sets with no spaces in them.
func CompactMembershipLine(m *Membership) string {
	return m.Project + " " + m.User + " " + string(m.Access)
}

// CompactTreePrefix is the indentation dep tree --compact puts before a node's
// compact line: two spaces per level of depth, the root at depth zero and
// therefore unindented. That prefix is the one thing that may precede the id,
// so a consumer strips the leading spaces, counts them to recover the depth,
// and parses the rest of the line as usual.
func CompactTreePrefix(depth int) string { return strings.Repeat("  ", depth) }

// jsonString encodes s as a JSON string with its surrounding quotes.
//
// encoding/json escapes <, > and & as < and friends by default, which is valid
// JSON but noisier than it needs to be in a line meant to be cheap to read;
// SetEscapeHTML(false) turns that off. Encode appends a newline, which is
// trimmed.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// A Go string always encodes; invalid UTF-8 cannot reach here, having been
		// refused by the text gate.
		return strconv.Quote(s)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
