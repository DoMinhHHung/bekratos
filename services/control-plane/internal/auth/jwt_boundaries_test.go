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

func TestValidateTokenTimeAndClaimBoundaries(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "boundary-kid"
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "EC",
				"crv": "P-256",
				"kid": keyID,
				"x":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.X.Bytes()),
				"y":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.Y.Bytes()),
				"alg": "ES256",
				"use": "sig",
			}},
		})
	}))
	defer jwks.Close()

	const (
		issuer   = "https://identity.example"
		audience = "bekratos-api"
	)
	leeway := 30 * time.Second
	maxLifetime := 90 * time.Second
	now := time.Unix(1_800_000_000, 0).UTC()
	validator := NewValidator(NewJWKSProvider(jwks.URL, time.Minute, time.Second), issuer, audience, leeway, maxLifetime)
	validator.now = func() time.Time { return now }

	userID := uuid.New()
	sessionID := uuid.New()
	makeToken := func(alg jwt.SigningMethod, claims jwt.MapClaims) string {
		token := jwt.NewWithClaims(alg, claims)
		token.Header["kid"] = keyID
		signed, err := token.SignedString(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	claims := func(iat, nbf, exp time.Time) jwt.MapClaims {
		return jwt.MapClaims{
			"sub": userID.String(),
			"sid": sessionID.String(),
			"iss": issuer,
			"aud": audience,
			"iat": iat.Unix(),
			"nbf": nbf.Unix(),
			"exp": exp.Unix(),
		}
	}
	validate := func(token string) error {
		_, err := validator.ValidateToken(context.Background(), token)
		return err
	}

	t.Run("nominal expiration", func(t *testing.T) {
		iat := now.Add(-time.Minute)
		exp := now
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, iat, exp))); err != nil {
			t.Fatalf("token at nominal expiration rejected: %v", err)
		}
	})
	t.Run("expiration leeway cutoff", func(t *testing.T) {
		iat := now.Add(-time.Minute)
		exp := now.Add(-(leeway - time.Second))
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, exp, exp))); err != nil {
			t.Fatalf("token within expiration leeway rejected: %v", err)
		}
		now = now.Add(time.Second)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, exp, exp))); err == nil {
			t.Fatal("token outside expiration leeway accepted")
		}
		now = now.Add(-time.Second)
	})
	t.Run("maximum acceptance lifetime boundary", func(t *testing.T) {
		iat := now.Add(-maxLifetime)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, iat, iat.Add(maxLifetime)))); err != nil {
			t.Fatalf("token at maximum acceptance lifetime rejected: %v", err)
		}
		now = now.Add(time.Second)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, iat, iat.Add(maxLifetime)))); err == nil {
			t.Fatal("token outside maximum acceptance lifetime accepted")
		}
		now = now.Add(-time.Second)
	})
	t.Run("lifetime equal to maximum", func(t *testing.T) {
		exp := now.Add(maxLifetime)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(now, now, exp))); err != nil {
			t.Fatalf("maximum nominal lifetime rejected: %v", err)
		}
	})
	t.Run("lifetime greater than maximum", func(t *testing.T) {
		exp := now.Add(maxLifetime + time.Second)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(now, now, exp))); err == nil {
			t.Fatal("overlong token accepted")
		}
	})
	t.Run("future issued-at beyond skew", func(t *testing.T) {
		iat := now.Add(leeway + time.Second)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, iat, iat.Add(time.Minute)))); err == nil {
			t.Fatal("future iat beyond leeway accepted")
		}
	})
	t.Run("future issued-at at skew boundary", func(t *testing.T) {
		iat := now.Add(leeway)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, iat, iat.Add(time.Minute)))); err != nil {
			t.Fatalf("future iat at leeway boundary rejected: %v", err)
		}
	})
	t.Run("expiration not after issued-at", func(t *testing.T) {
		iat := now.Add(time.Second)
		if err := validate(makeToken(jwt.SigningMethodES256, claims(iat, iat, iat))); err == nil {
			t.Fatal("exp equal to iat accepted")
		}
	})

	valid := claims(now, now, now.Add(time.Minute))
	for _, missing := range []string{"iat", "exp", "sub", "sid", "nbf"} {
		t.Run("missing "+missing, func(t *testing.T) {
			withoutClaim := jwt.MapClaims{}
			for key, value := range valid {
				withoutClaim[key] = value
			}
			delete(withoutClaim, missing)
			if err := validate(makeToken(jwt.SigningMethodES256, withoutClaim)); err == nil {
				t.Fatalf("missing %s accepted", missing)
			}
		})
	}
	for _, malformed := range []string{"iat", "exp", "nbf"} {
		t.Run("malformed "+malformed, func(t *testing.T) {
			invalid := claims(now, now, now.Add(time.Minute))
			invalid[malformed] = "not-a-numeric-date"
			if err := validate(makeToken(jwt.SigningMethodES256, invalid)); err == nil {
				t.Fatalf("malformed %s accepted", malformed)
			}
		})
	}
	for name, altered := range map[string]jwt.MapClaims{
		"wrong issuer": func() jwt.MapClaims {
			result := claims(now, now, now.Add(time.Minute))
			result["iss"] = "https://wrong.example"
			return result
		}(),
		"wrong audience": func() jwt.MapClaims {
			result := claims(now, now, now.Add(time.Minute))
			result["aud"] = "wrong-audience"
			return result
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(makeToken(jwt.SigningMethodES256, altered)); err == nil {
				t.Fatal("invalid issuer or audience accepted")
			}
		})
	}
	t.Run("non ES256", func(t *testing.T) {
		p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		token := jwt.NewWithClaims(jwt.SigningMethodES384, claims(now, now, now.Add(time.Minute)))
		token.Header["kid"] = keyID
		signed, err := token.SignedString(p384)
		if err != nil {
			t.Fatal(err)
		}
		if err := validate(signed); err == nil {
			t.Fatal("non-ES256 token accepted")
		}
	})
}
