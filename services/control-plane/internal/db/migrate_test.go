package db

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMigrateNilPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Migrate(ctx, nil)
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
}

func TestLoadMigrationsFindsInit(t *testing.T) {
	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one migration")
	}
	if files[0].version != 1 {
		t.Errorf("first version = %d, want 1", files[0].version)
	}
	if files[0].sql == "" {
		t.Error("migration SQL is empty")
	}
}

func TestMigrateAndPingIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate idempotent second run: %v", err)
	}

	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = 1`).Scan(&version); err != nil {
		t.Fatalf("schema_migrations row: %v", err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}

	if err := Ping(ctx, pool, 2*time.Second); err != nil {
		t.Fatalf("Ping after migrate: %v", err)
	}
}
