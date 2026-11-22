-- agent_templates on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS agent_templates (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL UNIQUE,
                description TEXT NOT NULL DEFAULT '',
                config_json TEXT NOT NULL DEFAULT '{}',
                enrollment_token TEXT NOT NULL UNIQUE,
                enrolled_device_id TEXT DEFAULT NULL,
                enrolled_at TEXT DEFAULT NULL,
                created_by INTEGER DEFAULT NULL,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_agent_templates_token ON agent_templates (enrollment_token);
