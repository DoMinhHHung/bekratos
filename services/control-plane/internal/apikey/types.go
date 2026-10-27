package apikey

import (
	"time"

	"github.com/google/uuid"
)

type KeyType string

const (
	TypePublishable  KeyType = "publishable"
	TypeServerSecret KeyType = "server_secret"
)

const (
	ScopeDataRead  = "data:read"
	ScopeDataWrite = "data:write"
)

var AllowedScopes = map[string]struct{}{
	ScopeDataRead:  {},
	ScopeDataWrite: {},
}

type Status string

const (
	StatusActive  Status = "active"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
)

type KeyRecord struct {
	ID               uuid.UUID
	EnvironmentID    uuid.UUID
	KeyType          KeyType
	Name             string
	KeyPrefix        string
	SecretHash       []byte
	Scopes           []string
	CreatedByUserID  uuid.UUID
	CreatedAt        time.Time
	LastUsedAt       *time.Time
	ExpiresAt        *time.Time
	RevokedAt        *time.Time
	RotationGroupID  uuid.UUID
	RotatedFromKeyID *uuid.UUID
}

func (k KeyRecord) Status(now time.Time) Status {
	if k.RevokedAt != nil {
		return StatusRevoked
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return StatusExpired
	}
	return StatusActive
}

type CreateInput struct {
	KeyType   KeyType
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

type IssuedKey struct {
	Record  KeyRecord
	FullKey string
}

type ResolveResult struct {
	KeyID         uuid.UUID
	EnvironmentID uuid.UUID
	KeyType       KeyType
	Scopes        []string
	Status        Status
}
