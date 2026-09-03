// Package handler is the HTTP adapter. It implements the server interface
// generated from openapi.yaml, over the same backend interface the CLI uses,
// so the API and the command line exercise one set of operations and cannot
// drift apart.
//
// Routing, parameter and body decoding, and the vocabulary and length rules
// the document states are the generated code's work; what is left here is the
// translation between the API's shapes and the domain's, the strictness rules
// the document states but a generator does not enforce (see middleware),
// and the mapping of awb's error taxonomy onto statuses (see NewError).
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/openapi"
)

// Handler serves the JSON API.
type Handler struct {
	// backendFor gives each request a backend acting as that request's identity,
	// which is the authenticated username or the server's fixed one. It reads
	// the context because that is what the generated server passes on: the
	// authentication middleware puts the username there before it arrives.
	backendFor func(context.Context) backend.Backend
	// operations is what the document says each operation accepts; see
	// middleware.
	operations map[string]openapi.Operation
}

var _ api.Handler = (*Handler)(nil)

// New builds the API handler. backendFor is called once per request.
func New(backendFor func(context.Context) backend.Backend,
	operations map[string]openapi.Operation) *Handler {
	return &Handler{backendFor: backendFor, operations: operations}
}

// NewServer builds the whole API surface: the generated router, decoders and
// validators in front of this handler, with the strictness rules and the error
// shapes that belong to awb rather than to a generator.
func NewServer(backendFor func(context.Context) backend.Backend,
	operations map[string]openapi.Operation) (http.Handler, error) {
	h := New(backendFor, operations)
	return api.NewServer(h, h,
		api.WithMiddleware(h.middleware()),
		api.WithErrorHandler(h.errorHandler),
		api.WithNotFound(notFound),
		api.WithMethodNotAllowed(methodNotAllowed),
	)
}

// HandleBasicAuth accepts whatever credentials reach it.
//
// Authentication happens in front of this server, in the middleware that checks
// the users table, and a request that failed it never arrives. The document
// declares the scheme because it describes the API, and the generated server
// asks about it because the document declares it; there is nothing left here
// to check.
//
// What the authenticated caller may then do is decided in the layer below, in
// the same transaction as the write it guards, and not here.
func (h *Handler) HandleBasicAuth(ctx context.Context, _ api.OperationName,
	_ api.BasicAuth) (context.Context, error) {
	return ctx, nil
}

// middleware enforces the rules the document states about what an operation
// accepts and a generated server does not enforce by itself.
//
// A query parameter the operation does not declare is refused, and so is a
// request body carried to an operation that declares none. Both are refusals
// rather than silent omissions, for the same reason an unrecognised body field
// is one — something the server never reads is a thing the client believes it
// said. What each operation declares is read out of the document, so neither
// rule can drift from it.
//
// The third rule is the UTF-8 half of the input rules, which has to be applied
// to the raw bytes: a decoder replaces an unpaired surrogate escape with
// U+FFFD, which is indistinguishable from a U+FFFD the caller meant to send.
// It is asked only of the bodies that are text. An attachment's content is
// bytes the caller chose and awb stores unread, so there is nothing there to
// be valid or invalid UTF-8; it is also streamed rather than buffered, so
// there are no raw bytes here to look at either.
func (h *Handler) middleware() api.Middleware {
	return func(req middleware.Request, next middleware.Next) (middleware.Response, error) {
		operation := h.operations[req.OperationID]

		// Sorted, so a request with two unaccepted parameters always names the
		// same one.
		for _, name := range slices.Sorted(maps.Keys(req.Raw.URL.Query())) {
			if !operation.QueryParameters[name] {
				return middleware.Response{}, awberr.Usagef(
					"this endpoint does not accept the %s parameter", name)
			}
		}

		if !operation.TakesBody {
			if err := rejectBody(req.Raw); err != nil {
				return middleware.Response{}, err
			}
		} else if operation.DeclaresJSONBody() {
			if err := checkText(req.RawBody); err != nil {
				return middleware.Response{}, err
			}
		}

		return next(req)
	}
}

// NewError maps a failure onto its status and the Error body.
//
// The five kinds carry the exit-code taxonomy; a failed precondition is the
// one extra case, answered 412, which has no exit code of its own, and a body
// over the transport cap the other, answered 413.
func (h *Handler) NewError(_ context.Context, err error) *api.ErrorStatusCode {
	status := awberr.KindOf(err).HTTPStatus()
	message := err.Error()

	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		// A body over the transport cap, read by the rule that refuses a body
		// to an operation that declares none.
		status, message = http.StatusRequestEntityTooLarge, "request body is too large"
	case errors.Is(err, awberr.ErrPreconditionFailed):
		status = http.StatusPreconditionFailed
	}
	return &api.ErrorStatusCode{StatusCode: status, Response: api.Error{Error: message}}
}

// errorHandler answers the failures that happen before an operation is
// reached: a request the generated decoders refused, and a body over the
// transport cap.
//
// The status comes from the error itself where it carries one, so that what a
// decoder considers a bad request stays a bad request, and is 500 otherwise,
// which is where an error nobody classified belongs.
//
// A body claiming a content type the operation does not declare is a 415, a
// missing Content-Type included — the rule is about what the body claims to
// be, and a body claiming nothing claims nothing useful. Which types those are
// is read out of the document, exactly as the other two strictness rules are,
// so the rule cannot drift from what each endpoint actually accepts. It is
// asked only of a request that carries a body: one that sends none is never
// asked to describe it, and reaches here only because the operation required a
// body it did not send, which is a 400.
func (h *Handler) errorHandler(_ context.Context, w http.ResponseWriter, r *http.Request,
	err error) {
	status := http.StatusInternalServerError
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		status = coded.Code()
	}

	// Both of these are about the request as a whole rather than about anything
	// in it, so neither takes the decoder's account of where it stopped.
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	case bodyWasCarried(r) && !h.bodyTypeIsDeclared(err, r):
		writeError(w, http.StatusUnsupportedMediaType,
			"request body must be "+h.declaredBodyType(err))
		return
	}
	writeError(w, status, message(err))
}

// bodyTypeIsDeclared reports whether the body's content type is one the
// operation the request reached declares.
//
// The operation is the one the failing error names: an ogen error carries the
// id of the operation it was raised in, which is how a rule stated per
// operation can be applied to a failure that happened before the handler was
// called. A failure carrying no operation at all never reached routing, and
// then the request's body cannot be right for anything.
func (h *Handler) bodyTypeIsDeclared(err error, r *http.Request) bool {
	operation, ok := h.operationOf(err)
	if !ok {
		return false
	}
	return operation.AcceptsBodyType(r.Header.Get("Content-Type"))
}

// declaredBodyType names what the operation would have accepted, for the
// refusal to say.
func (h *Handler) declaredBodyType(err error) string {
	operation, ok := h.operationOf(err)
	if !ok || len(operation.BodyMediaTypes) == 0 {
		return "application/json"
	}
	return strings.Join(operation.BodyMediaTypes, " or ")
}

func (h *Handler) operationOf(err error) (openapi.Operation, bool) {
	var ogenErr ogenerrors.Error
	if !errors.As(err, &ogenErr) {
		return openapi.Operation{}, false
	}
	operation, ok := h.operations[ogenErr.OperationID()]
	return operation, ok
}

// message is the line of prose a refused request is answered with.
//
// The generated decoders report where they stopped, which is worth keeping —
// it names the field. What they cannot say is what is wrong with a body that
// is wrong as a whole, so those cases are answered here: a body that was not
// sent where one is required, a body that was sent and holds no JSON value at
// all, and a body holding a null, which is a state these shapes do not have,
// since everything they say turns on a field being present or absent and null
// is neither.
func message(err error) string {
	if errors.Is(err, validate.ErrBodyRequired) {
		return "a request body is required"
	}

	var body *ogenerrors.DecodeBodyError
	if errors.As(err, &body) {
		switch {
		case holdsNoValue(body.Body):
			return "the request body holds no JSON value"
		case holdsNull(body.Body):
			return "request body holds a null: clear a value with \"\" and leave it alone by omitting it"
		}
		// jx names the callback it was inside when it stopped, which says
		// nothing to a caller.
		return "invalid request body: " + strings.ReplaceAll(body.Err.Error(), "callback: ", "")
	}

	var param *ogenerrors.DecodeParamError
	if errors.As(err, &param) {
		return fmt.Sprintf("invalid %s parameter %q: %s", param.In, param.Name, param.Err)
	}

	return err.Error()
}

// notFound and methodNotAllowed answer the two failures the router itself
// reports, in the API's error shape rather than net/http's plain text: a path
// under /api/ that no operation serves, and a path that exists whose method
// does not.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request, allowed string) {
	w.Header().Set("Allow", allowed)
	writeError(w, http.StatusMethodNotAllowed,
		r.Method+" is not allowed here; "+allowed+" is")
}

// writeError sends one error body, encoded as the generated code encodes the
// ones it sends: no HTML escaping and no trailing newline, so every error this
// API answers with looks the same whether it was reached before or after
// routing.
func writeError(w http.ResponseWriter, status int, message string) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(api.Error{Error: message}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(bytes.TrimRight(buffer.Bytes(), "\n"))
}

// The conversions between the API's shapes and the domain's. Relation titles
// are visibility-scoped presentation metadata carried by both implementations
// of the shared backend; the remote CLI adds web links after receiving one of
// these responses.

func toIssue(issue *domain.Issue) api.Issue {
	return api.Issue{
		ID:             issue.ID,
		Workspace:      api.WorkspaceKey(issue.Workspace),
		Title:          issue.Title,
		Description:    issue.Description,
		CommitHash:     api.CommitHash(issue.CommitHash),
		PullRequestURL: api.PullRequestURL(issue.PullRequestURL),
		Type:           api.Type(issue.Type),
		Status:         api.Status(issue.Status),
		Priority:       api.Priority(issue.Priority),
		Order:          issue.Order,
		Labels:         toLabels(issue.Labels),
		Assignees:      toAssignees(issue.Assignees),
		CreatedAt:      api.Timestamp(issue.CreatedAt),
		UpdatedAt:      api.Timestamp(issue.UpdatedAt),
		ClosedAt:       issue.ClosedAt,
		Blocked:        issue.Blocked,
		Blockers:       issue.Blockers,
		Relations:      toRelations(issue),
		Links:          toLinks(issue.Links),
		Attachments:    toAttachments(issue.Attachments),
	}
}

func toIssues(issues []domain.Issue) []api.Issue {
	out := make([]api.Issue, len(issues))
	for i := range issues {
		out[i] = toIssue(&issues[i])
	}
	return out
}

func toTree(tree *domain.IssueTree) api.IssueTree {
	issue := toIssue(&tree.Issue)
	children := make([]api.IssueTree, len(tree.Children))
	for i := range tree.Children {
		children[i] = toTree(&tree.Children[i])
	}
	return api.IssueTree{
		ID:             issue.ID,
		Workspace:      issue.Workspace,
		Title:          issue.Title,
		Description:    issue.Description,
		CommitHash:     issue.CommitHash,
		PullRequestURL: issue.PullRequestURL,
		Type:           issue.Type,
		Status:         issue.Status,
		Priority:       issue.Priority,
		Labels:         issue.Labels,
		Assignees:      issue.Assignees,
		CreatedAt:      issue.CreatedAt,
		UpdatedAt:      issue.UpdatedAt,
		Blocked:        issue.Blocked,
		Blockers:       issue.Blockers,
		Relations:      issue.Relations,
		Links:          issue.Links,
		Attachments:    issue.Attachments,
		Children:       children,
	}
}

func toWorkspace(workspace *domain.Workspace) api.Workspace {
	return api.Workspace{
		Key:          api.WorkspaceKey(workspace.Key),
		Name:         workspace.Name,
		Description:  workspace.Description,
		State:        api.WorkspaceState(workspace.State),
		ArchivedAt:   workspace.ArchivedAt,
		ArchivedBy:   workspace.ArchivedBy,
		ActiveIssues: workspace.ActiveIssues,
		CreatedAt:    api.Timestamp(workspace.CreatedAt),
		UpdatedAt:    api.Timestamp(workspace.UpdatedAt),
	}
}

func toWorkspaces(workspaces []domain.Workspace) []api.Workspace {
	out := make([]api.Workspace, len(workspaces))
	for i := range workspaces {
		out[i] = toWorkspace(&workspaces[i])
	}
	return out
}

func toFacets(facets []domain.Facet) []api.Facet {
	out := make([]api.Facet, len(facets))
	for i, facet := range facets {
		out[i] = api.Facet{Value: facet.Value, Count: facet.Count}
	}
	return out
}

func toLabels(labels []string) []api.Label {
	out := make([]api.Label, len(labels))
	for i, label := range labels {
		out[i] = api.Label(label)
	}
	return out
}

func toAssignees(assignees []string) []api.Assignee {
	out := make([]api.Assignee, len(assignees))
	for i := range assignees {
		out[i] = api.Assignee(assignees[i])
	}
	return out
}

func fromAssignees(assignees []api.Assignee) []string {
	out := make([]string, len(assignees))
	for i := range assignees {
		out[i] = string(assignees[i])
	}
	return out
}

func fromLabels(labels []api.Label) []string {
	out := make([]string, len(labels))
	for i, label := range labels {
		out[i] = string(label)
	}
	return out
}

func toRelations(issue *domain.Issue) []api.Relation {
	out := make([]api.Relation, len(issue.Relations))
	for i, relation := range issue.Relations {
		out[i] = api.Relation{
			Type:       api.RelationType(relation.Type),
			Other:      relation.Other,
			OtherTitle: issue.RelationTitle(relation.Other),
			Direction:  api.Direction(relation.Direction),
		}
	}
	return out
}

func toLinks(links []domain.Link) []api.Link {
	out := make([]api.Link, len(links))
	for i, link := range links {
		out[i] = api.Link{Text: link.Text, URL: link.URL}
	}
	return out
}

// The optional-value conversions. A field the caller omitted is nil, which is
// "leave it alone"; one it sent is a pointer, so an empty string clears a
// value rather than meaning nothing.

func optString(o api.OptString) *string {
	if value, ok := o.Get(); ok {
		return &value
	}
	return nil
}

func optInt(o api.OptInt) *int {
	if value, ok := o.Get(); ok {
		return &value
	}
	return nil
}

func optPriority(o api.OptPriority) *int {
	if value, ok := o.Get(); ok {
		number := int(value)
		return &number
	}
	return nil
}

func optType(o api.OptType) *domain.Type {
	if value, ok := o.Get(); ok {
		issueType := domain.Type(value)
		return &issueType
	}
	return nil
}

func optStatus(o api.OptStatus) *domain.Status {
	if value, ok := o.Get(); ok {
		status := domain.Status(value)
		return &status
	}
	return nil
}
