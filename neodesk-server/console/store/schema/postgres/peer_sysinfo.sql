-- peer_sysinfo on PostgreSQL, as the Node console created it.
CREATE TABLE IF NOT EXISTS peer_sysinfo (
    peer_id TEXT PRIMARY KEY,
    hostname TEXT DEFAULT '',
    username TEXT DEFAULT '',
    platform TEXT DEFAULT '',
    version TEXT DEFAULT '',
    cpu_name TEXT DEFAULT '',
    cpu_cores INTEGER DEFAULT 0,
    cpu_freq_ghz REAL DEFAULT 0,
    memory_gb REAL DEFAULT 0,
    os_full TEXT DEFAULT '',
    displays JSONB DEFAULT '[]',
    encoding JSONB DEFAULT '[]',
    features JSONB DEFAULT '{}',
    platform_additions JSONB DEFAULT '{}',
    raw_json JSONB DEFAULT '{}',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
