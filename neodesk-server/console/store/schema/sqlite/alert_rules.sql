-- alert_rules on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS alert_rules (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                description TEXT DEFAULT '',
                enabled INTEGER DEFAULT 1,
                condition_type TEXT NOT NULL,
                condition_op TEXT NOT NULL DEFAULT 'gt',
                condition_value REAL NOT NULL DEFAULT 0,
                severity TEXT DEFAULT 'warning',
                scope_device_id TEXT DEFAULT NULL,
                cooldown_secs INTEGER DEFAULT 300,
                notify_emails TEXT DEFAULT '',
                created_by TEXT DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
