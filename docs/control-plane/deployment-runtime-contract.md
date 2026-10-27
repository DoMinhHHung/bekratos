# Control Plane ↔ Backend Runtime Deployment Contract

## Current implementation status

The Control Plane owns deployment intent, configuration-version selection, authorization, idempotency, and durable deployment state. The production provider is currently `UnavailableProvider`. This repository does not contain a production Backend Runtime deployment endpoint or infrastructure provider, so deployment creation must not be described as a working end-to-end runtime deployment.

This document defines the integration contract to implement jointly with the Backend Runtime. It is not evidence that the remote side already implements it.

## Ownership and trust boundaries

- Control Plane is authoritative for organizations, projects, environments, configuration-version IDs, deployment IDs, idempotency keys, and deployment state.
- Backend Runtime/Data Plane is authoritative for executing the target runtime deployment and reporting observed runtime health.
- Platform JWTs authenticate Control Plane management requests. They are not runtime end-user credentials.
- Runtime API keys must not be used to call Control Plane management endpoints.
- Provider responses are untrusted input until IDs, outcome, readiness evidence, and public error mapping are validated.
- Provider diagnostics must never be copied into public failure messages or logs without an explicit safe diagnostic policy.

## Provision request

The provider receives a stable request containing:

- `deployment_id`: Control Plane deployment UUID.
- `environment_id`: exact environment UUID.
- `configuration_version_id`: immutable configuration version selected at create time.
- `provider_request_id`: stable idempotency identifier, unchanged for all safe retries of this deployment.
- `target_mode`: currently only `shared`.

The remote implementation must deduplicate requests by `provider_request_id`. Repeating a request with the same ID and same payload must return the same operation. Reusing the ID with a different payload must return a conflict and must not create a second deployment.

## Outcomes and retries

- **Success:** the provider returns matching deployment/environment/configuration IDs and affirmative readiness evidence.
- **Failure:** the provider confirms the operation failed without an ambiguous side effect.
- **Retryable:** the provider guarantees that retrying with the same `provider_request_id` is safe.
- **Ambiguous:** the request may have caused a side effect, but the provider cannot confirm the result. Do not blindly retry. Reconciliation must query the existing operation by `provider_request_id`.
- **Unavailable:** no production provider is configured; fail closed.

A network timeout is ambiguous by default. A timeout alone is not proof that the remote operation failed. The current implementation marks ambiguous outcomes terminal and documents reconciliation as not implemented; automatic reconciliation must not be claimed until an actual reconciler and provider lookup API exist.

The Control Plane enforces leases and fencing tokens for local worker ownership. Those controls do not replace remote idempotency: a remote operation can outlive a local worker lease.

## Readiness evidence

A provider may report success only after checking the deployed target, not merely after allocating a hostname or URL. The response must include:

- matching environment and configuration-version IDs;
- the same stable `provider_request_id` supplied by the Control Plane;
- `readiness_probe_passed = true`;
- a non-empty `readiness_probe_id` identifying the check;
- `readiness_observed_at`, a recent timestamp;
- a validated target host or URL.

The Control Plane rejects missing, stale, future-dated, or mismatched evidence. It persists the readiness probe ID and observation timestamp alongside the ready transition. A host or URL alone is insufficient.

The concrete Backend Runtime probe semantics, authentication, TLS policy, maximum response size, allowed host/URL rules, and how readiness checks are correlated with an immutable configuration version must be agreed and implemented before enabling a production provider.

## Error contract

The provider may return internal diagnostics, but the Control Plane maps them to a small allowlist of public codes and generic public messages. Never return raw provider messages, authorization headers, credentials, access tokens, private keys, or internal stack traces in API responses.

## State machine

`queued → provisioning → ready | failed`

- Retryable failures may return to `queued` with backoff while attempts remain.
- Ambiguous outcomes become terminal `failed` with `provider_ambiguous` until reconciliation exists.
- Exhausted attempts become terminal `failed`.
- `ready` requires validated readiness evidence.
- The unavailable production provider never reports `ready`.

Suspension/deletion states are reserved for future provider support; they are not currently operational APIs.

## Preconditions for production integration

Do not enable a real provider until all of the following are available:

1. A versioned Runtime-side API contract and authentication mechanism.
2. Stable remote idempotency and operation lookup by `provider_request_id`.
3. A documented readiness probe tied to the expected environment and configuration version.
4. Strict response validation, timeouts, retry classification, and safe error mapping.
5. Contract tests against a fake server and integration tests against a disposable Runtime environment.
6. Staging credentials and an isolated target environment.
7. An operator runbook for ambiguous outcomes and manual reconciliation until automatic reconciliation exists.
