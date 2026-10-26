package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/tofutools/awb/internal/awberr"
)

// Deletion requires explicit consent: --force or an affirmative terminal reply.
// Prompt on stderr so stdout remains usable for redirected or machine output.
// A pipe on either input or the prompt stream preserves the noninteractive error.
func (e *env) confirmDeletion(force bool, command, target string) error {
	if force {
		return nil
	}
	if !isTerminal(e.stdin) || !isTerminalWriter(e.stderr) {
		return awberr.Usagef("%s needs --force: it is not recoverable", command)
	}
	return deletionPrompt(e.stdin, e.stderr, target)
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(term.File)
	return ok && term.IsTerminal(f.Fd())
}

func deletionPrompt(in io.Reader, out io.Writer, target string) error {
	if _, err := fmt.Fprintf(out, "Delete %s? This is not recoverable. [y/N] ", target); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "write confirmation")
	}
	reply, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return awberr.Wrap(awberr.Runtime, err, "read confirmation")
	}
	// EOF, including a partial affirmative reply, never confirms a deletion.
	if err == nil {
		switch strings.ToLower(strings.TrimSpace(reply)) {
		case "y", "yes":
			return nil
		}
	}
	return awberr.Usagef("deletion cancelled")
}
