-- user_peer_grants on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS user_peer_grants (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    peer_id TEXT NOT NULL,
    granted_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, peer_id)
);
CREATE INDEX IF NOT EXISTS idx_user_peer_grants_user ON user_peer_grants (user_id);
