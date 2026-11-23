-- agent_bundles on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS agent_bundles (
    id SERIAL PRIMARY KEY,
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
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    download_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
