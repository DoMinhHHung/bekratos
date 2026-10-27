UPDATE deployments
SET status = 'failed',
    failure_code = 'legacy_unverified_readiness',
    failure_message = 'deployment readiness could not be verified',
    provider_url = NULL,
    provider_host = NULL,
    provider_region = NULL,
    ready_at = NULL,
    lease_owner = NULL,
    lease_expires_at = NULL,
    next_attempt_at = clock_timestamp(),
    finished_at = COALESCE(finished_at, clock_timestamp()),
    updated_at = clock_timestamp()
WHERE status = 'ready'
  AND (
    readiness_probe_id IS NULL
    OR length(btrim(readiness_probe_id)) = 0
    OR length(btrim(readiness_probe_id)) > 256
    OR readiness_observed_at IS NULL
    OR ready_at IS NULL
    OR (
      NULLIF(btrim(provider_url), '') IS NULL
      AND NULLIF(btrim(provider_host), '') IS NULL
    )
  );

ALTER TABLE deployments
    ADD CONSTRAINT deployments_ready_requires_evidence CHECK (
        status <> 'ready'
        OR (
            readiness_probe_id IS NOT NULL
            AND length(btrim(readiness_probe_id)) BETWEEN 1 AND 256
            AND readiness_observed_at IS NOT NULL
            AND ready_at IS NOT NULL
            AND (
                NULLIF(btrim(provider_url), '') IS NOT NULL
                OR NULLIF(btrim(provider_host), '') IS NOT NULL
            )
        )
    );
