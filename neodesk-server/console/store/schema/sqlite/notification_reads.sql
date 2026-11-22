-- notification_reads on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS notification_reads (
                user_id INTEGER NOT NULL,
                notification_id TEXT NOT NULL,
                read_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, notification_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE INDEX IF NOT EXISTS idx_notification_reads_user ON notification_reads (user_id, read_at);
