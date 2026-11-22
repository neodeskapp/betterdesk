-- tenant_devices on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS tenant_devices (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                tenant_id INTEGER NOT NULL,
                device_id TEXT NOT NULL,
                assigned_at DATETIME DEFAULT (datetime('now')),
                UNIQUE(tenant_id, device_id),
                FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
            );
CREATE INDEX IF NOT EXISTS idx_tenant_devices_tenant ON tenant_devices (tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_devices_device ON tenant_devices (device_id);
