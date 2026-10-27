# Identity ↔ Control Plane contract

## Trust model

Identity remains authoritative for platform accounts, credentials, session state, and primary-email verification. Control Plane validates the platform JWT signature locally using Identity's ES256 JWKS, exact issuer and audience, required `sub`, `sid`, `iat`, `nbf`, and `exp`, then asks Identity to validate the live session before accepting the request.

Control Plane calls `GET /v1/me` using the original caller's bearer token. Identity validates the token and checks the account and session against PostgreSQL. The response must contain the expected `id`, `status = active`, and `email_verified`. Control Plane rejects mismatched IDs or inactive sessions. Identity outages, timeouts, non-success responses, malformed/oversized responses, and invalid JSON fail closed; they never fall back to JWT-only acceptance.

The Identity profile response includes `email_verified`, derived from the primary `user_emails.verified_at` value. A missing primary email is unverified. Control Plane must use this field only for policies that explicitly require a verified email; it must not treat the email address itself as proof of verification.

## Configuration and operations

- `IDENTITY_JWKS_URL` is the JWKS endpoint used for local ES256 validation.
- `IDENTITY_PROFILE_URL` is the live-session endpoint. If omitted, Control Plane derives the same-origin `/v1/me` URL from the JWKS URL.
- `JWKS_HTTP_TIMEOUT` bounds both the JWKS fetch and live-session check.
- Non-loopback Identity URLs must use HTTPS. Local loopback HTTP is allowed for development only.

The original bearer token is forwarded only to the configured Identity URL. Do not log authorization headers or tokens. Keep Control Plane and Identity on a private network where possible, use TLS in deployed environments, and monitor Identity verification latency/failures. Because every management request performs a live check, Identity availability is a dependency of Control Plane authorization; fail closed rather than silently degrading to signature-only validation.

## Boundaries

- Never trust `X-User-ID` or client-provided email verification fields.
- Control Plane memberships, roles, organization/project ownership, and authorization decisions remain sourced from Control Plane PostgreSQL.
- Platform JWTs authenticate dashboard/platform users only. They are not project end-user tokens and do not authorize Runtime data-plane access.
- Runtime API keys and service credentials are never accepted as platform JWTs.
