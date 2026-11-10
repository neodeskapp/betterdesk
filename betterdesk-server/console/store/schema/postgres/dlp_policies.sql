-- dlp_policies on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS dlp_policies (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    policy_type TEXT DEFAULT '',
    action TEXT DEFAULT 'log',
    scope TEXT DEFAULT '',
    enabled BOOLEAN DEFAULT TRUE,
    rules JSONB DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
