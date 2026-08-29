// Package cli is the command line adapter. It owns the command tree, the three
// output modes and the exit codes — cobra's own defaults decide none of those.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/openapi"
	"github.com/tofutools/awb/internal/remote"
	"github.com/tofutools/awb/internal/storage"
)

// boaParams applies Boa's conventional generated names, short flags and bool
// defaults to every declarative parameter struct.
var boaParams = boa.ParamEnricherDefault

// command is the common Boa declaration for a command without flags. Commands
// with flags use CmdT directly so their parameter struct stays visible beside
// the command it describes.
func command(use, short, long string, run func(*cobra.Command, []string) error) *cobra.Command {
	return boa.CmdT[boa.NoParams]{
		Use: use, Short: short, Long: long,
		ParamEnrich: boaParams,
		RunFuncE: func(_ *boa.NoParams, cmd *cobra.Command, positional []string) error {
			return run(cmd, positional)
		},
	}.ToCobra()
}

type idParams struct {
	ID string `positional:"true" required:"true"`
}

func idCommand(use, short, long string,
	run func(*cobra.Command, string) error) *cobra.Command {
	return boa.CmdT[idParams]{
		Use: use, Short: short, Long: long, ParamEnrich: boaParams,
		RunFuncE: func(p *idParams, cmd *cobra.Command, _ []string) error {
			return run(cmd, p.ID)
		},
	}.ToCobra()
}

type idNameParams struct {
	ID   string `positional:"true" required:"true"`
	Name string `positional:"true" required:"true"`
}

func idNameCommand(use, short, long string,
	run func(*cobra.Command, string, string) error) *cobra.Command {
	return boa.CmdT[idNameParams]{
		Use: use, Short: short, Long: long, ParamEnrich: boaParams,
		RunFuncE: func(p *idNameParams, cmd *cobra.Command, _ []string) error {
			return run(cmd, p.ID, p.Name)
		},
	}.ToCobra()
}

func group(use, short, long string, subcommands ...*cobra.Command) *cobra.Command {
	return grouping(boa.CmdT[boa.NoParams]{
		Use: use, Short: short, Long: long, SubCmds: subcommands,
		ParamEnrich: boaParams,
	}.ToCobra())
}

// errWriter records the first write error, so the output helpers can report it
// once at the end rather than checking every Fprint call. A closed pipe or a
// full disk is a runtime failure like any other and must not pass as success.
type errWriter struct {
	w   io.Writer
	err error
}

func (w *errWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.w.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

// Err reports the first write failure, if any.
func (w *errWriter) Err() error {
	if w.err == nil {
		return nil
	}
	return awberr.Wrap(awberr.Runtime, w.err, "write output")
}

// env is everything one invocation needs, threaded through the command tree.
type env struct {
	stdout *errWriter
	stderr io.Writer
	stdin  io.Reader

	// workingDir is where the upward search for directory context starts.
	workingDir string

	// boxed says stdout is a terminal and width says how wide it is; the
	// default output mode draws boxes and fits itself to the window only when
	// there is one. Both are decided once, from the writer Execute was handed,
	// before anything wraps it.
	boxed bool
	width int

	flags   config.Flags
	json    bool
	compact bool

	// openAPI is the document serve publishes at /openapi.json and
	// /openapi.yaml. It arrives from main because openapi.yaml sits at the
	// repository root — it is the source the Go server and the TypeScript
	// client are generated from — and only the package there can embed it.
	openAPI *openapi.Document

	// cfg and be are resolved lazily, because init and agent-guide need different
	// amounts of it and --help needs none.
	cfg *config.Config
	be  backend.Backend
}

// Execute runs awb and returns the process exit status.
//
// Errors always go to stderr, as a single line in the default and compact
// modes and as {"error": "..."} under --json. The exit code is the
// machine-readable classification; the message is human-readable text.
func Execute(ctx context.Context, version string, document *openapi.Document, args []string,
	stdout, stderr io.Writer, stdin io.Reader) int {
	workingDir, err := os.Getwd()
	if err != nil {
		workingDir = "."
	}

	boxed, width := window(stdout)
	e := &env{
		stdout: &errWriter{w: stdout}, stderr: stderr, stdin: stdin,
		workingDir: workingDir, boxed: boxed, width: width, openAPI: document,
	}
	root := newRootCommand(e, version)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	runErr := root.ExecuteContext(ctx)
	if e.be != nil {
		_ = e.be.Close()
	}
	if runErr == nil {
		runErr = e.stdout.Err()
	}
	if runErr == nil {
		return 0
	}
	if boa.IsUserInputError(runErr) {
		runErr = awberr.Usagef("%s", runErr.Error())
	}

	e.reportError(runErr)
	return awberr.ExitCode(runErr)
}

func (e *env) reportError(err error) {
	if e.json {
		_, _ = fmt.Fprintf(e.stderr, "{%q: %q}\n", "error", err.Error())
		return
	}
	_, _ = fmt.Fprintln(e.stderr, err.Error())
}

type rootParams struct {
	DB          *string `long:"db" persistent:"true" help:"database file or http(s) URL of an awb server"`
	Attachments *string `long:"attachments" persistent:"true" help:"directory holding attachment content; defaults to \"attachments\" beside the database"`
	JSON        bool    `long:"json" persistent:"true" optional:"true" help:"print stable JSON, one object or array per invocation"`
	Compact     bool    `long:"compact" persistent:"true" optional:"true" help:"print one terse line per issue, for agents"`
	NoContext   bool    `long:"no-context" persistent:"true" optional:"true" help:"ignore the project and label of the local configuration file"`
	Color       string  `long:"color" persistent:"true" default:"auto" optional:"true" help:"when to colour the default output: auto, always or never"`
	NoColor     bool    `long:"no-color" persistent:"true" optional:"true" help:"alias for --color never"`
}

func newRootCommand(e *env, version string) *cobra.Command {
	root := boa.CmdT[rootParams]{
		Use:   "awb",
		Short: "Agent Work Board — an agent-first issue tracker",
		Long: "awb is an agent-first issue tracker: a single binary over SQLite, with a\n" +
			"command line interface for coding agents, humans and scripts.\n\n" +
			"Every command is non-interactive and safe to script. --compact is the\n" +
			"cheapest output there is and --json is the stable one; the default table is\n" +
			"for humans and nothing should parse it.",
		Version:     version,
		ParamEnrich: boaParams,
		SubCmds: []*cobra.Command{
			newInitCommand(e),
			newAgentGuideCommand(e),
			newProjectCommand(e),
			newCreateCommand(e),
			newShowCommand(e),
			newListCommand(e),
			newReadyCommand(e),
			newBlockedCommand(e),
			newSearchCommand(e),
			newUpdateCommand(e),
			newLabelCommand(e),
			newClaimCommand(e),
			newReleaseCommand(e),
			newCloseCommand(e),
			newReopenCommand(e),
			newDeleteCommand(e),
			newDepCommand(e),
			newAttachCommand(e),
			newDemoCommand(e),
			newServeCommand(e),
		},
		PreValidateFunc: func(p *rootParams, cmd *cobra.Command, _ []string) error {
			e.flags.DB = p.DB
			e.flags.Attachments = p.Attachments
			e.flags.NoContext = p.NoContext
			e.flags.NoColor = p.NoColor
			if cmd.Flags().Changed("color") {
				e.flags.Color = &p.Color
			}
			e.json = p.JSON
			e.compact = p.Compact
			if e.json && e.compact {
				return awberr.Usagef("--json and --compact are mutually exclusive")
			}
			return nil
		},
	}.ToCobra()
	// awb owns error output and the exit codes, so Cobra's own usage-on-error
	// and error printing are switched off and a usage error exits 2 rather than
	// Cobra's 1.
	root.SilenceUsage = true
	root.SilenceErrors = true

	root.SetVersionTemplate("{{.Version}}\n")

	// A flag cobra does not recognise is a usage error like any other, which is
	// what makes --project exit 2 on the commands that do not take it.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return awberr.Usagef("%s", err.Error())
	})

	return grouping(root)
}

// config resolves the configuration once per invocation.
func (e *env) config() (*config.Config, error) {
	if e.cfg != nil {
		return e.cfg, nil
	}
	cfg, err := config.Load(e.flags, e.workingDir)
	if err != nil {
		return nil, err
	}
	e.cfg = cfg
	return cfg, nil
}

// backend opens whichever backend the configuration points at. Direct mode
// opens the database file; remote mode builds an HTTP client. Every command
// behaves identically either way, because it cannot tell them apart.
func (e *env) backend(ctx context.Context) (backend.Backend, error) {
	if e.be != nil {
		return e.be, nil
	}
	cfg, err := e.config()
	if err != nil {
		return nil, err
	}

	if cfg.Remote() {
		e.be = remote.New(cfg.RemoteURL, cfg.User, cfg.Password, cfg.Identity)
		return e.be, nil
	}

	db, err := storage.Open(ctx, cfg.DB)
	if err != nil {
		return nil, err
	}
	e.be = local.New(db, storage.NewBlobs(cfg.Attachments), cfg.Identity)
	return e.be, nil
}

// requireLocal refuses a remote database for the two commands that are about a
// local file rather than about the data.
func (e *env) requireLocal(what string) (*config.Config, error) {
	cfg, err := e.config()
	if err != nil {
		return nil, err
	}
	if cfg.Remote() {
		return nil, awberr.Usagef("awb %s works on a local database file, but --db is %s", what, cfg.DB)
	}
	return cfg, nil
}

// identity returns the caller's identity, failing before anything is sent when
// there is none.
func (e *env) identity() (string, error) {
	cfg, err := e.config()
	if err != nil {
		return "", err
	}
	if cfg.Identity == "" {
		return "", awberr.Runtimef(
			"no identity is configured: set \"identity\" in %s, or AWB_IDENTITY",
			"the user configuration file")
	}
	return cfg.Identity, nil
}

// grouping prepares a command that only holds subcommands, so that a name it
// does not have is reported rather than swallowed: bare, it prints its own
// help, and given anything else it fails as a usage error. Both halves are
// needed, because cobra consults Args only on a runnable command and applies
// its own unknown-command check only to the root — so without them a removed
// or mistyped name, awb project add, prints the group's help and exits 0, and
// nothing can tell that spelling from a working one. With them an unknown
// command is the exit 2 the guide documents, at every level of the tree.
func grouping(cmd *cobra.Command) *cobra.Command {
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return awberr.Usagef("unknown command %q for %q", args[0], cmd.CommandPath())
		}
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	return cmd
}
