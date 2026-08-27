// Package handler is the HTTP adapter. It sits on the same backend interface
// the CLI uses, over the local implementation, so the API and the command line
// exercise one set of operations and cannot drift apart (SPEC §9).
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/local"
)

// Handler serves the JSON API of SPEC §6.
type Handler struct {
	// backendFor gives each request a backend acting as that request's
	// identity, which is the authenticated username or the server's fixed one.
	backendFor func(*http.Request) backend.Backend
}

// New builds the API handler. backendFor is called once per request.
func New(backendFor func(*http.Request) backend.Backend) *Handler {
	return &Handler{backendFor: backendFor}
}

// Routes registers every API endpoint on mux, under the /api/ prefix.
//
// Go's ServeMux answers 405 by itself when a path matches a pattern but the
// method does not, which is one of the six statuses SPEC §6.1 lists as having
// no exit code behind them.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/issues", h.listIssues)
	mux.HandleFunc("POST /api/issues", h.createIssue)
	mux.HandleFunc("GET /api/issues/{id}", h.getIssue)
	mux.HandleFunc("PATCH /api/issues/{id}", h.patchIssue)
	mux.HandleFunc("DELETE /api/issues/{id}", h.deleteIssue)

	mux.HandleFunc("POST /api/issues/{id}/claim", h.claim)
	mux.HandleFunc("POST /api/issues/{id}/release", h.release)
	mux.HandleFunc("POST /api/issues/{id}/close", h.closeIssue)
	mux.HandleFunc("POST /api/issues/{id}/reopen", h.reopen)

	mux.HandleFunc("POST /api/issues/{id}/labels", h.addLabel)
	mux.HandleFunc("DELETE /api/issues/{id}/labels", h.removeLabel)

	mux.HandleFunc("POST /api/issues/{id}/relations", h.addRelation)
	mux.HandleFunc("DELETE /api/issues/{id}/relations/{type}/{other}", h.removeRelation)

	mux.HandleFunc("GET /api/issues/{id}/tree", h.tree)

	mux.HandleFunc("GET /api/ready", h.listReady)
	mux.HandleFunc("GET /api/blocked", h.listBlocked)
	mux.HandleFunc("GET /api/search", h.search)

	mux.HandleFunc("GET /api/projects", h.listProjects)
	mux.HandleFunc("POST /api/projects", h.createProject)
	mux.HandleFunc("GET /api/projects/{key}", h.getProject)
	mux.HandleFunc("PATCH /api/projects/{key}", h.patchProject)
	mux.HandleFunc("DELETE /api/projects/{key}", h.deleteProject)

	mux.HandleFunc("GET /api/labels", h.labelFacets)
	mux.HandleFunc("GET /api/assignees", h.assigneeFacets)
	mux.HandleFunc("GET /api/identity", h.identity)
}

// writeJSON sends a successful response.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}

// writeError maps a failure onto its status and the Error body of SPEC §4.6.
//
// The four kinds carry the exit-code taxonomy; a failed precondition is the one
// extra case, answered 412, which has no exit code of its own.
func writeError(w http.ResponseWriter, err error) {
	status := awberr.KindOf(err).HTTPStatus()
	if errors.Is(err, awberr.ErrPreconditionFailed) {
		status = http.StatusPreconditionFailed
	}
	if errors.Is(err, errUnsupportedMediaType) {
		status = http.StatusUnsupportedMediaType
	}
	if errors.Is(err, errBodyTooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	writeJSON(w, status, domain.APIError{Error: err.Error()})
}

// writeIssue sends one issue with the ETag for the version it describes, so a
// client can make another conditional edit without first repeating the GET
// (SPEC §6.2).
func writeIssue(w http.ResponseWriter, status int, issue *domain.Issue) {
	w.Header().Set("ETag", local.ETag(issue.UpdatedAt))
	writeJSON(w, status, issue)
}

// writeProject is writeIssue for a project, whose tag is built from its own
// updated_at: active_issues moving because somebody created or closed an issue
// does not invalidate it, exactly as a new relation does not invalidate an
// issue's.
func writeProject(w http.ResponseWriter, status int, project *domain.Project) {
	w.Header().Set("ETag", local.ETag(project.UpdatedAt))
	writeJSON(w, status, project)
}

// writeList sends an array with the unpaged total, so a UI can show
// "1–50 of 214" and page through without loading everything.
func writeList(w http.ResponseWriter, value any, total int) {
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, value)
}

// ifMatch is the optional conditional-edit precondition. A caller that omits
// it, as the CLI always does, gets last-write-wins.
func ifMatch(r *http.Request) string { return r.Header.Get("If-Match") }
