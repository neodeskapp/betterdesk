-- user_dashboards on SQLite: the overview's boards an account arranged for
-- itself and keeps on every browser. The Go console added it; Node had no
-- such table. boards is the JSON of the layout (package console/dashboards).
CREATE TABLE IF NOT EXISTS user_dashboards (
    user_id INTEGER PRIMARY KEY,
    boards TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
