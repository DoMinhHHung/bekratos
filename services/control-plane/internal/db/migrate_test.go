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
	if files[0].sql == "" {
		t.Error("migration SQL is empty")
	}
	var has008 bool
	var maxVersion int64
	for _, f := range files {
		if f.version > maxVersion {
			maxVersion = f.version
		}
		if f.version == 8 {
			has008 = true
		}
	}
	if !has008 {
		t.Fatal("expected migration version 8 (schema_drafts)")
	}
	if maxVersion < 8 {
		t.Fatalf("max migration version=%d, want >= 8", maxVersion)
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

	var latest int64
	if err := pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest < 8 {
		t.Fatalf("latest migration=%d, want >= 8", latest)
	}

	var draftTable int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'environment_schema_drafts'`).Scan(&draftTable); err != nil {
		t.Fatal(err)
	}
	if draftTable != 1 {
		t.Fatal("environment_schema_drafts missing after migrate")
	}

	var nullable string
	var defaultValue *string
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'module_releases'
		  AND column_name = 'published_at'`).Scan(&nullable, &defaultValue); err != nil {
		t.Fatal(err)
	}
	if nullable != "YES" || defaultValue != nil {
		t.Fatalf("published_at nullable=%q default=%v", nullable, defaultValue)
	}
	if err := Ping(ctx, pool, 2*time.Second); err != nil {
		t.Fatalf("Ping after migrate: %v", err)
	}
}

func TestMigrateUpgradesPreviousPhase5Schema(t *testing.T) {
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
	if err := upgradePool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	files, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := upgradePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range files {
		if migration.version > 7 {
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
		t.Fatalf("upgrade from schema version 7: %v", err)
	}
	var latest int64
	if err := upgradePool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest != 8 {
		t.Fatalf("latest migration=%d, want 8", latest)
	}
	var seeded int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM module_releases
		WHERE module_slug IN ('core', 'auth') AND published_at IS NOT NULL`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != 3 {
		t.Fatalf("upgrade lost or unpublished seeded releases: count=%d", seeded)
	}
	var draftTable int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'environment_schema_drafts'`).Scan(&draftTable); err != nil {
		t.Fatal(err)
	}
	if draftTable != 1 {
		t.Fatal("environment_schema_drafts missing after upgrade to 8")
	}
	var nullable string
	var defaultValue *string
	if err := upgradePool.QueryRow(ctx, `
		SELECT is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'module_releases'
		  AND column_name = 'published_at'`).Scan(&nullable, &defaultValue); err != nil {
		t.Fatal(err)
	}
	if nullable != "YES" || defaultValue != nil {
		t.Fatalf("upgraded publication column nullable=%q default=%v", nullable, defaultValue)
	}
	if _, err := upgradePool.Exec(ctx, `
		INSERT INTO module_releases (id, module_slug, version, runtime_compat, api_contract_version)
		VALUES ($1, 'core', '2.0.0', '>=1.0.0', '1')`, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatalf("upgrade should create unpublished drafts by default: %v", err)
	}
}
