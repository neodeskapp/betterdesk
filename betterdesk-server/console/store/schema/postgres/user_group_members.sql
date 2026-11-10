-- user_group_members on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS user_group_members (
    user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_group_id, user_id)
);
