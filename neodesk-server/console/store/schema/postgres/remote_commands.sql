-- remote_commands on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS remote_commands (
    id SERIAL PRIMARY KEY,
    device_id TEXT NOT NULL,
    command_type TEXT NOT NULL DEFAULT 'shell',
    payload TEXT NOT NULL DEFAULT '',
    status TEXT DEFAULT 'pending',
    result TEXT DEFAULT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    executed_at TIMESTAMPTZ DEFAULT NULL,
    completed_at TIMESTAMPTZ DEFAULT NULL
);
