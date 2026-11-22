-- audit_connections on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS audit_connections (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                host_id TEXT NOT NULL,
                host_uuid TEXT DEFAULT '',
                peer_id TEXT DEFAULT '',
                peer_name TEXT DEFAULT '',
                action TEXT NOT NULL,
                conn_type INTEGER DEFAULT 0,
                session_id TEXT DEFAULT '',
                ip TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_audit_conn_host ON audit_connections (host_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_conn_peer ON audit_connections (peer_id, created_at);
