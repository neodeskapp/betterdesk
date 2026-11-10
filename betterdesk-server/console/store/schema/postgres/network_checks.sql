-- network_checks on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS network_checks (
    id SERIAL PRIMARY KEY,
    target_id INTEGER NOT NULL REFERENCES network_targets(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'unknown',
    rtt_ms REAL DEFAULT NULL,
    status_code INTEGER DEFAULT NULL,
    error_msg TEXT DEFAULT NULL,
    checked_at TIMESTAMPTZ DEFAULT NOW()
);
