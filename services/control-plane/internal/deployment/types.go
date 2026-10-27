package deployment

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusQueued       Status = "queued"
	StatusProvisioning Status = "provisioning"
	StatusReady        Status = "ready"
	StatusFailed       Status = "failed"
	StatusSuspending   Status = "suspending"
	StatusSuspended    Status = "suspended"
	StatusDeleting     Status = "deleting"
	StatusDeleted      Status = "deleted"
)

type TargetMode string

const TargetModeShared TargetMode = "shared"

type ProviderName string

const ProviderUnavailable ProviderName = "unavailable"

type Deployment struct {
	ID                     uuid.UUID
	EnvironmentID          uuid.UUID
	ConfigurationVersionID uuid.UUID
	TargetMode             TargetMode
	Provider               ProviderName
	Status                 Status
	FailureCode            string
	FailureMessage         string
	AttemptCount           int
	MaxAttempts            int
	IdempotencyKey         string
	RequestFingerprint     string
	ProviderRequestID      string
	LeaseOwner             *string
	LeaseExpiresAt         *time.Time
	FencingToken           int64
	NextAttemptAt          time.Time
	ProviderURL            *string
	ProviderHost           *string
	ProviderRegion         *string
	ReadinessProbeID       string
	ReadinessObservedAt    *time.Time
	CreatedByUserID        uuid.UUID
	CreatedAt              time.Time
	UpdatedAt              time.Time
	StartedAt              *time.Time
	ReadyAt                *time.Time
	FinishedAt             *time.Time
}

type CreateInput struct {
	ConfigurationVersionID *uuid.UUID
	IdempotencyKey         string
	TargetMode             TargetMode
}

type CreateResult struct {
	Deployment Deployment
	Reused     bool
}
