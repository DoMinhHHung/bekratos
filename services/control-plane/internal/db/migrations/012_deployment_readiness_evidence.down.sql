ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_readiness_evidence_pair,
    DROP COLUMN IF EXISTS readiness_observed_at,
    DROP COLUMN IF EXISTS readiness_probe_id;
