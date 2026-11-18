'use strict';

/**
 * Runs the old console's services/remoteTargetGateway.js on a real HTTP
 * server in front of a made-up API server and plays scenarios against it:
 * browser sockets opened with raw upgrade requests, messages, closes.
 * After each step it records what every browser socket got (the answer's
 * head, messages, the close frame, the end of the connection), what the
 * API server's tunnel socket got (the handshake's path and headers,
 * messages, the close frame) and which outside functions the gateway
 * called (permission lookup, target lookup, scope check). The Go tests
 * play the same steps against a made-up API server that behaves the same
 * and must see the same. See README.md.
 *
 *   node gateway.js      writes ../node_gateway.json
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const path = require('path');
const crypto = require('crypto');

// A long message is noted by its length and SHA-256.
const bin = (data) => (data.length > 4096
    ? `big ${data.length} ${crypto.createHash('sha256').update(data).digest('hex')}`
    : data.toString('base64'));
const txt = (data) => (data.length > 4096 ? bin(data) : data.toString('utf8'));

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const realTimeout = setTimeout;

// ---- What the gateway reaches out to ----

let calls = [];
const tick = () => new Promise((r) => setImmediate(r));
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
// Targets the API server knows; the account "scoped" sees rt_echo-0001
// only, "scopethrow" makes the scope check fail.
const known = new Set(['rt_echo-0001', 'rt_scoped-01', 'rt_refuse-01', 'rt_slow-0001', 'rt_close1000', 'rt_close1011',
    'rt_drop-0001', 'rt_big-00001', 'rt_hello-001', 'rt_end-00001']);
stub('services/database.js', {});
stub('services/betterdeskApi.js', {
    getRemoteTarget: async (id) => {
        calls.push({ getRemoteTarget: id });
        await tick();
        return known.has(id) ? { id, hostname: id, remote_target: true } : null;
    },
});
stub('services/deviceGroupService.js', {
    userCanAccessDevice: async (_db, user, target) => {
        calls.push({ scope: [user, target.id] });
        await tick();
        if (user.username === 'scopethrow') throw new Error('scope down');
        if (user.username === 'scoped') return target.id === 'rt_echo-0001';
        return true;
    },
});
stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, permission) => {
        calls.push({ permission: [role, permission] });
        await tick();
        if (role === 'throw') throw new Error('roles down');
        return (role === 'admin' || role === 'operator') && permission === 'remote_target.connect';
    },
});

// ---- The made-up API server ----

const WebSocket = require(require.resolve('ws', { paths: [WEB] }));
let events = [];
let upstreamSeq = 0;
const api = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
const apiWss = new WebSocket.Server({ noServer: true, maxPayload: 64 * 1024 * 1024 });
const HEADERS = ['x-api-key', 'x-username', 'x-role', 'origin', 'sec-websocket-protocol', 'sec-websocket-version', 'cookie', 'authorization'];
api.on('upgrade', (req, socket, head) => {
    const name = `up${++upstreamSeq}`;
    const note = (e) => events.push({ socket: name, ...e });
    const picked = {};
    for (const h of HEADERS) if (req.headers[h] !== undefined) picked[h] = req.headers[h];
    note({ handshake: { method: req.method, url: req.url, headers: picked } });
    const id = (req.url.match(/^\/api\/remote-targets\/([^/]+)\/tunnel$/) || [])[1] || '';
    if (id.startsWith('rt_refuse')) {
        socket.write('HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n{"error":"RDP/VNC gateway is not configured"}');
        socket.destroy();
        return;
    }
    const accept = () => apiWss.handleUpgrade(req, socket, head, (ws) => {
        ws.on('message', (data, isBinary) => {
            const text = data.toString('utf8');
            note(isBinary ? { binary: bin(data) } : { text: txt(data) });
            if (isBinary) { ws.send(data, { binary: true }); return; }
            if (text === 'close-1000') ws.close(1000, 'session ended');
            else if (text === 'close-1011') ws.close(1011, 'gateway unavailable');
            else if (text === 'drop') ws.terminate();
            else if (text === 'big') ws.send(Buffer.alloc(16 * 1024 * 1024 + 1, 0x79));
            else if (text === 'at-limit') ws.send(Buffer.alloc(16 * 1024 * 1024, 0x79));
            else ws.send(`echo:${text}`);
        });
        ws.on('close', (code, reason) => note({ close: code, reason: reason.toString('utf8') }));
        ws.on('error', () => {});
        if (id.startsWith('rt_hello')) ws.send('hello');
        if (id.startsWith('rt_close1000')) ws.close(1000, 'session ended');
        if (id.startsWith('rt_close1011')) ws.close(1011, 'gateway unavailable');
        if (id.startsWith('rt_drop')) ws.terminate();
    });
    if (id.startsWith('rt_slow')) realTimeout(accept, 300); else accept();
});

// ---- A raw websocket client that notes everything ----

const sockets = new Map();

function openSocket(name, port, target, headers) {
    const sock = net.connect(port, '127.0.0.1');
    const s = { name, sock, buf: Buffer.alloc(0), head: null, ended: false };
    sockets.set(name, s);
    const note = (e) => events.push({ socket: name, ...e });
    sock.on('data', (chunk) => {
        s.buf = Buffer.concat([s.buf, chunk]);
        if (s.head === null) {
            const at = s.buf.indexOf('\r\n\r\n');
            if (at < 0) return;
            s.head = s.buf.subarray(0, at + 4).toString('latin1');
            s.buf = s.buf.subarray(at + 4);
            note({ head: s.head });
            if (!s.head.startsWith('HTTP/1.1 101')) {
                if (s.buf.length) note({ body: s.buf.toString('latin1') });
                s.buf = Buffer.alloc(0);
                return;
            }
        }
        for (;;) {
            if (s.buf.length < 2) return;
            const op = s.buf[0] & 0x0f;
            let len = s.buf[1] & 0x7f;
            let off = 2;
            if (len === 126) { if (s.buf.length < 4) return; len = s.buf.readUInt16BE(2); off = 4; }
            if (len === 127) { if (s.buf.length < 10) return; len = Number(s.buf.readBigUInt64BE(2)); off = 10; }
            if (s.buf.length < off + len) return;
            const payload = s.buf.subarray(off, off + len);
            s.buf = s.buf.subarray(off + len);
            if (op === 1) note({ text: txt(payload) });
            else if (op === 2) note({ binary: bin(payload) });
            else if (op === 8) {
                note({ close: payload.length >= 2 ? payload.readUInt16BE(0) : 1005, reason: payload.subarray(2).toString('utf8') });
                if (!s.closeSent) writeFrame(s, 8, payload.subarray(0, 2));
            }
        }
    });
    sock.on('close', () => { if (!s.ended) { s.ended = true; note({ end: true }); } });
    sock.on('error', () => {});
    const lines = [`GET ${target} HTTP/1.1`, 'Host: console.test', 'Upgrade: websocket', 'Connection: Upgrade',
        'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==', 'Sec-WebSocket-Version: 13'];
    for (const k of Object.keys(headers || {}).sort()) lines.push(`${k}: ${headers[k]}`);
    sock.write(lines.join('\r\n') + '\r\n\r\n');
}

function writeFrame(s, op, payload) {
    const mask = Buffer.from([1, 2, 3, 4]);
    let head;
    if (payload.length < 126) head = Buffer.from([0x80 | op, 0x80 | payload.length]);
    else if (payload.length <= 0xffff) { head = Buffer.alloc(4); head[0] = 0x80 | op; head[1] = 0x80 | 126; head.writeUInt16BE(payload.length, 2); }
    else { head = Buffer.alloc(10); head[0] = 0x80 | op; head[1] = 0x80 | 127; head.writeBigUInt64BE(BigInt(payload.length), 2); }
    const body = Buffer.from(payload);
    for (let i = 0; i < body.length; i++) body[i] ^= mask[i % 4];
    if (op === 8) s.closeSent = true;
    s.sock.write(Buffer.concat([head, mask, body]));
}

const quiet = (rounds = 15) => new Promise((resolve) => {
    let last = events.length + calls.length;
    let still = 0;
    const check = () => {
        const n = events.length + calls.length;
        if (n !== last) { last = n; still = 0; }
        if (++still >= rounds) return resolve();
        realTimeout(check, 20);
    };
    realTimeout(check, 20);
});

async function run(step, port) {
    if (step.open) openSocket(step.open, port, step.path, step.headers);
    if (step.send) {
        const s = sockets.get(step.send);
        if (step.text !== undefined) writeFrame(s, 1, Buffer.from(step.text));
        if (step.binary !== undefined) writeFrame(s, 2, Buffer.from(step.binary, 'base64'));
        if (step.repeat !== undefined) writeFrame(s, step.opcode || 2, Buffer.alloc(step.repeat, 0x78));
        if (step.close !== undefined) writeFrame(s, 8, Buffer.from([step.close >> 8, step.close & 0xff]));
        if (step.drop) s.sock.destroy();
    }
    await quiet(step.wait || 15);
    const out = { events, calls };
    events = [];
    calls = [];
    return out;
}

// ---- The scenarios ----

const S = [];
const scenario = (name, steps) => S.push({ name, steps });
const session = (name, role, extra) => JSON.stringify({ userId: 7, user: { id: 7, username: name, role }, ...(extra || {}) });
const as = (name, role, extra) => ({ 'X-Oracle-Session': session(name, role, extra) });
const op = as('olga', 'operator');
const t = (id) => `/ws/remote-target/${id}`;
const b64 = (s) => Buffer.from(s).toString('base64');

scenario('checks', [
    { open: 'a', path: t('rt_echo-0001'), headers: { ...op, Origin: 'https://evil.example' } },
    { open: 'b', path: t('rt_echo-0001'), headers: { Cookie: 'bd.sid=s%3Ax' } },
    { open: 'c', path: t('rt_echo-0001') },
    { open: 'd', path: t('rt_echo-0001'), headers: { 'X-Oracle-Session': JSON.stringify({ user: { username: 'x', role: 'admin' } }) } },
    { open: 'e', path: t('rt_echo-0001'), headers: as('vic', 'viewer') },
    { open: 'f', path: t('rt_echo-0001'), headers: as('thor', 'throw') },
    { open: 'g', path: t('rt_echo-0001'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: 3, role: 'admin' }) } },
    { open: 'h', path: t('rt_missing1'), headers: op },
    { open: 'i', path: t('rt_scoped-01'), headers: as('scoped', 'operator') },
    { open: 'j', path: t('rt_echo-0001'), headers: as('scopethrow', 'operator') },
    { open: 'k', path: t('rt_echo-0001'), headers: as('scoped', 'operator') },
    { open: 'l', path: t('rt_echo-0001'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: 9, user: { role: 'admin' } }) } },
    { open: 'm', path: t('rt_echo-0001'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: 12, role: 'operator', username: 'sso' }), Origin: 'http://console.test' } },
    { open: 'n', path: `${t('rt_echo-0001')}?x=1` },
    { open: 'o', path: t(`rt_${'a'.repeat(80)}`), headers: op },
]);

scenario('passing messages', [
    { open: 'a', path: t('rt_echo-0001'), headers: as('root', 'admin') },
    { send: 'a', text: 'hello there' },
    { send: 'a', binary: b64('\x00\x01binary\xff') },
    { send: 'a', text: '' },
    { send: 'a', text: 'at-limit', wait: 40 },
    { send: 'a', repeat: 16 * 1024 * 1024, wait: 40 },
    { send: 'a', text: 'close-1000' },
    { open: 'b', path: t('rt_echo-0001'), headers: op },
    { send: 'b', close: 1000 },
    { open: 'c', path: t('rt_echo-0001'), headers: op },
    { send: 'c', close: 4001 },
    { open: 'd', path: t('rt_echo-0001'), headers: op },
    { send: 'd', drop: true },
    { open: 'e', path: t('rt_echo-0001'), headers: op },
    { send: 'e', text: 'close-1011' },
    { open: 'f', path: t('rt_echo-0001'), headers: op },
    { send: 'f', text: 'drop' },
]);

scenario('the upstream side', [
    { open: 'a', path: t('rt_refuse-01'), headers: op },
    { open: 'b', path: t('rt_hello-001'), headers: op },
    { open: 'c', path: t('rt_close1000'), headers: op },
    { open: 'd', path: t('rt_close1011'), headers: op },
    { open: 'e', path: t('rt_drop-0001'), headers: op },
    { open: 'f', path: t('rt_slow-0001'), headers: op },
    { send: 'f', text: 'queued one' },
    { send: 'f', binary: b64('queued two') },
    { send: 'f', text: 'queued three', wait: 40 },
    { open: 'g', path: t('rt_slow-0001'), headers: op },
    { send: 'g', text: 'never sent' },
    { send: 'g', close: 1000, wait: 40 },
]);

scenario('sizes', [
    { open: 'a', path: t('rt_echo-0001'), headers: op },
    { send: 'a', text: 'big', wait: 40 },
    { open: 'b', path: t('rt_echo-0001'), headers: op },
    { send: 'b', repeat: 16 * 1024 * 1024 + 1, wait: 40 },
]);

// A name Node's HTTP client cannot put into a header: it throws while the
// tunnel is made, after the 101 went out, and the upgrade's catch writes
// its 502 behind it.
// The session JSON goes in the header with \u escapes, so the header
// itself is ASCII.
const named = (username) => ({ 'X-Oracle-Session': JSON.stringify({ userId: 7, user: { id: 7, username, role: 'operator' } }).replace(/[^\x20-\x7e]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`) });
scenario('names in headers', [
    { open: 'a', path: t('rt_echo-0001'), headers: named('\u0141ukasz') },
    { open: 'b', path: t('rt_echo-0001'), headers: { ...named('tab\tname\u0007'), 'Sec-WebSocket-Protocol': 'guacamole, other' } },
    { open: 'c', path: t('rt_echo-0001'), headers: named('tab\tname') },
    { send: 'c', text: 'still works' },
]);

(async () => {
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    await new Promise((r) => api.listen(0, '127.0.0.1', r));
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${api.address().port}/api`;
    process.env.BETTERDESK_API_KEY = 'oracle-api-key';
    const gateway = require(path.join(WEB, 'services/remoteTargetGateway.js'));
    const results = [];
    for (const sc of S) {
        const server = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
        gateway.initRemoteTargetGateway(server, (req, _res, next) => {
            const raw = req.headers['x-oracle-session'];
            req.session = raw ? JSON.parse(raw) : {};
            next();
        });
        await new Promise((r) => server.listen(0, '127.0.0.1', r));
        const port = server.address().port;
        upstreamSeq = 0;
        const steps = [];
        for (const step of sc.steps) steps.push({ step, ...(await run(step, port)) });
        for (const s of sockets.values()) s.sock.destroy();
        await quiet();
        events = [];
        calls = [];
        sockets.clear();
        server.close();
        results.push({ name: sc.name, steps });
    }
    const file = path.join(__dirname, '..', 'node_gateway.json');
    fs.writeFileSync(file, `${JSON.stringify({ scenarios: results }, null, 1)}\n`);
    process.stdout.write(`${file}: ${results.length} scenarios, ${results.reduce((n, r) => n + r.steps.length, 0)} steps\n`);
    process.exit(0);
})().catch((err) => { process.stderr.write(`${err.stack}\n`); process.exit(1); });
