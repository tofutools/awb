package local

import (
	"context"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// The user and membership operations.
//
// They are gated rather than scoped. Everything about an issue or a project is
// hidden from a caller who is not a member, by the visibility scope the
// transaction carries; a user belongs to no project and so has nothing to be
// hidden behind, and who may read and change one is a rule instead. The rules
// themselves are in the domain layer, so both surfaces reach the same answer.

// CreateUser creates an account.
func (b *Backend) CreateUser(ctx context.Context, req backend.UserCreate) (*domain.User, error) {
	name, err := domain.ValidateUsername(req.Name)
	if err != nil {
		return nil, err
	}
	hash, err := credential(name, req.Password, req.PasswordHash, true)
	if err != nil {
		return nil, err
	}

	var user *domain.User
	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if !caller.MayManageUsers() {
			return awberr.Forbiddenf("only a user administrator may create a user")
		}
		if err := tx.InsertUser(name, *hash, req.ProjectAdmin, req.UserAdmin); err != nil {
			return err
		}
		user, err = tx.GetUser(name)
		return err
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// credential turns the two ways of stating a password into the one hash that
// is stored, and reports nil when neither was given.
//
// Exactly one of them may be given: a request carrying both says two things
// about one credential, and picking either would be a guess. required is set
// where an account is being made, which cannot be made without one.
func credential(name, password, hash string, required bool) (*string, error) {
	switch {
	case password != "" && hash != "":
		return nil, awberr.Usagef("give a password or a password hash, not both")
	case hash != "":
		parsed, err := domain.ParsePasswordHash(name, hash)
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	case password != "":
		derived, err := domain.HashPassword(password)
		if err != nil {
			return nil, err
		}
		return &derived, nil
	case required:
		return nil, awberr.Usagef("a user needs a password")
	default:
		return nil, nil
	}
}

// GetUser reads one account. A user may always read their own, which is how
// somebody without the user_admin flag learns what they are permitted to do.
func (b *Backend) GetUser(ctx context.Context, name string) (*domain.User, error) {
	if _, err := domain.ValidateUsername(name); err != nil {
		return nil, err
	}
	var user *domain.User
	err := b.read(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if !caller.MaySeeUser(name) {
			return awberr.Forbiddenf("only a user administrator may read the account of %s", name)
		}
		var err error
		user, err = tx.GetUser(name)
		return err
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ListUsers lists accounts ordered by name ascending.
func (b *Backend) ListUsers(ctx context.Context, limit, offset *int) (backend.UserPage, error) {
	var page backend.UserPage
	err := b.read(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if !caller.MayManageUsers() {
			return awberr.Forbiddenf("only a user administrator may list users")
		}
		var err error
		page.Users, page.Total, err = tx.ListUsers(limit, offset)
		return err
	})
	if err != nil {
		return backend.UserPage{}, err
	}
	if page.Users == nil {
		page.Users = []domain.User{}
	}
	return page, nil
}

// UpdateUser changes an account's password or its two flags. Giving no field
// at all succeeds and changes nothing, exactly as an empty PATCH does — but it
// is permitted as a read of the account, which is what it answers with.
//
// The two halves are permitted separately, because they are different powers:
// changing the flags is a user administrator's, and changing a password is
// theirs or the account holder's own. So a user may set their own password
// without being able to grant themselves anything.
func (b *Backend) UpdateUser(ctx context.Context, name string, req backend.UserPatch,
	ifMatch string) (*domain.User, error) {
	if _, err := domain.ValidateUsername(name); err != nil {
		return nil, err
	}
	hash, err := credential(name, deref(req.Password), deref(req.PasswordHash), false)
	if err != nil {
		return nil, err
	}
	changesFlags := req.ProjectAdmin != nil || req.UserAdmin != nil

	var user *domain.User
	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		// The floor, which an empty patch is held to as well: the operation
		// answers with the account, so a caller who may not read one may not
		// reach it by asking to change nothing about it.
		if !caller.MaySeeUser(name) {
			return awberr.Forbiddenf("only a user administrator may change the account of %s", name)
		}
		if hash != nil && !caller.MaySetPasswordOf(name) {
			return awberr.Forbiddenf(
				"only %s or a user administrator may change that password", name)
		}
		if changesFlags && !caller.MayManageUsers() {
			return awberr.Forbiddenf("only a user administrator may change what %s may do", name)
		}

		existing, err := tx.GetUser(name)
		if err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, existing.UpdatedAt, "the user"); err != nil {
			return err
		}

		fields := storage.UserFields{
			ProjectAdmin: existing.ProjectAdmin,
			UserAdmin:    existing.UserAdmin,
		}
		if req.ProjectAdmin != nil {
			fields.ProjectAdmin = *req.ProjectAdmin
		}
		if req.UserAdmin != nil {
			fields.UserAdmin = *req.UserAdmin
		}

		if err := tx.UpdateUser(existing, fields, hash); err != nil {
			return err
		}
		user, err = tx.GetUser(name)
		return err
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DeleteUser deletes an account and, by cascade, every membership it held.
//
// The issues it was assigned are left exactly as they are: an assignee records
// who holds or held a piece of work, and rewriting that because somebody's
// access was withdrawn would lose the only record of who did it.
func (b *Backend) DeleteUser(ctx context.Context, name, ifMatch string) (*backend.DeletedUser, error) {
	if _, err := domain.ValidateUsername(name); err != nil {
		return nil, err
	}

	var deleted backend.DeletedUser
	err := b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if !caller.MayManageUsers() {
			return awberr.Forbiddenf("only a user administrator may delete a user")
		}
		user, err := tx.GetUser(name)
		if err != nil {
			return err
		}
		if err := checkIfMatch(ifMatch, user.UpdatedAt, "the user"); err != nil {
			return err
		}
		deleted.User = *user
		return tx.DeleteUser(name)
	})
	if err != nil {
		return nil, err
	}
	return &deleted, nil
}

// ListMembers lists a project's members, ordered by username ascending.
//
// Any member of the project may read it: knowing who else is on a board is
// part of working on it, and every one of those names is already visible as an
// assignee.
func (b *Backend) ListMembers(ctx context.Context, project string, limit, offset *int) (
	backend.MemberPage, error) {
	if _, err := domain.ValidateProjectKey(project); err != nil {
		return backend.MemberPage{}, err
	}

	var page backend.MemberPage
	err := b.read(ctx, func(tx *storage.Tx, _ domain.Caller) error {
		// Scoped, so a project the caller is not in is not found rather than
		// empty.
		if _, err := tx.GetProject(project); err != nil {
			return err
		}
		var err error
		page.Members, page.Total, err = tx.ListMembers(project, limit, offset)
		return err
	})
	if err != nil {
		return backend.MemberPage{}, err
	}
	if page.Members == nil {
		page.Members = []domain.Membership{}
	}
	return page, nil
}

// SetMember grants a user an access level in a project, replacing whatever
// they held there before. Granting the level they already hold succeeds and
// changes nothing.
func (b *Backend) SetMember(ctx context.Context, project, user string, access domain.Access) (
	*domain.Membership, error) {
	membership, err := validateMembership(project, user, access)
	if err != nil {
		return nil, err
	}

	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if err := permitMembership(tx, caller, membership.Project); err != nil {
			return err
		}
		// A membership names a user, so the account has to exist: a row naming
		// nobody is a permission granted to nobody, and the foreign key would
		// refuse it anyway with a message about a constraint rather than about
		// the name that was wrong.
		if _, err := tx.GetUser(membership.User); err != nil {
			return err
		}
		return tx.SetMembership(membership.Project, membership.User, membership.Access)
	})
	if err != nil {
		return nil, err
	}
	return membership, nil
}

// RemoveMember withdraws a user's access to a project, and returns the
// membership as it was immediately before. Withdrawing access nobody holds is
// not found, exactly as deleting an attachment that is not there is.
func (b *Backend) RemoveMember(ctx context.Context, project, user string) (*domain.Membership, error) {
	membership, err := validateMembership(project, user, domain.AccessRegular)
	if err != nil {
		return nil, err
	}

	err = b.write(ctx, func(tx *storage.Tx, caller domain.Caller) error {
		if err := permitMembership(tx, caller, membership.Project); err != nil {
			return err
		}
		access, member, err := tx.Membership(membership.Project, membership.User)
		if err != nil {
			return err
		}
		if !member {
			return awberr.NotFoundf("%s has no access to project %s",
				membership.User, membership.Project)
		}
		membership.Access = access
		return tx.DeleteMembership(membership.Project, membership.User)
	})
	if err != nil {
		return nil, err
	}
	return membership, nil
}

func validateMembership(project, user string, access domain.Access) (*domain.Membership, error) {
	key, err := domain.ValidateProjectKey(project)
	if err != nil {
		return nil, err
	}
	name, err := domain.ValidateUsername(user)
	if err != nil {
		return nil, err
	}
	level, err := domain.ParseAccess(string(access))
	if err != nil {
		return nil, err
	}
	return &domain.Membership{Project: key, User: name, Access: level}, nil
}

// permitMembership is the gate on both membership writes: the project has to
// exist and be visible, and the caller has to hold admin access in it — either
// by their own membership or by the project_admin flag, which holds admin
// everywhere.
//
// The project is read first, so one the caller cannot see is not found rather
// than refused.
func permitMembership(tx *storage.Tx, caller domain.Caller, project string) error {
	if _, err := tx.GetProject(project); err != nil {
		return err
	}
	access, member, err := tx.Membership(project, caller.Name)
	if err != nil {
		return err
	}
	if !caller.MayAdministerProject(access, member) {
		return awberr.Forbiddenf(
			"only an administrator of project %s may change who works on it", project)
	}
	return nil
}
