-- peer on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS peer (
                id TEXT PRIMARY KEY,
                uuid TEXT DEFAULT '',
                pk BLOB,
                note TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now')),
                status_online INTEGER DEFAULT 0,
                last_online TEXT,
                is_deleted INTEGER DEFAULT 0,
                info TEXT DEFAULT '',
                ip TEXT DEFAULT '',
                "user" TEXT DEFAULT '',
                is_banned INTEGER DEFAULT 0,
                banned_at TEXT,
                banned_reason TEXT DEFAULT '',
                folder_id INTEGER DEFAULT NULL
            );
