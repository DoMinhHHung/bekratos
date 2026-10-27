# Identity ↔ Control Plane contract

## Trust model

Identity remains authoritative for platform accounts, credentials, active sessions, and primary-email verification. Control Plane validates the platform JWT signature locally using Identity's ES256 JWKS and requires exact issuer and audience plus valid `sub`, `sid`, `iat`, `nbf`, and `exp` claims. It then calls `GET /v1/internal/session` with the original bearer token and the shared `X-Control-Plane-Service-Token` credential.

Identity checks the service credential using a constant-time comparison, validates the access token again, checks the session ID and user ID against PostgreSQL, rejects revoked/expired sessions and non-active accounts, and derives primary-email verification from `user_emails.verified_at`. The response contains only `subject`, `session_id`, `account_status`, and `primary_email_verified`. Control Plane requires the subject and session ID to match the JWT it already validated and requires `account_status = active`.

The service credential is configured as `CONTROL_PLANE_SERVICE_TOKEN` in Identity and `IDENTITY_SERVICE_TOKEN` in Control Plane. Use a randomly generated secret of at least 32 characters, inject it from a secret manager in production, rotate both sides together, and never log it. The example value is local-development-only and is rejected by Identity in production.

Identity returns 401 for an invalid service credential or invalid/inactive user session. Control Plane returns 401 for rejected credentials and 503 for timeout, network failure, non-2xx dependency response other than 401/403, malformed JSON, oversized response, or a response whose subject/session ID does not match. All sensitive management requests fail closed; there is no JWT-only fallback. The HTTP client has a bounded timeout and response size. Keep the service endpoint private and use HTTPS outside loopback development.

## Configuration and operations

- `IDENTITY_JWKS_URL` is the JWKS endpoint used for local ES256 validation.
- `IDENTITY_PROFILE_URL` defaults to the same-origin `/v1/internal/session` endpoint derived from the JWKS URL.
- `IDENTITY_SERVICE_TOKEN` is required by Control Plane and must match Identity's `CONTROL_PLANE_SERVICE_TOKEN`.
- `JWKS_HTTP_TIMEOUT` bounds both the JWKS fetch and live-session check.

Do not log authorization headers, user tokens, or service credentials. Because every management request performs a live check, Identity availability is a dependency of Control Plane authorization; fail closed rather than silently degrading to signature-only validation.

## Boundaries

- Never trust `X-User-ID` or client-provided email verification fields.
- Control Plane memberships, roles, organization/project ownership, and authorization decisions remain sourced from Control Plane PostgreSQL.
- Platform JWTs authenticate dashboard/platform users only. They are not project end-user tokens and do not authorize Runtime data-plane access.
- Runtime API keys and service credentials are never accepted as platform JWTs.
