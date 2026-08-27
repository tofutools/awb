package handler

import (
	"net/http"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
)

// projectCreate is POST /api/projects.
type projectCreate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// projectPatch is PATCH /api/projects/{key}: the same without key, replacing
// each field it carries and leaving the others alone.
//
// key may appear but may not change, and is ignored when it equals the key in
// the path and rejected when it differs, exactly as status is on an issue.
// active_issues and the timestamps are derived and are ignored whatever they
// say, so a UI can send back the object it read.
type projectPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	Key          *string `json:"key"`
	ActiveIssues *int    `json:"active_issues"`
	CreatedAt    *string `json:"created_at"`
	UpdatedAt    *string `json:"updated_at"`
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := intParam(query, "limit")
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(query, "offset")
	if err != nil {
		writeError(w, err)
		return
	}

	page, err := h.backendFor(r).ListProjects(r.Context(), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeList(w, page.Projects, page.Total)
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	var body projectCreate
	if err := decodeBody(r, &body); err != nil {
		writeError(w, err)
		return
	}

	project, err := h.backendFor(r).CreateProject(r.Context(), backend.ProjectCreate{
		Key: body.Key, Name: body.Name, Description: body.Description,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Location", "/api/projects/"+project.Key)
	writeProject(w, http.StatusCreated, project)
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
	project, err := h.backendFor(r).GetProject(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeProject(w, http.StatusOK, project)
}

func (h *Handler) patchProject(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	var body projectPatch
	if err := decodeBody(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if body.Key != nil && *body.Key != key {
		writeError(w, awberr.Usagef("a project key is immutable"))
		return
	}

	project, err := h.backendFor(r).UpdateProject(r.Context(), key, backend.ProjectPatch{
		Name: body.Name, Description: body.Description,
	}, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeProject(w, http.StatusOK, project)
}

// deleteProject takes --cascade as a boolean query parameter. There is no force
// parameter: the HTTP method is the confirmation that --force supplies on the
// command line (SPEC §6).
func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	cascade, err := boolParam(r.URL.Query(), "cascade")
	if err != nil {
		writeError(w, err)
		return
	}

	deleted, err := h.backendFor(r).DeleteProject(r.Context(), r.PathValue("key"), cascade, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	// As with an issue, the response is the object as it was immediately before
	// deletion and carries no ETag.
	writeJSON(w, http.StatusOK, &deleted.Project)
}
