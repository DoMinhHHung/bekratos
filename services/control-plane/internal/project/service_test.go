package project

import (
	"context"
	"os"
	"testing"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
)

func TestCreateProjectWithDevEnv(t *testing.T) {
	url := os.Getenv("DATABASE_URL_TEST")
	if url == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	orgRepo := org.NewRepository(pool)
	orgSvc := org.NewService(orgRepo)
	ownerID := uuid.Must(uuid.NewV7())
	principal := auth.Principal{UserID: ownerID, SessionID: uuid.Must(uuid.NewV7())}
	o, err := orgSvc.CreateOrganization(ctx, principal, org.CreateOrgInput{
		Slug: "p4-" + ownerID.String()[24:], DisplayName: "P4",
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(NewRepository(pool), orgRepo)
	p, env, err := svc.CreateProject(ctx, principal, o.ID, CreateProjectInput{
		Slug: "app-" + ownerID.String()[24:], DisplayName: "App",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if env.Kind != KindDevelopment || env.Slug != "development" {
		t.Fatalf("default env: %+v", env)
	}
	if p.OrganizationID != o.ID {
		t.Fatal("org mismatch")
	}

	envs, err := svc.ListEnvironments(ctx, principal, o.ID, p.ID)
	if err != nil || len(envs) != 1 {
		t.Fatalf("list envs: %v len=%d", err, len(envs))
	}

	stranger := auth.Principal{UserID: uuid.Must(uuid.NewV7()), SessionID: uuid.Must(uuid.NewV7())}
	if _, err := svc.GetProject(ctx, stranger, o.ID, p.ID); err != ErrNotFound {
		t.Fatalf("stranger access: %v", err)
	}

	otherOrg, err := orgSvc.CreateOrganization(ctx, principal, org.CreateOrgInput{
		Slug: "p4b-" + ownerID.String()[24:], DisplayName: "P4B",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(ctx, principal, otherOrg.ID, p.ID); err != ErrNotFound {
		t.Fatalf("cross-org path should 404: %v", err)
	}
}
