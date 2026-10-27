package runtimeapi

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

const (
	maxRequestBytes  = 16 << 20
	maxResponseBytes = 1 << 20
)

var ErrTransport = errors.New("runtime transport failed")

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   token,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *Client) Configured() bool {
	return c != nil && c.baseURL != "" && len(c.token) >= 32
}

func (c *Client) Post(ctx context.Context, path, idempotencyKey string, requestBody any) (int, []byte, error) {
	if !c.Configured() {
		return 0, nil, ErrTransport
	}
	payload, err := json.Marshal(requestBody)
	if err != nil || len(payload) > maxRequestBytes {
		return 0, nil, ErrTransport
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, ErrTransport
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, ErrTransport
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return resp.StatusCode, nil, ErrTransport
	}
	return resp.StatusCode, body, nil
}
