package configversion

import (
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/schema"
	"github.com/google/uuid"
)

const ManifestFormatVersion = 1

type VersionState string

const (
	StateValidating           VersionState = "validating"
	StateAwaitingConfirmation VersionState = "awaiting_confirmation"
	StateQueued               VersionState = "queued"
	StateApplying             VersionState = "applying"
	StateActive               VersionState = "active"
	StateFailed               VersionState = "failed"
	StateSuperseded           VersionState = "superseded"
	StateBlocked              VersionState = "blocked"
)

type CompatibilityClass string

const (
	CompatCompatible            CompatibilityClass = "compatible"
	CompatMigrationRequired     CompatibilityClass = "migration_required"
	CompatBreakingOrDestructive CompatibilityClass = "breaking_or_destructive"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobLeased    JobState = "leased"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobBlocked   JobState = "blocked"
	JobCancelled JobState = "cancelled"
)

type ModulePinSnapshot struct {
	ModuleSlug         string    `json:"module_slug"`
	ReleaseID          uuid.UUID `json:"release_id"`
	Version            string    `json:"version"`
	APIContractVersion string    `json:"api_contract_version"`
}

type Manifest struct {
	ManifestFormatVersion int                   `json:"manifest_format_version"`
	EnvironmentID         uuid.UUID             `json:"environment_id"`
	SchemaRevision        int64                 `json:"schema_revision"`
	PinRevision           int64                 `json:"pin_revision"`
	Schema                schema.SchemaDocument `json:"schema"`
	ModulePins            []ModulePinSnapshot   `json:"module_pins"`
}

type Version struct {
	ID                      uuid.UUID
	EnvironmentID           uuid.UUID
	VersionNumber           int64
	ParentVersionID         *uuid.UUID
	ExpectedActiveVersionID *uuid.UUID
	SchemaRevision          int64
	PinRevision             int64
	ManifestFormatVersion   int
	ManifestCanonical       string
	ManifestSHA256          string
	Compatibility           CompatibilityClass
	State                   VersionState
	FailureCode             string
	FailureMessage          string
	ConfirmationRequired    bool
	ConfirmedAt             *time.Time
	ConfirmedByUserID       *uuid.UUID
	ConfirmationHash        string
	ActivatedAt             *time.Time
	CreatedByUserID         uuid.UUID
	CreatedAt               time.Time
	UpdatedAt               time.Time
	IdempotencyKey          string
}

type ApplyJob struct {
	ID                     uuid.UUID
	EnvironmentID          uuid.UUID
	ConfigurationVersionID uuid.UUID
	State                  JobState
	AttemptCount           int
	MaxAttempts            int
	LeaseOwner             string
	LeaseExpiresAt         *time.Time
	FencingToken           int64
	NextAttemptAt          time.Time
	LastErrorCode          string
	LastErrorMessage       string
	ApplyRequestID         string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CreateCandidateInput struct {
	ExpectedActiveVersionID *uuid.UUID
	ExpectedSchemaRevision  int64
	ExpectedPinRevision     int64
	IdempotencyKey          string
}

type ConfirmInput struct {
	VersionID               uuid.UUID
	ExpectedManifestSHA256  string
	ExpectedActiveVersionID *uuid.UUID
}

type Snapshot struct {
	SchemaRevision  int64
	PinRevision     int64
	Schema          schema.SchemaDocument
	Pins            []ModulePinSnapshot
	ActiveVersionID *uuid.UUID
}
