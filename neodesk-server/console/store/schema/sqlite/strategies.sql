-- strategies on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS strategies (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                user_group_guid TEXT DEFAULT '',
                device_group_guid TEXT DEFAULT '',
                enabled INTEGER DEFAULT 1,
                permissions TEXT DEFAULT '{}',
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
