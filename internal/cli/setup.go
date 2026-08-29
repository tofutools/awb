package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/storage"
)

func newInitCommand(e *env) *cobra.Command {
	return command("init", "Create the database if absent and bring its schema up to date",
		"Create the database, together with any missing parent directory, and the\n"+
			"directory attachment content is stored in.\n\n"+
			"This is the only command that creates one: any other command that finds it\n"+
			"missing fails and names the path, so that a typo in --db or AWB_DB cannot\n"+
			"silently produce a second, empty tracker.\n\n"+
			"init takes no arguments and is idempotent.", func(cmd *cobra.Command, _ []string) error {
			cfg, err := e.requireLocal("init")
			if err != nil {
				return err
			}
			db, err := storage.Init(cmd.Context(), cfg.DB)
			if err != nil {
				return err
			}
			defer db.Close() //nolint:errcheck // closed again below, for its error
			// The attachments directory is created here rather than on the first
			// upload, so that the whole layout exists as soon as init has run and
			// a mistyped --attachments is visible immediately.
			if err := storage.NewBlobs(cfg.Attachments).Create(); err != nil {
				return err
			}
			// init produces no object and ignores both output-mode flags on success.
			return db.Close()
		})
}

// The exact marker lines agent-guide --write delimits its block with, so that
// a second run, by any version of the binary, replaces the existing block
// rather than appending a duplicate.
const (
	guideBeginMarker = "<!-- awb:begin -->"
	guideEndMarker   = "<!-- awb:end -->"
)

type agentGuideParams struct {
	Write *string `long:"write" help:"write the block into this file instead of stdout"`
}

func newAgentGuideCommand(e *env) *cobra.Command {
	return boa.CmdT[agentGuideParams]{
		Use:   "agent-guide",
		Short: "Print a compact usage block for agents",
		Long: "Print a short block teaching an agent the whole of awb's vocabulary.\n\n" +
			"--write instead writes it into a file, typically AGENTS.md or CLAUDE.md,\n" +
			"delimited by marker lines so that a second run replaces the block rather\n" +
			"than appending a duplicate.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *agentGuideParams, _ *cobra.Command, _ []string) error {
			if p.Write == nil {
				_, err := fmt.Fprint(e.stdout, AgentGuide)
				return err
			}
			return writeGuideBlock(*p.Write, AgentGuide)
		},
	}.ToCobra()
}

// writeGuideBlock inserts or replaces awb's block in path.
//
// When the file exists and holds no such block, the block is appended at the
// end, preceded by a blank line. A file holding only one of the two markers,
// or holding them in the wrong order, fails rather than gaining a second
// block.
func writeGuideBlock(path, guide string) error {
	block := guideBeginMarker + "\n" + strings.TrimRight(guide, "\n") + "\n" + guideEndMarker + "\n"

	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(block), 0o644); err != nil { //nolint:gosec // a documentation file
			return awberr.Wrap(awberr.Runtime, err, "write %s", path)
		}
		return nil
	}
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "read %s", path)
	}

	updated, err := replaceGuideBlock(path, string(existing), block)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil { //nolint:gosec // a documentation file
		return awberr.Wrap(awberr.Runtime, err, "write %s", path)
	}
	return nil
}

func replaceGuideBlock(path, existing, block string) (string, error) {
	begin := strings.Index(existing, guideBeginMarker)
	end := strings.Index(existing, guideEndMarker)

	switch {
	case begin < 0 && end < 0:
		// No block yet: append it, preceded by a blank line.
		trimmed := strings.TrimRight(existing, "\n")
		if trimmed == "" {
			return block, nil
		}
		return trimmed + "\n\n" + block, nil

	case begin < 0 || end < 0:
		return "", awberr.Runtimef(
			"%s holds only one of the %s and %s markers; fix it by hand rather than gaining a second block",
			path, guideBeginMarker, guideEndMarker)

	case end < begin:
		return "", awberr.Runtimef(
			"%s holds the %s and %s markers in the wrong order; fix it by hand",
			path, guideBeginMarker, guideEndMarker)
	}

	// Replace everything from the first marker through the end of the closing
	// marker's line.
	tail := end + len(guideEndMarker)
	if tail < len(existing) && existing[tail] == '\n' {
		tail++
	}
	return existing[:begin] + block + existing[tail:], nil
}
