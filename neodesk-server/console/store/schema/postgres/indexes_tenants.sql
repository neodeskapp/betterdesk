-- Indexes of the tenant tables, created after all three tables.
CREATE INDEX IF NOT EXISTS idx_tenant_devices_tenant ON tenant_devices (tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_devices_device ON tenant_devices (device_id);
CREATE INDEX IF NOT EXISTS idx_tenant_users_tenant ON tenant_users (tenant_id);
