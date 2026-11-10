-- activity_summaries on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS activity_summaries (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                idle_seconds INTEGER DEFAULT 0,
                session_count INTEGER DEFAULT 0,
                total_active_secs INTEGER DEFAULT 0,
                reported_at TEXT NOT NULL,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id, reported_at)
            );
