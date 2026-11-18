'use strict';

/**
 * Records what routes/registration.routes.js answers, with the real
 * database adapter, the real permission loading, panel access guard,
 * translations, JSON content-type rule, API client, device layer, device
 * group service and key service, against a made-up API server. See
 * README.md.
 *
 *   node registration.js                         ../node_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node registration.js   ../node_postgres.json
 *
 * The panel events the routes publish are taken from a stand-in for
 * services/goConsoleBridge.js. Every request carries the session of its
 * role in the header X-Oracle-Role.
 */

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'registration-oracle-'));
const dbName = `oracle_reg_${process.pid}_${Date.now() % 1000000}`;

const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };
function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

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

// A valid server key in the key file, so the key service never asks the API
const SERVER_KEY = 'OeVuKk5nlHiXp+APNn0Y3pC1Iwpwn44JGqrQCsWqmBw=';

// ---- The made-up API server ----

const json = (status, value) => ({ status, type: 'application/json', body: JSON.stringify(value) });
const text = (status, body) => ({ status, type: 'text/plain; charset=utf-8', body });
const helpList = [
    { id: 21, device_id: 'DEV1', hostname: 'pc-1', message: 'printer', status: 'acknowledged', handled_by: 'apikey:console-bridge', created_at: '2001-03-01T00:00:30Z', updated_at: '2001-03-01T00:05:00Z' },
    { id: 22, device_id: 'DEV2', hostname: 'pc-2', message: 'vpn', status: 'acknowledged', handled_by: 'apikey:console-bridge', created_at: '2001-03-01T00:00:30Z' },
    { id: 23, device_id: 'DEV3', message: 'font', status: 'resolved', handled_by: 'apikey:other', created_at: '2001-02-01T00:00:00Z', updated_at: '2001-02-02T00:00:00.5Z' },
    { id: 24, device_id: 'DEV4', status: 'acknowledged', handled_by: 'apikey:x', created_at: '2001-01-01T00:00:00+02:00' },
    { id: 25, device_id: 'DEV5', status: 'pending', handled_by: '', created_at: '2001-01-05 10:00:00' },
    { id: 26, device_id: 'DEV6', status: 'resolved', handled_by: 'demo_helpdesk', created_at: null, updated_at: 'nonsense' },
    { id: 27, device_id: 'DEV7', status: 'accepted', handled_by: 'someone' },
];
const API = {
    'GET /api/peers': json(200, [
        { id: 'DEV1', hostname: 'pc-1', tags: ['lab'], online: true },
        { id: 'DEV2', hostname: 'pc-2', tags: [] },
        { id: 'DEV5', hostname: 'pc-5', tags: 'lab,office' },
    ]),
    'GET /api/remote-targets': json(200, []),
    'GET /api/peers/DEV1': json(200, { id: 'DEV1', hostname: 'pc-1', tags: ['lab'] }),
    'GET /api/peers/DEV2': json(200, { id: 'DEV2', hostname: 'pc-2' }),
    'GET /api/peers/DEV5': json(200, { id: 'DEV5', hostname: 'pc-5', tags: 'lab,office' }),
    'PATCH /api/peers/DEV1': json(200, { success: true }),
    'PATCH /api/peers/DEV2': json(200, { success: true }),
    'PATCH /api/peers/DEV5': json(500, { error: 'peer update failed' }),
    'PUT /api/peers/DEV1/tags': json(200, { success: true }),
    'PUT /api/peers/DEV2/tags': json(200, { success: true }),
    'PUT /api/peers/DEV5/tags': text(500, 'boom\n'),
    'GET /api/enrollment/pending': json(200, {
        devices: [
            { device_id: 'E1', hostname: 'enr-1', platform: 'Windows', ip: '10.1.0.1', created_at: '2001-04-01T00:00:00Z' },
            { device_id: 'E2', platform: 'Linux', created_at: '2001-04-02T00:00:00Z' },
        ],
        count: 2,
    }),
    'GET /api/enrollment/mode': json(200, { mode: 'approval' }),
    'GET /api/enrollment/history': json(200, { devices: [{ device_id: 'H1', status: 'rejected' }], count: 1 }),
    'POST /api/enrollment/approve/E1': json(200, { success: true, device_id: 'E1' }),
    'POST /api/enrollment/approve/DEV1': json(200, { success: true, device_id: 'DEV1' }),
    'POST /api/enrollment/reject/E1': json(200, { success: true, banned: false }),
    'POST /api/enrollment/clear-rejection/E1': json(200, { success: true, unbanned: true }),
    'POST /api/enrollment/clear-rejection/E2': json(200, { success: true, unbanned: false }),
    'GET /api/help/requests': json(200, helpList),
    'POST /api/help/requests/21/acknowledge': json(200, { id: 21, status: 'acknowledged', handled_by: 'apikey:console-bridge' }),
    'POST /api/help/requests/21/resolve': json(200, { id: 21, status: 'resolved', handled_by: 'apikey:console-bridge' }),
    'POST /api/help/requests/25/acknowledge': json(200, { id: 25, status: 'acknowledged', handled_by: 'apikey:console-bridge' }),
    'POST /api/help/requests/021/acknowledge': json(200, { id: 21, status: 'acknowledged' }),
};
const NOT_FOUND = json(404, { error: 'not found' });

// Every permission of the built-in role map (middleware/auth.js). The
// permission loading asks the API server for a role's permissions; the
// made-up server answers with the built-in map, which is what the Go
// console uses without overrides.
const PERMISSIONS = [
    'server.config', 'server.keys', 'server.attestation', 'blocklist.edit', 'user.view', 'user.create', 'user.edit',
    'user.delete', 'org.create', 'org.edit', 'org.delete', 'org.manage_users', 'org.manage_devices', 'device.view',
    'device.connect', 'device.edit', 'device.delete', 'remote_target.view', 'remote_target.connect', 'remote_target.edit',
    'remote_target.delete', 'remote_target.test', 'guest.create', 'device.ban', 'device.change_id',
    'device.connection_mode', 'audit.view', 'metrics.view', 'cdap.view', 'cdap.command', 'cdap.terminal', 'cdap.files',
    'mesh.terminal', 'mesh.files', 'mesh.power', 'chat.access', 'enrollment.manage', 'enrollment.approve',
    'branding.edit', 'billing.view', 'billing.manage', 'billing.reports', 'billing.export',
];
const rolePermissions = /^\/api\/roles\/([^/?]+)\/permissions/;

let overrides = {};
const calls = [];
function startApi() {
    const server = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            const body = Buffer.concat(chunks).toString('utf8');
            const roleMatch = req.method === 'GET' && rolePermissions.exec(req.url);
            if (roleMatch) {
                const { roleHasPermission } = web('middleware/auth');
                const role = decodeURIComponent(roleMatch[1]);
                res.writeHead(200, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({ role, permissions: PERMISSIONS.filter((p) => roleHasPermission(role, p)) }));
                return;
            }
            calls.push({ method: req.method, url: req.url, body });
            const key = `${req.method} ${req.url.split('?')[0]}`;
            const reply = overrides[key] || API[key] || NOT_FOUND;
            if (reply.hang) {
                req.socket.destroy();
                return;
            }
            const headers = reply.type ? { 'Content-Type': reply.type } : {};
            res.writeHead(reply.status, headers);
            res.end(reply.body || '');
        });
    });
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

// ---- The database ----

async function openDatabase() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
    let admin = null;
    if (pg) {
        const { Pool } = web('node_modules/pg');
        admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE DATABASE ${dbName}`);
        const url = new URL(PG_DSN);
        url.pathname = `/${dbName}`;
        process.env.DB_TYPE = 'postgres';
        process.env.DATABASE_URL = url.toString();
    } else {
        process.env.DB_TYPE = 'sqlite';
        delete process.env.DATABASE_URL;
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
            await admin.query(`DROP DATABASE ${dbName} WITH (FORCE)`);
            await admin.end();
        }
    };
    return { exec, close };
}

// ---- The rows every run starts from ----

const at = (clock) => (pg ? `2001-01-01 ${clock}+00` : `2001-01-01 ${clock}`);
const seed = [['DELETE FROM users', []]];
for (const u of Object.values(USERS)) {
    seed.push(['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [u.id, u.username, 'x', u.role]]);
}
const reg = `INSERT INTO pending_registrations (id, device_id, hostname, platform, version, ip_address, public_key, uuid, status,
    approved_by, approved_at, rejected_reason, access_token, console_url, server_address, server_key, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`;
seed.push(
    [reg, [1, 'DEV1', 'pc-1', 'Windows', '1.0', '10.0.0.1', '', '', 'pending', null, null, '', null, null, null, null, at('00:01:00'), at('00:01:00')]],
    [reg, [2, 'DEV2', 'Laptop 100%_off', 'Linux', '1.1', '10.0.0.2', 'key-2', 'uuid-2', 'pending', null, null, '', null, null, null, null, at('00:02:00'), at('00:02:00')]],
    [reg, [3, 'DEV3', 'approved-pc', 'macOS', '1.2', '10.0.0.3', '', '', 'approved', 'e2e_admin', at('00:03:30'), '', 'tok-3', 'http://h:5000', 'h:21116', 'key-3', at('00:03:00'), at('00:03:30')]],
    [reg, [4, 'DEV4', 'rejected-pc', 'Windows', '1.0', '10.0.0.4', '', '', 'rejected', null, null, 'no', null, null, null, null, at('00:04:00'), at('00:04:00')]],
    [reg, [5, 'DEV5', 'École-PC', 'Windows', '1.0', '192.168.1.5', '', '', 'pending', null, null, '', null, null, null, null, at('00:05:00'), at('00:05:00')]],
    [reg, [6, 'DEV6', null, null, null, null, null, null, null, null, null, null, null, null, null, null, at('00:06:00'), at('00:06:00')]],
    [reg, [7, 'DEV7', 'approved-bare', '', '', '', '', '', 'approved', null, null, '', null, null, null, null, at('00:07:00'), at('00:07:00')]],
    [reg, [8, 'DEV8', 'rejected-bare', '', '', '', '', '', 'rejected', null, null, null, null, null, null, null, at('00:08:00'), at('00:08:00')]],
);
if (pg) seed.push(["SELECT setval(pg_get_serial_sequence('pending_registrations', 'id'), 8)", []]);
seed.push(
    ["INSERT INTO folders (id, name, color, icon, sort_order) VALUES (1, 'Office', '#6366f1', 'folder', 0)", []],
    ["INSERT INTO folders (id, name, color, icon, sort_order) VALUES (2, 'Lab', '#22c55e', 'folder', 1)", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-a', 'Group A', '', '', 'manual', '')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-b', 'Group B', '', '', 'manual', '')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-c', 'Group C', '', '', 'manual', '')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-tag', 'Tag group', '', '', 'tag', 'lab')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('folder_1', 'Office', '', '', 'manual', '')", []],
    ["INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'dg-b'", []],
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV1' FROM device_groups WHERE guid = 'dg-c'", []],
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'DEV2' FROM device_groups WHERE guid = 'dg-b'", []],
    ["INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (6, 'help_request_accepted', 'Help request 21 accepted', '10.9.9.9', ?)", [pg ? '2001-03-01 00:00:00+00' : '2001-03-01 00:00:00']],
    ["INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (6, 'help_request_accepted', 'Help request 22 accepted', '10.9.9.9', ?)", [pg ? '2001-01-01 00:00:00+00' : '2001-01-01 00:00:00']],
    ["INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (NULL, 'help_request_resolved', 'Help request 23 resolved', '10.9.9.9', ?)", [pg ? '2001-02-01 00:00:10+00' : '2001-02-01 00:00:10']],
    ["INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (5, 'help_request_accepted', 'Help request 24 accepted by the night shift', '10.9.9.9', ?)", [pg ? '2001-01-01 00:00:00+00' : '2000-12-31 22:00:00']],
);

// ---- The steps ----

const steps = [];
let n = 0;
/**
 * One request. body is a value sent as JSON, or text with o.raw. o.type
 * is the Content-Type (application/json by default when there is a body),
 * o.headers more headers, o.host the Host header, o.api answers of the API
 * server for this request only, o.capture names values of the answer for
 * later paths: {name: 'id'} fills {name}.
 */
function req(role, method, target, body, o = {}) {
    n += 1;
    const s = { name: `${n} ${o.label ? o.label + ' ' : ''}${method} ${target}`, role, method, path: target };
    if (body !== undefined) s.body = o.raw ? body : JSON.stringify(body);
    if (o.type) s.type = o.type;
    if (o.headers) s.headers = o.headers;
    if (o.host) s.host = o.host;
    if (o.api) s.api = o.api;
    if (o.capture) s.capture = o.capture;
    steps.push(s);
}
function sql(label, query, params = []) {
    n += 1;
    steps.push({ name: `${n} ${label}`, sql: query, params });
}
/** listRegistrationNotifications(db, betterdeskApi) at this point */
function notifications(label, api) {
    n += 1;
    const s = { name: `${n} notifications ${label}`, notifications: true };
    if (api) s.api = api;
    steps.push(s);
}
const regTable = () => sql('registrations',
    "SELECT id, device_id, hostname, platform, version, ip_address, public_key, uuid, status, approved_by, rejected_reason, CASE WHEN access_token IS NULL THEN 'null' ELSE CAST(length(access_token) AS TEXT) END AS token, console_url, server_address, server_key FROM pending_registrations ORDER BY id");
const assignments = () => sql('folder assignments', 'SELECT device_id, folder_id FROM device_folder_assignments ORDER BY device_id');
const members = () => sql('group members', "SELECT g.guid, m.peer_id FROM device_group_members m INNER JOIN device_groups g ON g.id = m.device_group_id WHERE g.name <> 'Default' ORDER BY g.guid, m.peer_id");
const audit = () => sql('audit log', 'SELECT CAST(user_id AS TEXT) AS user_id, action, details, ip_address FROM audit_log ORDER BY id');
const setting = (key, value) => (value === null
    ? sql(`setting ${key} removed`, 'DELETE FROM settings WHERE key = ?', [key])
    : sql(`setting ${key}`, pg
        ? 'INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value'
        : 'INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)', [key, value]));

// The rows a run creates get fixed times, one minute apart in the order of
// their ids, so lists ordered by time have one order on every run
const fixTimes = () => sql('fixed times', pg
    ? "UPDATE pending_registrations SET created_at = TIMESTAMPTZ '2001-02-01 00:00:00+00' + id * INTERVAL '1 minute', updated_at = TIMESTAMPTZ '2001-02-01 00:00:00+00' + id * INTERVAL '1 minute' WHERE id > 8"
    : "UPDATE pending_registrations SET created_at = datetime('2001-02-01 00:00:00', '+' || id || ' minutes'), updated_at = datetime('2001-02-01 00:00:00', '+' || id || ' minutes') WHERE id > 8");

const bd = '/api/bd';
const long = (c, k) => c.repeat(k);

// 1. Who may call what: every route at both mounts as every role
for (const role of ROLES) {
    for (const m of ['', '/api/bd']) {
        req(role, 'GET', `${m}/api/registrations`);
        req(role, 'GET', `${m}/api/registrations/count`);
        req(role, 'GET', `${m}/api/registrations/enrollment-ui`);
        req(role, 'GET', `${m}/api/registrations/1`);
        req(role, 'PUT', `${m}/api/registrations/999/approve`, {});
        req(role, 'PUT', `${m}/api/registrations/999/reject`, {});
        req(role, 'DELETE', `${m}/api/registrations/999`);
        req(role, 'GET', `${m}/api/enrollment/pending`);
        req(role, 'GET', `${m}/api/enrollment/history`);
        req(role, 'POST', `${m}/api/enrollment/approve/X9`, {});
        req(role, 'POST', `${m}/api/enrollment/reject/X9`, {});
        req(role, 'POST', `${m}/api/enrollment/clear-rejection/X9`, {});
        req(role, 'GET', `${m}/api/help/requests`);
        req(role, 'POST', `${m}/api/help/requests/999/accept`, {});
        req(role, 'POST', `${m}/api/help/requests/999/resolve`, {});
    }
    req(role, 'POST', `${bd}/register-request`, {});
    req(role, 'GET', `${bd}/register-status`);
    req(role, 'POST', '/register-request', {});
    req(role, 'GET', '/register-status?device_id=DEV1');
}
req('admin', 'GET', '/API/Registrations/', undefined, { label: 'letter case and slash' });
req('admin', 'GET', '/api/BD/register-status?device_id=DEV3', undefined, { label: 'letter case of the mount' });
req('admin', 'GET', '/api/registrations/%E0%A4', undefined, { label: 'escape of no UTF-8' });

// 2. The device request: bodies of every kind
const deviceBodies = [
    {}, { device_id: null }, { device_id: '' }, { device_id: 'ab' }, { device_id: 'abc' }, { device_id: ' ab' },
    { device_id: '   ' }, { device_id: '   ' }, { device_id: '﻿ab' }, { device_id: '😀a' },
    { device_id: 'é' }, { device_id: long('d', 600) }, { device_id: 123456 }, { device_id: true }, { device_id: [] },
    { device_id: ['abcd'] }, { device_id: {} }, { device_id: { toString: 'x' } },
    { device_id: 'NEW1', hostname: 'h', platform: 'p', version: 'v', public_key: 'k', uuid: 'u' },
    { device_id: 'NEW2', hostname: long('h', 300), platform: long('p', 100), version: long('v', 40), public_key: long('k', 600), uuid: long('u', 80) },
    { device_id: 'NEW3', hostname: long('😀', 200), platform: long('é', 70), public_key: long('😀', 300), uuid: 'x😀'.repeat(40) },
    { device_id: 'NEW4', hostname: 0, platform: false, version: null, public_key: '', uuid: undefined },
    { device_id: 'NEW5', hostname: 5 }, { device_id: 'NEW6', platform: true }, { device_id: 'NEW7', version: ['1'] },
    { device_id: 'NEW8', public_key: { a: 1 } }, { device_id: 'NEW9', uuid: 7 }, { device_id: 'NEW10', hostname: 'ok', uuid: [] },
    { device_id: '  NEW11  ', hostname: 'trimmed id' }, { device_id: 'NEW12', hostname: 'line\nbreak sep', platform: '<b>&</b>' },
];
for (const body of deviceBodies) req('', 'POST', `${bd}/register-request`, body);
for (const raw of ['null', '[]', '["abc"]', '"abc"', '5', 'true', '{"device_id":"NEW14","device_id":"NEW15"}', '{"device_id":"NEW16"',
    '{"device_id":"NEW13","__proto__":{"device_id":"x"},"hostname":"proto"}', '{"device_id":"NEW22","hostname":"\\ud83d"}']) {
    req('', 'POST', `${bd}/register-request`, raw, { raw: true, label: 'raw' });
}
req('', 'POST', `${bd}/register-request`, 'device_id=NEW17', { raw: true, type: 'application/x-www-form-urlencoded', label: 'form at /api/bd' });
req('admin', 'POST', '/register-request', 'device_id=NEW18&hostname=form-pc&device_id=NEW19', { raw: true, type: 'application/x-www-form-urlencoded', label: 'form at the root' });
req('admin', 'POST', '/register-request', 'device_id=NEW20', { raw: true, type: 'text/plain', label: 'text at the root' });
req('', 'POST', `${bd}/register-request`, '{"device_id":"NEW21"}', { raw: true, type: 'application/json; charset=utf-8' });
regTable();

// The address: the first of X-Forwarded-For, else X-Real-IP, else the socket
const addresses = [
    { 'X-Forwarded-For': '203.0.113.5, 10.0.0.1' }, { 'X-Forwarded-For': '  198.51.100.7  ' }, { 'X-Forwarded-For': '' },
    { 'X-Forwarded-For': ',10.0.0.2' }, { 'X-Forwarded-For': ' , 10.0.0.3', 'X-Real-IP': '192.0.2.4' }, { 'X-Real-IP': '192.0.2.9' },
    { 'X-Real-IP': '  spaced  ' }, { 'X-Forwarded-For': 'not an address' }, { 'X-Forwarded-For': ' 198.51.100.8 ' },
    { 'X-Forwarded-For': '2001:db8::1' }, {},
];
addresses.forEach((headers, i) => req('', 'POST', `${bd}/register-request`, { device_id: `ADDR${i}` }, { headers, label: 'address' }));
sql('stored addresses', "SELECT device_id, ip_address FROM pending_registrations WHERE device_id LIKE 'ADDR%' ORDER BY id");

// Asking again: a pending request is updated, an approved one stays, a
// rejected one is pending again without its reason
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV2', hostname: 'renamed', platform: 'Linux' });
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV3', hostname: 'ignored' });
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV4', hostname: 'again' });
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV6' });
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV7', public_key: 'new-key' });
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV8' });
regTable();

// 3. The status a device polls
const statuses = [
    '', '?device_id=', '?device_id=DEV1', '?device_id=DEV3', '?device_id=DEV4', '?device_id=DEV7', '?device_id=DEV8', '?device_id=nope',
    '?device_id=DEV1&device_id=DEV2', '?device_id[]=DEV1', '?device_id[]=DEV1&device_id[]=DEV2', '?device_id[x]=DEV1', '?device_id=%20DEV1',
    '?device_id=dev1', '?device_id=NEW11', '?device_id=%F0%9F%98%80a', '?device_id=%E0%A4%A', '?DEVICE_ID=DEV1', `?device_id=${long('z', 3000)}`,
    '?device_id=0', '?device_id[0]=DEV3',
];
for (const q of statuses) req('', 'GET', `${bd}/register-status${q}`);
req('viewer', 'GET', '/register-status?device_id=DEV3', undefined, { label: 'with a session' });

// 4. The list with its filters
fixTimes();
const lists = [
    '', '?status=pending', '?status=approved', '?status=rejected', '?status=', '?status=nope', '?status=PENDING',
    '?status[]=pending', '?status=pending&status=approved', '?status[x]=pending', '?search=pc', '?search=PC', '?search=%25',
    '?search=_', '?search=100%25_', '?search=%C3%A9', '?search=%C3%89', '?search=%C3%A9cole', '?search=10.0.0', '?search=192.168',
    '?search=D', '?search=pc&status=pending', '?search[]=pc', '?search=pc&search=lap', '?search[a]=b', '?search=%5C',
    '?search=nothing-matches', '?search=%27%22', '?search=NEW3',
];
for (const q of lists) req('admin', 'GET', `/api/registrations${q}`);

// 5. One registration: ids in every spelling
for (const id of ['1', '01', '1abc', 'abc', '-1', '0', '1.9', '99999999999', '1e3', '%20%201', '0x10', long('9', 400), '%2B5', '6', '3']) {
    req('admin', 'GET', `/api/registrations/${id}`);
}

// 6. The count: what the API server's count adds to the LAN count
const pendingAnswer = (count) => ({ 'GET /api/enrollment/pending': json(200, { devices: [], count }) });
for (const count of [2, 0, '5', null, true, [3], {}, -1, 1.5, 'x', '']) {
    req('admin', 'GET', '/api/registrations/count', undefined, { api: pendingAnswer(count), label: `count ${JSON.stringify(count)}` });
}
req('admin', 'GET', '/api/registrations/count', undefined, { api: { 'GET /api/enrollment/pending': json(200, { devices: [] }) }, label: 'no count' });
req('admin', 'GET', '/api/registrations/count', undefined, { api: { 'GET /api/enrollment/pending': json(500, { error: 'down' }) }, label: 'API fails' });
req('admin', 'GET', '/api/registrations/count', undefined, { api: { 'GET /api/enrollment/pending': { hang: true } }, label: 'API hangs up' });
req('admin', 'GET', '/api/registrations/count', undefined, { api: { 'GET /api/enrollment/pending': json(200, [1, 2]) }, label: 'API list' });

// 7. The enrollment UI flags
const modes = [
    json(200, { mode: 'approval' }), json(200, { mode: '' }), json(200, {}), json(200, { mode: 5 }), json(200, { mode: ['a'] }),
    json(500, { error: 'x' }), json(200, null), text(200, 'open'),
];
modes.forEach((m, i) => req('admin', 'GET', '/api/registrations/enrollment-ui', undefined, { api: { 'GET /api/enrollment/mode': m }, label: `mode ${i}` }));
for (const [rich, tag] of [['0', 'false'], ['TRUE', 'Yes'], ['yes', '1'], ['no', ''], [' true', 'true '], ['1', null], [null, 'ON']]) {
    setting('enrollment_rich_approve', rich);
    setting('enrollment_tag_picker', tag);
    req('admin', 'GET', '/api/registrations/enrollment-ui');
}

// 8. Approving: a folder, device groups, the display name and the tags
req('admin', 'PUT', '/api/registrations/1/approve', {
    folder_id: 2, group_guids: ['dg-a', 'dg-tag', 'folder_1', 'bad guid!', 'DG-A', 5, 'dg-a', 'dg-b'],
    display_name: '  Kiosk  ', tags: ' a, ,b ,a',
});
regTable();
assignments();
members();
audit();
req('admin', 'PUT', '/api/registrations/1/approve', {}, { label: 'again' });
req('admin', 'PUT', '/api/registrations/4/approve', {}, { label: 'rejected' });
req('admin', 'PUT', '/api/registrations/999/approve', {}, { label: 'missing' });
req('admin', 'PUT', '/api/registrations/abc/approve', {}, { label: 'not a number' });
// As operator, who sees dg-b only: the groups it may see change
req('', 'POST', `${bd}/register-request`, { device_id: 'DEV2', hostname: 'pc-2' });
req('global_admin', 'PUT', '/api/registrations/2/approve', { folder_id: '1abc', group_guids: [], display_name: '', tags: '' }, { host: 'example.test' });
members();
assignments();
// DEV5: the API server refuses the peer update and the tags; the approval stands
req('admin', 'PUT', '/api/registrations/5/approve', { folder_id: 'x', group_guids: ['dg-c'], display_name: 'Five', tags: 'x,y' }, { host: '[::1]:5000' });
members();
// A body that is no object, odd folder ids, many groups, a device without a peer
const approvals = [
    { body: [1, 2], label: 'list body' },
    { body: { folder_id: 0, group_guids: 'dg-a' }, label: 'folder 0' },
    { body: { folder_id: -1 }, label: 'folder -1' },
    { body: { folder_id: 1e21 }, label: 'folder 1e21' },
    { body: { folder_id: 99 }, label: 'folder that does not exist' },
    { body: { folder_id: null, group_guids: Array.from({ length: 101 }, (_, i) => `g${i}`) }, label: '101 groups' },
    { body: { group_guids: Array.from({ length: 100 }, (_, i) => (i ? `g${i}` : 'dg-a')) }, label: '100 groups' },
    { body: { group_guids: [{ toString: 'x' }] }, label: 'group without text' },
    { body: { display_name: 5 }, label: 'display name a number' },
    { body: { tags: ['a'] }, label: 'tags a list' },
    { body: { display_name: '  ', tags: ' , ' }, label: 'blank' },
];
approvals.forEach((a, i) => {
    req('', 'POST', `${bd}/register-request`, { device_id: `APPR${i}`, hostname: `approve-${i}` }, { capture: { a: 'id' } });
    req('admin', 'PUT', '/api/registrations/{a}/approve', a.body, { label: a.label, host: i === 0 ? ':5000' : undefined });
});
req('', 'POST', `${bd}/register-request`, { device_id: 'APPR-form' }, { capture: { a: 'id' } });
req('admin', 'PUT', '/api/registrations/{a}/approve', 'folder_id=2', { raw: true, type: 'application/x-www-form-urlencoded', label: 'form' });
// The empty device id of a request that was only white space
fixTimes();
req('admin', 'GET', '/api/registrations?search=&status=pending');
regTable();
assignments();
members();
audit();

// 9. Rejecting
req('', 'POST', `${bd}/register-request`, { device_id: 'REJ1' }, { capture: { r: 'id' } });
for (const [body, label] of [
    [{ reason: 5 }, 'number'], [{ reason: ['a'] }, 'list'], [{ reason: { a: 1 } }, 'object'], [{ reason: true }, 'true'],
]) {
    req('admin', 'PUT', '/api/registrations/{r}/reject', body, { label });
}
req('operator', 'PUT', '/api/registrations/{r}/reject', { reason: 'x' }, { label: 'operator' });
req('admin', 'PUT', '/api/registrations/{r}/reject', { reason: `${long('r', 499)}😀tail` });
req('admin', 'PUT', '/api/registrations/{r}/reject', { reason: 'again' }, { label: 'again' });
req('admin', 'PUT', `${bd}/api/registrations/3/reject`, {}, { label: 'approved' });
req('admin', 'PUT', `${bd}/api/registrations/998/reject`, { reason: 5 }, { label: 'missing with a bad reason' });
for (const [body, label] of [[{ reason: 0 }, 'zero'], [{ reason: null }, 'null'], [{}, 'none'], [[], 'list body'], [{ reason: false }, 'false']]) {
    req('', 'POST', `${bd}/register-request`, { device_id: 'REJ2' }, { capture: { r: 'id' } });
    req('super_admin', 'PUT', '/api/registrations/{r}/reject', body, { label });
}
req('', 'GET', `${bd}/register-status?device_id=REJ2`);
regTable();
audit();

// 10. Deleting
req('admin', 'DELETE', '/api/registrations/4');
req('admin', 'DELETE', '/api/registrations/4', undefined, { label: 'again' });
req('server_admin', 'DELETE', '/api/registrations/8');
req('admin', 'DELETE', `${bd}/api/registrations/x`);
req('admin', 'DELETE', '/api/registrations/7abc');
regTable();

// 11. The enrollment queue of the API server
const enrollmentAnswers = [
    json(200, { devices: [{ device_id: 'E1' }], count: 1 }), json(200, { devices: null, count: null }), json(200, []),
    json(200, { count: 3 }), json(500, { error: 'down' }), text(200, 'nope'), { hang: true },
];
enrollmentAnswers.forEach((a, i) => req('admin', 'GET', '/api/enrollment/pending', undefined, { api: { 'GET /api/enrollment/pending': a }, label: `answer ${i}` }));
for (const q of ['', '?status=approved', '?status=rejected', '?status=', '?status[]=x', '?status=a&status=b', '?status[k]=v', '?status=%20']) {
    req('admin', 'GET', `/api/enrollment/history${q}`);
}
req('admin', 'GET', '/api/enrollment/history', undefined, { api: { 'GET /api/enrollment/history': json(400, { error: 'bad' }) }, label: 'refused' });

const enrollBodies = [
    [{}, 'empty'],
    [{ display_name: 'Lobby', sync_mode: 'full', tags: 'a,b', folder_id: 1, group_guids: ['dg-a', 'dg-c'] }, 'everything'],
    [{ sync_mode: 5, group_guids: ['x', null, ['y', 'z']] }, 'odd values'],
    [{ sync_mode: { toString: 'x' } }, 'sync mode without text'],
    [{ group_guids: [{ toString: 'x' }] }, 'groups without text'],
    [{ folder_id: '2x', group_guids: 'dg-a' }, 'folder text'],
    [[1], 'list body'],
];
for (const [body, label] of enrollBodies) {
    req('admin', 'POST', '/api/enrollment/approve/DEV1', body, { label });
    assignments();
    members();
}
req('admin', 'POST', '/api/enrollment/approve/E1', { folder_id: 1 });
req('admin', 'POST', '/api/enrollment/approve/E2', { folder_id: 1 }, { label: 'API refuses' });
req('admin', 'POST', '/api/enrollment/approve/E1', {}, { api: { 'POST /api/enrollment/approve/E1': json(200, { success: false, error: 'no' }) }, label: 'API says no' });
req('admin', 'POST', '/api/enrollment/approve/a%2Fb', {}, { label: 'slash in the id' });
req('global_admin', 'POST', `${bd}/api/enrollment/approve/DEV1`, { group_guids: ['dg-b'] });
members();
for (const ban of [true, 'yes', '0', 0, '', null, [], {}, undefined]) {
    req('admin', 'POST', '/api/enrollment/reject/E1', ban === undefined ? {} : { ban }, { label: `ban ${JSON.stringify(ban)}` });
}
req('admin', 'POST', '/api/enrollment/reject/E2', { ban: true }, { label: 'API refuses' });
req('admin', 'POST', '/api/enrollment/reject/E1', [true], { label: 'list body' });
req('admin', 'POST', '/api/enrollment/clear-rejection/E1');
req('admin', 'POST', '/api/enrollment/clear-rejection/E1', {});
req('admin', 'POST', '/api/enrollment/clear-rejection/E2', {});
req('admin', 'POST', '/api/enrollment/clear-rejection/E3', {}, { label: 'API refuses' });
req('admin', 'POST', '/api/enrollment/clear-rejection/E1', {}, { api: { 'POST /api/enrollment/clear-rejection/E1': json(200, { unbanned: 'yes' }) }, label: 'unbanned as text' });
req('admin', 'POST', '/api/enrollment/clear-rejection/E1', {}, { api: { 'POST /api/enrollment/clear-rejection/E1': json(200, null) }, label: 'no data' });
audit();

// 12. Help requests: the list, its names from the console's audit log,
// accepting and resolving at both mounts
req('viewer', 'GET', '/api/help/requests');
req('viewer', 'GET', '/api/help/requests?status=pending&device_id=DEV1');
req('viewer', 'GET', '/api/help/requests?status[]=a&status[]=b&device_id[x]=1');
req('viewer', 'GET', '/api/help/requests?status=&device_id=');
req('operator', 'POST', '/api/help/requests/21/accept', {});
req('viewer', 'GET', `${bd}/api/help/requests`, undefined, { label: 'the name of the accept at the other mount' });
req('admin', 'POST', `${bd}/api/help/requests/21/resolve`, {});
req('viewer', 'GET', '/api/help/requests', undefined, { api: { 'GET /api/help/requests': json(200, [{ id: 21, status: 'resolved', handled_by: 'apikey:console-bridge', created_at: '2001-03-01T00:00:30Z' }]) }, label: 'resolved' });
req('admin', 'POST', '/api/help/requests/021/accept', {}, { label: 'id with a zero' });
req('viewer', 'GET', '/api/help/requests', undefined, { label: 'after the accept of 021' });
req('admin', 'POST', '/api/help/requests/25/accept', {});
req('admin', 'POST', '/api/help/requests/x%20y/accept', {});
req('admin', 'POST', `/api/help/requests/${long('9', 50)}/accept`, {});
for (const [answer, label] of [
    [json(404, { error: 'not found' }), 'not found'], [json(400, { error: '' }), 'empty error'], [text(500, 'boom\n'), 'text'],
    [{ status: 204 }, 'no content'], [json(200, null), 'null'], [{ hang: true }, 'hangs up'],
]) {
    req('admin', 'POST', '/api/help/requests/26/accept', {}, { api: { 'POST /api/help/requests/26/acknowledge': answer }, label });
    req('admin', 'POST', '/api/help/requests/26/resolve', {}, { api: { 'POST /api/help/requests/26/resolve': answer }, label });
}
const helpAnswers = [
    [json(200, { requests: helpList.slice(0, 2) }), 'under requests'],
    [json(200, { requests: null }), 'requests null'],
    [json(200, {}), 'no list'],
    [json(200, { requests: { a: 1 } }), 'requests an object'],
    [json(200, 5), 'a number'],
    [json(500, { error: 'down' }), 'fails'],
    [json(200, [null, 5, 'x', [], {}, true]), 'odd entries'],
    [json(200, [
        { id: 31, status: 'constructor', created_at: '2001-05-01T00:00:00Z' },
        { id: 32, status: '__proto__', created_at: '2001-05-01T00:00:01Z' },
        { id: 33, status: 'toString', created_at: '2001-05-01T00:00:02Z' },
        { id: 34, status: 5, created_at: '2001-05-01T00:00:03Z' },
        { id: 35, status: ['acknowledged'], handled_by: ['apikey:a'], created_at: '2001-05-01T00:00:04Z' },
        { id: 36, status: '', device_id: 0, hostname: null, message: 5, created_at: '2001-05-01T00:00:05Z' },
        { id: [1, 2], status: 'resolved', handled_by: 'apikey:b', created_at: '2001-05-01T00:00:06Z' },
        { id: { a: 1 }, created_at: '2001-05-01T00:00:07Z' },
        { id: null, created_at: '2001-05-01T00:00:08.123456Z', updated_at: '2001-05-01T00:00:09.999+01:00' },
        { status: 'acknowledged', handled_by: 'apikey:c', created_at: '2001-05-01T00:00:07Z' },
        { id: 37, status: 'acknowledged', handled_by: 'APIKEY:no', created_at: '2001-05-01' },
        { id: 38, status: 'acknowledged', created_at: '2001-05-01T00:00:10' },
        { id: 39, status: 'resolved', handled_by: ' apikey:no', created_at: '2001-13-01T00:00:00Z' },
        { id: 40, created_at: 'nonsense', updated_at: '' },
        { id: 41, created_at: '2001-05-01T00:00:07Z', updated_at: 0 },
    ]), 'odd fields'],
    [json(200, [{ id: { toString: 'x' } }]), 'an id without text'],
];
for (const [answer, label] of helpAnswers) {
    req('viewer', 'GET', '/api/help/requests', undefined, { api: { 'GET /api/help/requests': answer }, label });
}
audit();

// 13. The notification view model on this database, with the API
// server's queue
fixTimes();
notifications('now');
notifications('API fails', { 'GET /api/enrollment/pending': json(500, { error: 'down' }) });
notifications('odd queue', {
    'GET /api/enrollment/pending': json(200, {
        devices: [null, { device_id: 'E9', created_at: 'nonsense' }, { device_id: 'E8', hostname: 7, created_at: '2001-01-01T00:04:30Z' }, { hostname: 'no id' }],
        count: 4,
    }),
});

// ---- Running ----

function pick(value, dotted) {
    return dotted.split('.').reduce((v, key) => (v && typeof v === 'object' ? v[key] : undefined), value);
}

function send(port, step, vars) {
    return new Promise((resolve, reject) => {
        const fill = (s) => s.replace(/\{([A-Za-z]+)\}/g, (_, name) => (name in vars ? encodeURIComponent(vars[name]) : '999999999'));
        const headers = { Accept: 'application/json' };
        if (step.role) headers['X-Oracle-Role'] = step.role;
        if (step.host) headers.Host = step.host;
        for (const [name, value] of Object.entries(step.headers || {})) headers[name] = value;
        let payload = null;
        if (step.body !== undefined) {
            payload = Buffer.from(step.body);
            headers['Content-Type'] = step.type || 'application/json';
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
    process.env.PUB_KEY_PATH = path.join(scratch, 'id_ed25519.pub');
    process.env.PORT = '5000';
    process.env.HTTPS_ENABLED = 'false';
    fs.writeFileSync(process.env.API_KEY_PATH, 'oracle-api-key');
    fs.writeFileSync(process.env.PUB_KEY_PATH, `${SERVER_KEY}\n`);
    const events = [];
    stub('services/goConsoleBridge', {
        learn: (_req, _res, next) => next(),
        publishPanelEvent: (data) => events.push(JSON.stringify(data)),
        relay: async () => null,
    });
    const db = await openDatabase();
    for (const [query, params] of seed) await db.exec(query, params);

    const express = web('node_modules/express');
    const app = express();
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use((r, _res, next) => {
        const user = USERS[r.headers['x-oracle-role']];
        r.session = user ? { userId: user.id, user: { ...user }, destroy() {} } : { destroy() {} };
        next();
    });
    app.use(web('middleware/auth').loadEffectivePermissions);
    app.use(web('services/panelAccessPolicyService').loadEffectivePolicy);
    app.use(web('middleware/panelAccess').panelAccessGuard);
    app.use(web('middleware/i18n').initI18n());
    const router = express.Router();
    router.use(web('lib/requireJsonContentType').requireJsonContentType);
    const routes = web('routes/registration.routes');
    router.use('/', routes);
    router.use('/api/bd', routes);
    app.use('/', router);
    app.use((r, res) => res.status(404).json({ oracle: 'no route' }));
    // The console's error handler, as it answers the API in production
    // eslint-disable-next-line no-unused-vars
    app.use((err, r, res, _next) => res.status(err.status || 500).json({ success: false, error: 'Internal Server Error' }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;
    const { listRegistrationNotifications } = web('services/registrationNotifications');
    const database = web('services/database');
    const betterdeskApi = web('services/betterdeskApi');

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
            events.length = 0;
            overrides = step.api || {};
            if (step.notifications) {
                const result = await listRegistrationNotifications(database, betterdeskApi);
                out.push({ ...step, items: JSON.stringify(result.items), count: result.count, calls: calls.map((c) => ({ ...c })) });
                continue;
            }
            const answer = await send(port, step, vars);
            // What the route started without waiting for
            await new Promise((resolve) => setTimeout(resolve, 20));
            if (step.capture) {
                let parsed = null;
                try { parsed = JSON.parse(answer.body); } catch (_) { /* not JSON */ }
                for (const [name, dotted] of Object.entries(step.capture)) {
                    const v = pick(parsed, dotted);
                    if (v !== undefined && v !== null) vars[name] = String(v);
                }
            }
            out.push({ ...step, status: answer.status, answer: answer.body, calls: calls.map((c) => ({ ...c })), events: [...events] });
        }
    } finally {
        console.error = realError;
        console.log = realLog;
        console.warn = realWarn;
        await new Promise((resolve) => server.close(resolve));
        apiServer.close();
        await db.close();
    }
    const file = path.join(__dirname, '..', `node_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, serverKey: SERVER_KEY, seed, api: API, steps: out }, null, 1)}\n`);
    realLog(`${file}: ${out.length} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
