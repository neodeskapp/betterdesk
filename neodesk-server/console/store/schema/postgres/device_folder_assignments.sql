-- device_folder_assignments on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS device_folder_assignments (
    device_id TEXT PRIMARY KEY NOT NULL,
    folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    assigned_at TIMESTAMPTZ DEFAULT NOW()
);
