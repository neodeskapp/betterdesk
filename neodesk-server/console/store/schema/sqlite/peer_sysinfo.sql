-- peer_sysinfo on SQLite. SQLite keeps the text of a CREATE statement, so the
-- statements below are byte for byte what the Node console ran.
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
                displays TEXT DEFAULT '[]',
                encoding TEXT DEFAULT '[]',
                features TEXT DEFAULT '{}',
                platform_additions TEXT DEFAULT '{}',
                raw_json TEXT DEFAULT '{}',
                updated_at TEXT DEFAULT (datetime('now'))
            );
