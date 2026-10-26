package project

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/google/uuid"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux, authMW *auth.Middleware) {
	mux.Handle("POST /v1/organizations/{orgID}/projects", authMW.RequirePrincipal(http.HandlerFunc(h.createProject)))
	mux.Handle("GET /v1/organizations/{orgID}/projects", authMW.RequirePrincipal(http.HandlerFunc(h.listProjects)))
	mux.Handle("GET /v1/organizations/{orgID}/projects/{projectID}", authMW.RequirePrincipal(http.HandlerFunc(h.getProject)))
	mux.Handle("PATCH /v1/organizations/{orgID}/projects/{projectID}", authMW.RequirePrincipal(http.HandlerFunc(h.updateProject)))
	mux.Handle("POST /v1/organizations/{orgID}/projects/{projectID}/archive", authMW.RequirePrincipal(http.HandlerFunc(h.archiveProject)))
	mux.Handle("POST /v1/organizations/{orgID}/projects/{projectID}/environments", authMW.RequirePrincipal(http.HandlerFunc(h.createEnv)))
	mux.Handle("GET /v1/organizations/{orgID}/projects/{projectID}/environments", authMW.RequirePrincipal(http.HandlerFunc(h.listEnvs)))
	mux.Handle("GET /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}", authMW.RequirePrincipal(http.HandlerFunc(h.getEnv)))
	mux.Handle("POST /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/archive", authMW.RequirePrincipal(http.HandlerFunc(h.archiveEnv)))
}

type createProjectRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type updateProjectRequest struct {
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

type createEnvRequest struct {
	Slug string `json:"slug"`
	Kind string `json:"kind"`
}

type projectResponse struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organization_id"`
	Slug           string  `json:"slug"`
	DisplayName    string  `json:"display_name"`
	Description    string  `json:"description,omitempty"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ArchivedAt     *string `json:"archived_at,omitempty"`
}

type envResponse struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"project_id"`
	Slug      string  `json:"slug"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	ArchivedAt *string `json:"archived_at,omitempty"`
}

func (h *Handlers) createProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	p, env, err := h.svc.CreateProject(r.Context(), principal, orgID, CreateProjectInput{
		Slug: req.Slug, DisplayName: req.DisplayName, Description: req.Description,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"project": toProjectResponse(p),
		"environment": toEnvResponse(env),
	})
}

func (h *Handlers) listProjects(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	items, err := h.svc.ListProjects(r.Context(), principal, orgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]projectResponse, 0, len(items))
	for _, p := range items {
		out = append(out, toProjectResponse(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) getProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	p, err := h.svc.GetProject(r.Context(), principal, orgID, projectID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectResponse(p))
}

func (h *Handlers) updateProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req updateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	p, err := h.svc.UpdateProject(r.Context(), principal, orgID, projectID, UpdateProjectInput{
		DisplayName: req.DisplayName, Description: req.Description,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectResponse(p))
}

func (h *Handlers) archiveProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := h.svc.ArchiveProject(r.Context(), principal, orgID, projectID); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) createEnv(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req createEnvRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	env, err := h.svc.CreateEnvironment(r.Context(), principal, orgID, projectID, CreateEnvironmentInput{
		Slug: req.Slug, Kind: EnvKind(req.Kind),
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toEnvResponse(env))
}

func (h *Handlers) listEnvs(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	items, err := h.svc.ListEnvironments(r.Context(), principal, orgID, projectID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]envResponse, 0, len(items))
	for _, e := range items {
		out = append(out, toEnvResponse(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) getEnv(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	e, err := h.svc.GetEnvironment(r.Context(), principal, orgID, projectID, envID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toEnvResponse(e))
}

func (h *Handlers) archiveEnv(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := h.svc.ArchiveEnvironment(r.Context(), principal, orgID, projectID, envID); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toProjectResponse(p Project) projectResponse {
	resp := projectResponse{
		ID: p.ID.String(), OrganizationID: p.OrganizationID.String(),
		Slug: p.Slug, DisplayName: p.DisplayName, Description: p.Description,
		Status: string(p.Status),
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if p.ArchivedAt != nil {
		s := p.ArchivedAt.UTC().Format(time.RFC3339Nano)
		resp.ArchivedAt = &s
	}
	return resp
}

func toEnvResponse(e Environment) envResponse {
	resp := envResponse{
		ID: e.ID.String(), ProjectID: e.ProjectID.String(),
		Slug: e.Slug, Kind: string(e.Kind), Status: string(e.Status),
		CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: e.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if e.ArchivedAt != nil {
		s := e.ArchivedAt.UTC().Format(time.RFC3339Nano)
		resp.ArchivedAt = &s
	}
	return resp
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
