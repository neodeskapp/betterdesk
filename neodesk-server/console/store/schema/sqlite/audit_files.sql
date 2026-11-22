-- audit_files on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS audit_files (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                host_id TEXT NOT NULL,
                host_uuid TEXT DEFAULT '',
                peer_id TEXT DEFAULT '',
                direction INTEGER DEFAULT 0,
                path TEXT DEFAULT '',
                is_file INTEGER DEFAULT 1,
                num_files INTEGER DEFAULT 0,
                files_json TEXT DEFAULT '[]',
                ip TEXT DEFAULT '',
                peer_name TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_audit_files_host ON audit_files (host_id, created_at);
