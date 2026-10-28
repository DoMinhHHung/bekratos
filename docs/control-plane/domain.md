# Bekratos Control Plane — Domain Design

Status: MVP design proposal, 2026-10-26.
Service: services/control-plane/.
Language: Go; verify the patched stable release at implementation time.
Primary store: PostgreSQL. HTTP: net/http. PostgreSQL driver: pgx/v5.

## 1. Service boundaries

Bekratos is a no-code/low-code Backend-as-a-Service platform. A user creates an organization and project, enables modules, configures entities/fields/relationships and access policies, then receives a runtime API URL and project credentials.

- Dashboard: UI for both non-technical users and developers.
- Identity Service: credentials, platform sessions and platform tokens.
- Control Plane: organizations, memberships, projects, environments, module catalog/config, schema metadata, configuration versions, API keys, deployment intent/state and control-plane audit.
- Backend Runtime/Data Plane: authenticates project credentials and project end-users, serves app CRUD, validates data and enforces policies.

Control Plane manages desired state and lifecycle. It must not proxy every runtime CRUD request.

## 2. Identity boundaries

Platform users are people who sign into Bekratos Dashboard. Their credentials, sessions and platform JWTs belong to Identity. Control Plane stores only external identity_user_id references and platform membership records; it does not duplicate passwords, refresh tokens or session tables.

Project end-users are customers of apps built on Bekratos. Their accounts and sessions belong to each project's Runtime Auth module, scoped to project and environment. They are not platform members.

The Identity API publishes JWKS at GET /v1/jwks. Its ES256 access-token contract includes sub, sid, iss, aud, iat, nbf and exp. Control Plane also calls the versioned GET /v1/internal/session contract with the user Bearer token and X-Control-Plane-Service-Token; Identity validates the service credential, JWT, active session, account status and verified primary email against PostgreSQL. Control Plane compares the returned subject and session ID with the JWT principal and fails closed on denial, timeout, malformed/oversized responses or upstream failure. The contract is implemented in this repository, but must still be verified between deployed services before production. The separate JWT policy uses MAX_REVOCATION_DELAY as the maximum acceptance window from iat; this is not a substitute for the active-session lookup. Never trust X-User-ID or an unverified email supplied by a caller.

`JWKS_CACHE_TTL` accepts configured durations up to 24 hours for compatibility, but the effective fresh-key age is capped at five minutes. Failed refreshes may use a cached key only within the provider's stale-if-error window (at most three times `JWKS_CACHE_TTL`) and strictly before that five-minute hard limit; at or beyond the limit, verification fails closed.

## 3. Core decisions

- PostgreSQL is the Control Plane source of truth. Do not add Redis in the foundation phase without a demonstrated need.
- Generate UUIDv7 in the application; persist timestamps as TIMESTAMPTZ in UTC.
- Shared runtime is default. Dedicated runtime is a later target behind a real isolation and deployment design.
- Module releases are immutable and pinned by projects; dependency resolution is deterministic.
- Configuration versions are immutable snapshots. Save creates a candidate. Compatible changes may auto-apply; the active pointer moves only after validation and apply succeed.
- Breaking/destructive changes require explicit confirmation and a safe migration plan. Failed candidates never replace the active healthy version.
- No arbitrary user SQL, scripts, code execution or eval in schema metadata/policies. Policies use a typed AST with an allow-listed operator set.
- System fields and module-owned behavior cannot be removed or overridden by custom schema.
- Never mark a deployment ready unless a real Runtime/provider confirms readiness.

## 4. Aggregates and entities

### Organization

Organization fields: id, slug, display_name, status, created_by_identity_user_id, created_at, updated_at, archived_at. Status values: active, suspended, archived. Slug is normalized and unique.

Create the organization and its initial owner membership in one transaction. Do not physically delete an organization while it owns projects, environments, keys or deployments. Archive/suspend first.

OrganizationMembership fields: organization_id, identity_user_id, role, status, created_at, updated_at. Unique organization_id + identity_user_id. MVP roles: owner, admin, developer, viewer, billing. The permission matrix is explicit in Control Plane code, not added to Identity JWT claims. Every organization must retain at least one active owner; transactions/locks protect the last owner. Check current membership from PostgreSQL for protected actions.

OrganizationInvitation fields: id, organization_id, email_normalized, role, token_hash, invited_by_identity_user_id, expires_at, accepted_at, revoked_at, created_at. Store only a hash of a high-entropy one-time token. Acceptance requires authenticated platform principal and a trusted verified-email contract; request-body email is not proof.

### Project and Environment

Project fields: id, organization_id, slug, display_name, description, status, created_by_identity_user_id, timestamps, archived_at. Unique organization_id + slug. Project is a primary ownership/quota boundary. A child route requires active organization, project and environment state; reads do not remain available after a parent or environment is archived.

Environment fields: id, project_id, slug, kind, status, active_configuration_version_id, active_deployment_id, timestamps. Kinds: development, staging, production, custom. Unique project_id + slug. Runtime data, keys, configuration versions and deployments are environment-scoped. Never authorize based on a caller-supplied project/environment ID without checking ownership. MVP may create one development environment automatically but schema should support multiple.

### Module catalog

Module is a stable identity such as auth, ecommerce, payment or storage. A release is authored as an unpublished draft, which may be edited with dependency/conflict metadata until its one-way publication transition. Publication validates the release and its metadata atomically; published releases and their dependency/conflict metadata are immutable and identified by module slug + canonical semver. Drafts are not included in public module/release listings, resolution candidates or pins. A release defines runtime compatibility, API contract version, config schema/defaults, entity templates, protected fields, generated endpoint declarations, required/optional dependencies, conflicts, migrations, policy templates and documentation.

The Phase 5 Control Plane target is global and server-authoritative: runtime version `1.0.0` and API contract version `1`. Resolution and pin validation use those same values; callers cannot select a different compatibility target. A future target change requires a coordinated runtime/API release and an update to this contract.

For MVP only reviewed first-party releases are trusted. Module migrations are reviewed program artifacts; modules cannot embed arbitrary user SQL/code.

Enable operations resolve and pin compatible releases. Reject dependency cycles and incompatible versions. Reject disabling a module with active dependants until those dependants are disabled/reconfigured. Disabling a module deactivates its API surface after successful activation but does not silently delete its data. Users may add custom fields to module-owned entities but cannot change protected fields or module-owned behavior.

### Entity, fields and relationships

Entity definitions have a stable ID, slug, display name, origin (system, module, custom), module release ownership where applicable, timestamps and lifecycle state.

Field definitions include stable ID/key, data type, required/default/validation rules, ownership, mutability and optional indexing/uniqueness capability. System fields such as id, tenant/environment scope and system timestamps are protected. Custom scalar fields must not override reserved names or change a protected field's type.

Relationships are first-class definitions, not untyped references hidden in JSON. A relationship records source entity/field, target entity, cardinality, nullability, delete behavior and owning module/dependency. Validate endpoints and targets against the complete candidate manifest. Enforce environment/project scope and define actual integrity behavior for the Runtime. Where database-level foreign keys are not used, the Runtime must validate referential behavior transactionally; an ID in JSONB alone is not proof of integrity.

MVP storage contract:
- Built-in/module entities use physical columns for their core fields and may store custom scalar extension values in JSONB.
- Custom entities use a generic record store, logically scoped by project/environment/entity/record, with custom values in JSONB.
- Control Plane owns schema metadata, not runtime application records.
- Unique/index/query guarantees over JSONB custom fields are limited and explicitly documented. Do not claim every field has physical-column semantics.
- Store a canonical configuration manifest in configuration_versions as the source of truth. Avoid separately editable entity/field/relationship copies that can drift. Add normalized projections only when justified and rebuildable.

### Configuration and change application

ConfigurationVersion fields: id, environment_id, version_number, parent_version_id, status, manifest, manifest_sha256, created_by_identity_user_id, created_at, activated_at, failure_code. Suggested statuses: validating, queued, applying, active, failed, awaiting_confirmation, superseded. Only one active version per environment.

ConfigurationChangeJob fields: id, environment_id, candidate_version_id, status, attempt_count, idempotency_key, safe_summary, failure_code, created_at, updated_at, lease_until. Validate a complete candidate before scheduling application. Classify changes as compatible, backfill/migration-required, or breaking/destructive. Compatible metadata changes can auto-apply. Destructive changes require confirmation/migration. Serialize activation per environment and use optimistic concurrency. Keep active version on failure. Rollback means reactivating an old compatible config, not reversing irreversible data migrations.

### API keys

Separate these credential classes:
- Management auth: platform Identity JWT + current organization membership.
- Publishable project key: identifies project/environment; may be public but grants no blanket data access and does not prove a project end-user identity.
- Server-secret project key: high-entropy secret for trusted server-side use; never expose in browser code or public examples. It does not automatically bypass every policy.

ApiKey metadata includes id, environment_id, key_type, prefix/key_id, secret_hash, scopes, created_by_identity_user_id, created_at, last_used_at, expires_at, revoked_at and optional rotation group. Generate at least 256 bits of randomness, show secret only once, persist only a cryptographic hash/reviewed keyed hash, and never accept keys in query strings or log keys. Revocation/cache propagation must be bounded and documented. The Runtime resolves the key into a trusted environment context; caller-supplied IDs cannot override it.

### Deployment and audit

Deployment fields include id, environment_id, target_mode, runtime/config version, status, URL/host, region, provider reference, attempt, timestamps and sanitized failure code. Status values include queued, provisioning, ready, failed, suspending, suspended, deleting and deleted. Shared runtime is the MVP target. Dedicated runtime requires a real provider and verified isolation. Never report ready from a stub.

ControlPlaneAuditEvent is append-only at application level and captures actor platform ID, scope, event, target, request ID, timestamp and redacted metadata. Audit organization/member, invitation, project/environment, module, schema, key, deployment, and authorization events. Never log keys, tokens, passwords, private keys or unnecessary PII.

## 5. Suggested PostgreSQL model

Core relational tables: organizations, organization_memberships, organization_invitations, projects, project_environments, module_catalog, module_releases, configuration_versions, configuration_change_jobs, api_keys, deployments and control_plane_audit_events.

Use foreign keys/constraints for ownership, uniqueness, state and idempotency. Important unique keys: org slug; organization + project slug; project + environment slug; environment + version number; key prefix/id. Use transactions and row locks for create-org+owner, last-owner protection, invitation acceptance, key rotation/revocation, config activation and deployment transitions. Composite ownership checks must prevent resources from being linked across projects/environments.

## 6. Management API proposal

These are target routes, not implemented APIs. All protected routes require a validated platform principal and explicit organization/project authorization.

- GET /healthz and GET /readyz
- POST/GET organizations; GET/PATCH organization by ID
- GET/POST organization members; PATCH/DELETE member by identity user ID
- GET/POST organization invitations; revoke invitation
- GET/POST organization projects; GET/PATCH project
- GET/POST project environments
- GET modules and module releases
- GET/POST environment modules and guarded enable/disable
- GET/POST environment schema entities, fields and relationships
- GET environment configuration versions; inspect change jobs; confirm breaking changes
- GET/POST environment API keys; rotate/revoke keys
- GET/POST deployments and inspect status

Route details and OpenAPI must be frozen in the phase that implements each feature. Control Plane never exposes project record CRUD; Runtime does.

## 7. MVP exclusions

- Arbitrary user code, SQL, stored procedures and dynamic executable plugins.
- Third-party module marketplace; reviewed first-party modules only.
- Automatic unique/index creation for every JSONB custom field.
- Workflow engine, custom hooks, billing metering and analytics dashboards.
- Dedicated deployment until a concrete provider and isolation strategy exist.
- Reusing platform membership roles as project end-user roles.
- Any claim of production readiness without actual migrations, integration, concurrency and security evidence.

## 8. Release invariants

1. No cross-organization/project/environment read or mutation by changing a URL/body ID.
2. Current membership, not stale JWT roles, determines permissions.
3. Owner/admin can create, update and archive projects and environments; developer can create/update projects and create environments but cannot archive; viewer is read-only; billing has no project/environment access.
4. Platform and project end-user identities use separate types, audiences and credentials.
5. Management JWT validation includes fixed algorithm, signature, issuer, audience, expiry, not-before and key ID, plus explicit bounded token-expiry revocation semantics.
6. API-key/invitation plaintext is never stored or logged; secret shown once.
7. Module dependency/version resolution is deterministic.
8. System/module-owned fields cannot be removed or mutated.
9. Relationships cannot silently cross tenant/environment boundaries.
10. Failed candidate never replaces the active healthy config version.
11. Destructive operations are explicit and are not promised to be rollback-safe.
12. Deployment is not ready without real Runtime/provider confirmation.
13. Tests cover last-owner concurrency, invitation replay, key revocation, module conflicts, config version races, cross-tenant isolation and failed apply/deploy.
