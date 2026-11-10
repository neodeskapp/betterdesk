-- users on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                password_hash TEXT NOT NULL,
                role TEXT DEFAULT 'admin',
                auth_provider TEXT DEFAULT 'local',
                created_at TEXT DEFAULT (datetime('now')),
                last_login TEXT,
                preferred_language TEXT DEFAULT NULL,
                totp_secret TEXT DEFAULT NULL,
                totp_enabled INTEGER DEFAULT 0,
                totp_recovery_codes TEXT DEFAULT NULL
            );
