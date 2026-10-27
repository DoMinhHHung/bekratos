package apikey

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/httpjson"
	"github.com/google/uuid"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

func (h *Handlers) Register(mux *http.ServeMux, authMW *auth.Middleware) {
	base := "/v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/api-keys"
	mux.Handle("GET "+base, authMW.RequirePrincipal(http.HandlerFunc(h.listKeys)))
	mux.Handle("POST "+base, authMW.RequirePrincipal(http.HandlerFunc(h.createKey)))
	mux.Handle("GET "+base+"/{keyID}", authMW.RequirePrincipal(http.HandlerFunc(h.getKey)))
	mux.Handle("POST "+base+"/{keyID}/revoke", authMW.RequirePrincipal(http.HandlerFunc(h.revokeKey)))
	mux.Handle("POST "+base+"/{keyID}/rotate", authMW.RequirePrincipal(http.HandlerFunc(h.rotateKey)))
}

type createKeyRequest struct {
	KeyType   string   `json:"key_type"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt *string  `json:"expires_at"`
}

type keyResponse struct {
	ID               string   `json:"id"`
	EnvironmentID    string   `json:"environment_id"`
	KeyType          string   `json:"key_type"`
	Name             string   `json:"name"`
	Prefix           string   `json:"prefix"`
	Scopes           []string `json:"scopes"`
	Status           string   `json:"status"`
	CreatedByUserID  string   `json:"created_by_user_id"`
	CreatedAt        string   `json:"created_at"`
	LastUsedAt       string   `json:"last_used_at,omitempty"`
	ExpiresAt        string   `json:"expires_at,omitempty"`
	RevokedAt        string   `json:"revoked_at,omitempty"`
	RotationGroupID  string   `json:"rotation_group_id"`
	RotatedFromKeyID string   `json:"rotated_from_key_id,omitempty"`
}

type issuedKeyResponse struct {
	Key     keyResponse `json:"key"`
	FullKey string      `json:"full_key"`
}

func (h *Handlers) createKey(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	var req createKeyRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	in, err := parseCreateInput(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
		return
	}
	issued, err := h.svc.Create(r.Context(), principal, orgID, projectID, envID, in)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, issuedKeyResponse{
		Key:     toKeyResponse(issued.Record),
		FullKey: issued.FullKey,
	})
}

func (h *Handlers) listKeys(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), principal, orgID, projectID, envID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]keyResponse, 0, len(list))
	for _, k := range list {
		out = append(out, toKeyResponse(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) getKey(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	rec, err := h.svc.Get(r.Context(), principal, orgID, projectID, envID, keyID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toKeyResponse(rec))
}

func (h *Handlers) revokeKey(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	rec, err := h.svc.Revoke(r.Context(), principal, orgID, projectID, envID, keyID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toKeyResponse(rec))
}

func (h *Handlers) rotateKey(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	issued, err := h.svc.Rotate(r.Context(), principal, orgID, projectID, envID, keyID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, issuedKeyResponse{
		Key:     toKeyResponse(issued.Record),
		FullKey: issued.FullKey,
	})
}

func parseCreateInput(req createKeyRequest) (CreateInput, error) {
	var expires *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			return CreateInput{}, ErrInvalidInput
		}
		utc := t.UTC()
		expires = &utc
	}
	return CreateInput{
		KeyType:   KeyType(req.KeyType),
		Name:      req.Name,
		Scopes:    req.Scopes,
		ExpiresAt: expires,
	}, nil
}

func (h *Handlers) parsePath(w http.ResponseWriter, r *http.Request) (auth.Principal, uuid.UUID, uuid.UUID, uuid.UUID, bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return auth.Principal{}, uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	orgID, err := uuid.Parse(r.PathValue("orgID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return auth.Principal{}, uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return auth.Principal{}, uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return auth.Principal{}, uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return principal, orgID, projectID, envID, true
}

func toKeyResponse(k KeyRecord) keyResponse {
	now := time.Now().UTC()
	resp := keyResponse{
		ID:              k.ID.String(),
		EnvironmentID:   k.EnvironmentID.String(),
		KeyType:         string(k.KeyType),
		Name:            k.Name,
		Prefix:          k.KeyPrefix,
		Scopes:          append([]string(nil), k.Scopes...),
		Status:          string(k.Status(now)),
		CreatedByUserID: k.CreatedByUserID.String(),
		CreatedAt:       k.CreatedAt.UTC().Format(time.RFC3339Nano),
		RotationGroupID: k.RotationGroupID.String(),
	}
	if k.LastUsedAt != nil {
		resp.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339Nano)
	}
	if k.ExpiresAt != nil {
		resp.ExpiresAt = k.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if k.RevokedAt != nil {
		resp.RevokedAt = k.RevokedAt.UTC().Format(time.RFC3339Nano)
	}
	if k.RotatedFromKeyID != nil {
		resp.RotatedFromKeyID = k.RotatedFromKeyID.String()
	}
	return resp
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
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrInactive):
		writeErr(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, ErrRevoked):
		writeErr(w, http.StatusConflict, "revoked", "key revoked")
	case errors.Is(err, ErrExpired):
		writeErr(w, http.StatusConflict, "expired", "key expired")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrInvalidSecret):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
