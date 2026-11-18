'use strict';

/**
 * Runs the old console's services/deviceStatusPush.js on a real HTTP
 * server, in front of a made-up event socket of the API server, and records
 * (1) what the browsers on /ws/device-status and /ws/panel-events get for
 * each event frame, with the device id changes it carried into the
 * database, and (2) the raw answer to upgrade requests: refusals, the
 * library's handshake checks, the handshake. See README.md.
 *
 *   node push.js     writes ../node_push.json
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const web = (rel) => require(path.join(WEB, rel));
const NOW = 1700000000123;

// ---- Stubs, in place before the service is loaded ----

const cascades = [];
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('services/database.js', { cascadePeerIdChange: (a, b) => { cascades.push([a, b]); } });
// The permission lookup: a few roles have enrollment.approve, "broken"
// makes the lookup fail, every other role lacks it.
const ALLOWED = new Set(['admin', 'super_admin', 'global_admin', 'allowed']);
stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, permission) => {
        if (role === 'broken') throw new Error('lookup failed');
        return permission === 'enrollment.approve' && ALLOWED.has(role);
    },
});
Date.now = () => NOW;

const WebSocket = web('node_modules/ws');
const { initDeviceStatusPush } = web('services/deviceStatusPush.js');

// The session middleware: the session is the JSON in X-Oracle-Session,
// none without the header. It runs asynchronously, as the real one does.
function sessionMiddleware(req, _res, next) {
    const raw = req.headers['x-oracle-session'];
    setImmediate(() => {
        if (raw !== undefined) req.session = JSON.parse(raw);
        next();
    });
}

// ---- Event frames as the API server's event socket writes them ----

// Go's encoding/json: object keys sorted (the data map), <, > and & and
// the two line separators escaped.
function goJSON(value) {
    // JSON.stringify writes array-index keys first whatever the order, so
    // the data object is written by hand in Go's order.
    const write = (v) => {
        if (v && typeof v === 'object' && !Array.isArray(v)) {
            const keys = Object.keys(v).sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
            return '{' + keys.map((k) => JSON.stringify(k) + ':' + write(v[k])).join(',') + '}';
        }
        return JSON.stringify(v);
    };
    return write(value).replace(/</g, '\\u003c').replace(/>/g, '\\u003e').replace(/&/g, '\\u0026')
        .replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
}

function frame(type, timestamp, data) {
    let text = '{"type":' + goJSON(type);
    if (timestamp !== undefined) text += ',"timestamp":' + goJSON(timestamp);
    if (data && Object.keys(data).length > 0) text += ',"data":' + goJSON(data);
    return text + '}';
}

// A deterministic pseudo-random source, so the vectors are the same on
// every run.
let seed = 42;
const rnd = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
const pick = (list) => list[rnd(list.length)];

const TYPES = ['peer_online', 'peer_offline', 'peer_status_changed', 'peer_registered', 'peer_id_changed',
    'device_pending', 'enrollment_pending', 'device_approved', 'device_rejected', 'enrollment_rejection_cleared',
    'help_request', 'peer_degraded', 'peer_banned', 'chat_message', 'server_stats', '', 'PEER_ONLINE', 'peer_online '];
const KEYS = ['id', 'peer_id', 'status', 'old_id', 'new_id', 'source', 'type', 'timestamp', 'device_id', 'hostname',
    'message', 'org_id', 'handled_by', 'ip', 'platform', '0', '1', '10', '2', '007', '4294967295', '4294967294', '-1',
    'a b', 'Z', 'é', '\u{1F600}', 'data', '__proto__', 'constructor', ''];
const VALUES = ['', 'online', 'offline', '900100001', 'ABC-123', 'panel', 'client', '0', 'false', 'null', ' ',
    '<script>&"\'', 'line\nbreak\ttab', '\u2028\u2029', '\u0001\u001f\u007f', 'é ü ß', '\u{1F600}', 'back\\slash', 'x'.repeat(300)];
const STAMPS = ['2026-11-10T23:40:14.212809301Z', '2026-11-10T23:40:14Z', '2026-11-11T00:40:14.5+01:00', '0001-01-01T00:00:00Z', ''];

const vectors = [];
const add = (type, timestamp, data) => vectors.push({ frame: frame(type, timestamp, data) });
// Every handled type with typical data
add('peer_online', STAMPS[0], { id: '900100001', status: 'online' });
add('peer_offline', STAMPS[0], { id: '900100001' });
add('peer_offline', STAMPS[0], {});
add('peer_status_changed', STAMPS[1], { peer_id: 'A', id: 'B', status: 'degraded' });
add('peer_registered', STAMPS[2], { status: '' });
add('peer_id_changed', STAMPS[0], { old_id: '900100018', new_id: '900100098', source: 'panel' });
add('peer_id_changed', STAMPS[0], { old_id: '900100018', new_id: '' });
add('peer_id_changed', STAMPS[0], { new_id: 'X' });
add('peer_id_changed', STAMPS[0], {});
add('enrollment_pending', STAMPS[0], { device_id: 'D1', ip: '10.0.0.1', hostname: 'h', platform: 'Linux' });
add('device_pending', '', { device_id: 'D1' });
add('device_approved', STAMPS[0], { device_id: 'D1' });
add('device_rejected', STAMPS[0], {});
add('enrollment_rejection_cleared', STAMPS[0], { id: '7' });
add('help_request', STAMPS[0], { id: '12', device_id: '900100001', hostname: 'h', org_id: '', message: 'Hi', status: 'pending' });
add('help_request', STAMPS[0], { type: 'evil', timestamp: 'mine', '2': 'two', '1': 'one', b: 'x' });
add('help_request', STAMPS[0], {});
add('peer_online', '', { id: '900100001' });
for (const t of TYPES) add(t, STAMPS[0], { id: 'T' });
// Random events
for (let i = 0; i < 1500; i++) {
    const data = {};
    const n = rnd(6);
    for (let j = 0; j < n; j++) {
        // defineProperty, so "__proto__" is a key like the others
        Object.defineProperty(data, pick(KEYS), { value: pick(VALUES), enumerable: true, writable: true, configurable: true });
    }
    add(pick(TYPES), pick(STAMPS), data);
}

// ---- The run ----

function connectBrowser(port, p, session) {
    return new Promise((resolve, reject) => {
        const ws = new WebSocket(`ws://127.0.0.1:${port}${p}`, { headers: { 'X-Oracle-Session': JSON.stringify(session) } });
        const got = [];
        ws.on('message', (data) => got.push(data.toString()));
        ws.on('open', () => resolve({ ws, got }));
        ws.on('error', reject);
    });
}

const until = async (cond, ms = 2000) => {
    const end = Date.now === undefined ? 0 : process.hrtime.bigint() + BigInt(ms) * 1000000n;
    while (!cond()) {
        if (process.hrtime.bigint() > end) throw new Error('timed out');
        await new Promise((r) => setTimeout(r, 2));
    }
};

// One raw upgrade request: the bytes of the answer until the server
// closes the connection, or the head of a 101 and 150 ms after it.
function rawRequest(port, text) {
    return new Promise((resolve) => {
        const sock = net.connect(port, '127.0.0.1');
        const chunks = [];
        let timer = null;
        const done = () => { clearTimeout(timer); sock.destroy(); resolve(Buffer.concat(chunks).toString('latin1')); };
        sock.on('data', (c) => {
            chunks.push(c);
            if (Buffer.concat(chunks).toString('latin1').startsWith('HTTP/1.1 101') && !timer) timer = setTimeout(done, 150);
        });
        sock.on('close', done);
        sock.on('error', done);
        timer = setTimeout(done, 3000);
        sock.write(text, 'latin1');
    });
}

const KEY = 'dGhlIHNhbXBsZSBub25jZQ==';
const base = (p, extra = '') => `GET ${p} HTTP/1.1\r\nHost: console.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ${KEY}\r\nSec-WebSocket-Version: 13\r\n${extra}\r\n`;
const as = (role) => `X-Oracle-Session: ${JSON.stringify(role === null ? { userId: 1 } : { userId: 1, user: { role } })}\r\n`;
const handshakes = [];
for (const p of ['/ws/device-status', '/ws/panel-events']) {
    handshakes.push({ name: `${p} signed out`, request: base(p) });
    handshakes.push({ name: `${p} empty session`, request: base(p, 'X-Oracle-Session: {}\r\n') });
    handshakes.push({ name: `${p} userId 0`, request: base(p, 'X-Oracle-Session: {"userId":0,"user":{"role":"admin"}}\r\n') });
    handshakes.push({ name: `${p} userId text`, request: base(p, 'X-Oracle-Session: {"userId":"7","user":{"role":"admin"}}\r\n') });
    for (const role of ['admin', 'viewer', 'broken', null, 'allowed']) {
        handshakes.push({ name: `${p} as ${role}`, request: base(p, as(role)) });
    }
    handshakes.push({ name: `${p} role number`, request: base(p, 'X-Oracle-Session: {"userId":1,"user":{"role":5}}\r\n') });
    handshakes.push({ name: `${p} session role only`, request: base(p, 'X-Oracle-Session: {"userId":1,"role":"admin"}\r\n') });
    const admin = as('admin');
    handshakes.push({ name: `${p} POST`, request: base(p, admin).replace(/^GET/, 'POST') });
    handshakes.push({ name: `${p} upgrade h2c`, request: base(p, admin).replace('Upgrade: websocket', 'Upgrade: h2c') });
    handshakes.push({ name: `${p} upgrade WebSocket`, request: base(p, admin).replace('Upgrade: websocket', 'Upgrade: WebSocket') });
    handshakes.push({ name: `${p} two upgrades`, request: base(p, admin + 'Upgrade: websocket\r\n') });
    handshakes.push({ name: `${p} no key`, request: base(p, admin).replace(`Sec-WebSocket-Key: ${KEY}\r\n`, '') });
    handshakes.push({ name: `${p} short key`, request: base(p, admin).replace(KEY, 'abc') });
    handshakes.push({ name: `${p} key without padding`, request: base(p, admin).replace(KEY, 'dGhlIHNhbXBsZSBub25jZQ') });
    handshakes.push({ name: `${p} two keys`, request: base(p, admin + `Sec-WebSocket-Key: ${KEY}\r\n`) });
    for (const v of ['12', '8', '', ' 13 ', '0xd', '13.0', '1e1', 'thirteen']) {
        handshakes.push({ name: `${p} version ${JSON.stringify(v)}`, request: base(p, admin).replace('Sec-WebSocket-Version: 13', `Sec-WebSocket-Version: ${v}`) });
    }
    handshakes.push({ name: `${p} no version`, request: base(p, admin).replace('Sec-WebSocket-Version: 13\r\n', '') });
    for (const proto of ['chat', 'b, a', 'a,b', 'a,,b', 'a, a', ',a', 'a b', 'x/y', 'a;b', '\ta', 'a\t,b']) {
        handshakes.push({ name: `${p} protocol ${JSON.stringify(proto)}`, request: base(p, admin + `Sec-WebSocket-Protocol: ${proto}\r\n`) });
    }
    handshakes.push({ name: `${p} two protocol headers`, request: base(p, admin + 'Sec-WebSocket-Protocol: a\r\nSec-WebSocket-Protocol: b\r\n') });
    handshakes.push({ name: `${p} extensions`, request: base(p, admin + 'Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits\r\n') });
    handshakes.push({ name: `${p} foreign origin`, request: base(p, admin + 'Origin: https://evil.example\r\n') });
}
handshakes.push({ name: 'dots resolve', request: base('/ws/./x/../device-status?a=b', as('admin')) });
handshakes.push({ name: 'backslash', request: base('/ws\\device-status', as('admin')) });
handshakes.push({ name: 'encoded dot segment', request: base('/ws/%2e/device-status', as('admin')) });
handshakes.push({ name: 'absolute form', request: base('http://other.test/ws/device-status', as('admin')) });

(async () => {
    const quiet = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    const bus = new WebSocket.Server({ host: '127.0.0.1', port: 0, path: '/api/ws/events' });
    await new Promise((r) => bus.on('listening', r));
    let goSocket = null;
    bus.on('connection', (s) => { goSocket = s; });
    const server = http.createServer((_req, res) => { res.statusCode = 404; res.end(); });
    initDeviceStatusPush(server, sessionMiddleware, `http://127.0.0.1:${bus.address().port}/api`, 'oracle-key');
    await new Promise((r) => server.listen(0, '127.0.0.1', r));
    const port = server.address().port;
    await until(() => goSocket !== null, 5000);

    const status = await connectBrowser(port, '/ws/device-status', { userId: 1, user: { role: 'viewer' } });
    const panel = await connectBrowser(port, '/ws/panel-events', { userId: 1, user: { role: 'admin' } });
    const out = [];
    for (let i = 0; i < vectors.length; i++) {
        cascades.length = 0;
        goSocket.send(vectors[i].frame);
        // Markers: a frame each socket gets for sure, after the vector's.
        goSocket.send(`{"type":"peer_online","timestamp":"m","data":{"id":"marker-${i}"}}`);
        goSocket.send(`{"type":"device_pending","timestamp":"m","data":{"marker":"${i}"}}`);
        await until(() => status.got.some((t) => t.includes(`"marker-${i}"`)) && panel.got.some((t) => t.includes(`"marker":"${i}"`)));
        const take = (got, marker) => {
            const at = got.findIndex((t) => t.includes(marker));
            const before = got.splice(0, at + 1);
            return before.slice(0, -1);
        };
        out.push({
            frame: vectors[i].frame,
            status: take(status.got, `"marker-${i}"`),
            panel: take(panel.got, `"marker":"${i}"`),
            cascades: cascades.slice(),
        });
    }
    status.ws.close();
    panel.ws.close();

    const answers = [];
    for (const h of handshakes) {
        answers.push({ name: h.name, request: h.request, answer: await rawRequest(port, h.request) });
    }
    Object.assign(console, quiet);
    server.close();
    bus.close();
    const file = path.join(__dirname, '..', 'node_push.json');
    fs.writeFileSync(file, `${JSON.stringify({ now: NOW, events: out, handshakes: answers }, null, 1)}\n`);
    console.log(`${file}: ${out.length} events, ${answers.length} handshakes`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
