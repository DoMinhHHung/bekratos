# Identity Service Operations

## Identity email delivery

Set `MAIL_PROVIDER=resend` for Resend HTTPS delivery or `MAIL_PROVIDER=smtp` for Gmail SMTP with an App Password. Production rejects `console` and `none`; there is no fallback between providers. The SMTP adapter uses implicit TLS to `smtp.gmail.com:465`, validates the server certificate, bounds the complete operation by `MAIL_TIMEOUT_SECS`, caps SMTP responses and message size, and does not log SMTP replies or credentials.

For Gmail SMTP, enable 2-Step Verification on the Google account, create an App Password for the account, and inject `SMTP_USERNAME` and `SMTP_APP_PASSWORD` from the deployment secret manager. The account may require a verified sender or configured “send mail as” alias matching `MAIL_FROM`. Do not use the account's normal Google password. Rotate by creating a replacement App Password, deploying it, verifying delivery, and revoking the old one. Treat provider acceptance as queued/accepted by the SMTP server, not proof of inbox placement.

For custom sender domains, publish and verify SPF and DKIM records with the actual sending provider and publish a DMARC policy, initially monitor-only if needed. Avoid multiple conflicting SPF records. Monitor provider acceptance/rejection, SMTP authentication failures, queue age, retries and expired tokens without logging recipient addresses, message bodies, or magic links. SMTP timeouts after DATA may cause duplicate delivery because the server may have accepted the message before the connection failed.

## Configuration and secrets

- Keep production credentials in the deployment platform's secret manager. Never put real values in `.env`, `.env.example`, CI variables, logs, or tickets.
- Use separate PostgreSQL, Redis, HMAC keyring, JWT encryption key, and signing-key material for each environment.
- `JWT_KEY_ENCRYPTION_KEY` must be a high-entropy secret with at least 32 bytes. Back it up separately from the database; losing it makes encrypted private signing keys unusable.
- `MAGIC_LINK_HMAC_KEYS` is a keyring. Keep existing key IDs available until all links signed by them have expired, then remove old entries.
- The service creates and stores an encrypted ES256 key in PostgreSQL when there is no active JWKS key. The legacy `JWT_SIGNING_KEY_PEM` value is only a fallback if database-backed key bootstrap fails.
- Rotate database credentials and application secrets through the deployment secret manager. Do not paste secret values into application logs or CI output.

## Database lifecycle

- Apply migrations with the service startup process or an explicitly authorized deployment job. Review migration changes before release and back up the database before production schema changes.
- The account purge worker permanently deletes accounts after their configured recovery window. Verify backups and retention requirements before enabling it in production.
- `DATABASE_URL_TEST` must point only to a disposable test database. The test suite applies migrations and creates/deletes test records.
- Restore production data only through the approved database recovery process; test migrations and purge behavior against disposable databases first.

## Key rotation and JWKS

- `GET /v1/jwks` publishes active and still-valid retiring keys. Verifiers select keys by `kid`.
- The application currently exposes key creation/rotation through internal service code, not a public HTTP administration endpoint. Do not expose this operation without authenticated operator authorization and audit logging.
- During rotation, publish the new public key before depending on it, keep the old key available for at least the maximum access-token lifetime plus clock skew, then retire it.
- Confirm the new key can sign and verify a token and that tokens carrying an unknown `kid` fail before completing a rotation.
- Back up `JWT_KEY_ENCRYPTION_KEY` independently. Restoring the database without the matching key-encryption secret will prevent use of stored signing keys.

## Health, logs, and incident response

- `/healthz` reports process liveness. `/readyz` checks PostgreSQL and Redis according to `READINESS_REQUIRE_DB` and `READINESS_REQUIRE_REDIS`.
- `/swagger-ui/` serves interactive API documentation and `/api-docs/openapi.json` serves the OpenAPI document. Restrict these routes at the ingress or disable them for deployments where public API documentation is not appropriate.
- Use structured logs and monitor startup failures, database/Redis errors, failed email delivery, purge activity, key reconciliation, repeated authentication failures, and elevated `401`/`429` responses.
- Treat request IP and user-agent data as sensitive operational data. Apply access controls and retention limits to logs and audit records.
- For a suspected signing-key exposure, stop issuing tokens with the affected key, create and activate a replacement through the authorized operator procedure, publish the new JWKS, revoke sessions as required, and remove the compromised key only after verifiers have migrated.
- For suspected database or encryption-key exposure, preserve relevant audit logs, rotate credentials, assess affected accounts and tokens, and follow the organization's incident response and notification process.

## Release readiness checklist

- [ ] CI passes format, Clippy, offline check, unit tests, PostgreSQL/Redis integration tests, container build, dependency audit, and secret scanning.
- [ ] Test database and Redis are disposable; CI has no production credentials.
- [ ] Review migration compatibility, database backup/restore, purge retention, readiness behavior, and rollback procedure.
- [ ] Verify JWT key bootstrap, signing, JWKS publication, key overlap, unknown-`kid` rejection, and encryption-key backup.
- [ ] Verify registration/login/reset/restore flows, rate limits, session revocation, email retry behavior, and generic responses for account-enumeration-sensitive endpoints.
- [ ] Confirm observability, alert ownership, secret rotation, and incident-response contacts.
- [ ] Keep branch protection and required checks aligned with the actual successful CI workflow runs.

## Running integration tests locally

Set `DATABASE_URL_TEST` to a disposable PostgreSQL database and `REDIS_URL_TEST` to a disposable Redis instance, then run:

```bash
cd services/identity
SQLX_OFFLINE=true cargo test --locked --all-targets
```

Integration tests skip when their corresponding test service URL is absent. CI supplies disposable PostgreSQL and Redis services, so those checks run there.


## Production email delivery

Identity supports either the Resend HTTPS API or Gmail SMTP with an App Password for verification, password-reset, and account-restore messages. Set `APP_ENV=production`, select `MAIL_PROVIDER=resend` or `MAIL_PROVIDER=smtp`, configure `MAIL_FROM`, and inject provider credentials through the deployment secret manager. Production startup rejects console/no-op providers and requires HTTPS `APP_PUBLIC_BASE_URL`.

Resend requires `MAIL_API_KEY` and a verified sender. Gmail SMTP requires `SMTP_USERNAME`, a 16-character `SMTP_APP_PASSWORD`, and the fixed `smtp.gmail.com:465` implicit-TLS endpoint. The Google account must have 2-Step Verification enabled and may need the sender configured as a verified alias. Never use the account's normal password.

Provider acceptance does not prove inbox placement. The durable PostgreSQL token queue retries failures with bounded exponential backoff. A timeout after the remote server accepts a message, or failure to persist `sent_at`, can cause duplicate delivery after lease recovery. Monitor queue age, delivery attempts, authentication failures, provider rejection rates, and expired tokens without logging recipient addresses, message bodies, credentials, SMTP responses, or magic links.

For custom sender domains, verify and publish the provider's SPF and DKIM records and publish a DMARC policy. Avoid multiple conflicting SPF records. Rotate provider credentials through the secret manager, verify the replacement, then revoke the old credential.
