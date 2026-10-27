package schema

import (
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
)

func TestEnforceOwnershipRejectsOriginChange(t *testing.T) {
	s := &Service{}
	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())
	prev := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"user": {
				ID: id, Key: "user", DisplayName: "User", Origin: OriginModule,
				ModuleSlug: "auth", ReleaseID: ptrUUID(uuid.Must(uuid.NewV7())),
				Protected: true, Fields: map[string]FieldDefinition{},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	next := cloneDocument(prev)
	ent := next.Entities["user"]
	ent.Origin = OriginCustom
	ent.Protected = false
	next.Entities["user"] = ent
	if err := s.enforceOwnershipInvariants(prev, next); !errors.Is(err, ErrProtected) {
		t.Fatalf("want protected, got %v", err)
	}
}

func TestEnforceOwnershipRejectsDeleteSystem(t *testing.T) {
	s := &Service{}
	now := time.Now().UTC()
	prev := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"meta": {
				ID: uuid.Must(uuid.NewV7()), Key: "meta", DisplayName: "Meta", Origin: OriginSystem,
				Protected: true, Fields: map[string]FieldDefinition{},
				Relationships: map[string]RelationshipDefinition{},
				CreatedAt:     now, UpdatedAt: now,
			},
		},
	}
	next := EmptyDocument()
	if err := s.enforceOwnershipInvariants(prev, next); !errors.Is(err, ErrProtected) {
		t.Fatalf("want protected, got %v", err)
	}
}

func TestRejectForgedSystemOnPut(t *testing.T) {
	s := &Service{}
	doc := SchemaDocument{Entities: map[string]EntityDefinition{
		"user": {
			ID: uuid.Must(uuid.NewV7()), Key: "user", DisplayName: "User", Origin: OriginSystem,
			Fields: map[string]FieldDefinition{}, Relationships: map[string]RelationshipDefinition{},
		},
	}}
	if err := s.rejectForgedOwnership(doc); !errors.Is(err, ErrProtected) {
		t.Fatalf("got %v", err)
	}
}

func TestEnforceOwnershipAllowsCustomMutation(t *testing.T) {
	s := &Service{}
	now := time.Now().UTC()
	id := uuid.Must(uuid.NewV7())
	prev := SchemaDocument{
		Entities: map[string]EntityDefinition{
			"order": {
				ID: id, Key: "order", DisplayName: "Order", Origin: OriginCustom,
				Fields: map[string]FieldDefinition{}, Relationships: map[string]RelationshipDefinition{},
				CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	next := cloneDocument(prev)
	ent := next.Entities["order"]
	ent.DisplayName = "Orders"
	next.Entities["order"] = ent
	if err := s.enforceOwnershipInvariants(prev, next); err != nil {
		t.Fatalf("custom mutation: %v", err)
	}
}

func TestRoleSchemaPermissions(t *testing.T) {
	cases := []struct {
		role   org.Role
		read   bool
		mutate bool
	}{
		{org.RoleOwner, true, true},
		{org.RoleAdmin, true, true},
		{org.RoleDeveloper, true, true},
		{org.RoleViewer, true, false},
		{org.RoleBilling, false, false},
	}
	for _, tc := range cases {
		if got := tc.role.CanReadSchema(); got != tc.read {
			t.Fatalf("%s CanReadSchema=%v want %v", tc.role, got, tc.read)
		}
		if got := tc.role.CanMutateSchema(); got != tc.mutate {
			t.Fatalf("%s CanMutateSchema=%v want %v", tc.role, got, tc.mutate)
		}
	}
}

func ptrUUID(id uuid.UUID) *uuid.UUID {
	return &id
}
