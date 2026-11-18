'use strict';

/**
 * Runs the Node console's routes that reach into a running agent
 * (routes/devices.routes.js after the other device routes moved, with the
 * real services/bdRelay.js) on a scratch database, in front of an API
 * server that answers from a table, with a made-up agent of device A1001
 * connected to /ws/bd-signal that answers each request the way the step
 * says. It records every answer, every call the routes made to the API
 * server and every request the agent got. See README.md.
 *
 *   node agent.js                                ../node_agent_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node agent.js   ../node_agent_postgres.json
 */

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'agent-oracle-'));
const schema = `oracle_agent_${process.pid}`;
const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

const USERS = {
    admin: { id: 1, username: 'e2e_admin', role: 'admin' },
    operator: { id: 5, username: 'e2e_operator', role: 'operator' },
    viewer: { id: 6, username: 'e2e_viewer', role: 'viewer' },
};

// The agent's token: the check is stubbed, as the Go test gives the
// device a real token instead.
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('lib/deviceTokenAuth.js', { verifyDeviceWsAuth: async (id, token) => id === 'A1001' && token === 'agent-token-A1001' });

// ---- The API server ----

const A = { id: 'A1001', hostname: 'alpha', live_online: true, tags: [] };
const B = { id: 'B1002', hostname: 'beta', tags: [] };
const C = { id: 'C1003', hostname: '', tags: [] };
const ok = (body, status = 200) => ({ status, body });
const TABLE = {
    'GET /api/peers/A1001': ok(A),
    'GET /api/peers/B1002': ok(B),
    'GET /api/peers/C1003': ok(C),
    'GET /api/peers/B1002/telemetry': ok({
        device_id: 'B1002', schema: 1, snapshots: {
            services: { collected_at: '2020-01-01T00:00:00Z', data: [{ name: 'sshd', state: 'running' }] },
            processes: { collected_at: '2020-01-01T00:00:00Z', data: null },
            files: { collected_at: '2020-01-02T00:00:00Z', data: { path: '/', entries: [] } },
        },
    }),
    'GET /api/peers/A1001/telemetry': ok({ device_id: 'A1001', snapshots: { services: { collected_at: 'x', data: ['from the server'] } } }),
    'POST /api/peers/B1002/telemetry/command': ok({ accepted: true, command_id: 9 }, 202),
    'POST /api/peers/A1001/telemetry/command': ok({ accepted: true, command_id: 10 }, 202),
};
const notFound = { status: 404, body: { error: 'not found' } };
const apiState = { override: {}, calls: [] };

function startAPI() {
    const server = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            const key = `${req.method} ${req.url}`;
            apiState.calls.push(key + (chunks.length ? ' ' + Buffer.concat(chunks).toString('utf8') : ''));
            const a = apiState.override[key] || TABLE[key] || notFound;
            res.writeHead(a.status, a.text === undefined ? { 'Content-Type': 'application/json' } : { 'Content-Type': 'text/plain' });
            res.end(a.text !== undefined ? a.text : (a.body === undefined ? '' : JSON.stringify(a.body)));
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

// The rows: the role accounts, and C1003 in a device group only e2e_viewer
// may see, so e2e_operator may not see it.
const seed = [
    ...(pg ? [
        ["ALTER TABLE users ADD COLUMN IF NOT EXISTS guid TEXT DEFAULT ''", []],
        ["ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS source_type TEXT DEFAULT 'manual'", []],
        ["ALTER TABLE device_groups ADD COLUMN IF NOT EXISTS tag_filter TEXT DEFAULT ''", []],
    ] : []),
    ['DELETE FROM users', []],
    ...Object.values(USERS).map((u) => ['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [u.id, u.username, 'x', u.role]]),
    ["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-viewer', 'Viewer only', '', '', 'manual', '')", []],
    ["INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'g-viewer'", []],
    ["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'C1003' FROM device_groups WHERE guid = 'g-viewer'", []],
];

// ---- The steps ----

const steps = [];
// agent: what the made-up agent does with the request: an object is its
// command_response (request_id filled in), "silent" sends nothing.
const step = (role, method, p, body, agent, api) => steps.push({
    name: `${method} ${p}${body === undefined ? '' : ' ' + body} as ${role}${agent === undefined ? '' : ' agent ' + JSON.stringify(agent)}`,
    role, method, path: p, ...(body === undefined ? {} : { body }), ...(agent === undefined ? {} : { agent }), ...(api ? { api } : {}),
});
const get = (role, p, agent, api) => step(role, 'GET', p, undefined, agent, api);
const post = (role, p, body, agent, api) => step(role, 'POST', p, typeof body === 'string' ? body : JSON.stringify(body === undefined ? {} : body), agent, api);
const sql = (name, query) => steps.push({ name, sql: query, params: [] });
const yes = (data) => ({ ok: true, data });
const no = (error) => ({ ok: false, ...(error === undefined ? {} : { error }) });

// Reads, online, with data of every JSON type
for (const p of ['services', 'processes', 'activity', 'clipboard']) {
    get('admin', `/api/devices/A1001/${p}`, yes({ items: [{ b: 1, a: 2, '1': 'x' }], n: 1.5, t: 'é ' }));
}
get('viewer', '/api/devices/A1001/services', yes([1, 'two', null, true, { x: {} }]));
get('admin', '/api/devices/A1001/services', yes('text'));
get('admin', '/api/devices/A1001/services', yes(0));
get('admin', '/api/devices/A1001/services', yes(''));
get('admin', '/api/devices/A1001/services', yes(false));
get('admin', '/api/devices/A1001/services', { ok: true });
get('admin', '/api/devices/A1001/services', { ok: 'yes', data: { deep: [[[]]] } });
get('admin', '/api/devices/A1001/services', no('permission denied'));
get('admin', '/api/devices/A1001/services', no());
get('admin', '/api/devices/A1001/services', no(''));
get('admin', '/api/devices/A1001/services', no(503));
get('admin', '/api/devices/A1001/services', no({ code: 1 }));
get('admin', '/api/devices/A1001/services', no(['a', ['b', null]]));
get('admin', '/api/devices/A1001/services', no('agent_timeout'));
get('admin', '/api/devices/A1001/services', no('agent_offline'));
get('admin', '/api/devices/A1001/processes', no('agent_offline'));
get('admin', '/api/devices/A1001/clipboard', no('agent_offline'));
for (const q of ['', '?limit=5', '?limit=0', '?limit=-3', '?limit=abc', '?limit=999', '?limit=12.9', '?limit=7x', '?limit[]=4', '?limit=500', '?limit=501']) {
    get('admin', `/api/devices/A1001/events${q}`, yes({ q }));
}
get('admin', '/api/devices/A1001/clipboard', 'silent');
// Not found, out of scope, every role's reach
get('admin', '/api/devices/ZZ9/services');
get('admin', '/api/devices/ZZ9/clipboard');
get('operator', '/api/devices/C1003/services');
get('viewer', '/api/devices/C1003/services', undefined);
get('operator', '/api/devices/A1001/activity', yes(1));
get('admin', '/api/devices/D1004/services');
// Offline: the server's snapshot, a queued collection, 503
get('admin', '/api/devices/B1002/services');
get('admin', '/api/devices/B1002/processes');
get('admin', '/api/devices/B1002/events?limit=3');
get('admin', '/api/devices/B1002/activity');
get('admin', '/api/devices/B1002/clipboard');
post('admin', '/api/devices/B1002/files/browse', { path: '/var', show_hidden: 'true' });
post('admin', '/api/devices/B1002/files/read', { path: '/etc/hosts' });
post('admin', '/api/devices/B1002/screenshot');
post('admin', '/api/devices/B1002/terminal/execute', { command: 'ls' });
get('admin', '/api/devices/B1002/services', undefined, { 'GET /api/peers/B1002/telemetry': { status: 500, body: { error: 'down' } } });
get('admin', '/api/devices/B1002/services', undefined, { 'GET /api/peers/B1002/telemetry': ok({ snapshots: { services: { data: 0 } } }) });
get('admin', '/api/devices/B1002/services', undefined, { 'GET /api/peers/B1002/telemetry': ok({ snapshots: { services: { data: 'yes' } } }) });
get('admin', '/api/devices/B1002/services', undefined, { 'GET /api/peers/B1002/telemetry': ok('not an object') });
get('admin', '/api/devices/B1002/processes', undefined, { 'POST /api/peers/B1002/telemetry/command': ok({ error: 'command is not allowed' }, 400) });
get('admin', '/api/devices/B1002/processes', undefined, { 'POST /api/peers/B1002/telemetry/command': { status: 500 } });
get('admin', '/api/devices/B1002/processes', undefined, { 'POST /api/peers/B1002/telemetry/command': ok({ success: false }, 202) });
get('admin', '/api/devices/A1001/services', no('agent_offline'), { 'GET /api/peers/A1001/telemetry': notFound });
// Files
for (const body of [{ path: '/tmp', show_hidden: true }, {}, { path: 5, show_hidden: 1 }, { path: ['x'], show_hidden: 'yes' }, { path: null },
    { path: 'p'.repeat(5000) }, { path: '\u{1F600}'.repeat(2049) }, { path: true, show_hidden: '0' }, [1, 2]]) {
    post('admin', '/api/devices/A1001/files/browse', body, yes({ entries: [] }));
}
for (const body of [{ path: '/a', offset: 7, length: 9 }, {}, { offset: -1, length: 1048577 }, { offset: '12', length: '0x10' }, { offset: 'abc', length: ' ' },
    { offset: 3.9, length: -2.5 }, { offset: [1], length: { a: 1 } }, 'not json', '[1]', '"text"', 'null']) {
    post('admin', '/api/devices/A1001/files/read', body, yes({ content: 'eA==' }));
}
for (const body of [{ path: '/tmp/f', data: 'eA==', mode: 'create' }, { path: '/tmp/f', data: 'eA==' }, { path: '/tmp/f', mode: 'delete' }, { data: 'x' },
    { path: '', data: 'x' }, { path: 0 }, { path: ['a', 'b'], data: { x: 1 }, mode: 'append' }, { path: 'p'.repeat(5000), data: 7 }]) {
    post('admin', '/api/devices/A1001/files/write', body, yes({ written: 1 }));
}
for (const body of [{ path: '/tmp/x', recursive: true }, { path: '/tmp/x', recursive: 'true' }, {}, { path: false }]) {
    post('admin', '/api/devices/A1001/files/delete', body, yes(null));
}
for (const body of [{ from: '/a', to: '/b' }, { from: '/a' }, { to: '/b' }, { from: 1, to: 2 }]) {
    post('admin', '/api/devices/A1001/files/rename', body, yes({ ok: 1 }));
}
for (const body of [{ path: '/tmp/d' }, { path: '/tmp/d', recursive: false }, { path: '/tmp/d', recursive: 0 }, {}]) {
    post('admin', '/api/devices/A1001/files/mkdir', body, yes({}));
}
post('admin', '/api/devices/B1002/files/write', { path: '/x', data: 'y' });
post('admin', '/api/devices/ZZ9/files/write', { path: '/x', data: 'y' });
post('operator', '/api/devices/C1003/files/delete', { path: '/x' });
// Clipboard, screenshot, input, terminal
for (const body of [{ text: 'copy' }, {}, { text: 5 }, { text: '' }, { text: 'c'.repeat(1024 * 1024) }, { text: 'c'.repeat(1024 * 1024 + 1) }, { text: '\u{1F600}'.repeat(524288) }]) {
    post('admin', '/api/devices/A1001/clipboard', body, yes({ set: true }));
}
post('admin', '/api/devices/A1001/screenshot', {}, yes({ image: 'iVBORw0KGgo=', width: 1, height: 1 }));
post('operator', '/api/devices/A1001/screenshot', {}, yes({ image: '' }));
for (const body of [{ action: 'click', x: 10.7, y: -0.5, button: 'left' }, {}, { action: 7, button: 3, x: '1', y: null },
    { action: '', x_rel: 0.25, y_rel: -1e-7, screen_w: 1920.9, screen_h: 1080, wheel_dx: -3.9, wheel_dy: 1e21 }, { x: -0 }, [1]]) {
    post('admin', '/api/devices/A1001/input/mouse', body, yes(null));
}
for (const body of [{ key: 'Enter' }, {}, { key: 'k'.repeat(40), action: 'down' }, { key: 9, action: 0 }, { key: ['a', 'b'] }, { key: '\u{1F600}'.repeat(20) }]) {
    post('admin', '/api/devices/A1001/input/key', body, yes(null));
}
for (const body of [{ text: 'hello' }, {}, { text: 0 }, { text: 't'.repeat(5000) }, { text: { a: 1 } }]) {
    post('admin', '/api/devices/A1001/input/text', body, yes(null));
}
for (const body of [{ command: 'uptime' }, {}, { command: '  \t ' }, { command: 'echo ' + 'x'.repeat(5000) }, { command: 42 }, { command: ' ' }]) {
    post('admin', '/api/devices/A1001/terminal/execute', body, yes({ stdout: 'up', code: 0 }));
}
post('operator', '/api/devices/A1001/terminal/execute', { command: 'id' }, no('denied'));
post('admin', '/api/devices/A1001/input/key', { key: 'a' }, 'silent');
sql('audit', 'SELECT user_id, action, details, ip_address FROM audit_log ORDER BY id');

// ---- The run ----

(async () => {
    const api = await startAPI();
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${api.address().port}/api`;
    const db = await openDatabase();
    for (const [query, params] of seed) await db.exec(query, params);

    const express = web('node_modules/express');
    const WebSocket = web('node_modules/ws');
    const bdRelay = web('services/bdRelay');
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
    app.use((req, res) => res.status(404).json({ oracle: 'no route' }));
    // eslint-disable-next-line no-unused-vars
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ oracle: 'error handler', status: err.status || 500 }));
    const server = http.createServer(app);
    bdRelay.initBdRelay(server);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;

    // The agent
    const agent = { got: [], answer: undefined };
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/bd-signal?device_id=A1001&token=agent-token-A1001`);
    await new Promise((resolve, reject) => { ws.on('open', resolve); ws.on('error', reject); });
    ws.on('message', (data) => {
        const msg = JSON.parse(data.toString());
        if (msg.type === 'welcome') return;
        agent.got.push(data.toString().replace(msg.request_id, '<id>'));
        const a = agent.answer;
        if (a && a !== 'silent') ws.send(JSON.stringify({ type: 'command_response', request_id: msg.request_id, ...a }));
    });
    await new Promise((r) => setTimeout(r, 100));

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
            agent.got = [];
            agent.answer = s.agent;
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
            out.push({ ...s, status: answer.status, answer: answer.body, extra: { calls: apiState.calls, agent: agent.got } });
        }
    } finally {
        Object.assign(console, quiet);
        ws.close();
        server.close();
        api.close();
        await db.close();
    }
    const file = path.join(__dirname, '..', `node_agent_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed, table: TABLE, steps: out }, null, 1)}\n`);
    console.log(`${file}: ${out.length} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
