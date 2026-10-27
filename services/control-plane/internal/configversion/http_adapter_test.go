package configversion

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPApplyAdapterSendsCanonicalManifestAndIdempotency(t *testing.T) {
	envID := uuid.New()
	versionID := uuid.New()
	baseID := uuid.New()
	applyID := "apply-" + uuid.NewString()
	hash := "9d2d2b7e8c53c83b8fb9fe3d4b3d4e2b"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/internal/configuration/apply" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-runtime-token-0123456789abcdef" {
			t.Fatal("runtime authorization header missing or incorrect")
		}
		if r.Header.Get("Idempotency-Key") != applyID {
			t.Fatal("apply request ID was not used for idempotency")
		}
		var request struct {
			ContractVersion string          `json:"contract_version"`
			EnvironmentID uuid.UUID         `json:"environment_id"`
			VersionID uuid.UUID             `json:"configuration_version_id"`
			ManifestHash string              `json:"manifest_sha256"`
			Manifest json.RawMessage         `json:"manifest"`
			ApplyRequestID string             `json:"apply_request_id"`
			ExpectedActiveVersionID *uuid.UUID `json:"expected_active_version_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.ContractVersion != "v1" ||
			request.EnvironmentID != envID ||
			request.VersionID != versionID ||
			request.ManifestHash != hash ||
			request.ApplyRequestID != applyID ||
			request.ExpectedActiveVersionID == nil ||
			*request.ExpectedActiveVersionID != baseID ||
			string(request.Manifest) != `{"entities":[]}` {
			t.Fatalf("request mismatch: %+v manifest=%s", request, request.Manifest)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
			"environment_id": envID,
			"applied_configuration_version_id": versionID,
			"applied_sha256": hash,
			"expected_active_version_id": baseID,
		})
	}))
	defer server.Close()

	adapter := NewHTTPApplyAdapter(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	result, err := adapter.Apply(t.Context(), ApplyRequest{
		EnvironmentID: envID,
		VersionID: versionID,
		ManifestFormatVersion: ManifestFormatVersion,
		ManifestSHA256: hash,
		ManifestCanonical: `{"entities":[]}`,
		ApplyRequestID: applyID,
		ExpectedActiveVersionID: &baseID,
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if !result.Accepted ||
		result.EnvironmentID != envID ||
		result.AppliedVersion != versionID ||
		result.AppliedSHA256 != hash ||
		result.ExpectedActiveVersionID == nil ||
		*result.ExpectedActiveVersionID != baseID {
		t.Fatalf("unexpected apply result: %+v", result)
	}
}

func TestHTTPApplyAdapterClassifiesServerErrorsAsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal runtime diagnostic", http.StatusBadGateway)
	}))
	defer server.Close()

	adapter := NewHTTPApplyAdapter(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	_, err := adapter.Apply(t.Context(), ApplyRequest{
		EnvironmentID: uuid.New(),
		VersionID: uuid.New(),
		ManifestCanonical: `{}`,
		ApplyRequestID: "apply-ambiguous",
	})
	var ambiguous AmbiguousApplyError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("expected AmbiguousApplyError, got %v", err)
	}
}

func TestHTTPApplyAdapterClassifiesPreAcceptanceRateLimitAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	adapter := NewHTTPApplyAdapter(server.URL, "synthetic-runtime-token-0123456789abcdef", time.Second)
	_, err := adapter.Apply(t.Context(), ApplyRequest{
		EnvironmentID: uuid.New(),
		VersionID: uuid.New(),
		ManifestCanonical: `{}`,
		ApplyRequestID: "apply-rate-limited",
	})
	var retryable RetryableApplyError
	if !errors.As(err, &retryable) {
		t.Fatalf("expected RetryableApplyError, got %v", err)
	}
}
