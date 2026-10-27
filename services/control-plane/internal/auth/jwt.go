package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

type Validator struct {
	provider            *JWKSProvider
	issuer              string
	audience            string
	leeway              time.Duration
	maxAcceptanceWindow time.Duration
	now                 func() time.Time
}

func NewValidator(provider *JWKSProvider, issuer, audience string, leeway, maxAcceptanceWindow time.Duration) *Validator {
	if leeway <= 0 {
		leeway = 30 * time.Second
	}
	if maxAcceptanceWindow <= 0 {
		maxAcceptanceWindow = 15 * time.Minute
	}
	return &Validator{
		provider:            provider,
		issuer:              issuer,
		audience:            audience,
		leeway:              leeway,
		maxAcceptanceWindow: maxAcceptanceWindow,
		now:                 time.Now,
	}
}

func (v *Validator) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

func (v *Validator) ValidateBearer(ctx context.Context, authorization string) (Principal, error) {
	tokenStr, err := extractBearer(authorization)
	if err != nil {
		return Principal{}, err
	}
	return v.ValidateToken(ctx, tokenStr)
}

func (v *Validator) ValidateToken(ctx context.Context, tokenStr string) (Principal, error) {
	now := v.clock()
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(v.leeway),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)

	var claims Claims
	token, err := parser.ParseWithClaims(tokenStr, &claims, v.provider.Keyfunc(ctx))
	if err != nil || !token.Valid {
		return Principal{}, fmt.Errorf("invalid token")
	}

	if claims.Subject == "" {
		return Principal{}, fmt.Errorf("missing sub")
	}
	if claims.SessionID == "" {
		return Principal{}, fmt.Errorf("missing sid")
	}
	if claims.IssuedAt == nil {
		return Principal{}, fmt.Errorf("missing iat")
	}
	if claims.ExpiresAt == nil {
		return Principal{}, fmt.Errorf("missing exp")
	}
	if claims.NotBefore == nil {
		return Principal{}, fmt.Errorf("missing nbf")
	}

	iat := claims.IssuedAt.Time
	exp := claims.ExpiresAt.Time
	if iat.After(now.Add(v.leeway)) {
		return Principal{}, fmt.Errorf("invalid iat")
	}
	lifetime := exp.Sub(iat)
	if lifetime <= 0 || lifetime > v.maxAcceptanceWindow {
		return Principal{}, fmt.Errorf("token lifetime exceeds maximum")
	}
	if now.After(iat.Add(v.maxAcceptanceWindow)) {
		return Principal{}, fmt.Errorf("token acceptance window exceeded")
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Principal{}, fmt.Errorf("invalid sub")
	}
	sessionID, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return Principal{}, fmt.Errorf("invalid sid")
	}

	return Principal{
		UserID:    userID,
		SessionID: sessionID,
		Issuer:    v.issuer,
		Audience:  v.audience,
		ExpiresAt: claims.ExpiresAt.Unix(),
	}, nil
}

func extractBearer(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", fmt.Errorf("missing authorization")
	}
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", fmt.Errorf("invalid authorization scheme")
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", fmt.Errorf("empty bearer token")
	}
	return token, nil
}
