-- login_attempts on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS login_attempts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT NOT NULL,
                ip_address TEXT DEFAULT '',
                success INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
