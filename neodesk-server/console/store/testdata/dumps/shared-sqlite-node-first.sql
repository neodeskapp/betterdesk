-- One SQLite file used by both: Node console initialised first, then the Go server started on the same file.
-- Worktree betterdesk-go, dumped 2026-11-19 on 3f4ffd70 plus the change that adds agent_templates to store.Migrate (upstream 3.5.264 merged).
-- sqlite3 <file> .schema  (SQLite 3 CLI of the dev image, statements in creation order, text as stored in sqlite_master)
-- Do not edit: regenerate (see data.md, section "How the schema dumps were produced").
-- users.credentials_changed_at and the trigger users_credentials_changed (with its function on PostgreSQL) added on 2026-11-21 in the text the signal server's Migrate creates since RV181, at the place a regenerated dump has them; the Node console's part is unchanged.
-- users.disabled added on 2026-11-22 where the signal server's Migrate (its CREATE TABLE) or the console's (its last step) puts it since L107.

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
CREATE INDEX idx_studio_flows_owner ON studio_flows (created_by, updated_at);
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
CREATE TABLE agent_templates (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL UNIQUE,
                description TEXT NOT NULL DEFAULT '',
                config_json TEXT NOT NULL DEFAULT '{}',
                enrollment_token TEXT NOT NULL UNIQUE,
                enrolled_device_id TEXT DEFAULT NULL,
                enrolled_at TEXT DEFAULT NULL,
                created_by INTEGER DEFAULT NULL,
                created_at TEXT NOT NULL DEFAULT (datetime('now')),
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            );
CREATE INDEX idx_agent_templates_token ON agent_templates (enrollment_token);
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
            , first_name TEXT DEFAULT '', last_name TEXT DEFAULT '', email TEXT DEFAULT '', phone TEXT DEFAULT '', role_display TEXT DEFAULT '', avatar_url TEXT DEFAULT '', guid TEXT DEFAULT '', disabled INTEGER NOT NULL DEFAULT 0, is_server_admin INTEGER DEFAULT 0, credentials_changed_at INTEGER DEFAULT NULL);
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
CREATE TABLE panel_access_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                resource_key TEXT NOT NULL,
                access_level TEXT NOT NULL DEFAULT 'none',
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, resource_key)
            );
CREATE INDEX idx_panel_access_policies_subject
                ON panel_access_policies (subject_type, subject_id);
CREATE TABLE client_feature_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                feature_key TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 0,
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, feature_key)
            );
CREATE INDEX idx_client_feature_policies_subject
                ON client_feature_policies (subject_type, subject_id);
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
CREATE TABLE user_avatars (
                    user_id INTEGER PRIMARY KEY,
                    mime_type TEXT NOT NULL,
                    data BLOB NOT NULL,
                    version TEXT NOT NULL,
                    source TEXT NOT NULL DEFAULT 'oidc',
                    source_url TEXT DEFAULT '',
                    updated_at TEXT DEFAULT (datetime('now')),
                    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
                );
CREATE UNIQUE INDEX idx_access_tokens_hash ON access_tokens (token_hash)
            WHERE token_hash IS NOT NULL;
CREATE TABLE panel_sessions (
                sid TEXT PRIMARY KEY,
                session_json TEXT NOT NULL,
                expires_at INTEGER NOT NULL,
                updated_at TEXT NOT NULL DEFAULT (datetime('now'))
            , user_id INTEGER, username TEXT, ip_address TEXT, user_agent TEXT, client_fingerprint TEXT, created_at INTEGER, last_activity_at INTEGER, ended_at INTEGER, end_reason TEXT, is_active INTEGER NOT NULL DEFAULT 1);
CREATE INDEX idx_panel_sessions_expires ON panel_sessions (expires_at);
CREATE INDEX idx_panel_sessions_user_active
                ON panel_sessions (user_id, is_active);
CREATE INDEX idx_panel_sessions_active
                ON panel_sessions (is_active, expires_at);
CREATE TABLE peers (
			id TEXT PRIMARY KEY,
			uuid TEXT DEFAULT '',
			pk BLOB DEFAULT NULL,
			ip TEXT DEFAULT '',
			user TEXT DEFAULT '',
			hostname TEXT DEFAULT '',
			os TEXT DEFAULT '',
			version TEXT DEFAULT '',
			status TEXT DEFAULT 'OFFLINE',
			nat_type INTEGER DEFAULT 0,
			last_online TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			disabled INTEGER DEFAULT 0,
			banned INTEGER DEFAULT 0,
			ban_reason TEXT DEFAULT '',
			banned_at TEXT DEFAULT NULL,
			soft_deleted INTEGER DEFAULT 0,
			deleted_at TEXT DEFAULT NULL,
			note TEXT DEFAULT '',
			tags TEXT DEFAULT '',
			heartbeat_seq INTEGER DEFAULT 0
		, device_type TEXT DEFAULT '', linked_peer_id TEXT DEFAULT '', display_name TEXT DEFAULT '', guid TEXT DEFAULT '');
CREATE INDEX idx_peers_uuid ON peers(uuid);
CREATE INDEX idx_peers_status ON peers(status);
CREATE TABLE server_config (
			key TEXT PRIMARY KEY,
			value TEXT DEFAULT ''
		);
CREATE TABLE remote_targets (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('rdp', 'vnc')),
			platform TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			credential_mode TEXT NOT NULL DEFAULT 'prompt',
			credential_ciphertext TEXT NOT NULL DEFAULT '',
			credential_nonce TEXT NOT NULL DEFAULT '',
			credential_key_id TEXT NOT NULL DEFAULT '',
			tls_mode TEXT NOT NULL DEFAULT 'preferred',
			cert_fingerprint TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			last_test_at TEXT DEFAULT NULL,
			last_test_status TEXT NOT NULL DEFAULT '',
			last_test_error TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
CREATE INDEX idx_remote_targets_org ON remote_targets(org_id);
CREATE INDEX idx_remote_targets_enabled ON remote_targets(enabled);
CREATE TABLE id_change_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			old_id TEXT NOT NULL,
			new_id TEXT NOT NULL,
			changed_at TEXT DEFAULT (datetime('now')),
			reason TEXT DEFAULT ''
		);
CREATE INDEX idx_id_history_old ON id_change_history(old_id);
CREATE INDEX idx_id_history_new ON id_change_history(new_id);
CREATE UNIQUE INDEX idx_users_username ON users(username);
CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key_hash TEXT UNIQUE NOT NULL,
			key_prefix TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'viewer',
			created_at TEXT DEFAULT (datetime('now')),
			expires_at TEXT DEFAULT '',
			last_used TEXT DEFAULT ''
		);
CREATE UNIQUE INDEX idx_api_keys_hash ON api_keys(key_hash);
CREATE TABLE device_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT UNIQUE NOT NULL,
			token_prefix TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			peer_id TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			max_uses INTEGER DEFAULT 1,
			use_count INTEGER DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now')),
			expires_at TEXT DEFAULT NULL,
			revoked_at TEXT DEFAULT NULL,
			last_used_at TEXT DEFAULT NULL,
			created_by TEXT DEFAULT '',
			note TEXT DEFAULT ''
		);
CREATE UNIQUE INDEX idx_device_tokens_hash ON device_tokens(token_hash);
CREATE INDEX idx_device_tokens_peer ON device_tokens(peer_id);
CREATE INDEX idx_device_tokens_status ON device_tokens(status);
CREATE INDEX idx_peer_metrics_peer ON peer_metrics(peer_id);
CREATE INDEX idx_peer_metrics_created ON peer_metrics(created_at);
CREATE TABLE peer_availability_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			peer_id TEXT NOT NULL,
			from_status TEXT NOT NULL DEFAULT '',
			to_status TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			created_at TEXT DEFAULT (datetime('now'))
		);
CREATE INDEX idx_peer_avail_peer_created
			ON peer_availability_events(peer_id, created_at);
CREATE INDEX idx_peer_avail_created
			ON peer_availability_events(created_at);
CREATE TABLE peer_connection_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host_id TEXT NOT NULL,
			peer_id TEXT NOT NULL DEFAULT '',
			peer_name TEXT NOT NULL DEFAULT '',
			peer_ip TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			transport TEXT NOT NULL DEFAULT '',
			relay_uuid TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT,
			source TEXT NOT NULL DEFAULT 'signal'
		);
CREATE INDEX idx_peer_conn_sess_host_started
			ON peer_connection_sessions(host_id, started_at DESC);
CREATE INDEX idx_peer_conn_sess_relay
			ON peer_connection_sessions(relay_uuid);
CREATE INDEX idx_peer_conn_sess_open
			ON peer_connection_sessions(host_id, peer_id, ended_at);
CREATE TABLE device_telemetry_snapshots (
			device_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			sample_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'ok',
			payload TEXT NOT NULL DEFAULT '{}',
			collected_at TEXT NOT NULL DEFAULT '',
			received_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (device_id, kind)
		);
CREATE INDEX idx_device_telemetry_snapshots_received
			ON device_telemetry_snapshots(received_at);
CREATE TABLE device_telemetry_commands (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id TEXT NOT NULL,
			command TEXT NOT NULL,
			args TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending',
			result TEXT NOT NULL DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL DEFAULT '',
			completed_at TEXT DEFAULT NULL
		);
CREATE INDEX idx_device_telemetry_commands_device_status
			ON device_telemetry_commands(device_id, status, created_at);
CREATE TABLE device_connection_policies (
			device_id TEXT PRIMARY KEY,
			device_uuid TEXT NOT NULL,
			desired_mode TEXT NOT NULL,
			effective_mode TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL,
			active_command_id TEXT NOT NULL DEFAULT '',
			updated_at TEXT DEFAULT (datetime('now'))
		);
CREATE TABLE device_connection_mode_commands (
			command_id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			device_uuid TEXT NOT NULL,
			revision INTEGER NOT NULL,
			mode TEXT NOT NULL,
			reason TEXT NOT NULL,
			operator_id TEXT NOT NULL DEFAULT '',
			operator_name TEXT NOT NULL DEFAULT '',
			previous_mode TEXT NOT NULL DEFAULT '',
			issued_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			status TEXT NOT NULL,
			rejection_code TEXT NOT NULL DEFAULT '',
			acknowledged_at INTEGER,
			delivered_at INTEGER,
			UNIQUE(device_uuid, revision)
		);
CREATE INDEX idx_conn_mode_commands_device
			ON device_connection_mode_commands(device_id, status, revision);
CREATE TABLE chat_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			from_id TEXT NOT NULL,
			from_name TEXT DEFAULT '',
			to_id TEXT DEFAULT '',
			text TEXT NOT NULL,
			read INTEGER DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now'))
		);
CREATE INDEX idx_chat_messages_conv ON chat_messages(conversation_id);
CREATE INDEX idx_chat_messages_from ON chat_messages(from_id);
CREATE INDEX idx_chat_messages_created ON chat_messages(created_at);
CREATE TABLE chat_groups (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			members TEXT DEFAULT '',
			created_by TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now'))
		);
CREATE TABLE help_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id TEXT NOT NULL,
			hostname TEXT DEFAULT '',
			org_id TEXT DEFAULT '',
			message TEXT DEFAULT '',
			status TEXT DEFAULT 'pending',
			handled_by TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
CREATE INDEX idx_help_requests_device ON help_requests(device_id);
CREATE INDEX idx_help_requests_status ON help_requests(status);
CREATE INDEX idx_help_requests_created ON help_requests(created_at);
CREATE INDEX idx_help_requests_org ON help_requests(org_id);
CREATE TABLE client_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT UNIQUE NOT NULL,
			user_id INTEGER NOT NULL,
			client_id TEXT DEFAULT '',
			client_uuid TEXT DEFAULT '',
			expires_at TEXT NOT NULL,
			last_used TEXT DEFAULT (datetime('now')),
			created_at TEXT DEFAULT (datetime('now')),
			revoked INTEGER DEFAULT 0,
			ip_address TEXT DEFAULT '',
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
CREATE UNIQUE INDEX idx_client_sessions_hash ON client_sessions(token_hash);
CREATE INDEX idx_client_sessions_user ON client_sessions(user_id);
CREATE INDEX idx_client_sessions_expires ON client_sessions(expires_at);
CREATE TABLE organizations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT UNIQUE NOT NULL,
			logo_url TEXT DEFAULT '',
			settings TEXT DEFAULT '{}',
			created_at TEXT DEFAULT (datetime('now'))
		);
CREATE UNIQUE INDEX idx_organizations_slug ON organizations(slug);
CREATE TABLE org_users (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL REFERENCES organizations(id),
			username TEXT NOT NULL,
			display_name TEXT DEFAULT '',
			email TEXT DEFAULT '',
			password_hash TEXT NOT NULL,
			role TEXT DEFAULT 'user',
			totp_secret TEXT DEFAULT '',
			avatar_url TEXT DEFAULT '',
			last_login TEXT DEFAULT NULL,
			created_at TEXT DEFAULT (datetime('now')), server_user_id INTEGER DEFAULT 0,
			UNIQUE(org_id, username)
		);
CREATE INDEX idx_org_users_org ON org_users(org_id);
CREATE TABLE org_devices (
			org_id TEXT NOT NULL REFERENCES organizations(id),
			device_id TEXT NOT NULL,
			assigned_user_id TEXT DEFAULT '',
			department TEXT DEFAULT '',
			location TEXT DEFAULT '',
			building TEXT DEFAULT '',
			tags TEXT DEFAULT '',
			PRIMARY KEY(org_id, device_id)
		);
CREATE INDEX idx_org_devices_org ON org_devices(org_id);
CREATE INDEX idx_org_devices_device ON org_devices(device_id);
CREATE TABLE org_invitations (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL REFERENCES organizations(id),
			token TEXT UNIQUE NOT NULL,
			email TEXT DEFAULT '',
			role TEXT DEFAULT 'user',
			expires_at TEXT NOT NULL,
			used_at TEXT DEFAULT NULL
		);
CREATE UNIQUE INDEX idx_org_invitations_token ON org_invitations(token);
CREATE TABLE org_settings (
			org_id TEXT NOT NULL REFERENCES organizations(id),
			key TEXT NOT NULL,
			value TEXT DEFAULT '',
			PRIMARY KEY(org_id, key)
		);
CREATE TABLE org_address_books (
			org_id TEXT NOT NULL REFERENCES organizations(id),
			ab_type TEXT NOT NULL DEFAULT 'legacy',
			data TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT DEFAULT (datetime('now')),
			updated_by TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(org_id, ab_type)
		);
CREATE TABLE org_peer_credentials (
			org_id TEXT NOT NULL REFERENCES organizations(id),
			peer_id TEXT NOT NULL,
			ciphertext TEXT NOT NULL,
			nonce TEXT NOT NULL,
			key_id TEXT NOT NULL DEFAULT 'v1',
			updated_at TEXT DEFAULT (datetime('now')),
			updated_by TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(org_id, peer_id)
		);
CREATE TABLE access_policies (
			peer_id TEXT PRIMARY KEY,
			unattended_enabled INTEGER DEFAULT 0,
			password_hash TEXT DEFAULT '',
			schedule_enabled INTEGER DEFAULT 0,
			schedule_days TEXT DEFAULT '',
			schedule_start_time TEXT DEFAULT '',
			schedule_end_time TEXT DEFAULT '',
			schedule_timezone TEXT DEFAULT '',
			allowed_operators TEXT DEFAULT '',
			updated_at TEXT DEFAULT '',
			updated_by TEXT DEFAULT ''
		);
CREATE TABLE role_permissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			role TEXT NOT NULL,
			permission TEXT NOT NULL,
			granted INTEGER NOT NULL DEFAULT 1,
			UNIQUE(role, permission)
		);
CREATE INDEX idx_role_permissions_role ON role_permissions(role);
CREATE INDEX idx_audit_files_peer ON audit_files(peer_id, created_at);
CREATE TABLE billing_packages (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			included_minutes INTEGER NOT NULL DEFAULT 0,
			overage_rate REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'PLN',
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		, rounding_minutes INTEGER NOT NULL DEFAULT 10);
CREATE TABLE billing_contracts (
			id TEXT PRIMARY KEY,
			target_type TEXT NOT NULL DEFAULT 'org',
			target_key TEXT NOT NULL,
			package_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			remaining_minutes INTEGER NOT NULL DEFAULT 0,
			overage_rate REAL,
			hourly_rate REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'PLN',
			valid_from TEXT,
			valid_until TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')), current_period_ym TEXT NOT NULL DEFAULT '',
			UNIQUE(target_type, target_key),
			FOREIGN KEY (package_id) REFERENCES billing_packages(id)
		);
CREATE INDEX idx_billing_contracts_target ON billing_contracts(target_type, target_key);
CREATE INDEX idx_billing_contracts_status ON billing_contracts(status);
CREATE TABLE billing_sessions (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL,
			contract_id TEXT NOT NULL DEFAULT '',
			operator_id TEXT NOT NULL DEFAULT '',
			operator_name TEXT NOT NULL DEFAULT '',
			device_id TEXT NOT NULL,
			device_name TEXT NOT NULL DEFAULT '',
			relay_uuid TEXT NOT NULL DEFAULT '',
			transport TEXT NOT NULL DEFAULT 'rustdesk',
			status TEXT NOT NULL DEFAULT 'active',
			billing_phase TEXT NOT NULL DEFAULT 'included',
			started_at TEXT NOT NULL,
			ended_at TEXT,
			raw_seconds INTEGER NOT NULL DEFAULT 0,
			billed_minutes INTEGER NOT NULL DEFAULT 0,
			included_minutes_used INTEGER NOT NULL DEFAULT 0,
			overage_minutes INTEGER NOT NULL DEFAULT 0,
			amount_included REAL NOT NULL DEFAULT 0,
			amount_overage REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'PLN',
			clock_offset_ms_at_start INTEGER NOT NULL DEFAULT 0,
			clock_synced_at_start INTEGER NOT NULL DEFAULT 1,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
CREATE INDEX idx_billing_sessions_org ON billing_sessions(org_id, started_at);
CREATE INDEX idx_billing_sessions_relay ON billing_sessions(relay_uuid);
CREATE TABLE billing_session_ledger (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			details TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now'))
		);
CREATE INDEX idx_billing_ledger_session ON billing_session_ledger(session_id);
CREATE TABLE billing_work_reports (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL UNIQUE,
			operator_id TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			category TEXT DEFAULT '',
			ticket_ref TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now'))
		);
CREATE TABLE billing_currencies (
			code TEXT PRIMARY KEY,
			symbol TEXT NOT NULL DEFAULT '',
			exchange_rate_to_base REAL NOT NULL DEFAULT 1
		);
CREATE TABLE billing_period_totals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			contract_id TEXT NOT NULL,
			package_id TEXT NOT NULL DEFAULT '',
			period_ym TEXT NOT NULL,
			timezone TEXT NOT NULL DEFAULT 'UTC',
			included_minutes_used INTEGER NOT NULL DEFAULT 0,
			overage_minutes INTEGER NOT NULL DEFAULT 0,
			amount_overage REAL NOT NULL DEFAULT 0,
			included_limit_snapshot INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT DEFAULT (datetime('now')),
			UNIQUE(contract_id, period_ym)
		);
CREATE INDEX idx_billing_period_totals_pkg ON billing_period_totals(package_id, period_ym);
CREATE INDEX idx_device_group_members_peer ON device_group_members (peer_id);
CREATE INDEX idx_peers_banned ON peers(banned);
CREATE INDEX idx_peers_soft_deleted ON peers(soft_deleted);
CREATE INDEX idx_peers_linked_peer ON peers(linked_peer_id);
CREATE INDEX idx_peer_metrics_peer_created ON peer_metrics(peer_id, created_at DESC);
CREATE TRIGGER users_credentials_changed
	AFTER UPDATE OF password_hash, totp_secret, totp_enabled ON users
	FOR EACH ROW
	WHEN OLD.password_hash IS NOT NEW.password_hash
		OR OLD.totp_enabled IS NOT NEW.totp_enabled
		OR (OLD.totp_secret IS NOT NEW.totp_secret AND COALESCE(NEW.totp_enabled, 0) NOT IN (0, '0', 'false', ''))
	BEGIN
		UPDATE users SET credentials_changed_at = CAST(strftime('%s', 'now') AS INTEGER) WHERE id = NEW.id;
	END;
