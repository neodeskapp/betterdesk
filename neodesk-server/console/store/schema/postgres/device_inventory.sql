-- device_inventory on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS device_inventory (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL UNIQUE,
    hardware JSONB DEFAULT '{}',
    software JSONB DEFAULT '{}',
    collected_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ DEFAULT NOW()
);
