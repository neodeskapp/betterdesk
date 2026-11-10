-- alert_history on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS alert_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                rule_id INTEGER NOT NULL,
                device_id TEXT DEFAULT NULL,
                severity TEXT DEFAULT 'warning',
                message TEXT NOT NULL DEFAULT '',
                triggered_at TEXT NOT NULL,
                acknowledged INTEGER DEFAULT 0,
                acknowledged_by TEXT DEFAULT NULL,
                acknowledged_at TEXT DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (rule_id) REFERENCES alert_rules(id) ON DELETE CASCADE
            );
CREATE INDEX IF NOT EXISTS idx_alert_history_rule ON alert_history (rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_history_device ON alert_history (device_id);
