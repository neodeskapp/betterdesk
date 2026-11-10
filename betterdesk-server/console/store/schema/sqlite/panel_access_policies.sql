-- panel_access_policies on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS panel_access_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                resource_key TEXT NOT NULL,
                access_level TEXT NOT NULL DEFAULT 'none',
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, resource_key)
            );
CREATE INDEX IF NOT EXISTS idx_panel_access_policies_subject
                ON panel_access_policies (subject_type, subject_id);
