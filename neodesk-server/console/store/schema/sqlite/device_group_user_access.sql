-- device_group_user_access on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
CREATE TABLE IF NOT EXISTS device_group_user_access (
                device_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
