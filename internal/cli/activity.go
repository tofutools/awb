package cli

import (
	"io"
	"os"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/domain"
)

type commentAddParams struct {
	ID       string  `positional:"true" required:"true"`
	Body     *string `long:"body" optional:"true" help:"Markdown comment text"`
	BodyFile *string `long:"body-file" optional:"true" help:"read the comment from this file, or from stdin with \"-\""`
}

func newCommentCommand(e *env) *cobra.Command {
	return group("comment", "Add and list issue comments",
		"Comments are append-only Markdown entries in an issue's activity timeline.",
		newCommentAddCommand(e),
		newCommentListCommand(e),
	)
}

func newCommentAddCommand(e *env) *cobra.Command {
	return boa.CmdT[commentAddParams]{
		Use: "add", Short: "Add a Markdown comment to an issue",
		ParamEnrich: boaParams,
		RunFuncE: func(p *commentAddParams, cmd *cobra.Command, _ []string) error {
			body, err := commentText(e, p.Body, p.BodyFile)
			if err != nil {
				return err
			}
			be, err := e.backend(cmd.Context())
			if err != nil {
				return err
			}
			entry, err := be.AddComment(cmd.Context(), p.ID, body)
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(entry)
			}
			return nil
		},
	}.ToCobra()
}

func commentText(e *env, body, file *string) (string, error) {
	if body != nil && file != nil {
		return "", awberr.Usagef("--body and --body-file are mutually exclusive")
	}
	if body != nil {
		return *body, nil
	}
	if file == nil {
		return "", awberr.Usagef("give --body or --body-file")
	}
	var (
		data []byte
		err  error
	)
	if *file == "-" {
		data, err = io.ReadAll(e.stdin)
	} else {
		data, err = os.ReadFile(*file)
	}
	if err != nil {
		return "", awberr.Wrap(awberr.Runtime, err, "read comment from %s", *file)
	}
	return string(data), nil
}

type activityParams struct {
	ID     string `positional:"true" required:"true"`
	Kind   string `long:"kind" optional:"true" alts:"comment,change" help:"show only comments or only changes"`
	Limit  *int   `long:"limit" optional:"true" help:"cap the entries returned"`
	Offset *int   `long:"offset" optional:"true" help:"skip this many entries"`
}

type commentListParams struct {
	ID     string `positional:"true" required:"true"`
	Limit  *int   `long:"limit" optional:"true" help:"cap the entries returned"`
	Offset *int   `long:"offset" optional:"true" help:"skip this many entries"`
}

func newActivityCommand(e *env) *cobra.Command {
	return activityListingCommand(e, "activity", "List an issue's comments and changes", "")
}

func newCommentListCommand(e *env) *cobra.Command {
	return boa.CmdT[commentListParams]{
		Use: "list", Short: "List an issue's comments", ParamEnrich: boaParams,
		RunFuncE: func(p *commentListParams, cmd *cobra.Command, _ []string) error {
			return runActivityListing(e, cmd, p.ID, domain.ActivityKindComment, p.Limit, p.Offset)
		},
	}.ToCobra()
}

func activityListingCommand(e *env, use, short string, fixed domain.ActivityKind) *cobra.Command {
	return boa.CmdT[activityParams]{
		Use: use, Short: short, ParamEnrich: boaParams,
		RunFuncE: func(p *activityParams, cmd *cobra.Command, _ []string) error {
			kind := fixed
			if kind == "" {
				kind = domain.ActivityKind(p.Kind)
			}
			return runActivityListing(e, cmd, p.ID, kind, p.Limit, p.Offset)
		},
	}.ToCobra()
}

func runActivityListing(e *env, cmd *cobra.Command, id string, kind domain.ActivityKind,
	limit, offset *int) error {
	if err := checkPaging(limit, offset); err != nil {
		return err
	}
	be, err := e.backend(cmd.Context())
	if err != nil {
		return err
	}
	page, err := be.ListActivity(cmd.Context(), id, kind, limit, offset)
	if err != nil {
		return err
	}
	if e.json {
		return e.writeJSON(page.Activity)
	}
	for i := range page.Activity {
		if _, err := io.WriteString(e.stdout, domain.CompactActivityLine(&page.Activity[i])+"\n"); err != nil {
			return err
		}
	}
	return nil
}
