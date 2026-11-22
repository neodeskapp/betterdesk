-- account_lockouts on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS account_lockouts (
    id SERIAL PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    locked_until TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
