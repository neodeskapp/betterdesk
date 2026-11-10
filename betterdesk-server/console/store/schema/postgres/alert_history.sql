-- alert_history on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS alert_history (
    id SERIAL PRIMARY KEY,
    rule_id INTEGER NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    device_id TEXT DEFAULT NULL,
    severity TEXT DEFAULT 'warning',
    message TEXT NOT NULL DEFAULT '',
    triggered_at TIMESTAMPTZ NOT NULL,
    acknowledged BOOLEAN DEFAULT FALSE,
    acknowledged_by TEXT DEFAULT NULL,
    acknowledged_at TIMESTAMPTZ DEFAULT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
