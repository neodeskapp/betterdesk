-- Schema the Node console v3.5.200 created on an empty SQLite install (main file and auth.db in one).
-- Produced by running that version's adapter init(), then: sqlite3 <file> .schema
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
CREATE TABLE device_inventory (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                hardware TEXT DEFAULT '{}',
                software TEXT DEFAULT '{}',
                collected_at TEXT,
                received_at TEXT DEFAULT (datetime('now')),
                UNIQUE(device_id)
            );
CREATE TABLE sqlite_sequence(name,seq);
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
CREATE INDEX idx_activity_device ON activity_sessions (device_id);
CREATE INDEX idx_activity_time ON activity_sessions (started_at);
CREATE TABLE tickets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                title TEXT NOT NULL,
                description TEXT DEFAULT '',
                status TEXT DEFAULT 'open',
                priority TEXT DEFAULT 'medium',
                category TEXT DEFAULT 'general',
                device_id TEXT DEFAULT NULL,
                created_by TEXT NOT NULL,
                assigned_to TEXT DEFAULT NULL,
                sla_due_at TEXT DEFAULT NULL,
                resolved_at TEXT DEFAULT NULL,
                closed_at TEXT DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE ticket_comments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ticket_id INTEGER NOT NULL,
                author TEXT NOT NULL,
                body TEXT NOT NULL,
                is_internal INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
            );
CREATE TABLE ticket_attachments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ticket_id INTEGER NOT NULL,
                filename TEXT NOT NULL,
                mimetype TEXT DEFAULT 'application/octet-stream',
                size_bytes INTEGER DEFAULT 0,
                storage_path TEXT NOT NULL,
                uploaded_by TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
            );
CREATE TABLE alert_rules (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                description TEXT DEFAULT '',
                enabled INTEGER DEFAULT 1,
                condition_type TEXT NOT NULL,
                condition_op TEXT NOT NULL DEFAULT 'gt',
                condition_value REAL NOT NULL DEFAULT 0,
                severity TEXT DEFAULT 'warning',
                scope_device_id TEXT DEFAULT NULL,
                cooldown_secs INTEGER DEFAULT 300,
                notify_emails TEXT DEFAULT '',
                created_by TEXT DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE alert_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                rule_id INTEGER NOT NULL,
                device_id TEXT DEFAULT NULL,
                severity TEXT DEFAULT 'warning',
                message TEXT NOT NULL DEFAULT '',
                triggered_at TEXT NOT NULL,
                acknowledged INTEGER DEFAULT 0,
                acknowledged_by TEXT DEFAULT NULL,
                acknowledged_at TEXT DEFAULT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (rule_id) REFERENCES alert_rules(id) ON DELETE CASCADE
            );
CREATE TABLE remote_commands (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_id TEXT NOT NULL,
                command_type TEXT NOT NULL DEFAULT 'shell',
                payload TEXT NOT NULL DEFAULT '',
                status TEXT DEFAULT 'pending',
                result TEXT DEFAULT NULL,
                created_by TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                executed_at TEXT DEFAULT NULL,
                completed_at TEXT DEFAULT NULL
            );
CREATE INDEX idx_alert_history_rule ON alert_history (rule_id);
CREATE INDEX idx_alert_history_device ON alert_history (device_id);
CREATE INDEX idx_remote_commands_device ON remote_commands (device_id, status);
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
CREATE INDEX idx_network_checks_target ON network_checks (target_id);
CREATE INDEX idx_network_checks_time ON network_checks (checked_at);
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
CREATE INDEX idx_dlp_events_device ON dlp_events (device_id);
CREATE INDEX idx_dlp_events_time ON dlp_events (created_at);
CREATE INDEX idx_dlp_events_source ON dlp_events (event_source);
CREATE TABLE saved_reports (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                title TEXT NOT NULL,
                report_type TEXT NOT NULL,
                filters TEXT DEFAULT '{}',
                payload TEXT DEFAULT '{}',
                created_by TEXT NOT NULL DEFAULT 'admin',
                created_at DATETIME DEFAULT (datetime('now'))
            );
CREATE INDEX idx_saved_reports_type ON saved_reports (report_type);
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
CREATE INDEX idx_tenant_devices_tenant ON tenant_devices (tenant_id);
CREATE INDEX idx_tenant_devices_device ON tenant_devices (device_id);
CREATE INDEX idx_tenant_users_tenant ON tenant_users (tenant_id);
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
CREATE INDEX idx_pending_reg_status ON pending_registrations (status);
CREATE INDEX idx_pending_reg_device ON pending_registrations (device_id);
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
                product_type TEXT NOT NULL DEFAULT 'betterdesk-support',
                revoked INTEGER NOT NULL DEFAULT 0,
                download_count INTEGER NOT NULL DEFAULT 0,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
CREATE INDEX idx_agent_bundles_bundle_id ON agent_bundles (bundle_id);
CREATE INDEX idx_agent_bundles_hash ON agent_bundles (branding_hash);
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
CREATE INDEX idx_agent_bundle_builds_hash ON agent_bundle_builds (branding_hash);
CREATE INDEX idx_agent_bundle_builds_status ON agent_bundle_builds (status);
CREATE UNIQUE INDEX idx_agent_bundles_slug ON agent_bundles (slug) WHERE slug IS NOT NULL AND slug != '';
CREATE TABLE users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                password_hash TEXT NOT NULL,
                role TEXT DEFAULT 'admin',
                auth_provider TEXT DEFAULT 'local',
                created_at TEXT DEFAULT (datetime('now')),
                last_login TEXT,
                preferred_language TEXT DEFAULT NULL,
                totp_secret TEXT DEFAULT NULL,
                totp_enabled INTEGER DEFAULT 0,
                totp_recovery_codes TEXT DEFAULT NULL
            , first_name TEXT DEFAULT '', last_name TEXT DEFAULT '', email TEXT DEFAULT '', phone TEXT DEFAULT '', role_display TEXT DEFAULT '', avatar_url TEXT DEFAULT '', guid TEXT DEFAULT '');
CREATE TABLE audit_log (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER,
                action TEXT NOT NULL,
                details TEXT,
                ip_address TEXT,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX idx_audit_log_created ON audit_log (created_at);
CREATE INDEX idx_audit_log_action ON audit_log (action, created_at);
CREATE TABLE access_tokens (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                token TEXT UNIQUE NOT NULL,
                token_hash TEXT,
                user_id INTEGER NOT NULL,
                client_id TEXT DEFAULT '',
                client_uuid TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now')),
                expires_at TEXT NOT NULL,
                last_used TEXT,
                ip_address TEXT DEFAULT '',
                revoked INTEGER DEFAULT 0,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE TABLE login_attempts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT NOT NULL,
                ip_address TEXT DEFAULT '',
                success INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE account_lockouts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                locked_until TEXT NOT NULL,
                attempt_count INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE folders (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                color TEXT DEFAULT '#6366f1',
                icon TEXT DEFAULT 'folder',
                sort_order INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE address_books (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                ab_type TEXT DEFAULT 'legacy',
                data TEXT DEFAULT '{}',
                updated_at TEXT DEFAULT (datetime('now')),
                UNIQUE(user_id, ab_type),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE TABLE settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE branding_config (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE branding_profiles (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL UNIQUE,
                description TEXT DEFAULT '',
                data TEXT NOT NULL,
                is_active INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE relay_sessions (
                id TEXT PRIMARY KEY,
                initiator_id TEXT NOT NULL,
                target_id TEXT NOT NULL,
                initiator_pk TEXT,
                target_pk TEXT,
                status TEXT DEFAULT 'pending',
                created_at TEXT DEFAULT (datetime('now')),
                expires_at TEXT NOT NULL
            );
CREATE TABLE device_folder_assignments (
                device_id TEXT PRIMARY KEY NOT NULL,
                folder_id INTEGER NOT NULL,
                assigned_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (folder_id) REFERENCES folders(id) ON DELETE CASCADE
            );
CREATE TABLE peer_sysinfo (
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
CREATE TABLE peer_metrics (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                peer_id TEXT NOT NULL,
                cpu_usage REAL DEFAULT 0,
                memory_usage REAL DEFAULT 0,
                disk_usage REAL DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX idx_peer_metrics_peer_time ON peer_metrics (peer_id, created_at);
CREATE TABLE audit_connections (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                host_id TEXT NOT NULL,
                host_uuid TEXT DEFAULT '',
                peer_id TEXT DEFAULT '',
                peer_name TEXT DEFAULT '',
                action TEXT NOT NULL,
                conn_type INTEGER DEFAULT 0,
                session_id TEXT DEFAULT '',
                ip TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX idx_audit_conn_host ON audit_connections (host_id, created_at);
CREATE INDEX idx_audit_conn_peer ON audit_connections (peer_id, created_at);
CREATE TABLE audit_files (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                host_id TEXT NOT NULL,
                host_uuid TEXT DEFAULT '',
                peer_id TEXT DEFAULT '',
                direction INTEGER DEFAULT 0,
                path TEXT DEFAULT '',
                is_file INTEGER DEFAULT 1,
                num_files INTEGER DEFAULT 0,
                files_json TEXT DEFAULT '[]',
                ip TEXT DEFAULT '',
                peer_name TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX idx_audit_files_host ON audit_files (host_id, created_at);
CREATE TABLE audit_alarms (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                alarm_type INTEGER NOT NULL,
                alarm_name TEXT DEFAULT '',
                host_id TEXT DEFAULT '',
                peer_id TEXT DEFAULT '',
                ip TEXT DEFAULT '',
                details TEXT DEFAULT '{}',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE INDEX idx_audit_alarms_type ON audit_alarms (alarm_type, created_at);
CREATE TABLE user_groups (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE user_group_members (
                user_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_group_id, user_id),
                FOREIGN KEY (user_group_id) REFERENCES user_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE TABLE device_groups (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                source_type TEXT DEFAULT 'manual',
                tag_filter TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE device_group_members (
                device_group_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, peer_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE
            );
CREATE TABLE device_group_user_access (
                device_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE TABLE device_group_user_group_access (
                device_group_id INTEGER NOT NULL,
                user_group_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_group_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_group_id) REFERENCES user_groups(id) ON DELETE CASCADE
            );
CREATE TABLE strategies (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                user_group_guid TEXT DEFAULT '',
                device_group_guid TEXT DEFAULT '',
                enabled INTEGER DEFAULT 1,
                permissions TEXT DEFAULT '{}',
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
CREATE TABLE strategy_assignments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                target_type TEXT NOT NULL,
                target_key TEXT NOT NULL,
                strategy_guid TEXT NOT NULL DEFAULT '',
                updated_at TEXT DEFAULT (datetime('now')),
                UNIQUE(target_type, target_key)
            );
CREATE INDEX idx_strategy_assignments_strategy ON strategy_assignments (strategy_guid);
CREATE TABLE user_peer_grants (
                user_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                granted_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, peer_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE INDEX idx_user_peer_grants_user ON user_peer_grants (user_id);
CREATE TABLE notification_reads (
                user_id INTEGER NOT NULL,
                notification_id TEXT NOT NULL,
                read_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, notification_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
CREATE INDEX idx_notification_reads_user ON notification_reads (user_id, read_at);
CREATE TABLE email_notification_log (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                event_type TEXT NOT NULL,
                event_id TEXT NOT NULL,
                recipient TEXT NOT NULL,
                sent_at TEXT DEFAULT (datetime('now')),
                UNIQUE(event_type, event_id, recipient)
            );
CREATE INDEX idx_email_notification_log_event ON email_notification_log (event_type, event_id);
CREATE UNIQUE INDEX idx_access_tokens_hash ON access_tokens (token_hash)
            WHERE token_hash IS NOT NULL;
