-- strategy_assignments on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS strategy_assignments (
    id SERIAL PRIMARY KEY,
    target_type TEXT NOT NULL,
    target_key TEXT NOT NULL,
    strategy_guid TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(target_type, target_key)
);
CREATE INDEX IF NOT EXISTS idx_strategy_assignments_strategy ON strategy_assignments (strategy_guid);
