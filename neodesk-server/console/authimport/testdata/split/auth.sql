PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
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
INSERT INTO tickets VALUES(1,'Printer','','open','medium','general',NULL,'alice','bob',NULL,NULL,NULL,'2026-11-18 14:37:15','2026-11-18 14:37:15');
INSERT INTO tickets VALUES(2,'VPN','','open','medium','general',NULL,'bob',NULL,NULL,NULL,NULL,'2026-11-18 14:37:15','2026-11-18 14:37:15');
CREATE TABLE ticket_comments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                ticket_id INTEGER NOT NULL,
                author TEXT NOT NULL,
                body TEXT NOT NULL,
                is_internal INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now')),
                FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
            );
INSERT INTO ticket_comments VALUES(1,1,'bob','on it',0,'2026-11-18 14:37:15');
INSERT INTO ticket_comments VALUES(2,2,'alice','done',0,'2026-11-18 14:37:15');
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
INSERT INTO ticket_attachments VALUES(1,1,'log.txt','application/octet-stream',0,'/data/attachments/1_x_log.txt','bob','2026-11-18 14:37:15');
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
INSERT INTO alert_rules VALUES(1,'offline','',1,'offline','gt',0.0,'warning',NULL,300,'',NULL,'2026-11-18 14:37:15','2026-11-18 14:37:15');
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
INSERT INTO alert_history VALUES(1,1,'PEER1','warning','gone','2026-11-01 00:00:00',0,NULL,NULL,'2026-11-18 14:37:15');
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
INSERT INTO remote_commands VALUES(1,'PEER1','shell','','pending',NULL,'alice','2026-11-18 14:37:15',NULL,NULL);
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
INSERT INTO users VALUES(1,'admin','$2b$10$adminhashfromtheconsole.........................','admin','local','2024-01-01 00:00:00','2026-11-01 10:00:00',NULL,NULL,0,NULL,'','','','','','','guid-admin-console');
INSERT INTO users VALUES(2,'alice','$2b$10$alicehash.......................................','operator','local','2024-02-01 00:00:00','2026-11-02 10:00:00','de','JBSWY3DPEHPK3PXP',1,'["AAAA-1111","BBBB-2222"]','Alice','Liddell','alice@example.com','+49 1','Ops lead','https://img.example/a.png','guid-alice-console');
INSERT INTO users VALUES(3,'bob','$2b$10$bobhash.........................................','viewer','local','2024-03-01 00:00:00',NULL,'en',NULL,0,NULL,'Bob','','bob@example.com','','','','');
INSERT INTO users VALUES(5,'Dave','$2b$10$davehash........................................','admin','local','2024-04-01 00:00:00','2026-10-01 00:00:00',NULL,NULL,0,NULL,'Dave','','','','','','guid-dave');
INSERT INTO users VALUES(6,'eve','$2b$10$evehash.........................................','viewer','local','2024-05-01 00:00:00',NULL,'pl',NULL,0,NULL,'Eve','','eve@example.com','','','','');
CREATE TABLE audit_log (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER,
                action TEXT NOT NULL,
                details TEXT,
                ip_address TEXT,
                created_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO audit_log VALUES(1,2,'login','ok','10.0.0.2','2026-11-01 00:00:00');
INSERT INTO audit_log VALUES(2,4,'login','gone','10.0.0.4','2026-11-01 00:00:01');
INSERT INTO audit_log VALUES(3,NULL,'boot','','','2026-11-01 00:00:02');
INSERT INTO audit_log VALUES(4,6,'logout','','','2026-11-01 00:00:03');
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
INSERT INTO access_tokens VALUES(1,'tok-alice','h-alice',2,'c1','','2026-11-18 14:37:15','2030-01-01 00:00:00',NULL,'',0);
CREATE TABLE login_attempts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT NOT NULL,
                ip_address TEXT DEFAULT '',
                success INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO login_attempts VALUES(1,'alice','10.0.0.2',1,'2026-11-18 14:37:15');
CREATE TABLE account_lockouts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                locked_until TEXT NOT NULL,
                attempt_count INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO account_lockouts VALUES(1,'bob','2030-01-01 00:00:00',5,'2026-11-18 14:37:15');
CREATE TABLE folders (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                color TEXT DEFAULT '#6366f1',
                icon TEXT DEFAULT 'folder',
                sort_order INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO folders VALUES(1,'Office','#123456','folder',0,'2026-11-18 14:37:15');
INSERT INTO folders VALUES(2,'Lab','#654321','folder',0,'2026-11-18 14:37:15');
CREATE TABLE address_books (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                ab_type TEXT DEFAULT 'legacy',
                data TEXT DEFAULT '{}',
                updated_at TEXT DEFAULT (datetime('now')),
                UNIQUE(user_id, ab_type),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
INSERT INTO address_books VALUES(1,2,'legacy','{"peers":[{"id":"PEER1"}]}','2026-11-18 14:37:15');
INSERT INTO address_books VALUES(2,3,'personal','{"peers":[]}','2026-11-18 14:37:15');
CREATE TABLE settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO settings VALUES('desktop_layout_2','{"icons":[1]}','2026-11-18 14:37:15');
INSERT INTO settings VALUES('desktop_wallpaper_4','w.png','2026-11-18 14:37:15');
INSERT INTO settings VALUES('desktop_layout_default','keep','2026-11-18 14:37:15');
INSERT INTO settings VALUES('smtp_host','mail.example','2026-11-18 14:37:15');
CREATE TABLE branding_config (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO branding_config VALUES('appName','ACME Desk','2026-11-18 14:37:15');
CREATE TABLE branding_profiles (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL UNIQUE,
                description TEXT DEFAULT '',
                data TEXT NOT NULL,
                is_active INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now')),
                updated_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO branding_profiles VALUES(1,'acme','','{}',1,'2026-11-18 14:37:15','2026-11-18 14:37:15');
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
INSERT INTO device_folder_assignments VALUES('PEER1',1,'2026-11-18 14:37:15');
INSERT INTO device_folder_assignments VALUES('PEER2',2,'2026-11-18 14:37:15');
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
INSERT INTO peer_sysinfo VALUES('PEER1','pc-1','','','','',0,0.0,0.0,'','[]','[]','{}','{}','{}','2026-11-18 14:37:15');
CREATE TABLE peer_metrics (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                peer_id TEXT NOT NULL,
                cpu_usage REAL DEFAULT 0,
                memory_usage REAL DEFAULT 0,
                disk_usage REAL DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO peer_metrics VALUES(1,'PEER1',10.0,0.0,0.0,'2026-11-01 00:00:00');
INSERT INTO peer_metrics VALUES(2,'PEER1',20.0,0.0,0.0,'2026-11-01 00:01:00');
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
INSERT INTO audit_connections VALUES(1,'PEER1','','','','new',0,'','','2026-11-01 00:00:00');
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
INSERT INTO audit_files VALUES(1,'PEER1','','',0,'/tmp/x',1,0,'[]','','','2026-11-01 00:00:00');
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
INSERT INTO audit_alarms VALUES(1,2,'','PEER1','','','{}','2026-11-01 00:00:00');
CREATE TABLE user_groups (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            );
INSERT INTO user_groups VALUES(1,'ug-default','Default','Default user group','','2026-11-18 14:37:15');
INSERT INTO user_groups VALUES(2,'ug-ops','ops','operators','team-1','2026-11-18 14:37:15');
CREATE TABLE panel_access_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                resource_key TEXT NOT NULL,
                access_level TEXT NOT NULL DEFAULT 'none',
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, resource_key)
            );
INSERT INTO panel_access_policies VALUES('user','2','devices','read','2026-11-18 14:37:15');
INSERT INTO panel_access_policies VALUES('user','4','devices','none','2026-11-18 14:37:15');
INSERT INTO panel_access_policies VALUES('group','ug-ops','devices','write','2026-11-18 14:37:15');
INSERT INTO panel_access_policies VALUES('user','x2','tickets','read','2026-11-18 14:37:15');
CREATE TABLE client_feature_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                feature_key TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 0,
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, feature_key)
            );
INSERT INTO client_feature_policies VALUES('user','3','file_transfer',1,'2026-11-18 14:37:15');
INSERT INTO client_feature_policies VALUES('user','6','chat',0,'2026-11-18 14:37:15');
CREATE TABLE user_group_members (
                user_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_group_id, user_id),
                FOREIGN KEY (user_group_id) REFERENCES user_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
INSERT INTO user_group_members VALUES(2,2,'2026-11-18 14:37:15');
INSERT INTO user_group_members VALUES(2,3,'2026-11-18 14:37:15');
INSERT INTO user_group_members VALUES(1,5,'2026-11-18 14:37:15');
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
INSERT INTO device_groups VALUES(1,'dg-default','Default','Default device group','','manual','','2026-11-18 14:37:15');
INSERT INTO device_groups VALUES(2,'dg-servers','servers','','','tag','srv','2026-11-18 14:37:15');
CREATE TABLE device_group_members (
                device_group_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, peer_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE
            );
INSERT INTO device_group_members VALUES(2,'PEER1','2026-11-18 14:37:15');
INSERT INTO device_group_members VALUES(1,'PEER2','2026-11-18 14:37:15');
CREATE TABLE device_group_user_access (
                device_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
INSERT INTO device_group_user_access VALUES(2,2,'2026-11-18 14:37:15');
INSERT INTO device_group_user_access VALUES(2,6,'2026-11-18 14:37:15');
CREATE TABLE device_group_user_group_access (
                device_group_id INTEGER NOT NULL,
                user_group_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_group_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_group_id) REFERENCES user_groups(id) ON DELETE CASCADE
            );
INSERT INTO device_group_user_group_access VALUES(2,2,'2026-11-18 14:37:15');
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
INSERT INTO strategies VALUES(1,'strat-1','Strict','ug-ops','dg-servers',1,'{"x":1}','2026-11-18 14:37:15','2026-11-18 14:37:15');
CREATE TABLE strategy_assignments (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                target_type TEXT NOT NULL,
                target_key TEXT NOT NULL,
                strategy_guid TEXT NOT NULL DEFAULT '',
                updated_at TEXT DEFAULT (datetime('now')),
                UNIQUE(target_type, target_key)
            );
INSERT INTO strategy_assignments VALUES(1,'user','guid-alice-console','strat-1','2026-11-18 14:37:15');
INSERT INTO strategy_assignments VALUES(2,'user','guid-dave','strat-1','2026-11-18 14:37:15');
INSERT INTO strategy_assignments VALUES(3,'device','PEER1','strat-1','2026-11-18 14:37:15');
CREATE TABLE user_peer_grants (
                user_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                granted_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, peer_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
INSERT INTO user_peer_grants VALUES(5,'PEER1','2026-11-18 14:37:15');
INSERT INTO user_peer_grants VALUES(6,'PEER2','2026-11-18 14:37:15');
CREATE TABLE notification_reads (
                user_id INTEGER NOT NULL,
                notification_id TEXT NOT NULL,
                read_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, notification_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            );
INSERT INTO notification_reads VALUES(3,'n-1','2026-11-18 14:37:15');
INSERT INTO notification_reads VALUES(2,'n-2','2026-11-18 14:37:15');
CREATE TABLE email_notification_log (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                event_type TEXT NOT NULL,
                event_id TEXT NOT NULL,
                recipient TEXT NOT NULL,
                sent_at TEXT DEFAULT (datetime('now')),
                UNIQUE(event_type, event_id, recipient)
            );
INSERT INTO email_notification_log VALUES(1,'help','h1','alice@example.com','2026-11-18 14:37:15');
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
INSERT INTO user_avatars VALUES(2,'image/png',X'89504e47','v1','oidc','','2026-11-18 14:37:15');
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('user_groups',2);
INSERT INTO sqlite_sequence VALUES('device_groups',2);
INSERT INTO sqlite_sequence VALUES('users',6);
INSERT INTO sqlite_sequence VALUES('folders',2);
INSERT INTO sqlite_sequence VALUES('strategies',1);
INSERT INTO sqlite_sequence VALUES('strategy_assignments',3);
INSERT INTO sqlite_sequence VALUES('address_books',2);
INSERT INTO sqlite_sequence VALUES('access_tokens',1);
INSERT INTO sqlite_sequence VALUES('tickets',2);
INSERT INTO sqlite_sequence VALUES('ticket_comments',2);
INSERT INTO sqlite_sequence VALUES('ticket_attachments',1);
INSERT INTO sqlite_sequence VALUES('alert_rules',1);
INSERT INTO sqlite_sequence VALUES('alert_history',1);
INSERT INTO sqlite_sequence VALUES('remote_commands',1);
INSERT INTO sqlite_sequence VALUES('audit_log',4);
INSERT INTO sqlite_sequence VALUES('login_attempts',1);
INSERT INTO sqlite_sequence VALUES('account_lockouts',1);
INSERT INTO sqlite_sequence VALUES('branding_profiles',1);
INSERT INTO sqlite_sequence VALUES('email_notification_log',1);
INSERT INTO sqlite_sequence VALUES('peer_metrics',2);
INSERT INTO sqlite_sequence VALUES('audit_connections',1);
INSERT INTO sqlite_sequence VALUES('audit_files',1);
INSERT INTO sqlite_sequence VALUES('audit_alarms',1);
CREATE INDEX idx_alert_history_rule ON alert_history (rule_id);
CREATE INDEX idx_alert_history_device ON alert_history (device_id);
CREATE INDEX idx_remote_commands_device ON remote_commands (device_id, status);
CREATE INDEX idx_audit_log_created ON audit_log (created_at);
CREATE INDEX idx_audit_log_action ON audit_log (action, created_at);
CREATE INDEX idx_peer_metrics_peer_time ON peer_metrics (peer_id, created_at);
CREATE INDEX idx_audit_conn_host ON audit_connections (host_id, created_at);
CREATE INDEX idx_audit_conn_peer ON audit_connections (peer_id, created_at);
CREATE INDEX idx_audit_files_host ON audit_files (host_id, created_at);
CREATE INDEX idx_audit_alarms_type ON audit_alarms (alarm_type, created_at);
CREATE INDEX idx_panel_access_policies_subject
                ON panel_access_policies (subject_type, subject_id);
CREATE INDEX idx_client_feature_policies_subject
                ON client_feature_policies (subject_type, subject_id);
CREATE INDEX idx_strategy_assignments_strategy ON strategy_assignments (strategy_guid);
CREATE INDEX idx_user_peer_grants_user ON user_peer_grants (user_id);
CREATE INDEX idx_notification_reads_user ON notification_reads (user_id, read_at);
CREATE INDEX idx_email_notification_log_event ON email_notification_log (event_type, event_id);
CREATE UNIQUE INDEX idx_access_tokens_hash ON access_tokens (token_hash)
            WHERE token_hash IS NOT NULL;
COMMIT;
