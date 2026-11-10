-- branding_profiles on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS branding_profiles (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    data TEXT NOT NULL,
    is_active INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
