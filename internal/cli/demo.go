package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// The demo project. It is the only project awb demo creates or deletes, but
// deleting it removes the relations its issues are on either end of, which can
// unblock work in another project.
//
// It creates no user, deliberately, although the data set otherwise shows
// everything there is to show. A database holding no user is a server without
// authentication, so seeding one would make "awb demo" quietly turn a server's
// authentication on — with a password the operator never chose and would then
// have to guess. What users look like is shown by "awb user add" instead,
// which is a decision somebody made.
const (
	demoProjectKey  = "demo"
	demoProjectName = "DEMO"
	demoProjectDesc = "A sample project for trying `awb` out.\n\n" +
		"`awb demo --force` replaces it **wholesale**, so anything written here is\n" +
		"scratch data. See the [worked example](https://example.com/widgets/example) for\n" +
		"what to try first.\n"
)

// demoIssue is one issue of the demo data set.
//
// The set is a table rather than a sequence of calls so that it can be read on
// one screen, and so that a new feature is exercised by editing a row. Every
// part of the vocabulary — each type, each priority, each status, each relation
// type — must appear in it somewhere; a test pins that down.
type demoIssue struct {
	// key names this issue within the set, so the relations below can refer to
	// it before it has an ID. It is not stored anywhere, and an issue may only
	// refer to one that comes before it.
	key string

	title       string
	description string
	issueType   domain.Type
	priority    int
	labels      []string
	// assignee makes creation an atomic create-and-claim, so the issue lands in
	// in_progress rather than open.
	assignee string
	// closeReason, when set, closes the issue once it exists. Closing keeps the
	// assignee, which is what records who did the work, so a closed issue with an
	// assignee is created with one and then closed.
	closeReason string

	hasParent      string
	blockedBy      []string
	discoveredFrom []string
	related        []string

	// attachments are the files the issue carries. Their content is written out
	// of the table, so the data set stays one thing to read and awb demo still
	// needs nothing on disk.
	attachments []demoAttachment
}

// demoAttachment is one file of the demo data set. Its content type is left to
// the sniffing rule, exactly as awb attach add without --content-type leaves
// it, so the demo exercises that too.
type demoAttachment struct {
	name    string
	content string
}

// demoIssues is the demo data set, in creation order.
var demoIssues = []demoIssue{{
	key:       "release",
	title:     "Ship the 1.0 release of the widget catalogue",
	issueType: domain.TypeEpic,
	priority:  0,
	labels:    []string{"release"},
	description: "Everything that must be true before the catalogue goes out.\n\n" +
		"The scope is fixed; see the [release checklist](https://example.com/widgets/checklist)\n" +
		"for what each item below has to satisfy.\n",
}, {
	key:         "schema",
	title:       "Design the catalogue database schema",
	issueType:   domain.TypeTask,
	priority:    1,
	labels:      []string{"backend"},
	assignee:    "alice",
	closeReason: "Schema reviewed and migrated",
	description: "Tables for widgets, tags and the join between them.\n",
	hasParent:   "release",
}, {
	key:         "catalogue",
	title:       "Browse the widget catalogue",
	issueType:   domain.TypeFeature,
	priority:    1,
	labels:      []string{"catalogue", "frontend"},
	assignee:    "alice",
	description: "A paged list of widgets, newest first, with a detail page for each.\n",
	hasParent:   "release",
	// The blocker is already closed, so this shows a dependency that no longer
	// holds anything up.
	blockedBy: []string{"schema"},
}, {
	key:         "thumbnails",
	title:       "Show thumbnails in the catalogue list",
	issueType:   domain.TypeTask,
	priority:    3,
	labels:      []string{"catalogue", "frontend"},
	assignee:    "carol",
	description: "Serve a 96×96 thumbnail per widget and lay the list out around it.\n",
	// One level below a child of the epic, so the decomposition is two deep.
	hasParent: "catalogue",
}, {
	key:         "index",
	title:       "Build the full text search index",
	issueType:   domain.TypeTask,
	priority:    2,
	labels:      []string{"backend", "search"},
	description: "Index widget names and tags, and keep the index up to date on write.\n",
	hasParent:   "release",
}, {
	key:         "search",
	title:       "Search the catalogue by name and tag",
	issueType:   domain.TypeFeature,
	priority:    2,
	labels:      []string{"catalogue", "frontend", "search"},
	description: "A single search box over the index, with the tag filters beside it.\n",
	hasParent:   "release",
	blockedBy:   []string{"index"},
	related:     []string{"catalogue"},
}, {
	key:         "pagination",
	title:       "Paginate the catalogue beyond fifty widgets",
	issueType:   domain.TypeFeature,
	priority:    3,
	labels:      []string{"catalogue", "frontend"},
	description: "Keyset pagination, so the page does not shift as widgets are added.\n",
	hasParent:   "release",
	// Two blockers, so the blocked listing has something to show in its column.
	blockedBy: []string{"index", "search"},
}, {
	key:         "empty-crash",
	title:       "Catalogue page crashes on an empty result set",
	issueType:   domain.TypeBug,
	priority:    0,
	labels:      []string{"catalogue", "frontend"},
	assignee:    "bob",
	description: "Opening the catalogue with every widget filtered out renders a nil row.\n",
	// Found while working on the feature, which is what discovered-from records.
	discoveredFrom: []string{"catalogue"},
	// A bug is where an attachment earns its place: the evidence is a file
	// rather than something to paste into the description.
	attachments: []demoAttachment{{
		name: "stack-trace.txt",
		content: "panic: runtime error: invalid memory address or nil pointer dereference\n" +
			"\tcatalogue/render.go:118 renderRow(nil)\n" +
			"\tcatalogue/render.go:74  renderList\n",
	}, {
		name: "steps-to-reproduce.md",
		content: "# Steps to reproduce\n\n" +
			"1. Open the catalogue.\n" +
			"2. Filter by a tag no widget carries.\n" +
			"3. The page renders an empty row and the server logs the trace above.\n",
	}},
}, {
	key:       "docs",
	title:     "Write the operator documentation",
	issueType: domain.TypeTask,
	priority:  2,
	labels:    []string{"docs"},
	// The one description written as more than a paragraph, so that the Markdown
	// a terminal and the web UI draw — headings, emphasis, a list, code, links —
	// has somewhere to be seen.
	description: "## What the operator needs\n\n" +
		"How to **install**, configure and *back the service up*, in that order.\n\n" +
		"- Follow the [documentation style guide](https://example.com/widgets/style).\n" +
		"- Keep the [configuration reference](https://example.com/widgets/config)\n" +
		"  generated rather than hand-written.\n" +
		"- Every example runs as written; `awb demo` is the fixture behind them.\n",
	hasParent: "release",
}, {
	key:         "test-runner",
	title:       "Upgrade the test runner to the current major version",
	issueType:   domain.TypeChore,
	priority:    4,
	labels:      []string{"maintenance"},
	description: "The version in use is two majors behind and no longer gets fixes.\n",
	related:     []string{"index"},
}, {
	key:         "flaky-import",
	title:       "Flaky test in the widget import suite",
	issueType:   domain.TypeBug,
	priority:    2,
	labels:      []string{"maintenance", "tests"},
	closeReason: "The suite depended on map iteration order",
	description: "Fails about one run in twenty, always in the same assertion.\n",
	// Found while doing the upgrade, and closed without anybody claiming it.
	discoveredFrom: []string{"test-runner"},
}, {
	key:         "legacy-importer",
	title:       "Remove the legacy widget importer",
	issueType:   domain.TypeChore,
	priority:    3,
	labels:      []string{"backend", "cleanup"},
	description: "Nothing has called it since the new schema landed.\n",
	// Top-level: not everything belongs to the epic.
	discoveredFrom: []string{"schema"},
}}

func newDemoCommand(e *env) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "demo [--force]",
		Short: "Fill the " + demoProjectKey + " project with a data set that exercises every feature",
		Long: "Create the " + demoProjectKey + " project and fill it with a small sample data set: every\n" +
			"issue type, every priority, every status, every relation type, blocked\n" +
			"and ready work, labels, assignees and Markdown links.\n\n" +
			"It is for trying commands out, and for looking at the web UI with\n" +
			"something in it.\n\n" +
			"It refuses while the " + demoProjectKey + " project exists, because it replaces the project\n" +
			"wholesale rather than reconciling it: nothing marks an issue there as one\n" +
			"a previous run wrote, so --force is what says that deleting whatever is\n" +
			"under the key is meant.\n\n" +
			"No other project is created or deleted, but deleting this one drops the\n" +
			"relations its issues were on either end of, which may unblock work\n" +
			"elsewhere.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			project, err := buildDemo(cmd.Context(), be, force)
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(project)
			}
			// demo is one of the exceptions to "mutating commands print nothing on
			// success", for the reason the deleting commands are: a command that
			// replaces a whole project should say what it left behind. The line is
			// not a compatibility surface; a script reads --json.
			return e.summarise("Created project %s with %d issue(s).\n",
				project.Key, len(demoIssues))
		},
	}

	cmd.Flags().BoolVar(&force, "force", false,
		"replace the "+demoProjectKey+" project, deleting everything in it")
	return cmd
}

// buildDemo replaces the demo project with the demo data set and returns the
// project as it stands afterwards.
//
// It is composed entirely of ordinary backend operations — the same ones a
// script would issue — so it works against a server exactly as it works against
// a file and adds no second code path. Each issue is therefore its own
// transaction: a build interrupted half way leaves a partial project, which the
// next run replaces.
func buildDemo(ctx context.Context, be backend.Backend, force bool) (*domain.Project, error) {
	// An existing demo project is deleted outright, issues and relations with it,
	// rather than reconciled: what awb demo promises is that afterwards the
	// project holds exactly this data set and nothing else. Nothing marks an
	// issue here as one a previous run wrote, so that deletes whatever is under
	// the key, and --force is what says it is meant.
	//
	// The refusal depends on what is stored rather than on the arguments alone,
	// which is what makes it a conflict where awb delete's missing --force is a
	// usage error. The check and the delete are two operations, so a project
	// created between them is deleted unasked; that window is one round trip
	// wide and is not worth an interface change to close.
	_, err := be.GetProject(ctx, demoProjectKey)
	switch {
	case err != nil && awberr.KindOf(err) != awberr.NotFound:
		return nil, err
	case err == nil && !force:
		return nil, awberr.Conflictf(
			"project %s already exists; awb demo --force replaces it and deletes everything in it",
			demoProjectKey)
	case err == nil:
		if _, err := be.DeleteProject(ctx, demoProjectKey, true, ""); err != nil {
			return nil, err
		}
	}

	if _, err := be.CreateProject(ctx, backend.ProjectCreate{
		Key:         demoProjectKey,
		Name:        demoProjectName,
		Description: demoProjectDesc,
	}); err != nil {
		return nil, err
	}

	ids := make(map[string]string, len(demoIssues))
	for i := range demoIssues {
		d := &demoIssues[i]

		relations, err := d.relations(ids)
		if err != nil {
			return nil, err
		}
		priority := d.priority
		issue, err := be.CreateIssue(ctx, backend.IssueCreate{
			Project:     demoProjectKey,
			Title:       d.title,
			Description: d.description,
			Type:        d.issueType,
			Priority:    &priority,
			Assignee:    d.assignee,
			Labels:      d.labels,
			Relations:   relations,
		})
		if err != nil {
			return nil, err
		}
		ids[d.key] = issue.ID

		for _, a := range d.attachments {
			if _, err := be.AddAttachment(ctx, issue.ID, backend.AttachmentCreate{
				Name:    a.name,
				Content: strings.NewReader(a.content),
			}); err != nil {
				return nil, err
			}
		}

		if d.closeReason != "" {
			reason := d.closeReason
			if _, err := be.CloseIssue(ctx, issue.ID,
				backend.CloseRequest{Reason: &reason}, ""); err != nil {
				return nil, err
			}
		}
	}

	// Re-read, so the returned project carries the count of the issues just
	// created rather than the zero it was born with.
	return be.GetProject(ctx, demoProjectKey)
}

// relations turns the table's keys into the relations of one issue, in the
// order awb create's flags would produce them.
func (d *demoIssue) relations(ids map[string]string) ([]backend.NewRelation, error) {
	lookup := func(keys []string) ([]string, error) {
		resolved := make([]string, 0, len(keys))
		for _, key := range keys {
			id, ok := ids[key]
			if !ok {
				// A mistake in the table above, not in what the caller asked for.
				return nil, awberr.Runtimef(
					"demo data is out of order: %q refers to %q, which is created after it",
					d.key, key)
			}
			resolved = append(resolved, id)
		}
		return resolved, nil
	}

	var parent string
	if d.hasParent != "" {
		resolved, err := lookup([]string{d.hasParent})
		if err != nil {
			return nil, err
		}
		parent = resolved[0]
	}
	blockedBy, err := lookup(d.blockedBy)
	if err != nil {
		return nil, err
	}
	discoveredFrom, err := lookup(d.discoveredFrom)
	if err != nil {
		return nil, err
	}
	related, err := lookup(d.related)
	if err != nil {
		return nil, err
	}
	return collectRelations(parent, blockedBy, discoveredFrom, related), nil
}
