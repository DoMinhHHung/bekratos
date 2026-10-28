# Control Plane deployments

## Scope

Phase 9 implements **deployment intent and state** in PostgreSQL, a provider interface, and a fail-closed production provider. There is **no** real Backend Runtime or cloud deployment provider in this repository.

## Provider reality

| Mode                             | Status                                                                      |
| -------------------------------- | --------------------------------------------------------------------------- |
| Production `UnavailableProvider` | Always returns `provider_unavailable`; never transitions to `ready`         |
| Test-only `RecordingProvider`    | Wired only in tests                                                         |
| Shared Runtime URL               | Not generated; only stored when a real provider supplies validated evidence |
| Dedicated isolation              | Unsupported                                                                 |

## State machine

`queued` → `provisioning` → `ready` | `failed`

- Ready requires `OutcomeSuccess`, matching deployment/environment/configuration-version IDs, the stable provider request ID, `readiness_probe_passed=true`, a non-empty probe ID, a recent observation timestamp, and a validated HTTPS URL or host. HTTP 200, a URL alone, or a host alone is not readiness evidence.
- The HTTP adapter bounds time and response size, rejects redirects, and reconciles ambiguous POST outcomes by the stable idempotency key. It retries only when Runtime explicitly confirms `deployment_not_found` and `safe_to_retry=true`; other ambiguous outcomes fail closed.
- `UnavailableProvider` remains the default when no runtime URL/token is configured. The repository still contains no production Backend Runtime endpoint, so end-to-end provisioning is not available here.
- Runtime URL and service credentials are operator-provided configuration; production requires HTTPS and a high-entropy service token.

Suspend/delete transitions exist in the schema for future providers; management routes for them are **not** exposed until a real provider supports them.

## API

All routes require platform JWT + membership checks (not client-supplied roles).

- `POST /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/deployments`
- `GET .../deployments`
- `GET .../deployments/{deploymentID}`

Create requires **owner** or **admin**. List/get allow owner, admin, developer, viewer.

Body:

```json
{
  "idempotency_key": "required-string",
  "configuration_version_id": "optional-uuid",
  "target_mode": "shared"
}
```
