-- client_feature_policies on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS client_feature_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                feature_key TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 0,
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, feature_key)
            );
CREATE INDEX IF NOT EXISTS idx_client_feature_policies_subject
                ON client_feature_policies (subject_type, subject_id);
