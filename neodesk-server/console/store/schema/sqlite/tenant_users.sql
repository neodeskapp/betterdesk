-- tenant_users on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS tenant_users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                tenant_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                assigned_at DATETIME DEFAULT (datetime('now')),
                UNIQUE(tenant_id, user_id),
                FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
            );
CREATE INDEX IF NOT EXISTS idx_tenant_users_tenant ON tenant_users (tenant_id);
