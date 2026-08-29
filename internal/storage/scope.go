package storage

// Scope is the set of projects a caller may see, carried by the transaction so
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
}

// Everything is the scope that hides nothing.
func Everything() Scope { return Scope{} }

// VisibleTo is the scope of one user: the projects they are a member of, and
// nothing else. A caller who holds AccessAdmin everywhere — a project
// administrator — is given Everything instead, because their access does not
// come from rows.
func VisibleTo(user string) Scope { return Scope{user: user, restricted: true} }

// Restricted reports whether this scope hides anything.
func (s Scope) Restricted() bool { return s.restricted }

// Restrict sets what this transaction may see. It is called once, at the top
// of an operation, before anything has been read.
func (t *Tx) Restrict(scope Scope) { t.scope = scope }

// Scope is what this transaction may see.
func (t *Tx) Scope() Scope { return t.scope }

// visible adds the visibility condition over a column holding a project key,
// and does nothing at all when the scope is unrestricted — so an unscoped
// query is the same SQL it was before there were users.
func (t *Tx) visible(c *conditions, column string) {
	if !t.scope.restricted {
		return
	}
	c.add(column+` IN (SELECT project FROM project_members WHERE user = ?)`, t.scope.user)
}

// visibleClause is visible for a query that is assembled by hand rather than
// through conditions. It returns a clause that is always safe to AND onto a
// WHERE, and its arguments.
func (t *Tx) visibleClause(column string) (string, []any) {
	if !t.scope.restricted {
		return "1 = 1", nil
	}
	return column + ` IN (SELECT project FROM project_members WHERE user = ?)`,
		[]any{t.scope.user}
}
