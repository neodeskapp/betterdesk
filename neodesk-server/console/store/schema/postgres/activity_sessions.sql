-- activity_sessions on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS activity_sessions (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL,
    app_name TEXT NOT NULL DEFAULT '',
    window_title TEXT NOT NULL DEFAULT '',
    category TEXT DEFAULT 'other',
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    duration_secs INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
