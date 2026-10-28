# Control Plane ↔ Runtime deployment contract

## Status and trust boundary

The Control Plane now supports an authenticated HTTP provider, but this repository does not contain the Backend Runtime service itself. Leave `RUNTIME_API_URL` and `RUNTIME_SERVICE_TOKEN` unset until a real Runtime endpoint implementing this contract is deployed. In that state deployment requests remain unavailable and must never become ready.

The Control Plane owns deployment intent, tenant authorization, idempotency, configuration-version selection, and durable state. Runtime owns execution and runtime-side readiness evidence. A successful HTTP response alone is not sufficient evidence of readiness.

## Request

The Control Plane sends `POST /v1/deployments` to the configured Runtime base URL. It sends a bearer service credential, `Idempotency-Key`, and JSON containing:

- `deployment_id`
- `environment_id`
- `configuration_version_id`
- `idempotency_key` (the same value sent in the `Idempotency-Key` HTTP header)
- `target_mode`

Runtime must authenticate the service credential, authorize the target environment, bind the request idempotently to the exact deployment and configuration version, and return the existing result for a duplicate idempotency key. It must not execute arbitrary SQL or code from the request.

## Reconciliation

The Runtime must implement `GET /v1/deployments/by-idempotency-key/{idempotency_key}` using the same service authentication. A found deployment returns the same response schema as the apply endpoint and must include the original idempotency key as `request_id`, with state and readiness evidence tied to the exact deployment, environment, and configuration version.

A `404` is safe to retry only when the response is JSON with `code = "deployment_not_found"` and `safe_to_retry = true`. Runtime must guarantee that this response means the idempotency key was not accepted and no side effect was applied. A generic `404`, malformed response, timeout, or any response without that explicit guarantee is ambiguous and must not trigger another apply attempt.

The Runtime must durably serialize concurrent requests with the same idempotency key, including in-progress requests, so retries cannot duplicate side effects.

## Response

The request JSON uses `idempotency_key`; the response echoes that value as `request_id`. A JSON response includes `outcome`, `request_id`, `deployment_id`, `environment_id`, `configuration_version_id`, and, for success, `readiness_probe_passed`, `readiness_probe_id`, and `readiness_observed_at`. Optional display fields are `url`, `host`, and `region`.

The Control Plane accepts a success result only when all IDs and the request ID match the request and the readiness evidence is affirmative, non-empty, recent, and not materially future-dated. Redirects are not followed; response bodies are size-limited; provider calls have a bounded timeout. A timeout or server error is an ambiguous outcome because the Runtime may have performed the side effect before the response was lost. The Control Plane now performs a bounded status lookup after an ambiguous apply outcome. It retries an apply only after the Runtime returns the explicit safe-not-found response defined above. A real Runtime implementation and end-to-end tests are still required before production readiness can be claimed.

## Configuration and operations

Set `RUNTIME_API_URL` to an HTTPS URL in deployed environments and `RUNTIME_SERVICE_TOKEN` to a secret of at least 32 characters from the secret manager. HTTP is allowed only for loopback development. Never expose the service token to browsers, project API clients, or logs.

This adapter is not proof of an end-to-end deployment. Production acceptance still requires a real Runtime implementation and integration tests for duplicate requests, cross-environment isolation, timeout-after-side-effect, stale readiness evidence, wrong configuration version, provider outage, and worker recovery.
