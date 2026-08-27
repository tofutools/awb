// Package backend is the one interface every awb command is written against.
//
// SPEC §6 requires that setting --db to a server's URL makes the CLI operate
// against it with every command behaving identically to direct mode. The way to
// guarantee that, rather than maintain it by hand, is to give the command line
// a single interface with two implementations: internal/local over the SQLite
// database, and internal/remote over the HTTP API. A command is written once
// and cannot behave differently in the two modes, because it cannot tell them
// apart.
//
// The HTTP handlers sit on the same interface, over the local implementation,
// so the API and the CLI exercise one code path (SPEC §9).
package backend

import (
	"context"

	"github.com/tofutools/awb/internal/domain"
)

// Backend is every operation awb can perform on the data.
//
// The ifMatch parameters carry the optional conditional-edit precondition of
// SPEC §6.2: the ETag a client read, or "" for no precondition. The CLI always
// passes "" and gets last-write-wins; a web UI passes the tag it read.
type Backend interface {
	// Identity is who the backend attributes an unattributed action to: the
	// caller's own identity locally, and the server's answer to
	// GET /api/identity remotely (SPEC §6.2).
	Identity(ctx context.Context) (string, error)

	CreateProject(ctx context.Context, req ProjectCreate) (*domain.Project, error)
	GetProject(ctx context.Context, key string) (*domain.Project, error)
	ListProjects(ctx context.Context, limit, offset *int) (ProjectPage, error)
	UpdateProject(ctx context.Context, key string, req ProjectPatch, ifMatch string) (*domain.Project, error)
	DeleteProject(ctx context.Context, key string, cascade bool, ifMatch string) (*DeletedProject, error)

	CreateIssue(ctx context.Context, req IssueCreate) (*domain.Issue, error)
	GetIssue(ctx context.Context, ref string) (*domain.Issue, error)
	ListIssues(ctx context.Context, filter *domain.Filter) (IssuePage, error)
	UpdateIssue(ctx context.Context, ref string, req IssuePatch, ifMatch string) (*domain.Issue, error)
	DeleteIssue(ctx context.Context, ref string, ifMatch string) (*DeletedIssue, error)

	Claim(ctx context.Context, ref string, req ClaimRequest, ifMatch string) (*domain.Issue, error)
	Release(ctx context.Context, ref string, req ReleaseRequest, ifMatch string) (*domain.Issue, error)
	CloseIssue(ctx context.Context, ref string, req CloseRequest, ifMatch string) (*domain.Issue, error)
	Reopen(ctx context.Context, ref string, ifMatch string) (*domain.Issue, error)

	AddLabel(ctx context.Context, ref, label string, ifMatch string) (*domain.Issue, error)
	RemoveLabel(ctx context.Context, ref, label string, ifMatch string) (*domain.Issue, error)

	AddRelation(ctx context.Context, ref string, req RelationRequest, ifMatch string) (*domain.Issue, error)
	RemoveRelation(ctx context.Context, ref string, relType domain.RelationType, other string,
		ifMatch string) (*domain.Issue, error)

	Tree(ctx context.Context, ref string) (*domain.IssueTree, error)

	LabelFacets(ctx context.Context, filter *domain.Filter) (FacetPage, error)
	AssigneeFacets(ctx context.Context, filter *domain.Filter) (FacetPage, error)

	// Close releases whatever the backend holds: the database file, or the
	// idle connections of an HTTP client.
	Close() error
}

// IssuePage is a listing with the unpaged total that X-Total-Count carries
// (SPEC §6.2), so a UI can show "1–50 of 214".
type IssuePage struct {
	Issues []domain.Issue
	Total  int
}

// ProjectPage is a project listing with its unpaged total.
type ProjectPage struct {
	Projects []domain.Project
	Total    int
}

// FacetPage is a facet listing with its unpaged total. The total counts the
// facet rows, not the issues behind them.
type FacetPage struct {
	Facets []domain.Facet
	Total  int
}

// IssueCreate is the body of awb create and of POST /api/issues (SPEC §6.4).
// Everything but Project and Title may be left at its zero value and then takes
// the default of §2.2.
type IssueCreate struct {
	Project     string
	Title       string
	Description string
	Type        domain.Type
	Priority    *int
	// A non-empty Assignee makes creation an atomic create-and-claim: it also
	// sets status to in_progress, so a new issue is never open and assigned at
	// once.
	Assignee string
	Labels   []string
	// Relations are read with the new issue as the subject, exactly as awb
	// create's relation flags are. At most one may be has-parent.
	Relations []NewRelation
}

// NewRelation is one entry of IssueCreate.Relations.
type NewRelation struct {
	Type  domain.RelationType
	Other string
}

// IssuePatch is what awb update and PATCH /api/issues/{id} may change. It
// cannot change status or assignee: the four transitions are the only way to
// move either, which keeps in_progress and an assignee from drifting apart and
// keeps a claim from being taken silently (SPEC §4.3).
//
// A nil field is left alone; a non-nil one is written, so an empty string
// clears an optional value.
type IssuePatch struct {
	Title       *string
	Description *string
	Type        *domain.Type
	Priority    *int
}

// ProjectCreate is the body of awb project add and of POST /api/projects.
type ProjectCreate struct {
	Key         string
	Name        string
	Description string
}

// ProjectPatch is what awb project update and PATCH /api/projects/{key} may
// change. The key itself is immutable.
type ProjectPatch struct {
	Name        *string
	Description *string
}

// ClaimRequest is the body of awb claim and POST /api/issues/{id}/claim.
type ClaimRequest struct {
	// Assignee names who takes the issue. The CLI always states it explicitly,
	// so that a remote claim records exactly what a local one would (SPEC §6).
	Assignee string
	// ExpectAssignee is the compare-and-set: when non-nil the claim proceeds
	// only if the current assignee is exactly that value, "" meaning
	// unassigned, and otherwise conflicts. It is what stops two agents racing
	// for the same issue from both winning (SPEC §6.4).
	ExpectAssignee *string
	// Force overrides a refusal on an issue that is held by somebody else,
	// blocked, or closed.
	Force bool
}

// ReleaseRequest is the body of awb release and POST /api/issues/{id}/release.
type ReleaseRequest struct {
	// Assignee is the caller's identity, and is what the "assigned to someone
	// else" refusal compares against. It may be empty when Force is set, that
	// refusal being the only thing it serves.
	Assignee string
	Force    bool
}

// CloseRequest is the body of awb close and POST /api/issues/{id}/close.
type CloseRequest struct {
	// Reason is nil when --reason was not given, which leaves any recorded
	// reason alone; a pointer to "" clears it.
	Reason *string
}

// RelationRequest is the body of awb dep add and
// POST /api/issues/{id}/relations, read with the addressed issue as the
// subject.
type RelationRequest struct {
	Type  domain.RelationType
	Other string
	// Force replaces an existing parent, which is the only refusal it
	// overrides.
	Force bool
}

// DeletedIssue is what a delete returns: the issue as it was immediately before
// deletion, and how many relations went with it (SPEC §4.3).
type DeletedIssue struct {
	Issue            domain.Issue
	RelationsRemoved int
}

// DeletedProject is what project rm returns: the project as it was immediately
// before deletion, and how many issues --cascade took with it.
type DeletedProject struct {
	Project       domain.Project
	IssuesRemoved int
}
