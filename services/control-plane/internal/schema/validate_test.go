package schema

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateDocumentEmpty(t *testing.T) {
	if err := ValidateDocument(EmptyDocument()); err != nil {
		t.Fatalf("empty document: %v", err)
	}
}

func TestValidateDocumentValidCustomEntity(t *testing.T) {
	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())
	fid := uuid.Must(uuid.NewV7())
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: id, Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{
					"title": {
						ID: fid, Key: "title", DisplayName: "Title", Type: TypeString,
						Required: true, Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
					},
				},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

func TestCanonicalizePersistsNormalizedKeys(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{Entities: map[string]EntityDefinition{
		"Order": {
			ID: uuid.Must(uuid.NewV7()), Key: "Order", DisplayName: "Order", Origin: OriginCustom,
			Fields: map[string]FieldDefinition{
				"Title": {ID: uuid.Must(uuid.NewV7()), Key: "Title", DisplayName: "Title", Type: TypeString, Origin: OriginCustom, CreatedAt: now, UpdatedAt: now},
			},
			Relationships: map[string]RelationshipDefinition{},
			CreatedAt:     now, UpdatedAt: now,
		},
	}}
	out, err := CanonicalizeDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Entities["order"]; !ok {
		t.Fatal("entity key not canonical")
	}
	if out.Entities["order"].Key != "order" {
		t.Fatal("entity.Key not canonical")
	}
	if _, ok := out.Entities["order"].Fields["title"]; !ok {
		t.Fatal("field key not canonical")
	}
}

func TestValidateDocumentReservedEntityKey(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"system": {
				ID: uuid.Must(uuid.NewV7()), Key: "system", DisplayName: "System",
				Origin: OriginCustom, Fields: map[string]FieldDefinition{},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrReservedKey) {
		t.Fatalf("want reserved, got %v", err)
	}
}

func TestDefaultDateInvalid(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{Entities: map[string]EntityDefinition{
		"order": {
			ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
			Fields: map[string]FieldDefinition{
				"shipped": {ID: uuid.Must(uuid.NewV7()), Key: "shipped", DisplayName: "Shipped", Type: TypeDate, Default: json.RawMessage(`"not-a-date"`), Origin: OriginCustom, CreatedAt: now, UpdatedAt: now},
			},
			Relationships: map[string]RelationshipDefinition{},
			CreatedAt:     now, UpdatedAt: now,
		},
	}}
	if err := ValidateDocument(doc); err == nil {
		t.Fatal("expected invalid date default")
	}
}

func TestValidateDocumentReservedFieldKey(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{
					"id": {
						ID: uuid.Must(uuid.NewV7()), Key: "id", DisplayName: "ID", Type: TypeUUID,
						Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
					},
				},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrReservedKey) {
		t.Fatalf("want reserved field, got %v", err)
	}
}

func TestValidateDocumentRequiredAndNullable(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{
					"title": {
						ID: uuid.Must(uuid.NewV7()), Key: "title", DisplayName: "Title", Type: TypeString,
						Required: true, Nullable: true, Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
					},
				},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestValidateDocumentInvalidType(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{
					"payload": {
						ID: uuid.Must(uuid.NewV7()), Key: "payload", DisplayName: "Payload", Type: "object",
						Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
					},
				},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want invalid input, got %v", err)
	}
}

func TestValidateDocumentDefaultTypeMismatch(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{
					"count": {
						ID: uuid.Must(uuid.NewV7()), Key: "count", DisplayName: "Count", Type: TypeInteger,
						Default: json.RawMessage(`"not-int"`), Origin: OriginCustom, CreatedAt: now, UpdatedAt: now,
					},
				},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestValidateDocumentRelationshipMissingTarget(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{},
				Relationships: map[string]RelationshipDefinition{
					"customer": {
						ID: uuid.Must(uuid.NewV7()), Key: "customer", SourceEntity: "order",
						TargetEntity: "customer", Cardinality: CardinalityManyToOne,
						DeleteBehavior: DeleteRestrict, Origin: OriginCustom,
						CreatedAt: now, UpdatedAt: now,
					},
				},
				CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrUnknownEntity) {
		t.Fatalf("want unknown entity, got %v", err)
	}
}

func TestValidateDocumentSetNullRequired(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{},
				Relationships: map[string]RelationshipDefinition{
					"self_ref": {
						ID: uuid.Must(uuid.NewV7()), Key: "self_ref", SourceEntity: "order",
						TargetEntity: "order", Cardinality: CardinalityManyToOne,
						Required: true, DeleteBehavior: DeleteSetNull, Origin: OriginCustom,
						CreatedAt: now, UpdatedAt: now,
					},
				},
				CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("want invalid relation, got %v", err)
	}
}

func TestValidateDocumentKeyMismatch(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: uuid.Must(uuid.NewV7()), Key: "other", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{}, Relationships: map[string]RelationshipDefinition{},
				CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}

func TestValidateDocumentModuleRequiresRelease(t *testing.T) {
	now := time.Now().UTC()
	doc := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"user": {
				ID: uuid.Must(uuid.NewV7()), Key: "user", DisplayName: "User", Origin: OriginModule,
				ModuleSlug: "auth", Fields: map[string]FieldDefinition{},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	if err := ValidateDocument(doc); !errors.Is(err, ErrValidation) {
		t.Fatalf("want validation, got %v", err)
	}
}
