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

- Ready requires `OutcomeSuccess` **and** matching `environment_id` + `configuration_version_id` **and** non-empty provider host or URL.
- Timeout / unknown outcome → retry with backoff until `max_attempts`, then terminal `failed`.
- Unavailable provider → terminal `failed` with code `provider_unavailable`.

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
