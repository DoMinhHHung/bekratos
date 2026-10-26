package org

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
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
	mux.Handle("POST /v1/organizations", authMW.RequirePrincipal(http.HandlerFunc(h.createOrg)))
	mux.Handle("GET /v1/organizations", authMW.RequirePrincipal(http.HandlerFunc(h.listOrgs)))
	mux.Handle("GET /v1/organizations/{orgID}", authMW.RequirePrincipal(http.HandlerFunc(h.getOrg)))
	mux.Handle("POST /v1/organizations/{orgID}/archive", authMW.RequirePrincipal(http.HandlerFunc(h.archiveOrg)))
	mux.Handle("GET /v1/organizations/{orgID}/members", authMW.RequirePrincipal(http.HandlerFunc(h.listMembers)))
	mux.Handle("PATCH /v1/organizations/{orgID}/members/{userID}", authMW.RequirePrincipal(http.HandlerFunc(h.changeRole)))
	mux.Handle("DELETE /v1/organizations/{orgID}/members/{userID}", authMW.RequirePrincipal(http.HandlerFunc(h.removeMember)))
}

type createOrgRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type orgResponse struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	DisplayName string  `json:"display_name"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	ArchivedAt  *string `json:"archived_at,omitempty"`
	Role        string  `json:"role,omitempty"`
}

type memberResponse struct {
	IdentityUserID string `json:"identity_user_id"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type changeRoleRequest struct {
	Role string `json:"role"`
}

func (h *Handlers) createOrg(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	var req createOrgRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	org, err := h.svc.CreateOrganization(r.Context(), principal, CreateOrgInput{
		Slug:        req.Slug,
		DisplayName: req.DisplayName,
	}, requestid.FromContext(r.Context()))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toOrgResponse(org, RoleOwner))
}

func (h *Handlers) listOrgs(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	orgs, err := h.svc.ListOrganizations(r.Context(), principal)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]orgResponse, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, toOrgResponse(o, ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) getOrg(w http.ResponseWriter, r *http.Request) {
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
	org, membership, err := h.svc.GetOrganization(r.Context(), principal, orgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrgResponse(org, membership.Role))
}

func (h *Handlers) archiveOrg(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.ArchiveOrganization(r.Context(), principal, orgID, requestid.FromContext(r.Context())); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listMembers(w http.ResponseWriter, r *http.Request) {
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
	members, err := h.svc.ListMembers(r.Context(), principal, orgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, memberResponse{
			IdentityUserID: m.IdentityUserID.String(),
			Role:           string(m.Role),
			Status:         string(m.Status),
			CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:      m.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) changeRole(w http.ResponseWriter, r *http.Request) {
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
	userID, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req changeRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	role, ok := ParseRole(req.Role)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_role", "invalid role")
		return
	}
	if err := h.svc.ChangeMemberRole(r.Context(), principal, orgID, userID, role, requestid.FromContext(r.Context())); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) removeMember(w http.ResponseWriter, r *http.Request) {
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
	userID, err := uuid.Parse(r.PathValue("userID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := h.svc.RemoveMember(r.Context(), principal, orgID, userID, requestid.FromContext(r.Context())); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toOrgResponse(o Organization, role Role) orgResponse {
	resp := orgResponse{
		ID:          o.ID.String(),
		Slug:        o.Slug,
		DisplayName: o.DisplayName,
		Status:      string(o.Status),
		CreatedAt:   o.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:   o.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if o.ArchivedAt != nil {
		s := o.ArchivedAt.UTC().Format(time.RFC3339Nano)
		resp.ArchivedAt = &s
	}
	if role != "" {
		resp.Role = string(role)
	}
	return resp
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
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
	case errors.Is(err, ErrLastOwner):
		writeErr(w, http.StatusConflict, "last_owner", "cannot remove or demote the last owner")
	case errors.Is(err, ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
