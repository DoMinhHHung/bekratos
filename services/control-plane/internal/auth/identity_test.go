package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityClientIntrospectSendsServiceCredentialAndAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/identity/introspect" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Identity-Service-Token") != "synthetic-identity-service-token-0123456789" {
			t.Fatal("service credential missing")
		}
		var request struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.AccessToken != "signed-platform-token" {
			t.Fatal("access token was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(IdentityIntrospection{
			Active              : undefined,
			UserID              : undefined,
			SessionID           : undefined,
			Issuer              : undefined,
			Audience            : undefined,
			ExpiresAt           : undefined,
			EmailVerified       : undefined,
			VerifiedPrimaryEmail: undefined,
		})
	}))
	defer server.Close()

	client := NewIdentityClient(server.URL+"/internal/v1/identity/introspect", "synthetic-identity-service-token-0123456789", time.Second)
	result, err := client.Introspect(context.Background(), "signed-platform-token")
	if err != nil {
		t.Fatalf("Introspect returned error: %v", err)
	}
	if !result.Active || !result.EmailVerified || result.VerifiedPrimaryEmail == nil || *result.VerifiedPrimaryEmail != "user@example.test" {
		t.Fatalf("unexpected introspection response: %+v", result)
	}
}

func TestIdentityClientFailsClosedOnUnauthorizedUnavailableAndRedirect(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "http://127.0.0.1:1/steal")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			client := NewIdentityClient(server.URL, "synthetic-identity-service-token-0123456789", time.Second)
			_, err := client.Introspect(context.Background(), "signed-platform-token")
			if status == http.StatusUnauthorized {
				if !errors.Is(err, ErrIdentityUnauthenticated) {
					t.Fatalf("expected unauthenticated error, got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrIdentityUnavailable) {
				t.Fatalf("expected unavailable error, got %v", err)
			}
		})
	}
}

func ptr(value string) *string {
	return &value
}
