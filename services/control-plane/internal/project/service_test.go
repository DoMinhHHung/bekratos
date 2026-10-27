package project

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/auth"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/db"
	"github.com/DoMinhHHung/bekratos/services/control-plane/internal/org"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	}, "test-request")
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
	}, "test-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(ctx, principal, otherOrg.ID, p.ID); err != ErrNotFound {
		t.Fatalf("cross-org path should 404: %v", err)
	}
}

func TestProjectMutationsFollowCanonicalRoles(t *testing.T) {
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

	ownerID := uuid.Must(uuid.NewV7())
	developerID := uuid.Must(uuid.NewV7())
	viewerID := uuid.Must(uuid.NewV7())
	billingID := uuid.Must(uuid.NewV7())
	orgRepo := org.NewRepository(pool)
	orgSvc := org.NewService(orgRepo)
	owner := auth.Principal{UserID: ownerID, SessionID: uuid.Must(uuid.NewV7())}
	o, err := orgSvc.CreateOrganization(ctx, owner, org.CreateOrgInput{
		Slug: "role-policy-" + ownerID.String()[24:], DisplayName: "Role policy",
	}, "role-policy")
	if err != nil {
		t.Fatal(err)
	}
	for userID, role := range map[uuid.UUID]string{
		developerID: "developer",
		viewerID:    "viewer",
		billingID:   "billing",
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO organization_memberships (
				organization_id, identity_user_id, role, status
			) VALUES ($1, $2, $3, 'active')`, o.ID, userID, role); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(NewRepository(pool), orgRepo)
	developer := auth.Principal{UserID: developerID, SessionID: uuid.Must(uuid.NewV7())}
	project, environment, err := svc.CreateProject(ctx, developer, o.ID, CreateProjectInput{
		Slug: "developer-created-" + developerID.String()[24:], DisplayName: "Developer-created",
	})
	if err != nil {
		t.Fatalf("developer create project: %v", err)
	}
	updatedName := "Developer updated"
	if _, err := svc.UpdateProject(ctx, developer, o.ID, project.ID, UpdateProjectInput{
		DisplayName: &updatedName,
	}); err != nil {
		t.Fatalf("developer update project: %v", err)
	}
	if err := svc.ArchiveProject(ctx, developer, o.ID, project.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("developer archive project error = %v, want forbidden", err)
	}
	if _, err := svc.CreateEnvironment(ctx, developer, o.ID, project.ID, CreateEnvironmentInput{
		Slug: "developer-environment", Kind: KindStaging,
	}); err != nil {
		t.Fatalf("developer create environment: %v", err)
	}
	if err := svc.ArchiveEnvironment(ctx, developer, o.ID, project.ID, environment.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("developer archive environment error = %v, want forbidden", err)
	}

	viewer := auth.Principal{UserID: viewerID, SessionID: uuid.Must(uuid.NewV7())}
	if _, _, err := svc.CreateProject(ctx, viewer, o.ID, CreateProjectInput{
		Slug: "viewer-created-" + viewerID.String()[24:], DisplayName: "Viewer-created",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create project error = %v, want forbidden", err)
	}
	if _, err := svc.UpdateProject(ctx, viewer, o.ID, project.ID, UpdateProjectInput{
		DisplayName: &updatedName,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer update project error = %v, want forbidden", err)
	}
	if _, err := svc.CreateEnvironment(ctx, viewer, o.ID, project.ID, CreateEnvironmentInput{
		Slug: "viewer-environment", Kind: KindStaging,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create environment error = %v, want forbidden", err)
	}
	if err := svc.ArchiveEnvironment(ctx, viewer, o.ID, project.ID, environment.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer archive environment error = %v, want forbidden", err)
	}
	if _, err := svc.ListProjects(ctx, viewer, o.ID); err != nil {
		t.Fatalf("viewer should be able to read projects: %v", err)
	}

	billing := auth.Principal{UserID: billingID, SessionID: uuid.Must(uuid.NewV7())}
	if _, err := svc.ListProjects(ctx, billing, o.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("billing read projects error = %v, want forbidden", err)
	}
	if _, err := svc.UpdateProject(ctx, billing, o.ID, project.ID, UpdateProjectInput{
		DisplayName: &updatedName,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("billing update project error = %v, want forbidden", err)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_audit_events
		WHERE organization_id = $1 AND event_type IN (
			'project.created', 'environment.created', 'project.updated',
			'project.archived', 'environment.archived'
		)`, o.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 {
		t.Fatalf("successful mutations should have four audit events, got %d", auditCount)
	}
}

func TestArchiveEnvironmentRequiresActiveParentInTransaction(t *testing.T) {
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

	userID := uuid.Must(uuid.NewV7())
	orgRepo := org.NewRepository(pool)
	orgSvc := org.NewService(orgRepo)
	principal := auth.Principal{UserID: userID, SessionID: uuid.Must(uuid.NewV7())}
	o, err := orgSvc.CreateOrganization(ctx, principal, org.CreateOrgInput{
		Slug: "archive-parent-" + userID.String()[24:], DisplayName: "Archive parent",
	}, "archive-parent")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewRepository(pool), orgRepo)
	project, env, err := svc.CreateProject(ctx, principal, o.ID, CreateProjectInput{
		Slug: "archive-project", DisplayName: "Archive project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveEnvironment(ctx, principal, o.ID, project.ID, env.ID); err != nil {
		t.Fatalf("active project should permit environment archive: %v", err)
	}

	activeProject, activeEnv, err := svc.CreateProject(ctx, principal, o.ID, CreateProjectInput{
		Slug: "archived-parent-project", DisplayName: "Archived parent project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE projects SET status = 'archived', archived_at = now()
		WHERE id = $1`, activeProject.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveEnvironment(ctx, principal, o.ID, activeProject.ID, activeEnv.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("archived parent should reject environment archive: %v", err)
	}
	if err := NewRepository(pool).ArchiveEnvironment(
		ctx, o.ID, activeProject.ID, activeEnv.ID, userID, "archive-parent",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("transactional repository should reject archived parent: %v", err)
	}

	var archiveEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_audit_events
		WHERE organization_id = $1 AND event_type = 'environment.archived'`, o.ID).Scan(&archiveEvents); err != nil {
		t.Fatal(err)
	}
	if archiveEvents != 1 {
		t.Fatalf("expected only the active-parent archive event, got %d", archiveEvents)
	}
}

func TestProjectMutationsSerializeWithRoleAndParentChanges(t *testing.T) {
	url := os.Getenv("DATABASE_URL_TEST")
	if url == "" {
		t.Skip("DATABASE_URL_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	ownerID := uuid.Must(uuid.NewV7())
	developerID := uuid.Must(uuid.NewV7())
	orgRepo := org.NewRepository(pool)
	orgSvc := org.NewService(orgRepo)
	owner := auth.Principal{UserID: ownerID, SessionID: uuid.Must(uuid.NewV7())}
	organization, err := orgSvc.CreateOrganization(ctx, owner, org.CreateOrgInput{
		Slug: "serialized-project-" + ownerID.String()[24:], DisplayName: "Serialized project",
	}, "serialized-project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, identity_user_id, role, status)
		VALUES ($1, $2, 'developer', 'active')`, organization.ID, developerID); err != nil {
		t.Fatal(err)
	}

	applicationName := "project-mutation-" + uuid.NewString()
	servicePoolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	servicePoolConfig.ConnConfig.RuntimeParams["application_name"] = applicationName
	servicePool, err := pgxpool.NewWithConfig(ctx, servicePoolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer servicePool.Close()
	serviceOrgRepo := org.NewRepository(servicePool)
	service := NewService(NewRepository(servicePool), serviceOrgRepo)

	developer := auth.Principal{UserID: developerID, SessionID: uuid.Must(uuid.NewV7())}
	project, _, err := NewService(NewRepository(pool), orgRepo).CreateProject(ctx, developer, organization.ID, CreateProjectInput{
		Slug: "preflight-race", DisplayName: "Original",
	})
	if err != nil {
		t.Fatal(err)
	}

	roleTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roleTx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, organization.ID); err != nil {
		_ = roleTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := roleTx.Exec(ctx, `
		UPDATE organization_memberships SET role = 'viewer'
		WHERE organization_id = $1 AND identity_user_id = $2`, organization.ID, developerID); err != nil {
		_ = roleTx.Rollback(ctx)
		t.Fatal(err)
	}

	displayName := "Must not apply"
	mutationResult := make(chan error, 1)
	go func() {
		_, err := service.UpdateProject(ctx, developer, organization.ID, project.ID, UpdateProjectInput{
			DisplayName: &displayName,
		})
		mutationResult <- err
	}()
	if err := waitForTransactionLock(ctx, pool, applicationName); err != nil {
		_ = roleTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := roleTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-mutationResult; !errors.Is(err, ErrForbidden) {
		t.Fatalf("mutation after role demotion error=%v, want forbidden", err)
	}
	unchanged, err := NewRepository(pool).GetProject(ctx, organization.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.DisplayName != "Original" {
		t.Fatalf("preflight authorization allowed stale-role update: %+v", unchanged)
	}

	ownerProject, ownerEnvironment, err := NewService(NewRepository(pool), orgRepo).CreateProject(ctx, owner, organization.ID, CreateProjectInput{
		Slug: "parent-race", DisplayName: "Parent race",
	})
	if err != nil {
		t.Fatal(err)
	}
	archiveTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archiveTx.Exec(ctx, `SELECT id FROM organizations WHERE id = $1 FOR UPDATE`, organization.ID); err != nil {
		_ = archiveTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := archiveTx.Exec(ctx, `
		SELECT identity_user_id FROM organization_memberships
		WHERE organization_id = $1 AND identity_user_id = $2 FOR UPDATE`, organization.ID, ownerID); err != nil {
		_ = archiveTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := archiveTx.Exec(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, ownerProject.ID); err != nil {
		_ = archiveTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := archiveTx.Exec(ctx, `
		UPDATE projects SET status = 'archived', archived_at = now()
		WHERE id = $1`, ownerProject.ID); err != nil {
		_ = archiveTx.Rollback(ctx)
		t.Fatal(err)
	}

	archiveResult := make(chan error, 1)
	go func() {
		archiveResult <- service.ArchiveEnvironment(ctx, owner, organization.ID, ownerProject.ID, ownerEnvironment.ID)
	}()
	if err := waitForTransactionLock(ctx, pool, applicationName); err != nil {
		_ = archiveTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := archiveTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-archiveResult; !errors.Is(err, ErrForbidden) {
		t.Fatalf("environment archive after parent archive error=%v, want forbidden", err)
	}
	var environmentStatus Status
	if err := pool.QueryRow(ctx, `SELECT status FROM project_environments WHERE id = $1`, ownerEnvironment.ID).Scan(&environmentStatus); err != nil {
		t.Fatal(err)
	}
	if environmentStatus != StatusActive {
		t.Fatalf("failed child mutation changed environment state: %s", environmentStatus)
	}
	var environmentArchiveEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_audit_events
		WHERE organization_id = $1 AND event_type = 'environment.archived'`, organization.ID).Scan(&environmentArchiveEvents); err != nil {
		t.Fatal(err)
	}
	if environmentArchiveEvents != 0 {
		t.Fatalf("failed child mutation wrote audit events: %d", environmentArchiveEvents)
	}
}

func waitForTransactionLock(ctx context.Context, pool *pgxpool.Pool, applicationName string) error {
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
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
			return nil
		}
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
