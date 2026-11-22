-- network_checks on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS network_checks (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                target_id INTEGER NOT NULL,
                status TEXT NOT NULL DEFAULT 'unknown',
                rtt_ms REAL DEFAULT NULL,
                status_code INTEGER DEFAULT NULL,
                error_msg TEXT DEFAULT NULL,
                checked_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (target_id) REFERENCES network_targets(id) ON DELETE CASCADE
            );
CREATE INDEX IF NOT EXISTS idx_network_checks_target ON network_checks (target_id);
CREATE INDEX IF NOT EXISTS idx_network_checks_time ON network_checks (checked_at);
