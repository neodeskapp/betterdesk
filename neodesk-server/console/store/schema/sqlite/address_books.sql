-- address_books on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS address_books (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                ab_type TEXT DEFAULT 'legacy',
                data TEXT DEFAULT '{}',
                updated_at TEXT DEFAULT (datetime('now')),
                UNIQUE(user_id, ab_type),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
