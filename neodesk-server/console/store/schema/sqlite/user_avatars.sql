-- user_avatars on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS user_avatars (
                    user_id INTEGER PRIMARY KEY,
                    mime_type TEXT NOT NULL,
                    data BLOB NOT NULL,
                    version TEXT NOT NULL,
                    source TEXT NOT NULL DEFAULT 'oidc',
                    source_url TEXT DEFAULT '',
                    updated_at TEXT DEFAULT (datetime('now')),
                    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
                );
