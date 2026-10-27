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
	url    string
	client *http.Client
}

type identityProfileResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	EmailVerified bool   `json:"email_verified"`
}

func NewIdentityProfileVerifier(url string, timeout time.Duration) *IdentityProfileVerifier {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &IdentityProfileVerifier{
		url:    url,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (v *IdentityProfileVerifier) Verify(ctx context.Context, authorization string, expectedUserID uuid.UUID) (bool, error) {
	if v == nil || v.url == "" || authorization == "" {
		return false, ErrIdentityUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return false, ErrIdentityUnavailable
	}
	req.Header.Set("Authorization", authorization)
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
	if profile.ID != expectedUserID.String() || profile.Status != "active" {
		return false, ErrIdentityUnauthorized
	}
	return profile.EmailVerified, nil
}
