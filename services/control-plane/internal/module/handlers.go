package module

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/httpjson"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/requestid"
	"github.com/google/uuid"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux, authMW *auth.Middleware) {
	mux.Handle("GET /v1/modules", authMW.RequirePrincipal(http.HandlerFunc(h.listModules)))
	mux.Handle("GET /v1/modules/{slug}/releases", authMW.RequirePrincipal(http.HandlerFunc(h.listReleases)))
	mux.Handle("POST /v1/modules/resolve", authMW.RequirePrincipal(http.HandlerFunc(h.resolve)))
	mux.Handle("GET /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/modules", authMW.RequirePrincipal(http.HandlerFunc(h.listPins)))
	mux.Handle("PUT /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/modules", authMW.RequirePrincipal(http.HandlerFunc(h.setPins)))
}

func (h *Handlers) listModules(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListModules(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, m := range items {
		out = append(out, map[string]any{
			"slug": m.Slug, "display_name": m.DisplayName, "description": m.Description,
			"created_at": m.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) listReleases(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	items, err := h.svc.ListReleases(r.Context(), slug)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, rel := range items {
		deps := make([]map[string]any, 0, len(rel.Dependencies))
		for _, d := range rel.Dependencies {
			deps = append(deps, map[string]any{
				"module_slug": d.ModuleSlug, "version_constraint": d.VersionConstraint, "required": d.Required,
			})
		}
		item := map[string]any{
			"id": rel.ID.String(), "module_slug": rel.ModuleSlug, "version": rel.Version,
			"runtime_compat": rel.RuntimeCompat, "api_contract_version": rel.APIContractVersion,
			"dependencies": deps, "conflicts": rel.Conflicts,
			"published_at": publishedAtJSON(rel.PublishedAt),
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

type resolveRequest struct {
	Selections []struct {
		ModuleSlug string `json:"module_slug"`
		Version    string `json:"version"`
	} `json:"selections"`
	ExpectedRevision *int64 `json:"expected_revision"`
}

func (h *Handlers) resolve(w http.ResponseWriter, r *http.Request) {
	var req resolveRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	sels := make([]Selection, 0, len(req.Selections))
	for _, s := range req.Selections {
		sels = append(sels, Selection{ModuleSlug: s.ModuleSlug, Version: s.Version})
	}
	plan, err := h.svc.ResolveSelections(r.Context(), sels)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h *Handlers) listPins(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, projectID, envID, okIDs := parseScope(r)
	if !okIDs {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	pins, rev, err := h.svc.ListPins(r.Context(), principal, orgID, projectID, envID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(pins))
	for _, p := range pins {
		out = append(out, map[string]any{
			"module_slug": p.ModuleSlug, "version": p.Version, "release_id": p.ReleaseID.String(),
			"pinned_at": p.PinnedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "revision": rev})
}

func (h *Handlers) setPins(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgID, projectID, envID, okIDs := parseScope(r)
	if !okIDs {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req resolveRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	sels := make([]Selection, 0, len(req.Selections))
	for _, s := range req.Selections {
		sels = append(sels, Selection{ModuleSlug: s.ModuleSlug, Version: s.Version})
	}
	plan, err := h.svc.SetPins(r.Context(), principal, orgID, projectID, envID, sels, req.ExpectedRevision, requestid.FromContext(r.Context()))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func parseScope(r *http.Request) (uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	orgID, err1 := uuid.Parse(r.PathValue("orgID"))
	projectID, err2 := uuid.Parse(r.PathValue("projectID"))
	envID, err3 := uuid.Parse(r.PathValue("envID"))
	return orgID, projectID, envID, err1 == nil && err2 == nil && err3 == nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func publishedAtJSON(publishedAt *time.Time) *string {
	if publishedAt == nil || publishedAt.IsZero() {
		return nil
	}
	formatted := publishedAt.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	case errors.Is(err, ErrInactive):
		writeErr(w, http.StatusConflict, "inactive", "project or environment is not active")
	case errors.Is(err, ErrConflictState), errors.Is(err, ErrConflictRevision):
		writeErr(w, http.StatusConflict, "revision_conflict", "pin revision conflict")
	case errors.Is(err, ErrCycle):
		writeErr(w, http.StatusConflict, "dependency_cycle", "dependency cycle")
	case errors.Is(err, ErrConflict):
		writeErr(w, http.StatusConflict, "module_conflict", "module conflict")
	case errors.Is(err, ErrMissingDependency), errors.Is(err, ErrUnknownModule), errors.Is(err, ErrUnknownRelease):
		writeErr(w, http.StatusBadRequest, "missing_dependency", "missing or unknown dependency")
	case errors.Is(err, ErrSearchBudget):
		writeErr(w, http.StatusConflict, "resolution_budget_exceeded", "dependency resolution limit exceeded")
	case errors.Is(err, ErrUnsatisfiable), errors.Is(err, ErrIncompatible), errors.Is(err, ErrDuplicateRelease):
		writeErr(w, http.StatusConflict, "unsatisfiable", "unsatisfiable constraint")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
