-- user_groups on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS user_groups (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
