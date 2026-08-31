// Package backend is the one interface every awb command is written against.
//
// Setting --db to a server's URL must make the CLI operate against it with
// every command behaving identically to direct mode. The way to guarantee
// that, rather than maintain it by hand, is to give the command line a single
// interface with two implementations: internal/local over the SQLite database,
// and internal/remote over the HTTP API. A command is written once and cannot
// behave differently in the two modes, because it cannot tell them apart.
//
// The HTTP handlers sit on the same interface, over the local implementation,
// so the API and the CLI exercise one code path.
package backend

import (
	"context"
	"io"

	"github.com/tofutools/awb/internal/domain"
)

// Backend is every operation awb can perform on the data.
//
// The ifMatch parameters carry the optional conditional-edit precondition: the
// ETag a client read, or "" for no precondition. The CLI passes the tag saved
// by a description fetch when replacing that description; callers which omit
// it get last-write-wins.
type Backend interface {
	// Identity is who the backend attributes an unattributed action to: the
	// caller's own identity locally, and the server's answer to GET /api/identity
	// remotely.
	Identity(ctx context.Context) (string, error)
	// AuthenticatedIdentity is who the data source sees as the caller. It is the
	// same as Identity in direct mode; against a server it is the server's
	// answer, which may differ from the client's configured default identity.
	AuthenticatedIdentity(ctx context.Context) (string, error)
	SearchNavigation(ctx context.Context, query string, limit int) (NavigationResults, error)

	CreateProject(ctx context.Context, req ProjectCreate) (*domain.Project, error)
	GetProject(ctx context.Context, key string) (*domain.Project, error)
	ListProjects(ctx context.Context, filter string, sort domain.ProjectSort, limit, offset *int) (ProjectPage, error)
	ListProjectsByState(ctx context.Context, filter string, state domain.ProjectStateFilter, sort domain.ProjectSort, limit, offset *int) (ProjectPage, error)
	UpdateProject(ctx context.Context, key string, req ProjectPatch, ifMatch string) (*domain.Project, error)
	ArchiveProject(ctx context.Context, key, ifMatch string) (*domain.Project, error)
	RestoreProject(ctx context.Context, key, ifMatch string) (*domain.Project, error)
	ListProjectActivity(ctx context.Context, key string, limit, offset *int) (ProjectActivityPage, error)
	DeleteProject(ctx context.Context, key string, cascade bool, ifMatch string) (*DeletedProject, error)
	ListProjectPreferences(ctx context.Context) ([]domain.ProjectPreference, error)
	SetProjectIgnored(ctx context.Context, key string, ignored bool) (*domain.ProjectPreference, error)

	CreateIssue(ctx context.Context, req IssueCreate) (*domain.Issue, error)
	GetIssue(ctx context.Context, ref string) (*domain.Issue, error)
	ListIssues(ctx context.Context, filter *domain.Filter) (IssuePage, error)
	SuggestIssues(ctx context.Context, query string, limit *int) (IssuePage, error)
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

	// Activity is append-only. Comments are explicit writes; change entries are
	// produced by the mutations above inside their own transactions.
	AddComment(ctx context.Context, ref, body string) (*domain.Activity, error)
	ListActivity(ctx context.Context, ref string, kind domain.ActivityKind,
		limit, offset *int) (ActivityPage, error)

	// The attachment operations. An attachment is addressed by the issue it
	// belongs to and its name, which is the pair that identifies one; it has no
	// identifier of its own. It is immutable once stored, so there is no update
	// and none of these takes an ifMatch: there is no second version of one to
	// guard against.
	AddAttachment(ctx context.Context, issueRef string, req AttachmentCreate) (*domain.Attachment, error)
	GetAttachment(ctx context.Context, issueRef, name string) (*domain.Attachment, error)
	ListAttachments(ctx context.Context, issueRef string, limit, offset *int) (AttachmentPage, error)
	// OpenAttachment returns the metadata and a reader over the content, which
	// the caller closes. It is the one operation that carries bytes rather than
	// a shape, because an attachment's content is the one thing awb stores that
	// is not text it validated.
	OpenAttachment(ctx context.Context, issueRef, name string) (*domain.Attachment, io.ReadCloser, error)
	DeleteAttachment(ctx context.Context, issueRef, name string) (*domain.Attachment, error)

	LabelFacets(ctx context.Context, filter *domain.Filter) (FacetPage, error)
	AssigneeFacets(ctx context.Context, filter *domain.Filter) (FacetPage, error)

	// The user operations. A user is an account a server authenticates and
	// authorizes; direct mode manages them but applies none of them to itself.
	//
	// A password never comes back out: no shape any of these returns carries
	// one, in either direction of the wire.
	CreateUser(ctx context.Context, req UserCreate) (*domain.User, error)
	GetUser(ctx context.Context, name string) (*domain.User, error)
	ListUsers(ctx context.Context, filter string, limit, offset *int) (UserPage, error)
	UpdateUser(ctx context.Context, name string, req UserPatch, ifMatch string) (*domain.User, error)
	DeleteUser(ctx context.Context, name string, ifMatch string) (*DeletedUser, error)

	// The membership operations. A membership is keyed on its project and its
	// user, as an attachment is keyed on its issue and its name, so it has no
	// identifier of its own and none of these takes an ifMatch: a membership
	// has one field and setting it is idempotent.
	ListMembers(ctx context.Context, project string, limit, offset *int) (MemberPage, error)
	SetMember(ctx context.Context, project, user string, access domain.Access) (*domain.Membership, error)
	RemoveMember(ctx context.Context, project, user string) (*domain.Membership, error)

	// Close releases whatever the backend holds: the database file, or the idle
	// connections of an HTTP client.
	Close() error
}

// ETag is the strong entity tag derived from an entity's strictly increasing
// updated_at value. Both backend implementations return updated_at, so a
// caller can preserve the same precondition in direct and remote mode. For an
// issue, updated_at also moves when its labels or attachments change or a
// comment is posted, so a tag guards all of those changes since the caller
// read it.
func ETag(updatedAt string) string { return `"` + updatedAt + `"` }

// IssuePage is a listing with the unpaged total that X-Total-Count carries, so
// a UI can show "1–50 of 214".
type IssuePage struct {
	Issues []domain.Issue
	Total  int
}

// ProjectPage is a project listing with its unpaged total.
type ProjectPage struct {
	Projects []domain.Project
	Total    int
}

type ProjectActivityPage struct {
	Activity []domain.ProjectActivity
	Total    int
}

// NavigationResults is the small, grouped autocomplete result used by clients
// that navigate to records without first loading their full collections.
type NavigationResults struct {
	Issues   []domain.Issue
	Projects []domain.Project
	Users    []domain.User
}

// FacetPage is a facet listing with its unpaged total. The total counts the
// facet rows, not the issues behind them.
type FacetPage struct {
	Facets []domain.Facet
	Total  int
}

// AttachmentPage is an attachment listing with its unpaged total.
type AttachmentPage struct {
	Attachments []domain.Attachment
	Total       int
}

// ActivityPage is an issue timeline with its unpaged total.
type ActivityPage struct {
	Activity []domain.Activity
	Total    int
}

// UserPage is a user listing with its unpaged total.
type UserPage struct {
	Users []domain.User
	Total int
}

// MemberPage is a project's member listing with its unpaged total.
type MemberPage struct {
	Members []domain.Membership
	Total   int
}

// UserCreate is the body of awb user add and of POST /api/users.
type UserCreate struct {
	Name     string
	FullName string
	// Password is the plaintext, which is hashed and then dropped; PasswordHash
	// is a bcrypt hash computed elsewhere, as "htpasswd -Bn" writes one. Exactly
	// one of the two is given: a request carrying both says two things about one
	// credential, and one carrying neither describes an account nobody can log
	// in to.
	Password     string
	PasswordHash string

	ProjectAdmin bool
	UserAdmin    bool
}

// UserPatch is what awb user update and PATCH /api/users/{name} may change. A
// nil field is left alone.
//
// The name itself is immutable, as a project key is: it is what the issues
// that user works on record as their assignee, and renaming it would leave
// that record pointing at nobody.
type UserPatch struct {
	// At most one of these two is given, for the reason UserCreate states.
	Password     *string
	PasswordHash *string
	FullName     *string

	ProjectAdmin *bool
	UserAdmin    *bool
}

// DeletedUser is what a delete returns: the user as they were immediately
// before deletion, memberships included.
type DeletedUser struct {
	User domain.User
}

// AttachmentCreate is the body of awb attach add and of POST
// /api/issues/{id}/attachments.
type AttachmentCreate struct {
	// Name is the file name the attachment is shown under, and half of what
	// identifies it. An issue cannot hold two under one name. It is not a path
	// and is never used to build one.
	Name string
	// ContentType may be empty, in which case it is sniffed from the first
	// bytes of the content. The sniffing rule lives in the domain layer, so a
	// file uploaded through either surface is typed identically.
	ContentType string
	// Content is read to its end and closed by the caller, not by the backend.
	Content io.Reader
}

// IssueCreate is the body of awb create and of POST /api/issues. Everything
// but Project and Title may be left at its zero value and then takes its
// documented default.
type IssueCreate struct {
	Project     string
	Title       string
	Description string
	Type        domain.Type
	Priority    *int
	// Assignees permits an atomic create-and-claim by several people.
	Assignees []string
	Labels    []string
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
// cannot change status or assignees: the four transitions are the only way to
// move either, which keeps in_progress and the assignment set from drifting
// apart and keeps a claim from being taken silently.
//
// A nil field is left alone; a non-nil one is written, so an empty string
// clears an optional value.
type IssuePatch struct {
	Title       *string
	Description *string
	Type        *domain.Type
	Priority    *int

	// The three fields below may appear in a request but may not change: each
	// is ignored when it equals what is stored and refused when it differs,
	// because labels are mutated individually and the transitions are their
	// own operations. Together with the rule that derived fields are ignored,
	// that is what lets a UI send back the object it read with only the fields
	// it edited changed.
	//
	// They are carried here, rather than compared in the HTTP adapter, so the
	// comparison happens inside the same transaction as the write. Checking
	// them beforehand would leave a window in which a concurrent transition
	// could make a stale value pass.
	ExpectLabels    *[]string
	ExpectStatus    *domain.Status
	ExpectAssignees *[]string
}

// ProjectCreate is the body of awb project create and of POST /api/projects.
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
	// Assignee names who takes the issue. The CLI always states it explicitly, so
	// that a remote claim records exactly what a local one would.
	Assignee string
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
	// A non-empty Reason is recorded as a typed comment on the closing
	// transition. Nil and a pointer to "" both record no reason comment.
	Reason *string
}

// RelationRequest is the body of awb dep add and POST
// /api/issues/{id}/relations, read with the addressed issue as the subject.
type RelationRequest struct {
	Type  domain.RelationType
	Other string
	// Force replaces an existing parent, which is the only refusal it overrides.
	Force bool
}

// DeletedIssue is what a delete returns: the issue as it was immediately
// before deletion, and how many relations went with it.
type DeletedIssue struct {
	Issue            domain.Issue
	RelationsRemoved int
}

// DeletedProject is what project delete returns: the project as it was immediately
// before deletion.
//
// It deliberately carries no count of the issues --cascade took with it. Direct
// mode knows that number, but the API response is a Project, whose
// active_issues excludes closed issues and so cannot stand in for it — and
// giving the response a count would be a second, HTTP-only representation of a
// shape the CLI also returns. Rather than let the two modes print different
// numbers, neither prints one.
type DeletedProject struct {
	Project domain.Project
}
