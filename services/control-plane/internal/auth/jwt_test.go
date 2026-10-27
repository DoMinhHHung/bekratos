package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestExtractBearer(t *testing.T) {
	_, err := extractBearer("")
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = extractBearer("Basic abc")
	if err == nil {
		t.Fatal("expected scheme error")
	}
	tok, err := extractBearer("Bearer token-value")
	if err != nil || tok != "token-value" {
		t.Fatalf("got %q %v", tok, err)
	}
}

func TestValidateRejectsBadAlgAndClaims(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-kid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x := base64.RawURLEncoding.EncodeToString(priv.PublicKey.X.Bytes())
		y := base64.RawURLEncoding.EncodeToString(priv.PublicKey.Y.Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "EC", "crv": "P-256", "kid": kid, "x": x, "y": y, "alg": "ES256",
			}},
		})
	}))
	defer srv.Close()

	provider := NewJWKSProvider(srv.URL, time.Minute, time.Second)
	v := NewValidator(provider, "https://identity.bekratos.local", "bekratos-api", time.Second, 15*time.Minute)

	uid := uuid.New()
	sid := uuid.New()
	now := time.Now()

	makeToken := func(alg jwt.SigningMethod, iss, aud string, exp time.Time, includeSid bool) string {
		claims := jwt.MapClaims{
			"sub": uid.String(),
			"iss": iss,
			"aud": aud,
			"iat": now.Unix(),
			"nbf": now.Unix(),
			"exp": exp.Unix(),
		}
		if includeSid {
			claims["sid"] = sid.String()
		}
		tkn := jwt.NewWithClaims(alg, claims)
		tkn.Header["kid"] = kid
		s, err := tkn.SignedString(priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	good := makeToken(jwt.SigningMethodES256, "https://identity.bekratos.local", "bekratos-api", now.Add(time.Minute), true)
	p, err := v.ValidateToken(context.Background(), good)
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if p.UserID != uid || p.SessionID != sid {
		t.Fatalf("principal mismatch: %+v", p)
	}

	cases := []string{
		makeToken(jwt.SigningMethodES256, "https://wrong", "bekratos-api", now.Add(time.Minute), true),
		makeToken(jwt.SigningMethodES256, "https://identity.bekratos.local", "wrong-aud", now.Add(time.Minute), true),
		makeToken(jwt.SigningMethodES256, "https://identity.bekratos.local", "bekratos-api", now.Add(-time.Minute), true),
		makeToken(jwt.SigningMethodES256, "https://identity.bekratos.local", "bekratos-api", now.Add(time.Minute), false),
	}
	for i, tok := range cases {
		if _, err := v.ValidateToken(context.Background(), tok); err == nil {
			t.Fatalf("case %d expected rejection", i)
		}
	}
}

func TestMiddlewareUnauthorized(t *testing.T) {
	provider := NewJWKSProvider("http://127.0.0.1:1/jwks", time.Minute, 50*time.Millisecond)
	v := NewValidator(provider, "iss", "aud", time.Second, 15*time.Minute)
	mw := NewMiddleware(v, nil, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next should not run")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	mw.RequirePrincipal(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
}
