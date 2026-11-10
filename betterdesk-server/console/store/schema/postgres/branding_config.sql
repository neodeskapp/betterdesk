-- branding_config on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS branding_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
