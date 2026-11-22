-- email_notification_log on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS email_notification_log (
    id SERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    event_id TEXT NOT NULL,
    recipient TEXT NOT NULL,
    sent_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(event_type, event_id, recipient)
);
CREATE INDEX IF NOT EXISTS idx_email_notification_log_event ON email_notification_log (event_type, event_id);
