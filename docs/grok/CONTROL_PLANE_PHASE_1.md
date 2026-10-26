# Grok task — Control Plane Phase 1: Go foundation

Read docs/control-plane/domain.md, docs/control-plane/implementation-plan.md, the root README, current services/identity source and CI before implementation. Implement only Phase 1 and stop.

## Goal

Create a runnable services/control-plane service using the currently patched stable Go release (verify https://go.dev/dl/ at implementation time), net/http, github.com/jackc/pgx/v5, PostgreSQL and explicit SQL migrations. Pin dependencies and explain important choices outside source code.

## Required scope

- go.mod/go.sum and executable under cmd/control-plane/
- environment-based config, fail-fast validation, no secret values in logs/errors
- structured logging, request IDs, safe error response
- request body limit, HTTP read/write/idle timeouts and graceful shutdown
- GET /healthz for process liveness
- GET /readyz performs a bounded real PostgreSQL ping and returns 503 if DB is unavailable
- PostgreSQL pool and versioned migrations; production migration execution must be an explicit controlled operation
- non-root multi-stage Dockerfile and Compose with disposable local PostgreSQL
- CI for gofmt, go vet, go test, go test -race, build, govulncheck and secret scanning with minimum permissions
- meaningful unit tests and disposable-PostgreSQL integration coverage for readiness and migrations where feasible

## Out of scope

No organizations, members, invitations, projects, modules, schema API, API keys, Runtime CRUD, fake deployment provider or assumed Identity integration. Do not add Redis without demonstrated need. Do not modify Identity behavior. Do not create empty files. Source code must contain no comments and no TODO/stub/placeholder/fake-success behavior.

## Process and deliverables

Before implementation, report verified repository facts, assumptions, planned files, acceptance criteria and threat model. Preserve existing user changes. Output complete file contents for every changed/new file with a clear path, then exact commands to validate. Distinguish tests actually executed from commands merely recommended. End with changed-file list, decisions, residual risks and one Conventional Commit message suggestion. Do not commit or push. Stop after Phase 1 for human review.
