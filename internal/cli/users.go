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
			"A database holding no user is a server without authentication. Adding the\n"+
			"first one turns it on, from the next request onwards, and deleting them all\n"+
			"does not turn it off again: that takes awb serve --no-auth.",
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
// --password says to read one, and is therefore only meaningful where taking a
// new password is a choice: awb user add always needs one and hides the flag,
// exactly as the listings that fix a status set for themselves hide --status.
type PasswordFlags struct {
	Password     bool    `long:"password" optional:"true" help:"read a new password from stdin, as awb user add does"`
	PasswordHash *string `long:"password-hash" help:"a bcrypt hash, as \"htpasswd -Bn <name>\" writes one, instead of a password"`
}

// alwaysReadsAPassword hides --password on a command that takes one either
// way.
func alwaysReadsAPassword() func(*boa.HookContext, *PasswordFlags, *cobra.Command) error {
	return func(ctx *boa.HookContext, f *PasswordFlags, _ *cobra.Command) error {
		boa.GetParamT(ctx, &f.Password).SetIgnored(true)
		return nil
	}
}

// value returns the two ways of stating the credential, at most one of which
// is non-empty. It reads stdin when it has to, which is why it is called
// before anything is sent.
//
// required is set where an account is being made, which cannot be made without
// one.
func (f *PasswordFlags) value(e *env, username string, required bool) (password, hash string, err error) {
	if f.PasswordHash != nil {
		if f.Password {
			return "", "", awberr.Usagef("--password and --password-hash are mutually exclusive")
		}
		return "", *f.PasswordHash, nil
	}
	if !f.Password && !required {
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
	Name         string `positional:"true" required:"true"`
	ProjectAdmin bool   `long:"project-admin" optional:"true" help:"may create, change and delete projects, and works in every one of them"`
	UserAdmin    bool   `long:"user-admin" optional:"true" help:"may create, change and delete users"`
}

func newUserAddCommand(e *env) *cobra.Command {
	return boa.CmdT[userAddParams]{
		Use:   "add",
		Short: "Create a user",
		Long: "Create a user, reading their password from stdin.\n\n" +
			"At a terminal the password is prompted for and typed without echo, and\n" +
			"asked for twice; otherwise it is read from stdin, so\n" +
			"\"echo secret | awb user add alice\" works in a script. It is never a flag:\n" +
			"a password on the command line is in the process listing and in the shell\n" +
			"history.\n\n" +
			"--password-hash takes a bcrypt hash computed elsewhere instead, so the\n" +
			"plaintext never reaches awb at all. It is what \"htpasswd -Bn <name>\"\n" +
			"writes, and either that whole line or the hash alone is accepted.\n\n" +
			"The name is the assignee the user's issues will record, and is immutable.\n" +
			"A new user has access to no project: grant it with awb project grant.",
		ParamEnrich: boaParams,
		InitFuncCtx: func(ctx *boa.HookContext, p *userAddParams, cmd *cobra.Command) error {
			return alwaysReadsAPassword()(ctx, &p.PasswordFlags, cmd)
		},
		RunFuncE: func(p *userAddParams, cmd *cobra.Command, _ []string) error {
			password, hash, err := p.value(e, p.Name, true)
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			user, err := be.CreateUser(cmd.Context(), backend.UserCreate{
				Name:         p.Name,
				Password:     password,
				PasswordHash: hash,
				ProjectAdmin: p.ProjectAdmin,
				UserAdmin:    p.UserAdmin,
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
	Name string `positional:"true" required:"true"`
	// The two flags are pointers because each has three states and not two:
	// granted, withdrawn, and left exactly as it was.
	ProjectAdmin *bool `long:"project-admin" help:"may create, change and delete projects, and works in every one of them"`
	UserAdmin    *bool `long:"user-admin" help:"may create, change and delete users"`
}

func newUserUpdateCommand(e *env) *cobra.Command {
	return boa.CmdT[userUpdateParams]{
		Use:   "update",
		Short: "Change a user's password or what they may do",
		Long: "Change a user's password or either of their two flags. The name itself is\n" +
			"immutable: it is the assignee their issues record.\n\n" +
			"--password reads a new password exactly as awb user add does.\n\n" +
			"Through a server the two halves are permitted separately: the flags are a\n" +
			"user administrator's, and the password is theirs or the account holder's\n" +
			"own, so anybody may change their own password without being able to grant\n" +
			"themselves anything.\n\n" +
			"Access to a project is not changed here; that is awb project grant.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *userUpdateParams, cmd *cobra.Command, _ []string) error {
			password, hash, err := p.value(e, p.Name, false)
			if err != nil {
				return err
			}

			patch := backend.UserPatch{
				ProjectAdmin: p.ProjectAdmin,
				UserAdmin:    p.UserAdmin,
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
		Short: "Print one user, with the projects they have access to",
		Long: "Print a user with their two flags and every project they have access to.\n\n" +
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

func newUserListCommand(e *env) *cobra.Command {
	return boa.CmdT[InteractiveFlags]{
		Use:         "list",
		Short:       "List users, with their flags and the projects they have access to",
		ParamEnrich: boaParams,
		RunFuncE: func(p *InteractiveFlags, cmd *cobra.Command, _ []string) error {
			out, err := e.interactively(p.Interactive)
			if err != nil {
				return err
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListUsers(cmd.Context(), nil, nil)
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
		Long: "Delete a user. Every access they had to a project goes with them.\n\n" +
			"The issues they were assigned are left exactly as they are: an assignee\n" +
			"records who holds or held a piece of work, and rewriting that because\n" +
			"somebody's access was withdrawn would lose the only record of who did it.\n\n" +
			"Deleting the last user does not turn a server's authentication off again:\n" +
			"a running server that has authenticated answers nothing until a user is\n" +
			"added back, rather than serving everybody.",
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

type projectGrantParams struct {
	Key    string `positional:"true" required:"true"`
	User   string `positional:"true" required:"true"`
	Access string `long:"access" default:"regular" optional:"true" alts:"regular,admin" help:"regular to work with the project's issues, admin to also grant and revoke its users"`
}

func newProjectGrantCommand(e *env) *cobra.Command {
	return boa.CmdT[projectGrantParams]{
		Use:   "grant",
		Short: "Give a user access to a project",
		Long: "Give a user access to a project, replacing whatever they had there before,\n" +
			"so granting the access they already hold changes nothing.\n\n" +
			"regular is working with the project's issues: reading them, creating them,\n" +
			"editing them, claiming them, everything awb does to an issue. admin is that\n" +
			"and, in addition, granting and revoking the project's other users.\n\n" +
			"admin is not power over the project itself. Creating, renaming and deleting\n" +
			"a project is what awb user's --project-admin confers, because a project's\n" +
			"own existence is not something its members decide.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *projectGrantParams, cmd *cobra.Command, _ []string) error {
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

type projectRevokeParams struct {
	Key  string `positional:"true" required:"true"`
	User string `positional:"true" required:"true"`
}

func newProjectRevokeCommand(e *env) *cobra.Command {
	return boa.CmdT[projectRevokeParams]{
		Use:   "revoke",
		Short: "Take away a user's access to a project",
		Long: "Take away a user's access to a project, which makes the project and every\n" +
			"issue in it invisible to them.\n\n" +
			"Their issues are left exactly as they are, assignee included: the record of\n" +
			"who did the work outlives the access that let them do it.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *projectRevokeParams, cmd *cobra.Command, _ []string) error {
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
			return e.summarise("Revoked %s access of %s to project %s.\n",
				membership.Access, membership.User, membership.Project)
		},
	}.ToCobra()
}

type projectMembersParams struct {
	Key string `positional:"true" required:"true"`
}

func newProjectMembersCommand(e *env) *cobra.Command {
	return boa.CmdT[projectMembersParams]{
		Use:         "members",
		Short:       "List the users with access to a project",
		ParamEnrich: boaParams,
		RunFuncE: func(p *projectMembersParams, cmd *cobra.Command, _ []string) error {
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
