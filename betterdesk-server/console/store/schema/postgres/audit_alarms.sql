-- audit_alarms on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS audit_alarms (
    id SERIAL PRIMARY KEY,
    alarm_type INTEGER NOT NULL,
    alarm_name TEXT DEFAULT '',
    host_id TEXT DEFAULT '',
    peer_id TEXT DEFAULT '',
    ip TEXT DEFAULT '',
    details JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_alarms_type ON audit_alarms (alarm_type, created_at);
