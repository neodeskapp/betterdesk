'use strict';

/**
 * Runs the old console's services/cdapTerminalProxy.js and
 * services/cdapMediaProxy.js on a real HTTP server, in front of a made-up
 * API server whose CDAP sockets do what each scenario says, and plays
 * scenarios against them: browser sockets opened with raw upgrade
 * requests, messages from the browser and from the API server's side,
 * closes and dropped connections on either side, an API server that holds
 * the handshake or refuses it. After each step it records what every
 * browser socket got (the answer's head, text and binary messages, the
 * close frame, the end of the connection), what the API server's side saw
 * (the handshake's path, subprotocol and user headers, each message with
 * its kind, the close) and every permission lookup. The Go tests play the
 * same steps on the Go proxy and must see the same. See README.md.
 *
 *   node proxy.js     writes ../node_proxy.json
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const web = (rel) => require(path.join(WEB, rel));

let events = [];

// ---- The made-up API server: its CDAP sockets follow the scenario ----

const WebSocket = web('node_modules/ws');
const upstreamWss = new WebSocket.Server({ noServer: true, handleProtocols: (protocols) => protocols.values().next().value });
// upstream: name -> {ws, held}
let upstreams = new Map();
let upstreamSeq = 0;
let plan = {};
const held = [];
const upstreamServer = http.createServer((_req, res) => { res.statusCode = 404; res.end(); });
upstreamServer.on('upgrade', (req, socket, head) => {
    const name = `u${++upstreamSeq}`;
    const note = (e) => events.push({ upstream: name, ...e });
    note({
        open: req.url,
        protocol: req.headers['sec-websocket-protocol'] || '',
        user: req.headers['x-username'] === undefined ? '<none>' : req.headers['x-username'],
        role: req.headers['x-role'] === undefined ? '<none>' : req.headers['x-role'],
    });
    const accept = () => {
        if (plan.refuse) {
            socket.write(`HTTP/1.1 ${plan.refuse} Refused\r\nContent-Type: application/json\r\nContent-Length: 15\r\nConnection: close\r\n\r\n{"error":"no"}\n`);
            socket.destroy();
            return;
        }
        upstreamWss.handleUpgrade(req, socket, head, (ws) => {
            upstreams.set(name, ws);
            ws.on('message', (data, isBinary) => {
                if (isBinary) note({ binary: data.toString('base64') });
                else note({ text: data.toString('utf8') });
                if (plan.echo) ws.send(data, { binary: isBinary });
            });
            ws.on('close', (code, reason) => note({ close: code, reason: reason.toString('utf8') }));
            ws.on('error', () => {});
            if (plan.greet) ws.send(plan.greet);
        });
    };
    if (plan.hold) held.push(accept);
    else accept();
});

// ---- What the proxies call: permissions and the session ----

const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
// The roles' permissions; "throw" makes the lookup fail
const PERMS = {
    admin: ['cdap.terminal', 'cdap.files', 'cdap.command'],
    global_admin: ['cdap.terminal', 'cdap.files', 'cdap.command'],
    operator: ['cdap.command'],
    viewer: [],
};
stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, permission) => {
        events.push({ permission: [String(role), permission] });
        if (role === 'throw') throw new Error('lookup failed');
        return (PERMS[role] || []).includes(permission);
    },
});
function sessionMiddleware(req, _res, next) {
    const raw = req.headers['x-oracle-session'];
    setImmediate(() => {
        req.session = raw !== undefined ? JSON.parse(raw) : {};
        next();
    });
}

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
            else if (len === 127) { if (s.buf.length < 10) return; len = Number(s.buf.readBigUInt64BE(2)); off = 10; }
            if (s.buf.length < off + len) return;
            const payload = s.buf.subarray(off, off + len);
            s.buf = s.buf.subarray(off + len);
            if (op === 1) note({ text: payload.toString('utf8') });
            else if (op === 2) note({ binary: payload.toString('base64') });
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
    sock.write(Buffer.from(lines.join('\r\n') + '\r\n\r\n', 'latin1'));
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

const quiet = () => new Promise((resolve) => {
    let last = events.length;
    let still = 0;
    const tick = () => {
        if (events.length !== last) { last = events.length; still = 0; }
        if (++still >= 8) return resolve();
        setTimeout(tick, 10);
    };
    setTimeout(tick, 10);
});

// ---- Steps ----

async function run(step, port) {
    if (step.plan) plan = { ...step.plan };
    if (step.open) openSocket(step.open, port, step.path, step.headers);
    if (step.send) {
        const s = sockets.get(step.send);
        if (step.text !== undefined) writeFrame(s, 1, Buffer.from(step.text));
        if (step.binary !== undefined) writeFrame(s, 2, Buffer.from(step.binary, 'base64'));
        if (step.bytes !== undefined) writeFrame(s, 1, Buffer.from(step.bytes, 'base64'));
        if (step.close !== undefined) writeFrame(s, 8, Buffer.from([step.close >> 8, step.close & 0xff]));
        if (step.drop) s.sock.destroy();
    }
    if (step.release) while (held.length) held.shift()();
    if (step.up) {
        const ws = upstreams.get(step.up);
        if (step.text !== undefined) ws.send(step.text);
        if (step.binary !== undefined) ws.send(Buffer.from(step.binary, 'base64'), { binary: true });
        if (step.bytes !== undefined) {
            // A text frame that is not UTF-8, written past the library's check
            const payload = Buffer.from(step.bytes, 'base64');
            ws._socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]));
        }
        if (step.close !== undefined) ws.close(step.close, step.reason || '');
        if (step.drop) ws.terminate();
    }
    await quiet();
    if (crashed) {
        events.push({ crash: crashed });
        crashed = null;
    }
    const out = { events };
    events = [];
    return out;
}

// What would end the Node process: noted as {crash}, and every connection
// goes, as it would with the process
let crashed = null;
const crash = (err) => {
    crashed = err.message;
    for (const s of sockets.values()) s.sock.destroy();
};

// ---- The scenarios ----

const S = [];
const scenario = (name, steps) => S.push({ name, steps });
// Header bytes reach the server as Latin-1: everything else goes as an escape
const session = (s) => ({ 'X-Oracle-Session': JSON.stringify(s).replace(/[\u007f-\uffff]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`) });
const user = (role, name) => session({ userId: 7, user: { id: 7, username: name || 'u', role } });
const dev = (channel, id) => `/api/cdap/devices/${id || 'CDAP-1'}/${channel}`;
const b64 = (s) => Buffer.from(s).toString('base64');
const CHANNELS = [
    ['terminal', 'cdap-terminal'], ['desktop', 'cdap-desktop'], ['video', 'cdap-video'], ['files', 'cdap-filebrowser'], ['audio', 'cdap-audio'],
];

// Who may open which socket, and the order of the checks
const checks = [];
for (const [channel, protocol] of CHANNELS) {
    const p = { 'Sec-WebSocket-Protocol': protocol };
    checks.push(
        { open: `${channel}-none`, path: dev(channel), headers: p },
        { open: `${channel}-zero`, path: dev(channel), headers: { ...p, ...session({ userId: 0, user: { role: 'admin' } }) } },
        { open: `${channel}-foreign`, path: dev(channel), headers: { ...p, ...user('admin'), Origin: 'https://evil.example' } },
        { open: `${channel}-cookie`, path: dev(channel), headers: { ...p, ...user('admin'), Cookie: 'bd.sid=x' } },
        { open: `${channel}-viewer`, path: dev(channel), headers: { ...p, ...user('viewer') } },
        { open: `${channel}-operator`, path: dev(channel), headers: { ...p, ...user('operator', 'op') } },
        { open: `${channel}-throw`, path: dev(channel), headers: { ...p, ...user('throw') } },
        { open: `${channel}-global`, path: dev(channel), headers: { ...p, ...user('global_admin', 'ga') } },
        { open: `${channel}-flat`, path: dev(channel), headers: { ...p, ...session({ userId: 9, role: 'admin', username: 'flat' }) } },
        { open: `${channel}-key`, path: dev(channel), headers: { ...p, ...user('admin'), 'Sec-WebSocket-Key': 'short' } },
        { open: `${channel}-nokey-viewer`, path: dev(channel), headers: { ...p, ...user('viewer'), 'Sec-WebSocket-Key': 'short' } },
    );
}
scenario('checks', [{ plan: {} }, ...checks]);

scenario('ids and names', [
    { plan: {} },
    { open: 'a', path: dev('terminal', 'a'), headers: user('admin', 'x y') },
    { open: 'b', path: dev('desktop', 'A_b-9'.repeat(12) + 'abcd'), headers: user('admin') },
    { open: 'd', path: dev('files', 'Ab9'), headers: session({ userId: 4, user: { role: 'admin' } }) },
    { open: 'e', path: dev('audio', 'dev'), headers: session({ userId: '12', user: { role: 'admin', username: '' }, username: 'flatname' }) },
    { open: 'f', path: dev('terminal', 'x') + '?a=1&b', headers: { ...user('admin'), 'Sec-WebSocket-Protocol': 'one, two' } },
    { open: 'g', path: '/api/cdap/devices/./x/../t/terminal', headers: user('admin') },
    { open: 'h', path: dev('desktop', 'h'), headers: user('admin', 'José ÿ') },
]);

// A name the HTTP client cannot put into a header ends the Node process
// once the browser's socket is open
scenario('crash: a user name outside Latin-1', [
    { plan: {} },
    { open: 'other', path: dev('video'), headers: user('admin', 'fine') },
    { open: 'b', path: dev('terminal'), headers: user('admin', 'Zh中') },
]);
scenario('crash: a user name with a control character', [
    { plan: {} },
    { open: 'other', path: dev('video'), headers: user('admin', 'fine') },
    { open: 'b', path: dev('files'), headers: user('admin', 'a\u0001b') },
]);

for (const [channel, protocol] of CHANNELS) {
    const p = { 'Sec-WebSocket-Protocol': protocol, ...user('admin', 'anna') };
    scenario(`${channel}: messages both ways`, [
        { plan: { echo: true } },
        { open: 'b', path: dev(channel), headers: p },
        { send: 'b', text: '{"type":"init","cols":80}' },
        { send: 'b', binary: b64('\u0000\u0001binary') },
        { send: 'b', text: '' },
        { up: 'u1', text: '{"type":"ready"}' },
        { up: 'u1', binary: Buffer.from([0, 255, 7]).toString('base64') },
        { send: 'b', close: 1000 },
    ]);
    scenario(`${channel}: messages before the API server answers`, [
        { plan: { hold: true } },
        { open: 'b', path: dev(channel), headers: p },
        { send: 'b', text: 'first' },
        { send: 'b', binary: b64('second') },
        { send: 'b', text: 'third' },
        { release: true },
        { send: 'b', text: 'fourth' },
        { up: 'u1', close: 1000 },
    ]);
}

scenario('closes and failures', [
    { plan: { greet: 'hello' } },
    // the API server closes with a code: the browser gets a close without one
    { open: 'b1', path: dev('desktop'), headers: user('admin') },
    { up: 'u1', close: 1011, reason: 'desktop start failed' },
    { send: 'b1', text: 'too late' },
    // the API server's connection breaks
    { open: 'b2', path: dev('terminal'), headers: user('admin') },
    { up: 'u2', drop: true },
    // the browser's connection breaks, then a close with a code
    { open: 'b3', path: dev('video'), headers: user('operator') },
    { send: 'b3', drop: true },
    { open: 'b4', path: dev('files'), headers: user('admin') },
    { send: 'b4', close: 4000 },
    // text that is not UTF-8 from the browser, and from the API server
    { open: 'b5', path: dev('audio'), headers: user('admin') },
    { send: 'b5', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    { open: 'b6', path: dev('desktop'), headers: user('admin') },
    { up: 'u6', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    { send: 'b6', text: 'after the error' },
    { open: 'b7', path: dev('terminal'), headers: user('admin') },
    { up: 'u7', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    // the API server refuses the handshake
    { plan: { refuse: 403 } },
    { open: 'b8', path: dev('terminal'), headers: user('admin') },
    { open: 'b9', path: dev('video'), headers: user('operator') },
    { plan: { refuse: 503 } },
    { open: 'b10', path: dev('files'), headers: user('admin') },
    // the browser leaves while the API server holds the handshake
    { plan: { hold: true } },
    { open: 'b11', path: dev('desktop'), headers: user('admin') },
    { send: 'b11', text: 'queued' },
    { send: 'b11', close: 1000 },
    { release: true },
    { open: 'b12', path: dev('terminal'), headers: user('admin') },
    { send: 'b12', drop: true },
    { release: true },
]);

(async () => {
    const quietLog = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    process.on('uncaughtException', crash);
    process.on('unhandledRejection', crash);
    await new Promise((r) => upstreamServer.listen(0, '127.0.0.1', r));
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${upstreamServer.address().port}/api`;
    process.env.BETTERDESK_API_KEY = 'oracle-key';
    const { initCdapTerminalProxy } = web('services/cdapTerminalProxy.js');
    const { initCdapMediaProxies } = web('services/cdapMediaProxy.js');
    const results = [];
    for (const sc of S) {
        const server = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
        initCdapTerminalProxy(server, sessionMiddleware);
        initCdapMediaProxies(server, sessionMiddleware);
        await new Promise((r) => server.listen(0, '127.0.0.1', r));
        const port = server.address().port;
        upstreamSeq = 0;
        upstreams = new Map();
        plan = {};
        const steps = [];
        for (const step of sc.steps) steps.push({ step, ...(await run(step, port)) });
        for (const s of sockets.values()) s.sock.destroy();
        for (const ws of upstreams.values()) ws.terminate();
        while (held.length) held.shift()();
        await quiet();
        events = [];
        sockets.clear();
        server.close();
        results.push({ name: sc.name, crash: sc.name.startsWith('crash:'), steps });
    }
    Object.assign(console, quietLog);
    const file = path.join(__dirname, '..', 'node_proxy.json');
    fs.writeFileSync(file, `${JSON.stringify({ scenarios: results }, null, 1)}\n`);
    console.log(`${file}: ${results.length} scenarios, ${results.reduce((n, r) => n + r.steps.length, 0)} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
