package deployment

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
	base := "/v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/deployments"
	mux.Handle("POST "+base, authMW.RequirePrincipal(http.HandlerFunc(h.create)))
	mux.Handle("GET "+base, authMW.RequirePrincipal(http.HandlerFunc(h.list)))
	mux.Handle("GET "+base+"/{deploymentID}", authMW.RequirePrincipal(http.HandlerFunc(h.get)))
}

type createRequest struct {
	ConfigurationVersionID *string `json:"configuration_version_id"`
	IdempotencyKey         string  `json:"idempotency_key"`
	TargetMode             string  `json:"target_mode"`
}

type deploymentResponse struct {
	ID                     string `json:"id"`
	EnvironmentID          string `json:"environment_id"`
	ConfigurationVersionID string `json:"configuration_version_id"`
	TargetMode             string `json:"target_mode"`
	Provider               string `json:"provider"`
	Status                 string `json:"status"`
	FailureCode            string `json:"failure_code,omitempty"`
	FailureMessage         string `json:"failure_message,omitempty"`
	AttemptCount           int    `json:"attempt_count"`
	MaxAttempts            int    `json:"max_attempts"`
	IdempotencyKey         string `json:"idempotency_key"`
	ProviderRequestID      string `json:"provider_request_id"`
	ProviderURL            string `json:"provider_url,omitempty"`
	ProviderHost           string `json:"provider_host,omitempty"`
	ProviderRegion         string `json:"provider_region,omitempty"`
	ReadinessProbeID       string `json:"readiness_probe_id,omitempty"`
	ReadinessObservedAt    string `json:"readiness_observed_at,omitempty"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
	StartedAt              string `json:"started_at,omitempty"`
	ReadyAt                string `json:"ready_at,omitempty"`
	FinishedAt             string `json:"finished_at,omitempty"`
}

type createResponse struct {
	Deployment deploymentResponse `json:"deployment"`
	Reused     bool               `json:"reused"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.IdempotencyKey == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "idempotency_key required")
		return
	}
	in := CreateInput{
		IdempotencyKey: req.IdempotencyKey,
		TargetMode:     TargetMode(req.TargetMode),
	}
	if req.ConfigurationVersionID != nil && *req.ConfigurationVersionID != "" {
		id, err := uuid.Parse(*req.ConfigurationVersionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_input", "invalid configuration_version_id")
			return
		}
		in.ConfigurationVersionID = &id
	}
	result, err := h.svc.Create(r.Context(), principal, orgID, projectID, envID, in)
	if err != nil {
		mapErr(w, err)
		return
	}
	status := http.StatusCreated
	if result.Reused {
		status = http.StatusOK
	}
	writeJSON(w, status, createResponse{
		Deployment: toResponse(result.Deployment),
		Reused:     result.Reused,
	})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), principal, orgID, projectID, envID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]deploymentResponse, 0, len(list))
	for _, d := range list {
		out = append(out, toResponse(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("deploymentID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	d, err := h.svc.Get(r.Context(), principal, orgID, projectID, envID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(d))
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

func toResponse(d Deployment) deploymentResponse {
	resp := deploymentResponse{
		ID:                     d.ID.String(),
		EnvironmentID:          d.EnvironmentID.String(),
		ConfigurationVersionID: d.ConfigurationVersionID.String(),
		TargetMode:             string(d.TargetMode),
		Provider:               string(d.Provider),
		Status:                 string(d.Status),
		FailureCode:            d.FailureCode,
		FailureMessage:         d.FailureMessage,
		AttemptCount:           d.AttemptCount,
		MaxAttempts:            d.MaxAttempts,
		IdempotencyKey:         d.IdempotencyKey,
		ProviderRequestID:      d.ProviderRequestID,
		CreatedAt:              d.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              d.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if d.ProviderURL != nil {
		resp.ProviderURL = *d.ProviderURL
	}
	if d.ProviderHost != nil {
		resp.ProviderHost = *d.ProviderHost
	}
	if d.ProviderRegion != nil {
		resp.ProviderRegion = *d.ProviderRegion
	}
	if d.ReadinessProbeID != nil {
		resp.ReadinessProbeID = *d.ReadinessProbeID
	}
	if d.ReadinessObservedAt != nil {
		resp.ReadinessObservedAt = d.ReadinessObservedAt.UTC().Format(time.RFC3339Nano)
	}
	if d.StartedAt != nil {
		resp.StartedAt = d.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if d.ReadyAt != nil {
		resp.ReadyAt = d.ReadyAt.UTC().Format(time.RFC3339Nano)
	}
	if d.FinishedAt != nil {
		resp.FinishedAt = d.FinishedAt.UTC().Format(time.RFC3339Nano)
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
	case errors.Is(err, ErrConflict), errors.Is(err, ErrInvalidTransition):
		writeErr(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, ErrNoActiveConfig):
		writeErr(w, http.StatusConflict, "no_active_configuration", "no active configuration version")
	case errors.Is(err, ErrConfigMismatch):
		writeErr(w, http.StatusConflict, "configuration_mismatch", "configuration version does not belong to environment")
	case errors.Is(err, ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	case errors.Is(err, ErrProviderUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "provider_unavailable", "deployment provider unavailable")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
