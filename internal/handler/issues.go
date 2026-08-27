package handler

import (
	"net/http"
	"slices"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// The request bodies of SPEC §6.4.
//
// Every one of them is decoded with unknown fields rejected, so a body carrying
// a field this API does not recognise is a 400 rather than something silently
// ignored.

// issueCreate is POST /api/issues.
//
// It carries the writable fields of an Issue plus labels and an initial
// relations array, read with the new issue as the subject. Nothing else is
// recognised: id, status, close_reason, the timestamps and the derived fields
// are rejected under the unknown-field rule rather than ignored, there being no
// object-it-read to send back here — status follows from assignee and the rest
// are the server's to assign.
type issueCreate struct {
	Project     string           `json:"project"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Type        domain.Type      `json:"type"`
	Priority    *int             `json:"priority"`
	Assignee    string           `json:"assignee"`
	Labels      []string         `json:"labels"`
	Relations   []createRelation `json:"relations"`
}

// createRelation carries no direction: it is unrecognised for the same reason
// the other server-assigned fields are.
type createRelation struct {
	Type  domain.RelationType `json:"type"`
	Other string              `json:"other"`
}

// issuePatch is PATCH /api/issues/{id}.
//
// It takes only what awb update can change. labels, status, assignee and
// close_reason may appear but may not change: each is ignored when it equals
// what is stored and rejected when it differs, because labels are mutated
// individually and the transitions are their own endpoints.
//
// The derived and immutable fields are ignored whatever they say — relations
// among them, because a relation added meanwhile does not move updated_at, so
// rejecting a stale one would fail a PATCH whose If-Match is still good.
// Together those rules let a UI send back the object it read with only the
// fields it edited changed.
type issuePatch struct {
	Title       *string      `json:"title"`
	Description *string      `json:"description"`
	Type        *domain.Type `json:"type"`
	Priority    *int         `json:"priority"`

	Labels      *[]string      `json:"labels"`
	Status      *domain.Status `json:"status"`
	Assignee    *string        `json:"assignee"`
	CloseReason *string        `json:"close_reason"`

	ID         *string            `json:"id"`
	ProjectKey *string            `json:"project"`
	CreatedAt  *string            `json:"created_at"`
	UpdatedAt  *string            `json:"updated_at"`
	Blocked    *bool              `json:"blocked"`
	Blockers   *[]string          `json:"blockers"`
	Relations  *[]domain.Relation `json:"relations"`
	Links      *[]domain.Link     `json:"links"`
}

type claimBody struct {
	Assignee       string  `json:"assignee"`
	ExpectAssignee *string `json:"expect_assignee"`
	Force          bool    `json:"force"`
}

type releaseBody struct {
	Assignee string `json:"assignee"`
	Force    bool   `json:"force"`
}

type closeBody struct {
	Reason *string `json:"reason"`
}

type labelBody struct {
	Label string `json:"label"`
}

type relationBody struct {
	Type  domain.RelationType `json:"type"`
	Other string              `json:"other"`
	Force bool                `json:"force"`
}

func (h *Handler) createIssue(w http.ResponseWriter, r *http.Request) {
	var body issueCreate
	if err := decodeBody(r, &body); err != nil {
		writeError(w, err)
		return
	}

	req := backend.IssueCreate{
		Project:     body.Project,
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		Priority:    body.Priority,
		Assignee:    body.Assignee,
		Labels:      body.Labels,
	}
	parents := 0
	for _, rel := range body.Relations {
		if rel.Type == domain.RelHasParent {
			parents++
		}
		req.Relations = append(req.Relations,
			backend.NewRelation{Type: rel.Type, Other: rel.Other})
	}
	if parents > 1 {
		writeError(w, awberr.Usagef("an issue has at most one parent"))
		return
	}

	issue, err := h.backendFor(r).CreateIssue(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}

	// The two creating endpoints answer 201 with the new object and a Location
	// header naming it (SPEC §6.1).
	w.Header().Set("Location", "/api/issues/"+issue.ID)
	writeIssue(w, http.StatusCreated, issue)
}

func (h *Handler) getIssue(w http.ResponseWriter, r *http.Request) {
	issue, err := h.backendFor(r).GetIssue(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

func (h *Handler) patchIssue(w http.ResponseWriter, r *http.Request) {
	be := h.backendFor(r)
	id := r.PathValue("id")

	var body issuePatch
	if err := decodeBody(r, &body); err != nil {
		writeError(w, err)
		return
	}

	// The fields that may appear but may not change are compared against what
	// is stored, which means reading the issue first.
	current, err := be.GetIssue(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := checkUnchangeable(&body, current); err != nil {
		writeError(w, err)
		return
	}

	issue, err := be.UpdateIssue(r.Context(), id, backend.IssuePatch{
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		Priority:    body.Priority,
	}, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

// checkUnchangeable enforces the "may appear but may not change" rule of SPEC
// §6.4. A PATCH that genuinely tries to close an issue or rewrite its labels is
// refused rather than silently dropped.
func checkUnchangeable(body *issuePatch, current *domain.Issue) error {
	if body.Status != nil && *body.Status != current.Status {
		return awberr.Usagef(
			"status cannot be changed here: use the claim, release, close or reopen endpoint")
	}
	if body.Assignee != nil && *body.Assignee != current.Assignee {
		return awberr.Usagef(
			"assignee cannot be changed here: use the claim or release endpoint")
	}
	if body.CloseReason != nil && *body.CloseReason != current.CloseReason {
		return awberr.Usagef("close_reason cannot be changed here: use the close endpoint")
	}
	if body.Labels != nil {
		// Compared as the sorted form of SPEC §4.6, which is what a client read.
		sent := slices.Clone(*body.Labels)
		slices.Sort(sent)
		if !slices.Equal(sent, current.Labels) {
			return awberr.Usagef(
				"labels cannot be changed here: add and remove them one at a time")
		}
	}
	return nil
}

func (h *Handler) deleteIssue(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.backendFor(r).DeleteIssue(r.Context(), r.PathValue("id"), ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	// A delete answers with the object as it was immediately before deletion,
	// and carries no ETag: the version it describes is gone (SPEC §6.4).
	writeJSON(w, http.StatusOK, &deleted.Issue)
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	var body claimBody
	if err := decodeOptionalBody(r, &body); err != nil {
		writeError(w, err)
		return
	}

	// assignee may be omitted, in which case the request's identity is used:
	// the authenticated username, or the server's fixed identity when it
	// authenticates nobody (SPEC §6.4).
	be := h.backendFor(r)
	if body.Assignee == "" {
		identity, err := be.Identity(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		body.Assignee = identity
	}

	issue, err := be.Claim(r.Context(), r.PathValue("id"), backend.ClaimRequest{
		Assignee:       body.Assignee,
		ExpectAssignee: body.ExpectAssignee,
		Force:          body.Force,
	}, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

func (h *Handler) release(w http.ResponseWriter, r *http.Request) {
	var body releaseBody
	if err := decodeOptionalBody(r, &body); err != nil {
		writeError(w, err)
		return
	}

	be := h.backendFor(r)
	if body.Assignee == "" && !body.Force {
		identity, err := be.Identity(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		body.Assignee = identity
	}

	issue, err := be.Release(r.Context(), r.PathValue("id"),
		backend.ReleaseRequest{Assignee: body.Assignee, Force: body.Force}, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

func (h *Handler) closeIssue(w http.ResponseWriter, r *http.Request) {
	var body closeBody
	if err := decodeOptionalBody(r, &body); err != nil {
		writeError(w, err)
		return
	}
	issue, err := h.backendFor(r).CloseIssue(r.Context(), r.PathValue("id"),
		backend.CloseRequest{Reason: body.Reason}, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

// reopen takes no body.
func (h *Handler) reopen(w http.ResponseWriter, r *http.Request) {
	issue, err := h.backendFor(r).Reopen(r.Context(), r.PathValue("id"), ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

func (h *Handler) addLabel(w http.ResponseWriter, r *http.Request) {
	var body labelBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, err)
		return
	}
	issue, err := h.backendFor(r).AddLabel(r.Context(), r.PathValue("id"), body.Label, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

// removeLabel takes the label as a query parameter rather than a path segment,
// because a label may contain a slash (SPEC §2.2, §6).
func (h *Handler) removeLabel(w http.ResponseWriter, r *http.Request) {
	label := r.URL.Query().Get("label")
	if label == "" {
		writeError(w, awberr.Usagef("the label parameter is required"))
		return
	}
	issue, err := h.backendFor(r).RemoveLabel(r.Context(), r.PathValue("id"), label, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

func (h *Handler) addRelation(w http.ResponseWriter, r *http.Request) {
	var body relationBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, err)
		return
	}
	issue, err := h.backendFor(r).AddRelation(r.Context(), r.PathValue("id"),
		backend.RelationRequest{Type: body.Type, Other: body.Other, Force: body.Force}, ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

func (h *Handler) removeRelation(w http.ResponseWriter, r *http.Request) {
	issue, err := h.backendFor(r).RemoveRelation(r.Context(), r.PathValue("id"),
		domain.RelationType(r.PathValue("type")), r.PathValue("other"), ifMatch(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeIssue(w, http.StatusOK, issue)
}

// tree returns an IssueTree whole; it is not paged, and carries no ETag,
// because an IssueTree aggregates many issues and no one version tags it
// (SPEC §6.2).
func (h *Handler) tree(w http.ResponseWriter, r *http.Request) {
	tree, err := h.backendFor(r).Tree(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}
