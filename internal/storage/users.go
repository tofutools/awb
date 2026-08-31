package storage

import (
	"database/sql"
	"errors"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

// The user and membership queries.
//
// None of them is scoped by Tx.scope. A user is not owned by a project, so
// there is no project key to hide one behind; who may read and change one is
// decided by the rules in the domain layer, which the local backend applies
// before it calls any of this.

// AnyUsers reports whether the database holds a user at all.
//
// This is what a server switches on: a database with no user is the version 1
// database, and a server over it behaves exactly as version 1's did. Adding
// the first user closes the door, and it closes on the next request rather
// than on the next restart, because this is asked per request. The answer
// going back to "none" does not reopen it — see cli.authenticator, which
// remembers — so the switch is one-way within one run of a server.
func (t *Tx) AnyUsers() (bool, error) {
	var one int
	err := t.q.QueryRowContext(t.ctx, `SELECT 1 FROM users LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "read users")
	}
	return true, nil
}

// PasswordHash returns a user's stored hash, and whether the user exists. It
// is the one place the hash is read, and the value never travels further than
// the comparison it is read for.
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
		`SELECT project_admin, user_admin FROM users WHERE name = ?`, name,
	).Scan(&caller.ProjectAdmin, &caller.UserAdmin)
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
		SELECT name, project_admin, user_admin, created_at, updated_at
		  FROM users WHERE name = ?`, name,
	).Scan(&u.Name, &u.ProjectAdmin, &u.UserAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, awberr.NotFoundf("no such user: %s", name)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read user %s", name)
	}

	if u.Projects, err = t.membershipsOf(name); err != nil {
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
func (t *Tx) ListUsers(limit, offset *int) (users []domain.User, total int, err error) {
	if err := t.q.QueryRowContext(t.ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count users")
	}

	rows, err := t.q.QueryContext(t.ctx, `
		SELECT name, project_admin, user_admin, created_at, updated_at
		  FROM users ORDER BY name ASC`+limitOffsetClause(limit, offset))
	if err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list users")
	}
	defer rows.Close()

	users = []domain.User{}
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.Name, &u.ProjectAdmin, &u.UserAdmin,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, awberr.Wrap(awberr.Runtime, err, "list users")
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "list users")
	}

	if err := t.hydrateMemberships(users); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// hydrateMemberships fills in the Projects of a page of users in one query.
func (t *Tx) hydrateMemberships(users []domain.User) error {
	if len(users) == 0 {
		return nil
	}
	names := make([]string, len(users))
	byName := make(map[string]*domain.User, len(users))
	for i := range users {
		names[i] = users[i].Name
		byName[users[i].Name] = &users[i]
	}

	rows, err := t.q.QueryContext(t.ctx, `
		SELECT project, user, access FROM project_members
		 WHERE user IN (`+placeholders(len(names))+`)
		 ORDER BY user ASC, project ASC`, anyArgs(names)...)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read memberships")
	}
	defer rows.Close()

	for rows.Next() {
		var m domain.Membership
		if err := rows.Scan(&m.Project, &m.User, &m.Access); err != nil {
			return awberr.Wrap(awberr.Runtime, err, "read memberships")
		}
		if u := byName[m.User]; u != nil {
			u.Projects = append(u.Projects, m)
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

// membershipsOf reads one user's memberships, ordered by project ascending.
func (t *Tx) membershipsOf(name string) ([]domain.Membership, error) {
	return t.scanMemberships(`
		SELECT project, user, access FROM project_members
		 WHERE user = ? ORDER BY project ASC`, []any{name})
}

// InsertUser stores a new user.
func (t *Tx) InsertUser(name, hash string, projectAdmin, userAdmin bool) error {
	now := Now()
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO users (name, password_hash, project_admin, user_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, name, hash, projectAdmin, userAdmin, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return awberr.Conflictf("user %s already exists", name)
		}
		return awberr.Wrap(awberr.Runtime, err, "create user %s", name)
	}
	return nil
}

// UserFields are the stored fields an update may change. The password is
// separate: it is write-only and is not compared, so a change that sets the
// same password again still counts as a change.
type UserFields struct {
	ProjectAdmin bool
	UserAdmin    bool
}

// UpdateUser writes a user's flags and, when hash is non-nil, their password,
// moving updated_at only when something actually changed. Granting an access
// level in a project does not touch it: memberships are their own rows, as an
// issue's labels are.
func (t *Tx) UpdateUser(u *domain.User, fields UserFields, hash *string) error {
	unchanged := fields.ProjectAdmin == u.ProjectAdmin && fields.UserAdmin == u.UserAdmin
	if unchanged && hash == nil {
		return nil
	}
	updated := bumpedTimestamp(u.UpdatedAt, Now())

	var err error
	if hash != nil {
		_, err = t.q.ExecContext(t.ctx, `
			UPDATE users SET password_hash = ?, project_admin = ?, user_admin = ?, updated_at = ?
			 WHERE name = ?`, *hash, fields.ProjectAdmin, fields.UserAdmin, updated, u.Name)
	} else {
		_, err = t.q.ExecContext(t.ctx, `
			UPDATE users SET project_admin = ?, user_admin = ?, updated_at = ?
			 WHERE name = ?`, fields.ProjectAdmin, fields.UserAdmin, updated, u.Name)
	}
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "update user %s", u.Name)
	}

	u.ProjectAdmin = fields.ProjectAdmin
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

// Membership returns a user's access to a project, and whether they hold any
// there at all.
func (t *Tx) Membership(project, user string) (domain.Access, bool, error) {
	var access domain.Access
	err := t.q.QueryRowContext(t.ctx,
		`SELECT access FROM project_members WHERE project = ? AND user = ?`,
		project, user).Scan(&access)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, awberr.Wrap(awberr.Runtime, err, "read the membership of %s in %s",
			user, project)
	}
	return access, true, nil
}

// ListMembers returns a project's members ordered by username ascending, with
// the unpaged total.
func (t *Tx) ListMembers(project string, limit, offset *int) (
	members []domain.Membership, total int, err error) {
	if err := t.q.QueryRowContext(t.ctx,
		`SELECT count(*) FROM project_members WHERE project = ?`, project,
	).Scan(&total); err != nil {
		return nil, 0, awberr.Wrap(awberr.Runtime, err, "count the members of %s", project)
	}

	members, err = t.scanMemberships(`
		SELECT project, user, access FROM project_members
		 WHERE project = ? ORDER BY user ASC`+limitOffsetClause(limit, offset), []any{project})
	return members, total, err
}

// SetMembership grants an access level, replacing whatever the user held in
// that project before. Granting the level they already hold succeeds and
// changes nothing.
func (t *Tx) SetMembership(project, user string, access domain.Access) error {
	_, err := t.q.ExecContext(t.ctx, `
		INSERT INTO project_members (project, user, access) VALUES (?, ?, ?)
		ON CONFLICT (project, user) DO UPDATE SET access = excluded.access`,
		project, user, access)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "grant %s access to %s in %s",
			access, user, project)
	}
	return nil
}

// DeleteMembership withdraws a user's access to a project.
func (t *Tx) DeleteMembership(project, user string) error {
	_, err := t.q.ExecContext(t.ctx,
		`DELETE FROM project_members WHERE project = ? AND user = ?`, project, user)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "withdraw the access of %s to %s", user, project)
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
		if err := rows.Scan(&m.Project, &m.User, &m.Access); err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read memberships")
		}
		members = append(members, m)
	}
	return members, awberr.Wrap(awberr.Runtime, rows.Err(), "read memberships")
}
