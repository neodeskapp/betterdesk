-- ticket_comments on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS ticket_comments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ticket_id INTEGER NOT NULL,
                author TEXT NOT NULL,
                body TEXT NOT NULL,
                is_internal INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
            );
