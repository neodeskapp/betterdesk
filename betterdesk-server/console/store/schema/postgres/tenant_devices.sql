-- tenant_devices on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS tenant_devices (
    id SERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL,
    assigned_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, device_id)
);
