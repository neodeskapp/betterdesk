-- dlp_policies on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS dlp_policies (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                description TEXT DEFAULT '',
                policy_type TEXT DEFAULT '',
                action TEXT DEFAULT 'log',
                scope TEXT DEFAULT '',
                enabled INTEGER DEFAULT 1,
                rules TEXT DEFAULT '[]',
                created_at DATETIME DEFAULT (datetime('now')),
                updated_at DATETIME DEFAULT (datetime('now'))
            );
