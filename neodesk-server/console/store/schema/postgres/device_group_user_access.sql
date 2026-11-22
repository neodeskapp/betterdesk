-- device_group_user_access on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS device_group_user_access (
    device_group_id INTEGER NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (device_group_id, user_id)
);
