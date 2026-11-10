-- address_books on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS address_books (
    username TEXT NOT NULL,
    ab_type TEXT NOT NULL DEFAULT 'legacy',
    data TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (username, ab_type)
);
