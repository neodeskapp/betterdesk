'use strict';

/**
 * Runs the old console's services/remoteRelay.js on a real HTTP server with
 * a made-up clock and a made-up random source, and plays scenarios against
 * it: sockets opened with raw upgrade requests, messages, the clock moved
 * on, calls of the functions the routes used. After each step it records
 * what every socket got (the answer's head, text and binary messages, the
 * close frame, the end of the connection), what a call returned and which
 * outside functions the service called (device lookup, scope check, token
 * check, permission lookup). The Go tests play the same steps on the Go
 * service and must see the same. See README.md.
 *
 *   node remote.js              writes ../node_remote.json
 *   node remote.js --crash <n>  runs crash case n and stays up; the parent
 *                               notes whether the process ended
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

// ---- The clock: Date.now and the timers remoteRelay.js starts ----

const real = { setTimeout, clearTimeout, setInterval, clearInterval };
let clock = 1700000000000;
let timerSeq = 0;
const timers = new Map();
const fakeTimer = (fn, delay, repeat, args) => {
    const id = { id: ++timerSeq, unref() { return this; }, ref() { return this; }, hasRef() { return true; }, [Symbol.toPrimitive]() { return this.id; } };
    timers.set(id.id, { at: clock + Math.max(1, delay | 0), delay: Math.max(1, delay | 0), fn, repeat, args, seq: id.id });
    return id;
};
global.setTimeout = (fn, delay, ...args) => fakeTimer(fn, delay, false, args);
global.setInterval = (fn, delay, ...args) => fakeTimer(fn, delay, true, args);
global.clearTimeout = (t) => { if (t) timers.delete(typeof t === 'object' ? t.id : t); };
global.clearInterval = global.clearTimeout;
Date.now = () => clock;
function advance(ms) {
    const until = clock + ms;
    for (;;) {
        let next = null;
        for (const [key, t] of timers) {
            if (t.at <= until && (!next || t.at < next.t.at || (t.at === next.t.at && t.seq < next.t.seq))) next = { key, t };
        }
        if (!next) break;
        clock = next.t.at;
        if (next.t.repeat) {
            next.t.at += next.t.delay;
        } else {
            timers.delete(next.key);
        }
        next.t.fn(...next.t.args);
    }
    clock = until;
}

// ---- The random source: the same bytes the Go test reads ----

let rngState = 0x2545f491;
function randomByte() {
    rngState ^= (rngState << 13) >>> 0;
    rngState ^= rngState >>> 17;
    rngState ^= (rngState << 5) >>> 0;
    rngState >>>= 0;
    return rngState & 0xff;
}
const origRandomBytes = crypto.randomBytes;
crypto.randomBytes = (n, ...rest) => (n === 32 && rest.length === 0
    ? Buffer.from(Array.from({ length: 32 }, randomByte))
    : origRandomBytes(n, ...rest));

// ---- What the service reaches out to ----

// calls notes each outside call of a step: lookups, scope, token, role.
let calls = [];
const devices = {
    dev1: { id: 'dev1', hostname: 'one', tags: [], folder_id: null },
    dev2: { id: 'dev2', hostname: 'two', tags: ['lab'], folder_id: 3 },
    'dev-3': { id: 'dev-3', hostname: 'three', tags: [], folder_id: null },
};
const tick = () => new Promise((r) => setImmediate(r));
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('services/database.js', {
    getPeerById: async (id) => {
        calls.push({ getPeerById: id });
        await tick();
        if (id === 'dev-throw') throw new Error('database down');
        return devices[id] || null;
    },
});
stub('services/deviceGroupService.js', {
    // The account "scoped" sees dev1 only; "scopethrow" makes the check fail
    userCanAccessDevice: async (_db, user, device) => {
        calls.push({ scope: [user === undefined ? '<undefined>' : user, device.id] });
        await tick();
        const name = user && user.username;
        if (name === 'scopethrow') throw new Error('scope down');
        if (name === 'scoped') return device.id === 'dev1';
        return true;
    },
});
stub('lib/deviceTokenAuth.js', {
    verifyDeviceWsAuth: async (deviceId, token) => {
        calls.push({ verify: [deviceId, token] });
        await tick();
        if (token === 'tok-throw') throw new Error('database down');
        return token === `tok-${deviceId}-good`;
    },
});
stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, permission) => {
        calls.push({ permission: [role === undefined ? '<undefined>' : role, permission] });
        await tick();
        if (role === 'throw') throw new Error('roles down');
        return (role === 'admin' || role === 'operator') && permission === 'device.connect';
    },
});

// Each scenario gets the module afresh: its sessions and tokens live in
// module state.
const RELAY = require.resolve(path.join(WEB, 'services/remoteRelay.js'));
let remoteRelay = null;

// The session is the JSON in X-Oracle-Session, none without it.
function sessionMiddleware(req, _res, next) {
    const raw = req.headers['x-oracle-session'];
    req.session = raw ? JSON.parse(raw) : {};
    next();
}

// ---- A raw websocket client that notes everything ----

const sockets = new Map();
let events = [];

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
            if (op === 1) note({ text: payload.toString('utf8') });
            else if (op === 2) note({ binary: payload.toString('base64') });
            else if (op === 8) {
                note({ close: payload.length >= 2 ? payload.readUInt16BE(0) : 1005, reason: payload.subarray(2).toString('utf8') });
                if (!s.closeSent) writeFrame(s, 8, payload.subarray(0, 2));
            }
            // pings and pongs are not noted
        }
    });
    sock.on('close', () => { if (!s.ended) { s.ended = true; note({ end: true }); } });
    sock.on('error', () => {});
    const lines = [`GET ${target} HTTP/1.1`, 'Host: console.test', 'Upgrade: websocket', 'Connection: Upgrade',
        'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==', 'Sec-WebSocket-Version: 13'];
    for (const k of Object.keys(headers || {}).sort()) lines.push(`${k}: ${headers[k]}`);
    sock.write(lines.join('\r\n') + '\r\n\r\n');
}

function writeFrame(s, op, payload, fin = true) {
    const mask = Buffer.from([1, 2, 3, 4]);
    let head;
    if (payload.length < 126) head = Buffer.from([(fin ? 0x80 : 0) | op, 0x80 | payload.length]);
    else if (payload.length <= 0xffff) { head = Buffer.alloc(4); head[0] = (fin ? 0x80 : 0) | op; head[1] = 0x80 | 126; head.writeUInt16BE(payload.length, 2); }
    else { head = Buffer.alloc(10); head[0] = (fin ? 0x80 : 0) | op; head[1] = 0x80 | 127; head.writeBigUInt64BE(BigInt(payload.length), 2); }
    const body = Buffer.from(payload);
    for (let i = 0; i < body.length; i++) body[i] ^= mask[i % 4];
    if (op === 8) s.closeSent = true;
    s.sock.write(Buffer.concat([head, mask, body]));
}

// ---- Steps ----

// vars holds what calls returned (by their "as"): {t1.token}.
const vars = {};
const fill = (text) => text.replace(/\{([a-z0-9_]+)\.([A-Za-z0-9_]+)\}/g, (_, v, k) => String(vars[v][k]));

const quiet = () => new Promise((resolve) => {
    let last = events.length + calls.length;
    let still = 0;
    const check = () => {
        const n = events.length + calls.length;
        if (n !== last) { last = n; still = 0; }
        if (++still >= 6) return resolve();
        real.setTimeout(check, 10);
    };
    real.setTimeout(check, 10);
});

const session = (name, role, extra) => JSON.stringify({ userId: 7, user: { id: 7, username: name, role }, ...(extra || {}) });

async function run(step, port) {
    const out = {};
    if (step.open) openSocket(step.open, port, fill(step.path), step.headers);
    if (step.send) {
        const s = sockets.get(step.send);
        if (step.text !== undefined) writeFrame(s, 1, Buffer.from(fill(step.text)));
        if (step.binary !== undefined) writeFrame(s, 2, Buffer.from(step.binary, 'base64'));
        if (step.repeat !== undefined) writeFrame(s, step.opcode || 2, Buffer.alloc(step.repeat, 0x78));
        if (step.close !== undefined) writeFrame(s, 8, Buffer.from([step.close >> 8, step.close & 0xff]));
        if (step.drop) s.sock.destroy();
    }
    if (step.advance) {
        await quiet();
        advance(step.advance);
    }
    if (step.call) {
        const args = (step.args || []).map((a) => (typeof a === 'string' ? fill(a) : a));
        let result;
        switch (step.call) {
            case 'issue': result = remoteRelay.issueRemoteAgentToken(...args); break;
            case 'state': result = remoteRelay.getSessionState(...args); break;
            case 'active': result = remoteRelay.getActiveSessions(); break;
            default: throw new Error(`unknown call ${step.call}`);
        }
        if (step.as) vars[step.as] = result;
        out.result = result === undefined ? '<undefined>' : result;
    }
    await quiet();
    out.events = events;
    out.calls = calls;
    events = [];
    calls = [];
    return out;
}

// ---- The scenarios ----

const S = [];
const scenario = (name, steps) => S.push({ name, steps });
const agent = (id, token) => `/ws/remote-agent/${id}${token === undefined ? '' : `?token=${token}`}`;
const good = (id) => agent(id, `tok-${id}-good`);
const viewer = (id) => `/ws/remote-viewer/${id}`;
const as = (name, role, extra) => ({ 'X-Oracle-Session': session(name, role, extra) });
const op = as('olga', 'operator');
const b64 = (s) => Buffer.from(s).toString('base64');

scenario('agent checks', [
    { open: 'a', path: good('dev1'), headers: { Origin: 'https://evil.example' } },
    { open: 'b', path: good('dev1'), headers: { Cookie: 'bd.sid=s%3Ax' } },
    { open: 'c', path: good('ab') },
    { open: 'd', path: agent('a%20b1', 'tok-a%20b1-good') },
    { open: 'e', path: agent('a'.repeat(65), 'x') },
    { open: 'f', path: agent('dev1') },
    { open: 'g', path: agent('dev1', '') },
    { open: 'h', path: agent('dev1', 'tok-throw') },
    { open: 'i', path: agent('dev1', 'wrong') },
    { open: 'j', path: agent('dev1', 'tok-dev2-good') },
    { open: 'k', path: '/ws/remote-agent/dev1?token=wrong&token=tok-dev1-good' },
    { open: 'l', path: agent('%64ev1', 'tok-dev1-good') },
    { call: 'active' },
    { call: 'state', args: ['dev1'] },
    { open: 'm', path: agent('dev1', 'tok-dev1-good'), headers: { Origin: 'http://console.test' } },
    { open: 'n', path: agent('a_b-9', 'tok-a_b-9-good'), headers: { Origin: 'https://console.test', Cookie: 'bd.sid=s%3Ax' } },
    { open: 'o', path: agent('dev%2D9', 'tok-dev-9-good') },
    { open: 'p', path: agent('dev1', 'tok%2Ddev1%2Dgood') },
    { call: 'active' },
]);

scenario('single-use token', [
    { call: 'issue', args: ['dev1'], as: 't1' },
    { open: 'a', path: agent('dev1', '{t1.token}') },
    { open: 'b', path: agent('dev1', '{t1.token}') },
    { call: 'issue', args: ['dev1'], as: 't2' },
    { call: 'issue', args: ['dev1'], as: 't3' },
    { open: 'c', path: agent('dev1', '{t2.token}') },
    { open: 'd', path: agent('dev1', '{t3.token}') },
    { call: 'issue', args: ['dev2'], as: 't4' },
    { open: 'e', path: agent('dev1', '{t4.token}') },
    { open: 'f', path: agent('dev2', '{t4.token}x') },
    { open: 'g', path: agent('dev2', 'short') },
    { open: 'h', path: agent('dev2', '{t4.token}') },
    { call: 'issue', args: ['dev2'], as: 't5' },
    { advance: 60000 },
    { open: 'i', path: agent('dev2', '{t5.token}') },
    { call: 'issue', args: ['dev2'], as: 't6' },
    { advance: 60001 },
    { open: 'j', path: agent('dev2', '{t6.token}') },
    { open: 'k', path: agent('dev2', '{t6.token}') },
    { call: 'issue', args: ['dev-3'], as: 't7' },
    { open: 'l', path: agent('dev-3', 'tok-dev-3-good') },
    { open: 'm', path: agent('dev-3', '{t7.token}') },
    { call: 'state', args: ['dev2'] },
    { call: 'active' },
]);

scenario('viewer checks', [
    { open: 'a', path: viewer('dev1') },
    { open: 'b', path: viewer('dev1'), headers: { 'X-Oracle-Session': JSON.stringify({ user: { role: 'admin' } }) } },
    { open: 'c', path: viewer('dev1'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: 0, user: { role: 'admin' } }) } },
    { open: 'd', path: viewer('dev1'), headers: as('vic', 'viewer') },
    { open: 'e', path: viewer('dev1'), headers: as('thor', 'throw') },
    { open: 'f', path: viewer('dev1'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: 3, role: 'admin' }) } },
    { open: 'g', path: viewer('nodev'), headers: op },
    { open: 'h', path: viewer('dev2'), headers: as('scoped', 'operator') },
    { open: 'i', path: viewer('dev1'), headers: as('scopethrow', 'operator') },
    { open: 'j', path: viewer('dev-throw'), headers: op },
    { open: 'k', path: viewer('%64ev1'), headers: op },
    { open: 'l', path: viewer('dev1'), headers: { ...op, Origin: 'https://evil.example' } },
    { open: 'm', path: viewer('dev1'), headers: { ...as('scoped', 'operator'), Origin: 'http://console.test' } },
    { open: 'n', path: viewer('dev1'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: 9, user: { id: 9, username: 'root', role: 'admin' } }) } },
    { open: 'o', path: viewer('dev2'), headers: { 'X-Oracle-Session': JSON.stringify({ userId: '12', username: 'sso', role: 'operator', user: { role: 'operator' } }) } },
    { call: 'state', args: ['dev1'] },
    { call: 'state', args: ['nodev'] },
    { call: 'active' },
]);

scenario('streaming', [
    { open: 'agent', path: good('dev1') },
    { call: 'state', args: ['dev1'] },
    { open: 'v1', path: viewer('dev1'), headers: op },
    { call: 'state', args: ['dev1'] },
    { send: 'agent', binary: b64('\xff\xd8 frame one') },
    { send: 'agent', repeat: 2 * 1024 * 1024 },
    { send: 'agent', repeat: 2 * 1024 * 1024 + 1 },
    { send: 'agent', text: '{"type":"status","fps":30}' },
    { send: 'agent', text: '{"type":"pong"}' },
    { send: 'agent', text: '{"type":"other"}' },
    { send: 'agent', text: 'not json' },
    { send: 'agent', text: '5' },
    { send: 'agent', text: '"status"' },
    { send: 'agent', text: '[{"type":"status"}]' },
    { send: 'agent', text: ' {"type" : "status", "n": 1.0, "m": -0, "e": 1E400, "f": -1e-400, "g": 0.1, "h": 123456789012345678901234567890, "i": 1e21, "j": 1e-7, "k": 0.000001} ' },
    { send: 'agent', text: '{"type":"status","s":"\\u2028\\u2029\\ud800 \\udfff x\\ud83d\\ude00\\u0000\\u001f\\"\\\\\\/","t":" é"}' },
    { send: 'agent', text: '{"b":1,"type":"status","2":"x","1":"y","__proto__":{"type":"stop"},"a":[],"type":"status","b":2,"4294967295":0,"4294967294":1,"01":2}' },
    { send: 'agent', text: '{"type":"status","nested":{"z":1,"a":[true,false,null,{"1":0,"0":1}]}}' },
    { send: 'agent', text: '{"type":"status"}x' },
    { send: 'agent', text: '{"type":"STATUS"}' },
    { send: 'v1', text: '{"type":"input","event_type":"mouse_move","x":5,"y":7}' },
    { send: 'v1', text: '{"x":1,"type":"input","kind":"key","event_type":""}' },
    { send: 'v1', text: '{"type":"input"}' },
    { send: 'v1', text: '{"type":"input","event_type":7,"kind":"k"}' },
    { send: 'v1', text: '{"type":"input","event_type":{"a":1},"z":[1]}' },
    { send: 'v1', text: '{"type":"input","event_type":0,"kind":false}' },
    { send: 'v1', text: '{"2":2,"type":"input","event_type":"key","1":1,"__proto__":{"x":1},"type":"input"}' },
    { send: 'v1', text: '{"type":"input","event_type":"\\ud800","n":-0,"big":1e400}' },
    { send: 'v1', binary: b64('{"type":"stop"}') },
    { send: 'v1', text: 'nope' },
    { send: 'v1', text: '{"type":"other"}' },
    { send: 'v1', text: '[1]' },
    { send: 'v1', text: '{"__proto__":{"type":"stop"}}' },
    { send: 'v1', text: '{"type":"stop"}' },
    { call: 'state', args: ['dev1'] },
    { send: 'v1', text: '{"type":"stop"}' },
    { send: 'v1', text: '{"type":"input","event_type":"after_stop"}' },
    { open: 'v2', path: viewer('dev1'), headers: as('root', 'admin') },
    { call: 'state', args: ['dev1'] },
    { send: 'v1', close: 1000 },
    { call: 'state', args: ['dev1'] },
    { send: 'v2', drop: true },
    { call: 'state', args: ['dev1'] },
    { advance: 89999 },
    { advance: 1 },
    { call: 'state', args: ['dev1'] },
    { call: 'active' },
]);

scenario('viewer first', [
    { open: 'v1', path: viewer('dev2'), headers: op },
    { call: 'state', args: ['dev2'] },
    { send: 'v1', text: '{"type":"input","event_type":"lost"}' },
    { send: 'v1', text: '{"type":"stop"}' },
    { open: 'a1', path: good('dev2') },
    { send: 'a1', text: '{"type":"status","n":1}' },
    { open: 'a2', path: good('dev2') },
    { send: 'a1', text: '{"type":"status","from":"a1"}' },
    { send: 'a2', text: '{"type":"status","from":"a2"}' },
    { send: 'v1', text: '{"type":"input","event_type":"to_a2"}' },
    { call: 'state', args: ['dev2'] },
    { send: 'a2', drop: true },
    { call: 'state', args: ['dev2'] },
    { open: 'a3', path: good('dev2') },
    { send: 'a3', close: 1000 },
    { call: 'state', args: ['dev2'] },
    { advance: 90000 },
    { open: 'a4', path: good('dev2') },
    { send: 'v1', close: 1000 },
    { advance: 30000 },
    { open: 'v2', path: viewer('dev2'), headers: op },
    { advance: 90000 },
    { call: 'state', args: ['dev2'] },
    { send: 'v2', close: 1000 },
    { advance: 89000 },
    { open: 'v3', path: viewer('dev2'), headers: op },
    { advance: 5000 },
    { send: 'v3', close: 1000 },
    { advance: 90000 },
    { call: 'state', args: ['dev2'] },
    { call: 'active' },
]);

scenario('max viewers', [
    { open: 'agent', path: good('dev1') },
    { open: 'v1', path: viewer('dev1'), headers: op },
    { open: 'v2', path: viewer('dev1'), headers: op },
    { open: 'v3', path: viewer('dev1'), headers: op },
    { open: 'v4', path: viewer('dev1'), headers: op },
    { open: 'v5', path: viewer('dev1'), headers: op },
    { open: 'v6', path: viewer('dev1'), headers: op },
    { send: 'v6', text: '{"type":"stop"}' },
    { send: 'agent', text: '{"type":"status","to":"five"}' },
    { call: 'state', args: ['dev1'] },
    { send: 'v1', close: 1000 },
    { open: 'v7', path: viewer('dev1'), headers: op },
    { call: 'state', args: ['dev1'] },
    { open: 'w1', path: viewer('dev-3'), headers: op },
    { open: 'w2', path: viewer('dev-3'), headers: op },
    { open: 'w3', path: viewer('dev-3'), headers: op },
    { open: 'w4', path: viewer('dev-3'), headers: op },
    { open: 'w5', path: viewer('dev-3'), headers: op },
    { open: 'w6', path: viewer('dev-3'), headers: op },
    { call: 'state', args: ['dev-3'] },
    { call: 'active' },
]);

// ---- Crash cases: does the Node process end? ----

const deep = (n) => '['.repeat(n) + ']'.repeat(n);
const CRASH = [
    { name: 'agent path with a broken escape', steps: [{ open: 'a', path: agent('%E0%A4%A', 'tok-x-good') }] },
    { name: 'agent path with a bad escape', steps: [{ open: 'a', path: agent('dev%ZZ', 'x') }] },
    { name: 'agent path with a lone surrogate escape', steps: [{ open: 'a', path: agent('%ED%A0%80', 'x') }] },
    { name: 'agent text null', steps: [{ open: 'a', path: good('dev1') }, { send: 'a', text: 'null' }] },
    { name: 'agent text 5', steps: [{ open: 'a', path: good('dev1') }, { send: 'a', text: '5' }] },
    { name: 'viewer text null', steps: [{ open: 'v', path: viewer('dev1'), headers: op }, { send: 'v', text: 'null' }] },
    { name: 'viewer text null without agent', steps: [{ open: 'v', path: viewer('dev2'), headers: op }, { send: 'v', text: ' null ' }] },
    { name: 'agent status nested 5000', steps: [{ open: 'a', path: good('dev1') }, { send: 'a', text: `{"type":"status","a":${deep(5000)}}` }] },
    { name: 'agent other nested 5000', steps: [{ open: 'a', path: good('dev1') }, { send: 'a', text: `{"type":"other","a":${deep(5000)}}` }] },
    { name: 'agent status nested 100000', steps: [{ open: 'a', path: good('dev1') }, { send: 'a', text: `{"type":"status","a":${deep(100000)}}` }] },
    { name: 'viewer input nested 5000 with agent', steps: [{ open: 'a', path: good('dev1') }, { open: 'v', path: viewer('dev1'), headers: op }, { send: 'v', text: `{"type":"input","a":${deep(5000)}}` }] },
    { name: 'viewer input nested 5000 without agent', steps: [{ open: 'v', path: viewer('dev2'), headers: op }, { send: 'v', text: `{"type":"input","a":${deep(5000)}}` }] },
    { name: 'viewer stop nested 5000', steps: [{ open: 'v', path: viewer('dev1'), headers: op }, { send: 'v', text: `{"type":"stop","a":${deep(5000)}}` }] },
];

async function serve() {
    delete require.cache[RELAY];
    remoteRelay = require(RELAY);
    const server = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
    remoteRelay.initRemoteRelay(server, sessionMiddleware);
    await new Promise((r) => server.listen(0, '127.0.0.1', r));
    return server;
}

// child runs one crash case and reports "done" if the process lives on.
async function child(n) {
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    const steps = typeof n === 'string' && n.startsWith('depth:')
        ? [{ open: 'a', path: good('dev1') }, { send: 'a', text: `{"type":"status","a":${deep(Number(n.slice(6)))}}` }]
        : CRASH[Number(n)].steps;
    const server = await serve();
    const port = server.address().port;
    for (const step of steps) await run(step, port);
    await new Promise((r) => real.setTimeout(r, 300));
    process.stdout.write('alive\n');
    process.exit(0);
}

function crashes() {
    const out = [];
    const ends = (arg) => {
        const r = spawnSync(process.execPath, [__filename, '--crash', String(arg)], { encoding: 'utf8', timeout: 60000 });
        return { ended: !r.stdout.includes('alive'), status: r.status, error: (r.stderr.match(/^(\w*Error[^\n]*)/m) || [''])[0] };
    };
    CRASH.forEach((c, i) => out.push({ name: c.name, ...ends(i) }));
    // The deepest array the agent's status message may hold before
    // JSON.stringify runs out of stack inside the socket's listener
    let lo = 1000;
    let hi = 6000;
    while (hi - lo > 1) {
        const mid = (lo + hi) >> 1;
        if (ends(`depth:${mid}`).ended) hi = mid; else lo = mid;
    }
    return { cases: out, deepestStatus: lo };
}

if (process.argv[2] === '--crash') {
    child(process.argv[3]).catch((err) => { process.stderr.write(String(err.stack)); process.exit(2); });
} else {
    (async () => {
        const quietLog = { log: console.log, warn: console.warn, error: console.error };
        console.log = () => {}; console.warn = () => {}; console.error = () => {};
        const results = [];
        for (const sc of S) {
            const server = await serve();
            const start = clock;
            const port = server.address().port;
            const steps = [];
            for (const step of sc.steps) {
                steps.push({ step, ...(await run(step, port)) });
            }
            for (const s of sockets.values()) s.sock.destroy();
            await quiet();
            events = [];
            calls = [];
            sockets.clear();
            server.close();
            results.push({ name: sc.name, clock: start, steps });
            timers.clear();
        }
        Object.assign(console, quietLog);
        const crash = crashes();
        const file = path.join(__dirname, '..', 'node_remote.json');
        fs.writeFileSync(file, `${JSON.stringify({ start: 1700000000000, scenarios: results, crash }, null, 1)}\n`);
        console.log(`${file}: ${results.length} scenarios, ${results.reduce((n, r) => n + r.steps.length, 0)} steps, ${crash.cases.length} crash cases, deepest status ${crash.deepestStatus}`);
        process.exit(0);
    })().catch((err) => { console.error(err); process.exit(1); });
}
