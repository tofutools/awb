package cli

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tofutools/awb/internal/config"
	"github.com/tofutools/awb/internal/domain"
)

type statusConnection struct {
	Mode        string `json:"mode"`
	Database    string `json:"database"`
	Server      string `json:"server"`
	Attachments string `json:"attachments"`
}

type statusConfiguration struct {
	Identity           string           `json:"identity"`
	ConfiguredIdentity string           `json:"configured_identity"`
	User               string           `json:"user"`
	PasswordSet        bool             `json:"password_set"`
	DefaultProject     string           `json:"default_project"`
	ContextProject     string           `json:"context_project"`
	ContextLabel       string           `json:"context_label"`
	UserFile           string           `json:"user_file"`
	LocalFile          string           `json:"local_file"`
	Color              config.ColorMode `json:"color"`
}

type statusEnvironment struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type statusProject struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Open       int    `json:"open"`
	InProgress int    `json:"in_progress"`
	Closed     int    `json:"closed"`
	Total      int    `json:"total"`
}

type statusReport struct {
	Connection    statusConnection    `json:"connection"`
	Configuration statusConfiguration `json:"configuration"`
	Environment   []statusEnvironment `json:"environment"`
	Projects      []statusProject     `json:"projects"`
}

var statusEnvironmentNames = []string{
	"AWB_CONFIG_FILE",
	"AWB_DB",
	"AWB_ATTACHMENTS",
	"AWB_USER",
	"AWB_PASSWORD",
	"AWB_IDENTITY",
	"AWB_PROJECT",
	"AWB_COLOR",
	"NO_COLOR",
}

func newStatusCommand(e *env) *cobra.Command {
	return command("status", "Show the active connection, identity, configuration and project counts",
		"Show how this invocation is configured and what data source it is using.\n\n"+
			"Local mode names the SQLite database and attachment directory. Remote mode\n"+
			"names the awb server and asks it which identity it authenticated. Environment\n"+
			"variables that are set are shown separately; password content is never shown.\n\n"+
			"Each visible project includes exact issue counts by status.",
		func(cmd *cobra.Command, _ []string) error {
			report, err := e.buildStatus(cmd)
			if err != nil {
				return err
			}
			if e.json {
				return e.writeJSON(report)
			}
			if e.compact {
				return e.printCompactStatus(report)
			}
			return e.printStatus(report)
		})
}

func (e *env) buildStatus(cmd *cobra.Command) (*statusReport, error) {
	cfg, err := e.config()
	if err != nil {
		return nil, err
	}
	be, err := e.backend(cmd.Context())
	if err != nil {
		return nil, err
	}
	identity, err := be.AuthenticatedIdentity(cmd.Context())
	if err != nil {
		return nil, err
	}

	report := &statusReport{
		Connection: statusConnection{Mode: "local", Database: cfg.DB, Attachments: cfg.Attachments},
		Configuration: statusConfiguration{
			Identity: identity, ConfiguredIdentity: cfg.Identity, User: cfg.User,
			PasswordSet: cfg.Password != "", DefaultProject: cfg.DefaultProject,
			ContextProject: cfg.ContextProject, ContextLabel: cfg.ContextLabel,
			UserFile: cfg.UserFilePath, LocalFile: cfg.LocalFilePath, Color: cfg.Color,
		},
		Environment: configuredEnvironment(),
		Projects:    []statusProject{},
	}
	if cfg.Remote() {
		report.Connection.Mode = "remote"
		report.Connection.Server = cfg.DB
		report.Connection.Database = ""
	}

	page, err := be.ListProjects(cmd.Context(), nil, nil)
	if err != nil {
		return nil, err
	}
	zero := 0
	for _, project := range page.Projects {
		counts := make(map[domain.Status]int, len(domain.Statuses))
		for _, status := range domain.Statuses {
			issues, err := be.ListIssues(cmd.Context(), &domain.Filter{
				Projects: []string{project.Key}, Statuses: []domain.Status{status}, Limit: &zero,
			})
			if err != nil {
				return nil, err
			}
			counts[status] = issues.Total
		}
		report.Projects = append(report.Projects, statusProject{
			Key: project.Key, Name: project.Name,
			Open: counts[domain.StatusOpen], InProgress: counts[domain.StatusInProgress],
			Closed: counts[domain.StatusClosed],
			Total:  counts[domain.StatusOpen] + counts[domain.StatusInProgress] + counts[domain.StatusClosed],
		})
	}
	return report, nil
}

func configuredEnvironment() []statusEnvironment {
	values := []statusEnvironment{}
	for _, name := range statusEnvironmentNames {
		value, set := os.LookupEnv(name)
		if !set {
			continue
		}
		if name == "AWB_PASSWORD" {
			value = "<redacted>"
		}
		values = append(values, statusEnvironment{Name: name, Value: value})
	}
	return values
}

func (e *env) printStatus(report *statusReport) error {
	w := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Connection\n  Mode:\t%s\n", report.Connection.Mode)
	if report.Connection.Mode == "remote" {
		_, _ = fmt.Fprintf(w, "  Server:\t%s\n", report.Connection.Server)
	} else {
		_, _ = fmt.Fprintf(w, "  SQLite database:\t%s\n  Attachments:\t%s\n",
			report.Connection.Database, report.Connection.Attachments)
	}

	c := report.Configuration
	_, _ = fmt.Fprintf(w, "\nConfiguration\n  Identity:\t%s\n  Configured identity:\t%s\n",
		valueOrNone(c.Identity), valueOrNone(c.ConfiguredIdentity))
	if report.Connection.Mode == "remote" {
		_, _ = fmt.Fprintf(w, "  User:\t%s\n  Password:\t%s\n", valueOrNone(c.User), setOrNot(c.PasswordSet))
	}
	_, _ = fmt.Fprintf(w,
		"  Default project:\t%s\n  Context project:\t%s\n  Context label:\t%s\n"+
			"  User config:\t%s\n  Local config:\t%s\n  Color:\t%s\n",
		valueOrNone(c.DefaultProject), valueOrNone(c.ContextProject), valueOrNone(c.ContextLabel),
		valueOrNone(c.UserFile), valueOrNone(c.LocalFile), c.Color)

	_, _ = fmt.Fprintln(w, "\nEnvironment")
	if len(report.Environment) == 0 {
		_, _ = fmt.Fprintln(w, "  (none)")
	} else {
		for _, variable := range report.Environment {
			_, _ = fmt.Fprintf(w, "  %s=%s\n", variable.Name, strconv.Quote(variable.Value))
		}
	}

	_, _ = fmt.Fprintln(w, "\nProjects")
	if len(report.Projects) == 0 {
		_, _ = fmt.Fprintln(w, "  (none)")
	} else {
		_, _ = fmt.Fprintln(w, "  KEY\tNAME\tOPEN\tIN PROGRESS\tCLOSED\tTOTAL")
		for _, project := range report.Projects {
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%d\t%d\t%d\t%d\n", project.Key, project.Name,
				project.Open, project.InProgress, project.Closed, project.Total)
		}
	}
	_ = w.Flush()
	return e.stdout.Err()
}

// Compact status output is one stable record per fact. Values that may carry
// whitespace are quoted with Go/JSON string syntax.
func (e *env) printCompactStatus(report *statusReport) error {
	q := strconv.Quote
	_, _ = fmt.Fprintf(e.stdout, "connection mode=%s database=%s server=%s attachments=%s\n",
		report.Connection.Mode, q(report.Connection.Database), q(report.Connection.Server),
		q(report.Connection.Attachments))
	c := report.Configuration
	_, _ = fmt.Fprintf(e.stdout,
		"configuration identity=%s configured_identity=%s user=%s password_set=%t default_project=%s "+
			"context_project=%s context_label=%s user_file=%s local_file=%s color=%s\n",
		q(c.Identity), q(c.ConfiguredIdentity), q(c.User), c.PasswordSet, q(c.DefaultProject),
		q(c.ContextProject), q(c.ContextLabel), q(c.UserFile), q(c.LocalFile), c.Color)
	for _, variable := range report.Environment {
		_, _ = fmt.Fprintf(e.stdout, "environment name=%s value=%s\n", variable.Name, q(variable.Value))
	}
	for _, project := range report.Projects {
		_, _ = fmt.Fprintf(e.stdout,
			"project key=%s name=%s open=%d in_progress=%d closed=%d total=%d\n",
			project.Key, q(project.Name), project.Open, project.InProgress, project.Closed, project.Total)
	}
	return e.stdout.Err()
}

func valueOrNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

func setOrNot(set bool) string {
	if set {
		return "set"
	}
	return "not set"
}
