package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

const dumpPageSize = 100

type dumpParams struct {
	OutputDB          string `long:"output-db" required:"true" help:"new SQLite database to create"`
	OutputAttachments string `long:"output-attachments" required:"true" help:"new attachment content directory to create"`
	IncludeUsers      bool   `long:"include-users" optional:"true" help:"include users and credentials (not yet implemented)"`
}

func newDumpCommand(e *env) *cobra.Command {
	return boa.CmdT[dumpParams]{
		Use:   "dump",
		Short: "Download a data source into a new local database",
		Long: "Download every project, issue and attachment visible to the caller,\n" +
			"following every paginated listing, and create a local SQLite database and\n" +
			"attachment directory. IDs, timestamps and\n" +
			"stored issue state are preserved, so the result can be served by this version\n" +
			"of awb for local testing. Against a server, dump uses only the existing read\n" +
			"API and requires no server upgrade.\n\n" +
			"Both output paths must be absent. A failed dump removes the outputs it created.\n" +
			"Users, credentials and project memberships are not included; the resulting\n" +
			"server is therefore unauthenticated.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *dumpParams, cmd *cobra.Command, _ []string) error {
			if p.IncludeUsers {
				return awberr.Usagef("--include-users is not implemented")
			}
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			return dump(cmd, be, p.OutputDB, p.OutputAttachments)
		},
	}.ToCobra()
}

func dump(cmd *cobra.Command, source backend.Backend, outputDB, outputAttachments string) (err error) {
	overlap, overlapErr := pathsOverlap(outputDB, outputAttachments)
	if overlapErr != nil {
		return overlapErr
	}
	if overlap {
		return awberr.Usagef("--output-db and --output-attachments must not contain one another")
	}
	if err := requireAbsent(outputDB); err != nil {
		return err
	}
	if err := requireAbsent(outputAttachments); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outputDB), 0o755); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create directory for %s", outputDB)
	}
	if err := os.MkdirAll(filepath.Dir(outputAttachments), 0o755); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create directory for %s", outputAttachments)
	}
	// Reserve both absent destinations before doing network work. O_EXCL and
	// Mkdir make the refusal race-safe: another process can never have its file
	// or directory adopted or overwritten by this dump.
	reserved, err := os.OpenFile(outputDB, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create %s", outputDB)
	}
	if err := reserved.Close(); err != nil {
		_ = os.Remove(outputDB)
		return awberr.Wrap(awberr.Runtime, err, "create %s", outputDB)
	}
	if err := os.Mkdir(outputAttachments, 0o755); err != nil {
		_ = os.Remove(outputDB)
		return awberr.Wrap(awberr.Runtime, err, "create attachment directory %s", outputAttachments)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(outputDB)
			_ = os.Remove(outputDB + "-wal")
			_ = os.Remove(outputDB + "-shm")
			_ = os.RemoveAll(outputAttachments)
		}
	}()

	db, err := storage.Init(cmd.Context(), outputDB)
	if err != nil {
		return err
	}
	dbOpen := true
	defer func() {
		if dbOpen {
			_ = db.Close()
		}
	}()

	projects, err := dumpProjects(cmd, source)
	if err != nil {
		return err
	}
	issues, err := dumpIssues(cmd, source)
	if err != nil {
		return err
	}
	attachments, err := dumpAttachments(cmd, source, issues, storage.NewBlobs(outputAttachments))
	if err != nil {
		return err
	}

	if err := db.RestoreSnapshot(cmd.Context(), storage.Snapshot{
		Projects: projects, Issues: issues, Attachments: attachments,
	}); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	dbOpen = false
	complete = true
	return nil
}

func requireAbsent(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return awberr.Usagef("output already exists: %s", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return awberr.Wrap(awberr.Runtime, err, "inspect output %s", path)
	}
	return nil
}

func dumpProjects(cmd *cobra.Command, source backend.Backend) ([]domain.Project, error) {
	projects := []domain.Project{}
	seen := make(map[string]struct{})
	total := -1
	for offset := 0; total < 0 || offset < total; offset = len(projects) {
		limit, pageOffset := dumpPageSize, offset
		page, err := source.ListProjects(cmd.Context(), &limit, &pageOffset)
		if err != nil {
			return nil, err
		}
		if total >= 0 && page.Total != total {
			return nil, awberr.Runtimef("projects changed while the dump was being read")
		}
		total = page.Total
		if len(page.Projects) == 0 && offset < total {
			return nil, awberr.Runtimef("project pagination ended after %d of %d projects", offset, total)
		}
		for _, project := range page.Projects {
			if _, duplicate := seen[project.Key]; duplicate {
				return nil, awberr.Runtimef("project %s appeared in more than one dump page", project.Key)
			}
			seen[project.Key] = struct{}{}
			projects = append(projects, project)
		}
	}
	if len(projects) != total {
		return nil, awberr.Runtimef("project pagination returned %d of %d projects", len(projects), total)
	}
	return projects, nil
}

func dumpIssues(cmd *cobra.Command, source backend.Backend) ([]domain.Issue, error) {
	issues := []domain.Issue{}
	seen := make(map[string]struct{})
	total := -1
	for offset := 0; total < 0 || offset < total; offset = len(issues) {
		limit, pageOffset := dumpPageSize, offset
		page, err := source.ListIssues(cmd.Context(), &domain.Filter{
			IncludeClosed: true,
			Limit:         &limit,
			Offset:        &pageOffset,
			Sort:          domain.Sort{Key: domain.SortID},
		})
		if err != nil {
			return nil, err
		}
		if total >= 0 && page.Total != total {
			return nil, awberr.Runtimef("issues changed while the dump was being read")
		}
		total = page.Total
		if len(page.Issues) == 0 && offset < total {
			return nil, awberr.Runtimef("issue pagination ended after %d of %d issues", offset, total)
		}
		for _, issue := range page.Issues {
			if _, duplicate := seen[issue.ID]; duplicate {
				return nil, awberr.Runtimef("issue %s appeared in more than one dump page", issue.ID)
			}
			seen[issue.ID] = struct{}{}
			issues = append(issues, issue)
		}
	}
	if len(issues) != total {
		return nil, awberr.Runtimef("issue pagination returned %d of %d issues", len(issues), total)
	}
	return issues, nil
}

func dumpAttachments(cmd *cobra.Command, source backend.Backend, issues []domain.Issue,
	blobs *storage.Blobs) ([]domain.Attachment, error) {
	attachments := []domain.Attachment{}
	downloaded := make(map[string]int64)
	for i := range issues {
		issue := &issues[i]
		total := -1
		fetched := 0
		seen := make(map[string]struct{})
		for total < 0 || fetched < total {
			offset := fetched
			limit, pageOffset := dumpPageSize, offset
			page, err := source.ListAttachments(cmd.Context(), issue.ID, &limit, &pageOffset)
			if err != nil {
				return nil, err
			}
			if total >= 0 && page.Total != total {
				return nil, awberr.Runtimef("attachments of %s changed while the dump was being read", issue.ID)
			}
			total = page.Total
			if len(page.Attachments) == 0 && offset < total {
				return nil, awberr.Runtimef("attachment pagination for %s ended after %d of %d attachments",
					issue.ID, offset, total)
			}
			fetched += len(page.Attachments)
			for j := range page.Attachments {
				a := page.Attachments[j]
				if _, duplicate := seen[a.Name]; duplicate {
					return nil, awberr.Runtimef("attachment %q of %s appeared in more than one dump page",
						a.Name, issue.ID)
				}
				seen[a.Name] = struct{}{}
				attachments = append(attachments, a)
				if size, ok := downloaded[a.Sha256]; ok {
					if size != a.Size {
						return nil, awberr.Runtimef("attachments with digest %s disagree on size", a.Sha256)
					}
					continue
				}
				metadata, content, err := source.OpenAttachment(cmd.Context(), a.Issue, a.Name)
				if err != nil {
					return nil, err
				}
				staged, stageErr := blobs.Stage(content, domain.MaxAttachmentBytes)
				closeErr := content.Close()
				if stageErr != nil {
					return nil, stageErr
				}
				if closeErr != nil {
					blobs.Discard(staged)
					return nil, awberr.Wrap(awberr.Runtime, closeErr, "download attachment %q of %s", a.Name, a.Issue)
				}
				if *metadata != a || staged.Sha256 != a.Sha256 || staged.Size != a.Size {
					blobs.Discard(staged)
					return nil, awberr.Runtimef("downloaded attachment %q of %s does not match its metadata",
						a.Name, a.Issue)
				}
				if err := blobs.Place(staged); err != nil {
					blobs.Discard(staged)
					return nil, err
				}
				downloaded[a.Sha256] = a.Size
			}
		}
		if fetched != total {
			return nil, awberr.Runtimef("attachment pagination for %s returned %d of %d attachments",
				issue.ID, fetched, total)
		}
	}
	return attachments, nil
}

func pathsOverlap(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "resolve output path %s", a)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "resolve output path %s", b)
	}
	contains := func(parent, child string) (bool, error) {
		rel, err := filepath.Rel(parent, child)
		if err != nil {
			return false, err
		}
		return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
	}
	aContainsB, err := contains(absA, absB)
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "compare output paths")
	}
	bContainsA, err := contains(absB, absA)
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "compare output paths")
	}
	return aContainsB || bContainsA, nil
}
