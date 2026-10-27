# Control Plane ↔ Backend Runtime HTTP Contract

## Current implementation status

The Control Plane owns authorization, deployment intent, immutable configuration-version selection, idempotency keys, durable state and audit. The current integration branch implements the HTTP clients/adapters for these Runtime APIs and keeps the unavailable provider as the default when Runtime credentials are absent.

**This repository still does not contain the Runtime server or a cloud/container infrastructure provider.** The adapters can be contract-tested against a fake HTTP server, but setting `RUNTIME_API_URL` does not create a real Runtime. Do not call the whole product flow end-to-end until the Runtime API and actual deployment executor are implemented and tested in a staging environment.

## Trust boundaries

- Control Plane is authoritative for organizations, projects, environments, deployment IDs, configuration-version IDs and deployment status.
- Runtime is authoritative for applied runtime configuration, actual provisioning and observed application readiness.
- The platform Identity JWT is only used to authorize Control Plane management requests. It is not a project end-user credential.
- Control Plane calls Runtime using a dedicated service bearer token. Runtime must not accept a platform user JWT as its service credential, and Runtime API keys must not authorize Control Plane management endpoints.
- Use HTTPS except for loopback-only development/test endpoints. Do not follow redirects for authenticated service requests.
- Validate every Runtime response as untrusted input. Do not return provider diagnostics, raw response bodies, tokens, authorization headers or stack traces to API clients.

## Shared HTTP rules

Runtime API base URL is configured by `RUNTIME_API_URL`; service credential is `RUNTIME_API_TOKEN`; timeout is `RUNTIME_HTTP_TIMEOUT`. URL and token must be configured together. The adapter stays unavailable and fails closed when either is missing. Production URL must use HTTPS.

All requests must use:
- `Authorization: Bearer <runtime-service-token>`
- `Content-Type: application/json`
- `Accept: application/json`
- `Idempotency-Key: <stable request id>` for side-effecting operations.

Use `contract_version: "v1"` in request JSON. Runtime must authenticate the caller, scope every operation to the exact environment, enforce payload size limits, reject unknown or malformed identifiers, and never execute arbitrary SQL, shell or user code from the manifest. Keep secrets and end-user data out of logs.

Responses must be bounded JSON. A redirect is not followed. Request timeouts, transport errors, malformed successful responses and server errors are ambiguous; Control Plane must not blindly submit the operation under a new idempotency key.

## Configuration apply

Control Plane calls `POST /v1/internal/configuration/apply`.

Request:
- `contract_version`: `"v1"`
- `environment_id`: UUID
- `configuration_version_id`: immutable configuration version UUID
- `manifest_format_version`: integer
- `manifest_sha256`: SHA-256 of the exact canonical manifest
- `manifest`: canonical JSON object, not a string containing JSON
- `apply_request_id`: stable unique operation ID, unchanged on safe retries
- `expected_active_version_id`: UUID or null for compare-and-swap against the prior active version.

Runtime must atomically deduplicate `apply_request_id`, reject reuse with different payload, verify the canonical manifest hash itself, validate the manifest/schema/module pins, and compare the currently active version to `expected_active_version_id` before applying. A stale base must be rejected without silently rebasing.

Successful acknowledgement JSON:
```json
{
  "accepted": true,
  "environment_id": "00000000-0000-7000-8000-000000000001",
  "applied_configuration_version_id": "00000000-0000-7000-8000-000000000002",
  "applied_sha256": "<exact manifest_sha256>",
  "expected_active_version_id": null,
  "needs_reconciliation": false
}
```

The acknowledgement must reflect observed Runtime state, not merely acceptance by a background queue. `accepted=false` may include a stable allowlisted `code`. If an operation may have been accepted but cannot be confirmed, return a 2xx response with `accepted=true` and `needs_reconciliation=true` only when the operation identity is durable, or return a server error that the caller will treat as ambiguous. Never claim the new active pointer unless the expected version/hash and base all match.

A `429` may be returned only when Runtime guarantees it has not accepted or started the operation; only then is the Control Plane allowed to retry with the same `apply_request_id`. A transport timeout or `5xx` is ambiguous and moves the job to reconciliation-required state rather than automatic retry.

## Deployment provision

Control Plane calls `POST /v1/internal/deployments`.

Request:
```json
{
  "contract_version": "v1",
  "deployment_id": "00000000-0000-7000-8000-000000000010",
  "environment_id": "00000000-0000-7000-8000-000000000011",
  "configuration_version_id": "00000000-0000-7000-8000-000000000012",
  "provider_request_id": "dep-00000000-0000-7000-8000-000000000010",
  "target_mode": "shared"
}
```

Runtime must deduplicate requests by `provider_request_id`. Repeating the same request and payload returns the same operation. Reusing the key with a different payload returns conflict and must not create a second operation. Runtime must fetch/verify the exact immutable configuration version and apply it to the target associated with that environment; it must not trust a client-supplied URL or treat IDs as authorization by themselves.

A terminal success response must include:
- `outcome: "success"`
- matching `deployment_id`, `environment_id`, `configuration_version_id` and `provider_request_id`
- `readiness_probe_passed: true`
- non-empty `readiness_probe_id`
- recent RFC3339 `readiness_observed_at`
- `url` and/or `host` identifying the actual deployed target; the Control Plane requires HTTPS URL/normalized host validation before accepting `ready`.

Example:
```json
{
  "outcome": "success",
  "deployment_id": "00000000-0000-7000-8000-000000000010",
  "environment_id": "00000000-0000-7000-8000-000000000011",
  "configuration_version_id": "00000000-0000-7000-8000-000000000012",
  "provider_request_id": "dep-00000000-0000-7000-8000-000000000010",
  "url": "https://app.example.test",
  "host": "app.example.test",
  "region": "ap-southeast-1",
  "readiness_probe_passed": true,
  "readiness_probe_id": "probe-opaque-id",
  "readiness_observed_at": "2026-10-27T12:00:00Z"
}
```

Other supported `outcome` values are `failure`, `retryable`, `ambiguous` and `unavailable`. Runtime must return only sanitized `code` values from a documented allowlist; the Control Plane ignores provider-provided public messages and emits generic user-facing messages.

A `429` means the request was rejected before the operation was accepted; it is the only current HTTP failure treated as retryable by the adapter. `408`, network failures, redirects, malformed 2xx responses and `5xx` responses are ambiguous. Ambiguous outcomes must not be blindly repeated with a new key.

## Reconciliation endpoint required before production

The Runtime must additionally implement an authenticated operation lookup:
`GET /v1/internal/operations/by-request-id/{provider_request_id}`

It must return the durable operation state and original IDs/readiness evidence for that exact idempotency key, or a definitive `404` only when the Runtime can guarantee the operation was never accepted. A `404` must not be used while an operation is merely not yet visible. The current Control Plane adapter does not yet run an automatic deployment reconciler; until that exists and is tested, ambiguous deployments end as a safe terminal failure for operator handling. Configuration apply ambiguity is stored as reconciliation-required, but automatic lookup/replay is not implemented.

## Readiness and state transitions

A hostname or successful allocation is not readiness. The probe must reach the actual target and verify the expected environment and immutable configuration version. The Control Plane rejects missing, stale, future-dated or mismatched evidence; evidence must be recent (at most two minutes old, with a five-second future skew) when persisted. It persists probe ID and observation time atomically with the `ready` transition and the audit entry.

Local worker leases and fencing tokens only protect Control Plane ownership; remote idempotency protects side effects that outlive a worker lease. Terminal transitions (`ready`, `failed`, retry schedule and reconciliation-needed apply outcome) must be auditable and public errors must remain generic.

## Production enablement checklist

Do not enable `RUNTIME_API_URL` in production until the Runtime service and its real executor are present and all of the following pass in staging:

1. Authentication, TLS, environment/project scope checks and strict JSON validation.
2. Atomic idempotency for both apply and deployment operations.
3. Operation lookup by stable request ID and tested reconciliation after timeouts/restarts.
4. Readiness probes bound to the expected config version and immutable deployment.
5. Contract tests against the real Runtime plus failure/concurrency tests in an isolated environment.
6. Secret management, rate/resource quotas, logs/metrics/alerts, operator runbook, rollback and recovery procedures.

The current HTTP adapters implement the Control Plane side of this contract only; they are not a substitute for the Runtime server, infrastructure provider, or staging verification.
