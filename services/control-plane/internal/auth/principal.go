package auth

import (
	"context"

	"github.com/google/uuid"
)

type contextKey int

const principalKey contextKey = 1

type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Issuer    string
	Audience  string
	ExpiresAt int64
}

func ContextWithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
