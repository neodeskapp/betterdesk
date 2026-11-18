'use strict';

/**
 * Runs the old console's services/chatRelay.js on a real HTTP server with a
 * made-up clock, a made-up API server and stubs for the session, the
 * permission lookup and the device token check, and plays scenarios
 * against it: sockets opened with raw upgrade requests, messages sent,
 * the API server's answers set per scenario. After each step it records
 * what every socket got (the answer's head, text and binary messages, the
 * close frame, the end of the connection), every call to the API server
 * (method, path, body), every token check and every permission lookup.
 * The Go tests play the same steps on the Go relay and must see the same.
 * See README.md.
 *
 *   node relay.js     writes ../node_relay.json
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

// ---- The clock: Date.now and the timers the relay and ws start ----

const real = { setTimeout, clearTimeout, setInterval, clearInterval, setImmediate };
let clock = 1700000000000;
let timerSeq = 0;
const timers = new Map();
const fakeTimer = (fn, delay, repeat, args) => {
    const id = { id: ++timerSeq, unref() { return this; }, ref() { return this; }, hasRef() { return true; }, refresh() { return this; }, [Symbol.toPrimitive]() { return this.id; } };
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
        if (next.t.repeat) next.t.at += next.t.delay;
        else timers.delete(next.key);
        next.t.fn(...next.t.args);
    }
    clock = until;
}

// ---- What the relay calls: token check, permissions, the API server ----

let events = [];
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('services/database.js', {});
// tok-<id>-good passes for that device id, tok-throw makes the check fail
stub('lib/deviceTokenAuth.js', {
    verifyDeviceWsAuth: async (deviceId, token) => {
        events.push({ verify: [deviceId, token] });
        await new Promise((r) => real.setImmediate(r));
        if (token === 'tok-throw') throw new Error('database down');
        return token === `tok-${deviceId}-good`;
    },
});
// admin and super_admin have chat.access, so has operator; "throw" makes
// the lookup fail; every other role lacks it
const CHAT = new Set(['admin', 'super_admin', 'operator']);
stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, permission) => {
        events.push({ permission: [role === undefined ? '' : String(role), permission] });
        if (role === 'throw') throw new Error('lookup failed');
        return permission === 'chat.access' && CHAT.has(role);
    },
});

const { assertSafeGoApiRelativePath } = require(path.join(WEB, 'lib/goApiPath.js'));

// The API server: the answers of the scenario by "METHOD path" (a list is
// used up in order, its last answer stays); anything else is a 500.
let answers = {};
function answerFor(key) {
    const a = answers[key];
    if (Array.isArray(a)) return a.length > 1 ? a.shift() : a[0];
    return a || { status: 500, body: '{"error":"internal"}' };
}
// axios: a body that is JSON is parsed, other text stays text
function axiosData(text) {
    try { return JSON.parse(text); } catch (_) { return text; }
}
function apiCall(method, url, body) {
    return Promise.resolve().then(() => {
        assertSafeGoApiRelativePath(String(url));
        const call = { method, path: url };
        if (body !== undefined) call.body = JSON.stringify(body);
        events.push({ api: call });
        const a = answerFor(`${method} ${url}`);
        if (a.status >= 200 && a.status < 300) return { status: a.status, data: axiosData(a.body) };
        throw new Error(`Request failed with status code ${a.status}`);
    });
}
const goApi = { get: (url) => apiCall('GET', url), post: (url, body) => apiCall('POST', url, body) };

// The session middleware: the session is the JSON in X-Oracle-Session, a
// new empty one without the header. It runs asynchronously, as the real
// one does.
function sessionMiddleware(req, _res, next) {
    const raw = req.headers['x-oracle-session'];
    real.setImmediate(() => {
        req.session = raw !== undefined ? JSON.parse(raw) : {};
        next();
    });
}

// ---- A raw websocket client that notes everything ----

const sockets = new Map();

function openSocket(name, port, target, headers, method) {
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
            // pings and pongs are not noted
        }
    });
    sock.on('close', () => { if (!s.ended) { s.ended = true; note({ end: true }); } });
    sock.on('error', () => {});
    const lines = [`${method || 'GET'} ${target} HTTP/1.1`, 'Host: console.test', 'Upgrade: websocket', 'Connection: Upgrade',
        'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==', 'Sec-WebSocket-Version: 13'];
    const own = Object.keys(headers || {}).sort();
    for (const k of own) lines.push(`${k}: ${headers[k]}`);
    // Header values are bytes: each character below 256 is one byte
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

// ---- Steps ----

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

let crashed = null;

async function run(step, port) {
    const out = {};
    if (step.answers) Object.assign(answers, JSON.parse(JSON.stringify(step.answers)));
    if (step.open) openSocket(step.open, port, step.path, step.headers, step.method);
    if (step.send) {
        const s = sockets.get(step.send);
        if (step.text !== undefined) writeFrame(s, 1, Buffer.from(step.text));
        if (step.texts !== undefined) for (const t of step.texts) writeFrame(s, 1, Buffer.from(t));
        if (step.binary !== undefined) writeFrame(s, 2, Buffer.from(step.binary, 'base64'));
        if (step.bytes !== undefined) writeFrame(s, 1, Buffer.from(step.bytes, 'base64'));
        if (step.repeat !== undefined) writeFrame(s, step.opcode || 1, Buffer.alloc(step.repeat, 0x78));
        if (step.close !== undefined) writeFrame(s, 8, Buffer.from([step.close >> 8, step.close & 0xff]));
        if (step.drop) s.sock.destroy();
    }
    if (step.advance) advance(step.advance);
    await quiet();
    if (crashed) {
        events.push({ crash: crashed });
        crashed = null;
    }
    out.events = events;
    events = [];
    return out;
}

// ---- The scenarios ----

const S = [];
const scenario = (name, steps, opts) => S.push({ name, steps, ...(opts || {}) });
// Header bytes reach the server as Latin-1: everything else goes as an escape
const session = (s) => ({ 'X-Oracle-Session': JSON.stringify(s).replace(/[\u007f-\uffff]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`) });
const op = (name, role) => session({ userId: 7, user: { id: 7, username: name, role: role || 'operator' } });
const agent = (id) => `/ws/chat/${id}?token=tok-${id}-good`;
const ok = (body) => ({ status: 200, body: typeof body === 'string' ? body : JSON.stringify(body) });
const fail = (status) => ({ status, body: '{"error":"internal"}' });
const send = (name, frame) => ({ send: name, text: typeof frame === 'string' ? frame : JSON.stringify(frame) });
const b64 = (s) => Buffer.from(s).toString('base64');

// The answers a working API server gives for a device
const working = (id, extra) => ({
    [`GET /chat/history/${encodeURIComponent(id)}?limit=100`]: ok({ conversation_id: id, messages: [{ id: 3, conversation_id: id, from_id: id, text: 'stored', created_at: '2026-11-10T10:00:00Z' }] }),
    [`GET /chat/contacts/${encodeURIComponent(id)}`]: ok({ contacts: [{ id: 'dev2', name: 'two', hostname: 'two', online: true, last_seen: 1700000000999, unread: 0, avatar_color: 'hsl(1, 65%, 55%)', role: 'device' }] }),
    [`GET /chat/groups/${encodeURIComponent(id)}`]: ok({ groups: [{ id: 'group:1', name: 'g', members: `${id},dev2`, created_by: 'x', created_at: '2026-11-10T10:00:00Z' }] }),
    ...(extra || {}),
});
const workingPanel = () => ({
    'GET /chat/contacts/panel': ok({ contacts: [{ id: 'dev1', name: 'one', online: true }, { id: 'operator:admin', name: 'admin', role: 'operator' }] }),
    'GET /chat/groups/panel': ok({ groups: [] }),
});

scenario('agent checks', [
    { open: 'a', path: '/ws/chat/dev1' },
    { open: 'b', path: agent('dev1'), headers: { Origin: 'https://evil.example' } },
    { open: 'c', path: agent('dev1'), headers: { Cookie: 'bd.sid=x' } },
    { open: 'd', path: '/ws/chat/dev1?token=tok-dev2-good' },
    { open: 'e', path: '/ws/chat/dev1?token=tok-throw' },
    { open: 'f', path: '/ws/chat/dev1', headers: { Authorization: 'Bearer tok-dev1-good' } },
    { open: 'g', path: '/ws/chat/dev2', headers: { Authorization: 'bearer tok-dev2-good' } },
    { open: 'h', path: '/ws/chat/dev3', headers: { Authorization: 'Bearer \t tok-dev3-good' } },
    { open: 'i', path: '/ws/chat/dev4', headers: { Authorization: 'Bearer tok-dev4-good extra' } },
    { open: 'j', path: '/ws/chat/dev5?token=', headers: { Authorization: 'Bearer tok-dev5-good' } },
    { open: 'k', path: '/ws/chat/dev6?token=tok-dev6-good', headers: { Authorization: 'Bearer nope' } },
    { open: 'l', path: '/ws/chat/dev%207?token=tok-dev%207-good' },
    { open: 'm', path: '/ws/chat/dev8', headers: { Authorization: 'Bearer tok-dev8-good' } },
    { open: 'n', path: '/ws/chat/dev9', headers: { Authorization: 'Bearer tok-dev9-géod' } },
    { open: 'o', path: agent('dev10'), headers: { 'Sec-WebSocket-Key': 'short' } },
    { open: 'p', path: agent('dev11'), method: 'POST' },
    { open: 'q', path: '/ws/chat/dev12?token=tok-dev12-good&token=x' },
    { open: 'r', path: '/ws/chat/dev13?token=tok-dev13-g%6Fod' },
    { open: 's', path: agent('dev14'), headers: { Origin: 'http://console.test' } },
    { open: 't', path: agent('dev15'), headers: { Cookie: 'other=1', Origin: '' } },
    { open: 'u', path: '/ws/chat/dev16?tok%65n=tok-dev16-good' },
    { open: 'v', path: '/ws/chat/dev17', headers: { Authorization: 'Bearer tok-dev17-good ' } },
], { answers: {} });

scenario('operator checks', [
    { open: 'a', path: '/ws/chat-operator/dev1' },
    { open: 'b', path: '/ws/chat-operator/dev1', headers: session({ userId: 0, user: { role: 'admin' } }) },
    { open: 'c', path: '/ws/chat-operator/dev1', headers: op('v', 'viewer') },
    { open: 'd', path: '/ws/chat-operator/dev1', headers: op('t', 'throw') },
    { open: 'e', path: '/ws/chat-operator/dev1', headers: { ...op('x'), Origin: 'https://evil.example' } },
    { open: 'f', path: '/ws/chat-operator/dev1', headers: session({ userId: 3, role: 'operator', username: 'flat' }) },
    { open: 'g', path: '/ws/chat-operator/dev1', headers: { ...op('k'), 'Sec-WebSocket-Key': 'short' } },
    { open: 'h', path: '/ws/chat-operator/dev1', headers: { ...op('m'), 'Sec-WebSocket-Version': '12' } },
    { open: 'i', path: '/ws/chat-operator/dev1', headers: op('ok') },
    { open: 'j', path: '/ws/chat-operator/dev1', headers: session({ userId: '5', user: { role: 'admin' } }) },
    { open: 'k', path: '/ws/chat-operator/dev1', headers: session({ userId: 5, username: 'flat', user: { role: 'admin', username: '' } }) },
    { send: 'j', text: '{"type":"hello"}' },
    { send: 'k', text: '{"type":"typing"}' },
    { open: 'l', path: '/ws/chat-operator/dev1', headers: { ...op('p'), 'Sec-WebSocket-Protocol': 'chat, other' } },
    { open: 'm', path: '/ws/chat-operator/dev1', headers: { ...op('n'), Cookie: 'bd.sid=x' } },
    { open: 'n', path: '/ws/chat-operator/dev1', headers: { Cookie: 'bd.sid=x' } },
], { answers: working('dev1') });

scenario('agent session', [
    { open: 'op', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { open: 'panel', path: '/ws/chat-operator/panel', headers: op('ben') },
    { open: 'a', path: agent('dev1') },
    send('a', { type: 'hello', device_id: 'other', capabilities: ['x'] }),
    send('a', { type: 'message', text: 'hello operator', conversation_id: 'dev1' }),
    send('a', { type: 'message', text: 'no conversation', from_name: 'Front desk', timestamp: 'yesterday' }),
    send('a', { type: 'message', text: 5, conversation_id: 0, from_name: '', timestamp: 0 }),
    send('a', { type: 'message', text: [1, [2, null], 'x', true], conversation_id: { a: 1 }, from_name: { b: 2 } }),
    send('a', { type: 'message', text: { a: 1 } }),
    send('a', { type: 'message' }),
    send('a', { type: 'message', text: 'y'.repeat(3000) }),
    send('a', { type: 'message', text: 'a'.repeat(2047) + '\u{1F600}' + 'b' }),
    send('a', { type: 'message', text: '  <>&"\\\u0001 é \u{1F600}' }),
    send('a', { type: 'typing' }),
    send('a', { type: 'typing', conversation_id: 'c1' }),
    send('a', { type: 'presence_update' }),
    send('a', { type: 'presence_update', online: false, status: 'busy' }),
    send('a', { type: 'presence_update', online: 0, status: '' }),
    send('a', { type: 'key_exchange' }),
    send('a', { type: 'key_exchange', public_key: { k: [1] }, conversation_id: 'c2' }),
    send('a', { type: 'read_receipt' }),
    send('a', { type: 'read_receipt', message_ids: [1, 2], conversation_id: 'dev1' }),
    send('a', { type: 'file_share' }),
    send('a', { type: 'file_share', from_name: 'F', conversation_id: 'c3', file_id: 'f1', file_name_encrypted: 'e', file_size: 12, encrypted_metadata: 'm' }),
    send('a', { type: 'mark_read' }),
    send('a', { type: 'mark_read', conversation_id: 'dev1' }),
    send('a', { type: 'get_contacts' }),
    send('a', { type: 'get_contacts', device_id: 'dev2' }),
    send('a', { type: 'get_history' }),
    send('a', { type: 'get_history', conversation_id: 'dev1' }),
    send('a', { type: 'get_history', conversation_id: 'nothing' }),
    send('a', { type: 'create_group', name: 'G', member_ids: 'dev2' }),
    send('a', { type: 'create_group', name: 'G' }),
    send('a', { type: 'create_group', name: '', member_ids: ['x'] }),
    send('a', { type: 'unknown' }),
    send('a', '{}'),
    send('a', 'not json'),
    send('a', '[1]'),
    send('a', '5'),
    send('a', '"text"'),
    send('a', ' {"type":"hello"} '),
    send('a', '{"type":"hello","type":"typing"}'),
    send('a', '{"type":"typing","__proto__":{"x":1},"conversation_id":"p"}'),
    { send: 'a', binary: b64('{"type":"hello"}') },
    send('a', JSON.stringify({ type: 'hello', pad: '' }).replace('""', '"' + 'x'.repeat(8192 - 25) + '"')),
    send('a', JSON.stringify({ type: 'hello', pad: '' }).replace('""', '"' + 'x'.repeat(8192 - 24) + '"')),
    { send: 'a', bytes: Buffer.from([0x7b, 0x22, 0xc3, 0x28, 0x22, 0x7d]).toString('base64') },
], {
    answers: {
        ...working('dev1'), ...workingPanel(),
        'GET /chat/contacts/dev2': ok({ contacts: [] }),
        'GET /chat/history/nothing?limit=100': ok({ messages: null }),
        'POST /chat/groups': ok({ id: 'group:abc', name: 'G', members: 'dev2', type: 'clash', 7: 'seven' }),
        'POST /chat/messages': ok({ id: 1 }),
        'POST /chat/read': ok('{"ok":true}'),
    },
});

scenario('operator session', [
    { open: 'a1', path: agent('dev1') },
    { open: 'a2', path: agent('dev2') },
    { open: 'o1', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { open: 'o1b', path: '/ws/chat-operator/dev1', headers: op('carl') },
    { open: 'o2', path: '/ws/chat-operator/dev2', headers: op('dora') },
    { open: 'p1', path: '/ws/chat-operator/panel', headers: op('ben') },
    { open: 'p2', path: '/ws/chat-operator/panel', headers: op('eve') },
    send('o1', { type: 'hello' }),
    send('o1', { type: 'message', text: 'to my device' }),
    send('o1', { type: 'message', text: 'to the other', conversation_id: 'dev2' }),
    send('o1', { type: 'message', text: 'to nobody', conversation_id: 'nosuch' }),
    send('o1', { type: 'message', text: 'number room', conversation_id: 5 }),
    send('p1', { type: 'message', text: 'from the panel', conversation_id: 'dev1' }),
    send('p1', { type: 'message', text: 'to a group', conversation_id: 'group:x' }),
    send('p1', { type: 'message', text: 'no conversation' }),
    send('p1', { type: 'message', text: 'z'.repeat(2100), conversation_id: 'panel' }),
    send('o1', { type: 'typing' }),
    send('o1', { type: 'typing', conversation_id: 'dev2' }),
    send('p1', { type: 'typing', conversation_id: 'dev1' }),
    send('p1', { type: 'typing', conversation_id: 'group:x' }),
    send('o1', { type: 'key_exchange', public_key: 'pk' }),
    send('p1', { type: 'key_exchange', conversation_id: 'dev2' }),
    send('p1', { type: 'key_exchange', public_key: 'pk', conversation_id: 'group:y' }),
    send('o1', { type: 'read_receipt' }),
    send('p1', { type: 'read_receipt', message_ids: ['m1'], conversation_id: 'dev2' }),
    send('p1', { type: 'read_receipt', message_ids: 7, conversation_id: 'group:z' }),
    send('o1', { type: 'mark_read' }),
    send('o1', { type: 'mark_read', conversation_id: 'dev1' }),
    send('o1', { type: 'presence_update' }),
    send('p1', { type: 'presence_update', online: false, status: 'away' }),
    send('o1', { type: 'reaction', message_id: '74', emoji: '\u{1F44D}' }),
    send('p1', { type: 'reaction', message_id: 74, emoji: 'x'.repeat(20), conversation_id: 'dev1' }),
    send('p1', { type: 'reaction', message_id: 'm'.repeat(70), emoji: '\u{1F600}'.repeat(9), conversation_id: 'g'.repeat(130) }),
    send('p1', { type: 'reaction', message_id: '', emoji: 'x' }),
    send('p1', { type: 'reaction', message_id: '1' }),
    send('p1', { type: 'reaction', message_id: '1', emoji: 'x', conversation_id: 0 }),
    send('o1', { type: 'file_share' }),
    send('p1', { type: 'file_share', conversation_id: 'dev2', file_id: 'f', file_size: '9' }),
    send('p1', { type: 'file_share', conversation_id: 'group:q' }),
    send('o1', { type: 'get_contacts' }),
    send('p1', { type: 'get_contacts', device_id: 'dev2' }),
    send('o1', { type: 'get_groups' }),
    send('p1', { type: 'get_groups', device_id: 'dev2' }),
    send('o1', { type: 'get_history' }),
    send('o1', { type: 'get_history', conversation_id: 'dev2' }),
    send('o1', { type: 'get_history', conversation_id: 'dev1' }),
    send('o1', { type: 'create_group', name: 'Team', member_ids: ['dev1', 'dev2'] }),
    send('o1', { type: 'create_group', name: 'n'.repeat(130), member_ids: [] }),
    send('o1', { type: 'create_group', name: 5, member_ids: [1] }),
    send('o1', { type: 'create_group', name: 'x', member_ids: 'dev1' }),
    send('o1', { type: 'nothing' }),
    { send: 'a2', close: 1000 },
    send('o2', { type: 'get_history', conversation_id: 'dev2' }),
], {
    answers: {
        'GET /chat/history/dev1?limit=100': [fail(500), ok({ messages: [] })],
        'GET /chat/history/dev2?limit=100': ok({ conversation_id: 'dev2', messages: [{ id: 9 }] }),
        'GET /chat/contacts/dev1': ok({ contacts: [{ id: 'x' }] }),
        'GET /chat/contacts/dev2': fail(503),
        'GET /chat/contacts/panel': ok({ contacts: [] }),
        'GET /chat/groups/dev1': ok({ groups: [{ id: 'group:1' }] }),
        'GET /chat/groups/dev2': ok({ nothing: true }),
        'GET /chat/groups/panel': fail(500),
        'POST /chat/groups': [ok({ id: 'group:new', name: 'Team' }), fail(400)],
        'POST /chat/messages': ok({ id: 1 }),
        'POST /chat/read': fail(500),
    },
});

scenario('fallbacks and odd answers', [
    { open: 'o1', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { open: 'a1', path: agent('dev1') },
    send('a1', { type: 'message', text: 'kept in memory' }),
    send('o1', { type: 'message', text: 'also kept' }),
    { open: 'a2', path: agent('dev2') },
    { open: 'a3', path: agent('dev3') },
    send('a1', { type: 'get_contacts' }),
    send('o1', { type: 'get_contacts' }),
    send('o1', { type: 'get_history', conversation_id: 'dev1' }),
    send('o1', { type: 'get_history', conversation_id: 'dev2' }),
    { open: 'o2', path: '/ws/chat-operator/dev1', headers: op('bert') },
    { open: 'p', path: '/ws/chat-operator/panel', headers: op('ben') },
    send('a2', { type: 'get_contacts' }),
    send('a1', { type: 'create_group', name: 'G', member_ids: ['x'] }),
    send('o1', { type: 'create_group', name: 'G', member_ids: ['x'] }),
    send('o1', { type: 'get_groups' }),
    // a length that is true but not above 0
    send('o1', { type: 'get_contacts', device_id: 'odd' }),
    send('a1', { type: 'get_contacts', device_id: 'odd' }),
], {
    answers: {
        'GET /chat/contacts/odd': ok({ contacts: { length: '0' } }),
        'GET /chat/history/dev2?limit=100': ok({ conversation_id: 'dev2', messages: 'not a list' }),
        'GET /chat/history/dev3?limit=100': ok('plain text'),
        'GET /chat/contacts/dev2': ok({ contacts: 'ab' }),
        'GET /chat/contacts/dev3': ok({ contacts: [] }),
        'GET /chat/groups/dev2': ok({ groups: 0 }),
        'GET /chat/groups/dev3': ok({ groups: {} }),
        'POST /chat/groups': [ok('"made"'), ok('[5,6]'), ok('null')],
        'POST /chat/messages': fail(500),
    },
});

scenario('conversation ids the API path cannot take', [
    { open: 'o1', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { open: 'a1', path: agent('dev1') },
    send('o1', { type: 'get_history', conversation_id: 'a b/c?d#e' }),
    send('o1', { type: 'get_history', conversation_id: '..' }),
    send('o1', { type: 'get_history', conversation_id: 'x..y' }),
    send('o1', { type: 'get_history', conversation_id: '.' }),
    send('o1', { type: 'get_history', conversation_id: 5 }),
    send('o1', { type: 'get_history', conversation_id: [1, 2] }),
    send('o1', { type: 'get_history', conversation_id: { toString: 1 } }),
    send('o1', { type: 'get_history', conversation_id: '\ud800' }),
    send('o1', { type: 'get_history', conversation_id: '\u{1F600}é!*\'()~' }),
    send('o1', { type: 'get_contacts', device_id: '%2e%2e' }),
    send('o1', { type: 'get_groups', device_id: 'g/../x' }),
    send('a1', { type: 'get_contacts', device_id: '..' }),
    send('a1', { type: 'get_history', conversation_id: '..' }),
    { open: 'o3', path: '/ws/chat-operator/..%2F', headers: op('cid') },
    { open: 'o4', path: '/ws/chat-operator/a/../dev1', headers: op('dot') },
    { open: 'a4', path: '/ws/chat/a%2Fb?token=tok-a%2Fb-good' },
    { open: 'a5', path: '/ws/chat/./x?token=tok-x-good' },
    { open: 'a6', path: '/ws/chat/%E2%82%AC?token=tok-%E2%82%AC-good' },
], {
    answers: {
        'GET /chat/history/a%20b%2Fc%3Fd%23e?limit=100': ok({ messages: ['encoded'] }),
        'GET /chat/history/5?limit=100': ok({ messages: ['five'] }),
        'GET /chat/history/1%2C2?limit=100': ok({ messages: ['list'] }),
        "GET /chat/history/%F0%9F%98%80%C3%A9!*'()~?limit=100": ok({ messages: ['emoji'] }),
        'GET /chat/contacts/%252e%252e': ok({ contacts: [{ id: 'dots' }] }),
    },
});

scenario('agent replaced and gone', [
    { open: 'o', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { open: 'p', path: '/ws/chat-operator/panel', headers: op('ben') },
    { open: 'a1', path: agent('dev1') },
    { open: 'a2', path: agent('dev1') },
    send('a1', { type: 'hello' }),
    send('a2', { type: 'message', text: 'from the second' }),
    { send: 'a2', close: 1000 },
    { open: 'a3', path: agent('dev1') },
    { send: 'a3', drop: true },
    { open: 'a4', path: agent('dev1') },
    { send: 'a4', repeat: 9000 },
    { send: 'a4', repeat: 10, opcode: 2 },
    { send: 'a4', bytes: Buffer.from([0x22, 0xc3, 0x28, 0x22]).toString('base64') },
    { send: 'o', close: 1001 },
    { open: 'a5', path: agent('panel') },
    send('a5', { type: 'message', text: 'an agent called panel' }),
    { open: 'p2', path: '/ws/chat-operator/panel', headers: op('cora') },
    send('a5', { type: 'get_contacts' }),
    { send: 'p', close: 1000 },
    { advance: 30000 },
    send('p2', { type: 'get_contacts' }),
    // the inbox's own room: to the other inbox operators, not to an agent
    // that has the room's id
    send('p2', { type: 'message', text: 'to the inbox' }),
], { answers: { ...working('dev1'), ...workingPanel(), 'POST /chat/messages': ok({}) } });

scenario('history ring of 500', [
    { open: 'o', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { send: 'o', texts: Array.from({ length: 503 }, (_, i) => JSON.stringify({ type: 'message', text: `m${i}` })) },
    send('o', { type: 'get_history', conversation_id: 'dev1' }),
    { open: 'o2', path: '/ws/chat-operator/dev1', headers: op('bert') },
], { answers: {} });

// Frames that end the Node process: String() of an object with a member
// toString throws inside the message listener, and so does reading a
// member of null. Each is played on a fresh relay; the uncaught exception
// is noted as {crash}.
const crash = (name, who, frame) => scenario(`crash: ${name}`, [
    { open: 'o', path: '/ws/chat-operator/dev1', headers: op('anna') },
    { open: 'a', path: agent('dev1') },
    { send: who, text: typeof frame === 'string' ? frame : JSON.stringify(frame) },
], { answers: {}, crash: true });
crash('agent sends null', 'a', 'null');
crash('operator sends null', 'o', 'null');
crash('agent message text with toString', 'a', { type: 'message', text: { toString: 1 } });
crash('operator message text with toString', 'o', { type: 'message', text: { toString: 'x' } });
crash('operator message text list with toString', 'o', { type: 'message', text: [{ toString: 1 }] });
crash('operator reaction message id with toString', 'o', { type: 'reaction', message_id: { toString: 1 }, emoji: 'x' });
crash('operator reaction emoji with toString', 'o', { type: 'reaction', message_id: '1', emoji: { toString: 1 } });
crash('operator reaction conversation with toString', 'o', { type: 'reaction', conversation_id: { toString: 1 }, message_id: '1', emoji: 'x' });
crash('operator group name with toString', 'o', { type: 'create_group', name: { toString: 1 }, member_ids: [] });
crash('agent file share with toString is no crash', 'a', { type: 'file_share', file_id: { toString: 1 } });
crash('operator group name with toString but no list is no crash', 'o', { type: 'create_group', name: { toString: 1 }, member_ids: 'x' });

(async () => {
    const quietLog = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {}; console.warn = () => {}; console.error = () => {};
    process.on('uncaughtException', (err) => {
        crashed = err.message;
        // The process would end here: every connection goes with it
        for (const s of sockets.values()) s.sock.destroy();
    });
    const results = [];
    for (const sc of S) {
        // A fresh relay for every scenario: the module keeps its rooms
        delete require.cache[require.resolve(path.join(WEB, 'services/chatRelay.js'))];
        const relay = require(path.join(WEB, 'services/chatRelay.js'));
        const server = http.createServer((_q, r) => { r.statusCode = 404; r.end(); });
        const start = clock;
        answers = JSON.parse(JSON.stringify(sc.answers || {}));
        relay.initChatRelay(server, sessionMiddleware, goApi);
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
        server.close();
        results.push({ name: sc.name, clock: start, answers: sc.answers || {}, crash: !!sc.crash, steps });
        timers.clear();
        clock += 1000;
    }
    Object.assign(console, quietLog);
    const file = path.join(__dirname, '..', 'node_relay.json');
    fs.writeFileSync(file, `${JSON.stringify({ scenarios: results }, null, 1)}\n`);
    console.log(`${file}: ${results.length} scenarios, ${results.reduce((n, r) => n + r.steps.length, 0)} steps`);
    process.exit(0);
})().catch((err) => { console.error(err); process.exit(1); });
