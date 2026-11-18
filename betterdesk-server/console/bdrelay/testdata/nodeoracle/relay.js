'use strict';

/**
 * Runs the old console's services/bdRelay.js on a real HTTP server with a
 * made-up clock and a made-up random source, and plays scenarios against
 * it: sockets opened with raw upgrade requests, messages, the clock moved
 * on, calls of the functions the routes used. After each step it records
 * what every socket got (the answer's head, text and binary messages, the
 * close frame, the end of the connection) and what a call returned. The
 * Go tests play the same steps on the Go service with the same clock and
 * random source and must see the same. See README.md.
 *
 *   node relay.js     writes ../node_relay.json
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const path = require('path');
const crypto = require('crypto');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

// ---- The clock: Date.now and the timers bdRelay.js starts ----

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
const randomBuf = (n) => Buffer.from(Array.from({ length: n }, randomByte));
const origRandomBytes = crypto.randomBytes;
crypto.randomBytes = (n, ...rest) => (n === 32 && rest.length === 0 ? randomBuf(32) : origRandomBytes(n, ...rest));
crypto.randomUUID = () => {
    const b = randomBuf(16);
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    const h = b.toString('hex');
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
};

// ---- The token check: tok-<id>-good passes, tok-throw fails ----

const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('services/database.js', {});
stub('lib/deviceTokenAuth.js', {
    verifyDeviceWsAuth: async (deviceId, token) => {
        await new Promise((r) => setImmediate(r));
        if (token === 'tok-throw') throw new Error('database down');
        return token === `tok-${deviceId}-good`;
    },
});

const bdRelay = require(path.join(WEB, 'services/bdRelay.js'));

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
            if (op === 1) {
                const text = payload.toString('utf8');
                note({ text });
                try {
                    const id = JSON.parse(text).request_id;
                    if (typeof id === 'string') vars.req[Object.keys(vars.req).length] = id;
                } catch (_) { /* not a request */ }
            }
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
    for (const [k, v] of Object.entries(headers || {})) lines.push(`${k}: ${v}`);
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

// vars holds what calls returned (by their "as"), and in req the request
// ids the agents got, in order: {s1.sessionId}, {req.0}.
const vars = { req: {} };
const fill = (text) => text.replace(/\{([a-z0-9_]+)\.([A-Za-z0-9]+)\}/g, (_, v, k) => String(vars[v][k]));
const requests = {};

const quiet = () => new Promise((resolve) => {
    let last = events.length;
    let still = 0;
    const tick = () => {
        if (events.length !== last) { last = events.length; still = 0; }
        if (++still >= 6) return resolve();
        real.setTimeout(tick, 10);
    };
    real.setTimeout(tick, 10);
});

async function run(step, port) {
    const out = {};
    if (step.open) openSocket(step.open, port, fill(step.path), step.headers);
    if (step.send) {
        const s = sockets.get(step.send);
        if (step.text !== undefined) writeFrame(s, 1, Buffer.from(fill(step.text)));
        if (step.binary !== undefined) writeFrame(s, 2, Buffer.from(step.binary, 'base64'));
        if (step.bytes !== undefined) writeFrame(s, 1, Buffer.from(step.bytes, 'base64'));
        if (step.repeat !== undefined) writeFrame(s, step.opcode || 1, Buffer.alloc(step.repeat, 0x78));
        if (step.close !== undefined) writeFrame(s, 8, Buffer.from([step.close >> 8, step.close & 0xff]));
        if (step.drop) s.sock.destroy();
    }
    if (step.advance) advance(step.advance);
    if (step.call) {
        const args = (step.args || []).map((a) => (typeof a === 'string' ? fill(a) : a));
        let result;
        switch (step.call) {
            case 'create': result = bdRelay.createRelaySession(...args); break;
            case 'get': result = bdRelay.getRelaySession(...args); break;
            case 'delete': result = bdRelay.activeSessions.delete(args[0]); break;
            case 'notify': result = bdRelay.notifyTarget(...args); break;
            case 'online': result = bdRelay.getOnlineDeviceIds(); break;
            case 'isOnline': result = bdRelay.isDeviceOnline(...args); break;
            case 'pending': {
                result = [];
                for (const [sid, session] of bdRelay.activeSessions) {
                    if (session.targetId === args[0] && session.status === 'pending') {
                        result.push({ session_id: sid, initiator_id: session.initiatorId, created_at: session.createdAt });
                    }
                }
                break;
            }
            case 'request': {
                const r = { settled: false };
                requests[step.as] = r;
                bdRelay.requestFromDevice(...args)
                    .then((data) => { r.settled = true; r.result = { data: data === undefined ? '<undefined>' : data }; })
                    .catch((err) => { r.settled = true; r.result = { error: err.message }; });
                break;
            }
            case 'await': {
                const r = requests[args[0]];
                await quiet();
                result = r.settled ? r.result : { pending: true };
                break;
            }
            default: throw new Error(`unknown call ${step.call}`);
        }
        if (step.as && step.call !== 'request') vars[step.as] = result;
        if (step.call !== 'request') out.result = result === undefined ? '<undefined>' : result;
    }
    await quiet();
    out.events = events;
    events = [];
    return out;
}

// ---- The scenarios ----

const S = [];
const scenario = (name, steps) => S.push({ name, steps });
const sig = (id, token) => `/ws/bd-signal?device_id=${id}&token=${token}`;
const good = (id) => sig(id, `tok-${id}-good`);
const relay = (v, role, tokenKey) => `/ws/bd-relay?session={${v}.sessionId}&token={${v}.${tokenKey || (role === 'target' ? 'targetToken' : 'initiatorToken')}}&role=${role}`;
const b64 = (s) => Buffer.from(s).toString('base64');

scenario('signal checks', [
    { open: 'a', path: '/ws/bd-signal' },
    { open: 'b', path: '/ws/bd-signal?device_id=dev1' },
    { open: 'c', path: '/ws/bd-signal?device_id=&token=x' },
    { open: 'd', path: '/ws/bd-signal?device_id=dev1&token=' },
    { open: 'e', path: sig('ab', 'tok-ab-good') },
    { open: 'f', path: sig('a'.repeat(65), 'x') },
    { open: 'g', path: sig('a+b1', 'tok-a%20b1-good') },
    { open: 'h', path: sig('%64ev1', 'tok-dev1-good') },
    { open: 'i', path: sig('dev1', 'tok-dev2-good') },
    { open: 'j', path: sig('dev1', 'tok-throw') },
    { open: 'k', path: '/ws/bd-signal?device_id=dev1&device_id=dev2&token=tok-dev1-good' },
    { open: 'l', path: sig('dev1', 'tok-dev1-good'), headers: { Origin: 'https://evil.example' } },
    { open: 'm', path: sig('dev1', 'tok-dev1-good'), headers: { Cookie: 'bd.sid=x' } },
    { open: 'n', path: sig('dev1', 'tok-dev1-good'), headers: { Origin: 'http://console.test' } },
    { call: 'online' },
]);
scenario('signal messages', [
    { open: 'a', path: good('dev1') },
    { send: 'a', text: '{"type":"heartbeat"}' },
    { send: 'a', binary: b64('{"type":"heartbeat"}') },
    { send: 'a', binary: Buffer.from([0x7b, 0xff, 0x7d]).toString('base64') },
    { send: 'a', text: 'not json' },
    { send: 'a', text: 'null' },
    { send: 'a', text: '[1,2]' },
    { send: 'a', text: '{"type":["heartbeat"]}' },
    { send: 'a', text: ' {"type":"heartbeat","extra":{"a":1}} ' },
    { send: 'a', text: '{"type":"relay_accept","session_id":5}' },
    { send: 'a', text: '{"type":"command_response","request_id":"none","ok":true}' },
    { call: 'online' },
    { call: 'isOnline', args: ['dev1'] },
    { call: 'isOnline', args: ['dev9'] },
    { send: 'a', close: 1000 },
    { call: 'isOnline', args: ['dev1'] },
    { call: 'online' },
]);
scenario('signal replaced', [
    { open: 'a', path: good('dev1') },
    { open: 'b', path: good('dev2') },
    { open: 'c', path: good('dev1') },
    { call: 'online' },
    { send: 'c', close: 1000 },
    { call: 'online' },
    { open: 'd', path: good('dev1') },
    { call: 'online' },
    { call: 'isOnline', args: ['dev1'] },
]);
scenario('signal size limit', [
    { open: 'a', path: good('dev1') },
    { send: 'a', repeat: 65536 },
    { send: 'a', repeat: 65537 },
    { open: 'b', path: good('dev2') },
    { send: 'b', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
]);
const ipHeaders = (ip) => ({ 'X-Forwarded-For': ip });
const many = [];
for (let i = 1; i <= 10; i++) many.push({ open: `s${i}`, path: good(`dev${i}`), headers: ipHeaders('10.0.0.1') });
scenario('per-address limit', [
    ...many,
    { open: 'over', path: good('dev11'), headers: ipHeaders('10.0.0.1') },
    { open: 'other', path: good('dev12'), headers: ipHeaders('10.0.0.2') },
    { open: 'list', path: good('dev13'), headers: ipHeaders(' 10.0.0.1 , 10.0.0.9') },
    { send: 's1', close: 1000 },
    { open: 'again', path: good('dev14'), headers: ipHeaders('10.0.0.1') },
    { open: 'again2', path: good('dev15'), headers: ipHeaders('10.0.0.1') },
]);
const many3 = [];
for (let i = 1; i <= 10; i++) many3.push({ open: `s${i}`, path: good(`dev${i}`), headers: ipHeaders('10.0.0.3') });
scenario('an error counts down twice', [
    ...many3,
    { send: 's1', repeat: 65537 },
    { open: 'x1', path: good('dev21'), headers: ipHeaders('10.0.0.3') },
    { open: 'x2', path: good('dev22'), headers: ipHeaders('10.0.0.3') },
    { open: 'x3', path: good('dev23'), headers: ipHeaders('10.0.0.3') },
]);
scenario('requests', [
    { open: 'a', path: good('dev1') },
    { call: 'request', args: ['dev1', 'services.list', null, 15000], as: 'r1' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.0}","ok":true,"data":{"b":1,"a":[2,"3"],"2":0}}', },
    { call: 'await', args: ['r1'] },
    { call: 'request', args: ['dev1', 'files.browse', { path: '/tmp', show_hidden: false }, 30000], as: 'r2' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.1}","ok":false,"error":"permission denied"}', },
    { call: 'await', args: ['r2'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r3' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.2}","ok":0}', },
    { call: 'await', args: ['r3'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r4' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.3}","ok":true,"data":0}', },
    { call: 'await', args: ['r4'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r5' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.4}","ok":false,"error":{"code":5}}', },
    { call: 'await', args: ['r5'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r6' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.5}","ok":false,"error":[1,[2,null],"x",true]}', },
    { call: 'await', args: ['r6'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r7' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.6}","ok":false,"error":1.5e21}', },
    { call: 'await', args: ['r7'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r8' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.7}","ok":"yes","data":"agent_offline"}', },
    { call: 'await', args: ['r8'] },
    { call: 'request', args: ['dev1', 'x', null, 15000], as: 'r9' },
    { send: 'a', text: '{"type":"command_response","request_id":"{req.8}","ok":false,"error":"agent_offline"}', },
    { call: 'await', args: ['r9'] },
    { call: 'request', args: ['dev1', 'slow', { n: 1 }, 15000], as: 't1' },
    { advance: 14999 },
    { call: 'await', args: ['t1'] },
    { advance: 1 },
    { call: 'await', args: ['t1'] },
    { call: 'request', args: ['dev1', 'short', null, 100], as: 't2' },
    { advance: 999 },
    { call: 'await', args: ['t2'] },
    { advance: 1 },
    { call: 'await', args: ['t2'] },
    { call: 'request', args: ['dev1', 'long', null, 120000], as: 't3' },
    { advance: 59999 },
    { call: 'await', args: ['t3'] },
    { advance: 1 },
    { call: 'await', args: ['t3'] },
    { call: 'request', args: ['dev1', 'closed', null, 15000], as: 't4' },
    { send: 'a', close: 1000 },
    { call: 'await', args: ['t4'] },
    { advance: 15000 },
    { call: 'await', args: ['t4'] },
    { call: 'request', args: ['dev1', 'offline', null, 15000], as: 'o1' },
    { call: 'await', args: ['o1'] },
    { call: 'request', args: ['dev9', 'never', null, 15000], as: 'o2' },
    { call: 'await', args: ['o2'] },
]);
scenario('relay pairing', [
    { open: 'tsig', path: good('dev1') },
    { call: 'create', args: ['dev2', 'dev1'], as: 's1' },
    { call: 'notify', args: ['dev1', { type: 'incoming_connection', session_id: 'x', initiator_id: 'dev2', initiator_pk: null }] },
    { call: 'notify', args: ['dev9', { type: 'x' }] },
    { call: 'pending', args: ['dev1'] },
    { call: 'get', args: ['{s1.sessionId}'] },
    { send: 'tsig', text: '{"type":"relay_accept","session_id":"{s1.sessionId}"}' },
    { open: 'init', path: relay('s1', 'initiator') },
    { send: 'init', text: 'before the pair' },
    { open: 'tgt', path: relay('s1', 'target') },
    { call: 'get', args: ['{s1.sessionId}'] },
    { call: 'pending', args: ['dev1'] },
    { send: 'tsig', text: '{"type":"relay_accept","session_id":"{s1.sessionId}"}' },
    { send: 'init', text: 'hello' },
    { send: 'tgt', binary: Buffer.from([0, 1, 2, 255]).toString('base64') },
    { open: 'again', path: relay('s1', 'initiator') },
    { send: 'init', close: 1000 },
    { call: 'get', args: ['{s1.sessionId}'] },
]);
scenario('relay checks', [
    { call: 'create', args: ['dev2', 'dev1'], as: 's1' },
    { open: 'a', path: '/ws/bd-relay' },
    { open: 'b', path: '/ws/bd-relay?session={s1.sessionId}&token=x&role=viewer' },
    { open: 'c', path: '/ws/bd-relay?session=none&token=x&role=initiator' },
    { open: 'd', path: `/ws/bd-relay?session={s1.sessionId}&token=${'0'.repeat(64)}&role=initiator` },
    { open: 'e', path: relay('s1', 'target', 'initiatorToken') },
    { open: 'f', path: relay('s1', 'initiator'), headers: { Origin: 'https://evil.example' } },
    { open: 'g', path: relay('s1', 'initiator') },
    { open: 'h', path: relay('s1', 'initiator') },
]);
scenario('relay expiry and timeouts', [
    { call: 'create', args: ['dev2', 'dev1'], as: 's1' },
    { advance: 60001 },
    { open: 'late', path: relay('s1', 'initiator') },
    { call: 'get', args: ['{s1.sessionId}'] },
    { call: 'create', args: ['dev2', 'dev1'], as: 's2' },
    { open: 'wait', path: relay('s2', 'initiator') },
    { advance: 59999 },
    { advance: 1 },
    { call: 'get', args: ['{s2.sessionId}'] },
    { call: 'create', args: ['dev2', 'dev1'], as: 's3' },
    { open: 'i3', path: relay('s3', 'initiator') },
    { open: 't3', path: relay('s3', 'target') },
    { advance: 119999 },
    { send: 'i3', text: 'keep' },
    { advance: 119999 },
    { advance: 1 },
    { call: 'get', args: ['{s3.sessionId}'] },
]);
scenario('relay cleanup and delete', [
    { call: 'create', args: ['dev2', 'dev1'], as: 's1' },
    { call: 'create', args: ['dev3', 'dev1'], as: 's2' },
    { call: 'pending', args: ['dev1'] },
    { open: 'i2', path: relay('s2', 'initiator') },
    { call: 'delete', args: ['{s2.sessionId}'] },
    { call: 'get', args: ['{s2.sessionId}'] },
    { advance: 60000 },
    { advance: 60000 },
    { advance: 30000 },
    { call: 'get', args: ['{s1.sessionId}'] },
    { call: 'pending', args: ['dev1'] },
    { send: 'i2', close: 1000 },
]);
scenario('relay reject and errors', [
    { open: 'tsig', path: good('dev1') },
    { open: 'other', path: good('dev3') },
    { call: 'create', args: ['dev2', 'dev1'], as: 's1' },
    { open: 'init', path: relay('s1', 'initiator') },
    { send: 'other', text: '{"type":"relay_reject","session_id":"{s1.sessionId}"}' },
    { send: 'other', text: '{"type":"relay_accept","session_id":"{s1.sessionId}"}' },
    { send: 'tsig', text: '{"type":"relay_reject","session_id":"{s1.sessionId}"}' },
    { call: 'get', args: ['{s1.sessionId}'] },
    { call: 'create', args: ['dev2', 'dev1'], as: 's2' },
    { open: 'i2', path: relay('s2', 'initiator') },
    { open: 't2', path: relay('s2', 'target') },
    { send: 't2', repeat: 2 * 1024 * 1024, opcode: 2 },
    { send: 'i2', repeat: 2 * 1024 * 1024 + 1, opcode: 2 },
    { call: 'get', args: ['{s2.sessionId}'] },
]);

(async () => {
    const quietLog = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    const results = [];
    for (const sc of S) {
        const server = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
        const start = clock;
        vars.req = {};
        bdRelay.initBdRelay(server);
        await new Promise((r) => server.listen(0, '127.0.0.1', r));
        const port = server.address().port;
        const steps = [];
        for (const step of sc.steps) {
            steps.push({ step, ...(await run(step, port)) });
        }
        for (const s of sockets.values()) s.sock.destroy();
        await quiet();
        events = [];
        sockets.clear();
        // Reset the module's state between scenarios
        bdRelay.activeSessions.clear();
        bdRelay.onlineDevices.clear();
        server.close();
        results.push({ name: sc.name, clock: start, steps });
        timers.clear();
    }
    Object.assign(console, quietLog);
    const file = path.join(__dirname, '..', 'node_relay.json');
    fs.writeFileSync(file, `${JSON.stringify({ start: 1700000000000, scenarios: results }, null, 1)}\n`);
    console.log(`${file}: ${results.length} scenarios, ${results.reduce((n, r) => n + r.steps.length, 0)} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
