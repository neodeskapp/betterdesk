-- activity_summaries on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS activity_summaries (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL,
    idle_seconds INTEGER DEFAULT 0,
    session_count INTEGER DEFAULT 0,
    total_active_secs INTEGER DEFAULT 0,
    reported_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(device_id, reported_at)
);
