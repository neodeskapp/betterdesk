-- agent_bundle_builds on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS agent_bundle_builds (
    id SERIAL PRIMARY KEY,
    branding_hash TEXT NOT NULL,
    platform TEXT NOT NULL,
    arch TEXT NOT NULL DEFAULT 'x64',
    format TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    artifact_path TEXT DEFAULT NULL,
    artifact_size BIGINT DEFAULT 0,
    artifact_sha256 TEXT DEFAULT NULL,
    error_message TEXT DEFAULT '',
    client_commit TEXT DEFAULT NULL,
    generation_id TEXT DEFAULT NULL,
    config_fingerprint TEXT DEFAULT NULL,
    progress_percent INTEGER NOT NULL DEFAULT 0,
    progress_phase TEXT DEFAULT '',
    started_at TIMESTAMPTZ DEFAULT NULL,
    finished_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(branding_hash, platform, arch, format)
);
