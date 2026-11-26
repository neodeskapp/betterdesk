-- user_preferences on PostgreSQL: what an account chose for itself in the
-- console and keeps on every browser. The Go console added it; Node had no
-- such table. accessibility is the JSON of the accessibility settings.
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    accessibility TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
