-- account_lockouts on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS account_lockouts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                locked_until TEXT NOT NULL,
                attempt_count INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
