-- device_telemetry on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS device_telemetry (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL UNIQUE,
    cpu_usage_percent REAL DEFAULT 0,
    memory_used_bytes BIGINT DEFAULT 0,
    memory_total_bytes BIGINT DEFAULT 0,
    uptime_secs INTEGER DEFAULT 0,
    timestamp TIMESTAMPTZ,
    received_at TIMESTAMPTZ DEFAULT NOW()
);
