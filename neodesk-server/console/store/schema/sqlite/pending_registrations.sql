-- pending_registrations on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS pending_registrations (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                hostname TEXT DEFAULT '',
                platform TEXT DEFAULT '',
                version TEXT DEFAULT '',
                ip_address TEXT DEFAULT '',
                public_key TEXT DEFAULT '',
                uuid TEXT DEFAULT '',
                status TEXT DEFAULT 'pending',
                approved_by TEXT DEFAULT NULL,
                approved_at TEXT DEFAULT NULL,
                rejected_reason TEXT DEFAULT '',
                access_token TEXT DEFAULT NULL,
                console_url TEXT DEFAULT NULL,
                server_address TEXT DEFAULT NULL,
                server_key TEXT DEFAULT NULL,
                created_at DATETIME DEFAULT (datetime('now')),
                updated_at DATETIME DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
CREATE INDEX IF NOT EXISTS idx_pending_reg_status ON pending_registrations (status);
CREATE INDEX IF NOT EXISTS idx_pending_reg_device ON pending_registrations (device_id);
