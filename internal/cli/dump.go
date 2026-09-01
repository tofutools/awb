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
	Force             bool   `long:"force" optional:"true" help:"replace existing outputs after the new dump completes"`
	IncludeUsers      bool   `long:"include-users" optional:"true" help:"include users and credentials (not yet implemented)"`
}

func newDumpCommand(e *env) *cobra.Command {
	return boa.CmdT[dumpParams]{
		Use:   "dump",
		Short: "Download a data source into a new local database",
		Long: "Download every workspace, issue and attachment visible to the caller,\n" +
			"following every paginated listing, and create a local SQLite database and\n" +
			"attachment directory. IDs, timestamps and\n" +
			"stored issue state are preserved, so the result can be served by this version\n" +
			"of awb for local testing. Against a server, dump uses only the existing read\n" +
			"API and requires no server upgrade.\n\n" +
			"Both output paths must be absent unless --force is given. A forced dump\n" +
			"keeps the existing outputs until the replacement has downloaded successfully.\n" +
			"A failed dump removes only the new outputs it created.\n" +
			"Users, credentials and workspace memberships are not included; the resulting\n" +
			"server is therefore unauthenticated.",
		ParamEnrich: boaParams,
		RunFuncE: func(p *dumpParams, cmd *cobra.Command, _ []string) error {
			if p.IncludeUsers {
				return awberr.Usagef("--include-users is not implemented")
			}
			cfg, err := e.config()
			if err != nil {
				return err
			}
			if p.Force && !cfg.Remote() {
				conflict, err := localSourceOutputConflict(cfg.DB, cfg.Attachments,
					p.OutputDB, p.OutputAttachments)
				if err != nil {
					return err
				}
				if conflict {
					return awberr.Usagef(
						"--force outputs must not overlap the local database or attachment directory being dumped")
				}
			}
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			return dump(cmd, be, p.OutputDB, p.OutputAttachments, p.Force)
		},
	}.ToCobra()
}

func dump(cmd *cobra.Command, source backend.Backend, outputDB, outputAttachments string,
	force bool) error {
	overlap, overlapErr := pathsOverlap(outputDB, outputAttachments)
	if overlapErr != nil {
		return overlapErr
	}
	if overlap {
		return awberr.Usagef("--output-db and --output-attachments must not contain one another")
	}
	if force {
		return overwriteDump(cmd, source, outputDB, outputAttachments)
	}
	return createDump(cmd, source, outputDB, outputAttachments)
}

// overwriteDump builds the complete replacement beside each destination before
// moving either existing output. A failed download therefore leaves the last
// usable dump untouched. publishDump moves the old pair aside while it swaps in
// the new pair, so a publication failure can put both old outputs back.
func overwriteDump(cmd *cobra.Command, source backend.Backend, outputDB,
	outputAttachments string) error {
	if err := validateOverwriteTarget(outputDB, false); err != nil {
		return err
	}
	if err := validateOverwriteTarget(outputAttachments, true); err != nil {
		return err
	}
	if err := ensureDatabaseNotActive(outputDB); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputDB), 0o755); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create directory for %s", outputDB)
	}
	if err := os.MkdirAll(filepath.Dir(outputAttachments), 0o755); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create directory for %s", outputAttachments)
	}

	dbStageDir, err := os.MkdirTemp(filepath.Dir(outputDB), ".awb-dump-db-*")
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create staging directory for %s", outputDB)
	}
	defer os.RemoveAll(dbStageDir) //nolint:errcheck // an unreachable staging directory is harmless
	attachmentsStageDir, err := os.MkdirTemp(
		filepath.Dir(outputAttachments), ".awb-dump-attachments-*")
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err,
			"create staging directory for %s", outputAttachments)
	}
	defer os.RemoveAll(attachmentsStageDir) //nolint:errcheck // as above

	stagedDB := filepath.Join(dbStageDir, "awb.db")
	stagedAttachments := filepath.Join(attachmentsStageDir, "attachments")
	if err := createDump(cmd, source, stagedDB, stagedAttachments); err != nil {
		return err
	}
	return publishDump(stagedDB, stagedAttachments, outputDB, outputAttachments)
}

func createDump(cmd *cobra.Command, source backend.Backend, outputDB,
	outputAttachments string) (err error) {
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
	activity, err := dumpActivity(cmd, source, issues)
	if err != nil {
		return err
	}
	projectActivity, err := dumpProjectActivity(cmd, source, projects)
	if err != nil {
		return err
	}

	if err := db.RestoreSnapshot(cmd.Context(), storage.Snapshot{
		Projects: projects, Issues: issues, Attachments: attachments, Activity: activity,
		ProjectActivity: projectActivity,
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

func dumpProjectActivity(cmd *cobra.Command, source backend.Backend,
	projects []domain.Project) ([]domain.ProjectActivity, error) {
	entries := []domain.ProjectActivity{}
	for _, project := range projects {
		total := -1
		fetched := 0
		seen := map[int64]struct{}{}
		for total < 0 || fetched < total {
			limit, offset := dumpPageSize, fetched
			page, err := source.ListProjectActivity(cmd.Context(), project.Key, &limit, &offset)
			if err != nil {
				return nil, err
			}
			if total >= 0 && page.Total != total {
				return nil, awberr.Runtimef("lifecycle of workspace %s changed while the dump was being read", project.Key)
			}
			total = page.Total
			for _, entry := range page.Activity {
				if _, duplicate := seen[entry.ID]; duplicate {
					return nil, awberr.Runtimef("workspace activity %d appeared in more than one dump page", entry.ID)
				}
				seen[entry.ID] = struct{}{}
				entries = append(entries, entry)
			}
			fetched += len(page.Activity)
			if len(page.Activity) == 0 && fetched < total {
				return nil, awberr.Runtimef("workspace activity pagination ended early for %s", project.Key)
			}
		}
	}
	return entries, nil
}

func dumpActivity(cmd *cobra.Command, source backend.Backend, issues []domain.Issue) (
	[]domain.Activity, error) {
	activity := []domain.Activity{}
	for i := range issues {
		issue := &issues[i]
		total := -1
		fetched := 0
		seen := make(map[int64]struct{})
		for total < 0 || fetched < total {
			limit, offset := dumpPageSize, fetched
			page, err := source.ListActivity(cmd.Context(), issue.ID, "", &limit, &offset)
			if err != nil {
				return nil, err
			}
			if total >= 0 && page.Total != total {
				return nil, awberr.Runtimef("activity of %s changed while the dump was being read", issue.ID)
			}
			total = page.Total
			if len(page.Activity) == 0 && fetched < total {
				return nil, awberr.Runtimef("activity pagination for %s ended after %d of %d entries",
					issue.ID, fetched, total)
			}
			fetched += len(page.Activity)
			for _, entry := range page.Activity {
				if _, duplicate := seen[entry.ID]; duplicate {
					return nil, awberr.Runtimef("activity %d of %s appeared in more than one dump page",
						entry.ID, issue.ID)
				}
				seen[entry.ID] = struct{}{}
				activity = append(activity, entry)
			}
		}
	}
	return activity, nil
}

type displacedOutput struct {
	original string
	backup   string
	dir      string
}

func displaceOutput(path string) (*displacedOutput, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "inspect output %s", path)
	}

	backupDir, err := os.MkdirTemp(filepath.Dir(path), "."+filepath.Base(path)+".awb-old-*")
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "prepare replacement of %s", path)
	}
	backup := filepath.Join(backupDir, "output")
	if err := os.Rename(path, backup); err != nil {
		_ = os.Remove(backupDir)
		return nil, awberr.Wrap(awberr.Runtime, err, "move existing output %s aside", path)
	}
	return &displacedOutput{original: path, backup: backup, dir: backupDir}, nil
}

func (d *displacedOutput) restore() error {
	if d == nil {
		return nil
	}
	if err := os.RemoveAll(d.original); err != nil {
		return err
	}
	if err := os.Rename(d.backup, d.original); err != nil {
		return err
	}
	return os.Remove(d.dir)
}

func (d *displacedOutput) discard() {
	if d != nil {
		_ = os.RemoveAll(d.dir)
	}
}

func publishDump(stagedDB, stagedAttachments, outputDB, outputAttachments string) error {
	// The download may have taken a long time. Repeat every destination check at
	// the publication boundary, most importantly the SQLite sidecars: a local
	// server may have opened the old dump while staging was in progress.
	if err := validateOverwriteTarget(outputDB, false); err != nil {
		return err
	}
	if err := validateOverwriteTarget(outputAttachments, true); err != nil {
		return err
	}
	if err := ensureDatabaseNotActive(outputDB); err != nil {
		return err
	}

	oldDB, err := displaceOutput(outputDB)
	if err != nil {
		return err
	}
	oldAttachments, err := displaceOutput(outputAttachments)
	if err != nil {
		return publishRollback(err, oldDB)
	}

	if err := os.Rename(stagedDB, outputDB); err != nil {
		return publishRollback(
			awberr.Wrap(awberr.Runtime, err, "publish dump database %s", outputDB),
			oldAttachments, oldDB)
	}
	if err := os.Rename(stagedAttachments, outputAttachments); err != nil {
		publishErr := awberr.Wrap(awberr.Runtime, err,
			"publish dump attachment directory %s", outputAttachments)
		if removeErr := os.Remove(outputDB); removeErr != nil {
			publishErr = awberr.Wrap(awberr.Runtime, errors.Join(publishErr, removeErr),
				"roll back dump publication")
		}
		return publishRollback(publishErr, oldAttachments, oldDB)
	}

	oldAttachments.discard()
	oldDB.discard()
	return nil
}

func publishRollback(cause error, outputs ...*displacedOutput) error {
	errs := []error{cause}
	for _, output := range outputs {
		if err := output.restore(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 1 {
		return cause
	}
	return awberr.Wrap(awberr.Runtime, errors.Join(errs...), "roll back dump publication")
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

func validateOverwriteTarget(path string, wantDirectory bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return awberr.Wrap(awberr.Runtime, err, "inspect output %s", path)
	}
	if wantDirectory && !info.IsDir() {
		return awberr.Usagef("cannot overwrite %s: attachment output is not a directory", path)
	}
	if !wantDirectory && !info.Mode().IsRegular() {
		return awberr.Usagef("cannot overwrite %s: database output is not a regular file", path)
	}
	return nil
}

func ensureDatabaseNotActive(path string) error {
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Lstat(sidecar); err == nil {
			return awberr.Runtimef(
				"cannot overwrite %s while %s exists: stop the local server first", path, sidecar)
		} else if !errors.Is(err, os.ErrNotExist) {
			return awberr.Wrap(awberr.Runtime, err, "inspect %s", sidecar)
		}
	}
	return nil
}

func sameExistingFile(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

func localSourceOutputConflict(sourceDB, sourceAttachments, outputDB,
	outputAttachments string) (bool, error) {
	for _, source := range []string{sourceDB, sourceAttachments} {
		for _, output := range []string{outputDB, outputAttachments} {
			overlap, err := pathsOverlap(source, output)
			if err != nil {
				return false, err
			}
			if overlap || sameExistingFile(source, output) {
				return true, nil
			}
		}
	}
	return false, nil
}

func dumpProjects(cmd *cobra.Command, source backend.Backend) ([]domain.Project, error) {
	projects := []domain.Project{}
	seen := make(map[string]struct{})
	total := -1
	for offset := 0; total < 0 || offset < total; offset = len(projects) {
		limit, pageOffset := dumpPageSize, offset
		page, err := source.ListProjectsByState(cmd.Context(), "", domain.ProjectsAll,
			domain.DefaultProjectSort, &limit, &pageOffset)
		if err != nil {
			return nil, err
		}
		if total >= 0 && page.Total != total {
			return nil, awberr.Runtimef("workspaces changed while the dump was being read")
		}
		total = page.Total
		if len(page.Projects) == 0 && offset < total {
			return nil, awberr.Runtimef("workspace pagination ended after %d of %d workspaces", offset, total)
		}
		for _, project := range page.Projects {
			if _, duplicate := seen[project.Key]; duplicate {
				return nil, awberr.Runtimef("workspace %s appeared in more than one dump page", project.Key)
			}
			seen[project.Key] = struct{}{}
			projects = append(projects, project)
		}
	}
	if len(projects) != total {
		return nil, awberr.Runtimef("workspace pagination returned %d of %d workspaces", len(projects), total)
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
			IncludeClosed: true, IncludeArchived: true,
			Limit:  &limit,
			Offset: &pageOffset,
			Sort:   domain.Sort{Key: domain.SortID},
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
	absA, err := physicalPath(a)
	if err != nil {
		return false, awberr.Wrap(awberr.Runtime, err, "resolve output path %s", a)
	}
	absB, err := physicalPath(b)
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

// physicalPath resolves every symlink in the existing part of path. The output
// itself need not exist yet, so components are peeled off until EvalSymlinks
// reaches an existing ancestor and are then joined back onto its physical path.
func physicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := abs
	tail := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		tail = append(tail, filepath.Base(current))
		current = parent
	}
}
