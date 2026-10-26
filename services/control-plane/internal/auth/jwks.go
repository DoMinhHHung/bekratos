package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const maxJWKSBytes = 1 << 20

type jwkKey struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type JWKSProvider struct {
	url        string
	httpClient *http.Client
	ttl        time.Duration
	mu         sync.RWMutex
	keys       map[string]*ecdsa.PublicKey
	fetchedAt  time.Time
}

func NewJWKSProvider(url string, ttl, timeout time.Duration) *JWKSProvider {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &JWKSProvider{
		url: url,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		ttl:  ttl,
		keys: make(map[string]*ecdsa.PublicKey),
	}
}

func (p *JWKSProvider) Keyfunc(ctx context.Context) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodES256.Alg() {
			return nil, fmt.Errorf("unexpected signing method")
		}
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("missing kid")
		}
		key, err := p.lookup(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}
}

func (p *JWKSProvider) lookup(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	p.mu.RLock()
	key, ok := p.keys[kid]
	fresh := !p.fetchedAt.IsZero() && time.Since(p.fetchedAt) < p.ttl
	p.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}

	if err := p.refresh(ctx); err != nil {
		if ok {
			return key, nil
		}
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	key, ok = p.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid")
	}
	return key, nil
}

func (p *JWKSProvider) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return fmt.Errorf("jwks request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxJWKSBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("jwks read: %w", err)
	}
	if len(body) > maxJWKSBytes {
		return fmt.Errorf("jwks response too large")
	}

	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("jwks parse: %w", err)
	}

	next := make(map[string]*ecdsa.PublicKey)
	for _, k := range doc.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" {
			continue
		}
		if k.Alg != "" && k.Alg != "ES256" {
			continue
		}
		if k.Kid == "" || k.X == "" || k.Y == "" {
			continue
		}
		pub, err := parseECPublicKey(k.X, k.Y)
		if err != nil {
			continue
		}
		next[k.Kid] = pub
	}
	if len(next) == 0 {
		return fmt.Errorf("jwks contained no usable ES256 keys")
	}

	p.mu.Lock()
	p.keys = next
	p.fetchedAt = time.Now()
	p.mu.Unlock()
	return nil
}

func parseECPublicKey(xB64, yB64 string) (*ecdsa.PublicKey, error) {
	xb, err := base64.RawURLEncoding.DecodeString(xB64)
	if err != nil {
		return nil, err
	}
	yb, err := base64.RawURLEncoding.DecodeString(yB64)
	if err != nil {
		return nil, err
	}
	curve := elliptic.P256()
	x := new(big.Int).SetBytes(xb)
	y := new(big.Int).SetBytes(yb)
	if !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("point not on curve")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}
