package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

const descriptionReceiptSuffix = ".awb-receipt.json"

// descriptionReceipt is deliberately small and lives beside one downloaded
// working file. It records the entity version that file was fetched from;
// ordinary show output never refreshes it behind an older working copy.
type descriptionReceipt struct {
	Version int    `json:"version"`
	Backend string `json:"backend"`
	Entity  string `json:"entity"`
	ID      string `json:"id"`
	ETag    string `json:"etag"`
}

type descriptionGetParams struct {
	ID     string `positional:"true" required:"true"`
	Output string `long:"output" required:"true" help:"write the Markdown to this file"`
}

func newDescriptionCommand(e *env) *cobra.Command {
	return group("description", "Fetch an issue description for safe file-based editing",
		"Fetch records the issue version in a receipt beside the output file. A later\n"+
			"awb update --description-file uses that receipt as a conditional-edit\n"+
			"precondition, so it refuses to overwrite a concurrent change.",
		newDescriptionGetCommand(e, "issue"))
}

func newProjectDescriptionCommand(e *env) *cobra.Command {
	return group("description", "Fetch a workspace description for safe file-based editing",
		"Fetch records the workspace version in a receipt beside the output file. A later\n"+
			"awb workspace update --description-file uses that receipt as a conditional-edit\n"+
			"precondition, so it refuses to overwrite a concurrent change.",
		newDescriptionGetCommand(e, "project"))
}

func newDescriptionGetCommand(e *env, entity string) *cobra.Command {
	return boa.CmdT[descriptionGetParams]{
		Use:   "get",
		Short: "Write the description and a version receipt to a file",
		Long: "Write the stored Markdown exactly as received, plus a small JSON receipt at\n" +
			"<output>" + descriptionReceiptSuffix + ". Keep the receipt beside the file: it binds a\n" +
			"later update to this backend, entity and entity version.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *descriptionGetParams, cmd *cobra.Command, _ []string) error {
			if p.Output == "-" {
				return awberr.Usagef("description get needs a file for --output so it can store a receipt")
			}

			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}

			var id, description, updatedAt string
			switch entity {
			case "issue":
				issue, err := be.GetIssue(cmd.Context(), p.ID)
				if err != nil {
					return err
				}
				id, description, updatedAt = issue.ID, issue.Description, issue.UpdatedAt
			case "project":
				project, err := be.GetProject(cmd.Context(), p.ID)
				if err != nil {
					return err
				}
				id, description, updatedAt = project.Key, project.Description, project.UpdatedAt
			default:
				panic("unknown description entity " + entity)
			}

			backendID, err := e.descriptionBackendIdentity()
			if err != nil {
				return err
			}
			receipt := descriptionReceipt{
				Version: 1, Backend: backendID, Entity: entity, ID: id,
				ETag: backend.ETag(updatedAt),
			}
			return writeDescriptionFetch(p.Output, description, receipt)
		},
	}.ToCobra()
}

func writeDescriptionFetch(path, description string, receipt descriptionReceipt) error {
	if err := os.WriteFile(path, []byte(description), 0o644); err != nil { //nolint:gosec // caller chose the output
		return awberr.Wrap(awberr.Runtime, err, "write %s", path)
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "encode description receipt")
	}
	encoded = append(encoded, '\n')
	receiptPath := path + descriptionReceiptSuffix
	if err := os.WriteFile(receiptPath, encoded, 0o600); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "write %s", receiptPath)
	}
	return nil
}

// descriptionBackendIdentity identifies the data source, not the caller. A
// receipt may be used under different valid credentials against the same
// server, while one from another database or server is rejected.
func (e *env) descriptionBackendIdentity() (string, error) {
	cfg, err := e.config()
	if err != nil {
		return "", err
	}
	if cfg.Remote() {
		return cfg.RemoteURL.String(), nil
	}
	abs, err := filepath.Abs(cfg.DB)
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "identify database %s", cfg.DB)
	}
	return filepath.Clean(abs), nil
}

func (d *DescriptionFlags) valueForUpdate(e *env, entity, id string, force bool) (*string, string, error) {
	description, err := d.value(e)
	if err != nil || description == nil {
		return description, "", err
	}
	if force {
		return description, "", nil
	}
	if d.File == nil || *d.File == "-" {
		return nil, "", descriptionFetchRequired(entity, id, d.File)
	}

	receipt, err := readDescriptionReceipt(*d.File + descriptionReceiptSuffix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", descriptionFetchRequired(entity, id, d.File)
		}
		return nil, "", err
	}
	backendID, err := e.descriptionBackendIdentity()
	if err != nil {
		return nil, "", err
	}
	if receipt.Version != 1 {
		return nil, "", invalidDescriptionReceipt(*d.File, "unsupported receipt version")
	}
	if receipt.Backend != backendID {
		return nil, "", invalidDescriptionReceipt(*d.File, "receipt belongs to another backend")
	}
	if receipt.Entity != entity || !receiptMatches(entity, receipt.ID, id) {
		return nil, "", invalidDescriptionReceipt(*d.File,
			fmt.Sprintf("receipt is for %s %s, not %s %s", receipt.Entity, receipt.ID, entity, id))
	}
	if receipt.ETag == "" {
		return nil, "", invalidDescriptionReceipt(*d.File, "receipt has no ETag")
	}
	return description, receipt.ETag, nil
}

func readDescriptionReceipt(path string) (descriptionReceipt, error) {
	data, err := os.ReadFile(path) //nolint:gosec // sidecar of the caller's chosen file
	if err != nil {
		return descriptionReceipt{}, err
	}
	var receipt descriptionReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return descriptionReceipt{}, invalidDescriptionReceipt(strings.TrimSuffix(path, descriptionReceiptSuffix),
			"receipt is not valid JSON")
	}
	return receipt, nil
}

func receiptMatches(entity, canonical, ref string) bool {
	if entity == "project" {
		return canonical == ref
	}
	parsed, err := domain.ParseIssueRef(ref)
	if err != nil {
		return false
	}
	project, hash, ok := domain.SplitID(canonical)
	return ok && (parsed.Project == "" || parsed.Project == project) && strings.HasPrefix(hash, parsed.Hash)
}

func descriptionFetchRequired(entity, id string, file *string) error {
	path := "<file>"
	if file != nil && *file != "-" {
		path = *file
	}
	command := fmt.Sprintf("awb description get %s --output %s", id, path)
	if entity == "project" {
		command = fmt.Sprintf("awb workspace description get %s --output %s", id, path)
	}
	return awberr.Usagef("description must be fetched before it can be updated; run %s, or use --force to replace it without a precondition", command)
}

func invalidDescriptionReceipt(file, reason string) error {
	return awberr.Usagef("cannot use the description receipt for %s: %s; fetch the description again", file, reason)
}

func descriptionPreconditionError(err error, entity, id, file string) error {
	if !errors.Is(err, awberr.ErrPreconditionFailed) {
		return err
	}
	command := fmt.Sprintf("awb description get %s --output %s", id, file)
	if entity == "project" {
		command = fmt.Sprintf("awb workspace description get %s --output %s", id, file)
	}
	return awberr.Wrap(awberr.KindOf(err), err,
		"description receipt is stale; fetch the description again with %s", command)
}
