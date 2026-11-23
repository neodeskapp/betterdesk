PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE peer (
                id TEXT PRIMARY KEY,
                uuid TEXT DEFAULT '',
                pk BLOB,
                note TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now')),
                status_online INTEGER DEFAULT 0,
                last_online TEXT,
                is_deleted INTEGER DEFAULT 0,
                info TEXT DEFAULT '',
                ip TEXT DEFAULT '',
                "user" TEXT DEFAULT '',
                is_banned INTEGER DEFAULT 0,
                banned_at TEXT,
                banned_reason TEXT DEFAULT '',
                folder_id INTEGER DEFAULT NULL
            , tags TEXT DEFAULT '');
INSERT INTO peer VALUES('PEER1','u1',NULL,'','2026-11-18 14:37:15',0,NULL,0,'{}','','',0,NULL,'',NULL,'');
INSERT INTO peer VALUES('PEER2','u2',NULL,'','2026-11-18 14:37:15',0,NULL,0,'{}','','',0,NULL,'',NULL,'');
CREATE TABLE device_inventory (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                hardware TEXT DEFAULT '{}',
                software TEXT DEFAULT '{}',
                collected_at TEXT,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
CREATE TABLE device_telemetry (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                cpu_usage_percent REAL DEFAULT 0,
                memory_used_bytes INTEGER DEFAULT 0,
                memory_total_bytes INTEGER DEFAULT 0,
                uptime_secs INTEGER DEFAULT 0,
                timestamp TEXT,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
CREATE TABLE activity_sessions (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                app_name TEXT NOT NULL DEFAULT '',
                window_title TEXT NOT NULL DEFAULT '',
                category TEXT DEFAULT 'other',
                started_at TEXT NOT NULL,
                ended_at TEXT NOT NULL,
                duration_secs INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE activity_summaries (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                idle_seconds INTEGER DEFAULT 0,
                session_count INTEGER DEFAULT 0,
                total_active_secs INTEGER DEFAULT 0,
                reported_at TEXT NOT NULL,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id, reported_at)
            );
CREATE TABLE network_targets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                host TEXT DEFAULT '',
                port INTEGER DEFAULT NULL,
                url TEXT DEFAULT NULL,
                check_type TEXT NOT NULL DEFAULT 'ping',
                timeout_ms INTEGER DEFAULT 5000,
                interval_ms INTEGER DEFAULT 60000,
                enabled INTEGER DEFAULT 1,
                last_status TEXT DEFAULT NULL,
                last_check_at TEXT DEFAULT NULL,
                last_rtt_ms REAL DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE network_checks (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                target_id INTEGER NOT NULL,
                status TEXT NOT NULL DEFAULT 'unknown',
                rtt_ms REAL DEFAULT NULL,
                status_code INTEGER DEFAULT NULL,
                error_msg TEXT DEFAULT NULL,
                checked_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (target_id) REFERENCES network_targets(id) ON DELETE CASCADE
            );
CREATE TABLE dlp_policies (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                description TEXT DEFAULT '',
                policy_type TEXT DEFAULT '',
                action TEXT DEFAULT 'log',
                scope TEXT DEFAULT '',
                enabled INTEGER DEFAULT 1,
                rules TEXT DEFAULT '[]',
                created_at DATETIME DEFAULT (datetime('now')),
                updated_at DATETIME DEFAULT (datetime('now'))
            );
CREATE TABLE dlp_events (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                event_source TEXT NOT NULL DEFAULT 'unknown',
                event_type TEXT NOT NULL DEFAULT 'info',
                policy_id INTEGER DEFAULT NULL,
                policy_name TEXT DEFAULT '',
                action TEXT DEFAULT 'log',
                details TEXT DEFAULT '{}',
                created_at DATETIME DEFAULT (datetime('now')),
                FOREIGN KEY (policy_id) REFERENCES dlp_policies(id) ON DELETE SET NULL
            );
CREATE TABLE saved_reports (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                title TEXT NOT NULL,
                report_type TEXT NOT NULL,
                filters TEXT DEFAULT '{}',
                payload TEXT DEFAULT '{}',
                created_by TEXT NOT NULL DEFAULT 'admin',
                created_at DATETIME DEFAULT (datetime('now'))
            );
CREATE TABLE studio_flows (
                id TEXT PRIMARY KEY,
                name TEXT NOT NULL,
                description TEXT NOT NULL DEFAULT '',
                flow_json TEXT NOT NULL DEFAULT '{}',
                status TEXT NOT NULL DEFAULT 'draft',
                version INTEGER NOT NULL DEFAULT 1,
                created_by TEXT NOT NULL DEFAULT '',
                created_at TEXT NOT NULL,
                updated_at TEXT NOT NULL
            );
CREATE TABLE tenants (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                slug TEXT NOT NULL UNIQUE,
                contact_name TEXT DEFAULT '',
                contact_email TEXT DEFAULT '',
                max_devices INTEGER DEFAULT 0,
                notes TEXT DEFAULT '',
                active INTEGER DEFAULT 1,
                created_at DATETIME DEFAULT (datetime('now')),
                updated_at DATETIME DEFAULT (datetime('now'))
            );
INSERT INTO tenants VALUES(1,'Tenant A','tenant-a','','',0,'',1,'2026-11-18 14:37:15','2026-11-18 14:37:15');
CREATE TABLE tenant_devices (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                tenant_id INTEGER NOT NULL,
                device_id TEXT NOT NULL,
                assigned_at DATETIME DEFAULT (datetime('now')),
                UNIQUE(tenant_id, device_id),
                FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
            );
CREATE TABLE tenant_users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                tenant_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                assigned_at DATETIME DEFAULT (datetime('now')),
                UNIQUE(tenant_id, user_id),
                FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
            );
INSERT INTO tenant_users VALUES(1,1,2,'2026-11-18 14:37:15');
INSERT INTO tenant_users VALUES(2,1,4,'2026-11-18 14:37:15');
INSERT INTO tenant_users VALUES(3,1,3,'2026-11-18 14:37:15');
CREATE TABLE pending_registrations (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                hostname TEXT DEFAULT '',
                platform TEXT DEFAULT '',
                version TEXT DEFAULT '',
                ip_address TEXT DEFAULT '',
                public_key TEXT DEFAULT '',
                uuid TEXT DEFAULT '',
                status TEXT DEFAULT 'pending',
                approved_by TEXT DEFAULT NULL,
                approved_at TEXT DEFAULT NULL,
                rejected_reason TEXT DEFAULT '',
                access_token TEXT DEFAULT NULL,
                console_url TEXT DEFAULT NULL,
                server_address TEXT DEFAULT NULL,
                server_key TEXT DEFAULT NULL,
                created_at DATETIME DEFAULT (datetime('now')),
                updated_at DATETIME DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
CREATE TABLE agent_bundles (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                bundle_id TEXT NOT NULL UNIQUE,
                slug TEXT DEFAULT NULL,
                name TEXT NOT NULL,
                branding TEXT NOT NULL DEFAULT '{}',
                branding_hash TEXT NOT NULL DEFAULT '',
                config_fingerprint TEXT NOT NULL DEFAULT '',
                client_commit TEXT DEFAULT NULL,
                active_generation_hash TEXT DEFAULT NULL,
                created_by INTEGER DEFAULT NULL,
                product_type TEXT NOT NULL DEFAULT 'neodesk-support',
                revoked INTEGER NOT NULL DEFAULT 0,
                download_count INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
INSERT INTO agent_bundles VALUES(1,'b-1','bundle','Bundle','{}','','',NULL,NULL,2,'neodesk-support',0,0,'2026-11-18 14:37:15','2026-11-18 14:37:15');
INSERT INTO agent_bundles VALUES(2,'b-2','old','Old','{}','','',NULL,NULL,4,'neodesk-support',0,0,'2026-11-18 14:37:15','2026-11-18 14:37:15');
CREATE TABLE agent_bundle_builds (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                branding_hash TEXT NOT NULL,
                platform TEXT NOT NULL,
                arch TEXT NOT NULL DEFAULT 'x64',
                format TEXT NOT NULL,
                status TEXT NOT NULL DEFAULT 'queued',
                artifact_path TEXT DEFAULT NULL,
                artifact_size INTEGER DEFAULT 0,
                artifact_sha256 TEXT DEFAULT NULL,
                error_message TEXT DEFAULT '',
                client_commit TEXT DEFAULT NULL,
                generation_id TEXT DEFAULT NULL,
                config_fingerprint TEXT DEFAULT NULL,
                progress_percent INTEGER NOT NULL DEFAULT 0,
                progress_phase TEXT DEFAULT '',
                started_at TEXT DEFAULT NULL,
                finished_at TEXT DEFAULT NULL,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now')),
                UNIQUE(branding_hash, platform, arch, format)
            );
CREATE TABLE panel_sessions (
                sid TEXT PRIMARY KEY,
                session_json TEXT NOT NULL,
                expires_at INTEGER NOT NULL,
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            , user_id INTEGER, username TEXT, ip_address TEXT, user_agent TEXT, client_fingerprint TEXT, created_at INTEGER, last_activity_at INTEGER, ended_at INTEGER, end_reason TEXT, is_active INTEGER NOT NULL DEFAULT 1);
INSERT INTO panel_sessions VALUES('sid-admin','{"cookie":{},"userId":1,"username":"x"}',1795099035483,'2026-11-18 14:37:15',1,'x',NULL,NULL,NULL,1795012635483,1795012635483,NULL,NULL,1);
INSERT INTO panel_sessions VALUES('sid-alice','{"cookie":{},"userId":2,"username":"x"}',1795099035483,'2026-11-18 14:37:15',2,'x',NULL,NULL,NULL,1795012635483,1795012635483,NULL,NULL,1);
INSERT INTO panel_sessions VALUES('sid-carol','{"cookie":{},"userId":4,"username":"x"}',1795099035483,'2026-11-18 14:37:15',4,'x',NULL,NULL,NULL,1795012635483,1795012635483,NULL,NULL,1);
INSERT INTO panel_sessions VALUES('sid-anon','{}',1795099035483,'2026-11-18 14:37:15',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('tenants',1);
INSERT INTO sqlite_sequence VALUES('tenant_users',3);
INSERT INTO sqlite_sequence VALUES('agent_bundles',2);
CREATE INDEX idx_activity_device ON activity_sessions (device_id);
CREATE INDEX idx_activity_time ON activity_sessions (started_at);
CREATE INDEX idx_network_checks_target ON network_checks (target_id);
CREATE INDEX idx_network_checks_time ON network_checks (checked_at);
CREATE INDEX idx_dlp_events_device ON dlp_events (device_id);
CREATE INDEX idx_dlp_events_time ON dlp_events (created_at);
CREATE INDEX idx_dlp_events_source ON dlp_events (event_source);
CREATE INDEX idx_saved_reports_type ON saved_reports (report_type);
CREATE INDEX idx_studio_flows_owner ON studio_flows (created_by, updated_at);
CREATE INDEX idx_tenant_devices_tenant ON tenant_devices (tenant_id);
CREATE INDEX idx_tenant_devices_device ON tenant_devices (device_id);
CREATE INDEX idx_tenant_users_tenant ON tenant_users (tenant_id);
CREATE INDEX idx_pending_reg_status ON pending_registrations (status);
CREATE INDEX idx_pending_reg_device ON pending_registrations (device_id);
CREATE INDEX idx_agent_bundles_bundle_id ON agent_bundles (bundle_id);
CREATE INDEX idx_agent_bundles_hash ON agent_bundles (branding_hash);
CREATE INDEX idx_agent_bundle_builds_hash ON agent_bundle_builds (branding_hash);
CREATE INDEX idx_agent_bundle_builds_status ON agent_bundle_builds (status);
CREATE UNIQUE INDEX idx_agent_bundles_slug ON agent_bundles (slug) WHERE slug IS NOT NULL AND slug != '';
CREATE INDEX idx_panel_sessions_expires ON panel_sessions (expires_at);
CREATE INDEX idx_panel_sessions_user_active
                ON panel_sessions (user_id, is_active);
CREATE INDEX idx_panel_sessions_active
                ON panel_sessions (is_active, expires_at);
COMMIT;
