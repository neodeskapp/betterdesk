'use strict';

/**
 * Runs the old console's services/meshAshxProxy.js on a real HTTP server,
 * in front of a made-up API server whose MeshCentral sockets do what each
 * scenario says, and plays scenarios against it: browser (or agent)
 * sockets opened with raw upgrade requests, messages from either side,
 * closes and dropped connections, an API server that holds the handshake
 * or refuses it. After each step it records what every client socket got
 * (the answer's head, text and binary messages, the close frame, the end
 * of the connection) and what the API server's side saw (the handshake's
 * path and query, subprotocol and X-Forwarded-For, each message with its
 * kind, the close). The Go tests play the same steps on the Go proxy and
 * must see the same. See README.md.
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

// ---- The made-up API server ----

const WebSocket = web('node_modules/ws');
const upstreamWss = new WebSocket.Server({ noServer: true });
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
        protocol: req.headers['sec-websocket-protocol'] === undefined ? '<none>' : req.headers['sec-websocket-protocol'],
        forwarded: req.headers['x-forwarded-for'] === undefined ? '<none>' : req.headers['x-forwarded-for'],
    });
    const accept = () => {
        if (plan.refuse) {
            socket.write(`HTTP/1.1 ${plan.refuse} Refused\r\nContent-Type: text/plain\r\nContent-Length: 3\r\nConnection: close\r\n\r\nno\n`);
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
            if (plan.greet) ws.send(Buffer.from(plan.greet), { binary: true });
        });
    };
    if (plan.hold) held.push(accept);
    else accept();
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
            const payload = Buffer.from(step.bytes, 'base64');
            ws._socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]));
        }
        if (step.close !== undefined) ws.close(step.close, step.reason || '');
        if (step.drop) ws.terminate();
    }
    await quiet();
    const out = { events };
    events = [];
    return out;
}

// ---- The scenarios ----

const S = [];
const scenario = (name, steps) => S.push({ name, steps });
// Header bytes reach the server as Latin-1: everything else goes as an escape
const session = (s) => ({ 'X-Oracle-Session': JSON.stringify(s).replace(/[\u007f-\uffff]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`) });
const signedIn = session({ userId: 7, user: { id: 7, username: 'u', role: 'viewer' } });
const b64 = (s) => Buffer.from(s).toString('base64');
const PATHS = ['/agent.ashx', '/meshrelay.ashx', '/control.ashx'];

const checks = [];
for (const p of PATHS) {
    const n = p.slice(1, p.indexOf('.'));
    checks.push(
        { open: `${n}-bare`, path: p },
        { open: `${n}-foreign`, path: p, headers: { Origin: 'https://evil.example' } },
        { open: `${n}-cookie`, path: p, headers: { Cookie: 'bd.sid=x' } },
        { open: `${n}-own`, path: p, headers: { Origin: 'http://console.test', ...signedIn } },
        { open: `${n}-zero`, path: p, headers: session({ userId: 0 }) },
        { open: `${n}-key`, path: p, headers: { 'Sec-WebSocket-Key': 'short' } },
        { open: `${n}-key-signed-in`, path: p, headers: { 'Sec-WebSocket-Key': 'short', ...signedIn } },
        { open: `${n}-protocol`, path: p, headers: { 'Sec-WebSocket-Protocol': 'mesh, other', ...signedIn } },
    );
}
scenario('checks', [{ plan: {} }, ...checks]);

scenario('paths and queries', [
    { plan: {} },
    { open: 'a', path: '/agent.ashx?id=1&auth=x%20y&z' },
    { open: 'b', path: '/meshrelay.ashx?' },
    { open: 'c', path: '/control.ashx?a=%zz&b=%27x+y', headers: signedIn },
    { open: 'd', path: '/x/../agent.ashx?p=2#frag' },
    { open: 'e', path: '/meshrelay.ashx?id=r1&browser=1&p=2&nodeid=n&auth=a', headers: { 'X-Forwarded-For': '10.9.8.7' } },
]);

for (const p of PATHS) {
    const headers = p === '/control.ashx' ? signedIn : {};
    scenario(`${p}: messages both ways`, [
        { plan: { echo: true } },
        { open: 'b', path: p, headers },
        { send: 'b', text: '{"action":"hello"}' },
        { send: 'b', binary: b64('\u0000\u0001bin') },
        { send: 'b', text: '' },
        { up: 'u1', text: 'from the server' },
        { up: 'u1', binary: Buffer.from([0, 255, 7]).toString('base64') },
        { send: 'b', close: 1000 },
    ]);
    scenario(`${p}: messages before the API server answers`, [
        { plan: { hold: true } },
        { open: 'b', path: p, headers },
        { send: 'b', text: 'lost' },
        { send: 'b', binary: b64('lost too') },
        { release: true },
        { send: 'b', text: 'kept' },
        { up: 'u1', close: 4001, reason: 'bye' },
    ]);
}

scenario('closes and failures', [
    { plan: { greet: 'hi' } },
    { open: 'b1', path: '/agent.ashx' },
    { up: 'u1', close: 1011, reason: 'agent error' },
    { send: 'b1', text: 'too late' },
    { open: 'b2', path: '/meshrelay.ashx?id=x' },
    { up: 'u2', drop: true },
    { open: 'b3', path: '/agent.ashx' },
    { send: 'b3', drop: true },
    { open: 'b4', path: '/control.ashx', headers: signedIn },
    { send: 'b4', close: 4000 },
    { open: 'b5', path: '/agent.ashx' },
    { send: 'b5', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    { open: 'b6', path: '/agent.ashx' },
    { up: 'u6', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    { send: 'b6', text: 'after the error' },
    { plan: { refuse: 401 } },
    { open: 'b7', path: '/control.ashx', headers: signedIn },
    { plan: { refuse: 400 } },
    { open: 'b8', path: '/meshrelay.ashx' },
    { plan: { hold: true } },
    { open: 'b9', path: '/agent.ashx' },
    { send: 'b9', close: 1000 },
    { release: true },
    { open: 'b10', path: '/agent.ashx' },
    { send: 'b10', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    { release: true },
]);

(async () => {
    const quietLog = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    await new Promise((r) => upstreamServer.listen(0, '127.0.0.1', r));
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${upstreamServer.address().port}/api`;
    const { initMeshAshxProxy } = web('services/meshAshxProxy.js');
    const results = [];
    for (const sc of S) {
        const server = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
        initMeshAshxProxy(server, sessionMiddleware);
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
        results.push({ name: sc.name, steps });
    }
    Object.assign(console, quietLog);
    const file = path.join(__dirname, '..', 'node_proxy.json');
    fs.writeFileSync(file, `${JSON.stringify({ scenarios: results }, null, 1)}\n`);
    console.log(`${file}: ${results.length} scenarios, ${results.reduce((n, r) => n + r.steps.length, 0)} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
