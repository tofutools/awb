// Package cli is the command line adapter. It owns the command tree, the three
// output modes and the exit codes — cobra's own defaults decide none of those.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/local"
	"github.com/tofutools/awb/internal/remote"
	"github.com/tofutools/awb/internal/storage"
)

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

	flags   config.Flags
	json    bool
	compact bool

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
func Execute(ctx context.Context, version string, args []string,
	stdout, stderr io.Writer, stdin io.Reader) int {
	workingDir, err := os.Getwd()
	if err != nil {
		workingDir = "."
	}

	e := &env{stdout: &errWriter{w: stdout}, stderr: stderr, stdin: stdin, workingDir: workingDir}
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

func newRootCommand(e *env, version string) *cobra.Command {
	var (
		dbFlag    string
		colorFlag string
	)

	root := &cobra.Command{
		Use:   "awb",
		Short: "Agent Work Board — an agent-first issue tracker",
		Long: "awb is an agent-first issue tracker: a single binary over SQLite, with a\n" +
			"command line interface for coding agents, humans and scripts.\n\n" +
			"Every command is non-interactive and safe to script. --compact is the\n" +
			"cheapest output there is and --json is the stable one; the default table is\n" +
			"for humans and nothing should parse it.",
		Version: version,
		// awb owns error output and the exit codes, so cobra's own usage-on-error
		// and error printing are switched off and a usage error exits 2 rather than
		// cobra's 1.
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("db") {
				e.flags.DB = &dbFlag
			}
			if cmd.Flags().Changed("color") {
				e.flags.Color = &colorFlag
			}
			if e.json && e.compact {
				return awberr.Usagef("--json and --compact are mutually exclusive")
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&dbFlag, "db", "",
		"database file or http(s) URL of an awb server")
	root.PersistentFlags().BoolVar(&e.json, "json", false,
		"print stable JSON, one object or array per invocation")
	root.PersistentFlags().BoolVar(&e.compact, "compact", false,
		"print one terse line per issue, for agents")
	root.PersistentFlags().BoolVar(&e.flags.NoContext, "no-context", false,
		"ignore the project and label of the local configuration file")
	root.PersistentFlags().StringVar(&colorFlag, "color", "auto",
		"when to colour the default output: auto, always or never")
	root.PersistentFlags().BoolVar(&e.flags.NoColor, "no-color", false,
		"alias for --color never")

	root.SetVersionTemplate("{{.Version}}\n")

	// A flag cobra does not recognise is a usage error like any other, which is
	// what makes --project exit 2 on the commands that do not take it.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return awberr.Usagef("%s", err.Error())
	})

	root.AddCommand(
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
		newServeCommand(e),
	)
	return root
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
	e.be = local.New(db, cfg.Identity)
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

// exactArgs is cobra.ExactArgs with awb's own classification, so a wrong
// argument count exits 2 like every other usage mistake.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return awberr.Usagef("%s takes exactly %d argument(s), got %d",
				cmd.CommandPath(), n, len(args))
		}
		return nil
	}
}

// minArgs is cobra.MinimumNArgs with awb's classification.
func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return awberr.Usagef("%s takes at least %d argument(s), got %d",
				cmd.CommandPath(), n, len(args))
		}
		return nil
	}
}

// noArgs rejects any positional argument.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return awberr.Usagef("%s takes no arguments", cmd.CommandPath())
	}
	return nil
}
