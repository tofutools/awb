package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
)

func newAttachCommand(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach files to issues",
		Long: "Attach arbitrary files to an issue.\n\n" +
			"The content is stored outside the database, as one file per distinct\n" +
			"content in the attachments directory — \"attachments\" beside the database\n" +
			"unless --attachments, AWB_ATTACHMENTS or the configuration file says\n" +
			"otherwise. Only the metadata is in the database: name, content type, size\n" +
			"and the SHA-256 of the content.\n\n" +
			"An attachment is immutable. There is no command that changes one: attach\n" +
			"the file again and remove the old one.",
	}
	cmd.AddCommand(
		newAttachAddCommand(e),
		newAttachListCommand(e),
		newAttachShowCommand(e),
		newAttachGetCommand(e),
		newAttachRemoveCommand(e),
	)
	return grouping(cmd)
}

func newAttachAddCommand(e *env) *cobra.Command {
	var (
		name        string
		contentType string
	)

	cmd := &cobra.Command{
		Use:   "add <id> <file>",
		Short: "Attach a file to an issue",
		Long: "Attach a file, which is read and stored as it is.\n\n" +
			"The name it is shown under is the file's own base name unless --name says\n" +
			"otherwise. \"-\" reads the content from stdin, and then --name is required,\n" +
			"stdin having no name of its own.\n\n" +
			"What the file is is sniffed from its first bytes unless --content-type says\n" +
			"otherwise. It is sniffed from the content rather than from the extension,\n" +
			"so the same file is typed the same way on every machine.\n\n" +
			"Attaching the same file twice makes two attachments, each with its own id;\n" +
			"they share one stored copy of the bytes.",
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			content, defaultName, err := openContent(e, args[1])
			if err != nil {
				return err
			}
			defer content.Close() //nolint:errcheck // the input is only read

			if !cmd.Flags().Changed("name") {
				name = defaultName
			}
			if name == "" {
				return awberr.Usagef("reading the content from stdin needs --name")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			attachment, err := be.AddAttachment(cmd.Context(), args[0], backend.AttachmentCreate{
				Name:        name,
				ContentType: contentType,
				Content:     content,
			})
			if err != nil {
				return err
			}

			if e.json {
				return e.writeJSON(attachment)
			}
			// Like awb create, this prints the new id: minting it is the point,
			// since nothing else names the attachment afterwards.
			_, err = io.WriteString(e.stdout, attachment.ID+"\n")
			return err
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "show it under this name instead of the file's own")
	cmd.Flags().StringVar(&contentType, "content-type", "",
		"what the file is; sniffed from its first bytes when omitted")
	return cmd
}

// openContent opens what attach add was pointed at, and reports the name the
// attachment takes when --name was not given. Stdin has no name, so it reports
// none and the command refuses without one.
func openContent(e *env, path string) (io.ReadCloser, string, error) {
	if path == "-" {
		return io.NopCloser(e.stdin), "", nil
	}
	file, err := os.Open(path) //nolint:gosec // the path is what the caller asked to attach
	if err != nil {
		return nil, "", awberr.Wrap(awberr.Runtime, err, "read %s", path)
	}
	return file, filepath.Base(path), nil
}

func newAttachListCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "ls <id>",
		Short: "List the files attached to an issue",
		Long: "List an issue's attachments, oldest first.\n\n" +
			"Under --compact each line is five fields: the id, the size in bytes and\n" +
			"the content's SHA-256, none of which can hold a space, followed by the\n" +
			"content type and the name as JSON strings.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListAttachments(cmd.Context(), args[0], nil, nil)
			if err != nil {
				return err
			}
			return e.printAttachments(page.Attachments)
		},
	}
}

func newAttachShowCommand(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "show <attachment-id>",
		Short: "Print one attachment's metadata",
		Long: "Print what is recorded about an attachment. Its content is what\n" +
			"awb attach get writes.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			attachment, err := be.GetAttachment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return e.printAttachment(attachment)
		},
	}
}

func newAttachGetCommand(e *env) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "get <attachment-id>",
		Short: "Write an attachment's content to a file or to stdout",
		Long: "Write the bytes exactly as they were uploaded.\n\n" +
			"They go to stdout unless --output names a file, so the content can be\n" +
			"piped. This is the one command whose output is not text and not a mode:\n" +
			"--json and --compact do not apply to it, awb attach show being what\n" +
			"prints the metadata.\n\n" +
			"--output never writes to a name the attachment chose: what a file is\n" +
			"called on this machine is the caller's decision, not the uploader's.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			_, content, err := be.OpenAttachment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer content.Close() //nolint:errcheck // the content is only read

			if output == "" || output == "-" {
				_, err = io.Copy(e.stdout, content)
				return err
			}
			return writeContentFile(output, content)
		},
	}

	cmd.Flags().StringVar(&output, "output", "",
		"write to this file instead of stdout")
	return cmd
}

func writeContentFile(path string, content io.Reader) error {
	file, err := os.Create(path) //nolint:gosec // the path is what the caller asked to write
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "write %s", path)
	}
	if _, err := io.Copy(file, content); err != nil {
		_ = file.Close()
		return awberr.Wrap(awberr.Runtime, err, "write %s", path)
	}
	if err := file.Close(); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "write %s", path)
	}
	return nil
}

func newAttachRemoveCommand(e *env) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "rm <attachment-id> --force",
		Short: "Remove an attachment",
		Long: "Remove an attachment. This is not recoverable.\n\n" +
			"The stored content goes with it unless another attachment holds the same\n" +
			"bytes, in which case that copy stays.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A missing --force depends on the arguments alone and not on anything
			// the database holds, so it is a usage error.
			if !force {
				return awberr.Usagef("awb attach rm needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteAttachment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(deleted)
			}
			return e.summarise("Removed attachment %s (%s).\n", deleted.ID, deleted.Name)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "confirm the removal")
	return cmd
}
