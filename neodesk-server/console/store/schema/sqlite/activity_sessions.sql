-- activity_sessions on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS activity_sessions (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                app_name TEXT NOT NULL DEFAULT '',
                window_title TEXT NOT NULL DEFAULT '',
                category TEXT DEFAULT 'other',
                started_at TEXT NOT NULL,
                ended_at TEXT NOT NULL,
                duration_secs INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_activity_device ON activity_sessions (device_id);
CREATE INDEX IF NOT EXISTS idx_activity_time ON activity_sessions (started_at);
