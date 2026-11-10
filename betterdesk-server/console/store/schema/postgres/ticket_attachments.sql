-- ticket_attachments on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS ticket_attachments (
    id SERIAL PRIMARY KEY,
    ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    mimetype TEXT DEFAULT 'application/octet-stream',
    size_bytes INTEGER DEFAULT 0,
    storage_path TEXT NOT NULL,
    uploaded_by TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
