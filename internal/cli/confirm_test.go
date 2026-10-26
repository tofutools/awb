package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only a complete affirmative line supplies consent. Empty input, EOF and
// unrecognised replies all leave the deletion unconfirmed.
func TestDeletionPrompt(t *testing.T) {
	for _, reply := range []string{"y\n", "yes\n", " YES \r\n", "n\n", "\n", "sure\n", "", "yes"} {
		t.Run(strings.ReplaceAll(reply, "\n", "newline"), func(t *testing.T) {
			var out bytes.Buffer
			err := deletionPrompt(strings.NewReader(reply), &out, `issue "awb-123"`)
			if reply == "y\n" || reply == "yes\n" || reply == " YES \r\n" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "cancelled")
			}
			assert.Contains(t, out.String(), `Delete issue "awb-123"?`)
			assert.Contains(t, out.String(), "[y/N]")
		})
	}
}

// A supplied answer in a pipe must never substitute for terminal consent;
// --force must bypass both reading and prompting.
func TestDeletionConfirmationWithoutTerminal(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("yes\n")
	e := &env{stdin: in, stderr: &out}
	require.EqualError(t, e.confirmDeletion(false, "awb delete", "issue"),
		"awb delete needs --force: it is not recoverable")
	assert.Equal(t, 4, in.Len())
	assert.Empty(t, out.String())
	require.NoError(t, e.confirmDeletion(true, "awb delete", "issue"))
	assert.Equal(t, 4, in.Len())
	assert.Empty(t, out.String())
}

type failedConfirmationIO struct{}

func (failedConfirmationIO) Read([]byte) (int, error)  { return 0, errors.New("broken input") }
func (failedConfirmationIO) Write([]byte) (int, error) { return 0, errors.New("broken output") }

func TestDeletionPromptIOFailures(t *testing.T) {
	require.ErrorContains(t, deletionPrompt(strings.NewReader("yes\n"), failedConfirmationIO{}, "issue"), "write confirmation")
	require.ErrorContains(t, deletionPrompt(failedConfirmationIO{}, io.Discard, "issue"), "read confirmation")
}
