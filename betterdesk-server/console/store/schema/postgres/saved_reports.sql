-- saved_reports on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS saved_reports (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    report_type TEXT NOT NULL,
    filters JSONB DEFAULT '{}',
    payload JSONB DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT 'admin',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_saved_reports_type ON saved_reports (report_type);
