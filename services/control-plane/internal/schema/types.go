package schema

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Origin string

const (
	OriginSystem Origin = "system"
	OriginModule Origin = "module"
	OriginCustom Origin = "custom"
)

type ScalarType string

const (
	TypeString   ScalarType = "string"
	TypeInteger  ScalarType = "integer"
	TypeNumber   ScalarType = "number"
	TypeBoolean  ScalarType = "boolean"
	TypeDate     ScalarType = "date"
	TypeDateTime ScalarType = "datetime"
	TypeUUID     ScalarType = "uuid"
)

type Cardinality string

const (
	CardinalityOneToOne   Cardinality = "one_to_one"
	CardinalityManyToOne  Cardinality = "many_to_one"
	CardinalityOneToMany  Cardinality = "one_to_many"
	CardinalityManyToMany Cardinality = "many_to_many"
)

type DeleteBehavior string

const (
	DeleteRestrict DeleteBehavior = "restrict"
	DeleteSetNull  DeleteBehavior = "set_null"
)

type FieldConstraints struct {
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
}

type FieldDefinition struct {
	ID          uuid.UUID         `json:"id"`
	Key         string            `json:"key"`
	DisplayName string            `json:"display_name"`
	Type        ScalarType        `json:"type"`
	Required    bool              `json:"required"`
	Nullable    bool              `json:"nullable"`
	Default     json.RawMessage   `json:"default,omitempty"`
	Constraints *FieldConstraints `json:"constraints,omitempty"`
	Origin      Origin            `json:"origin"`
	ModuleSlug  string            `json:"module_slug,omitempty"`
	ReleaseID   *uuid.UUID        `json:"release_id,omitempty"`
	Protected   bool              `json:"protected"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type RelationshipDefinition struct {
	ID             uuid.UUID      `json:"id"`
	Key            string         `json:"key"`
	SourceEntity   string         `json:"source_entity"`
	TargetEntity   string         `json:"target_entity"`
	Cardinality    Cardinality    `json:"cardinality"`
	Required       bool           `json:"required"`
	DeleteBehavior DeleteBehavior `json:"delete_behavior"`
	Origin         Origin         `json:"origin"`
	ModuleSlug     string         `json:"module_slug,omitempty"`
	ReleaseID      *uuid.UUID     `json:"release_id,omitempty"`
	Protected      bool           `json:"protected"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type EntityDefinition struct {
	ID            uuid.UUID                         `json:"id"`
	Key           string                            `json:"key"`
	DisplayName   string                            `json:"display_name"`
	Origin        Origin                            `json:"origin"`
	ModuleSlug    string                            `json:"module_slug,omitempty"`
	ReleaseID     *uuid.UUID                        `json:"release_id,omitempty"`
	Protected     bool                              `json:"protected"`
	Fields        map[string]FieldDefinition        `json:"fields"`
	Relationships map[string]RelationshipDefinition `json:"relationships"`
	AccessPolicy  *PolicyNode                       `json:"access_policy,omitempty"`
	CreatedAt     time.Time                         `json:"created_at"`
	UpdatedAt     time.Time                         `json:"updated_at"`
}

type SchemaDocument struct {
	Entities map[string]EntityDefinition `json:"entities"`
}

type Draft struct {
	EnvironmentID uuid.UUID
	Revision      int64
	Document      SchemaDocument
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type PutSchemaInput struct {
	ExpectedRevision int64
	Document         SchemaDocument
}

type EntityMutationInput struct {
	Key         string
	DisplayName string
}

type FieldMutationInput struct {
	Key         string
	DisplayName string
	Type        ScalarType
	Required    bool
	Nullable    bool
	Default     json.RawMessage
	Constraints *FieldConstraints
}

type RelationshipMutationInput struct {
	Key            string
	SourceEntity   string
	TargetEntity   string
	Cardinality    Cardinality
	Required       bool
	DeleteBehavior DeleteBehavior
}
