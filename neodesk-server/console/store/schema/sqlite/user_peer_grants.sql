-- user_peer_grants on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS user_peer_grants (
                user_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                granted_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, peer_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE INDEX IF NOT EXISTS idx_user_peer_grants_user ON user_peer_grants (user_id);
