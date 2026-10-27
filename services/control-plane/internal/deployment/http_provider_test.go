package deployment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHTTPProviderAcceptsMatchingReadinessEvidence(t *testing.T) {
	deploymentID := uuid.New()
	environmentID := uuid.New()
	versionID := uuid.New()
	requestID := "deploy-idempotency-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/deployments" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatal("missing Runtime service authorization")
		}
		if r.Header.Get("Idempotency-Key") != requestID {
			t.Fatal("missing idempotency key")
		}
		var body runtimeProvisionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.DeploymentID != deploymentID || body.EnvironmentID != environmentID || body.ConfigurationVersionID != versionID || body.IdempotencyKey != requestID {
			t.Fatal("request identifiers or stable provider request ID did not match")
		}
		_ = json.NewEncoder(w).Encode(runtimeProvisionResponse{
			Outcome:              OutcomeSuccess,
			RequestID:            requestID,
			DeploymentID:         deploymentID,
			EnvironmentID:        environmentID,
			VersionID:            versionID,
			ReadinessProbePassed: true,
			ReadinessProbeID:     "probe-123",
			ReadinessObservedAt:  time.Now().UTC(),
		})
	}))
	defer server.Close()

	provider := NewHTTPProvider(server.URL, "runtime-secret", time.Second)
	result, err := provider.Provision(context.Background(), ProvisionRequest{
		DeploymentID:           deploymentID,
		EnvironmentID:          environmentID,
		ConfigurationVersionID: versionID,
		ProviderRequestID:      requestID,
		TargetMode:             TargetMode("shared"),
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeSuccess || !result.ReadinessProbePassed {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestHTTPProviderRejectsMismatchedVersionEvidence(t *testing.T) {
	deploymentID := uuid.New()
	environmentID := uuid.New()
	versionID := uuid.New()
	requestID := "deploy-idempotency-456"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(runtimeProvisionResponse{
			Outcome:              OutcomeSuccess,
			RequestID:            requestID,
			DeploymentID:         deploymentID,
			EnvironmentID:        environmentID,
			VersionID:            uuid.New(),
			ReadinessProbePassed: true,
			ReadinessProbeID:     "probe-456",
			ReadinessObservedAt:  time.Now().UTC(),
		})
	}))
	defer server.Close()

	provider := NewHTTPProvider(server.URL, "runtime-secret", time.Second)
	result, err := provider.Provision(context.Background(), ProvisionRequest{
		DeploymentID:           deploymentID,
		EnvironmentID:          environmentID,
		ConfigurationVersionID: versionID,
		ProviderRequestID:      requestID,
		TargetMode:             TargetMode("shared"),
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeAmbiguous || result.ReadinessProbePassed {
		t.Fatalf("mismatched evidence was not rejected: %#v", result)
	}
}

func TestHTTPProviderDoesNotFollowRedirects(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	provider := NewHTTPProvider(source.URL, "runtime-secret", time.Second)
	result, err := provider.Provision(context.Background(), ProvisionRequest{
		DeploymentID:           uuid.New(),
		EnvironmentID:          uuid.New(),
		ConfigurationVersionID: uuid.New(),
		ProviderRequestID:      "redirect-test",
		TargetMode:             TargetMode("shared"),
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if redirected {
		t.Fatal("provider followed redirect to another host")
	}
	if result.Outcome != OutcomeFailure {
		t.Fatalf("outcome = %q, want failure for redirect", result.Outcome)
	}
}
