package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	var has011 bool
	var maxVersion int64
	for _, f := range files {
		if f.version > maxVersion {
			maxVersion = f.version
		}
		if f.version == 11 {
			has011 = true
		}
	}
	if !has011 {
		t.Fatal("expected migration version 11 (deployments)")
	}
	if maxVersion < 11 {
		t.Fatalf("max migration version=%d, want >= 11", maxVersion)
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
	var latest int64
	if err := pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest < 11 {
		t.Fatalf("latest migration=%d, want >= 11", latest)
	}
	var deployTable int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.tables
WHERE table_schema = current_schema() AND table_name = 'deployments'`).Scan(&deployTable); err != nil {
		t.Fatal(err)
	}
	if deployTable != 1 {
		t.Fatal("deployments table missing after migrate")
	}
	if err := Ping(ctx, pool, 2*time.Second); err != nil {
		t.Fatalf("Ping after migrate: %v", err)
	}
}

func TestMigrateUpgradesFromPhase8Schema(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminPool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()

	schema := "control_plane_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	}()

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	upgradePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer upgradePool.Close()

	files, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := upgradePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range files {
		if migration.version > 10 {
			continue
		}
		if _, err := tx.Exec(ctx, migration.sql); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply preceding migration %s: %v", migration.name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, migration.version); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("record preceding migration %s: %v", migration.name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, upgradePool); err != nil {
		t.Fatalf("upgrade from schema version 10: %v", err)
	}
	var latest int64
	if err := upgradePool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest != 11 {
		t.Fatalf("latest migration=%d, want 11", latest)
	}
	var deployTable int
	if err := upgradePool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.tables
WHERE table_schema = current_schema() AND table_name = 'deployments'`).Scan(&deployTable); err != nil {
		t.Fatal(err)
	}
	if deployTable != 1 {
		t.Fatal("deployments missing after upgrade to 11")
	}
}
