package schema

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/project"
	"github.com/google/uuid"
)

func TestSchemaDraftPostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	orgRepo := org.NewRepository(pool)
	projectRepo := project.NewRepository(pool)
	schemaRepo := NewRepository(pool)
	svc := NewService(schemaRepo, orgRepo, projectRepo)

	userID := uuid.Must(uuid.NewV7())
	suffix := uuid.NewString()[:8]
	orgID := uuid.Must(uuid.NewV7())
	projectID := uuid.Must(uuid.NewV7())
	envID := uuid.Must(uuid.NewV7())

	if _, err := pool.Exec(ctx, `
INSERT INTO organizations (id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1, $2, $2, 'active', $3)`, orgID, "schema-org-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1, $2, 'owner', 'active')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO projects (id, organization_id, slug, display_name, status, created_by_identity_user_id)
VALUES ($1, $2, $3, $3, 'active', $4)`, projectID, orgID, "schema-proj-"+suffix, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO project_environments (id, project_id, slug, kind, status)
VALUES ($1, $2, 'development', 'development', 'active')`, envID, projectID); err != nil {
		t.Fatal(err)
	}

	principal := auth.Principal{UserID: userID}

	draft, err := svc.GetSchema(ctx, principal, orgID, projectID, envID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision != 0 {
		t.Fatalf("initial revision=%d", draft.Revision)
	}

	d1, err := svc.UpsertCustomEntity(ctx, principal, orgID, projectID, envID, 0, EntityMutationInput{
		Key: "order", DisplayName: "Order",
	})
	if err != nil {
		t.Fatalf("upsert entity: %v", err)
	}
	if d1.Revision != 1 {
		t.Fatalf("revision=%d", d1.Revision)
	}
	if _, ok := d1.Document.Entities["order"]; !ok {
		t.Fatal("entity missing")
	}

	_, err = svc.UpsertCustomEntity(ctx, principal, orgID, projectID, envID, 0, EntityMutationInput{
		Key: "stale", DisplayName: "Stale",
	})
	if !errors.Is(err, ErrConflictRevision) {
		t.Fatalf("stale write: %v", err)
	}
	current, err := svc.GetSchema(ctx, principal, orgID, projectID, envID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != 1 {
		t.Fatalf("stale write mutated revision to %d", current.Revision)
	}
	if _, ok := current.Document.Entities["stale"]; ok {
		t.Fatal("stale write applied entity")
	}

	d2, err := svc.UpsertCustomField(ctx, principal, orgID, projectID, envID, 1, "order", FieldMutationInput{
		Key: "title", DisplayName: "Title", Type: TypeString, Required: true,
	})
	if err != nil {
		t.Fatalf("upsert field: %v", err)
	}
	if d2.Revision != 2 {
		t.Fatalf("revision after field=%d", d2.Revision)
	}

	d3, err := svc.UpsertCustomEntity(ctx, principal, orgID, projectID, envID, 2, EntityMutationInput{
		Key: "customer", DisplayName: "Customer",
	})
	if err != nil {
		t.Fatal(err)
	}
	d4, err := svc.UpsertCustomRelationship(ctx, principal, orgID, projectID, envID, d3.Revision, "order", RelationshipMutationInput{
		Key: "customer", SourceEntity: "order", TargetEntity: "customer",
		Cardinality: CardinalityManyToOne, Required: false, DeleteBehavior: DeleteRestrict,
	})
	if err != nil {
		t.Fatalf("relationship: %v", err)
	}
	d5, err := svc.PutSchema(ctx, principal, orgID, projectID, envID, PutSchemaInput{
		ExpectedRevision: d4.Revision,
		Document:         d4.Document,
	})
	if err != nil {
		t.Fatalf("put schema through service: %v", err)
	}
	if d5.Revision != d4.Revision+1 {
		t.Fatalf("revision after put schema=%d, want %d", d5.Revision, d4.Revision+1)
	}

	_, err = svc.UpsertCustomEntity(ctx, principal, orgID, projectID, envID, d5.Revision, EntityMutationInput{
		Key: "system", DisplayName: "Nope",
	})
	if !errors.Is(err, ErrReservedKey) && !errors.Is(err, ErrInvalidInput) {
		if err == nil {
			t.Fatal("reserved entity accepted")
		}
	}

	viewerID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1, $2, 'viewer', 'active')`, orgID, viewerID); err != nil {
		t.Fatal(err)
	}
	viewer := auth.Principal{UserID: viewerID}
	if _, err := svc.GetSchema(ctx, viewer, orgID, projectID, envID); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
	if _, err := svc.UpsertCustomEntity(ctx, viewer, orgID, projectID, envID, d5.Revision, EntityMutationInput{
		Key: "blocked", DisplayName: "Blocked",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer mutate: %v", err)
	}

	billingID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
VALUES ($1, $2, 'billing', 'active')`, orgID, billingID); err != nil {
		t.Fatal(err)
	}
	billing := auth.Principal{UserID: billingID}
	if _, err := svc.GetSchema(ctx, billing, orgID, projectID, envID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("billing read: %v", err)
	}

	otherOrg := uuid.Must(uuid.NewV7())
	if _, err := svc.GetSchema(ctx, principal, otherOrg, projectID, envID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org: %v", err)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM control_plane_audit_events
WHERE target_type = 'environment_schema_draft' AND target_id = $1`, envID.String()).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount < 1 {
		t.Fatalf("expected audit events, got %d", auditCount)
	}

	if _, err := pool.Exec(ctx, `UPDATE organizations SET status = 'archived', archived_at = now() WHERE id = $1`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetSchema(ctx, principal, orgID, projectID, envID); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInactive) {
		t.Fatalf("archived org access: %v", err)
	}
}
