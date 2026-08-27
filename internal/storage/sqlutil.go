package storage

import (
	"strconv"
	"strings"
)

// limitOffsetClause renders the paging SPEC §6.2 requires of every array
// endpoint. There is no default limit: omitting it returns every row, as the
// command line does, so a remote-mode listing is never silently truncated. A
// limit of zero returns no rows while the caller still reports the unpaged
// total.
//
// SQLite needs a LIMIT before it will accept an OFFSET, and -1 is its "no
// limit" value.
func limitOffsetClause(limit, offset *int) string {
	if limit == nil && offset == nil {
		return ""
	}
	value := -1
	if limit != nil {
		value = *limit
	}
	clause := " LIMIT " + strconv.Itoa(value)
	if offset != nil && *offset > 0 {
		clause += " OFFSET " + strconv.Itoa(*offset)
	}
	return clause
}

// placeholders renders "?, ?, ?" for an IN clause of n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// isUniqueViolation reports whether err is SQLite refusing a duplicate key.
//
// The pure-Go driver reports constraint failures as text rather than as a typed
// error, so this matches on the message. It is used only to turn an
// already-exists into the right classification, and a miss would show up as a
// runtime error rather than as a wrong answer.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}

// isCheckViolation reports whether err is SQLite refusing a CHECK constraint,
// which for awb means an invariant of SPEC §2.2 was about to be broken.
func isCheckViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "CHECK constraint failed")
}

// anyArgs converts a slice to the []any a variadic query needs.
func anyArgs[T any](values []T) []any {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return args
}
