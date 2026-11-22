-- login_attempts on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS login_attempts (
    id SERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    ip_address TEXT DEFAULT '',
    success BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
