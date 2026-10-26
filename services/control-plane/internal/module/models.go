package module

import (
	"time"

	"github.com/google/uuid"
)

type Module struct {
	Slug        string
	DisplayName string
	Description string
	CreatedAt   time.Time
}

type Release struct {
	ID                 uuid.UUID
	ModuleSlug         string
	Version            string
	RuntimeCompat      string
	APIContractVersion string
	Metadata           []byte
	PublishedAt        time.Time
	Dependencies       []Dependency
	Conflicts          []string
}

type Dependency struct {
	ModuleSlug        string
	VersionConstraint string
	Required          bool
}

type Pin struct {
	EnvironmentID uuid.UUID
	ModuleSlug    string
	ReleaseID     uuid.UUID
	Version       string
	PinnedAt      time.Time
	UpdatedAt     time.Time
}

type Selection struct {
	ModuleSlug string
	Version    string
}

type ResolvedRelease struct {
	ModuleSlug string `json:"module_slug"`
	Version    string `json:"version"`
	ReleaseID  string `json:"release_id"`
}

type ResolutionPlan struct {
	Releases []ResolvedRelease `json:"releases"`
	Revision int64             `json:"revision,omitempty"`
}
