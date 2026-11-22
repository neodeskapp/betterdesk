-- device_group_members on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS device_group_members (
    device_group_id INTEGER NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
    peer_id TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (device_group_id, peer_id)
);
