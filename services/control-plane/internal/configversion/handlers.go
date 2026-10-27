package configversion

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
	base := "/v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/configuration-versions"
	mux.Handle("POST "+base, authMW.RequirePrincipal(http.HandlerFunc(h.createCandidate)))
	mux.Handle("GET "+base, authMW.RequirePrincipal(http.HandlerFunc(h.listVersions)))
	mux.Handle("GET "+base+"/{versionID}", authMW.RequirePrincipal(http.HandlerFunc(h.getVersion)))
	mux.Handle("POST "+base+"/{versionID}/confirm", authMW.RequirePrincipal(http.HandlerFunc(h.confirm)))
	mux.Handle("GET "+base+"/{versionID}/job", authMW.RequirePrincipal(http.HandlerFunc(h.getJob)))
}

type createCandidateRequest struct {
	ExpectedActiveVersionID *string `json:"expected_active_version_id"`
	ExpectedSchemaRevision  *int64  `json:"expected_schema_revision"`
	ExpectedPinRevision     *int64  `json:"expected_pin_revision"`
	IdempotencyKey          string  `json:"idempotency_key"`
}

type confirmRequest struct {
	ExpectedManifestSHA256  string  `json:"expected_manifest_sha256"`
	ExpectedActiveVersionID *string `json:"expected_active_version_id"`
}

type versionResponse struct {
	ID                      string  `json:"id"`
	EnvironmentID           string  `json:"environment_id"`
	VersionNumber           int64   `json:"version_number"`
	ParentVersionID         *string `json:"parent_version_id,omitempty"`
	ExpectedActiveVersionID *string `json:"expected_active_version_id,omitempty"`
	SchemaRevision          int64   `json:"schema_revision"`
	PinRevision             int64   `json:"pin_revision"`
	ManifestFormatVersion   int     `json:"manifest_format_version"`
	ManifestSHA256          string  `json:"manifest_sha256"`
	Compatibility           string  `json:"compatibility"`
	State                   string  `json:"state"`
	FailureCode             string  `json:"failure_code,omitempty"`
	FailureMessage          string  `json:"failure_message,omitempty"`
	ConfirmationRequired    bool    `json:"confirmation_required"`
	ConfirmedAt             string  `json:"confirmed_at,omitempty"`
	ActivatedAt             string  `json:"activated_at,omitempty"`
	CreatedAt               string  `json:"created_at"`
	UpdatedAt               string  `json:"updated_at"`
}

type jobResponse struct {
	ID                     string `json:"id"`
	EnvironmentID          string `json:"environment_id"`
	ConfigurationVersionID string `json:"configuration_version_id"`
	State                  string `json:"state"`
	AttemptCount           int    `json:"attempt_count"`
	MaxAttempts            int    `json:"max_attempts"`
	FencingToken           int64  `json:"fencing_token"`
	LastErrorCode          string `json:"last_error_code,omitempty"`
	LastErrorMessage       string `json:"last_error_message,omitempty"`
	ApplyRequestID         string `json:"apply_request_id"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type createCandidateResponse struct {
	Version versionResponse `json:"version"`
	Job     *jobResponse    `json:"job,omitempty"`
	Reused  bool            `json:"reused"`
}

func (h *Handlers) createCandidate(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	var req createCandidateRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.ExpectedSchemaRevision == nil || req.ExpectedPinRevision == nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_schema_revision and expected_pin_revision required")
		return
	}
	if *req.ExpectedSchemaRevision < 0 || *req.ExpectedPinRevision < 0 {
		writeErr(w, http.StatusBadRequest, "invalid_input", "revisions must be non-negative")
		return
	}
	if req.IdempotencyKey == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "idempotency_key required")
		return
	}
	var expectedActive *uuid.UUID
	if req.ExpectedActiveVersionID != nil && *req.ExpectedActiveVersionID != "" {
		id, err := uuid.Parse(*req.ExpectedActiveVersionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_input", "invalid expected_active_version_id")
			return
		}
		expectedActive = &id
	}
	result, err := h.svc.CreateCandidate(r.Context(), principal, orgID, projectID, envID, CreateCandidateInput{
		ExpectedActiveVersionID: expectedActive,
		ExpectedSchemaRevision:  *req.ExpectedSchemaRevision,
		ExpectedPinRevision:     *req.ExpectedPinRevision,
		IdempotencyKey:          req.IdempotencyKey,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	resp := createCandidateResponse{
		Version: toVersionResponse(result.Version),
		Reused:  result.Reused,
	}
	if result.Job != nil {
		j := toJobResponse(*result.Job)
		resp.Job = &j
	}
	status := http.StatusCreated
	if result.Reused {
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
}

func (h *Handlers) listVersions(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	list, err := h.svc.ListVersions(r.Context(), principal, orgID, projectID, envID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]versionResponse, 0, len(list))
	for _, v := range list {
		out = append(out, toVersionResponse(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handlers) getVersion(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	versionID, err := uuid.Parse(r.PathValue("versionID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	v, err := h.svc.GetVersion(r.Context(), principal, orgID, projectID, envID, versionID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVersionResponse(v))
}

func (h *Handlers) confirm(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	versionID, err := uuid.Parse(r.PathValue("versionID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req confirmRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if req.ExpectedManifestSHA256 == "" {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_manifest_sha256 required")
		return
	}
	var expectedActive *uuid.UUID
	if req.ExpectedActiveVersionID != nil && *req.ExpectedActiveVersionID != "" {
		id, err := uuid.Parse(*req.ExpectedActiveVersionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_input", "invalid expected_active_version_id")
			return
		}
		expectedActive = &id
	}
	v, err := h.svc.Confirm(r.Context(), principal, orgID, projectID, envID, ConfirmInput{
		VersionID:               versionID,
		ExpectedManifestSHA256:  req.ExpectedManifestSHA256,
		ExpectedActiveVersionID: expectedActive,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVersionResponse(v))
}

func (h *Handlers) getJob(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	versionID, err := uuid.Parse(r.PathValue("versionID"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	j, err := h.svc.GetJob(r.Context(), principal, orgID, projectID, envID, versionID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toJobResponse(j))
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

func toVersionResponse(v Version) versionResponse {
	resp := versionResponse{
		ID:                    v.ID.String(),
		EnvironmentID:         v.EnvironmentID.String(),
		VersionNumber:         v.VersionNumber,
		SchemaRevision:        v.SchemaRevision,
		PinRevision:           v.PinRevision,
		ManifestFormatVersion: v.ManifestFormatVersion,
		ManifestSHA256:        v.ManifestSHA256,
		Compatibility:         string(v.Compatibility),
		State:                 string(v.State),
		FailureCode:           v.FailureCode,
		FailureMessage:        v.FailureMessage,
		ConfirmationRequired:  v.ConfirmationRequired,
		CreatedAt:             v.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             v.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if v.ParentVersionID != nil {
		s := v.ParentVersionID.String()
		resp.ParentVersionID = &s
	}
	if v.ExpectedActiveVersionID != nil {
		s := v.ExpectedActiveVersionID.String()
		resp.ExpectedActiveVersionID = &s
	}
	if v.ConfirmedAt != nil {
		resp.ConfirmedAt = v.ConfirmedAt.UTC().Format(time.RFC3339Nano)
	}
	if v.ActivatedAt != nil {
		resp.ActivatedAt = v.ActivatedAt.UTC().Format(time.RFC3339Nano)
	}
	return resp
}

func toJobResponse(j ApplyJob) jobResponse {
	return jobResponse{
		ID:                     j.ID.String(),
		EnvironmentID:          j.EnvironmentID.String(),
		ConfigurationVersionID: j.ConfigurationVersionID.String(),
		State:                  string(j.State),
		AttemptCount:           j.AttemptCount,
		MaxAttempts:            j.MaxAttempts,
		FencingToken:           j.FencingToken,
		LastErrorCode:          j.LastErrorCode,
		LastErrorMessage:       j.LastErrorMessage,
		ApplyRequestID:         j.ApplyRequestID,
		CreatedAt:              j.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:              j.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
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
	case errors.Is(err, ErrInactive):
		writeErr(w, http.StatusForbidden, "forbidden", "inactive resource")
	case errors.Is(err, ErrConflictRevision), errors.Is(err, ErrConflict), errors.Is(err, ErrConflictIdempotency):
		writeErr(w, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, ErrConfirmationMismatch), errors.Is(err, ErrConfirmationRequired):
		writeErr(w, http.StatusConflict, "conflict", "confirmation mismatch")
	case errors.Is(err, ErrInvalidTransition), errors.Is(err, ErrNotEligible):
		writeErr(w, http.StatusConflict, "conflict", "invalid transition")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrValidation):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	case errors.Is(err, ErrRuntimeUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime unavailable")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
