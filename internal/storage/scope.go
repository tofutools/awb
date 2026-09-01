package storage

import "strings"

// Scope is the set of workspaces a caller may see, carried by the transaction so
// that every read inside it is answered from the same set.
//
// It is put on the Tx rather than passed to each query for one reason: a read
// that forgets it does not fail, it leaks. Having exactly one place where a
// transaction is restricted, and having every query consult the transaction it
// is running in, is what keeps a listing added later from being the one that
// forgets.
//
// The zero value is unrestricted, which is direct mode and a server whose
// database holds no user: there is nothing there to scope by, and the CLI on a
// file could read around any check anyway.
type Scope struct {
	user       string
	restricted bool
	ignoredBy  string
}

// Everything is the scope that hides nothing.
func Everything() Scope { return Scope{} }

// VisibleTo is the scope of one user: the workspaces they are a member of, and
// nothing else. A caller who holds AccessAdmin everywhere — a workspace
// administrator — is given Everything instead, because their access does not
// come from rows.
func VisibleTo(user string) Scope { return Scope{user: user, restricted: true} }

// HideIgnoredBy adds one user's preference boundary without changing the
// authorization boundary already carried by the scope.
func (s Scope) HideIgnoredBy(user string) Scope {
	s.ignoredBy = user
	return s
}

// Restricted reports whether this scope hides anything.
func (s Scope) Restricted() bool { return s.restricted }

// Restrict sets what this transaction may see. It is called once, at the top
// of an operation, before anything has been read.
func (t *Tx) Restrict(scope Scope) { t.scope = scope }

// Scope is what this transaction may see.
func (t *Tx) Scope() Scope { return t.scope }

// visible adds the visibility condition over a column holding a workspace key,
// and does nothing at all when the scope is unrestricted — so an unscoped
// query is the same SQL it was before there were users.
func (t *Tx) visible(c *conditions, column string) {
	if !t.scope.restricted {
		if t.scope.ignoredBy == "" {
			return
		}
	} else {
		c.add(column+` IN (SELECT workspace FROM workspace_members WHERE user = ?)`, t.scope.user)
	}
	if t.scope.ignoredBy != "" {
		c.add(`NOT EXISTS (SELECT 1 FROM ignored_workspaces ip
			WHERE ip.user = ? AND ip.workspace = `+column+`)`, t.scope.ignoredBy)
	}
}

// visibleClause is visible for a query that is assembled by hand rather than
// through conditions. It returns a clause that is always safe to AND onto a
// WHERE, and its arguments.
func (t *Tx) visibleClause(column string) (string, []any) {
	clauses := []string{}
	args := []any{}
	if t.scope.restricted {
		clauses = append(clauses, column+` IN (SELECT workspace FROM workspace_members WHERE user = ?)`)
		args = append(args, t.scope.user)
	}
	if t.scope.ignoredBy != "" {
		clauses = append(clauses, `NOT EXISTS (SELECT 1 FROM ignored_workspaces ip
			WHERE ip.user = ? AND ip.workspace = `+column+`)`)
		args = append(args, t.scope.ignoredBy)
	}
	if len(clauses) == 0 {
		return "1 = 1", nil
	}
	return strings.Join(clauses, " AND "), args
}

// notIgnoredClause applies only the preference half of the scope. Relation
// hydration uses it for counterpart records: authorization deliberately keeps
// graph names visible, while the opt-in preference asks those connections to
// disappear from presentation without changing graph rules.
func (t *Tx) notIgnoredClause(column string) (string, []any) {
	if t.scope.ignoredBy == "" {
		return "1 = 1", nil
	}
	return `NOT EXISTS (SELECT 1 FROM ignored_workspaces ip
		WHERE ip.user = ? AND ip.workspace = ` + column + `)`, []any{t.scope.ignoredBy}
}
