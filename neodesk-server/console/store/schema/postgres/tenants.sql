-- tenants on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS tenants (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    contact_name TEXT DEFAULT '',
    contact_email TEXT DEFAULT '',
    max_devices INTEGER DEFAULT 0,
    notes TEXT DEFAULT '',
    active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
