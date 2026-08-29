package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
)

func newAttachCommand(e *env) *cobra.Command {
	return group("attach", "Attach files to issues",
		"Attach arbitrary files to an issue.\n\n"+
			"The content is stored outside the database, as one file per distinct\n"+
			"content in the attachments directory — \"attachments\" beside the database\n"+
			"unless --attachments, AWB_ATTACHMENTS or the configuration file says\n"+
			"otherwise. Only the metadata is in the database: name, content type, size\n"+
			"and the SHA-256 of the content.\n\n"+
			"An attachment is addressed by its issue and its name, the way a label is,\n"+
			"and holds no id of its own. An issue holds at most one attachment under\n"+
			"any one name.\n\n"+
			"An attachment is immutable. There is no command that changes one: delete\n"+
			"it and attach the file again.",
		newAttachAddCommand(e),
		newAttachListCommand(e),
		newAttachShowCommand(e),
		newAttachGetCommand(e),
		newAttachDeleteCommand(e),
	)
}

type attachAddParams struct {
	ID          string  `positional:"true" required:"true"`
	File        string  `positional:"true" required:"true"`
	Name        *string `long:"name" help:"show it under this name instead of the file's own"`
	ContentType string  `long:"content-type" optional:"true" help:"what the file is; sniffed from its first bytes when omitted"`
}

func newAttachAddCommand(e *env) *cobra.Command {
	return boa.CmdT[attachAddParams]{
		Use:   "add",
		Short: "Attach a file to an issue",
		Long: "Attach a file, which is read and stored as it is.\n\n" +
			"The name it is held under is the file's own base name unless --name says\n" +
			"otherwise, and that name is how it is addressed afterwards. \"-\" reads the\n" +
			"content from stdin, and then --name is required, stdin having no name of\n" +
			"its own.\n\n" +
			"An issue holds at most one attachment under any one name, so attaching a\n" +
			"second file under a name it already holds fails; delete that one first,\n" +
			"or give --name. Two issues may each hold one called the same thing, and if\n" +
			"the bytes are identical they share one stored copy.\n\n" +
			"What the file is is sniffed from its first bytes unless --content-type says\n" +
			"otherwise. It is sniffed from the content rather than from the extension,\n" +
			"so the same file is typed the same way on every machine.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *attachAddParams, cmd *cobra.Command, _ []string) error {
			content, defaultName, err := openContent(e, p.File)
			if err != nil {
				return err
			}
			defer content.Close() //nolint:errcheck // the input is only read

			name := defaultName
			if p.Name != nil {
				name = *p.Name
			}
			if name == "" {
				return awberr.Usagef("reading the content from stdin needs --name")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			attachment, err := be.AddAttachment(cmd.Context(), p.ID, backend.AttachmentCreate{
				Name:        name,
				ContentType: p.ContentType,
				Content:     content,
			})
			if err != nil {
				return err
			}
			// A mutating command that prints nothing on success, like almost all
			// of them: awb create prints an id because minting one is the point,
			// and there is no id here to print. The caller already knows what it
			// attached and under what name, which is the whole of the reference.
			return e.attached(attachment)
		},
	}.ToCobra()
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
	return idCommand("list", "List the files attached to an issue",
		"List an issue's attachments, oldest first.\n\n"+
			"Under --compact each line is five fields: the id, the size in bytes and\n"+
			"the content's SHA-256, none of which can hold a space, followed by the\n"+
			"content type and the name as JSON strings.", func(cmd *cobra.Command, id string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			page, err := be.ListAttachments(cmd.Context(), id, nil, nil)
			if err != nil {
				return err
			}
			return e.printAttachments(page.Attachments)
		})
}

func newAttachShowCommand(e *env) *cobra.Command {
	return idNameCommand("show", "Print one attachment's metadata",
		"Print what is recorded about an attachment. Its content is what\n"+
			"awb attach get writes.", func(cmd *cobra.Command, id, name string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			attachment, err := be.GetAttachment(cmd.Context(), id, name)
			if err != nil {
				return err
			}
			return e.printAttachment(attachment)
		})
}

type attachGetParams struct {
	ID     string `positional:"true" required:"true"`
	Name   string `positional:"true" required:"true"`
	Output string `long:"output" optional:"true" help:"write to this file instead of stdout"`
}

func newAttachGetCommand(e *env) *cobra.Command {
	return boa.CmdT[attachGetParams]{
		Use:   "get",
		Short: "Write an attachment's content to a file or to stdout",
		Long: "Write the bytes exactly as they were uploaded.\n\n" +
			"They go to stdout unless --output names a file, so the content can be\n" +
			"piped. This is the one command whose output is not text and not a mode:\n" +
			"--json and --compact do not apply to it, awb attach show being what\n" +
			"prints the metadata.\n\n" +
			"--output never writes to a name the attachment chose: what a file is\n" +
			"called on this machine is the caller's decision, not the uploader's.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *attachGetParams, cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			_, content, err := be.OpenAttachment(cmd.Context(), p.ID, p.Name)
			if err != nil {
				return err
			}
			defer content.Close() //nolint:errcheck // the content is only read

			if p.Output == "" || p.Output == "-" {
				_, err = io.Copy(e.stdout, content)
				return err
			}
			return writeContentFile(p.Output, content)
		},
	}.ToCobra()
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

type attachDeleteParams struct {
	ID    string `positional:"true" required:"true"`
	Name  string `positional:"true" required:"true"`
	Force bool   `long:"force" optional:"true" help:"confirm the deletion"`
}

func newAttachDeleteCommand(e *env) *cobra.Command {
	return boa.CmdT[attachDeleteParams]{
		Use:   "delete",
		Short: "Delete an attachment",
		Long: "Delete an attachment. This is not recoverable.\n\n" +
			"The stored content goes with it unless another attachment holds the same\n" +
			"bytes, in which case that copy stays.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *attachDeleteParams, cmd *cobra.Command, _ []string) error {
			// A missing --force depends on the arguments alone and not on anything
			// the database holds, so it is a usage error.
			if !p.Force {
				return awberr.Usagef("awb attach delete needs --force: it is not recoverable")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			deleted, err := be.DeleteAttachment(cmd.Context(), p.ID, p.Name)
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(deleted)
			}
			// The one line a deleting command says, in the form the other two
			// deleting commands say it.
			return e.summarise("Deleted attachment %q from %s.\n", deleted.Name, deleted.Issue)
		},
	}.ToCobra()
}
