-- agent_bundles on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS agent_bundles (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                bundle_id TEXT NOT NULL UNIQUE,
                slug TEXT DEFAULT NULL,
                name TEXT NOT NULL,
                branding TEXT NOT NULL DEFAULT '{}',
                branding_hash TEXT NOT NULL DEFAULT '',
                config_fingerprint TEXT NOT NULL DEFAULT '',
                client_commit TEXT DEFAULT NULL,
                active_generation_hash TEXT DEFAULT NULL,
                created_by INTEGER DEFAULT NULL,
                product_type TEXT NOT NULL DEFAULT 'neodesk-support',
                revoked INTEGER NOT NULL DEFAULT 0,
                download_count INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_agent_bundles_bundle_id ON agent_bundles (bundle_id);
CREATE INDEX IF NOT EXISTS idx_agent_bundles_hash ON agent_bundles (branding_hash);
