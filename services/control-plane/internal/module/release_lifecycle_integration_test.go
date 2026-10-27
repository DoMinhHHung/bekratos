package module

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/project"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReleaseLifecyclePostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
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
		t.Fatalf("published_at schema nullable=%q default=%v", nullable, defaultValue)
	}
	var seededPublished int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM module_releases
		WHERE module_slug IN ('core', 'auth') AND published_at IS NOT NULL`).Scan(&seededPublished); err != nil {
		t.Fatal(err)
	}
	if seededPublished < 3 {
		t.Fatalf("seeded published releases missing: count=%d", seededPublished)
	}

	repo := NewRepository(pool)
	suffix := uuid.NewString()
	moduleSlug := "lifecycle-" + suffix
	peerSlug := "lifecycle-peer-" + suffix
	conflictSlug := "lifecycle-conflict-" + suffix
	draftOnlySlug := "lifecycle-draft-only-" + suffix
	for _, slug := range []string{moduleSlug, peerSlug, conflictSlug, draftOnlySlug} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO modules (slug, display_name) VALUES ($1, $1)`, slug); err != nil {
			t.Fatal(err)
		}
	}

	draftID := uuid.Must(uuid.NewV7())
	if err := repo.CreateDraft(ctx, Release{
		ID:                 draftID,
		ModuleSlug:         moduleSlug,
		Version:            "1.0.0",
		RuntimeCompat:      ">=1.0.0",
		APIContractVersion: "1",
		Dependencies: []Dependency{{
			ModuleSlug: peerSlug, VersionConstraint: "^1.0.0", Required: true,
		}},
		Conflicts: []string{conflictSlug},
	}); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	var draftPublishedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT published_at FROM module_releases WHERE id = $1`, draftID).Scan(&draftPublishedAt); err != nil {
		t.Fatal(err)
	}
	if draftPublishedAt != nil {
		t.Fatalf("draft created published: %v", draftPublishedAt)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE module_releases SET metadata = '{"draft_edit":true}'::jsonb WHERE id = $1`, draftID); err != nil {
		t.Fatalf("update draft release: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE module_release_dependencies
		SET version_constraint = '>=1.0.0', required = false
		WHERE release_id = $1 AND dep_module_slug = $2`, draftID, peerSlug); err != nil {
		t.Fatalf("update draft dependency: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM module_release_conflicts WHERE release_id = $1`, draftID); err != nil {
		t.Fatalf("delete draft conflict: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO module_release_conflicts (release_id, conflict_module_slug) VALUES ($1, $2)`,
		draftID, conflictSlug); err != nil {
		t.Fatalf("add draft conflict: %v", err)
	}

	draftOnlyID := uuid.Must(uuid.NewV7())
	if err := repo.CreateDraft(ctx, Release{
		ID: draftOnlyID, ModuleSlug: draftOnlySlug, Version: "1.0.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "1",
	}); err != nil {
		t.Fatalf("create draft-only module release: %v", err)
	}
	moduleService := NewService(repo)
	modules, err := moduleService.ListModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		if module.Slug == draftOnlySlug {
			t.Fatal("public module listing included a module with only drafts")
		}
	}
	if _, err := moduleService.ResolveSelections(ctx, []Selection{{ModuleSlug: draftOnlySlug}}); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("draft-only module selected in production resolution: %v", err)
	}
	releases, err := repo.ListReleases(ctx, moduleSlug)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("public release listing exposed draft: %+v", releases)
	}
	deletableDraftID := uuid.Must(uuid.NewV7())
	if err := repo.CreateDraft(ctx, Release{
		ID: deletableDraftID, ModuleSlug: draftOnlySlug, Version: "1.1.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "1",
		Dependencies: []Dependency{{ModuleSlug: peerSlug, VersionConstraint: "^1.0.0", Required: true}},
		Conflicts:    []string{conflictSlug},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM module_releases WHERE id = $1`, deletableDraftID); err != nil {
		t.Fatalf("delete unpublished draft and its metadata: %v", err)
	}

	invalidSlug := "lifecycle-invalid-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO modules (slug, display_name) VALUES ($1, $1)`, invalidSlug); err != nil {
		t.Fatal(err)
	}
	invalidID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
		INSERT INTO module_releases (
			id, module_slug, version, runtime_compat, api_contract_version, metadata, published_at
		) VALUES ($1, $2, '1.0.0', '', '0', '{}'::jsonb, NULL)`,
		invalidID, invalidSlug); err != nil {
		t.Fatalf("insert invalid draft for publication test: %v", err)
	}
	if _, err := repo.PublishRelease(ctx, invalidID); !errors.Is(err, ErrInvalidRelease) {
		t.Fatalf("invalid release publication error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE module_releases SET published_at = now() WHERE id = $1`, invalidID); err == nil {
		t.Fatal("database allowed publication with invalid metadata")
	}
	if err := pool.QueryRow(ctx, `SELECT published_at FROM module_releases WHERE id = $1`, invalidID).Scan(&draftPublishedAt); err != nil {
		t.Fatal(err)
	}
	if draftPublishedAt != nil {
		t.Fatal("failed publication left partial published state")
	}
	invalidConstraintID := uuid.Must(uuid.NewV7())
	invalidConstraintSlug := "lifecycle-invalid-constraint-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO modules (slug, display_name) VALUES ($1, $1)`, invalidConstraintSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO module_releases (
			id, module_slug, version, runtime_compat, api_contract_version
		) VALUES ($1, $2, '1.0.0', '>=1.0.0', '1')`,
		invalidConstraintID, invalidConstraintSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO module_release_dependencies (release_id, dep_module_slug, version_constraint)
		VALUES ($1, $2, 'not a version constraint')`, invalidConstraintID, peerSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PublishRelease(ctx, invalidConstraintID); !errors.Is(err, ErrInvalidRelease) {
		t.Fatalf("invalid dependency constraint publication error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE module_releases SET published_at = now() WHERE id = $1`, invalidConstraintID); err == nil {
		t.Fatal("database allowed publication with invalid dependency constraint")
	}
	if err := pool.QueryRow(ctx, `SELECT published_at FROM module_releases WHERE id = $1`, invalidConstraintID).Scan(&draftPublishedAt); err != nil {
		t.Fatal(err)
	}
	if draftPublishedAt != nil {
		t.Fatal("invalid constraint publication left partial state")
	}

	publishedAt, err := repo.PublishRelease(ctx, draftID)
	if err != nil {
		t.Fatalf("publish valid release: %v", err)
	}
	if publishedAt.IsZero() {
		t.Fatal("publication returned zero timestamp")
	}
	if _, err := repo.PublishRelease(ctx, draftID); !errors.Is(err, ErrReleaseAlreadyPublished) {
		t.Fatalf("second publication error = %v", err)
	}
	releases, err = repo.ListReleases(ctx, moduleSlug)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].PublishedAt == nil || releases[0].PublishedAt.IsZero() {
		t.Fatalf("published listing mismatch: %+v", releases)
	}
	if len(releases[0].Dependencies) != 1 || releases[0].Dependencies[0].VersionConstraint != ">=1.0.0" {
		t.Fatalf("published dependency metadata mismatch: %+v", releases[0].Dependencies)
	}
	if len(releases[0].Conflicts) != 1 || releases[0].Conflicts[0] != conflictSlug {
		t.Fatalf("published conflict metadata mismatch: %+v", releases[0].Conflicts)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO module_releases (id, module_slug, version, runtime_compat, api_contract_version)
		VALUES ($1, $2, '1.0.0+build.1', '>=1.0.0', '1')`,
		uuid.Must(uuid.NewV7()), moduleSlug); err == nil {
		t.Fatal("database accepted a release with SemVer-equivalent build metadata")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO module_releases (id, module_slug, version, runtime_compat, api_contract_version)
		VALUES ($1, $2, 'v1.0.1', '>=1.0.0', '1')`,
		uuid.Must(uuid.NewV7()), moduleSlug); err == nil {
		t.Fatal("database accepted non-canonical leading-v SemVer")
	}

	mutations := []struct {
		name string
		sql  string
		args []any
	}{
		{"dependency insert", `INSERT INTO module_release_dependencies (release_id, dep_module_slug, version_constraint) VALUES ($1, $2, '^1.0.0')`, []any{draftID, conflictSlug}},
		{"dependency update", `UPDATE module_release_dependencies SET required = true WHERE release_id = $1`, []any{draftID}},
		{"dependency delete", `DELETE FROM module_release_dependencies WHERE release_id = $1`, []any{draftID}},
		{"conflict insert", `INSERT INTO module_release_conflicts (release_id, conflict_module_slug) VALUES ($1, $2)`, []any{draftID, peerSlug}},
		{"conflict update", `UPDATE module_release_conflicts SET conflict_module_slug = $2 WHERE release_id = $1`, []any{draftID, peerSlug}},
		{"conflict delete", `DELETE FROM module_release_conflicts WHERE release_id = $1`, []any{draftID}},
		{"release update", `UPDATE module_releases SET metadata = '{}'::jsonb WHERE id = $1`, []any{draftID}},
		{"release delete", `DELETE FROM module_releases WHERE id = $1`, []any{draftID}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, mutation.sql, mutation.args...); err == nil {
				t.Fatalf("published mutation succeeded: %s", mutation.name)
			}
		})
	}

	runtimeMismatch := Release{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "runtime-target-" + suffix, Version: "1.0.0",
		RuntimeCompat: ">=99.0.0", APIContractVersion: "1",
	}
	apiMismatch := Release{
		ID: uuid.Must(uuid.NewV7()), ModuleSlug: "api-target-" + suffix, Version: "1.0.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "2",
	}
	for _, release := range []Release{runtimeMismatch, apiMismatch} {
		if _, err := pool.Exec(ctx, `INSERT INTO modules (slug, display_name) VALUES ($1, $1)`, release.ModuleSlug); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateDraft(ctx, release); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PublishRelease(ctx, release.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := moduleService.ResolveSelections(ctx, []Selection{{ModuleSlug: runtimeMismatch.ModuleSlug}}); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("production resolver accepted runtime-incompatible release: %v", err)
	}
	if _, err := moduleService.ResolveSelections(ctx, []Selection{{ModuleSlug: apiMismatch.ModuleSlug}}); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("production resolver accepted API-incompatible release: %v", err)
	}

	if err := testConcurrentReleaseMetadataPublication(ctx, t, pool, repo, suffix, peerSlug); err != nil {
		t.Fatal(err)
	}
}

func testConcurrentReleaseMetadataPublication(ctx context.Context, t *testing.T, pool *pgxpool.Pool, repo *Repository, suffix, dependencySlug string) error {
	t.Helper()
	releaseID := uuid.Must(uuid.NewV7())
	moduleSlug := "lifecycle-race-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO modules (slug, display_name) VALUES ($1, $1)`, moduleSlug); err != nil {
		return err
	}
	if err := repo.CreateDraft(ctx, Release{
		ID: releaseID, ModuleSlug: moduleSlug, Version: "1.0.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "1",
	}); err != nil {
		return err
	}

	writerName := "release-race-" + suffix
	config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL_TEST"))
	if err != nil {
		return err
	}
	config.ConnConfig.RuntimeParams["application_name"] = writerName
	writer, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}
	defer writer.Close()

	publishTx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer publishTx.Rollback(ctx)
	var publishedAt *time.Time
	if err := publishTx.QueryRow(ctx, `SELECT published_at FROM module_releases WHERE id = $1 FOR UPDATE`, releaseID).Scan(&publishedAt); err != nil {
		return err
	}
	if publishedAt != nil {
		return fmt.Errorf("new release unexpectedly published")
	}

	insertStarted := make(chan struct{})
	insertResult := make(chan error, 1)
	go func() {
		tx, err := writer.Begin(ctx)
		if err != nil {
			insertResult <- err
			return
		}
		defer tx.Rollback(ctx)
		close(insertStarted)
		_, err = tx.Exec(ctx, `
			INSERT INTO module_release_dependencies (release_id, dep_module_slug, version_constraint)
			VALUES ($1, $2, '^1.0.0')`, releaseID, dependencySlug)
		if err == nil {
			err = tx.Commit(ctx)
		}
		insertResult <- err
	}()
	<-insertStarted

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for {
		var blocked bool
		if err := pool.QueryRow(waitCtx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock'
			)`, writerName).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			break
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("metadata insert did not wait for publication row lock: %w", waitCtx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	if _, err := publishTx.Exec(ctx, `UPDATE module_releases SET published_at = now() WHERE id = $1`, releaseID); err != nil {
		return err
	}
	if err := publishTx.Commit(ctx); err != nil {
		return err
	}
	if err := <-insertResult; err == nil {
		return fmt.Errorf("metadata insert committed after publication")
	}
	return nil
}

func TestPinsEnforceCompatibilityAndActiveParentLifecycle(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL_TEST")
	if dsn == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	userID := uuid.Must(uuid.NewV7())
	principal := auth.Principal{UserID: userID, SessionID: uuid.Must(uuid.NewV7())}
	orgRepo := org.NewRepository(pool)
	orgService := org.NewService(orgRepo)
	organization, err := orgService.CreateOrganization(ctx, principal, org.CreateOrgInput{
		Slug: "pin-policy-" + userID.String()[24:], DisplayName: "Pin policy",
	}, "pin-policy")
	if err != nil {
		t.Fatal(err)
	}
	projectRepo := project.NewRepository(pool)
	projectService := project.NewService(projectRepo, orgRepo)
	firstProject, firstEnvironment, err := projectService.CreateProject(ctx, principal, organization.ID, project.CreateProjectInput{
		Slug: "active-project", DisplayName: "Active project",
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(NewRepository(pool))
	revision := int64(0)
	plan, err := svc.SetPins(ctx, principal, organization.ID, firstProject.ID, firstEnvironment.ID,
		[]Selection{{ModuleSlug: "core"}}, &revision, "pin-policy")
	if err != nil {
		t.Fatalf("pin compatible release: %v", err)
	}
	if len(plan.Releases) != 1 || plan.Releases[0].ModuleSlug != "core" || plan.Releases[0].Version != "1.0.0" {
		t.Fatalf("pin plan is not compatible with Phase 5 target: %+v", plan.Releases)
	}
	pins, _, err := svc.ListPins(ctx, principal, organization.ID, firstProject.ID, firstEnvironment.ID)
	if err != nil || len(pins) != 1 || pins[0].ReleaseID.String() != plan.Releases[0].ReleaseID {
		t.Fatalf("list compatible pin: pins=%+v err=%v", pins, err)
	}

	suffix := uuid.NewString()
	draftSlug := "pin-draft-" + suffix
	if _, err := pool.Exec(ctx, `INSERT INTO modules (slug, display_name) VALUES ($1, $1)`, draftSlug); err != nil {
		t.Fatal(err)
	}
	draftID := uuid.Must(uuid.NewV7())
	if err := NewRepository(pool).CreateDraft(ctx, Release{
		ID: draftID, ModuleSlug: draftSlug, Version: "1.0.0",
		RuntimeCompat: ">=1.0.0", APIContractVersion: "1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO environment_module_pins (environment_id, module_slug, release_id)
		VALUES ($1, $2, $3)`, firstEnvironment.ID, draftSlug, draftID); err == nil {
		t.Fatal("database allowed pinning an unpublished draft")
	}

	otherProject, otherEnvironment, err := projectService.CreateProject(ctx, principal, organization.ID, project.CreateProjectInput{
		Slug: "other-project", DisplayName: "Other project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ListPins(ctx, principal, organization.ID, firstProject.ID, otherEnvironment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project environment was accepted: %v", err)
	}
	if err := projectService.ArchiveEnvironment(ctx, principal, organization.ID, firstProject.ID, firstEnvironment.ID); err != nil {
		t.Fatalf("archive active environment: %v", err)
	}
	if _, _, err := svc.ListPins(ctx, principal, organization.ID, firstProject.ID, firstEnvironment.ID); !errors.Is(err, ErrInactive) {
		t.Fatalf("archived environment remained readable: %v", err)
	}
	if err := projectService.ArchiveProject(ctx, principal, organization.ID, otherProject.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ListPins(ctx, principal, organization.ID, otherProject.ID, otherEnvironment.ID); !errors.Is(err, ErrInactive) {
		t.Fatalf("archived project remained readable: %v", err)
	}
	if err := testConcurrentPinReadAndProjectArchive(ctx, t, dsn, pool, userID, organization.ID); err != nil {
		t.Fatal(err)
	}
	if err := orgService.ArchiveOrganization(ctx, principal, organization.ID, "pin-policy"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ListPins(ctx, principal, organization.ID, firstProject.ID, firstEnvironment.ID); !errors.Is(err, ErrInactive) {
		t.Fatalf("archived organization remained readable: %v", err)
	}
}

func testConcurrentPinReadAndProjectArchive(ctx context.Context, t *testing.T, dsn string, pool *pgxpool.Pool, userID, organizationID uuid.UUID) error {
	t.Helper()
	projectID := uuid.Must(uuid.NewV7())
	envID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
		INSERT INTO projects (
			id, organization_id, slug, display_name, status, created_by_identity_user_id
		) VALUES ($1, $2, $3, 'Concurrent archive', 'active', $4)`,
		projectID, organizationID, "concurrent-"+projectID.String()[24:], userID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_environments (id, project_id, slug, kind, status)
		VALUES ($1, $2, 'development', 'development', 'active')`, envID, projectID); err != nil {
		return err
	}

	readPoolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return err
	}
	applicationName := "pin-read-" + uuid.NewString()
	readPoolConfig.ConnConfig.RuntimeParams["application_name"] = applicationName
	readPool, err := pgxpool.NewWithConfig(ctx, readPoolConfig)
	if err != nil {
		return err
	}
	defer readPool.Close()
	reader := NewService(NewRepository(readPool))

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		SELECT identity_user_id FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`, organizationID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE projects SET status = 'archived', archived_at = now()
		WHERE id = $1`, projectID); err != nil {
		return err
	}

	readResult := make(chan error, 1)
	go func() {
		_, _, err := reader.ListPins(ctx,
			auth.Principal{UserID: userID, SessionID: uuid.Must(uuid.NewV7())},
			organizationID, projectID, envID)
		readResult <- err
	}()
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for {
		var blocked bool
		if err := pool.QueryRow(waitCtx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock'
			)`, applicationName).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			break
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("pin read did not serialize with parent archive: %w", waitCtx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := <-readResult; !errors.Is(err, ErrInactive) {
		return fmt.Errorf("read after serialized parent archive error=%v, want inactive", err)
	}
	return nil
}
