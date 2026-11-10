-- device_inventory on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS device_inventory (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                hardware TEXT DEFAULT '{}',
                software TEXT DEFAULT '{}',
                collected_at TEXT,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
