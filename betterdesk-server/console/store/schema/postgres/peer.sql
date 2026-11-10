-- peer on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS peer (
    id TEXT PRIMARY KEY,
    uuid TEXT DEFAULT '',
    pk BYTEA,
    note TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    status_online BOOLEAN DEFAULT FALSE,
    last_online TIMESTAMPTZ,
    is_deleted BOOLEAN DEFAULT FALSE,
    info JSONB DEFAULT '{}',
    ip TEXT DEFAULT '',
    "user" TEXT DEFAULT '',
    is_banned BOOLEAN DEFAULT FALSE,
    banned_at TIMESTAMPTZ,
    banned_reason TEXT DEFAULT '',
    folder_id INTEGER DEFAULT NULL
);
