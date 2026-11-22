-- relay_sessions on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS relay_sessions (
                id TEXT PRIMARY KEY,
                initiator_id TEXT NOT NULL,
                target_id TEXT NOT NULL,
                initiator_pk TEXT,
                target_pk TEXT,
                status TEXT DEFAULT 'pending',
                created_at TEXT DEFAULT (datetime('now')),
                expires_at TEXT NOT NULL
            );
