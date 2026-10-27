ALTER TABLE deployments
    ADD COLUMN readiness_probe_id TEXT,
    ADD COLUMN readiness_observed_at TIMESTAMPTZ,
    ADD CONSTRAINT deployments_readiness_evidence_pair CHECK (
        (readiness_probe_id IS NULL AND readiness_observed_at IS NULL)
        OR (
            readiness_probe_id IS NOT NULL
            AND length(btrim(readiness_probe_id)) > 0
            AND readiness_observed_at IS NOT NULL
        )
    );
