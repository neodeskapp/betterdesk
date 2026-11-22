-- client_feature_policies on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS client_feature_policies (
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    feature_key TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (subject_type, subject_id, feature_key)
);
CREATE INDEX IF NOT EXISTS idx_client_feature_policies_subject ON client_feature_policies (subject_type, subject_id);
