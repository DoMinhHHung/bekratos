package project

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusArchived  Status = "archived"
)

type EnvKind string

const (
	KindDevelopment EnvKind = "development"
	KindStaging     EnvKind = "staging"
	KindProduction  EnvKind = "production"
	KindCustom      EnvKind = "custom"
)

type Project struct {
	ID                      uuid.UUID
	OrganizationID          uuid.UUID
	Slug                    string
	DisplayName             string
	Description             string
	Status                  Status
	CreatedByIdentityUserID uuid.UUID
	CreatedAt               time.Time
	UpdatedAt               time.Time
	ArchivedAt              *time.Time
}

type Environment struct {
	ID                           uuid.UUID
	ProjectID                    uuid.UUID
	Slug                         string
	Kind                         EnvKind
	Status                       Status
	ActiveConfigurationVersionID *uuid.UUID
	ActiveDeploymentID           *uuid.UUID
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	ArchivedAt                   *time.Time
}

type CreateProjectInput struct {
	Slug        string
	DisplayName string
	Description string
}

type UpdateProjectInput struct {
	DisplayName *string
	Description *string
}

type CreateEnvironmentInput struct {
	Slug string
	Kind EnvKind
}
