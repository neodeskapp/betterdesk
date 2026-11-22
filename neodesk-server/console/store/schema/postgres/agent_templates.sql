-- agent_templates on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS agent_templates (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    config_json TEXT NOT NULL DEFAULT '{}',
    enrollment_token TEXT NOT NULL UNIQUE,
    enrolled_device_id TEXT DEFAULT NULL,
    enrolled_at TIMESTAMPTZ DEFAULT NULL,
    created_by INTEGER DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
