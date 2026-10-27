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
	"golang.org/x/sync/singleflight"
)

const (
	maxJWKSBytes     = 1 << 20
	hardMaxStaleTTL  = 5 * time.Minute
	defaultRefreshCD = 5 * time.Second
)

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
	url            string
	httpClient     *http.Client
	effectiveTTL   time.Duration
	staleTTL       time.Duration
	refreshCD      time.Duration
	mu             sync.RWMutex
	keys           map[string]*ecdsa.PublicKey
	fetchedAt      time.Time
	lastRefreshAt  time.Time
	lastRefreshErr error
	now            func() time.Time
	sf             singleflight.Group
}

func NewJWKSProvider(url string, ttl, timeout time.Duration) *JWKSProvider {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	effectiveTTL := ttl
	if effectiveTTL > hardMaxStaleTTL {
		effectiveTTL = hardMaxStaleTTL
	}
	stale := hardMaxStaleTTL
	if ttl <= hardMaxStaleTTL/3 {
		stale = ttl * 3
	}
	return &JWKSProvider{
		url: url,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		effectiveTTL: effectiveTTL,
		staleTTL:     stale,
		refreshCD:    defaultRefreshCD,
		keys:         make(map[string]*ecdsa.PublicKey),
		now:          time.Now,
	}
}

func (p *JWKSProvider) currentTime() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
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
	age := p.currentTime().Sub(p.fetchedAt)
	fresh := !p.fetchedAt.IsZero() && age < p.effectiveTTL
	staleOK := ok && !p.fetchedAt.IsZero() && age < p.staleTTL && age < hardMaxStaleTTL
	p.mu.RUnlock()

	if ok && fresh {
		return key, nil
	}

	if err := p.refreshSerialized(ctx); err != nil {
		if staleOK {
			p.mu.RLock()
			key, ok = p.keys[kid]
			age = p.currentTime().Sub(p.fetchedAt)
			staleOK = ok && !p.fetchedAt.IsZero() && age < p.staleTTL && age < hardMaxStaleTTL
			p.mu.RUnlock()
			if staleOK {
				return key, nil
			}
		}
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	key, ok = p.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid")
	}
	age = p.currentTime().Sub(p.fetchedAt)
	if p.fetchedAt.IsZero() || age >= p.staleTTL || age >= hardMaxStaleTTL {
		return nil, fmt.Errorf("jwks stale")
	}
	return key, nil
}

func (p *JWKSProvider) refreshSerialized(ctx context.Context) error {
	p.mu.RLock()
	cdActive := !p.lastRefreshAt.IsZero() && p.currentTime().Sub(p.lastRefreshAt) < p.refreshCD
	lastErr := p.lastRefreshErr
	p.mu.RUnlock()
	if cdActive {
		if lastErr != nil {
			return lastErr
		}
		return nil
	}

	_, err, _ := p.sf.Do("jwks", func() (any, error) {
		p.mu.RLock()
		if !p.lastRefreshAt.IsZero() && p.currentTime().Sub(p.lastRefreshAt) < p.refreshCD {
			err := p.lastRefreshErr
			p.mu.RUnlock()
			return nil, err
		}
		p.mu.RUnlock()
		err := p.refresh(ctx)
		p.mu.Lock()
		p.lastRefreshAt = p.currentTime()
		p.lastRefreshErr = err
		p.mu.Unlock()
		return nil, err
	})
	return err
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
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if k.Kid == "" || k.X == "" || k.Y == "" {
			continue
		}
		if _, dup := next[k.Kid]; dup {
			return fmt.Errorf("jwks duplicate kid")
		}
		pub, err := parseECPublicKey(k.X, k.Y)
		if err != nil {
			return fmt.Errorf("jwks invalid key material")
		}
		next[k.Kid] = pub
	}
	if len(next) == 0 {
		return fmt.Errorf("jwks contained no usable ES256 keys")
	}

	p.mu.Lock()
	p.keys = next
	p.fetchedAt = p.currentTime()
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
