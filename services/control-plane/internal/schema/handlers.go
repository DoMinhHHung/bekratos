package schema

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	base := "/v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/schema"
	mux.Handle("GET "+base, authMW.RequirePrincipal(http.HandlerFunc(h.getSchema)))
	mux.Handle("PUT "+base, authMW.RequirePrincipal(http.HandlerFunc(h.putSchema)))
	mux.Handle("PUT "+base+"/entities/{entityKey}", authMW.RequirePrincipal(http.HandlerFunc(h.upsertEntity)))
	mux.Handle("DELETE "+base+"/entities/{entityKey}", authMW.RequirePrincipal(http.HandlerFunc(h.deleteEntity)))
	mux.Handle("PUT "+base+"/entities/{entityKey}/fields/{fieldKey}", authMW.RequirePrincipal(http.HandlerFunc(h.upsertField)))
	mux.Handle("DELETE "+base+"/entities/{entityKey}/fields/{fieldKey}", authMW.RequirePrincipal(http.HandlerFunc(h.deleteField)))
	mux.Handle("PUT "+base+"/entities/{entityKey}/relationships/{relKey}", authMW.RequirePrincipal(http.HandlerFunc(h.upsertRelationship)))
	mux.Handle("DELETE "+base+"/entities/{entityKey}/relationships/{relKey}", authMW.RequirePrincipal(http.HandlerFunc(h.deleteRelationship)))
}

type putSchemaRequest struct {
	ExpectedRevision *int64         `json:"expected_revision"`
	Document         SchemaDocument `json:"document"`
}

type entityRequest struct {
	ExpectedRevision *int64 `json:"expected_revision"`
	DisplayName      string `json:"display_name"`
}

type fieldRequest struct {
	ExpectedRevision *int64            `json:"expected_revision"`
	DisplayName      string            `json:"display_name"`
	Type             string            `json:"type"`
	Required         bool              `json:"required"`
	Nullable         bool              `json:"nullable"`
	Default          json.RawMessage   `json:"default"`
	Constraints      *FieldConstraints `json:"constraints"`
}

type relationshipRequest struct {
	ExpectedRevision *int64 `json:"expected_revision"`
	TargetEntity     string `json:"target_entity"`
	Cardinality      string `json:"cardinality"`
	Required         bool   `json:"required"`
	DeleteBehavior   string `json:"delete_behavior"`
}

type draftResponse struct {
	EnvironmentID string         `json:"environment_id"`
	Revision      int64          `json:"revision"`
	Document      SchemaDocument `json:"document"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
}

func requireExpectedRevision(ptr *int64) (int64, error) {
	if ptr == nil {
		return 0, ErrInvalidInput
	}
	if *ptr < 0 {
		return 0, ErrInvalidInput
	}
	return *ptr, nil
}

func (h *Handlers) getSchema(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	d, err := h.svc.GetSchema(r.Context(), principal, orgID, projectID, envID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) putSchema(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	var req putSchemaRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	rev, err := requireExpectedRevision(req.ExpectedRevision)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.PutSchema(r.Context(), principal, orgID, projectID, envID, PutSchemaInput{
		ExpectedRevision: rev,
		Document:         req.Document,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) upsertEntity(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	entityKey := r.PathValue("entityKey")
	var req entityRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	rev, err := requireExpectedRevision(req.ExpectedRevision)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.UpsertCustomEntity(r.Context(), principal, orgID, projectID, envID, rev, EntityMutationInput{
		Key: entityKey, DisplayName: req.DisplayName,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) deleteEntity(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	entityKey := r.PathValue("entityKey")
	rev, err := parseExpectedRevision(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.DeleteCustomEntity(r.Context(), principal, orgID, projectID, envID, rev, entityKey)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) upsertField(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	entityKey := r.PathValue("entityKey")
	fieldKey := r.PathValue("fieldKey")
	var req fieldRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	rev, err := requireExpectedRevision(req.ExpectedRevision)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.UpsertCustomField(r.Context(), principal, orgID, projectID, envID, rev, entityKey, FieldMutationInput{
		Key: fieldKey, DisplayName: req.DisplayName, Type: ScalarType(req.Type),
		Required: req.Required, Nullable: req.Nullable, Default: req.Default, Constraints: req.Constraints,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) deleteField(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	entityKey := r.PathValue("entityKey")
	fieldKey := r.PathValue("fieldKey")
	rev, err := parseExpectedRevision(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.DeleteCustomField(r.Context(), principal, orgID, projectID, envID, rev, entityKey, fieldKey)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) upsertRelationship(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	entityKey := r.PathValue("entityKey")
	relKey := r.PathValue("relKey")
	var req relationshipRequest
	if err := httpjson.Decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	rev, err := requireExpectedRevision(req.ExpectedRevision)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.UpsertCustomRelationship(r.Context(), principal, orgID, projectID, envID, rev, entityKey, RelationshipMutationInput{
		Key: relKey, SourceEntity: entityKey, TargetEntity: req.TargetEntity,
		Cardinality: Cardinality(req.Cardinality), Required: req.Required,
		DeleteBehavior: DeleteBehavior(req.DeleteBehavior),
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
}

func (h *Handlers) deleteRelationship(w http.ResponseWriter, r *http.Request) {
	principal, orgID, projectID, envID, ok := h.parsePath(w, r)
	if !ok {
		return
	}
	entityKey := r.PathValue("entityKey")
	relKey := r.PathValue("relKey")
	rev, err := parseExpectedRevision(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_input", "expected_revision required")
		return
	}
	d, err := h.svc.DeleteCustomRelationship(r.Context(), principal, orgID, projectID, envID, rev, entityKey, relKey)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDraftResponse(d))
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

func parseExpectedRevision(r *http.Request) (int64, error) {
	q := r.URL.Query().Get("expected_revision")
	if q == "" {
		return 0, ErrInvalidInput
	}
	v, err := strconv.ParseInt(q, 10, 64)
	if err != nil || v < 0 {
		return 0, ErrInvalidInput
	}
	return v, nil
}

func toDraftResponse(d Draft) draftResponse {
	resp := draftResponse{
		EnvironmentID: d.EnvironmentID.String(),
		Revision:      d.Revision,
		Document:      d.Document,
	}
	if !d.CreatedAt.IsZero() {
		resp.CreatedAt = d.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !d.UpdatedAt.IsZero() {
		resp.UpdatedAt = d.UpdatedAt.UTC().Format(time.RFC3339Nano)
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
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrProtected):
		writeErr(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrConflictRevision):
		writeErr(w, http.StatusConflict, "conflict", "revision conflict")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrValidation),
		errors.Is(err, ErrDuplicateKey), errors.Is(err, ErrReservedKey),
		errors.Is(err, ErrUnknownEntity), errors.Is(err, ErrUnknownField),
		errors.Is(err, ErrInvalidRelation), errors.Is(err, ErrInvalidPolicy):
		writeErr(w, http.StatusBadRequest, "invalid_input", "invalid input")
	case errors.Is(err, ErrInactive):
		writeErr(w, http.StatusForbidden, "forbidden", "inactive resource")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
