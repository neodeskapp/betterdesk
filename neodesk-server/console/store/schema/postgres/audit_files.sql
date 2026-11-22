-- audit_files on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS audit_files (
    id SERIAL PRIMARY KEY,
    host_id TEXT NOT NULL,
    host_uuid TEXT DEFAULT '',
    peer_id TEXT DEFAULT '',
    direction INTEGER DEFAULT 0,
    path TEXT DEFAULT '',
    is_file BOOLEAN DEFAULT TRUE,
    num_files INTEGER DEFAULT 0,
    files_json JSONB DEFAULT '[]',
    ip TEXT DEFAULT '',
    peer_name TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_files_host ON audit_files (host_id, created_at);
