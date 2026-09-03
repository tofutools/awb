package domain

import (
	"slices"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/tofutools/awb/internal/awberr"
)

// Access is what one user may do in one workspace. It is the only per-workspace
// vocabulary there is: a user either holds one of these in a workspace or holds
// nothing there, and holding nothing means the workspace does not exist as far
// as that user is concerned.
type Access string

const (
	// AccessRegular is working with the issues of a workspace: reading them,
	// creating them, editing them, claiming them and everything else awb does
	// to an issue.
	AccessRegular Access = "regular"
	// AccessAdmin is AccessRegular and, in addition, adding and removing the
	// workspace's other users. It is not power over the workspace itself: creating,
	// renaming and deleting a workspace is the workspace_admin flag's, because a
	// workspace's own existence is not something its members decide.
	AccessAdmin Access = "admin"
)

// AccessLevels lists every access level, weakest first.
var AccessLevels = []Access{AccessRegular, AccessAdmin}

// ParseAccess validates s as an access level.
func ParseAccess(s string) (Access, error) {
	if slices.Contains(AccessLevels, Access(s)) {
		return Access(s), nil
	}
	return "", awberr.Usagef("invalid access %q: must be one of %s", s, join(AccessLevels))
}

// MaxPasswordBytes is the longest password that may be set.
//
// It is bcrypt's own limit rather than a policy: bcrypt hashes the first 72
// bytes and ignores the rest, so a longer password would be silently truncated
// and two passwords agreeing in their first 72 bytes would both open the
// account. A password over it is refused rather than cut, so nothing is ever
// stored that is not what was typed.
const MaxPasswordBytes = 72

// ValidatePassword applies the input rules to a password.
//
// A password is text like everything else awb accepts — valid UTF-8 and no
// control characters — and it may not be empty, because an empty one is a
// disabled account written as if it were a credential. Nothing else about it
// is prescribed: length beyond bcrypt's limit, character classes and expiry
// are policy, and awb has no opinion to enforce.
func ValidatePassword(s string) error {
	if err := checkUTF8("password", s); err != nil {
		return err
	}
	if err := checkNoControls("password", s); err != nil {
		return err
	}
	if s == "" {
		return awberr.Usagef("password must not be empty")
	}
	if len(s) > MaxPasswordBytes {
		return awberr.Usagef("password is longer than %d bytes, which is all bcrypt hashes",
			MaxPasswordBytes)
	}
	return nil
}

// ValidateUsername applies the username vocabulary, which is the assignee
// vocabulary: a username is what the issues that user works on record as their
// assignee, so anything a username may be an assignee must be able to be.
func ValidateUsername(s string) (string, error) {
	return validateLabelLike("username", s, MaxAssigneeLen)
}

// HashPassword derives the stored form of a password.
//
// The cost is bcrypt's own default rather than a number of awb's choosing, so
// it follows the library as hardware moves. The cost of every stored hash is
// recorded in the hash itself, which is what lets an old one keep verifying
// after the default has risen.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "hash the password")
	}
	return string(hash), nil
}

// ParsePasswordHash reads a bcrypt hash computed elsewhere, so that a password
// can be set without the plaintext ever reaching awb.
//
// The form it is written in is what "htpasswd -Bn <name>" prints, which is
// "<name>:<hash>". Both that whole line and the bare hash are accepted, since
// a bcrypt hash never contains a colon and the two can therefore not be
// confused; a line naming somebody other than the user being written is
// refused rather than silently applied to the wrong account.
//
// Only bcrypt is accepted. htpasswd's other schemes — MD5, crypt and SHA-1 —
// are refused rather than stored, because a hash awb cannot verify is a login
// that would never work, and one it could verify but that is not bcrypt would
// be a weaker credential than the ones it writes itself.
func ParsePasswordHash(username, value string) (string, error) {
	hash := value
	if named, rest, found := strings.Cut(value, ":"); found {
		if named != username {
			return "", awberr.Usagef(
				"the password hash is written for %q, not for %q", named, username)
		}
		hash = rest
	}
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return "", awberr.Usagef(
			"that is not a bcrypt password hash: write one with \"htpasswd -Bn %s\"", username)
	}
	return hash, nil
}

// CheckPassword reports whether a password matches a stored hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// User is one account. The password hash is deliberately not a field: it never
// leaves the storage layer, so no listing, no response and no --json output
// can carry it by accident.
//
// Workspaces is the user's memberships, ordered by workspace key ascending. It is
// derived and read-only, exactly as an issue's relations are: membership is
// changed by its own operations.
type User struct {
	Name               string       `json:"name"`
	FullName           string       `json:"full_name"`
	WorkspaceAdmin     bool         `json:"workspace_admin"`
	UserAdmin          bool         `json:"user_admin"`
	CreatedAt          string       `json:"created_at"`
	UpdatedAt          string       `json:"updated_at"`
	Workspaces         []Membership `json:"workspaces"`
	ActivityWorkspaces []string     `json:"-"`
}

// Normalize replaces a nil membership slice with an empty one, so the JSON
// encoding carries [] and never null.
func (u *User) Normalize() {
	if u.Workspaces == nil {
		u.Workspaces = []Membership{}
	}
}

// Membership is one user's access to one workspace. Both ends are named
// whichever way round it is read, so the shape a workspace's member list returns
// and the shape a user's own listing returns are one shape.
type Membership struct {
	Workspace string `json:"workspace"`
	User      string `json:"user"`
	Access    Access `json:"access"`
}

// Caller is who is acting, as the authorization rules see them.
//
// It is built once per operation from the stored user row, and it is the only
// input those rules take beyond the membership of the workspace in question.
// Every rule below is a pure function of it, so the two surfaces cannot come
// to different conclusions about the same caller.
type Caller struct {
	// Name is the username the request authenticated as. It is empty when
	// Unrestricted is set and nobody authenticated.
	Name string

	// Unrestricted turns every rule below into a yes and lifts the visibility
	// scope entirely. It is what direct mode is: the CLI on a database file
	// applies no authorization at all, because whoever can open the file can
	// already read and write every byte of it, and a check there would be a
	// suggestion rather than a control. A server whose database holds no user
	// with a password, and so authenticates nobody, is the same case.
	Unrestricted bool

	// WorkspaceAdmin may create, change and delete workspaces, and holds
	// AccessAdmin in every one of them — including the ones nobody has been
	// given access to, since somebody has to be able to.
	WorkspaceAdmin bool
	// UserAdmin may create, change and delete users, which includes granting
	// both of these flags.
	UserAdmin bool
}

// AccessTo is the caller's effective access to a workspace, given the membership
// stored for them there and whether one is stored at all.
//
// It is where the two flags meet the membership table: an unrestricted caller
// and a workspace administrator hold AccessAdmin everywhere without a row saying
// so, and everybody else holds exactly what their row says.
func (c Caller) AccessTo(stored Access, member bool) (Access, bool) {
	if c.Unrestricted || c.WorkspaceAdmin {
		return AccessAdmin, true
	}
	return stored, member
}

// MaySeeWorkspace reports whether the workspace is visible to the caller at all.
// It is membership and nothing more: a workspace a user is not in does not exist
// for them, and neither do its issues.
func (c Caller) MaySeeWorkspace(stored Access, member bool) bool {
	_, ok := c.AccessTo(stored, member)
	return ok
}

// MayWorkOn reports whether the caller may change the issues of a workspace.
// Every access level may, membership being the whole of the question; what
// AccessAdmin adds is over the membership, not over the work.
func (c Caller) MayWorkOn(stored Access, member bool) bool {
	return c.MaySeeWorkspace(stored, member)
}

// MayAdministerWorkspace reports whether the caller may add and remove the
// workspace's users.
func (c Caller) MayAdministerWorkspace(stored Access, member bool) bool {
	access, ok := c.AccessTo(stored, member)
	return ok && access == AccessAdmin
}

// MayManageWorkspaces reports whether the caller may create, change and delete
// workspaces themselves. That is the workspace_admin flag alone: a workspace's own
// administrators run its membership, not its existence.
func (c Caller) MayManageWorkspaces() bool { return c.Unrestricted || c.WorkspaceAdmin }

// MayManageUsers reports whether the caller may create, change and delete
// users.
func (c Caller) MayManageUsers() bool { return c.Unrestricted || c.UserAdmin }

// MayManageBoardView keeps a personal view personal: administrative flags do
// not confer ownership of another user's saved workflow.
func (c Caller) MayManageBoardView(owner string) bool {
	return c.Unrestricted || c.Name == owner
}

// MaySeeUser reports whether the caller may read one user's account. A user
// may always read their own, which is how anybody without the flag learns what
// they are permitted to do.
func (c Caller) MaySeeUser(name string) bool {
	return c.MayManageUsers() || (c.Name != "" && c.Name == name)
}

// MaySetPasswordOf reports whether the caller may change one user's password.
// A user may always change their own; changing anybody else's is a user
// administrator's.
func (c Caller) MaySetPasswordOf(name string) bool { return c.MaySeeUser(name) }
