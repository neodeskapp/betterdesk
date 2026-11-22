-- strategy_assignments on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS strategy_assignments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                target_type TEXT NOT NULL,
                target_key TEXT NOT NULL,
                strategy_guid TEXT NOT NULL DEFAULT '',
                updated_at TEXT DEFAULT (datetime('now')),
                UNIQUE(target_type, target_key)
            );
CREATE INDEX IF NOT EXISTS idx_strategy_assignments_strategy ON strategy_assignments (strategy_guid);
