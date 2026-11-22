-- ticket_attachments on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS ticket_attachments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ticket_id INTEGER NOT NULL,
                filename TEXT NOT NULL,
                mimetype TEXT DEFAULT 'application/octet-stream',
                size_bytes INTEGER DEFAULT 0,
                storage_path TEXT NOT NULL,
                uploaded_by TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
            );
