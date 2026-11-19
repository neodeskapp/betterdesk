-- alert_rules on PostgreSQL, as the Node console created it, but for
-- condition_value: double precision here, a four-byte real there (BUGS AE4).
CREATE TABLE IF NOT EXISTS alert_rules (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    enabled BOOLEAN DEFAULT TRUE,
    condition_type TEXT NOT NULL,
    condition_op TEXT NOT NULL DEFAULT 'gt',
    condition_value DOUBLE PRECISION NOT NULL DEFAULT 0,
    severity TEXT DEFAULT 'warning',
    scope_device_id TEXT DEFAULT NULL,
    cooldown_secs INTEGER DEFAULT 300,
    notify_emails TEXT DEFAULT '',
    created_by TEXT DEFAULT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
