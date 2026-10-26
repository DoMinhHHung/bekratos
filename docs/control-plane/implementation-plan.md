# Bekratos Control Plane — Implementation Plan

Một phase mỗi lần, branch riêng từ dev mới nhất, review trước khi chuyển phase tiếp theo. Control Plane tách khỏi Rust Identity Service và Backend Runtime. Không làm business logic trong phase foundation.

## Phase 0 — Repository and contracts

Inspect actual branch/tree, root README, Identity routes/JWKS claims, migration, CI and ops docs. Record gaps for active-session validation and verified-primary-email. Reconcile docs only.

Acceptance: boundaries, API-key trust model, schema/version lifecycle, security invariants and exclusions are documented; discrepancies are backed by source paths.

## Phase 1 — Go foundation

Create services/control-plane with pinned patched stable Go, net/http, pgx/v5, explicit versioned SQL migrations, executable, strict env config, JSON structured logging, safe errors, request IDs, request body limits, HTTP timeouts, graceful shutdown, health/readiness, DB pool and readiness ping, non-root multi-stage Dockerfile, disposable local PostgreSQL Compose and CI.

Acceptance: gofmt, go vet, go test, go test -race, migration tests, Docker build and DB-backed readiness tests run and results are reported accurately. No protected business routes or fake-success endpoints. Production migrations require a controlled deployment path.

## Phase 2 — Platform principal integration

Add JWT/JWKS validation with fixed algorithm allow-list, exact issuer/audience, exp/nbf/sub/sid/kid checks, bounded JWKS refresh and explicit active-session contract. Add principal context and authorization foundations. Never trust X-User-ID. Do not claim signature validation alone detects session revocation.

Blocking decision: add/test a trusted Identity session introspection contract or explicitly accept token-expiry-bounded revocation delay. Invitation acceptance needs trusted verified-primary-email lookup or a documented alternative.

## Phase 3 — Organizations and memberships

Create/list/get/archive org; create owner membership atomically; list/change/remove members; role matrix owner/admin/developer/viewer/billing; audit. Transactions/locks prevent removing/demoting the last owner.

Acceptance: tenant isolation, role matrix, duplicate/race cases and last-owner concurrency tested; no passwords/session data duplicated from Identity.

## Phase 4 — Projects and environments

Project lifecycle, default development environment, environment lifecycle and hierarchical ownership checks. Don't cascade-delete project data on archive.

Acceptance: comprehensive cross-org/project/environment tests; DB constraints reject invalid parent relationships.

## Phase 5 — Module catalog and dependency graph

Reviewed first-party modules/releases, immutable semver releases, pinned versions, required/optional dependency resolution, conflict/cycle detection. Disable only when dependants are safely reconfigured; retain data by default.

Acceptance: deterministic dependency resolution; cycles, conflicts and unsupported version combinations rejected.

## Phase 6 — Schema definitions

Typed definitions for built-in/module/custom entity origins, protected fields, scalar types, defaults, validation, relationships/cardinality/delete behavior and a small allow-listed access-policy AST. Control Plane manages metadata; Runtime owns data CRUD.

Storage MVP: physical core columns for built-in/module entities, custom scalar extension values in JSONB; generic JSONB record store for custom entities. Document unique/index/query limitations.

Acceptance: reject reserved field names, incompatible types, invalid targets, dependency violations and arbitrary SQL/eval in policies.

## Phase 7 — Configuration versions and auto-apply

Immutable canonical manifest snapshots, hashes, expected-version concurrency, idempotency keys, candidate validation, compatibility classifier, durable change jobs and active pointer switch only after successful apply. Compatible changes may auto-apply; breaking/destructive changes require confirmation/migration.

Acceptance: concurrent edits cannot silently overwrite each other; failed candidate leaves active version unchanged; retries are idempotent; no false promise that destructive data operations can be rolled back.

## Phase 8 — API-key lifecycle

Separate management JWT from publishable and server-secret project keys. Generate 256-bit random secrets; store hash only; show secret once; bind to environment; expiry, revocation, rotation, audit and documented cache propagation.

Acceptance: no plaintext storage/logging, no query-string keys, environment crossover rejected, secret displayed once, unauthorized roles cannot manage keys.

## Phase 9 — Deployment coordination

Create deployment intent/state and provider contract. Shared runtime is target. Implement real status/readiness checks; dedicated mode remains unavailable until provider/isolation are real.

Acceptance: never mark ready without affirmative Runtime/provider readiness; retries are idempotent; duplicate requests do not duplicate deployments; environment isolation is tested.

## Phase 10 — Integration hardening

Disposable PostgreSQL integration tests, migrations from clean/previous schema, concurrency/security tests, OpenAPI contracts, vulnerability and secret scans, Docker/CI review, operations docs, backup/restore, retention, alerts and incident response.

Acceptance: required commands ran and their real outputs are reported; high-risk tenant tests pass; no secrets leak; residual risks and deployment assumptions are explicit.

## First vertical slice

1. Authenticate platform user through the agreed Identity contract.
2. Create organization and owner membership in one transaction.
3. Create a project and development environment.
4. Enable one reviewed first-party module and inspect built-in entities.
5. Create a custom entity/field and a valid relationship.
6. Validate and store a new immutable configuration version.
7. Auto-apply compatible changes; require confirmation for breaking changes.
8. Issue publishable and server-secret keys with one-time display.
9. Integrate a real Runtime/provider and show API URL only when actual readiness is confirmed.

Do not claim the final step is complete until Runtime end-to-end isolation tests pass.
