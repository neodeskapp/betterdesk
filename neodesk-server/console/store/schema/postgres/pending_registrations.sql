-- pending_registrations on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS pending_registrations (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL UNIQUE,
    hostname TEXT DEFAULT '',
    platform TEXT DEFAULT '',
    version TEXT DEFAULT '',
    ip_address TEXT DEFAULT '',
    public_key TEXT DEFAULT '',
    uuid TEXT DEFAULT '',
    status TEXT DEFAULT 'pending',
    approved_by TEXT DEFAULT NULL,
    approved_at TIMESTAMPTZ DEFAULT NULL,
    rejected_reason TEXT DEFAULT '',
    access_token TEXT DEFAULT NULL,
    console_url TEXT DEFAULT NULL,
    server_address TEXT DEFAULT NULL,
    server_key TEXT DEFAULT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pending_reg_status ON pending_registrations (status);
CREATE INDEX IF NOT EXISTS idx_pending_reg_device ON pending_registrations (device_id);
