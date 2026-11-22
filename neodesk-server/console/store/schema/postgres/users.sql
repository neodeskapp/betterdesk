-- users on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT DEFAULT 'admin',
    auth_provider TEXT DEFAULT 'local',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    last_login TIMESTAMPTZ,
    preferred_language TEXT DEFAULT NULL,
    totp_secret TEXT DEFAULT NULL,
    totp_enabled BOOLEAN DEFAULT FALSE,
    totp_recovery_codes TEXT DEFAULT NULL
);
