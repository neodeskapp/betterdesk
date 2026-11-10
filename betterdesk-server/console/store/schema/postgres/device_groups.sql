-- device_groups on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS device_groups (
    id SERIAL PRIMARY KEY,
    guid TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    note TEXT DEFAULT '',
    team_id TEXT DEFAULT '',
    source_type TEXT DEFAULT 'manual',
    tag_filter TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
