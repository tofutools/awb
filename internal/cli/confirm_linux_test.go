package cli

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Use real terminal descriptors to exercise detection and each command's prompt
// target. Declining must stop before opening a backend or performing a mutation.
func TestDeleteCommandsOnTerminal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func(*env) *cobra.Command
		args    []string
		target  string
	}{
		{"issue", newDeleteCommand, []string{"awb-123"}, `issue "awb-123"`},
		{"workspace", newWorkspaceDeleteCommand, []string{"example", "--cascade"}, `workspace "example" (cascade: true)`},
		{"user", newUserDeleteCommand, []string{"alice"}, `user "alice"`},
		{"attachment", newAttachDeleteCommand, []string{"awb-123", "file.txt"}, `attachment "file.txt" on issue "awb-123"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := confirmationTerminal(t)
			var stdout bytes.Buffer
			e := &env{stdin: slave, stderr: slave, stdout: &errWriter{w: &stdout}}
			cmd := tc.command(e)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs(tc.args)
			result := make(chan error, 1)
			go func() { result <- cmd.Execute() }()
			prompt := readConfirmationPrompt(t, master)
			assert.Contains(t, prompt, tc.target)
			_, err := master.WriteString("n\n")
			require.NoError(t, err)
			select {
			case err := <-result:
				require.ErrorContains(t, err, "deletion cancelled")
			case <-time.After(5 * time.Second):
				t.Fatal("command did not cancel")
			}
			assert.Empty(t, stdout.String())
		})
	}
}

func TestTerminalDeletionAcceptsConfirmation(t *testing.T) {
	master, slave := confirmationTerminal(t)
	e := &env{stdin: slave, stderr: slave}
	result := make(chan error, 1)
	go func() { result <- e.confirmDeletion(false, "awb delete", "issue") }()
	readConfirmationPrompt(t, master)
	_, err := master.WriteString("yes\n")
	require.NoError(t, err)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation did not finish")
	}
}

func confirmationTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = master.Close() })
	require.NoError(t, unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0))
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	require.NoError(t, err)
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func readConfirmationPrompt(t *testing.T, master *os.File) string {
	t.Helper()
	var prompt bytes.Buffer
	for !bytes.Contains(prompt.Bytes(), []byte("[y/N]")) {
		ready, err := unix.Poll([]unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}, 5000)
		require.NoError(t, err)
		require.Positive(t, ready, "confirmation prompt did not arrive")
		var buf [512]byte
		n, err := master.Read(buf[:])
		require.NoError(t, err)
		_, _ = prompt.Write(buf[:n])
	}
	return prompt.String()
}
