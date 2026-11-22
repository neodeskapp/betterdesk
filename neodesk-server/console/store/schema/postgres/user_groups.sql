-- user_groups on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS user_groups (
    id SERIAL PRIMARY KEY,
    guid TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    note TEXT DEFAULT '',
    team_id TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
