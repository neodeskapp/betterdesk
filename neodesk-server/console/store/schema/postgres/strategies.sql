-- strategies on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS strategies (
    id SERIAL PRIMARY KEY,
    guid TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    user_group_guid TEXT DEFAULT '',
    device_group_guid TEXT DEFAULT '',
    enabled BOOLEAN DEFAULT TRUE,
    permissions JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
