-- email_notification_log on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS email_notification_log (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                event_type TEXT NOT NULL,
                event_id TEXT NOT NULL,
                recipient TEXT NOT NULL,
                sent_at TEXT DEFAULT (datetime('now')),
                UNIQUE(event_type, event_id, recipient)
            );
CREATE INDEX IF NOT EXISTS idx_email_notification_log_event ON email_notification_log (event_type, event_id);
