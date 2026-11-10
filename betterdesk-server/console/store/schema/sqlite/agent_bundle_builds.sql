-- agent_bundle_builds on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS agent_bundle_builds (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                branding_hash TEXT NOT NULL,
                platform TEXT NOT NULL,
                arch TEXT NOT NULL DEFAULT 'x64',
                format TEXT NOT NULL,
                status TEXT NOT NULL DEFAULT 'queued',
                artifact_path TEXT DEFAULT NULL,
                artifact_size INTEGER DEFAULT 0,
                artifact_sha256 TEXT DEFAULT NULL,
                error_message TEXT DEFAULT '',
                client_commit TEXT DEFAULT NULL,
                generation_id TEXT DEFAULT NULL,
                config_fingerprint TEXT DEFAULT NULL,
                progress_percent INTEGER NOT NULL DEFAULT 0,
                progress_phase TEXT DEFAULT '',
                started_at TEXT DEFAULT NULL,
                finished_at TEXT DEFAULT NULL,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now')),
                UNIQUE(branding_hash, platform, arch, format)
            );
CREATE INDEX IF NOT EXISTS idx_agent_bundle_builds_hash ON agent_bundle_builds (branding_hash);
CREATE INDEX IF NOT EXISTS idx_agent_bundle_builds_status ON agent_bundle_builds (status);
