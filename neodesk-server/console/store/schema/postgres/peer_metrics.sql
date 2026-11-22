-- peer_metrics on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS peer_metrics (
    id SERIAL PRIMARY KEY,
    peer_id TEXT NOT NULL,
    cpu_usage REAL DEFAULT 0,
    memory_usage REAL DEFAULT 0,
    disk_usage REAL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_peer_metrics_peer_time ON peer_metrics (peer_id, created_at);
