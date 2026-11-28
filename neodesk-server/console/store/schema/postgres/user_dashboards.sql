-- user_dashboards on PostgreSQL: the overview's boards an account arranged
-- for itself and keeps on every browser. The Go console added it; Node had
-- no such table. boards is the JSON of the layout (package
-- console/dashboards).
CREATE TABLE IF NOT EXISTS user_dashboards (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    boards TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
