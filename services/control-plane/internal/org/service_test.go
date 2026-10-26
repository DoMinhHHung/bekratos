package org

import (
	"context"
	"os"
	"testing"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/google/uuid"
)

func TestCreateAndLastOwner(t *testing.T) {
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

	repo := NewRepository(pool)
	svc := NewService(repo)
	ownerID := uuid.Must(uuid.NewV7())
	principal := auth.Principal{UserID: ownerID, SessionID: uuid.Must(uuid.NewV7())}

	org, err := svc.CreateOrganization(ctx, principal, CreateOrgInput{
		Slug: "acme-" + ownerID.String()[:8], DisplayName: "Acme",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := svc.RemoveMember(ctx, principal, org.ID, ownerID); err != ErrLastOwner {
		t.Fatalf("expected last owner error, got %v", err)
	}
	if err := svc.ChangeMemberRole(ctx, principal, org.ID, ownerID, RoleAdmin); err != ErrLastOwner {
		t.Fatalf("expected last owner demote error, got %v", err)
	}

	got, membership, err := svc.GetOrganization(ctx, principal, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != org.ID || membership.Role != RoleOwner {
		t.Fatalf("get mismatch: %+v %+v", got, membership)
	}

	stranger := auth.Principal{UserID: uuid.Must(uuid.NewV7()), SessionID: uuid.Must(uuid.NewV7())}
	if _, _, err := svc.GetOrganization(ctx, stranger, org.ID); err != ErrNotFound {
		t.Fatalf("stranger should get not found, got %v", err)
	}
}

func TestArchiveRequiresOwner(t *testing.T) {
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

	repo := NewRepository(pool)
	svc := NewService(repo)
	ownerID := uuid.Must(uuid.NewV7())
	principal := auth.Principal{UserID: ownerID, SessionID: uuid.Must(uuid.NewV7())}
	org, err := svc.CreateOrganization(ctx, principal, CreateOrgInput{
		Slug: "arc-" + ownerID.String()[:8], DisplayName: "Arc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveOrganization(ctx, principal, org.ID); err != nil {
		t.Fatalf("owner archive: %v", err)
	}
}
