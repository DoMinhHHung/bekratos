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

func TestHTTPProviderRejectsUnsafeRuntimeLocations(t *testing.T) {
	cases := []struct {
		name string
		url  string
		host string
	}{
		{name: "javascript scheme", url: "javascript:alert(1)"},
		{name: "userinfo", url: "https://user:password@runtime.example/app"},
		{name: "query", url: "https://runtime.example/app?token=secret"},
		{name: "fragment", url: "https://runtime.example/app#fragment"},
		{name: "host mismatch", url: "https://runtime.example/app", host: "attacker.example"},
		{name: "invalid host", host: "runtime.example/path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deploymentID := uuid.New()
			environmentID := uuid.New()
			versionID := uuid.New()
			requestID := "runtime-location-test"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(runtimeProvisionResponse{
					Outcome:              OutcomeSuccess,
					RequestID:            requestID,
					DeploymentID:         deploymentID,
					EnvironmentID:        environmentID,
					VersionID:            versionID,
					ReadinessProbePassed: true,
					ReadinessProbeID:     "probe-123",
					ReadinessObservedAt:  time.Now().UTC(),
					URL:                  tc.url,
					Host:                 tc.host,
				})
			}))
			defer server.Close()
			provider := NewHTTPProvider(server.URL, "runtime-secret", time.Second)
			result, err := provider.Provision(context.Background(), ProvisionRequest{
				DeploymentID:           deploymentID,
				EnvironmentID:          environmentID,
				ConfigurationVersionID: versionID,
				ProviderRequestID:      requestID,
				TargetMode:             TargetModeShared,
			})
			if err != nil {
				t.Fatalf("Provision returned error: %v", err)
			}
			if result.Outcome != OutcomeAmbiguous || result.ReadinessProbePassed {
				t.Fatalf("unsafe runtime location was not rejected: %#v", result)
			}
		})
	}
}

func TestHTTPProviderAcceptsSafeRuntimeLocation(t *testing.T) {
	deploymentID := uuid.New()
	environmentID := uuid.New()
	versionID := uuid.New()
	requestID := "runtime-location-safe"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(runtimeProvisionResponse{
			Outcome:              OutcomeSuccess,
			RequestID:            requestID,
			DeploymentID:         deploymentID,
			EnvironmentID:        environmentID,
			VersionID:            versionID,
			ReadinessProbePassed: true,
			ReadinessProbeID:     "probe-123",
			ReadinessObservedAt:  time.Now().UTC(),
			URL:                  "https://runtime.example/app",
			Host:                 "runtime.example",
			Region:               "ap-southeast-1",
		})
	}))
	defer server.Close()
	provider := NewHTTPProvider(server.URL, "runtime-secret", time.Second)
	result, err := provider.Provision(context.Background(), ProvisionRequest{
		DeploymentID:           deploymentID,
		EnvironmentID:          environmentID,
		ConfigurationVersionID: versionID,
		ProviderRequestID:      requestID,
		TargetMode:             TargetModeShared,
	})
	if err != nil {
		t.Fatalf("Provision returned error: %v", err)
	}
	if result.Outcome != OutcomeSuccess {
		t.Fatalf("outcome = %q, want success", result.Outcome)
	}
}

func TestHTTPProviderReconcilesByIdempotencyKey(t *testing.T) {
	deploymentID := uuid.New()
	environmentID := uuid.New()
	versionID := uuid.New()
	requestID := "reconcile-idempotency-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/deployments/by-idempotency-key/"+requestID {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Error("missing Runtime service authorization")
		}
		_ = json.NewEncoder(w).Encode(runtimeProvisionResponse{
			Outcome:              OutcomeSuccess,
			RequestID:            requestID,
			DeploymentID:         deploymentID,
			EnvironmentID:        environmentID,
			VersionID:            versionID,
			ReadinessProbePassed: true,
			ReadinessProbeID:     "probe-reconcile",
			ReadinessObservedAt:  time.Now().UTC(),
		})
	}))
	defer server.Close()
	provider := NewHTTPProvider(server.URL, "runtime-secret", time.Second)
	result, err := provider.Reconcile(context.Background(), ProvisionRequest{
		DeploymentID:           deploymentID,
		EnvironmentID:          environmentID,
		ConfigurationVersionID: versionID,
		ProviderRequestID:      requestID,
		TargetMode:             TargetModeShared,
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Outcome != OutcomeSuccess || !result.ReadinessProbePassed || result.RequestID != requestID {
		t.Fatalf("unexpected reconciliation result: %#v", result)
	}
}

func TestHTTPProviderOnlyRetriesWhenRuntimeConfirmsSafeNotFound(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    Outcome
		wantErr bool
	}{
		{name: "confirmed safe not found", body: `{"code":"deployment_not_found","safe_to_retry":true}`, want: OutcomeRetryable},
		{name: "unknown 404", body: `{"message":"not found"}`, want: OutcomeAmbiguous, wantErr: true},
		{name: "unsafe not found", body: `{"code":"deployment_not_found","safe_to_retry":false}`, want: OutcomeAmbiguous, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			provider := NewHTTPProvider(server.URL, "runtime-secret", time.Second)
			result, err := provider.Reconcile(context.Background(), ProvisionRequest{
				DeploymentID:           uuid.New(),
				EnvironmentID:          uuid.New(),
				ConfigurationVersionID: uuid.New(),
				ProviderRequestID:      "reconcile-not-found",
				TargetMode:             TargetModeShared,
			})
			if tc.wantErr && err == nil {
				t.Fatal("expected ambiguous reconciliation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q", result.Outcome, tc.want)
			}
		})
	}
}
