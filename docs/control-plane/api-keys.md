# Control Plane API keys

## Credential classes

- **Management auth**: platform Identity JWT + current organization membership (owner/admin for key management).
- **Publishable** (`bkr_pub_<key-id>.<secret>`): identifies environment for public SDK clients. Does not authenticate an end user and grants no blanket data access.
- **Server secret** (`bkr_sec_<key-id>.<secret>`): high-entropy server-side credential. Never embed in browsers or public examples.

## Format and storage

- Secret: 32 cryptographically random bytes, unpadded base64url.
- Persist only SHA-256 of the secret bytes (`BYTEA` length 32).
- Key id is a UUIDv7. Prefix is unique and non-secret.
- Full key is returned **once** on create and rotate. Lost secret cannot be recovered; rotate again.

## Management API

All routes require platform JWT middleware.

- `GET/POST /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/api-keys`
- `GET /v1/organizations/{orgID}/projects/{projectID}/environments/{envID}/api-keys/{keyID}`
- `POST .../api-keys/{keyID}/revoke`
- `POST .../api-keys/{keyID}/rotate`

Only **owner** and **admin** may list, view, create, revoke, or rotate.

## Scopes (MVP allow-list)

- `data:read`
- `data:write`

Scopes constrain intended capability metadata. They do not override module policies, entity access policies, or tenant isolation. Runtime must enforce scopes; Control Plane stores and validates the allow-list only.

## Expiration

`expires_at` may be omitted (non-expiring). Non-expiring keys increase long-term exposure risk. Rotation preserves the previous `expires_at` and does not silently extend lifetime.

## Revocation and cache contract

- PostgreSQL is the Control Plane source of truth.
- After commit, revocation/expiry is durable in the database.
- No Redis cache is implemented in this phase.
- Future Runtime consumers that cache verification results must bound stale acceptance to **at most 60 seconds**.
- This phase does **not** claim instantaneous Runtime rejection after revoke.

## Internal verification contract

`apikey.Repository.Verify` / `Service.Verify` parse the full key, load the row by id, constant-time compare the hash, and reject revoked/expired keys and inactive org/project/environment parents. Environment binding comes from the stored row, never from caller-supplied environment ids.

There is **no** public unauthenticated verification endpoint and **no** Runtime consumer in this phase.

## Audit

Events: `api_key.create`, `api_key.revoke`, `api_key.rotate`. Metadata may include key id, type, name, and prefix only—never plaintext secrets or hashes.
