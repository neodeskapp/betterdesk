'use strict';

/**
 * Records what routes/bd-api.routes.js answers, with the real database
 * adapter, permission loading, panel access guard, sign-in service,
 * branding service, registration limiter and API client, against a
 * made-up API server. See README.md.
 *
 *   node bdapi.js                                ../node_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node bdapi.js   ../node_postgres.json
 *
 * The agents' relay comes from a stand-in for services/goConsoleBridge.js
 * that keeps sessions in memory and has no agent online, as the Go
 * console's relay without sockets. Every request carries the session of
 * its role in the header X-Oracle-Role. The CSRF check of the two
 * notification writes passes here: the contract cases pin it.
 */

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'bdapi-oracle-'));
const dbName = `oracle_bdapi_${process.pid}_${Date.now() % 1000000}`;

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

// ---- Keys: Ed25519 from fixed seeds, so the Go test signs the same way ----

const SEEDS = {
    a: '01'.repeat(32),
    b: '02'.repeat(32),
    c: '03'.repeat(32),
};
const PKCS8 = Buffer.from('302e020100300506032b657004220420', 'hex');
const keys = {};
for (const [name, seed] of Object.entries(SEEDS)) {
    const privateKey = crypto.createPrivateKey({ key: Buffer.concat([PKCS8, Buffer.from(seed, 'hex')]), format: 'der', type: 'pkcs8' });
    const raw = crypto.createPublicKey(privateKey).export({ format: 'der', type: 'spki' }).subarray(12);
    keys[name] = { privateKey, publicKey: raw.toString('base64') };
}

/**
 * The proof headers of a step. p: {key, device, nonce, offset, method,
 * path, sign: key name to sign with, signature: a fixed signature,
 * timestamp: a fixed timestamp, signKey: the public key written into the
 * payload}. The Go test builds the same headers from the same fields.
 */
function proofHeaders(step) {
    const p = step.proof;
    const timestamp = p.timestamp !== undefined ? p.timestamp : new Date(Date.now() + (p.offset || 0)).toISOString();
    const payloadKey = p.payloadKey !== undefined ? p.payloadKey : keys[p.key].publicKey;
    const payload = Buffer.from(['bd-enrollment-v1', (p.method || step.method).toUpperCase(), p.path || step.path.split('?')[0],
        p.device, payloadKey, timestamp, p.nonce].join('\n'), 'utf8');
    const signature = p.signature !== undefined ? p.signature
        : crypto.sign(null, payload, keys[p.sign || p.key].privateKey).toString('base64');
    return {
        'X-BD-Enrollment-Timestamp': timestamp,
        'X-BD-Enrollment-Nonce': p.nonce,
        'X-BD-Enrollment-Signature': signature,
    };
}

// ---- The made-up API server ----

const json = (status, value) => ({ status, type: 'application/json', body: JSON.stringify(value) });
const helpList = [
    { id: 21, device_id: 'DEV1', hostname: 'pc-1', message: 'printer', status: 'pending', created_at: '2001-03-01T00:00:30Z' },
    { id: 22, device_id: 'DEV2', hostname: '', message: 'vpn', status: 'acknowledged', handled_by: 'apikey:console-bridge', created_at: '2001-03-02T00:00:00Z' },
    { id: 23, device_id: 'DEV3', message: 'font', status: 'resolved', handled_by: 'helpdesk', created_at: '2001-02-01T00:00:00Z' },
    { id: 24, device_id: 'DEV4', status: 'constructor', created_at: '2001-01-01 10:00:00' },
    { id: 25, status: '__proto__', created_at: '2001-01-02T00:00:00+02:00' },
];
const API = {
    'GET /api/help/requests': json(200, helpList),
    'POST /api/help/requests/21/acknowledge': json(200, { id: 21, status: 'acknowledged' }),
    'POST /api/help/requests/21/resolve': json(200, { id: 21, status: 'resolved' }),
    'POST /api/help/requests/22/resolve': json(200, { id: 22, status: 'resolved' }),
    'GET /api/enrollment/pending': json(200, {
        devices: [{ device_id: 'E1', hostname: 'enr-1', platform: 'Windows', ip: '10.1.0.1', created_at: '2001-03-01T12:00:00Z' }],
        count: 1,
    }),
};
const NOT_FOUND = json(404, { error: 'not found' });
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
            res.writeHead(reply.status, reply.type ? { 'Content-Type': reply.type } : {});
            res.end(reply.body || '');
        });
    });
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

// ---- The relay without agents: what the Go console's relay answers ----

function makeRelay() {
    const sessions = new Map();
    // A value that is no text reaches the Go console's relay as ""
    const text = (v) => (typeof v === 'string' ? v : '');
    return {
        createRelaySession: async (initiatorId, targetId) => {
            const sessionId = crypto.randomUUID();
            const initiatorToken = crypto.randomBytes(32).toString('hex');
            const targetToken = crypto.randomBytes(32).toString('hex');
            sessions.set(sessionId, { initiatorId: text(initiatorId), targetId: text(targetId), status: 'pending', createdAt: Date.now() });
            return { sessionId, initiatorToken, targetToken };
        },
        getRelaySession: async (id) => {
            const s = sessions.get(text(id));
            return s ? { sessionId: text(id), initiatorId: s.initiatorId, targetId: s.targetId, status: s.status, createdAt: s.createdAt } : null;
        },
        deleteSession: async (id) => sessions.delete(text(id)),
        notifyTarget: async () => false,
        getOnlineDeviceIds: async () => [],
        isDeviceOnline: async () => undefined,
        pendingConnections: async (target) => [...sessions.entries()]
            .filter(([, s]) => s.targetId === text(target) && s.status === 'pending')
            .map(([id, s]) => ({ session_id: id, initiator_id: s.initiatorId, created_at: s.createdAt })),
    };
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

const sha = (t) => crypto.createHash('sha256').update(t).digest('hex');
const at = (clock) => (pg ? `2001-01-01 ${clock}+00` : `2001-01-01 ${clock}`);
const FUTURE = pg ? '2999-01-01 00:00:00+00' : '2999-01-01 00:00:00';
const PAST = pg ? '2000-01-01 00:00:00+00' : '2000-01-01 00:00:00';
const TOKENS = {
    dev1: ['tok-dev1-0123456789', 1, 'DEV1', FUTURE, false],
    dev2: ['tok-dev2-0123456789', 1, 'DEV2', FUTURE, false],
    dev3: ['tok-dev3-0123456789', 5, 'DEV3', FUTURE, false],
    unbound: ['tok-unbound-0123456789', 1, '', FUTURE, false],
    odd: ['tok-odd-0123456789', 1, 'not a device', FUTURE, false],
    expired: ['tok-expired-0123456789', 1, 'DEV1', PAST, false],
    revoked: ['tok-revoked-0123456789', 1, 'DEV1', FUTURE, true],
    operator: ['tok-operator-0123456789', 5, 'OPDESK', FUTURE, false],
    viewer: ['tok-viewer-0123456789', 6, 'DEV6', FUTURE, false],
    renamed: ['tok-renamed-0123456789', 1, 'OLDID1', FUTURE, false],
};
const bearer = (name) => ({ Authorization: `Bearer ${TOKENS[name][0]}` });

const seed = [['DELETE FROM users', []]];
for (const u of Object.values(USERS)) {
    seed.push(['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [u.id, u.username, 'x', u.role]]);
}
for (const [token, user, client, expires, revoked] of Object.values(TOKENS)) {
    seed.push([`INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ${revoked ? (pg ? 'TRUE' : '1') : (pg ? 'FALSE' : '0')})`,
        [token, sha(token), user, client, '', expires]]);
}
const peer = `INSERT INTO peer (id, uuid, pk, note, info, ip, status_online, is_deleted, is_banned) VALUES (?, ?, ?, ?, ${pg ? '?::jsonb' : '?'}, ?, ${pg ? 'FALSE' : '0'}, ?, ?)`;
const flag = (v) => (pg ? v : (v ? 1 : 0));
seed.push(
    [peer, ['DEV1', 'uuid-1', null, 'Front desk', '{"hostname":"pc-1","os":"Windows"}', '10.0.0.1', flag(false), flag(false)]],
    [peer, ['DEV2', '', null, '', '{"hostname":"pc-2","platform":"Linux"}', '10.0.0.2', flag(false), flag(false)]],
    [peer, ['DEV3', 'uuid-3', null, '', '{}', '', flag(false), flag(false)]],
    [peer, ['KEYDEV', 'uuid-k', keys.a.publicKey, 'Keyed', '{}', '', flag(false), flag(false)]],
    [peer, ['BANNED1', 'uuid-b', null, '', '{}', '', flag(false), flag(true)]],
    [peer, ['GONE1', 'uuid-g', null, '', '{}', '', flag(true), flag(false)]],
    [peer, ['NEWID1', 'uuid-n', null, '', '{}', '203.0.113.9', flag(false), flag(false)]],
    [peer, ['NEWID2', 'uuid-n2', keys.b.publicKey, '', '{}', '', flag(false), flag(false)]],
    [peer, ['900100001', '', null, 'Numbered', '{"hostname":5,"os":["w"]}', '', flag(false), flag(false)]],
);
seed.push(
    [pg ? 'CREATE TABLE IF NOT EXISTS id_change_history (id BIGSERIAL PRIMARY KEY, old_id TEXT NOT NULL, new_id TEXT NOT NULL)'
        : 'CREATE TABLE IF NOT EXISTS id_change_history (id INTEGER PRIMARY KEY AUTOINCREMENT, old_id TEXT NOT NULL, new_id TEXT NOT NULL)', []],
    ['INSERT INTO id_change_history (old_id, new_id) VALUES (?, ?)', ['OLDID1', 'NEWID1']],
    ['INSERT INTO id_change_history (old_id, new_id) VALUES (?, ?)', ['OLDID2', 'NEWID2']],
    ['INSERT INTO id_change_history (old_id, new_id) VALUES (?, ?)', ['OLDID3', 'NOSUCH']],
);
const reg = `INSERT INTO pending_registrations (id, device_id, hostname, platform, version, ip_address, public_key, uuid, status,
    approved_by, approved_at, rejected_reason, access_token, console_url, server_address, server_key, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, '', NULL, NULL, NULL, NULL, ?, ?)`;
seed.push(
    [reg, [1, 'APPROVED1', 'ap-1', 'Windows', '1.0', '10.0.0.5', '', '', 'approved', at('00:01:00'), at('00:01:00')]],
    [reg, [2, 'APPROVED2', 'ap-2', 'Linux', '1.0', '10.0.0.6', keys.a.publicKey, '', 'approved', at('00:02:00'), at('00:02:00')]],
    [reg, [3, 'PENDING1', 'pe-1', 'macOS', '1.0', '10.0.0.7', '', '', 'pending', at('00:03:00'), at('00:03:00')]],
    [reg, [4, 'REJECTED1', 're-1', 'Windows', '1.0', '10.0.0.8', '', '', 'rejected', at('00:04:00'), at('00:04:00')]],
);
if (pg) seed.push(["SELECT setval(pg_get_serial_sequence('pending_registrations', 'id'), 4)", []]);
seed.push(
    [pg ? `CREATE TABLE IF NOT EXISTS agent_templates (id SERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '',
        config_json TEXT NOT NULL DEFAULT '{}', enrollment_token TEXT NOT NULL UNIQUE, enrolled_device_id TEXT DEFAULT NULL,
        enrolled_at TIMESTAMPTZ DEFAULT NULL, created_by INTEGER DEFAULT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`
        : `CREATE TABLE IF NOT EXISTS agent_templates (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE,
        description TEXT NOT NULL DEFAULT '', config_json TEXT NOT NULL DEFAULT '{}', enrollment_token TEXT NOT NULL UNIQUE,
        enrolled_device_id TEXT DEFAULT NULL, enrolled_at TEXT DEFAULT NULL, created_by INTEGER DEFAULT NULL,
        created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')))`, []],
    ["INSERT INTO agent_templates (name, enrollment_token, enrolled_device_id) VALUES ('fresh', 'enroll-fresh', NULL)", []],
    ["INSERT INTO agent_templates (name, enrollment_token, enrolled_device_id) VALUES ('empty', 'enroll-empty', '')", []],
    ["INSERT INTO agent_templates (name, enrollment_token, enrolled_device_id) VALUES ('taken', 'enroll-taken', 'OTHERDEV')", []],
    ["INSERT INTO agent_templates (name, enrollment_token, enrolled_device_id) VALUES ('mine', 'enroll-mine', 'TOKDEV3')", []],
);
const conn = `INSERT INTO audit_connections (host_id, host_uuid, peer_id, peer_name, action, conn_type, session_id, ip, created_at) VALUES (?, ?, ?, ?, ?, 1, ?, '', ?)`;
seed.push(
    [conn, ['alice', 'OPDESK', 'DEV1', 'pc-1', 'session_start', 's-1', at('01:00:00')]],
    [conn, ['alice', 'OPDESK', 'DEV1', 'pc-1', 'session_end', 's-1', at('01:30:10')]],
    [conn, ['bob', '', 'DEV2', '', 'session_end', 's-2', at('02:00:00')]],
    [conn, ['carol', '', 'DEV3', 'pc-3', 'session_start', '', at('03:00:00')]],
    [conn, ['carol', '', 'DEV3', 'pc-3', 'connect', 's-3', at('03:10:00')]],
    [conn, ['dave', '', 'DEV2', 'pc-2b', 'session_start', 's-2', at('01:59:00')]],
);
seed.push(["INSERT INTO notification_reads (user_id, notification_id, read_at) VALUES (1, '21', ?)", [at('05:00:00')]]);

// ---- The steps ----

const steps = [];
let n = 0;
/**
 * One request. body is a value sent as JSON, or text with o.raw. o.type is
 * the Content-Type, o.headers more headers, o.api answers of the API
 * server for this request only, o.proof the proof headers to make, o.capture
 * names values of the answer: {name: 'session_id'} fills {name} later.
 */
function req(role, method, target, body, o = {}) {
    n += 1;
    const s = { name: `${n} ${o.label ? o.label + ' ' : ''}${method} ${target}`, role, method, path: target };
    if (body !== undefined) s.body = o.raw ? body : JSON.stringify(body);
    for (const k of ['type', 'headers', 'api', 'proof', 'capture']) if (o[k]) s[k] = o[k];
    steps.push(s);
}
function sql(label, query, params = []) {
    n += 1;
    steps.push({ name: `${n} ${label}`, sql: query, params });
}
const dev = (name, extra = {}) => ({ ...bearer(name), ...extra });
const xid = (id, extra = {}) => ({ 'X-Device-Id': id, ...extra });
const peerRows = (label, ids) => sql(label, `SELECT id, uuid, ${pg ? "encode(pk, 'escape') AS pk" : 'pk'}, info, ip, status_online, is_deleted FROM peer WHERE id IN (${ids.map(() => '?').join(', ')}) ORDER BY id`, ids);
const audit = () => sql('audit', 'SELECT user_id, action, details, ip_address FROM audit_log ORDER BY id');

// 1. Who the panel is and how it looks
req('', 'GET', '/api/bd/branding');
req('', 'GET', '/api/bd/appearance');

// 2. Registration: who is let through to the policy
req('', 'POST', '/api/bd/register', { device_id: 'DEVX' }, { label: 'no identification' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('ab'), label: 'header too short' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('a'.repeat(65)), label: 'header too long' });
req('', 'POST', '/api/bd/register', {}, { headers: { 'X-Device-Id': ['DEVA', 'DEVB'] }, label: 'header twice' });
req('', 'POST', '/api/bd/register', {}, { headers: dev('unbound'), label: 'unbound token' });
req('', 'POST', '/api/bd/register', {}, { headers: dev('odd'), label: 'token of no device id' });
req('', 'POST', '/api/bd/register', {}, { headers: dev('expired'), label: 'expired token' });
req('', 'POST', '/api/bd/register', {}, { headers: dev('revoked'), label: 'revoked token' });
req('', 'POST', '/api/bd/register', {}, { headers: { Authorization: 'Bearer    ' }, label: 'blank token' });
req('', 'POST', '/api/bd/register', {}, { headers: { Authorization: 'bearer tok-dev1-0123456789' }, label: 'lower-case bearer' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV1', uuid: 'uuid-1' }, { headers: { Authorization: 'Bearer  tok-dev1-0123456789  ' }, label: 'token with spaces' });
req('', 'POST', '/api/bd/register', { uuid: 'uuid-1' }, { headers: dev('unbound', xid('DEV1')), label: 'unbound token and header' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV2' }, { headers: dev('dev1'), label: 'token for another device' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV2' }, { headers: xid('DEV1'), label: 'header for another device' });
req('', 'POST', '/api/bd/register', { device_id: '  DEV1  ', uuid: 'uuid-1' }, { headers: xid('DEV1'), label: 'claim with spaces' });
req('', 'POST', '/api/bd/register', { device_id: 7 }, { headers: xid('DEV1'), label: 'claim a number' });

// 3. Known devices
req('', 'POST', '/api/bd/register', { device_id: 'DEV1', uuid: 'uuid-1', hostname: 'pc-1b', platform: 'Windows 11', version: '1.4' }, { headers: dev('dev1'), label: 'refresh' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV2' }, { headers: xid('DEV2'), label: 'refresh by header alone' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV1', uuid: 'other' }, { headers: dev('dev1'), label: 'uuid change, no stored key' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV1', uuid: '  uuid-1  ' }, { headers: dev('dev1'), label: 'same uuid with spaces' });
req('', 'POST', '/api/bd/register', { device_id: 'DEV3', public_key: keys.b.publicKey }, { headers: dev('dev3'), label: 'key where none was' });
req('', 'POST', '/api/bd/register', { device_id: 'BANNED1' }, { headers: xid('BANNED1'), label: 'banned' });
req('', 'POST', '/api/bd/register', { device_id: 'GONE1', enrollment_token: 'enroll-fresh' }, { headers: xid('GONE1'), label: 'removed, with a token' });
req('', 'POST', '/api/bd/register', { device_id: 'KEYDEV', uuid: 'uuid-k', public_key: keys.a.publicKey }, { headers: xid('KEYDEV'), label: 'same key' });
req('', 'POST', '/api/bd/register', { device_id: 'KEYDEV', public_key: keys.b.publicKey }, { headers: xid('KEYDEV'), label: 'key swap without proof' });
const rotate = (label, proof, body = { device_id: 'KEYDEV', public_key: keys.b.publicKey }) =>
    req('', 'POST', '/api/bd/register', body, { headers: xid('KEYDEV'), proof: { device: 'KEYDEV', key: 'a', nonce: `n-${n}`, ...proof }, label });
rotate('key swap, proof by another key', { sign: 'c', payloadKey: keys.a.publicKey });
rotate('key swap, proof too old', { offset: -6 * 60 * 1000 });
rotate('key swap, proof from the future', { offset: 6 * 60 * 1000 });
rotate('key swap, unreadable time', { timestamp: 'yesterday' });
rotate('key swap, nonce too long', { nonce: 'x'.repeat(257) });
rotate('key swap, signature too short', { signature: 'AAAA' });
rotate('key swap, signature of another path', { path: '/api/bd/heartbeat' });
rotate('key swap, uuid change with proof', { nonce: 'rot-1' }, { device_id: 'KEYDEV', uuid: 'uuid-k2' });
rotate('the same proof again', { nonce: 'rot-1' }, { device_id: 'KEYDEV', uuid: 'uuid-k3' });
rotate('key swap with proof', { nonce: 'rot-2' });
peerRows('known devices after', ['DEV1', 'DEV2', 'DEV3', 'KEYDEV', 'GONE1']);

// 4. Renamed ids
req('', 'POST', '/api/bd/register', { device_id: 'OLDID1', uuid: 'uuid-x' }, { headers: dev('renamed'), label: 'renamed, other uuid' });
req('', 'POST', '/api/bd/register', { device_id: 'OLDID1', uuid: 'UUID-N' }, { headers: dev('renamed'), label: 'renamed, same uuid in capitals' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('OLDID1', { 'X-Forwarded-For': '203.0.113.9, 10.0.0.1' }), label: 'renamed, same address' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('OLDID1', { 'X-Forwarded-For': '203.0.113.8' }), label: 'renamed, other address' });
req('', 'POST', '/api/bd/register', { public_key: keys.b.publicKey }, { headers: xid('OLDID2'), label: 'renamed, same key' });
req('', 'POST', '/api/bd/register', { public_key: 'other' }, { headers: xid('OLDID2'), label: 'renamed, other key' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('OLDID3'), label: 'renamed to an id nobody has' });
peerRows('renamed devices after', ['NEWID1', 'NEWID2', 'OLDID1', 'OLDID2', 'OLDID3']);

// 5. New devices: approval, enrollment token, proof
req('', 'POST', '/api/bd/register', { hostname: 'ap' }, { headers: xid('APPROVED1'), label: 'approved' });
req('', 'POST', '/api/bd/register', { public_key: keys.c.publicKey }, { headers: xid('APPROVED2'), label: 'approved with another key' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('APPROVED2'), label: 'approved, no key sent' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('PENDING1'), label: 'only pending' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('REJECTED1'), label: 'rejected' });
req('', 'POST', '/api/bd/register', { enrollment_token: 'enroll-taken' }, { headers: xid('TOKDEV1'), label: 'token of another device' });
req('', 'POST', '/api/bd/register', { enrollment_token: 'enroll-nope' }, { headers: xid('TOKDEV1'), label: 'unknown token' });
req('', 'POST', '/api/bd/register', { enrollment_token: '  enroll-fresh  ' }, { headers: xid('TOKDEV1'), label: 'fresh token' });
req('', 'POST', '/api/bd/register', { enrollment_token: 'enroll-fresh' }, { headers: xid('TOKDEV2'), label: 'the fresh token again' });
req('', 'POST', '/api/bd/register', { enrollment_token: 'enroll-mine' }, { headers: xid('TOKDEV3'), label: 'token bound to this device' });
req('', 'POST', '/api/bd/register', { enrollment_token: 'enroll-empty' }, { headers: xid('TOKDEV4'), label: 'token bound to nobody' });
req('', 'POST', '/api/bd/register', { enrollment_token: 7 }, { headers: xid('TOKDEV5'), label: 'token a number' });
req('', 'POST', '/api/bd/register', { enrollment_token: { toString: 'x' } }, { headers: xid('TOKDEV5'), label: 'token without text' });
sql('templates after', 'SELECT name, enrolled_device_id FROM agent_templates ORDER BY id');
const firstProof = (label, device, proof, body = {}) =>
    req('', 'POST', '/api/bd/register', { public_key: keys.a.publicKey, ...body }, { headers: xid(device), proof: { device, key: 'a', nonce: `p-${n}`, ...proof }, label });
firstProof('proof', 'PROOF1', { nonce: 'first' });
firstProof('same nonce, another device', 'PROOF2', { nonce: 'first' });
firstProof('proof replayed', 'PROOF3', { nonce: 'again' });
firstProof('proof replayed (2)', 'PROOF3', { nonce: 'again' });
firstProof('proof for another device', 'PROOF4', { device: 'PROOF9' });
firstProof('proof with a lower-case method', 'PROOF5', { method: 'post' });
req('', 'POST', '/api/bd/register', {}, { headers: xid('PROOF6'), proof: { device: 'PROOF6', key: 'a', nonce: 'nokey' }, label: 'proof without a key' });
req('', 'POST', '/api/bd/register', { public_key: 'not base64 !!' }, { headers: xid('PROOF7'), proof: { device: 'PROOF7', key: 'a', nonce: 'badkey' }, label: 'proof, key unreadable' });
req('', 'POST', '/api/bd/register', { public_key: ` ${keys.a.publicKey.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')} ` },
    { headers: xid('PROOF8'), proof: { device: 'PROOF8', key: 'a', nonce: 'urlkey' }, label: 'proof, key in the URL alphabet' });
req('', 'POST', '/API/BD/Register/', { public_key: keys.a.publicKey }, { headers: xid('PROOF10'), proof: { device: 'PROOF10', key: 'a', nonce: 'spelled' }, label: 'proof, path spelled otherwise' });
req('', 'POST', '/api/bd/register', ['DEV1'], { headers: xid('NEW2'), label: 'a list for a body' });
req('', 'POST', '/api/bd/register', 'device_id=FORM1&uuid=u', { raw: true, type: 'application/x-www-form-urlencoded', headers: xid('FORM1'), label: 'a form' });
peerRows('new devices after', ['APPROVED1', 'APPROVED2', 'TOKDEV1', 'TOKDEV2', 'TOKDEV3', 'TOKDEV4', 'PROOF1', 'PROOF2', 'PROOF3', 'PROOF8', 'PROOF10']);

// 6. Heartbeat
req('', 'POST', '/api/bd/heartbeat', {}, { headers: dev('dev1'), label: 'without a claim' });
req('', 'POST', '/api/bd/heartbeat', { device_id: 'DEV1' }, { headers: dev('dev1') });
req('', 'POST', '/api/bd/heartbeat', { device_id: 'DEV2' }, { headers: dev('dev1'), label: 'another device' });
req('', 'POST', '/api/bd/heartbeat', { device_id: ['DEV1'] }, { headers: dev('dev1'), label: 'claim in a list' });
req('', 'POST', '/api/bd/heartbeat', { device_id: 900100001 }, { headers: dev('odd'), label: 'number for a device' });
req('', 'POST', '/api/bd/heartbeat', {}, { headers: dev('unbound'), label: 'unbound token' });
req('', 'POST', '/api/bd/heartbeat', {}, { headers: { Authorization: 'Basic eDp5' }, label: 'basic' });

// 7. Relay sessions
req('', 'POST', '/api/bd/connect', {}, { headers: dev('dev1'), label: 'no target' });
req('', 'POST', '/api/bd/connect', { target_id: 'DEV2' }, { headers: dev('unbound'), label: 'unbound token' });
req('', 'POST', '/api/bd/connect', { target_id: 'DEV2', initiator_id: 'DEV3' }, { headers: dev('dev1'), label: 'initiator mismatch' });
req('', 'POST', '/api/bd/connect', { target_id: 'DEV2', initiator_id: ['DEV1'] }, { headers: dev('dev1'), label: 'initiator in a list' });
req('', 'POST', '/api/bd/connect', { target_id: 'NOPE' }, { headers: dev('dev1'), label: 'unknown target' });
req('', 'POST', '/api/bd/connect', { target_id: 'GONE1' }, { headers: dev('dev1'), label: 'removed target' });
req('', 'POST', '/api/bd/connect', { target_id: 'BANNED1' }, { headers: dev('dev1'), label: 'banned target' });
req('', 'POST', '/api/bd/connect', { target_id: 900100001 }, { headers: dev('dev1'), label: 'target a number' });
req('', 'POST', '/api/bd/connect', { target_id: 'DEV2', initiator_id: 'DEV1', public_key: 'pk' }, { headers: dev('dev1'), capture: { sess: 'session_id' } });
req('', 'POST', '/api/bd/connect', { target_id: 'DEV1' }, { headers: dev('viewer'), label: 'token of a viewer' });
req('', 'POST', '/api/bd/heartbeat', { device_id: 'DEV2' }, { headers: dev('dev2'), label: 'target sees it' });
req('', 'GET', '/api/bd/session/{sess}', undefined, { headers: dev('dev1'), label: 'initiator' });
req('', 'GET', '/api/bd/session/{sess}', undefined, { headers: dev('dev2'), label: 'target' });
req('', 'GET', '/api/bd/session/{sess}', undefined, { headers: dev('dev3'), label: 'somebody else' });
req('', 'GET', '/api/bd/session/nope', undefined, { headers: dev('dev1'), label: 'unknown' });
req('', 'DELETE', '/api/bd/session/{sess}', undefined, { headers: dev('dev3'), label: 'somebody else' });
req('', 'DELETE', '/api/bd/session/{sess}', undefined, { headers: dev('dev2'), label: 'the target' });
req('', 'DELETE', '/api/bd/session/{sess}', undefined, { headers: dev('dev2'), label: 'again' });
req('', 'GET', '/api/bd/peers', undefined, { headers: dev('dev1') });
req('', 'GET', '/api/bd/peer/DEV1', undefined, { headers: dev('dev2') });
req('', 'GET', '/api/bd/peer/BANNED1', undefined, { headers: dev('dev2') });
req('', 'GET', '/api/bd/peer/GONE1', undefined, { headers: dev('dev2') });
req('', 'GET', '/api/bd/peer/%44EV1', undefined, { headers: dev('dev2'), label: 'escaped' });
req('', 'GET', '/api/bd/peer/NOPE', undefined, { headers: dev('dev2') });

// 8. Help requests and chat of a device
req('', 'POST', '/api/bd/help-request', { device_id: 'DEV1', hostname: 'pc-1', message: 'help' }, { headers: dev('dev1'), label: 'API has no route' });
req('', 'POST', '/api/bd/help-request', { device_id: 'DEV1', message: 'm'.repeat(600), hostname: { x: 1 } },
    { headers: dev('dev1'), api: { 'POST /api/help/requests': json(201, { id: 77 }) }, label: 'API gives an id' });
req('', 'POST', '/api/bd/help-request', { device_id: 'DEV1' }, { headers: dev('dev1'), api: { 'POST /api/help/requests': json(200, { request_id: 'r-9' }) }, label: 'API gives a request id' });
req('', 'POST', '/api/bd/help-request', { device_id: 'DEV1' }, { headers: dev('dev1'), api: { 'POST /api/help/requests': json(200, { id: 0, request_id: [1, 2] }) }, label: 'API gives a list' });
req('', 'POST', '/api/bd/help-request', { device_id: 'DEV1' + 'x'.repeat(40) }, { headers: dev('odd'), label: 'long id' });
req('', 'POST', '/api/bd/help-request', { device_id: ['DEV1'] }, { headers: dev('dev1'), label: 'id in a list' });
req('', 'POST', '/api/bd/help-request', { device_id: 'DEV1', message: { toString: 1 } }, { headers: dev('dev1'), label: 'message without text' });
req('', 'POST', '/api/bd/help-request', {}, { headers: dev('dev1'), label: 'no id' });
audit();
req('', 'POST', '/api/bd/chat/send', { device_id: 'DEV1', content: 'hi' }, { headers: dev('dev1') });
req('', 'POST', '/api/bd/chat/send', { device_id: 'DEV1', content: ' ' }, { headers: dev('dev1'), label: 'blank' });
req('', 'POST', '/api/bd/chat/send', { device_id: 'DEV1', content: 5 }, { headers: dev('dev1'), label: 'content a number' });
req('', 'POST', '/api/bd/chat/send', { device_id: 'DEV1', content: '😀'.repeat(2049) }, { headers: dev('dev1'), label: 'content of 4098 units' });
req('', 'POST', '/api/bd/chat/send', { device_id: 'DEV1', content: '😀'.repeat(2048) }, { headers: dev('dev1'), label: 'content of 4096 units' });
req('', 'GET', '/api/bd/chat/history', undefined, { headers: dev('dev1'), label: 'without a device' });
req('', 'GET', '/api/bd/chat/history?device_id=', undefined, { headers: dev('dev1'), label: 'empty device' });
req('', 'GET', '/api/bd/chat/history?device_id=DEV2', undefined, { headers: dev('dev1'), label: 'another device' });

// Values of every type reach the device table of a known device
req('', 'POST', '/api/bd/register', { uuid: 7, hostname: { a: [1, 'é'] }, platform: 0, version: null }, { headers: xid('DEV2'), label: 'values of every type' });
req('', 'POST', '/api/bd/register', { uuid: '', public_key: true }, { headers: xid('DEV2'), label: 'key true' });
req('', 'POST', '/api/bd/register', { public_key: { k: 1 } }, { headers: xid('DEV2'), label: 'key an object' });
req('', 'POST', '/api/bd/register', { public_key: 12 }, { headers: xid('DEV3'), label: 'key a number' });
req('', 'POST', '/api/bd/register', { uuid: [1, 2] }, { headers: xid('DEV2'), label: 'uuid a list' });
req('', 'POST', '/api/bd/register', { uuid: { toString: 1 } }, { headers: xid('DEV2'), label: 'uuid without text' });
peerRows('odd values after', ['DEV2', 'DEV3']);

// 9. Operator mode
req('', 'POST', '/api/bd/operator/login', {}, { label: 'empty' });
req('', 'POST', '/api/bd/operator/login', { username: 'e2e_operator', password: ['x'] }, { label: 'password a list' });
req('', 'POST', '/api/bd/operator/login', { username: 'e2e_operator', password: 'wrong', device_id: 'OPDESK' });
req('', 'GET', '/api/bd/operator/devices', undefined, { headers: dev('operator') });
req('', 'GET', '/api/bd/operator/devices', undefined, { headers: dev('viewer'), label: 'viewer' });
req('', 'GET', '/api/bd/operator/devices', undefined, { headers: dev('expired'), label: 'expired token' });
req('', 'GET', '/api/bd/operator/devices', undefined, { headers: { Authorization: 'Bearer  tok-operator-0123456789 ' }, label: 'token with spaces' });
req('', 'GET', '/api/bd/operator/devices', undefined, { label: 'no token' });
req('', 'GET', '/api/bd/help-requests', undefined, { headers: dev('operator') });
req('', 'GET', '/api/bd/help-requests?status=pending&device_id=DEV1', undefined, { headers: dev('operator'), label: 'filtered' });
req('', 'GET', '/api/bd/help-requests?status[]=a&status[]=b', undefined, { headers: dev('operator'), label: 'status twice' });
req('', 'GET', '/api/bd/help-requests', undefined, { headers: dev('operator'), api: { 'GET /api/help/requests': json(500, { error: 'down' }) }, label: 'API fails' });
req('', 'GET', '/api/bd/help-requests', undefined, { headers: dev('operator'), api: { 'GET /api/help/requests': json(200, { a: 1 }) }, label: 'API gives an object' });
req('', 'GET', '/api/bd/help-requests', undefined, { headers: dev('operator'),
    api: { 'GET /api/help/requests': json(200, [null, 5, { id: { toString: 'x' } }]) }, label: 'API gives an id without text' });
req('', 'GET', '/api/bd/help-requests', undefined, { headers: dev('viewer'), label: 'viewer' });
req('', 'POST', '/api/bd/help-requests/21/accept', undefined, { headers: dev('operator') });
req('', 'POST', '/api/bd/help-requests/21/resolve', undefined, { headers: dev('dev1'), label: 'admin token' });
req('', 'POST', '/api/bd/help-requests/99/accept', undefined, { headers: dev('operator'), label: 'unknown' });
req('', 'POST', '/api/bd/help-requests/99/resolve', undefined, { headers: dev('operator'), label: 'unknown' });
req('', 'DELETE', '/api/bd/help-requests/22', undefined, { headers: dev('operator') });
req('', 'DELETE', '/api/bd/help-requests/99', undefined, { headers: dev('operator'), label: 'unknown' });
audit();
req('', 'POST', '/api/bd/operator/sessions', { action: ' Start ', device_id: ' DEV1 ', hostname: ' pc-1 ', session_id: ' s-9 ' }, { headers: dev('operator') });
req('', 'POST', '/api/bd/operator/sessions', { action: 'end', device_id: 'DEV1', session_id: 's-9' }, { headers: dev('operator') });
req('', 'POST', '/api/bd/operator/sessions', { action: 'session_start', device_id: 'DEV2' }, { headers: dev('dev1'), label: 'without a session id' });
req('', 'POST', '/api/bd/operator/sessions', { action: 'pause', device_id: 'DEV1' }, { headers: dev('operator'), label: 'bad action' });
req('', 'POST', '/api/bd/operator/sessions', { action: 'start', device_id: 'D' }, { headers: dev('operator'), label: 'bad device' });
req('', 'POST', '/api/bd/operator/sessions', { action: 'start', device_id: { toString: 1 } }, { headers: dev('operator'), label: 'device without text' });
req('', 'POST', '/api/bd/operator/sessions', { action: 'start', device_id: 'DEV1', hostname: 'h'.repeat(200), session_id: 's'.repeat(120) }, { headers: dev('operator'), label: 'long fields' });
sql('connections', "SELECT host_id, host_uuid, peer_id, peer_name, action, conn_type, session_id, ip FROM audit_connections WHERE host_id <> 'alice' AND host_id <> 'bob' AND host_id <> 'carol' AND host_id <> 'dave' AND session_id LIKE 's-%' ORDER BY id");
for (const q of ['', '?limit=1', '?limit=0', '?limit=abc', '?limit=500', '?limit[]=2']) {
    req('', 'GET', `/api/bd/operator/sessions${q}`, undefined, { headers: dev('operator') });
}

// 10. The notification bell
for (const role of ['', 'pro', 'viewer', 'server_admin', 'admin']) {
    req(role, 'GET', '/api/bd/notifications');
}
for (const q of ['?limit=1', '?limit=0', '?limit=99', '?limit=x', '?unread_only=TRUE', '?unread_only=1', '?unread_only=true&limit=2']) {
    req('admin', 'GET', `/api/bd/notifications${q}`);
}
req('admin', 'GET', '/api/bd/notifications', undefined, { api: { 'GET /api/help/requests': json(500, { error: 'down' }) }, label: 'API fails' });
req('admin', 'GET', '/api/bd/notifications', undefined, { api: { 'GET /api/help/requests': json(200, 'text') }, label: 'API gives text' });
req('viewer', 'POST', '/api/bd/notifications/22/read');
req('viewer', 'POST', `/api/bd/notifications/${'n'.repeat(140)}/read`, undefined, { label: 'long id' });
req('', 'POST', '/api/bd/notifications/22/read', undefined, { label: 'signed out' });
req('viewer', 'GET', '/api/bd/notifications');
req('admin', 'POST', '/api/bd/notifications/read-all');
req('admin', 'GET', '/api/bd/notifications');
req('operator', 'POST', '/api/bd/notifications/read-all', undefined, { api: { 'GET /api/help/requests': json(200, { a: 1 }) }, label: 'API gives an object' });
sql('read marks', 'SELECT user_id, notification_id FROM notification_reads ORDER BY user_id, notification_id');

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
        if (step.proof) Object.assign(headers, proofHeaders({ ...step, path: fill(step.path) }));
        let payload = null;
        if (step.body !== undefined) {
            payload = Buffer.from(step.body);
            headers['Content-Type'] = step.type || 'application/json';
            headers['Content-Length'] = payload.length;
        }
        const r = http.request({ host: '127.0.0.1', port, method: step.method, path: fill(step.path), headers }, (res) => {
            const chunks = [];
            res.on('data', (c) => chunks.push(c));
            res.on('end', () => resolve({ status: res.statusCode, body: Buffer.concat(chunks).toString('utf8'), headers: res.headers }));
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
    process.env.BD_REGISTER_RATE_LIMIT_MAX = '1000000';
    process.env.RATE_LIMIT_MAX = '1000000';
    fs.writeFileSync(process.env.API_KEY_PATH, 'oracle-api-key');
    stub('services/goConsoleBridge', { learn: (_req, _res, next) => next(), publishPanelEvent: () => {}, relay: makeRelay() });
    const db = await openDatabase();
    const csrf = web('middleware/csrf');
    stub('middleware/csrf', { ...csrf, doubleCsrfProtection: (_req, _res, next) => next() });
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
    app.use('/api/bd', web('routes/bd-api.routes'));
    app.use((r, res) => res.status(404).json({ oracle: 'no route' }));
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
            overrides = step.api || {};
            const answer = await send(port, step, vars);
            await new Promise((resolve) => setTimeout(resolve, 10));
            if (step.capture) {
                let parsed = null;
                try { parsed = JSON.parse(answer.body); } catch (_) { /* not JSON */ }
                for (const [name, dotted] of Object.entries(step.capture)) {
                    const v = pick(parsed, dotted);
                    if (v !== undefined && v !== null) vars[name] = String(v);
                }
            }
            const kept = {};
            for (const h of ['cache-control', 'ratelimit-limit']) if (answer.headers[h] !== undefined) kept[h] = answer.headers[h];
            out.push({ ...step, status: answer.status, answer: answer.body, header: kept, calls: calls.map((c) => ({ ...c })) });
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
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seeds: SEEDS, seed, api: API, steps: out }, null, 1)}\n`);
    realLog(`${file}: ${out.length} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
