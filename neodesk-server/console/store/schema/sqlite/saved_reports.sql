-- saved_reports on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS saved_reports (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                title TEXT NOT NULL,
                report_type TEXT NOT NULL,
                filters TEXT DEFAULT '{}',
                payload TEXT DEFAULT '{}',
                created_by TEXT NOT NULL DEFAULT 'admin',
                created_at DATETIME DEFAULT (datetime('now'))
            );
CREATE INDEX IF NOT EXISTS idx_saved_reports_type ON saved_reports (report_type);
