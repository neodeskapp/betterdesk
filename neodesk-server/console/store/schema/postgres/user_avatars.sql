-- user_avatars on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS user_avatars (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    mime_type TEXT NOT NULL,
    data BYTEA NOT NULL,
    version TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'oidc',
    source_url TEXT DEFAULT '',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
