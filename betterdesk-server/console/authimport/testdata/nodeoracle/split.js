'use strict';

/**
 * Builds an install in the old split layout with the real Node console:
 * auth.db and db_v2.sqlite3 created by its database adapter, then filled
 * with accounts (one deleted), groups, folders, strategies, address books,
 * panel access rows, settings, tickets, alerts, the audit log, tokens,
 * metrics and client audit rows, and in the main file devices, tenants,
 * agent bundles and signed-in sessions. Writes the two files into the
 * folder given, and their SQL text into ../split/ for the Go tests.
 *
 *   node split.js <folder>
 */

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const dir = path.resolve(process.argv[2] || fs.mkdtempSync('/tmp/split-'));
fs.mkdirSync(dir, { recursive: true });
const mainPath = path.join(dir, 'db_v2.sqlite3');
const authPath = path.join(dir, 'auth.db');
for (const f of [mainPath, authPath]) for (const s of ['', '-wal', '-shm']) fs.rmSync(f + s, { force: true });
fs.writeFileSync(authPath, '');

process.env.NODE_ENV = 'test';
process.env.DB_TYPE = 'sqlite';
process.env.DATA_DIR = dir;
process.env.DB_PATH = mainPath;
process.env.AUTH_DB_PATH = authPath;
process.chdir(WEB);
const Database = require(path.join(WEB, 'node_modules', 'better-sqlite3'));
const adapter = require(path.join(WEB, 'services', 'dbAdapter')).getAdapter();

(async () => {
    console.warn = () => {};
    await adapter.init();
    // Touch a method that uses the auth handle so the adapter opens it
    await adapter.getAllUsers();
    // The session table, as the console's session store makes it
    const config = require(path.join(WEB, 'config', 'config'));
    const { DatabaseSessionStore } = require(path.join(WEB, 'services', 'databaseSessionStore'));
    const store = new DatabaseSessionStore({ config, ttlMs: 86400000, idleTimeoutMs: 1800000, maxAgeMs: 86400000 });
    await store.ready;
    const auth = new Database(authPath);
    const main = new Database(mainPath);
    auth.pragma('foreign_keys = ON');
    const a = (sql, ...args) => auth.prepare(sql).run(...args);
    const m = (sql, ...args) => main.prepare(sql).run(...args);

    // Accounts: 4 is deleted, so rows that still name it point at nobody
    const users = [
        [1, 'admin', '$2b$10$adminhashfromtheconsole.........................', 'admin', '2024-01-01 00:00:00', '2026-11-01 10:00:00', null, null, 0, null, '', '', '', '', '', '', 'guid-admin-console'],
        [2, 'alice', '$2b$10$alicehash.......................................', 'operator', '2024-02-01 00:00:00', '2026-11-02 10:00:00', 'de', 'JBSWY3DPEHPK3PXP', 1, '["AAAA-1111","BBBB-2222"]', 'Alice', 'Liddell', 'alice@example.com', '+49 1', 'Ops lead', 'https://img.example/a.png', 'guid-alice-console'],
        [3, 'bob', '$2b$10$bobhash.........................................', 'viewer', '2024-03-01 00:00:00', null, 'en', null, 0, null, 'Bob', '', 'bob@example.com', '', '', '', ''],
        [4, 'carol', '$2b$10$carol...........................................', 'viewer', '2024-03-02 00:00:00', null, null, null, 0, null, '', '', '', '', '', '', ''],
        [5, 'Dave', '$2b$10$davehash........................................', 'admin', '2024-04-01 00:00:00', '2026-10-01 00:00:00', null, null, 0, null, 'Dave', '', '', '', '', '', 'guid-dave'],
        [6, 'eve', '$2b$10$evehash.........................................', 'viewer', '2024-05-01 00:00:00', null, 'pl', null, 0, null, 'Eve', '', 'eve@example.com', '', '', '', ''],
    ];
    for (const u of users) {
        a(`INSERT INTO users (id, username, password_hash, role, created_at, last_login, preferred_language, totp_secret, totp_enabled, totp_recovery_codes,
            first_name, last_name, email, phone, role_display, avatar_url, guid) VALUES (${'?, '.repeat(16)}?)`, ...u);
    }

    // Groups: the console's own Default groups and one more of each
    a("DELETE FROM user_groups"); a("DELETE FROM device_groups");
    a("INSERT INTO user_groups (id, guid, name, note) VALUES (1, 'ug-default', 'Default', 'Default user group')");
    a("INSERT INTO user_groups (id, guid, name, note, team_id) VALUES (2, 'ug-ops', 'ops', 'operators', 'team-1')");
    a("INSERT INTO user_group_members (user_group_id, user_id) VALUES (2, 2), (2, 3), (1, 5)");
    a("INSERT INTO device_groups (id, guid, name, note) VALUES (1, 'dg-default', 'Default', 'Default device group')");
    a("INSERT INTO device_groups (id, guid, name, source_type, tag_filter) VALUES (2, 'dg-servers', 'servers', 'tag', 'srv')");
    a("INSERT INTO device_group_members (device_group_id, peer_id) VALUES (2, 'PEER1'), (1, 'PEER2')");
    a("INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (2, 2), (2, 6)");
    a("INSERT INTO device_group_user_group_access (device_group_id, user_group_id) VALUES (2, 2)");
    a("INSERT INTO folders (id, name, color) VALUES (1, 'Office', '#123456'), (2, 'Lab', '#654321')");
    a("INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('PEER1', 1), ('PEER2', 2)");
    a("INSERT INTO strategies (id, guid, name, user_group_guid, device_group_guid, permissions) VALUES (1, 'strat-1', 'Strict', 'ug-ops', 'dg-servers', '{\"x\":1}')");
    a("INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('user', 'guid-alice-console', 'strat-1'), ('user', 'guid-dave', 'strat-1'), ('device', 'PEER1', 'strat-1')");
    a("INSERT INTO address_books (user_id, ab_type, data) VALUES (2, 'legacy', '{\"peers\":[{\"id\":\"PEER1\"}]}'), (3, 'personal', '{\"peers\":[]}')");
    a("INSERT INTO user_peer_grants (user_id, peer_id) VALUES (5, 'PEER1'), (6, 'PEER2')");
    a("INSERT INTO notification_reads (user_id, notification_id) VALUES (3, 'n-1'), (2, 'n-2')");
    a("INSERT INTO user_avatars (user_id, mime_type, data, version, source) VALUES (2, 'image/png', X'89504E47', 'v1', 'oidc')");
    a("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES ('tok-alice', 'h-alice', 2, 'c1', '2030-01-01 00:00:00')");

    // Panel access and client features: a user row, a deleted user's row, a group row, an odd id
    a("INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('user', '2', 'devices', 'read'), ('user', '4', 'devices', 'none'), ('group', 'ug-ops', 'devices', 'write'), ('user', 'x2', 'tickets', 'read')");
    a("INSERT INTO client_feature_policies (subject_type, subject_id, feature_key, enabled) VALUES ('user', '3', 'file_transfer', 1), ('user', '6', 'chat', 0)");
    a("INSERT INTO settings (key, value) VALUES ('desktop_layout_2', '{\"icons\":[1]}'), ('desktop_wallpaper_4', 'w.png'), ('desktop_layout_default', 'keep'), ('smtp_host', 'mail.example')");

    // Tickets, alerts, commands, the audit log
    a("INSERT INTO tickets (id, title, created_by, assigned_to) VALUES (1, 'Printer', 'alice', 'bob'), (2, 'VPN', 'bob', NULL)");
    a("INSERT INTO ticket_comments (ticket_id, author, body) VALUES (1, 'bob', 'on it'), (2, 'alice', 'done')");
    a("INSERT INTO ticket_attachments (ticket_id, filename, storage_path, uploaded_by) VALUES (1, 'log.txt', '/data/attachments/1_x_log.txt', 'bob')");
    a("INSERT INTO alert_rules (id, name, condition_type) VALUES (1, 'offline', 'offline')");
    a("INSERT INTO alert_history (rule_id, device_id, message, triggered_at) VALUES (1, 'PEER1', 'gone', '2026-11-01 00:00:00')");
    a("INSERT INTO remote_commands (device_id, created_by) VALUES ('PEER1', 'alice')");
    a("INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (2, 'login', 'ok', '10.0.0.2', '2026-11-01 00:00:00'), (4, 'login', 'gone', '10.0.0.4', '2026-11-01 00:00:01'), (NULL, 'boot', '', '', '2026-11-01 00:00:02'), (6, 'logout', '', '', '2026-11-01 00:00:03')");
    a("INSERT INTO login_attempts (username, ip_address, success) VALUES ('alice', '10.0.0.2', 1)");
    a("INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('bob', '2030-01-01 00:00:00', 5)");
    a("INSERT INTO branding_config (key, value) VALUES ('appName', 'ACME Desk')");
    a("INSERT INTO branding_profiles (name, data, is_active) VALUES ('acme', '{}', 1)");
    a("INSERT INTO email_notification_log (event_type, event_id, recipient) VALUES ('help', 'h1', 'alice@example.com')");
    a("INSERT INTO peer_sysinfo (peer_id, hostname) VALUES ('PEER1', 'pc-1')");
    a("INSERT INTO peer_metrics (peer_id, cpu_usage, created_at) VALUES ('PEER1', 10, '2026-11-01 00:00:00'), ('PEER1', 20, '2026-11-01 00:01:00')");
    a("INSERT INTO audit_connections (host_id, action, created_at) VALUES ('PEER1', 'new', '2026-11-01 00:00:00')");
    a("INSERT INTO audit_files (host_id, path, created_at) VALUES ('PEER1', '/tmp/x', '2026-11-01 00:00:00')");
    a("INSERT INTO audit_alarms (alarm_type, host_id, created_at) VALUES (2, 'PEER1', '2026-11-01 00:00:00')");

    // carol is deleted: her rows with a foreign key go with her, the
    // others stay and point at nobody
    a("DELETE FROM users WHERE id = 4");

    // The main file: devices, tenants, bundles, sessions
    m("INSERT INTO peer (id, uuid, info) VALUES ('PEER1', 'u1', '{}'), ('PEER2', 'u2', '{}')");
    m("INSERT INTO tenants (id, name, slug) VALUES (1, 'Tenant A', 'tenant-a')");
    m("INSERT INTO tenant_users (tenant_id, user_id) VALUES (1, 2), (1, 4), (1, 3)");
    m("INSERT INTO agent_bundles (id, bundle_id, name, slug, created_by) VALUES (1, 'b-1', 'Bundle', 'bundle', 2), (2, 'b-2', 'Old', 'old', 4)");
    const now = Date.now();
    const session = (sid, userId) => m(`INSERT INTO panel_sessions (sid, session_json, expires_at, user_id, username, created_at, last_activity_at, is_active)
        VALUES (?, ?, ?, ?, ?, ?, ?, 1)`, sid, JSON.stringify({ cookie: {}, userId, username: 'x' }), now + 86400000, userId, 'x', now, now);
    session('sid-admin', 1);
    session('sid-alice', 2);
    session('sid-carol', 4);
    m(`INSERT INTO panel_sessions (sid, session_json, expires_at, is_active) VALUES ('sid-anon', '{}', ${now + 86400000}, 1)`);

    auth.close();
    main.close();
    await adapter.close();

    // SQL text of both files for the Go tests
    const out = path.join(__dirname, '..', 'split');
    fs.mkdirSync(out, { recursive: true });
    for (const [file, name] of [[authPath, 'auth.sql'], [mainPath, 'main.sql']]) {
        const sql = execFileSync('sqlite3', [file, '.dump'], { encoding: 'utf8' });
        fs.writeFileSync(path.join(out, name), sql);
    }
    console.log(`${dir}: split install written; ${out}/auth.sql, main.sql`);
})().catch((err) => { console.error(err); process.exit(1); });
