-- network_targets on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS network_targets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                host TEXT DEFAULT '',
                port INTEGER DEFAULT NULL,
                url TEXT DEFAULT NULL,
                check_type TEXT NOT NULL DEFAULT 'ping',
                timeout_ms INTEGER DEFAULT 5000,
                interval_ms INTEGER DEFAULT 60000,
                enabled INTEGER DEFAULT 1,
                last_status TEXT DEFAULT NULL,
                last_check_at TEXT DEFAULT NULL,
                last_rtt_ms REAL DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
