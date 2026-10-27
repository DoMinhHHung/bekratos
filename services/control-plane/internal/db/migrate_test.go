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
	var has008, has009, has010, has011, has012 bool
	var maxVersion int64
	for _, f := range files {
		if f.version > maxVersion {
			maxVersion = f.version
		}
		switch f.version {
		case 8:
			has008 = true
		case 9:
			has009 = true
		case 10:
			has010 = true
		case 11:
			has011 = true
		case 12:
			has012 = true
		}
	}
	if !has008 {
		t.Fatal("expected migration version 8 (schema_drafts)")
	}
	if !has009 {
		t.Fatal("expected migration version 9 (config_versions)")
	}
	if !has010 {
		t.Fatal("expected migration version 10 (api_keys)")
	}
	if !has011 {
		t.Fatal("expected migration version 11 (deployments)")
	}
	if !has012 {
		t.Fatal("expected migration version 12 (deployment readiness evidence)")
	}
	if maxVersion < 12 {
		t.Fatalf("max migration version=%d, want >= 12", maxVersion)
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
	if latest < 12 {
		t.Fatalf("latest migration=%d, want >= 12", latest)
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

	var apiKeysTable int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'api_keys'`).Scan(&apiKeysTable); err != nil {
		t.Fatal(err)
	}
	if apiKeysTable != 1 {
		t.Fatal("api_keys missing after migrate")
	}

	var cfgTable int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'environment_configuration_versions'`).Scan(&cfgTable); err != nil {
		t.Fatal(err)
	}
	if cfgTable != 1 {
		t.Fatal("environment_configuration_versions missing after migrate")
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

	var fpCol int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'deployments'
  AND column_name = 'request_fingerprint'`).Scan(&fpCol); err != nil {
		t.Fatal(err)
	}
	if fpCol != 1 {
		t.Fatal("request_fingerprint column missing on deployments")
	}

	var readinessColumns int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'deployments'
  AND column_name IN ('readiness_probe_id', 'readiness_observed_at')`).Scan(&readinessColumns); err != nil {
		t.Fatal(err)
	}
	if readinessColumns != 2 {
		t.Fatalf("readiness evidence columns=%d, want 2", readinessColumns)
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
	var cfgTable int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'environment_configuration_versions'`).Scan(&cfgTable); err != nil {
		t.Fatal(err)
	}
	if cfgTable != 1 {
		t.Fatal("environment_configuration_versions missing after upgrade")
	}
	var apiKeysTable int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'api_keys'`).Scan(&apiKeysTable); err != nil {
		t.Fatal(err)
	}
	if apiKeysTable != 1 {
		t.Fatal("api_keys missing after upgrade")
	}
	var draftTable int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'environment_schema_drafts'`).Scan(&draftTable); err != nil {
		t.Fatal(err)
	}
	if draftTable != 1 {
		t.Fatal("environment_schema_drafts missing after upgrade")
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
		if migration.version > 9 {
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
		t.Fatalf("upgrade from schema version 9: %v", err)
	}
	var latest int64
	if err := upgradePool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest != 11 {
		t.Fatalf("latest migration=%d, want 11", latest)
	}

	var apiKeysTable int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'api_keys'`).Scan(&apiKeysTable); err != nil {
		t.Fatal(err)
	}
	if apiKeysTable != 1 {
		t.Fatal("api_keys missing after upgrade to 11")
	}

	var cfgTable int
	if err := upgradePool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'environment_configuration_versions'`).Scan(&cfgTable); err != nil {
		t.Fatal(err)
	}
	if cfgTable != 1 {
		t.Fatal("environment_configuration_versions missing after upgrade")
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
	var deployTable int
	if err := upgradePool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.tables
WHERE table_schema = current_schema() AND table_name = 'deployments'`).Scan(&deployTable); err != nil {
		t.Fatal(err)
	}
	if deployTable != 1 {
		t.Fatal("deployments missing after full upgrade path")
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
