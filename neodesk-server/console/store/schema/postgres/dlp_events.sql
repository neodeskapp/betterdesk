-- dlp_events on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS dlp_events (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL,
    event_source TEXT NOT NULL DEFAULT 'unknown',
    event_type TEXT NOT NULL DEFAULT 'info',
    policy_id INTEGER DEFAULT NULL REFERENCES dlp_policies(id) ON DELETE SET NULL,
    policy_name TEXT DEFAULT '',
    action TEXT DEFAULT 'log',
    details JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_dlp_events_device ON dlp_events (device_id);
CREATE INDEX IF NOT EXISTS idx_dlp_events_time ON dlp_events (created_at);
CREATE INDEX IF NOT EXISTS idx_dlp_events_source ON dlp_events (event_source);
