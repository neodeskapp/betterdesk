'use strict';

/**
 * Records what routes/users.routes.js answers, with the real database
 * adapter, the real permission checks, panel access guard, translations,
 * rate limiter and API client, against a made-up API server. See README.md.
 *
 *   node users.js                                   ../node_sqlite.json
 *   ORACLE_SHAPE=go node users.js                   ../node_sqlite-go.json
 *   ORACLE_PG_DSN=postgres://... node users.js      ../node_postgres.json
 *   ORACLE_PG_DSN=... ORACLE_SHAPE=go node users.js ../node_postgres-go.json
 *
 * ORACLE_SHAPE=go starts from the schema the signal server and the console
 * leave when the signal server starts first (store/testdata/dumps); without
 * it the Node console creates its tables on an empty database.
 */

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const DUMPS = process.env.ORACLE_DUMPS || '/repo/betterdesk-server/console/store/testdata/dumps';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const SHAPE = process.env.ORACLE_SHAPE === 'go' ? 'go' : 'node';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const variant = dialect + (SHAPE === 'go' ? '-go' : '');
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'users-oracle-'));
const schema = `oracle_users_${process.pid}_${Date.now() % 1000000}`;

const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

/** The accounts a step can act as, by role, with the ids routetest gives them. */
const USERS = {
    admin: { id: 1, username: 'e2e_admin', role: 'admin' },
    super_admin: { id: 2, username: 'e2e_super_admin', role: 'super_admin' },
    server_admin: { id: 3, username: 'e2e_server_admin', role: 'server_admin' },
    global_admin: { id: 4, username: 'e2e_global_admin', role: 'global_admin' },
    operator: { id: 5, username: 'e2e_operator', role: 'operator' },
    viewer: { id: 6, username: 'e2e_viewer', role: 'viewer' },
    guest_issuer: { id: 7, username: 'e2e_guest_issuer', role: 'guest_issuer' },
    pro: { id: 8, username: 'e2e_pro', role: 'pro' },
};
const ROLES = ['', 'pro', 'guest_issuer', 'viewer', 'operator', 'global_admin', 'server_admin', 'super_admin', 'admin'];

// ---- The made-up API server ----

const json = (status, value) => ({ status, type: 'application/json', body: JSON.stringify(value) });
const text = (status, body) => ({ status, type: 'text/plain; charset=utf-8', body });
const API = {
    'GET /api/peers': json(200, [
        { id: 'D1', hostname: 'pc-1', tags: ['lab'], online: true },
        { id: 'D2', hostname: 'pc-2', tags: [] },
        { id: 'D3', hostname: 'pc-3', tags: 'lab,office' },
        { id: 'D4', hostname: 'pc-4' },
    ]),
    'GET /api/remote-targets': json(200, []),
    'GET /api/users/13/organizations': json(200, [{ org_id: 'org-1', name: 'Acme', role: 'member' }]),
    'GET /api/users/14/organizations': json(404, { error: 'user not found' }),
    'GET /api/users/16/organizations': text(500, 'boom\n'),
    'GET /api/users/17/organizations': { status: 200, type: 'application/json', body: '' },
    'POST /api/users/13/organizations': json(201, { success: true, org_id: 'org-1' }),
    'POST /api/users/14/organizations': json(409, { error: 'already a member' }),
    'POST /api/users/17/organizations': { status: 400, type: 'application/json', body: '' },
    'POST /api/strategies/assign': json(200, { success: true }),
};
const NOT_FOUND = json(404, { error: 'not found' });

// Every permission of the built-in role map (middleware/auth.js). The panel
// access policy asks the API server for a role's permissions; the made-up
// server answers with the built-in map, which is what the Go console uses
// without overrides.
const PERMISSIONS = [
    'server.config', 'server.keys', 'server.attestation', 'blocklist.edit', 'user.view', 'user.create', 'user.edit',
    'user.delete', 'org.create', 'org.edit', 'org.delete', 'org.manage_users', 'org.manage_devices', 'device.view',
    'device.connect', 'device.edit', 'device.delete', 'remote_target.view', 'remote_target.connect', 'remote_target.edit',
    'remote_target.delete', 'remote_target.test', 'guest.create', 'device.ban', 'device.change_id',
    'device.connection_mode', 'audit.view', 'metrics.view', 'cdap.view', 'cdap.command', 'cdap.terminal', 'cdap.files',
    'mesh.terminal', 'mesh.files', 'mesh.power', 'chat.access', 'enrollment.manage', 'enrollment.approve',
    'branding.edit', 'billing.view', 'billing.manage', 'billing.reports', 'billing.export',
];
const rolePermissions = /^\/api\/roles\/([^/]+)\/permissions$/;

const calls = [];
function startApi() {
    const server = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            const body = Buffer.concat(chunks).toString('utf8');
            const roleMatch = req.method === 'GET' && rolePermissions.exec(req.url);
            if (roleMatch) {
                // Loaded here: its modules read the API address when they load
                const { roleHasPermission } = web('middleware/auth');
                const role = decodeURIComponent(roleMatch[1]);
                res.writeHead(200, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({ role, permissions: PERMISSIONS.filter((p) => roleHasPermission(role, p)) }));
                return;
            }
            calls.push({ method: req.method, url: req.url, body });
            const reply = API[`${req.method} ${req.url.split('?')[0]}`] || NOT_FOUND;
            res.writeHead(reply.status, { 'Content-Type': reply.type });
            res.end(reply.body);
        });
    });
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

// ---- The database ----

function dumpScript(file) {
    const out = [];
    for (const line of fs.readFileSync(path.join(DUMPS, file), 'utf8').replace(/\r\n/g, '\n').split('\n')) {
        if (line.startsWith('--') || line.startsWith('\\')) continue;
        if (line.startsWith('SET ') || line.startsWith('SELECT pg_catalog.set_config(')) continue;
        if (line.startsWith('CREATE TABLE sqlite_sequence(')) continue;
        out.push(pg ? line.split('public.').join('') : line);
    }
    return out.join('\n');
}

async function openDatabase() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
    let admin = null;
    let url = null;
    if (pg) {
        // A database of its own: the Node console looks for existing
        // columns in every schema of the database, so a schema next to
        // other tests' tables would not get its columns.
        const { Pool } = web('node_modules/pg');
        admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE DATABASE ${schema}`);
        url = new URL(PG_DSN);
        url.pathname = `/${schema}`;
        process.env.DB_TYPE = 'postgres';
        process.env.DATABASE_URL = url.toString();
        if (SHAPE === 'go') {
            const setup = new Pool({ connectionString: url.toString(), max: 1 });
            await setup.query(dumpScript('shared-postgres-go-first.sql'));
            await setup.end();
        }
    } else {
        process.env.DB_TYPE = 'sqlite';
        delete process.env.DATABASE_URL;
        if (SHAPE === 'go') {
            const Database = web('node_modules/better-sqlite3');
            const file = new Database(process.env.DB_PATH);
            file.exec(`BEGIN;\n${dumpScript('shared-sqlite-go-first.sql')}\nCOMMIT;`);
            file.close();
        }
    }
    process.chdir(WEB);
    const adapter = web('services/dbAdapter').getAdapter();
    await adapter.init();

    let exec;
    let pool = null;
    if (pg) {
        const { Pool } = web('node_modules/pg');
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
        exec = async (sql, params = []) => (await pool.query(rebind(sql), params)).rows;
    } else {
        const raw = adapter.getSqliteMainDb();
        exec = async (sql, params = []) => {
            const stmt = raw.prepare(sql);
            if (stmt.reader) return stmt.all(...params);
            stmt.run(...params);
            return [];
        };
    }
    const close = async () => {
        try { await adapter.close(); } catch (_) { /* closed */ }
        if (pool) await pool.end();
        if (admin) {
            await admin.query(`DROP DATABASE ${schema} WITH (FORCE)`);
            await admin.end();
        }
    };
    return { exec, close };
}

// ---- The rows every run starts from ----

const on = pg ? true : 1;
const off = pg ? false : 0;
const seed = [
    ['DELETE FROM users', []],
];
for (const u of Object.values(USERS)) {
    seed.push(['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [u.id, u.username, 'x', u.role]]);
}
const more = [
    [9, 'ldap_op', 'operator', 'ldap', ''],
    [10, 'oidc_viewer', 'viewer', 'oidc', 'oidc@example.invalid'],
    [11, 'target_admin', 'admin', 'local', ''],
    [12, 'target_super', 'super_admin', 'local', ''],
    [13, 'target_op', 'operator', 'local', 'op@example.invalid'],
    [14, 'target_gi', 'guest_issuer', 'local', ''],
    [15, 'mixed_ldap', 'viewer', ' LDAP ', ''],
    [16, 'target_glob', 'global_admin', 'local', ''],
    [17, 'target_srv', 'server_admin', 'local', ''],
    [18, 'target_view', 'viewer', '', ''],
];
for (const [id, username, role, provider, email] of more) {
    seed.push(['INSERT INTO users (id, username, password_hash, role, auth_provider, email) VALUES (?, ?, ?, ?, ?, ?)', [id, username, 'x', role, provider, email]]);
}
seed.push(
    ['UPDATE users SET totp_enabled = ? WHERE id = ?', [on, 13]],
    ['UPDATE users SET totp_enabled = ? WHERE id = ?', [off, 14]],
    ["UPDATE users SET guid = 'guid-13' WHERE id = 13", []],
);
if (pg) seed.push(["SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT MAX(id) FROM users))", []]);
seed.push(
    ["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-alpha', 'Alpha', 'note a', 'team-1')", []],
    ["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-beta', 'Beta', '', '')", []],
    ["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-gamma', 'Gamma', 'g', 'team:2')", []],
    ["INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 13 FROM user_groups WHERE guid = 'ug-alpha'", []],
    ["INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 13 FROM user_groups WHERE guid = 'ug-beta'", []],
    ["INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 5 FROM user_groups WHERE guid = 'ug-alpha'", []],
    ["INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 6 FROM user_groups WHERE guid = 'ug-gamma'", []],
    ["INSERT INTO folders (name, color, icon, sort_order) VALUES ('Office', '#6366f1', 'folder', 0)", []],
    ["INSERT INTO folders (name, color, icon, sort_order) VALUES ('Lab', '#22c55e', 'folder', 1)", []],
    ["INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('D1', 1)", []],
    ["INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('D2', 2)", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('folder_1', 'Office', 'BetterDesk folder access scope', '', 'manual', '')", []],
    ["INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'folder_1'", []],
    ["INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 13 FROM device_groups WHERE guid = 'folder_1'", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-lab', 'Lab devices', '', '', 'tag', 'lab')", []],
    ["INSERT INTO device_group_user_group_access (device_group_id, user_group_id) SELECT d.id, u.id FROM device_groups d, user_groups u WHERE d.guid = 'dg-lab' AND u.guid = 'ug-beta'", []],
    ["INSERT INTO device_group_user_group_access (device_group_id, user_group_id) SELECT d.id, u.id FROM device_groups d, user_groups u WHERE d.guid = 'dg-lab' AND u.guid = 'ug-gamma'", []],
    ["INSERT INTO strategies (guid, name) VALUES ('st-1', 'Strategy one')", []],
    ["INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('user', 'guid-13', 'st-1')", []],
    ["INSERT INTO user_peer_grants (user_id, peer_id) VALUES (13, 'D3')", []],
    ["INSERT INTO user_peer_grants (user_id, peer_id) VALUES (13, 'D9')", []],
    [pg
        ? "INSERT INTO user_avatars (user_id, mime_type, data, version, source) VALUES (13, 'image/png', decode('89504e47', 'hex'), 'v13', 'profile')"
        : "INSERT INTO user_avatars (user_id, mime_type, data, version, source) VALUES (13, 'image/png', X'89504E47', 'v13', 'profile')", []],
    ["INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('group', 'ug-gamma', 'devices', 'read')", []],
    ["INSERT INTO client_feature_policies (subject_type, subject_id, feature_key, enabled) VALUES ('group', 'ug-gamma', 'audio', ?)", [on]],
    ["INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('user', '13', 'devices', 'write')", []],
);

// ---- The steps ----

const steps = [];
let n = 0;
let address = 0;
function nextAddress() {
    address += 1;
    return `10.20.${Math.floor(address / 250)}.${(address % 250) + 1}`;
}

/**
 * One request. body is a value sent as JSON, or text with raw: true.
 * o.ip picks the client address (one of its own by default, so the
 * password limiter counts only where a step shares one on purpose).
 * o.capture names values of the answer for later paths: {name: 'data.id'}.
 * o.stringId sends the session's userId as text, as the console stores it
 * for 64-bit ids.
 */
function req(role, method, path, body, o = {}) {
    n += 1;
    const s = { name: `${n} ${o.label ? o.label + ' ' : ''}${method} ${path}`, role, method, path };
    if (body !== undefined) s.body = o.raw ? body : JSON.stringify(body);
    s.headers = { 'X-Forwarded-For': o.ip || nextAddress() };
    if (o.stringId) s.headers['X-Oracle-String-Id'] = '1';
    if (o.capture) s.capture = o.capture;
    steps.push(s);
}
function sql(label, query, params = []) {
    n += 1;
    steps.push({ name: `${n} ${label}`, sql: query, params });
}
const usersTable = () => sql('users table',
    "SELECT CAST(id AS TEXT) AS id, username, role, auth_provider, email, CASE WHEN password_hash = 'x' THEN 0 ELSE 1 END AS hashed, CASE WHEN guid <> '' THEN 1 ELSE 0 END AS has_guid FROM users ORDER BY id");
const memberships = () => sql('memberships',
    'SELECT g.guid, CAST(m.user_id AS TEXT) AS user_id FROM user_group_members m INNER JOIN user_groups g ON g.id = m.user_group_id ORDER BY g.guid, m.user_id');
const scopeRows = () => {
    sql('device group access', 'SELECT d.guid, u.username FROM device_group_user_access a INNER JOIN device_groups d ON d.id = a.device_group_id INNER JOIN users u ON u.id = a.user_id ORDER BY d.guid, u.username');
    sql('device group group access', 'SELECT d.guid, g.guid AS user_group FROM device_group_user_group_access a INNER JOIN device_groups d ON d.id = a.device_group_id INNER JOIN user_groups g ON g.id = a.user_group_id ORDER BY d.guid, g.guid');
    // The default group has a random guid, which has no place in the order
    sql('device groups', "SELECT guid, name, note, source_type, tag_filter FROM device_groups WHERE name <> 'Default' ORDER BY guid");
    sql('peer grants', 'SELECT CAST(user_id AS TEXT) AS user_id, peer_id FROM user_peer_grants ORDER BY user_id, peer_id');
    sql('strategy assignments', "SELECT u.username, a.strategy_guid FROM strategy_assignments a INNER JOIN users u ON u.guid = a.target_key WHERE a.target_type = 'user' ORDER BY u.username");
};
const groupsTable = () => sql('user groups', 'SELECT name, note, team_id FROM user_groups ORDER BY name, note');
const policies = () => {
    sql('panel access policies', 'SELECT subject_type, subject_id, resource_key, access_level FROM panel_access_policies ORDER BY subject_type, subject_id, resource_key');
    sql('client feature policies', 'SELECT subject_type, subject_id, feature_key FROM client_feature_policies ORDER BY subject_type, subject_id, feature_key');
};
// The Go console's session notes every request from another address than
// the sign-in's, which this harness has no part of
const audit = () => sql('audit log',
    "SELECT CAST(user_id AS TEXT) AS user_id, action, details, ip_address FROM audit_log WHERE action <> 'session_ip_change' ORDER BY id");

const long = (c, k) => c.repeat(k);
const strong = 'Strong-Pass-1!';

// 1. Who may call what: every route as every role, with what is refused
// after the guards.
for (const role of ROLES) {
    req(role, 'GET', '/api/users');
    req(role, 'GET', '/api/panel/user-groups');
    req(role, 'POST', '/api/panel/user-groups', {});
    req(role, 'PATCH', '/api/panel/user-groups/no-such-group', {});
    req(role, 'DELETE', '/api/panel/user-groups/no-such-group');
    req(role, 'POST', '/api/users', {});
    req(role, 'PATCH', '/api/users/999999', {});
    req(role, 'GET', '/api/users/13/effective-scope');
    req(role, 'DELETE', '/api/users/999999');
    req(role, 'POST', '/api/users/999999/reset-password', {});
    req(role, 'GET', '/api/users/13/organizations');
    req(role, 'POST', '/api/users/999999/organizations', {});
}
req('admin', 'GET', '/API/Users', undefined, { label: 'letter case' });
req('admin', 'GET', '/api/users/', undefined, { label: 'slash at the end' });
req('admin', 'GET', '/api/users?x=1', undefined, { label: 'with a query' });
usersTable();

// 2. The user groups
req('operator', 'GET', '/api/panel/user-groups');
const groupBodies = [
    { name: 'New group' }, { name: '  Trim me  ', note: '  spaced  ', team_id: '  t-1  ' }, { name: long('n', 80) },
    { name: long('n', 81) }, { name: long('\u{1F600}', 40) }, { name: long('\u{1F600}', 41) }, { name: '' }, { name: '   ' },
    { name: null }, { name: 0 }, { name: 5 }, { name: true }, { name: false }, { name: [] }, { name: ['x', 'y'] }, { name: {} },
    { name: { toString: 'x' } }, { name: 'Note', note: long('a', 500) }, { name: 'Note', note: long('a', 501) },
    { name: 'Note', note: 7 }, { name: 'Note', note: false }, { name: 'Team', team_id: 'bad team' }, { name: 'Team', team_id: long('t', 81) },
    { name: 'Team', team_id: long('t', 80) }, { name: 'Team', team_id: 'a:b.c-d_e' }, { name: 'Team', team_id: 12 }, { name: 'Team', team_id: [1, 2] },
    { name: 'Team', team_id: { toString: 1 } }, { guid: 'my-own-guid', name: 'With a guid' },
];
groupBodies.forEach((body) => req('admin', 'POST', '/api/panel/user-groups', body));
req('admin', 'POST', '/api/panel/user-groups', '[{"name":"In a list"}]', { raw: true });
req('global_admin', 'POST', '/api/panel/user-groups', { name: 'By global admin' }, { capture: { byGlobal: 'data.group.guid' } });
req('admin', 'POST', '/api/panel/user-groups', { name: 'Patched later', note: 'n1' }, { capture: { patched: 'data.group.guid' } });
req('admin', 'PATCH', '/api/panel/user-groups/{patched}', { name: 'Patched now', note: '', team_id: 'team-9' });
req('admin', 'PATCH', '/api/panel/user-groups/{patched}', { note: 'no name' });
req('admin', 'PATCH', '/api/panel/user-groups/{patched}', { name: 'x', team_id: 'bad id' });
req('super_admin', 'PATCH', '/api/panel/user-groups/{patched}', { name: 5, note: 6, team_id: 7 });
req('operator', 'PATCH', '/api/panel/user-groups/{patched}', { name: 'by operator' });
for (const guid of ['%20ug-beta%20', 'bad%20guid', 'a%2Fb', long('g', 80), long('g', 81), 'UG-ALPHA', 'ug.alpha:1']) {
    req('admin', 'PATCH', `/api/panel/user-groups/${guid}`, { name: 'Renamed' });
}
req('admin', 'PATCH', '/api/panel/user-groups/ug-alpha', '[1]', { raw: true });
req('admin', 'GET', '/api/panel/user-groups');
groupsTable();
req('admin', 'DELETE', '/api/panel/user-groups/{patched}');
req('admin', 'DELETE', '/api/panel/user-groups/{patched}');
req('global_admin', 'DELETE', '/api/panel/user-groups/{byGlobal}');
req('admin', 'DELETE', '/api/panel/user-groups/%20ug-gamma');
req('admin', 'DELETE', '/api/panel/user-groups/bad%20guid');
req('admin', 'DELETE', '/api/panel/user-groups/my-own-guid');
policies();
memberships();
scopeRows();
groupsTable();
req('admin', 'GET', '/api/panel/user-groups');

// 3. Creating accounts
const creates = [
    {}, { username: 'nopass' }, { password: strong }, { username: '', password: strong }, { username: 0, password: strong },
    { username: 'zero_pass', password: 0 }, { username: 'ab', password: strong }, { username: long('a', 33), password: strong },
    { username: long('a', 32), password: strong }, { username: 'bad name', password: strong }, { username: 'ümlaut', password: strong },
    { username: 'a-b', password: strong }, { username: 'line\nbreak', password: strong }, { username: 12345, password: strong },
    { username: 1.5, password: strong }, { username: true, password: strong }, { username: ['abc'], password: strong },
    { username: ['ab', 'cd'], password: strong }, { username: {}, password: strong }, { username: { toString: 'x' }, password: strong },
    { username: 'e2e_admin', password: strong }, { username: 'E2E_ADMIN', password: strong },
    { username: 'grp_list', password: strong, groupGuids: ['ug-alpha', ' ug-beta ', 'ug-alpha', 'missing'] },
    { username: 'grp_text', password: strong, groupGuids: 'ug-alpha,ug-beta,,' },
    { username: 'grp_bad', password: strong, groupGuids: ['bad guid'] },
    { username: 'grp_long', password: strong, groupGuids: [long('g', 81)] },
    { username: 'grp_empty', password: strong, groupGuids: [''] },
    { username: 'grp_num', password: strong, groupGuids: 5 },
    { username: 'grp_null', password: strong, groupGuids: null },
    { username: 'grp_nulls', password: strong, groupGuids: [null, 'ug-alpha', 0, false] },
    { username: 'grp_obj', password: strong, groupGuids: [{ guid: 'ug-alpha' }] },
    { username: 'grp_tostr', password: strong, groupGuids: [{ toString: 1 }] },
    { username: 'grp_many', password: strong, groupGuids: Array.from({ length: 101 }, (_, i) => `g${i}`) },
    { username: 'grp_bad_weak', password: 'weak', groupGuids: ['bad guid'] },
    { username: 'weak_short', password: 'short' }, { username: 'weak_lower', password: 'abcdefgh' },
    { username: 'medium_one', password: 'Abcdefgh' }, { username: 'weak_digits', password: '12345678901234' },
    { username: 'pw_number', password: 12345678 }, { username: 'pw_true', password: true },
    { username: 'pw_list', password: ['Abcdefgh1!'] }, { username: 'pw_list8', password: ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'] },
    { username: 'pw_object', password: {} }, { username: 'pw_length', password: { length: 20 } }, { username: 'pw_weaklist', password: ['a', 'b'] },
    { username: 'pw_surrogate', password: 'Abc\ud800defg1!' }, { username: 'pw_long', password: long('Ab1!', 30) },
    { username: 'mail_trim', password: strong, email: '  Mail@Example.com  ' }, { username: 'mail_empty', password: strong, email: '' },
    { username: 'mail_null', password: strong, email: null }, { username: 'mail_bad', password: strong, email: 'not-a-mail' },
    { username: 'mail_long', password: strong, email: long('a', 195) + '@b.cde' }, { username: 'mail_num', password: strong, email: 5 },
    { username: 'mail_tostr', password: strong, email: { toString: 1 } }, { username: 'mail_true', password: strong, email: true },
    { username: 'mail_200', password: strong, email: long('a', 194) + '@b.cde' },
    { username: 'scope_folders', password: strong, folderIds: [1, '2', 'x', 1.9, 99] },
    { username: 'scope_text', password: strong, folderIds: '1' },
    { username: 'scope_peers', password: strong, peerIds: ' D1, D2,,D1 ' },
    { username: 'scope_peerlist', password: strong, peerIds: ['D3', 5, null, ' D4 '] },
    { username: 'scope_strategy', password: strong, strategyGuid: 'st-1' },
    { username: 'scope_nostrategy', password: strong, strategyGuid: '' },
    { username: 'scope_missing', password: strong, strategyGuid: 'missing' },
    { username: 'scope_all', password: strong, folderIds: [2], peerIds: ['D2'], strategyGuid: ' st-1 ', groupGuids: ['ug-beta'], email: 'all@example.invalid' },
];
creates.forEach((body) => req('admin', 'POST', '/api/users', body));
req('admin', 'POST', '/api/users', '[{"username":"in_a_list"}]', { raw: true });
const targetRoles = ['super_admin', 'admin', 'server_admin', 'global_admin', 'operator', 'viewer', 'pro', 'guest_issuer', 'nonsense', null, 5];
for (const caller of ['global_admin', 'super_admin', 'admin', 'guest_issuer']) {
    targetRoles.forEach((role, i) => req(caller, 'POST', '/api/users', { username: `by_${caller}_${i}`, password: strong, role }));
    req(caller, 'POST', '/api/users', { username: `by_${caller}_none`, password: strong });
}
req('global_admin', 'POST', '/api/users', { username: 'weak_and_high', password: 'weak', role: 'admin' });
req('global_admin', 'POST', '/api/users', { username: 'list_and_high', password: ['Abcdefgh1!'], role: 'admin' });
usersTable();
memberships();
scopeRows();
audit();

// 4. The password limiter: creating, resetting and changing passwords
// share three per five minutes per address.
for (let i = 0; i < 4; i++) req('admin', 'POST', '/api/users', { username: `limit_${i}`, password: strong }, { ip: '10.99.0.1' });
req('admin', 'POST', '/api/users/13/reset-password', { newPassword: strong }, { ip: '10.99.0.1' });
for (let i = 0; i < 3; i++) req('super_admin', 'POST', '/api/users/999999/reset-password', {}, { ip: '10.99.0.2' });
req('super_admin', 'POST', '/api/users', { username: 'limit_after_reset', password: strong }, { ip: '10.99.0.2' });
for (let i = 0; i < 5; i++) req('viewer', 'POST', '/api/users', { username: 'limit_viewer', password: strong }, { ip: '10.99.0.3' });
req('admin', 'POST', '/api/users', { username: 'limit_viewer_ip', password: strong }, { ip: '10.99.0.3' });
for (let i = 0; i < 4; i++) req('admin', 'POST', '/api/users', {}, { ip: '10.99.0.4' });

// 5. Changing accounts
for (const id of ['abc', '0', '-1', '1.5', '13abc', '%2013', '0x10', '1e3', '99999999999', '2147483648', '999999']) {
    req('admin', 'PATCH', `/api/users/${id}`, { email: 'id@example.invalid' });
}
req('admin', 'PATCH', '/api/users/9', { password: strong });
req('admin', 'PATCH', '/api/users/9', { role: 'viewer' });
req('admin', 'PATCH', '/api/users/9', { role: '', password: '', email: 'ldap@example.invalid' });
req('admin', 'PATCH', '/api/users/10', { password: strong });
req('admin', 'PATCH', '/api/users/10', { role: 'operator' });
req('admin', 'PATCH', '/api/users/15', { role: 'viewer' });
req('admin', 'PATCH', '/api/users/15', { password: strong });
req('super_admin', 'PATCH', '/api/users/2', { role: 'operator' });
req('super_admin', 'PATCH', '/api/users/2', { role: 'nonsense' });
req('super_admin', 'PATCH', '/api/users/2', { role: 'admin' });
req('admin', 'PATCH', '/api/users/1', { role: 'viewer' });
req('admin', 'PATCH', '/api/users/1', { role: 'super_admin' });
req('global_admin', 'PATCH', '/api/users/4', { role: 'operator' });
for (const role of ['operator', 'admin', 'super_admin', 'server_admin', 'global_admin', 'pro', 'viewer', 'guest_issuer', 'nonsense', 5, true, [], 'Operator']) {
    req('global_admin', 'PATCH', '/api/users/18', { role });
}
req('global_admin', 'PATCH', '/api/users/12', { role: 'viewer' });
req('global_admin', 'PATCH', '/api/users/12', { password: strong });
req('super_admin', 'PATCH', '/api/users/16', { role: 'super_admin' });
req('admin', 'PATCH', '/api/users/13', { role: 'viewer', password: 'weak' });
req('admin', 'PATCH', '/api/users/13', { password: 12345678 });
req('admin', 'PATCH', '/api/users/13', { password: {} });
req('admin', 'PATCH', '/api/users/13', { password: strong, role: 'operator' });
req('admin', 'PATCH', '/api/users/13', { role: 'operator', groupGuids: ['bad guid'] });
req('admin', 'PATCH', '/api/users/13', { groupGuids: ['ug-beta'] });
req('admin', 'PATCH', '/api/users/13', { groupGuids: 'ug-alpha' });
req('admin', 'PATCH', '/api/users/13', { groupGuids: [] });
req('admin', 'PATCH', '/api/users/13', { groupGuids: ['ug-alpha', 'ug-beta'], folderIds: [2], peerIds: ['D9', 'D1'] });
req('admin', 'PATCH', '/api/users/13', { folderIds: [], peerIds: '' });
req('admin', 'PATCH', '/api/users/13', { strategyGuid: 'st-1' });
req('admin', 'PATCH', '/api/users/13', { strategyGuid: null });
req('admin', 'PATCH', '/api/users/13', { strategyGuid: 'missing', folderIds: [1] });
req('admin', 'PATCH', '/api/users/13', { email: ' New@Mail.io ' });
req('admin', 'PATCH', '/api/users/13', { email: null });
req('admin', 'PATCH', '/api/users/13', { email: 'bad', groupGuids: ['ug-beta'] });
req('admin', 'PATCH', '/api/users/13', { email: { toString: 1 } });
req('admin', 'PATCH', '/api/users/13', {});
req('admin', 'PATCH', '/api/users/13', '[{"role":"viewer"}]', { raw: true });
req('admin', 'PATCH', '/api/users/14', {});
req('admin', 'PATCH', '/api/users/18', { folderIds: [1, 2], strategyGuid: 'st-1' });
req('operator', 'PATCH', '/api/users/13', { role: 'viewer' });
req('server_admin', 'PATCH', '/api/users/13', { role: 'viewer' });
usersTable();
memberships();
scopeRows();

// A panel access row can give an account a section it has no permission
// for: guest_issuer gets write on users.
sql('policy for guest_issuer', "INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES ('user', '7', 'users', 'write')");
req('guest_issuer', 'GET', '/api/users');
req('guest_issuer', 'PATCH', '/api/users/18', { role: 'viewer' });
req('guest_issuer', 'POST', '/api/users', { username: 'by_policy', password: strong });
req('guest_issuer', 'POST', '/api/panel/user-groups', { name: 'By policy' });
req('guest_issuer', 'POST', '/api/users/18/reset-password', { newPassword: strong });
req('guest_issuer', 'DELETE', '/api/users/999999');
sql('policy for guest_issuer gone', "DELETE FROM panel_access_policies WHERE subject_type = 'user' AND subject_id = '7'");

// 6. Effective scope and organizations
for (const id of ['1', '5', '6', '13', '14', '18', '999999', 'abc', '0', '5x']) {
    req('admin', 'GET', `/api/users/${id}/effective-scope`);
}
req('operator', 'GET', '/api/users/5/effective-scope');
for (const id of ['13', '14', '16', '17', '18', '999999', 'abc', '0']) {
    req('admin', 'GET', `/api/users/${id}/organizations`);
    req('admin', 'POST', `/api/users/${id}/organizations`, { org_id: 'org-1', role: 'member' });
}
req('admin', 'POST', '/api/users/13/organizations', '[1,2]', { raw: true });
req('admin', 'POST', '/api/users/13/organizations', {});
req('operator', 'GET', '/api/users/13/organizations');
req('operator', 'POST', '/api/users/13/organizations', {});
req('global_admin', 'POST', '/api/users/13/organizations', { org_id: 'org-2' });

// 7. Resetting passwords
for (const [id, body] of [
    ['abc', { newPassword: strong }], ['0', { newPassword: strong }], ['999999', { newPassword: strong }],
    ['14', {}], ['14', { newPassword: '' }], ['14', { newPassword: 0 }], ['14', { newPassword: 'weak' }],
    ['14', { newPassword: 12345678 }], ['14', { newPassword: {} }], ['14', { newPassword: ['Abcdefgh1!'] }],
    ['14', { newPassword: strong }], ['9', { newPassword: strong }], ['14', '[1]'],
]) {
    if (typeof body === 'string') req('admin', 'POST', `/api/users/${id}/reset-password`, body, { raw: true });
    else req('admin', 'POST', `/api/users/${id}/reset-password`, body);
}
req('global_admin', 'POST', '/api/users/12/reset-password', { newPassword: strong });
usersTable();

// 8. Deleting accounts
for (const id of ['abc', '0', '999999']) req('admin', 'DELETE', `/api/users/${id}`);
req('admin', 'DELETE', '/api/users/1');
req('super_admin', 'DELETE', '/api/users/2');
req('admin', 'DELETE', '/api/users/13');
req('admin', 'DELETE', '/api/users/13');
req('global_admin', 'DELETE', '/api/users/9');
req('global_admin', 'DELETE', '/api/users/11');
req('operator', 'DELETE', '/api/users/14');
usersTable();
memberships();
scopeRows();
policies();
sql('pictures', 'SELECT CAST(user_id AS TEXT) AS user_id, version FROM user_avatars ORDER BY user_id');
req('admin', 'GET', '/api/users');

// The last administrator: user 1 is super_admin now (step 5), 2 is
// admin, 12 was made a viewer, 16 a super_admin. A session whose userId
// is text compares unequal to the number of the path.
req('admin', 'DELETE', '/api/users/1', undefined, { stringId: true, label: 'text id' });
req('admin', 'PATCH', '/api/users/2', { role: 'viewer' }, { stringId: true, label: 'text id' });
req('global_admin', 'DELETE', '/api/users/2');
req('global_admin', 'PATCH', '/api/users/2', { role: 'viewer' });
// One administrator left: user 16, a super_admin since step 5.
sql('one administrator left', "UPDATE users SET role = 'viewer' WHERE role IN ('admin', 'super_admin') AND id <> 16");
req('global_admin', 'DELETE', '/api/users/16');
req('global_admin', 'PATCH', '/api/users/16', { role: 'operator' });
sql('the administrator is a server admin now', "UPDATE users SET role = 'server_admin' WHERE id = 16");
req('global_admin', 'DELETE', '/api/users/16');
usersTable();
audit();

// ---- Running ----

function pick(value, dotted) {
    return dotted.split('.').reduce((v, key) => (v && typeof v === 'object' ? v[key] : undefined), value);
}

function send(port, step, vars) {
    return new Promise((resolve, reject) => {
        const fill = (s) => s.replace(/\{([A-Za-z]+)\}/g, (_, name) => (name in vars ? encodeURIComponent(vars[name]) : '999999999'));
        const headers = { Accept: 'application/json' };
        if (step.role) headers['X-Oracle-Role'] = step.role;
        for (const [name, value] of Object.entries(step.headers || {})) headers[name] = value;
        let payload = null;
        if (step.body !== undefined) {
            payload = Buffer.from(step.body);
            headers['Content-Type'] = 'application/json';
            headers['Content-Length'] = payload.length;
        }
        const r = http.request({ host: '127.0.0.1', port, method: step.method, path: fill(step.path), headers }, (res) => {
            const chunks = [];
            res.on('data', (c) => chunks.push(c));
            res.on('end', () => resolve({ status: res.statusCode, body: Buffer.concat(chunks).toString('utf8') }));
        });
        r.on('error', reject);
        if (payload) r.write(payload);
        r.end();
    });
}

(async () => {
    const apiServer = await startApi();
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${apiServer.address().port}/api`;
    process.env.HBBS_API_URL = process.env.BETTERDESK_API_URL;
    process.env.SERVER_BACKEND = 'betterdesk';
    process.env.API_KEY_PATH = path.join(scratch, '.api_key');
    fs.writeFileSync(process.env.API_KEY_PATH, 'oracle-api-key');
    const db = await openDatabase();
    for (const [query, params] of seed) await db.exec(query, params);

    const express = web('node_modules/express');
    const cookieParser = web('node_modules/cookie-parser');
    const app = express();
    app.set('trust proxy', true);
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use(cookieParser());
    app.use((r, _res, next) => {
        const user = USERS[r.headers['x-oracle-role']];
        r.session = user
            ? { userId: r.headers['x-oracle-string-id'] ? String(user.id) : user.id, user: { ...user }, destroy() {} }
            : { destroy() {} };
        next();
    });
    app.use(web('services/panelAccessPolicyService').loadEffectivePolicy);
    app.use(web('middleware/panelAccess').panelAccessGuard);
    app.use(web('middleware/i18n').initI18n());
    app.use(web('lib/requireJsonContentType').requireJsonContentType);
    app.use('/', web('routes/users.routes'));
    app.use((r, res) => res.status(404).json({ oracle: 'no route' }));
    // The console's error handler, as it answers the API in production
    // eslint-disable-next-line no-unused-vars
    app.use((err, r, res, _next) => res.status(err.status || 500).json({ success: false, error: 'Internal Server Error' }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;

    const realError = console.error;
    const realLog = console.log;
    const realWarn = console.warn;
    console.error = () => {};
    console.log = () => {};
    console.warn = () => {};
    const out = [];
    const vars = {};
    try {
        for (const step of steps) {
            if (step.sql) {
                const rows = await db.exec(step.sql, step.params);
                out.push({ ...step, rows: JSON.stringify(rows) });
                continue;
            }
            calls.length = 0;
            const answer = await send(port, step, vars);
            // Calls that leave without being awaited, as the organization proxy's
            await new Promise((resolve) => setTimeout(resolve, 20));
            if (step.capture) {
                let parsed = null;
                try { parsed = JSON.parse(answer.body); } catch (_) { /* not JSON */ }
                for (const [name, dotted] of Object.entries(step.capture)) {
                    const v = pick(parsed, dotted);
                    if (v !== undefined && v !== null) vars[name] = String(v);
                }
            }
            out.push({ ...step, status: answer.status, answer: answer.body, extra: { calls: calls.map((c) => ({ ...c })) } });
        }
    } finally {
        console.error = realError;
        console.log = realLog;
        console.warn = realWarn;
        await new Promise((resolve) => server.close(resolve));
        apiServer.close();
        await db.close();
    }
    const file = path.join(__dirname, '..', `node_${variant}.json`);
    const api = Object.fromEntries(Object.entries(API).map(([k, v]) => [k, v]));
    fs.writeFileSync(file, `${JSON.stringify({ dialect, shape: SHAPE, seed, api, steps: out }, null, 1)}\n`);
    realLog(`${file}: ${out.length} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
