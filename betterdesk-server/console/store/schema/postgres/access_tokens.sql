-- access_tokens on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS access_tokens (
    id SERIAL PRIMARY KEY,
    token TEXT UNIQUE NOT NULL,
    token_hash TEXT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id TEXT DEFAULT '',
    client_uuid TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_used TIMESTAMPTZ,
    ip_address TEXT DEFAULT '',
    revoked BOOLEAN DEFAULT FALSE
);
