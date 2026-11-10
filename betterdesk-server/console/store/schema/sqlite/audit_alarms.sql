-- audit_alarms on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS audit_alarms (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                alarm_type INTEGER NOT NULL,
                alarm_name TEXT DEFAULT '',
                host_id TEXT DEFAULT '',
                peer_id TEXT DEFAULT '',
                ip TEXT DEFAULT '',
                details TEXT DEFAULT '{}',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_audit_alarms_type ON audit_alarms (alarm_type, created_at);
