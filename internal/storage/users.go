package storage

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// The user and membership queries.
//
// A user is not owned by a workspace, so the account-management queries are not
// scoped. ListVisibleUsers is the exception used by the collaborative user
// directory: it derives people from workspaces in Tx.scope, and scopes the
// memberships it returns by that same set.

// AnyUsers reports whether the database holds a user at all, with a password
// or without one.
//
// This is what the assignee rule switches on: a database with no user has no
// directory to check a name against, so it keeps the version 1 behaviour of
// taking any assignee as free text. Adding the first user turns the check on,
// whether or not that user can log in.
func (t *Tx) AnyUsers() (bool, error) {
	return t.exists(`SELECT 1 FROM users LIMIT 1`, "read users")
}

// AnyUsersWithPassword reports whether the database holds a user who can log
// in.
//
// This is what a server switches on: a database with no such user is the
// version 1 database, and a server over it behaves exactly as version 1's did.
// Adding the first password closes the door, and it closes on the next request
// rather than on the next restart, because this is asked per request.
//
// A user without a password is not one: they exist to be an assignee, and an
// account nobody can authenticate as cannot be what makes a server start
// demanding authentication.
//
// The answer going back to "none" does not open it again. That is what
// UsersWithPasswordHaveExisted is for, and the two are asked together.
func (t *Tx) AnyUsersWithPassword() (bool, error) {
	return t.exists(`SELECT 1 FROM users WHERE password_hash <> '' LIMIT 1`,
		"read users")
}

// UsersWithPasswordHaveExisted reports whether this database has ever held a
// user who could log in, which no deletion clears; see schemaV6 for why the
// fact is stored rather than remembered.
//
// It is what tells a database that authenticates and has just lost its last
// credential apart from one that never had one. The first is a server that
// must refuse everybody until a password exists again; the second is a local
// tracker, and is what every version 1 database still is.
func (t *Tx) UsersWithPasswordHaveExisted() (bool, error) {
	return t.exists(`SELECT 1 FROM user_history LIMIT 1`, "read user history")
}

func (t *Tx) exists(query, what string) (bool, error) {
	var one int
	err := t.q.QueryRowContext(t.ctx, query).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "%s", what)
	}
	return true, nil
}

// PasswordHash returns a user's stored hash, and whether the user has one at
// all. It is the one place the hash is read, and the value never travels
// further than the comparison it is read for.
//
// A user without a password is reported exactly as a name that is no user is,
// so that the one caller cannot treat the empty hash as something to compare
// against: there is no password for it to be the wrong one for.
func (t *Tx) PasswordHash(name string) (string, bool, error) {
	var hash string
	err := t.q.QueryRowContext(t.ctx,
		`SELECT password_hash FROM users WHERE name = ?`, name).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, awberr.Wrap(awberr.Runtime, err, "read user %s", name)
	}
	if hash == "" {
		return "", false, nil
	}
	return hash, true, nil
}

// Caller reads the permissions half of a user row, which is what every
// authorization decision of one operation is made from.
//
// A name that is not a user is not found rather than a caller with no
// permissions: the server authenticated against this same table, so a missing
// row here means the account was deleted between the two, and answering as if
// the request were merely unprivileged would be a guess.
func (t *Tx) Caller(name string) (domain.Caller, error) {
	caller := domain.Caller{Name: name}
	err := t.q.QueryRowContext(t.ctx,
		`SELECT workspace_admin, user_admin FROM users WHERE name = ?`, name,
	).Scan(&caller.WorkspaceAdmin, &caller.UserAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Caller{}, awberr.Forbiddenf("no such user: %s", name)
	}
	if err != nil {
		return domain.Caller{}, awberr.Wrap(awberr.Runtime, err, "read user %s", name)
	}
	return caller, nil
}

// GetUser reads one user with their memberships.
func (t *Tx) GetUser(name string) (*domain.User, error) {
	var u domain.User
	err := t.q.QueryRowContext(t.ctx, `
		SELECT name, full_name, workspace_admin, user_admin, created_at, updated_at
		  FROM users WHERE name = ?`, name,
	).Scan(&u.Name, &u.FullName, &u.WorkspaceAdmin, &u.UserAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such user: %s", name)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read user %s", name)
	}

	if u.Workspaces, err = t.membershipsOf(name); err != nil {
		return nil, err
	}
	u.Normalize()
	return &u, nil
}

// ListUsers returns every user ordered by name ascending, which is what makes
// the corresponding endpoint pageable. total is the unpaged count.
//
// The memberships of each are read in one further query rather than one per
// user, exactly as an issue listing hydrates its labels.
func (t *Tx) ListUsers(filter string, limit, offset *int) (users []domain.User, total int, err error) {
	match, args := t.userListingFilter(filter, false)
	if err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM users u WHERE `+match, args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count users")
	}

	rows, err := t.q.QueryContext(t.ctx, `
		SELECT name, full_name, workspace_admin, user_admin, created_at, updated_at
		  FROM users u WHERE `+match+` ORDER BY name ASC`+limitOffsetClause(limit, offset), args...)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list users")
	}
	defer rows.Close()

	users = []domain.User{}
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.Name, &u.FullName, &u.WorkspaceAdmin, &u.UserAdmin,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list users")
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list users")
	}

	if err := t.hydrateMemberships(users, false); err != nil {
		return nil, 0, err
	}
	if err := t.hydrateActivityWorkspaces(users, false); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// SearchUsersForNavigation performs a bounded username and full-name substring search.
func (t *Tx) SearchUsersForNavigation(query string, limit int) ([]domain.User, error) {
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT name, full_name, workspace_admin, user_admin, created_at, updated_at
		  FROM users
		 WHERE instr(lower(name), lower(?)) > 0 OR instr(lower(full_name), lower(?)) > 0
		 ORDER BY CASE WHEN lower(name) = lower(?) THEN 0 ELSE 1 END, name ASC LIMIT ?`,
		query, query, query, limit)
	return t.navigationUsers(rows, false, err)
}

// ListVisibleUsers returns current accounts that have participated in a
// workspace visible to the transaction, plus the caller themselves. Participation
// is a membership, an assignment, or an activity entry: removing somebody's
// membership must not erase them from the history their former collaborators
// can already read.
//
// Only visible memberships are hydrated. A username may be shared across
// workspaces without disclosing the names of workspaces the caller cannot see.
func (t *Tx) ListVisibleUsers(caller, filter string, limit, offset *int) (users []domain.User, total int, err error) {
	visibleWorkspace, visibleArgs := t.visibleClause("workspace_members.workspace")
	visibleAssignmentWorkspace, visibleAssignmentArgs := t.visibleClause("i.workspace")
	visibleActivityWorkspace, visibleActivityArgs := t.visibleClause("i.workspace")
	args := append([]any{caller}, visibleArgs...)
	args = append(args, visibleAssignmentArgs...)
	args = append(args, visibleActivityArgs...)
	match, matchArgs := t.userListingFilter(filter, true)
	args = append(args, matchArgs...)
	cte := `WITH visible_users(name) AS (
		SELECT ?
		UNION SELECT user FROM workspace_members WHERE ` + visibleWorkspace + `
		UNION SELECT assignee FROM issue_assignees ia JOIN issues i ON i.id = ia.issue
			WHERE ` + visibleAssignmentWorkspace + `
		UNION SELECT actor FROM issue_activity a JOIN issues i ON i.id = a.issue
			WHERE actor <> '' AND ` + visibleActivityWorkspace + `
	)`

	if err := t.q.QueryRowContext(t.ctx, cte+`
		SELECT count(*) FROM users u WHERE name IN (SELECT name FROM visible_users)
		  AND `+match, args...).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count visible users")
	}

	rows, err := t.q.QueryContext(t.ctx, cte+`
		SELECT name, full_name, workspace_admin, user_admin, created_at, updated_at
		  FROM users u WHERE name IN (SELECT name FROM visible_users)
		   AND `+match+`
		 ORDER BY name ASC`+limitOffsetClause(limit, offset), args...)
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list visible users")
	}
	defer rows.Close()

	users = []domain.User{}
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.Name, &u.FullName, &u.WorkspaceAdmin, &u.UserAdmin,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list visible users")
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list visible users")
	}
	if err := t.hydrateMemberships(users, true); err != nil {
		return nil, 0, err
	}
	if err := t.hydrateActivityWorkspaces(users, true); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// userListingFilter matches exactly the directory values the web table shows.
// visibleOnly applies the transaction's workspace scope inside both correlated
// workspace lookups, so a filter cannot infer a hidden membership or activity
// workspace. Each whitespace-separated word may match a different field.
func (t *Tx) userListingFilter(filter string, visibleOnly bool) (string, []any) {
	clauses := []string{}
	args := []any{}
	for _, word := range strings.Fields(filter) {
		var membershipScope, activityScope string
		var membershipScopeArgs, activityScopeArgs []any
		if visibleOnly {
			membershipScope, membershipScopeArgs = t.visibleClause("pm.workspace")
			activityScope, activityScopeArgs = t.visibleClause("ap.workspace")
		} else {
			membershipScope, membershipScopeArgs = t.notIgnoredClause("pm.workspace")
			activityScope, activityScopeArgs = t.notIgnoredClause("ap.workspace")
		}
		clauses = append(clauses, `(
			instr(awb_casefold(u.name || ' ' || u.full_name ||
				CASE WHEN u.workspace_admin THEN ' workspace administrator' ELSE '' END ||
				CASE WHEN u.user_admin THEN ' user administrator' ELSE '' END), awb_casefold(?)) > 0
			OR EXISTS (SELECT 1 FROM workspace_members pm
			            WHERE pm.user = u.name AND `+membershipScope+`
			              AND instr(awb_casefold(pm.workspace || ' ' || pm.access), awb_casefold(?)) > 0)
			OR EXISTS (SELECT 1 FROM (
				SELECT i.workspace, ia.assignee AS user
				  FROM issue_assignees ia JOIN issues i ON i.id = ia.issue
				UNION SELECT i.workspace, a.actor AS user
				  FROM issue_activity a JOIN issues i ON i.id = a.issue
				 WHERE a.actor <> ''
			) ap WHERE ap.user = u.name AND `+activityScope+`
			  AND instr(awb_casefold(ap.workspace), awb_casefold(?)) > 0)
		)`)
		args = append(args, word)
		args = append(args, membershipScopeArgs...)
		args = append(args, word)
		args = append(args, activityScopeArgs...)
		args = append(args, word)
	}
	if len(clauses) == 0 {
		return "1 = 1", nil
	}
	return strings.Join(clauses, " AND "), args
}

// SearchVisibleUsersForNavigation applies the directory visibility set before
// matching, so autocomplete cannot disclose an otherwise hidden account.
func (t *Tx) SearchVisibleUsersForNavigation(caller, query string, limit int) ([]domain.User, error) {
	visibleWorkspace, visibleArgs := t.visibleClause("workspace_members.workspace")
	visibleAssignmentWorkspace, visibleAssignmentArgs := t.visibleClause("i.workspace")
	visibleActivityWorkspace, visibleActivityArgs := t.visibleClause("i.workspace")
	args := append([]any{caller}, visibleArgs...)
	args = append(args, visibleAssignmentArgs...)
	args = append(args, visibleActivityArgs...)
	args = append(args, query, query, query, limit)
	cte := `WITH visible_users(name) AS (
		SELECT ?
		UNION SELECT user FROM workspace_members WHERE ` + visibleWorkspace + `
		UNION SELECT assignee FROM issue_assignees ia JOIN issues i ON i.id = ia.issue
			WHERE ` + visibleAssignmentWorkspace + `
		UNION SELECT actor FROM issue_activity a JOIN issues i ON i.id = a.issue
			WHERE actor <> '' AND ` + visibleActivityWorkspace + `
	)`
	rows, err := t.q.QueryContext(t.ctx, cte+`
		SELECT name, full_name, workspace_admin, user_admin, created_at, updated_at
		  FROM users
		 WHERE name IN (SELECT name FROM visible_users)
		   AND (instr(lower(name), lower(?)) > 0 OR instr(lower(full_name), lower(?)) > 0)
		 ORDER BY CASE WHEN lower(name) = lower(?) THEN 0 ELSE 1 END, name ASC LIMIT ?`, args...)
	return t.navigationUsers(rows, true, err)
}

func (t *Tx) navigationUsers(rows *sql.Rows, visibleOnly bool, err error) ([]domain.User, error) {
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "search users for navigation")
	}
	defer rows.Close()
	users := []domain.User{}
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.Name, &u.FullName, &u.WorkspaceAdmin, &u.UserAdmin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "search users for navigation")
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "search users for navigation")
	}
	if err := t.hydrateMemberships(users, visibleOnly); err != nil {
		return nil, err
	}
	if err := t.hydrateActivityWorkspaces(users, visibleOnly); err != nil {
		return nil, err
	}
	return users, nil
}

// hydrateActivityWorkspaces fills the directory-only history for a page in one
// query. Both forms omit ignored workspaces; visibleOnly also applies ordinary
// workspace authorization. Retained assignments and activity keep a workspace
// associated after an issue closes or access is withdrawn. No status predicate
// means closed issues count. Membership stays separate: it says what the account
// may access now.
func (t *Tx) hydrateActivityWorkspaces(users []domain.User, visibleOnly bool) error {
	if len(users) == 0 {
		return nil
	}
	names := make([]string, len(users))
	byName := make(map[string]*domain.User, len(users))
	for i := range users {
		names[i] = users[i].Name
		users[i].ActivityWorkspaces = []string{}
		byName[users[i].Name] = &users[i]
	}

	where := `p.user IN (` + placeholders(len(names)) + `)`
	args := anyArgs(names)
	var visible string
	var visibleArgs []any
	if visibleOnly {
		visible, visibleArgs = t.visibleClause("p.workspace")
	} else {
		visible, visibleArgs = t.notIgnoredClause("p.workspace")
	}
	where += ` AND ` + visible
	args = append(args, visibleArgs...)
	rows, err := t.q.QueryContext(t.ctx, `
		WITH activity_workspaces (workspace, user) AS (
			SELECT i.workspace, ia.assignee
			  FROM issue_assignees ia JOIN issues i ON i.id = ia.issue
			UNION SELECT i.workspace, a.actor
			  FROM issue_activity a JOIN issues i ON i.id = a.issue
			 WHERE a.actor <> ''
		)
		SELECT p.workspace, p.user FROM activity_workspaces p
		 WHERE `+where+`
		 ORDER BY p.user ASC, p.workspace ASC`, args...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read user workspace involvement")
	}
	defer rows.Close()

	for rows.Next() {
		var workspace, user string
		if err := rows.Scan(&workspace, &user); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read user workspace involvement")
		}
		if u := byName[user]; u != nil {
			u.ActivityWorkspaces = append(u.ActivityWorkspaces, workspace)
		}
	}
	return awberr.Wrap(awberr.Runtime, rows.Err(), "read user workspace involvement")
}

// hydrateMemberships fills in the Workspaces of a page of users in one query.
// Both forms omit ignored workspaces; visibleOnly additionally applies ordinary
// workspace authorization for the collaborative directory, while a user
// administrator's complete directory deliberately does not.
func (t *Tx) hydrateMemberships(users []domain.User, visibleOnly bool) error {
	if len(users) == 0 {
		return nil
	}
	names := make([]string, len(users))
	byName := make(map[string]*domain.User, len(users))
	for i := range users {
		names[i] = users[i].Name
		byName[users[i].Name] = &users[i]
	}

	where := `user IN (` + placeholders(len(names)) + `)`
	args := anyArgs(names)
	var visible string
	var visibleArgs []any
	if visibleOnly {
		visible, visibleArgs = t.visibleClause("workspace_members.workspace")
	} else {
		visible, visibleArgs = t.notIgnoredClause("workspace_members.workspace")
	}
	where += ` AND ` + visible
	args = append(args, visibleArgs...)
	rows, err := t.q.QueryContext(t.ctx, `
		SELECT workspace, user, access FROM workspace_members
		 WHERE `+where+`
		 ORDER BY user ASC, workspace ASC`, args...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read memberships")
	}
	defer rows.Close()

	for rows.Next() {
		var m domain.Membership
		if err := rows.Scan(&m.Workspace, &m.User, &m.Access); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read memberships")
		}
		if u := byName[m.User]; u != nil {
			u.Workspaces = append(u.Workspaces, m)
		}
	}
	if err := rows.Err(); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read memberships")
	}

	for i := range users {
		users[i].Normalize()
	}
	return nil
}

// membershipsOf reads one user's memberships, ordered by workspace ascending.
func (t *Tx) membershipsOf(name string) ([]domain.Membership, error) {
	visible, visibleArgs := t.visibleClause("workspace_members.workspace")
	args := append([]any{name}, visibleArgs...)
	return t.scanMemberships(`
		SELECT workspace, user, access FROM workspace_members
		 WHERE user = ? AND `+visible+` ORDER BY workspace ASC`, args)
}

// InsertUser stores a new user and, when they have a password, records that
// this database has had one who can log in. An empty hash is an account that
// exists to be an assignee and cannot authenticate.
//
// The two are one statement pair in one transaction, and the record is written
// here rather than by the operation above, so that no way of giving a user a
// password can leave the fact unwritten.
func (t *Tx) InsertUser(name, fullName, hash string, workspaceAdmin, userAdmin bool) error {
	now := Now()
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO users (name, full_name, password_hash, workspace_admin, user_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, name, fullName, hash, workspaceAdmin, userAdmin, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return awberr.Conflictf("user %s already exists", name)
		}
		return awberr.Wrap(awberr.Runtime, err, "create user %s", name)
	}
	return t.recordPassword(name, hash)
}

// recordPassword marks the database as one whose server authenticates, which
// only a password does and no deletion undoes.
func (t *Tx) recordPassword(name, hash string) error {
	if hash == "" {
		return nil
	}
	if _, err := t.q.ExecContext(t.ctx,
		`INSERT OR IGNORE INTO user_history (one) VALUES (1)`); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "record that user %s has a password", name)
	}
	return nil
}

// UserFields are the stored fields an update may change. The password is
// separate: it is write-only and is not compared, so a change that sets the
// same password again still counts as a change.
type UserFields struct {
	FullName       string
	WorkspaceAdmin bool
	UserAdmin      bool
}

// UpdateUser writes a user's flags and, when hash is non-nil, their password,
// moving updated_at only when something actually changed. Granting an access
// level in a workspace does not touch it: memberships are their own rows, as an
// issue's labels are.
func (t *Tx) UpdateUser(u *domain.User, fields UserFields, hash *string) error {
	unchanged := fields.FullName == u.FullName && fields.WorkspaceAdmin == u.WorkspaceAdmin &&
		fields.UserAdmin == u.UserAdmin
	if unchanged && hash == nil {
		return nil
	}
	updated := bumpedTimestamp(u.UpdatedAt, Now())

	var err error
	if hash != nil {
		_, err = t.q.ExecContext(t.ctx, `
			UPDATE users SET password_hash = ?, full_name = ?, workspace_admin = ?, user_admin = ?, updated_at = ?
			 WHERE name = ?`, *hash, fields.FullName, fields.WorkspaceAdmin, fields.UserAdmin, updated, u.Name)
	} else {
		_, err = t.q.ExecContext(t.ctx, `
			UPDATE users SET full_name = ?, workspace_admin = ?, user_admin = ?, updated_at = ?
			 WHERE name = ?`, fields.FullName, fields.WorkspaceAdmin, fields.UserAdmin, updated, u.Name)
	}
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update user %s", u.Name)
	}
	if hash != nil {
		if err := t.recordPassword(u.Name, *hash); err != nil {
			return err
		}
	}

	u.FullName = fields.FullName
	u.WorkspaceAdmin = fields.WorkspaceAdmin
	u.UserAdmin = fields.UserAdmin
	u.UpdatedAt = updated
	return nil
}

// DeleteUser removes a user and, by cascade, every membership they held.
//
// The issues they were assigned are left exactly as they are. An assignee is a
// record of who holds or held a piece of work, not a reference to an account,
// and rewriting history because somebody's access was withdrawn would lose the
// only record of who did it.
func (t *Tx) DeleteUser(name string) error {
	_, err := t.q.ExecContext(t.ctx, `DELETE FROM users WHERE name = ?`, name)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "delete user %s", name)
	}
	return nil
}

// Membership returns a user's access to a workspace, and whether they hold any
// there at all.
func (t *Tx) Membership(workspace, user string) (domain.Access, bool, error) {
	var access domain.Access
	err := t.q.QueryRowContext(t.ctx,
		`SELECT access FROM workspace_members WHERE workspace = ? AND user = ?`,
		workspace, user).Scan(&access)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, awberr.Wrap(awberr.Runtime, err, "read the membership of %s in %s",
			user, workspace)
	}
	return access, true, nil
}

// ListMembers returns a workspace's members ordered by username ascending, with
// the unpaged total.
func (t *Tx) ListMembers(workspace string, limit, offset *int) (
	members []domain.Membership, total int, err error) {
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace = ?`, workspace,
	).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count the members of %s", workspace)
	}

	members, err = t.scanMemberships(`
		SELECT workspace, user, access FROM workspace_members
		 WHERE workspace = ? ORDER BY user ASC`+limitOffsetClause(limit, offset), []any{workspace})
	return members, total, err
}

// SetMembership grants an access level, replacing whatever the user held in
// that workspace before. Granting the level they already hold succeeds and
// changes nothing.
func (t *Tx) SetMembership(workspace, user string, access domain.Access) error {
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO workspace_members (workspace, user, access) VALUES (?, ?, ?)
		ON CONFLICT (workspace, user) DO UPDATE SET access = excluded.access`,
		workspace, user, access)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "grant %s access to %s in %s",
			access, user, workspace)
	}
	return nil
}

// DeleteMembership withdraws a user's access to a workspace.
func (t *Tx) DeleteMembership(workspace, user string) error {
	_, err := t.q.ExecContext(t.ctx,
		`DELETE FROM workspace_members WHERE workspace = ? AND user = ?`, workspace, user)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "withdraw the access of %s to %s", user, workspace)
	}
	return nil
}

func (t *Tx) scanMemberships(query string, args []any) ([]domain.Membership, error) {
	rows, err := t.q.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read memberships")
	}
	defer rows.Close()

	members := []domain.Membership{}
	for rows.Next() {
		var m domain.Membership
		if err := rows.Scan(&m.Workspace, &m.User, &m.Access); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read memberships")
		}
		members = append(members, m)
	}
	return members, awberr.Wrap(awberr.Runtime, rows.Err(), "read memberships")
}
