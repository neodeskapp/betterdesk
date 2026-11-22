-- device_telemetry on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS device_telemetry (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                cpu_usage_percent REAL DEFAULT 0,
                memory_used_bytes INTEGER DEFAULT 0,
                memory_total_bytes INTEGER DEFAULT 0,
                uptime_secs INTEGER DEFAULT 0,
                timestamp TEXT,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
