-- network_targets on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS network_targets (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT DEFAULT '',
    port INTEGER DEFAULT NULL,
    url TEXT DEFAULT NULL,
    check_type TEXT NOT NULL DEFAULT 'ping',
    timeout_ms INTEGER DEFAULT 5000,
    interval_ms INTEGER DEFAULT 60000,
    enabled BOOLEAN DEFAULT TRUE,
    last_status TEXT DEFAULT NULL,
    last_check_at TIMESTAMPTZ DEFAULT NULL,
    last_rtt_ms REAL DEFAULT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
