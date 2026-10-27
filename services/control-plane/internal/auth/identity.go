package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrIdentityUnauthenticated = errors.New("identity rejected the access token")
	ErrIdentityUnavailable     = errors.New("identity introspection is unavailable")
)

const maxIdentityResponseBytes = 64 << 10

type IdentityIntrospection struct {
	Active               bool    `json:"active"`
	UserID               string  `json:"user_id"`
	SessionID            string  `json:"session_id"`
	Issuer               string  `json:"issuer"`
	Audience             string  `json:"audience"`
	ExpiresAt            int64   `json:"expires_at"`
	EmailVerified        bool    `json:"email_verified"`
	VerifiedPrimaryEmail *string `json:"verified_primary_email"`
}

type IdentityClient struct {
	url        string
	serviceKey string
	httpClient *http.Client
}

func NewIdentityClient(url, serviceKey string, timeout time.Duration) *IdentityClient {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &IdentityClient{
		url       : undefined,
		serviceKey: undefined,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *IdentityClient) Introspect(ctx context.Context, accessToken string) (IdentityIntrospection, error) {
	if c == nil || c.url == "" || len(c.serviceKey) < 32 || accessToken == "" {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	payload, err := json.Marshal(struct {
		AccessToken string `json:"access_token"`
	}{AccessToken: accessToken})
	if err != nil {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Identity-Service-Token", c.serviceKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return IdentityIntrospection{}, ErrIdentityUnauthenticated
	}
	if resp.StatusCode != http.StatusOK {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdentityResponseBytes+1))
	if err != nil || len(body) > maxIdentityResponseBytes {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	var result IdentityIntrospection
	if err := json.Unmarshal(body, &result); err != nil {
		return IdentityIntrospection{}, ErrIdentityUnavailable
	}
	return result, nil
}
