-- access_tokens on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS access_tokens (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                token TEXT UNIQUE NOT NULL,
                token_hash TEXT,
                user_id INTEGER NOT NULL,
                client_id TEXT DEFAULT '',
                client_uuid TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now')),
                expires_at TEXT NOT NULL,
                last_used TEXT,
                ip_address TEXT DEFAULT '',
                revoked INTEGER DEFAULT 0,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
