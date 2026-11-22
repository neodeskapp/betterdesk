-- tickets on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS tickets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                title TEXT NOT NULL,
                description TEXT DEFAULT '',
                status TEXT DEFAULT 'open',
                priority TEXT DEFAULT 'medium',
                category TEXT DEFAULT 'general',
                device_id TEXT DEFAULT NULL,
                created_by TEXT NOT NULL,
                assigned_to TEXT DEFAULT NULL,
                sla_due_at TEXT DEFAULT NULL,
                resolved_at TEXT DEFAULT NULL,
                closed_at TEXT DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
