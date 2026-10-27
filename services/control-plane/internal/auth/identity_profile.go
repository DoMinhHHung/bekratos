package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const maxIdentityProfileBytes = 16 * 1024

var (
	ErrIdentityUnauthorized = errors.New("identity rejected the session")
	ErrIdentityUnavailable  = errors.New("identity session verification unavailable")
)

type IdentityProfileVerifier struct {
	url          string
	serviceToken string
	client       *http.Client
}

type identityProfileResponse struct {
	Subject                string `json:"subject"`
	SessionID              string `json:"session_id"`
	AccountStatus          string `json:"account_status"`
	PrimaryEmailVerified   bool   `json:"primary_email_verified"`
}

func NewIdentityProfileVerifier(url, serviceToken string, timeout time.Duration) *IdentityProfileVerifier {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &IdentityProfileVerifier{
		url:          url,
		serviceToken: serviceToken,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (v *IdentityProfileVerifier) Verify(ctx context.Context, authorization string, expectedUserID, expectedSessionID uuid.UUID) (bool, error) {
	if v == nil || v.url == "" || v.serviceToken == "" || authorization == "" {
		return false, ErrIdentityUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return false, ErrIdentityUnavailable
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("X-Control-Plane-Service-Token", v.serviceToken)
	resp, err := v.client.Do(req)
	if err != nil {
		return false, ErrIdentityUnavailable
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, ErrIdentityUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return false, ErrIdentityUnavailable
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdentityProfileBytes+1))
	if err != nil || len(body) > maxIdentityProfileBytes {
		return false, ErrIdentityUnavailable
	}

	var profile identityProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		return false, ErrIdentityUnavailable
	}
	if profile.Subject != expectedUserID.String() || profile.SessionID != expectedSessionID.String() || profile.AccountStatus != "active" {
		return false, ErrIdentityUnauthorized
	}
	return profile.PrimaryEmailVerified, nil
}
