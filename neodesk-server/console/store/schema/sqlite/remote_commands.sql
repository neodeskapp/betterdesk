-- remote_commands on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS remote_commands (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                command_type TEXT NOT NULL DEFAULT 'shell',
                payload TEXT NOT NULL DEFAULT '',
                status TEXT DEFAULT 'pending',
                result TEXT DEFAULT NULL,
                created_by TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                executed_at TEXT DEFAULT NULL,
                completed_at TEXT DEFAULT NULL
            );
CREATE INDEX IF NOT EXISTS idx_remote_commands_device ON remote_commands (device_id, status);
