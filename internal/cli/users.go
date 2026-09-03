package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

func newUserCommand(e *env) *cobra.Command {
	return group("user", "Manage the users a server authenticates and authorizes",
		"A user is an account awb serve authenticates, and whose permissions it\n"+
			"then applies to every request.\n\n"+
			"None of it applies to the command line on a database file: direct mode\n"+
			"applies no authorization at all, because whoever can open the file can\n"+
			"already read and write every byte of it. That is also how the first user\n"+
			"is created, and how an instance whose last administrator is gone is\n"+
			"recovered.\n\n"+
			"A user's password is optional. One without a password is an assignee and\n"+
			"nothing else: the tracker knows the name, and nobody can log in as it. That\n"+
			"is what a database with no server at all wants, and what an agent working\n"+
			"through the command line is.\n\n"+
			"A database holding no user with a password is a server without\n"+
			"authentication. Adding the first password turns it on, from the next request\n"+
			"onwards, and deleting them all does not turn it off again: that takes\n"+
			"awb serve --no-auth.",
		newUserAddCommand(e),
		newUserUpdateCommand(e),
		newUserShowCommand(e),
		newUserListCommand(e),
		newUserDeleteCommand(e),
	)
}

// PasswordFlags are the two ways of stating a credential.
//
// There is deliberately no flag carrying a password itself. One given on the
// command line is in the process listing while the command runs and in the
// shell history afterwards, and neither is somewhere a credential belongs. So
// a password arrives on stdin — typed without echo when there is a terminal to
// type at, and piped in when there is not — or does not arrive at all, the
// caller having hashed it themselves.
//
// --password says to read one, and means the same thing on both commands that
// take a credential: neither flag is a user who cannot log in, which is an
// account that exists to be an assignee.
type PasswordFlags struct {
	Password     bool    `long:"password" optional:"true" help:"read a password from stdin; without it the user cannot log in"`
	PasswordHash *string `long:"password-hash" help:"a bcrypt hash, as \"htpasswd -Bn <name>\" writes one, instead of a password"`
}

// value returns the two ways of stating the credential, at most one of which
// is non-empty; both are empty when neither flag was given. It reads stdin
// when it has to, which is why it is called before anything is sent.
func (f *PasswordFlags) value(e *env, username string) (password, hash string, err error) {
	if f.PasswordHash != nil {
		if f.Password {
			return "", "", awberr.Usagef("--password and --password-hash are mutually exclusive")
		}
		return "", *f.PasswordHash, nil
	}
	if !f.Password {
		return "", "", nil
	}
	password, err = readPassword(e, username)
	if err != nil {
		return "", "", err
	}
	return password, "", nil
}

// readPassword takes a password from stdin without ever putting it in argv.
//
// At a terminal it is prompted for and typed without echo, and asked for a
// second time, because a value typed blind and stored hashed can never be
// compared with what was meant afterwards. The prompts go to stderr, so that
// --json output redirected to a file is still only JSON.
//
// Without a terminal it is read from stdin as it arrives, with one trailing
// line ending removed, which is what "echo secret | awb user add alice" sends.
// Nothing else is trimmed: a password may legitimately end in a space, and
// quietly dropping one would leave an account nobody could log in to.
func readPassword(e *env, username string) (string, error) {
	file, ok := e.stdin.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return readPipedPassword(e)
	}

	first, err := promptPassword(e, file, fmt.Sprintf("Password for %s: ", username))
	if err != nil {
		return "", err
	}
	again, err := promptPassword(e, file, "Repeat the password: ")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", awberr.Usagef("the two passwords do not match")
	}
	return first, nil
}

func promptPassword(e *env, file *os.File, prompt string) (string, error) {
	_, _ = fmt.Fprint(e.stderr, prompt)
	typed, err := term.ReadPassword(file.Fd())
	_, _ = fmt.Fprintln(e.stderr)
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "read the password")
	}
	return string(typed), nil
}

func readPipedPassword(e *env) (string, error) {
	data, err := io.ReadAll(e.stdin)
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "read the password from stdin")
	}
	password := strings.TrimSuffix(string(data), "\n")
	password = strings.TrimSuffix(password, "\r")
	if password == "" {
		return "", awberr.Usagef("no password on stdin")
	}
	return password, nil
}

type userAddParams struct {
	PasswordFlags
	Name           string `positional:"true" required:"true"`
	FullName       string `long:"full-name" optional:"true" help:"the user's descriptive name"`
	WorkspaceAdmin bool   `long:"workspace-admin" optional:"true" help:"may create, change and delete workspaces, and works in every one of them"`
	UserAdmin      bool   `long:"user-admin" optional:"true" help:"may create, change and delete users"`
}

func newUserAddCommand(e *env) *cobra.Command {
	return boa.CmdT[userAddParams]{
		Use:   "add",
		Short: "Create a user",
		Long: "Create a user.\n\n" +
			"Without --password or --password-hash the account has no password: it can be\n" +
			"an assignee and nobody can log in as it, which is what a tracker with no\n" +
			"server, and an agent working through the command line, wants.\n\n" +
			"--password reads one from stdin. At a terminal it is prompted for and typed\n" +
			"without echo, and asked for twice; otherwise it is read from stdin, so\n" +
			"\"echo secret | awb user add alice --password\" works in a script. It is never\n" +
			"a flag value: a password on the command line is in the process listing and in\n" +
			"the shell history.\n\n" +
			"--password-hash takes a bcrypt hash computed elsewhere instead, so the\n" +
			"plaintext never reaches awb at all. It is what \"htpasswd -Bn <name>\"\n" +
			"writes, and either that whole line or the hash alone is accepted.\n\n" +
			"The name is the assignee the user's issues will record, and is immutable.\n" +
			"A new user has access to no workspace: grant it with awb workspace grant.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *userAddParams, cmd *cobra.Command, _ []string) error {
			password, hash, err := p.value(e, p.Name)
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			user, err := be.CreateUser(cmd.Context(), backend.UserCreate{
				Name:           p.Name,
				FullName:       p.FullName,
				Password:       password,
				PasswordHash:   hash,
				WorkspaceAdmin: p.WorkspaceAdmin,
				UserAdmin:      p.UserAdmin,
			})
			if err != nil {
				return err
			}
			return e.mutatedUser(user)
		},
	}.ToCobra()
}

type userUpdateParams struct {
	PasswordFlags
	Name     string  `positional:"true" required:"true"`
	FullName *string `long:"full-name" help:"replace the user's descriptive name; empty clears it"`
	// The two flags are pointers because each has three states and not two:
	// granted, withdrawn, and left exactly as it was.
	WorkspaceAdmin *bool `long:"workspace-admin" help:"may create, change and delete workspaces, and works in every one of them"`
	UserAdmin      *bool `long:"user-admin" help:"may create, change and delete users"`
}

func newUserUpdateCommand(e *env) *cobra.Command {
	return boa.CmdT[userUpdateParams]{
		Use:   "update",
		Short: "Change a user's full name, password or what they may do",
		Long: "Change a user's full name, password or either of their two flags. The username itself is\n" +
			"immutable: it is the assignee their issues record.\n\n" +
			"--password reads a new password exactly as awb user add does, and is how an\n" +
			"account created without one is given the ability to log in.\n\n" +
			"Through a server the two halves are permitted separately: the flags are a\n" +
			"user administrator's, and the full name and password are theirs or the account\n" +
			"holder's own, so anybody may change their own profile without being able to grant\n" +
			"themselves anything.\n\n" +
			"Access to a workspace is not changed here; that is awb workspace grant.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *userUpdateParams, cmd *cobra.Command, _ []string) error {
			password, hash, err := p.value(e, p.Name)
			if err != nil {
				return err
			}

			patch := backend.UserPatch{
				FullName:       p.FullName,
				WorkspaceAdmin: p.WorkspaceAdmin,
				UserAdmin:      p.UserAdmin,
			}
			if password != "" {
				patch.Password = &password
			}
			if hash != "" {
				patch.PasswordHash = &hash
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			user, err := be.UpdateUser(cmd.Context(), p.Name, patch, "")
			if err != nil {
				return err
			}
			return e.mutatedUser(user)
		},
	}.ToCobra()
}

type userShowParams struct {
	// Optional, because with no name it prints the caller's own account.
	Name *string `positional:"true"`
}

func newUserShowCommand(e *env) *cobra.Command {
	return boa.CmdT[userShowParams]{
		Use:   "show",
		Short: "Print one user, with the workspaces they have access to",
		Long: "Print a user with their two flags and every workspace they have access to.\n\n" +
			"Without a name it prints your own account, which is how you find out what\n" +
			"you are permitted to do: through a server a user may always read their\n" +
			"own, whatever else they may not.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *userShowParams, cmd *cobra.Command, _ []string) error {
			name := ""
			if p.Name != nil {
				name = *p.Name
			} else {
				var err error
				if name, err = e.identity(); err != nil {
					return err
				}
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			user, err := be.GetUser(cmd.Context(), name)
			if err != nil {
				return err
			}
			return e.printUser(user)
		},
	}.ToCobra()
}

// userListParams is the picker flag plus the window. The order is fixed at
// name ascending, so there is no --sort to go with them.
type userListParams struct {
	InteractiveFlags
	Limit  *int `long:"limit" optional:"true" help:"cap the number of results; zero returns none"`
	Offset *int `long:"offset" optional:"true" help:"skip this many results"`
}

func newUserListCommand(e *env) *cobra.Command {
	return boa.CmdT[userListParams]{
		Use:         "list",
		Short:       "List users, with their flags and the workspaces they have access to",
		ParamEnrich: boaParams,
		RunFuncE: func(p *userListParams, cmd *cobra.Command, _ []string) error {
			out, err := e.interactively(p.Interactive)
			if err != nil {
				return err
			}
			if err := checkPaging(p.Limit, p.Offset); err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListUsers(cmd.Context(), "", p.Limit, p.Offset)
			if err != nil {
				return err
			}
			if out != nil {
				return e.pickUser(cmd.Context(), be, out, page.Users)
			}
			return e.printUsers(page.Users)
		},
	}.ToCobra()
}

type userDeleteParams struct {
	Name  string `positional:"true" required:"true"`
	Force bool   `long:"force" optional:"true" help:"confirm the deletion"`
}

func newUserDeleteCommand(e *env) *cobra.Command {
	return boa.CmdT[userDeleteParams]{
		Use:   "delete",
		Short: "Delete a user",
		Long: "Delete a user. Every access they had to a workspace goes with them.\n\n" +
			"The issues they were assigned are left exactly as they are: an assignee\n" +
			"records who holds or held a piece of work, and rewriting that because\n" +
			"somebody's access was withdrawn would lose the only record of who did it.\n\n" +
			"Deleting the last user does not turn a server's authentication off again:\n" +
			"the server answers nothing until a user is added back, rather than serving\n" +
			"everybody, and one started afterwards starts in that state rather than\n" +
			"open. Serving such a database openly is awb serve --no-auth.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *userDeleteParams, cmd *cobra.Command, _ []string) error {
			if !p.Force {
				return awberr.Usagef("awb user delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteUser(cmd.Context(), p.Name, "")
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(&deleted.User)
			}
			return e.summarise("Deleted user %s.\n", deleted.User.Name)
		},
	}.ToCobra()
}

type workspaceGrantParams struct {
	Key    string `positional:"true" required:"true"`
	User   string `positional:"true" required:"true"`
	Access string `long:"access" default:"regular" optional:"true" alts:"regular,admin" help:"regular to work with the workspace's issues, admin to also grant and revoke its users"`
}

func newWorkspaceGrantCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceGrantParams]{
		Use:   "grant",
		Short: "Give a user access to a workspace",
		Long: "Give a user access to a workspace, replacing whatever they had there before,\n" +
			"so granting the access they already hold changes nothing.\n\n" +
			"regular is working with the workspace's issues: reading them, creating them,\n" +
			"editing them, claiming them, everything awb does to an issue. admin is that\n" +
			"and, in addition, granting and revoking the workspace's other users.\n\n" +
			"admin is not power over the workspace itself. Creating, renaming and deleting\n" +
			"a workspace is what awb user's --workspace-admin flag confers, because a workspace's\n" +
			"own existence is not something its members decide.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *workspaceGrantParams, cmd *cobra.Command, _ []string) error {
			access, err := domain.ParseAccess(p.Access)
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			membership, err := be.SetMember(cmd.Context(), p.Key, p.User, access)
			if err != nil {
				return err
			}
			return e.mutatedMembership(membership)
		},
	}.ToCobra()
}

type workspaceRevokeParams struct {
	Key  string `positional:"true" required:"true"`
	User string `positional:"true" required:"true"`
}

func newWorkspaceRevokeCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceRevokeParams]{
		Use:   "revoke",
		Short: "Take away a user's access to a workspace",
		Long: "Take away a user's access to a workspace, which makes the workspace and every\n" +
			"issue in it invisible to them.\n\n" +
			"Their issues are left exactly as they are, assignee included: the record of\n" +
			"who did the work outlives the access that let them do it.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *workspaceRevokeParams, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			membership, err := be.RemoveMember(cmd.Context(), p.Key, p.User)
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(membership)
			}
			return e.summarise("Revoked %s access of %s to workspace %s.\n",
				membership.Access, membership.User, membership.Workspace)
		},
	}.ToCobra()
}

type workspaceMembersParams struct {
	Key string `positional:"true" required:"true"`
}

func newWorkspaceMembersCommand(e *env) *cobra.Command {
	return boa.CmdT[workspaceMembersParams]{
		Use:         "members",
		Short:       "List the users with access to a workspace",
		ParamEnrich: boaParams,
		RunFuncE: func(p *workspaceMembersParams, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListMembers(cmd.Context(), p.Key, nil, nil)
			if err != nil {
				return err
			}
			return e.printMemberships(page.Members)
		},
	}.ToCobra()
}
