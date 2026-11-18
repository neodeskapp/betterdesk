'use strict';

/**
 * Runs the Node console's device routes (routes/devices.routes.js) with the
 * real database adapter on a scratch database, in front of an API server
 * that answers from a table, and records every answer and every call the
 * routes made to the API server. See README.md.
 *
 *   node routes.js                               ../node_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node routes.js  ../node_postgres.json
 */

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'devices-oracle-'));
const schema = `oracle_dev_${process.pid}`;
const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

/** The accounts a step can act as. Ids as the Go test gives them. */
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

// ---- The API server: a table of answers by "METHOD url" ----

const A = {
    id: 'A1001', hostname: 'alpha', user: 'ann', os: 'Windows 11', version: '1.4.2', ip: '10.0.0.1', note: '',
    live_online: true, last_online: '2020-01-01T00:00:00Z', created_at: '2019-01-01T00:00:00Z',
    tags: ['lab', 'Office'], uuid: '11111111-2222-3333-4444-555555555555',
};
const B = {
    id: 'B1002', hostname: 'beta', user: '', os: 'Linux', version: '', banned: true, ban_reason: 'stolen',
    banned_at: '2020-02-02T10:00:00Z', tags: 'kiosk,lab', last_online: '2020-01-02T00:00:00Z',
};
const C = { id: 'C1003', hostname: '', os: '', version: '', note: 'fan loud', tags: [], last_online: '' };
const D = { id: 'D1004', hostname: 'delta', soft_deleted: true, deleted_at: '2020-03-03T00:00:00Z' };
const E = { id: 'E1005', hostname: 'echo', soft_deleted: true, deleted_at: '2020-04-04T00:00:00Z' };
const RT1 = {
    id: 'rt_1', name: 'VNC box', protocol: 'vnc', host: 'vnc.example.invalid', port: 5900, credential_mode: 'prompt',
    tls_mode: 'preferred', enabled: true, created_by: 'apikey:console-bridge', created_at: '2020-01-01T00:00:00Z',
};
const RT2 = { ...RT1, id: 'rt_2', name: 'RDP box', protocol: 'rdp', port: 3389, enabled: false };
const notFound = { status: 404, body: { error: 'not found' } };
const ok = (body, status = 200) => ({ status, body });

const TABLE = {
    'GET /api/peers': ok({ peers: [A, B, C] }),
    'GET /api/peers?include_deleted=true': ok({ peers: [A, B, C, D] }),
    'GET /api/remote-targets?org_id=&include_disabled=false': ok({ targets: [RT1] }),
    'GET /api/remote-targets?org_id=&include_disabled=true': ok({ targets: [RT1, RT2] }),
    'GET /api/peers/A1001': ok(A),
    'GET /api/peers/B1002': ok(B),
    'GET /api/peers/C1003': ok(C),
    'GET /api/remote-targets/rt_1': ok(RT1),
    'GET /api/remote-targets/rt_2': ok(RT2),
    'GET /api/peers/A1001/telemetry': ok({ device_id: 'A1001', schema: 1, snapshots: { services: { collected_at: '2020-01-01T00:00:00Z', data: [{ name: 'svc' }] } } }),
    'GET /api/peers/A1001/connection-mode': ok({ device_id: 'A1001', controllable: true, policy: { effective_mode: 'normal' }, command: null }),
    'GET /api/peers/B1002/connection-mode': ok({ device_id: 'B1002', controllable: false, can_change: 'kept in place', policy: null }),
    'GET /api/peers/A1001/availability?day=2020-01-01&tz_offset=-60': ok({ peer_id: 'A1001', day: '2020-01-01', events: [], count: 0 }),
    'GET /api/peers/A1001/availability?tz_offset=0': ok({ peer_id: 'A1001', day: 'today', events: [{ to: 'ONLINE' }] }),
    'GET /api/peers/A1001/connection-sessions?limit=3': ok({ peer_id: 'A1001', sessions: [{ id: 1 }], count: 1 }),
    'GET /api/peers/A1001/connection-sessions?limit=50': ok({ peer_id: 'A1001', sessions: [], count: 0 }),
    'GET /api/peers/A1001/connection-sessions?limit=1': ok({ peer_id: 'A1001', sessions: [{ id: 1 }], count: 1 }),
    'GET /api/peers/A1001/connection-sessions?limit=200': ok({ peer_id: 'A1001', sessions: [{ id: 2 }], count: 1 }),
    'POST /api/peers/A1001/telemetry/refresh': ok({ accepted: true, command_id: 7, device_id: 'A1001' }, 202),
    'POST /api/peers/C1003/telemetry/refresh': ok({ error: 'hardware refresh is rate limited' }, 429),
    'POST /api/peers/A1001/telemetry/command': ok({ accepted: true, command_id: 8 }, 202),
    'POST /api/peers/C1003/telemetry/command': ok({ error: 'command is not allowed' }, 400),
    'PATCH /api/peers/A1001': ok({ status: 'updated' }),
    'PATCH /api/peers/C1003': ok({ error: 'database is locked' }, 500),
    'DELETE /api/peers/A1001': ok({ status: 'deleted', id: 'A1001', cascaded: ['X'] }),
    'DELETE /api/peers/A1001?revoke=true&cascade=true': ok({ status: 'revoked' }),
    'DELETE /api/peers/A1001?hard=true': ok({ status: 'purged' }),
    'DELETE /api/peers/D1004?hard=true': ok({ status: 'purged' }),
    'DELETE /api/peers/B1002': ok({ error: 'delete failed in the server' }, 500),
    'POST /api/peers/A1001/restore': ok({ id: 'A1001', status: 'restored' }),
    'POST /api/peers/D1004/restore': ok({ id: 'D1004', status: 'restored' }),
    'POST /api/peers/ZZ9/restore': ok({ error: 'peer not found' }, 404),
    'POST /api/peers/A1001/ban': ok({ status: 'banned' }),
    'POST /api/peers/C1003/ban': ok({ error: 'database is locked' }, 500),
    'POST /api/peers/A1001/unban': ok({ status: 'unbanned' }),
    'POST /api/peers/A1001/change-id': ok({ status: 'changed', old_id: 'A1001' }),
    'PUT /api/peers/A1001/tags': ok({ status: 'ok' }),
    'PUT /api/peers/B1002/tags': ok({ status: 'ok' }),
    'PUT /api/peers/C1003/tags': ok({ error: 'tags not stored' }, 500),
    'POST /api/peers/A1001/connection-mode': ok({ command: { previous_mode: 'normal', revision: 3, command_id: 'cmd-1' }, policy: { effective_mode: 'incoming-only' } }),
    'POST /api/peers/C1003/connection-mode': ok({ error: 'capability_required' }, 409),
    'GET /api/peers/A1001/access-policy': ok({ peer_id: 'A1001', unattended_enabled: false, allowed_operators: '' }),
    'PUT /api/peers/A1001/access-policy': ok({ success: true }),
    'DELETE /api/peers/A1001/access-policy': ok({ success: true }),
    'GET /api/peers/A1001/connect-password': ok({ password_set: true, password: 'made-up-not-a-secret' }),
    'GET /api/peers/B1002/connect-password': ok({ password_set: false }, 404),
    'GET /api/peers/C1003/connect-password': ok({ error: 'forbidden' }, 403),
    'POST /api/remote-targets': ok({ ...RT1, id: 'rt_new' }, 201),
    'POST /api/remote-targets/test': ok({ error: 'remote target is unreachable' }, 502),
    'PATCH /api/remote-targets/rt_1': ok({ ...RT1, name: 'Renamed' }),
    'GET /api/remote-targets/rt_1/credentials': ok({ username: 'u', password_set: false, credential_mode: 'prompt' }),
    'PUT /api/remote-targets/rt_1/credentials': ok({ error: 'remote target credential vault is not configured' }, 503),
    'DELETE /api/remote-targets/rt_1/credentials': ok({ status: 'ok', credential_mode: 'prompt' }),
    'POST /api/remote-targets/rt_1/certificate/accept': ok({ error: 'invalid certificate fingerprint' }, 400),
    'DELETE /api/remote-targets/rt_1': ok({ status: 'deleted', id: 'rt_1' }),
    'POST /api/strategies/assign': ok({ status: 'ok' }),
    'GET /api/strategies/remote-only': ok({ guid: 'remote-only', name: 'From the server', enabled: true, permissions: '{"enable-audio":"Y"}' }),
    'DELETE /api/strategies/s-two': ok({ error: 'strategy not found' }, 404),
};

const apiState = { override: {}, calls: [] };

function startAPI() {
    const server = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            const key = `${req.method} ${req.url}`;
            apiState.calls.push(key + (chunks.length ? ' ' + Buffer.concat(chunks).toString('utf8') : ''));
            const a = apiState.override[key] || TABLE[key] || notFound;
            if (a.hangup) {
                req.socket.destroy();
                return;
            }
            res.writeHead(a.status, a.text === undefined ? { 'Content-Type': 'application/json' } : { 'Content-Type': 'text/plain' });
            res.end(a.text !== undefined ? a.text : JSON.stringify(a.body));
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
        await admin.query(`CREATE SCHEMA ${schema}`);
        const url = new URL(PG_DSN);
        url.searchParams.set('options', `-c search_path=${schema}`);
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
        if (admin) { await admin.query(`DROP SCHEMA ${schema} CASCADE`); await admin.end(); }
    };
    return { exec, close };
}

// The rows every run starts from. Times are given so both databases read
// them the same way.
const t = (sqlite, postgres) => (pg ? postgres : sqlite);
const seed = [
    // Node's PostgreSQL start looks for columns in every schema, so on a
    // shared server it can skip columns this schema lacks
    ...(pg ? [
        ["ALTER TABLE users ADD COLUMN IF NOT EXISTS guid TEXT DEFAULT ''", []],
        ["ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS source_type TEXT DEFAULT 'manual'", []],
        ["ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS tag_filter TEXT DEFAULT ''", []],
    ] : []),
    ['DELETE FROM users', []],
    ...Object.values(USERS).map((u) => ['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [u.id, u.username, 'x', u.role]]),
    ["UPDATE users SET guid = 'guid-viewer' WHERE id = 6", []],
    ["UPDATE users SET guid = '' WHERE id <> 6", []],
    // The server's own device table, as far as the routes read it
    ["CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')", []],
    // A random guid sorts before this uuid and before guid-c, whatever it is
    ["INSERT INTO peers (id, uuid, guid) VALUES ('A1001', 'FFFFFFFF-FFFF-4FFF-BFFF-FFFFFFFFFFFF', '')", []],
    ["INSERT INTO peers (id, uuid, guid) VALUES ('B1002', 'not-a-uuid', '')", []],
    ["INSERT INTO peers (id, uuid, guid) VALUES ('C1003', '', 'guid-c')", []],
    // Groups: the default group the database starts with (under a fixed
    // guid, so the made-up server can answer for it), one open, one only
    // e2e_viewer may see, a tag group, one of a user group with
    // e2e_operator in it, and a folder's group
    ["UPDATE device_groups SET guid = 'g-default'", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-open', 'Open group', '', '', 'manual', '')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-viewer', 'Viewer only', 'n', 'team-1', 'manual', '')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-lab', 'Lab tag', '', '', 'tag', 'LAB')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-ops', 'Ops', '', '', 'manual', '')", []],
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('folder_5', 'Folder five', 'BetterDesk folder access scope', '', 'manual', '')", []],
    ["INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid IN ('g-viewer', 'folder_5')", []],
    ["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-ops', 'Operators', '', '')", []],
    ["INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 5 FROM user_groups WHERE guid = 'ug-ops'", []],
    ["INSERT INTO device_group_user_group_access (device_group_id, user_group_id) SELECT d.id, u.id FROM device_groups d, user_groups u WHERE d.guid = 'g-ops' AND u.guid = 'ug-ops'", []],
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'C1003' FROM device_groups WHERE guid = 'g-viewer'", []],
    // E1005 is a deleted device in the viewer's group (the server lists it
    // with the deleted ones where a step says so)
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'E1005' FROM device_groups WHERE guid = 'g-viewer'", []],
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'A1001' FROM device_groups WHERE guid = 'g-ops'", []],
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'A1001' FROM device_groups WHERE guid = 'g-open'", []],
    ["INSERT INTO folders (id, name, color, icon, sort_order) VALUES (5, 'Five', '#fff', 'folder', 0)", []],
    ["INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('B1002', 5)", []],
    // System information and metrics of A1001
    [`INSERT INTO peer_sysinfo (peer_id, hostname, username, platform, version, cpu_name, cpu_cores, cpu_freq_ghz, memory_gb, os_full, displays, encoding, features, platform_additions, raw_json, updated_at)
      VALUES ('A1001', 'alpha-host', 'ann', 'Windows', '1.4.2', 'Ryzen', 8, 3.7, 15.5, 'Windows 11 Pro', '[{"w":1920}]', '["h264"]', '{"privacy":true}', '{"b":1,"a":2}', '{}', ?)`, [t('2020-01-01 00:00:00', '2020-01-01T00:00:00Z')]],
    ['INSERT INTO peer_metrics (peer_id, cpu_usage, memory_usage, disk_usage, created_at) VALUES (?, ?, ?, ?, ?)', ['A1001', 12.3, 45.6, 0.1, t('2020-01-01 00:01:00', '2020-01-01T00:01:00Z')]],
    ['INSERT INTO peer_metrics (peer_id, cpu_usage, memory_usage, disk_usage, created_at) VALUES (?, ?, ?, ?, ?)', ['A1001', 99.9, 1, 2.5, t('2020-01-01 00:02:00', '2020-01-01T00:02:00Z')]],
    // The console's entry of the ban of B1002, a minute after the ban
    ['INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (1, ?, ?, ?, ?)', ['device_banned', 'Device B1002 banned: stolen', '127.0.0.1', t('2020-02-02 10:01:00', '2020-02-02T10:01:00Z')]],
    // Two rules, one assigned
    ['INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
        ['s-one', 'Rule one', '', 'g-open', pg ? false : 0, '{"enable-audio":"Y","1":"N"}', t('2020-01-01 00:00:00', '2020-01-01T00:00:00Z'), t('2020-01-01 00:00:00', '2020-01-01T00:00:00Z')]],
    ['INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
        ['s-two', 'Alpha rule', 'ug-ops', '', pg ? true : 1, pg ? '"a text"' : 'not json', t('2020-01-02 00:00:00', '2020-01-02T00:00:00Z'), t('2020-01-02 00:00:00', '2020-01-02T00:00:00Z')]],
    ["INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('peer', 'guid-c', 's-one')", []],
    ["INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('user', 'guid-viewer', 's-one')", []],
    ["INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('user', 'guid-nobody', 's-one')", []],
];

// ---- The requests ----

const steps = [];
const step = (role, method, p, body, api) => steps.push({
    name: `${method} ${p}${body === undefined ? '' : ' ' + body} as ${role}`, role, method, path: p,
    ...(body === undefined ? {} : { body }), ...(api ? { api } : {}),
});
const get = (role, p, api) => step(role, 'GET', p, undefined, api);
const send = (role, method, p, body, api) => step(role, method, p, JSON.stringify(body === undefined ? {} : body), api);
const sql = (name, query) => steps.push({ name, sql: query, params: [] });
// A request that may reach the end of the app: its 404 is the oracle's own,
// so only the status and the calls are compared
const loose = (role, method, p, body) => {
    step(role, method, p, body === undefined ? undefined : JSON.stringify(body));
    steps[steps.length - 1].statusOnly = true;
};
const long = 'x'.repeat(600);
const readers = ['admin', 'operator', 'viewer', 'guest_issuer'];
// The answers of the API server's panel grouping (/api/panel/...)
const panel = (status, body) => ({ status, body });
const hangup = { hangup: true };
const peersOf = (guid, peers) => ok({ guid, peers, total: peers.length });

// The list
for (const role of readers) get(role, '/api/devices?sortBy=id&sortOrder=asc');
for (const q of ['', '?sortBy=hostname&sortOrder=DESC', '?search=ALP', '?search=fan&sortBy=note', '?search=', '?search[]=x', '?search[a]=1',
    '?status=online', '?status=offline', '?status=banned', '?status[]=banned', '?hasNotes=true', '?hasNotes=TRUE', '?includeDeleted=true&sortBy=id',
    '?sortOrder[]=asc', '?sortOrder[x]=1', '?sortOrder=', '?sortBy=password&sortOrder=asc', '?sortBy[]=id', '?sortBy=version&sortOrder=asc',
    '?sortBy=created_at', '?sortBy=os&sortOrder=ASC', '?sortBy=username', '?page=2&limit=1&sortBy=id']) {
    get('admin', '/api/devices' + q);
}
get('operator', '/api/devices?includeDeleted=true');
get('admin', '/api/devices?sortBy=id', { 'GET /api/peers': { status: 500, body: { error: 'down' } } });
get('admin', '/api/devices?sortBy=id', { 'GET /api/peers': { status: 200, text: 'not json' } });
get('admin', '/api/devices?sortBy=id', { 'GET /api/peers': ok([A, { id: 'E1005', tags: '["json","list"]', live_online: true }]) });

// One device
for (const role of readers) get(role, '/api/devices/A1001');
get('admin', '/api/devices/B1002');
get('viewer', '/api/devices/B1002');
get('admin', '/api/devices/C1003');
get('operator', '/api/devices/C1003');
get('admin', '/api/devices/D1004');
get('admin', '/api/devices/rt_1');
get('admin', '/api/devices/rt_nope');
get('admin', '/api/devices/ZZ9');
get('admin', '/api/devices/A1001', { 'GET /api/peers/A1001/telemetry': { status: 500, body: { error: 'x' } }, 'GET /api/peers/A1001/connection-mode': ok('text mode') });
get('admin', '/api/devices/A1001', { 'GET /api/peers/A1001/connection-mode': ok([1, 2]) });
get('admin', '/api/devices/A1001', { 'GET /api/peers/A1001/connection-mode': ok({ error: 'not supported' }) });
get('admin', '/api/devices/B1002', { 'GET /api/peers/B1002': ok({ ...B, banned_at: null }) });
get('admin', '/api/devices/B1002', { 'GET /api/peers/B1002': ok({ ...B, banned_at: '2020-02-02T11:00:00Z' }) });
get('admin', '/api/devices/B1002', { 'GET /api/peers/B1002': ok({ ...B, banned_at: 'not a time' }) });
get('admin', '/api/devices/B1002', { 'GET /api/peers/B1002': ok({ ...B, banned_at: '2020-02-02 10:03:00' }) });

// Availability, sessions, telemetry
get('admin', '/api/devices/A1001/availability?day=2020-01-01&tz_offset=-60');
get('admin', '/api/devices/A1001/availability');
get('admin', '/api/devices/A1001/availability?day[]=x&tz_offset=');
get('admin', '/api/devices/A1001/availability?tz_offset[]=1&tz_offset[]=2');
get('admin', '/api/devices/A1001/availability?day=bad', { 'GET /api/peers/A1001/availability?day=bad&tz_offset=0': ok({ error: 'Invalid day; expected YYYY-MM-DD' }, 400) });
get('admin', '/api/devices/A1001/availability?day=x', { 'GET /api/peers/A1001/availability?day=x&tz_offset=0': { status: 500 } });
get('admin', '/api/devices/A1001/availability?day=null', { 'GET /api/peers/A1001/availability?day=null&tz_offset=0': ok(null) });
get('admin', '/api/devices/ZZ9/availability');
get('operator', '/api/devices/C1003/availability');
for (const q of ['?limit=3', '', '?limit=abc', '?limit=0', '?limit=-2', '?limit[]=3', '?limit=3x', '?limit=1', '?limit=200', '?limit=201',
    '?limit=99999', '?limit=1e3', '?limit=' + '9'.repeat(400), '?limit=%207', '?limit=0x10', '?limit=-0', '?limit=2.9', '?limit[a]=5',
    '?limit=7&limit=500', '?limit=', '?limit=Infinity']) {
    get('admin', '/api/devices/A1001/connection-sessions' + q);
}
get('admin', '/api/devices/A1001/connection-sessions?limit=9', { 'GET /api/peers/A1001/connection-sessions?limit=9': ok({ error: 'no sessions table' }) });
send('admin', 'POST', '/api/devices/A1001/telemetry/refresh');
send('admin', 'POST', '/api/devices/C1003/telemetry/refresh');
send('admin', 'POST', '/api/devices/ZZ9/telemetry/refresh');
send('operator', 'POST', '/api/devices/C1003/telemetry/refresh');
for (const body of [{ command: 'collect.services', args: { limit: 5 } }, { command: 5, args: 'x' }, { args: [1] }, { command: 'x', args: null }]) {
    send('admin', 'POST', '/api/devices/A1001/telemetry/command', body);
}
send('admin', 'POST', '/api/devices/C1003/telemetry/command', { command: 'format' });

// Remote targets
for (const role of ['admin', 'operator', 'viewer']) get(role, '/api/remote-targets');
get('admin', '/api/remote-targets?include_disabled=true');
send('admin', 'POST', '/api/remote-targets', { name: 'x', protocol: 'vnc', host: 'h.example.invalid', port: 1.0, nested: { b: 1, a: 2 } });
send('admin', 'POST', '/api/remote-targets', [1, 2]);
send('admin', 'POST', '/api/remote-targets', {}, { 'POST /api/remote-targets': ok({ error: 'name is required' }, 400) });
send('operator', 'POST', '/api/remote-targets/test', { host: 'h.example.invalid' });
get('admin', '/api/remote-targets/rt_1');
get('admin', '/api/remote-targets/rt_nope');
send('admin', 'PATCH', '/api/remote-targets/rt_1', { name: 'Renamed', port: '5901' });
send('admin', 'PATCH', '/api/remote-targets/rt_nope', { name: 'x' });
get('admin', '/api/remote-targets/rt_1/credentials');
get('admin', '/api/remote-targets/rt_1/credentials', { 'GET /api/remote-targets/rt_1/credentials': { status: 500, text: 'boom' } });
send('admin', 'PUT', '/api/remote-targets/rt_1/credentials', { username: 'u', password: 'made-up-not-a-secret' });
send('admin', 'DELETE', '/api/remote-targets/rt_1/credentials');
for (const body of [{ fingerprint: 'AB:CD' }, { fingerprint: ['ab', 'cd'] }, { fingerprint: 0 }, {}, { fingerprint: { toString: 1 } }]) {
    send('admin', 'POST', '/api/remote-targets/rt_1/certificate/accept', body);
}
send('admin', 'DELETE', '/api/remote-targets/rt_1');
send('admin', 'DELETE', '/api/remote-targets/rt_nope');

// Tags
for (const role of readers) get(role, '/api/tags');
for (const body of [{}, { from: ' ', to: 'x' }, { from: 'lab', to: 'lab' }, { from: ['lab'], to: 'x' }, { from: 5, to: 6 }, { from: 'nobody-has', to: 'y' },
    { from: 'LAB', to: 'lab-2' }]) {
    send('admin', 'POST', '/api/tags/rename', body);
}
send('operator', 'POST', '/api/tags/rename', { from: 'lab-2', to: 'lab' });
send('admin', 'POST', '/api/tags/rename', { from: 'lab', to: 'lab-3' }, { 'PUT /api/peers/A1001/tags': ok({ error: 'not stored' }, 500) });
for (const body of [{}, { tag: '' }, { tag: 42 }, { tag: 'Office' }, { tag: 'fan' }]) send('admin', 'POST', '/api/tags/delete', body);
send('operator', 'POST', '/api/tags/delete', { tag: 'kiosk' });
for (const body of [{ tags: 'a,b' }, { tags: Array.from({ length: 21 }, (_, i) => 't' + i) }, { tags: [' a ', '', 5, null, 'b', '  ', 'c'.repeat(60), 'd\u{1F600}'.repeat(30)] },
    { tags: [] }, {}, { tags: Array.from({ length: 25 }, (_, i) => (i % 2 ? ' ' : 'k' + i)) }]) {
    send('admin', 'PUT', '/api/devices/A1001/tags', body);
}
send('admin', 'PUT', '/api/devices/C1003/tags', { tags: ['x'] });
send('admin', 'PUT', '/api/devices/ZZ9/tags', { tags: ['x'] });
send('operator', 'PUT', '/api/devices/C1003/tags', { tags: ['x'] });
send('admin', 'PUT', '/api/devices/A1001/tags', { tags: ['x'] }, { 'PUT /api/peers/A1001/tags': ok({ error: { code: 7 } }, 400) });
send('admin', 'PUT', '/api/devices/A1001/tags', { tags: ['x'] }, { 'PUT /api/peers/A1001/tags': { status: 500 } });

// Device groups
for (const role of readers) get(role, '/api/device-groups');

// Members through the API server's panel grouping: the route reads the
// members of every manual group the account may see, then writes each one
// back with the device in the selected group only. Where a read gives no
// list, or a write gets 404, 501, 503 or no answer, the console's own
// writer takes over (B1002 is in no manual group, so its writes change
// nothing); any other refusal is the answer.
const membersRead = (extra = {}) => ({
    'GET /api/panel/device-groups/g-default/peers': peersOf('g-default', []),
    'PUT /api/panel/device-groups/g-default/peers': peersOf('g-default', []),
    'GET /api/panel/device-groups/g-open/peers': peersOf('g-open', ['A1001', 'B1002', 'A1001']),
    'GET /api/panel/device-groups/g-viewer/peers': peersOf('g-viewer', ['C1003']),
    'GET /api/panel/device-groups/g-ops/peers': peersOf('g-ops', []),
    'PUT /api/panel/device-groups/g-open/peers': peersOf('g-open', ['B1002']),
    'PUT /api/panel/device-groups/g-viewer/peers': peersOf('g-viewer', ['C1003']),
    'PUT /api/panel/device-groups/g-ops/peers': peersOf('g-ops', ['A1001']),
    ...extra,
});
send('admin', 'PUT', '/api/devices/A1001/groups', { groupGuids: ['g-ops'] }, membersRead());
send('operator', 'PUT', '/api/devices/A1001/groups', { groupGuids: ['g-open'] }, membersRead());
send('admin', 'PUT', '/api/devices/A1001/groups', { groupGuids: ['g-viewer'] }, membersRead({
    'GET /api/panel/device-groups/g-open/peers': peersOf('g-open', [5, 'A1001', 5, null, { a: 1 }, 'B1002', 'A1001']),
    'GET /api/panel/device-groups/g-viewer/peers': peersOf('g-viewer', []),
}));
for (const extra of [
    { 'GET /api/panel/device-groups/g-ops/peers': panel(404, { error: 'not found' }) },
    { 'GET /api/panel/device-groups/g-viewer/peers': panel(501, { error: 'not implemented' }) },
    { 'GET /api/panel/device-groups/g-open/peers': hangup },
    { 'GET /api/panel/device-groups/g-open/peers': ok({ guid: 'g-open', peers: 'B1002' }) },
    { 'GET /api/panel/device-groups/g-open/peers': ok(null) },
    { 'GET /api/panel/device-groups/g-open/peers': panel(500, { error: 'boom' }) },
    { 'GET /api/panel/device-groups/g-viewer/peers': panel(401, { error: 'Invalid or missing credentials' }) },
    { 'PUT /api/panel/device-groups/g-viewer/peers': panel(503, { error: 'unavailable' }) },
    { 'PUT /api/panel/device-groups/g-open/peers': panel(400, { error: 'invalid JSON' }) },
    { 'PUT /api/panel/device-groups/g-viewer/peers': panel(500, { error: 'SetDeviceGroupPeers' }) },
    { 'PUT /api/panel/device-groups/g-open/peers': ok(null) },
    { 'PUT /api/panel/device-groups/g-ops/peers': ok({ error: 'locked' }) },
    { 'PUT /api/panel/device-groups/g-ops/peers': ok({ error: 0 }) },
    { 'PUT /api/panel/device-groups/g-ops/peers': ok({ error: 'request timeout' }) },
    { 'PUT /api/panel/device-groups/g-ops/peers': ok({ error: { toString: 1 } }) },
    { 'PUT /api/panel/device-groups/g-open/peers': panel(403, { error: 'Forbidden' }) },
    { 'PUT /api/panel/device-groups/g-viewer/peers': hangup },
    { 'PUT /api/panel/device-groups/g-open/peers': { status: 200, text: 'saved' } },
]) {
    send('admin', 'PUT', '/api/devices/B1002/groups', { groupGuids: [] }, membersRead(extra));
}
sql('members after the panel writes', 'SELECT g.guid, m.peer_id FROM device_group_members m JOIN device_groups g ON g.id = m.device_group_id ORDER BY g.guid, m.peer_id');

// Deleting through the API server; the made-up server leaves the row, so
// g-ops stays for the steps after these
for (const answer of [ok({ guid: 'g-ops', deleted: true }), panel(400, { error: 'guid is required' }), panel(403, { error: 'Forbidden' }),
    panel(500, { error: 'DeleteDeviceGroup' }), ok({ error: 'locked' }), ok({ error: '' }), ok(null), { status: 200, text: '' }]) {
    send('admin', 'DELETE', '/api/device-groups/g-ops', undefined, { 'DELETE /api/panel/device-groups/g-ops': answer });
}
send('operator', 'DELETE', '/api/device-groups/g-ops', undefined, { 'DELETE /api/panel/device-groups/g-ops': ok({ guid: 'g-ops', deleted: true }) });

// Saving through the API server: its answer is the route's, with the
// API server's status for a refusal (502 without one)
const goGroup = { name: 'Go group', note: ' n ', team_id: 't', allowed_users: ['e2e_viewer', 'e2e_viewer'], allowed_groups: ['ug-ops'] };
const saved = { guid: 'g-go', name: 'Go group', source_type: 'manual', tag_filter: '', allowed_users: ['e2e_viewer'], allowed_groups: ['ug-ops'], created: true };
for (const answer of [panel(201, saved), panel(400, { error: 'name is required' }), panel(403, { error: 'Forbidden' }), panel(500, { error: 'boom' }),
    ok({ error: 'name is required' }), ok({ error: 0 }), ok({ error: { toString: 1 } }), ok([1]), { status: 200, text: 'saved' },
    ok({ name: 5 }), ok({}), ok({ name: { toString: 1 } }), panel(409, null), { status: 500 }]) {
    send('admin', 'POST', '/api/device-groups', goGroup, { 'POST /api/panel/device-groups': answer });
}
send('operator', 'POST', '/api/device-groups', { name: 'By operator via Go' }, { 'POST /api/panel/device-groups': panel(201, { ...saved, name: 'By operator via Go' }) });
send('admin', 'POST', '/api/device-groups', { guid: 'g-open', name: 'Open via Go', source_type: 'tag', tag: 'lab' },
    { 'POST /api/panel/device-groups': ok({ guid: 'g-open', name: 'Open via Go', source_type: 'tag', tag_filter: 'lab', allowed_users: [], allowed_groups: [], created: false }) });
// These fall back to the console's own writer, which creates the group
for (const answer of [ok({ error: 'socket timeout' }), panel(501, { error: 'x' }), panel(503, { error: 'x' }), ok(null), hangup, { status: 200, text: '' }]) {
    send('admin', 'POST', '/api/device-groups', { name: 'Fallback group' }, { 'POST /api/panel/device-groups': answer });
}

for (const body of [{}, { name: '  ' }, { name: 'T', dynamic: true }, { name: 'T', source_type: 'tag', tag: ' kiosk ' }, { name: 'G', allowed_groups: ['bad guid!'] },
    { name: 'G', guid: 'bad guid!' }, { name: 'G', guid: 'FOLDER_5' }, { name: 'G', guid: 'no-such' }, [1],
    { name: ['a', 'b'], note: 5, team_id: null, allowed_users: 'e2e_viewer, e2e_operator ,x', allowed_groups: [{ guid: 'ug-ops' }, 'ug-ops', 'missing'] },
    { name: 'Open group 2', guid: 'g-open', note: ' kept ' }, { name: 'Open group 3', guid: 'g-open', source_type: 'tag', tag_filter: 'lab', allowed_users: [] },
    { name: 'Open group', guid: 'g-open', source_type: 'manual', allowed_groups: ['ug-ops'] }]) {
    send('admin', 'POST', '/api/device-groups', body);
}
send('operator', 'POST', '/api/device-groups', { name: 'By operator', allowed_users: ['e2e_operator'] });
get('admin', '/api/device-groups');
for (const g of ['bad$guid', 'no-such', 'folder_5', 'g-lab']) send('admin', 'DELETE', '/api/device-groups/' + g);
for (const role of ['admin', 'operator', 'viewer']) get(role, '/api/devices/A1001/groups');
get('admin', '/api/devices/C1003/groups');
get('operator', '/api/devices/C1003/groups');
get('admin', '/api/devices/ZZ9/groups');
for (const body of [{ groupGuids: 'g-open' }, { groupGuids: ['bad guid!'] }, { groupGuids: Array(101).fill('g') }, { groupGuids: ['g-open', 'g-ops'] },
    { groupGuids: ['g-ops', 'g-ops', 'no-such', 'g-lab'] }, { groupGuids: [null, 5] }, {}]) {
    send('admin', 'PUT', '/api/devices/A1001/groups', body);
}
send('operator', 'PUT', '/api/devices/A1001/groups', { groupGuids: ['g-ops', 'g-viewer'] });
send('operator', 'PUT', '/api/devices/C1003/groups', { groupGuids: [] });
send('admin', 'PUT', '/api/devices/ZZ9/groups', { groupGuids: [] });
send('admin', 'PUT', '/api/devices/A1001/groups', { groupGuids: ['g-open'] });
get('admin', '/api/devices/A1001/groups');
get('admin', '/api/devices/A1001');
sql('members', 'SELECT g.guid, m.peer_id FROM device_group_members m JOIN device_groups g ON g.id = m.device_group_id ORDER BY g.guid, m.peer_id');

// Changing a device
for (const body of [{ note: '  N ' + long, user: 5, display_name: null }, {}, { note: [1, [2, null]] }, { note: { toString: 1 } }, [1], { display_name: 'D'.repeat(200) }]) {
    send('admin', 'PATCH', '/api/devices/A1001', body);
}
send('admin', 'PATCH', '/api/devices/C1003', { note: 'x' });
send('admin', 'PATCH', '/api/devices/ZZ9', { note: 'x' });
send('operator', 'PATCH', '/api/devices/C1003', { note: 'x' });
for (const body of [{}, { display_name: '   ' }, { display_name: 0 }, { display_name: ' New name ' + long }, { display_name: ['a', 'b'] }]) {
    send('admin', 'POST', '/api/devices/A1001/rename', body);
}
send('admin', 'POST', '/api/devices/C1003/rename', { display_name: 'x' });
send('admin', 'POST', '/api/devices/ZZ9/rename', { display_name: 'x' });

// Delete and restore
send('admin', 'DELETE', '/api/devices/A1001?revoke=true&cascade=true');
send('admin', 'DELETE', '/api/devices/A1001?hard=true');
send('admin', 'DELETE', '/api/devices/A1001');
send('admin', 'DELETE', '/api/devices/D1004?hard=true');
send('admin', 'DELETE', '/api/devices/D1004');
send('admin', 'DELETE', '/api/devices/B1002');
send('admin', 'DELETE', '/api/devices/ZZ9?hard=true');
send('admin', 'DELETE', '/api/devices/A1001?revoke=TRUE&hard=1');
sql('cleared', 'SELECT peer_id FROM peer_sysinfo UNION ALL SELECT peer_id FROM peer_metrics');
for (const id of ['A1001', 'D1004', 'ZZ9']) send('admin', 'POST', `/api/devices/${id}/restore`);
send('admin', 'POST', '/api/devices/A1001/restore', {}, { 'POST /api/peers/A1001/restore': ok({}, 500) });
send('admin', 'POST', '/api/devices/A1001/restore', {}, { 'POST /api/peers/A1001/restore': { status: 500 } });
send('admin', 'POST', '/api/devices/A1001/restore', {}, { 'POST /api/peers/A1001/restore': ok('') });
for (const body of [{ ids: 'A1001' }, { ids: [] }, { ids: ['A1001', 'ZZ9'] }, { ids: [0] }, { ids: [null] }, { ids: ['A1001', 'B1002'] }, { ids: ['rt_1'] }]) {
    send('admin', 'POST', '/api/devices/bulk-delete', body);
}

// Ban
for (const body of [{ reason: '  Lost ' + long }, {}, { reason: ['x'] }, { reason: 5 }]) send('admin', 'POST', '/api/devices/A1001/ban', body);
send('admin', 'POST', '/api/devices/C1003/ban', {});
send('admin', 'POST', '/api/devices/ZZ9/ban', {});
send('admin', 'POST', '/api/devices/A1001/unban');
send('admin', 'POST', '/api/devices/C1003/unban', {}, { 'POST /api/peers/C1003/unban': { status: 500 } });

// A new id
for (const newId of [undefined, '', 'short', 'x'.repeat(17), 'bad id!', 1234567, true, false, ['a', 'b', 'c', 'd', 'e', 'f'], ['abcdef'],
    { length: 8 }, { length: '7' }, { length: null }, { length: true }, { a: 1 }, 'B1002', 'D1004', 'NEW-ID_1', 'rt_target1', '\u{1F600}'.repeat(4)]) {
    send('admin', 'POST', '/api/devices/A1001/change-id', newId === undefined ? {} : { newId });
}
send('admin', 'POST', '/api/devices/ZZ9/change-id', { newId: 'NEW-ID_2' });
send('admin', 'POST', '/api/devices/A1001/change-id', { newId: 'NEW-ID_3' }, { 'POST /api/peers/A1001/change-id': ok({ error: 'Device ID already exists' }, 409) });
send('admin', 'POST', '/api/devices/A1001/change-id', { newId: 'NEW-ID_4' }, { 'POST /api/peers/A1001/change-id': ok({ error: 'This ID belongs to a deleted device. Restore or permanently delete that device before reusing the ID.' }, 409) });
send('admin', 'POST', '/api/devices/A1001/change-id', { newId: 'NEW-ID_5' }, { 'POST /api/peers/A1001/change-id': ok({ error: { why: 'locked' } }, 500) });
send('admin', 'POST', '/api/devices/A1001/change-id', { newId: 'NEW-ID_6' }, { 'POST /api/peers/A1001/change-id': { status: 500 } });
send('admin', 'POST', '/api/devices/A1001/change-id', { newId: 'NEW-ID_7' }, { 'POST /api/peers/A1001/change-id': ok({ error: 'gone: deleted device' }, 409) });

// Policies, password, connection mode
get('admin', '/api/devices/A1001/access-policy');
get('admin', '/api/devices/ZZ9/access-policy');
get('operator', '/api/devices/C1003/access-policy');
send('admin', 'PUT', '/api/devices/A1001/access-policy', { unattended_enabled: true, allowed_operators: 'e2e_admin', n: 1.50 });
send('admin', 'PUT', '/api/devices/A1001/access-policy', [1, 2]);
send('admin', 'PUT', '/api/devices/C1003/access-policy', {});
send('admin', 'DELETE', '/api/devices/A1001/access-policy');
for (const id of ['A1001', 'B1002', 'C1003', 'ZZ9']) get('admin', `/api/devices/${id}/connect-password`);
get('admin', '/api/devices/A1001/connect-password', { 'GET /api/peers/A1001/connect-password': { status: 500, body: { error: 'x' } } });
get('admin', '/api/devices/A1001/connect-password', { 'GET /api/peers/A1001/connect-password': ok(null) });
for (const body of [{ mode: 'incoming-only', reason: ' Support ' + long }, { mode: 'outgoing', reason: 'x' }, { mode: ' normal ', reason: '  ' }, { mode: 5, reason: 'x' }, { mode: 'normal' }]) {
    send('operator', 'POST', '/api/devices/A1001/connection-mode', body);
}
send('admin', 'POST', '/api/devices/C1003/connection-mode', { mode: 'normal', reason: 'x' });
send('admin', 'POST', '/api/devices/A1001/connection-mode', { mode: 'normal', reason: 'x' }, { 'POST /api/peers/A1001/connection-mode': ok({ policy: null }) });
send('admin', 'POST', '/api/devices/A1001/connection-mode', { mode: 'normal', reason: 'x' }, { 'POST /api/peers/A1001/connection-mode': { status: 503 } });
send('admin', 'POST', '/api/devices/A1001/connection-mode', { mode: 'normal', reason: 'x' }, { 'POST /api/peers/A1001/connection-mode': ok({ error: 'refused' }) });

// The device scope check in front of every address below /api/devices/:id,
// before the route's own sign-in and permission checks: e2e_operator may
// not see C1003, B1002 and the deleted E1005, may see A1001 and lacks the
// permissions to ban, delete, change ids and connection modes; e2e_viewer
// may see C1003; ZZ9 does not exist. The panel access guard of the console
// lets viewer and guest_issuer read the device section only, so their
// steps are reads.
const withDeleted = { 'GET /api/peers?include_deleted=true': ok({ peers: [A, B, C, D, E] }) };
const everyRoute = (id) => [
    ['GET', `/api/devices/${id}`], ['GET', `/api/devices/${id}/groups`], ['PUT', `/api/devices/${id}/groups`, { groupGuids: [] }],
    ['GET', `/api/devices/${id}/availability`], ['GET', `/api/devices/${id}/connection-sessions`],
    ['POST', `/api/devices/${id}/telemetry/refresh`, {}], ['POST', `/api/devices/${id}/telemetry/command`, { command: 'collect.services' }],
    ['PATCH', `/api/devices/${id}`, { note: 'x' }], ['DELETE', `/api/devices/${id}`], ['POST', `/api/devices/${id}/restore`, {}],
    ['POST', `/api/devices/${id}/connection-mode`, { mode: 'normal', reason: 'x' }], ['POST', `/api/devices/${id}/ban`, {}],
    ['POST', `/api/devices/${id}/unban`, {}], ['POST', `/api/devices/${id}/change-id`, { newId: 'NEWID9' }],
    ['PUT', `/api/devices/${id}/tags`, { tags: ['x'] }], ['GET', `/api/devices/${id}/access-policy`],
    ['PUT', `/api/devices/${id}/access-policy`, {}], ['DELETE', `/api/devices/${id}/access-policy`],
    ['GET', `/api/devices/${id}/connect-password`], ['POST', `/api/devices/${id}/rename`, { display_name: 'x' }],
    ['PATCH', `/api/devices/${id}/folder`, { folderId: 5 }],
];
const call = (role, [method, p, body], api) => (method === 'GET' ? get(role, p, api) : send(role, method, p, body, api));
for (const route of everyRoute('C1003')) call('operator', route);
for (const route of everyRoute('C1003').filter(([method]) => method === 'GET')) call('viewer', route);
for (const route of everyRoute('B1002').filter(([method]) => method === 'GET')) call('guest_issuer', route);
send('operator', 'POST', '/api/devices/B1002/ban', {});
send('operator', 'PATCH', '/api/devices/B1002/folder', { folderId: null });
for (const route of [['POST', '/api/devices/A1001/ban', {}], ['POST', '/api/devices/A1001/unban', {}], ['POST', '/api/devices/A1001/change-id', { newId: 'NEWID9' }],
    ['DELETE', '/api/devices/A1001'], ['POST', '/api/devices/A1001/restore', {}], ['POST', '/api/devices/A1001/connection-mode', { mode: 'normal', reason: 'x' }]]) {
    call('operator', route);
}
for (const route of [['POST', '/api/devices/ZZ9/ban', {}], ['POST', '/api/devices/ZZ9/restore', {}], ['GET', '/api/devices/ZZ9'], ['PATCH', '/api/devices/ZZ9/folder', {}]]) {
    call('operator', route);
}
// The deleted E1005: the check finds it in the list with the deleted ones
for (const role of ['operator', 'admin']) send(role, 'POST', '/api/devices/E1005/restore', {}, withDeleted);
get('viewer', '/api/devices/E1005', withDeleted);
get('operator', '/api/devices/E1005', withDeleted);
send('operator', 'DELETE', '/api/devices/E1005?hard=true', undefined, withDeleted);
// bulk-delete is an id to the check: it finds no device and goes on
send('operator', 'POST', '/api/devices/bulk-delete', { ids: ['A1001'] });
send('operator', 'POST', '/api/devices/bulk-delete', { ids: ['C1003'] }, { 'GET /api/peers/bulk-delete': ok(C) });
// Other spellings, other methods and addresses no route has
get('operator', '/API/Devices/C1003/Groups');
get('operator', '/api/devices/C1003/');
send('operator', 'PUT', '/api/devices/C1003', { note: 'x' });
get('operator', '/api/devices/C1003/no/such/route');
send('operator', 'POST', '/api/devices/C1003/groups/extra', {});
loose('operator', 'GET', '/api/devices/A1001/no-such-route');
loose('admin', 'GET', '/api/devices/C1003/no-such-route');
loose('operator', 'PUT', '/api/devices/A1001', { note: 'x' });
loose('viewer', 'GET', '/api/devices/C1003/no-such-route');
loose('signed-out', 'GET', '/api/devices/C1003/no-such-route');
get('signed-out', '/api/devices/C1003');
send('signed-out', 'PATCH', '/api/devices/C1003/folder', { folderId: 5 });
// The roles that see every device skip the check and its lookup
for (const role of ['super_admin', 'global_admin', 'server_admin']) get(role, '/api/devices/C1003/access-policy');
// Devices the operator may see go through to the route
send('operator', 'PATCH', '/api/devices/A1001/folder', { folderId: null });
send('operator', 'PATCH', '/api/devices/A1001/folder', { folderId: 5 });
send('operator', 'PATCH', '/api/devices/A1001/folder', { folderId: 999 });
send('admin', 'PATCH', '/api/devices/A1001/folder', {});
get('operator', '/api/devices/D1004');
sql('folders after the scope steps', 'SELECT device_id, folder_id FROM device_folder_assignments ORDER BY device_id');

// Rules for RustDesk clients
for (const role of ['admin', 'viewer']) get(role, '/api/panel/strategies');
for (const body of [{}, { name: '  ' }, { name: 'R', user_group_guid: 'bad guid!' }, { name: 'R', device_group_guid: 'bad guid!' }, [1], { name: { toString: 1 } },
    { name: ' New rule ' + 'r'.repeat(90), enabled: 0, permissions: { 'enable-audio': true, x: 'n', y: ' v ', z: 5, w: '', '2': 'Y' } },
    { name: 'Off rule', enabled: false, permissions: [1] },
    { name: 'RustDesk keys', permissions: { 'enable-tunnel': 'y', 'enable-camera': 'N', 'disable-clipboard': ' Y ', 'enable-file-transfer': false,
        'Enable-Audio': true, 'enable-block-input': 1, 'enable-remote-restart': null, 'enable-clipboard': 'custom', '3': 'Y', 'enable-audio': 'n' } }]) {
    send('admin', 'POST', '/api/panel/strategies', body);
}
get('admin', '/api/panel/strategies');
for (const g of ['s-one', 's-two', 'remote-only', 'no-such', 'bad$guid', '%20s-one']) get('admin', '/api/panel/strategies/' + g);
for (const body of [{}, { name: '' }, { enabled: false }, { permissions: { 'enable-tunnel': 'N' } }, { name: 'Rule one!', user_group_guid: 'ug-ops' },
    { permissions: { 'enable-audio': 'custom', 'enable-camera': true, other: 'Y' } }]) {
    send('admin', 'PATCH', '/api/panel/strategies/s-one', body);
}
send('admin', 'PATCH', '/api/panel/strategies/no-such', { name: 'x' });
send('admin', 'PATCH', '/api/panel/strategies/bad$guid', { name: 'x' });
for (const body of [{}, { peers: [] }, { peers: 5 }, { peers: { length: 2 } }, { peers: ['ZZ9'] }, { peers: [''] }, { peers: 'AB' }, { users: ['nobody'] },
    { groups: ['no-such'] }, { peers: ['A1001', 'B1002', 'guid-c'], users: ['e2e_operator', '5', 'guid-viewer'], groups: ['g-open', 'Ops'] }]) {
    send('admin', 'POST', '/api/panel/strategies/s-two/assign', body);
}
send('admin', 'POST', '/api/panel/strategies/no-such/assign', { peers: ['A1001'] });
get('admin', '/api/panel/strategies/s-two');
get('admin', '/api/panel/strategies/s-one');
for (const body of [{}, { users: ['nobody'] }, { peers: 5 }, { peers: ['A1001'], users: ['e2e_operator'] }, { groups: ['g-open'] }]) {
    send('admin', 'POST', '/api/panel/strategies/unassign', body);
}
get('admin', '/api/panel/strategies/s-two');
send('admin', 'DELETE', '/api/panel/strategies/s-two');
send('admin', 'DELETE', '/api/panel/strategies/no-such');
send('admin', 'DELETE', '/api/panel/strategies/bad$guid');
get('admin', '/api/panel/strategies');

// What the routes left in the tables
sql('assignments', 'SELECT target_type, target_key, strategy_guid FROM strategy_assignments ORDER BY target_type, target_key');
sql('peer guids', "SELECT id, CASE WHEN guid = '' THEN 'none' WHEN guid LIKE '%-%-%-%-%' THEN 'uuid' ELSE guid END AS guid FROM peers ORDER BY id");
sql('user guids', "SELECT username, CASE WHEN guid = '' THEN 'none' WHEN guid LIKE '%-%-%-%-%' AND guid <> 'guid-viewer' THEN 'uuid' ELSE guid END AS guid FROM users ORDER BY id");
sql('audit', 'SELECT user_id, action, details, ip_address FROM audit_log ORDER BY id');

// A failing scope check answers 500: without the table of the accounts a
// group allows, the device groups cannot be read
sql('drop the access lists of the device groups', 'DROP TABLE device_group_user_access');
get('operator', '/api/devices/C1003');
send('operator', 'PATCH', '/api/devices/A1001/folder', { folderId: null });
get('admin', '/api/devices/A1001/access-policy');

(async () => {
    const api = await startAPI();
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${api.address().port}/api`;
    const db = await openDatabase();
    for (const [query, params] of seed) await db.exec(query, params);

    const express = web('node_modules/express');
    const app = express();
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use(web('lib/requireJsonContentType').requireJsonContentType);
    app.use((req, _res, next) => {
        const user = USERS[req.headers['x-oracle-role']];
        req.session = user ? { userId: user.id, user: { ...user } } : {};
        next();
    });
    app.use(web('middleware/i18n').initI18n());
    app.use('/', web('routes/devices.routes'));
    // The folder routes come after the device routes, as in routes/index.js:
    // PATCH /api/devices/:id/folder is behind the device routes' scope check
    app.use('/', web('routes/folders.routes'));
    app.use((req, res) => res.status(404).json({ oracle: 'no route' }));
    // eslint-disable-next-line no-unused-vars
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ success: false, error: 'Internal Server Error' }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;

    const quiet = { error: console.error, warn: console.warn, log: console.log };
    console.error = () => {}; console.warn = () => {}; console.log = () => {};
    const out = [];
    try {
        for (const s of steps) {
            if (s.sql) {
                out.push({ ...s, rows: JSON.stringify(await db.exec(s.sql, s.params)) });
                continue;
            }
            apiState.override = s.api || {};
            apiState.calls = [];
            const answer = await new Promise((resolve, reject) => {
                const headers = { Accept: 'application/json', 'X-Oracle-Role': s.role };
                let payload = null;
                if (s.body !== undefined) {
                    payload = Buffer.from(s.body);
                    headers['Content-Type'] = 'application/json';
                    headers['Content-Length'] = payload.length;
                }
                const req = http.request({ host: '127.0.0.1', port, method: s.method, path: s.path, headers }, (res) => {
                    const chunks = [];
                    res.on('data', (c) => chunks.push(c));
                    res.on('end', () => resolve({ status: res.statusCode, body: Buffer.concat(chunks).toString('utf8') }));
                });
                req.on('error', reject);
                if (payload) req.write(payload);
                req.end();
            });
            out.push({ ...s, status: answer.status, answer: answer.body, extra: { calls: apiState.calls } });
        }
    } finally {
        Object.assign(console, quiet);
        server.close();
        api.close();
        await db.close();
    }
    const file = path.join(__dirname, '..', `node_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed, table: TABLE, steps: out }, null, 1)}\n`);
    console.log(`${file}: ${out.length} steps`);
})().catch((err) => { console.error(err); process.exit(1); });
