-- relay_sessions on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS relay_sessions (
    id TEXT PRIMARY KEY,
    initiator_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    initiator_pk TEXT,
    target_pk TEXT,
    status TEXT DEFAULT 'pending',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
