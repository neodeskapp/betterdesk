'use strict';

/**
 * Runs the old console's services/wsRelay.js on a real HTTP server, with
 * fake signal and relay servers on TCP, and plays scenarios against it
 * (see README.md). Stubbed are only what the service reached out to: the
 * session middleware (the session is the JSON of the X-Oracle-Session
 * header), the permission lookup (admin and operator have device.connect,
 * "throw" makes the lookup fail), the API server's client (scripted guest
 * links; every call is recorded with its answer), the guest cookie's name,
 * the configuration of the two addresses and the machine's interfaces.
 * Date.now and the timers are a made-up clock that moves only when a step
 * says so.
 *
 * After each step it records what every browser socket got (the answer's
 * head, messages, the close frame, the end), what each fake server
 * accepted, received and saw closed, which API calls were made with their
 * answers, and what a call returned.
 *
 *   node relay.js     writes ../node_relay.json
 */

const fs = require('fs');
const http = require('http');
const net = require('net');
const os = require('os');
const path = require('path');
const crypto = require('crypto');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

// ---- The clock ----

const real = { setTimeout, clearTimeout, setInterval, clearInterval };
const START = 1700000000000;
let clock = START;
let timerSeq = 0;
const timers = new Map();
const fakeTimer = (fn, delay, repeat, args) => {
    const id = { id: ++timerSeq, unref() { return this; }, ref() { return this; }, hasRef() { return true; }, [Symbol.toPrimitive]() { return this.id; } };
    let d = Number(delay);
    if (!(d >= 1 && d <= 2147483647)) d = 1;
    d = Math.trunc(d);
    timers.set(id.id, { at: clock + d, delay: d, fn, repeat, args, seq: id.id });
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

// ---- Stubs ----

const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
const fresh = (rel) => { delete require.cache[require.resolve(path.join(WEB, rel))]; };

stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, perm) => {
        await new Promise((r) => setImmediate(r));
        if (role === 'throw') throw new Error('lookup failed');
        return perm === 'device.connect' && (role === 'admin' || role === 'operator');
    },
});
stub('middleware/guestAccess.js', { GUEST_COOKIE: 'bd.guest' });

// The API server: guest links by token. expires is milliseconds after the
// scenario's start; null leaves expires_at out.
let api;
let events = [];
// activity counts bytes that arrived, so a long transfer is not taken for
// quiet while no new event is noted.
let activity = 0;
const note = (e) => events.push(e);
function newApi() {
    return {
        links: {
            'tok-valid': { id: 'g1', expires: 600000 },
            'tok-second': { id: 'g1', expires: 600000 },
            'tok-other': { id: 'g2', expires: 600000 },
            'tok-short': { id: 'g3', expires: 20000 },
            'tok-short-long': { id: 'g3', expires: 600000 },
            'tok-noexp': { id: 'g4', expires: null },
            'tok-num': { id: 5, expires: 600000 },
            'tok-null': { id: 'g6', expires: 'null' },
            'tok-noid': { id: '', expires: 600000 },
            'tok-consume-fail': { id: 'g7', expires: 600000, consumeFails: true },
            'tok-hold': { id: 'g8', expires: 600000, hold: true },
        },
        revoked: new Set(),
        down: false,
        held: [],
    };
}
function linkData(token, link) {
    const data = { valid: true, id: link.id, token_prefix: token.slice(0, 4), peer_ids: ['TARGET01'], view_only: false, label: 'oracle' };
    if (link.expires === 'null') data.expires_at = null;
    else if (link.expires !== null) data.expires_at = new Date(START + link.expires).toISOString();
    return data;
}
const failure = (status, message) => {
    const err = new Error(message);
    err.response = { status, data: { error: message } };
    return err;
};
const apiClient = {
    async get(url, opts) {
        await new Promise((r) => setImmediate(r));
        const call = { api: `GET ${url}`, params: JSON.stringify(opts.params) };
        note(call);
        const { token } = opts.params;
        const link = api.links[token];
        if (api.down || token === 'tok-throw') {
            call.answer = { status: 502, error: 'Request failed with status code 502' };
            throw failure(502, call.answer.error);
        }
        let data;
        if (!link) data = { valid: false, error: 'guest link not found' };
        else if (api.revoked.has(token)) data = { valid: false, error: 'guest link revoked' };
        else data = linkData(token, link);
        call.answer = { status: 200, data };
        return { status: 200, data };
    },
    async post(url, body) {
        await new Promise((r) => setImmediate(r));
        const call = { api: `POST ${url}`, body: JSON.stringify(body) };
        note(call);
        if (url === '/guest/access-links/consume') {
            const link = api.links[body.token];
            if (link && link.hold) {
                await new Promise((resolve) => api.held.push(resolve));
            }
            if (api.down || !link || link.consumeFails || api.revoked.has(body.token)) {
                call.answer = { status: 403, error: 'Request failed with status code 403' };
                throw failure(403, call.answer.error);
            }
            call.answer = { status: 200, data: linkData(body.token, link) };
            return { status: 200, data: call.answer.data };
        }
        call.answer = { status: 200, data: { success: true } };
        return { status: 200, data: call.answer.data };
    },
};
stub('services/betterdeskApi.js', { apiClient });

// ---- Fake signal and relay servers ----

const RELAY_HOST = '127.0.0.2';
function fakeServer(name, host) {
    const conns = [];
    const server = net.createServer((sock) => {
        const id = `${name}#${conns.length + 1}`;
        conns.push(sock);
        note({ tcp: id, accept: true });
        let entry = null;
        sock.on('data', (d) => {
            if (!entry || entry.step !== stepNo) {
                entry = { tcp: id, chunks: [], step: stepNo };
                note(entry);
            }
            entry.chunks.push(d);
            activity += d.length;
        });
        sock.on('close', () => note({ tcp: id, closed: true }));
        sock.on('error', () => {});
    });
    return { server, conns, listen: () => new Promise((r) => server.listen(0, host, () => r(server.address().port))) };
}

// ---- Browser sockets ----

const sockets = new Map();
const showBinary = (buf) => (buf.length > 4096
    ? { length: buf.length, sha256: crypto.createHash('sha256').update(buf).digest('hex') }
    : buf.toString('base64'));

function openSocket(name, port, target, headers) {
    const sock = net.connect(port, '127.0.0.1');
    const s = { name, sock, buf: Buffer.alloc(0), head: null, ended: false };
    sockets.set(name, s);
    const mine = (e) => note({ socket: name, ...e });
    sock.on('data', (chunk) => {
        s.buf = Buffer.concat([s.buf, chunk]);
        activity += chunk.length;
        if (s.head === null) {
            const at = s.buf.indexOf('\r\n\r\n');
            if (at < 0) return;
            s.head = s.buf.subarray(0, at + 4).toString('latin1');
            s.buf = s.buf.subarray(at + 4);
            mine({ head: s.head });
            if (!s.head.startsWith('HTTP/1.1 101')) {
                if (s.buf.length) mine({ body: s.buf.toString('latin1') });
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
            if (op === 1) mine({ text: payload.toString('utf8') });
            else if (op === 2) mine({ binary: showBinary(payload) });
            else if (op === 8) {
                mine({ close: payload.length >= 2 ? payload.readUInt16BE(0) : 1005, reason: payload.subarray(2).toString('utf8') });
                if (!s.closeSent) writeFrame(s, 8, payload.subarray(0, 2));
            }
        }
    });
    sock.on('close', () => { if (!s.ended) { s.ended = true; mine({ end: true }); } });
    sock.on('error', () => {});
    const all = { Host: 'console.test', Upgrade: 'websocket', Connection: 'Upgrade',
        'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==', 'Sec-WebSocket-Version': '13', ...(headers || {}) };
    const lines = [`GET ${target} HTTP/1.1`];
    for (const [k, v] of Object.entries(all)) if (v !== null) lines.push(`${k}: ${v}`);
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

// ---- Steps ----

let stepNo = 0;
const quiet = (n = 8) => new Promise((resolve) => {
    let last = events.length + activity;
    let still = 0;
    const tick = () => {
        if (events.length + activity !== last) { last = events.length + activity; still = 0; }
        if (++still >= n) return resolve();
        real.setTimeout(tick, 10);
    };
    real.setTimeout(tick, 10);
});

function payloadOf(step) {
    if (step.hex !== undefined) return Buffer.from(step.hex, 'hex');
    if (step.repeat !== undefined) return Buffer.alloc(step.repeat, step.byte || 0x78);
    return Buffer.from(step.text || '');
}

async function run(step, env) {
    stepNo++;
    const out = {};
    if (step.open) {
        const headers = { ...(step.headers || {}) };
        if (step.session !== undefined) headers['X-Oracle-Session'] = JSON.stringify(step.session);
        openSocket(step.open, env.port, step.path, headers);
    }
    if (step.send) {
        const s = sockets.get(step.send);
        if (step.close !== undefined) writeFrame(s, 8, Buffer.from([step.close >> 8, step.close & 0xff]));
        else if (step.drop) s.sock.destroy();
        else writeFrame(s, step.text !== undefined ? 1 : 2, payloadOf(step));
    }
    if (step.tcp) {
        const srv = env.servers[step.tcp];
        const conn = srv.conns[(step.conn || srv.conns.length) - 1];
        if (step.end) conn.end();
        else if (step.destroy) conn.resetAndDestroy();
        else conn.write(payloadOf(step));
    }
    if (step.revoke) api.revoked.add(step.revoke);
    if (step.down !== undefined) api.down = step.down;
    if (step.release) { const held = api.held.splice(0); for (const r of held) r(); }
    if (step.advance) {
        await quiet();
        advance(step.advance);
    }
    if (step.call === 'invalidate') out.result = env.wsRelay.invalidateGuestGrant(step.args[0]);
    await quiet(step.repeat && step.repeat > 1 << 20 ? 60 : 8);
    out.events = events.map((e) => (e.chunks ? { tcp: e.tcp, data: showBinary(Buffer.concat(e.chunks)) } : e));
    events = [];
    return out;
}

// ---- Rendezvous messages ----

const protobuf = require(require.resolve('protobufjs', { paths: [WEB] }));
const RV = protobuf.loadSync(path.join(WEB, 'protos/rendezvous.proto')).lookupType('hbb.RendezvousMessage');
const enc = (obj) => Buffer.from(RV.encode(RV.create(obj)).finish());
function frame(buf) {
    const n = buf.length;
    let h;
    if (n <= 0x3f) h = Buffer.from([n << 2]);
    else if (n <= 0x3fff) { h = Buffer.alloc(2); h.writeUInt16LE((n << 2) | 1); }
    else { const v = n * 4 + 2; h = Buffer.from([v & 0xff, (v >>> 8) & 0xff, (v >>> 16) & 0xff]); }
    return Buffer.concat([h, buf]);
}
const P = 'TARGET01';
const punch = (id) => enc({ punchHoleRequest: { id } });
const reqRelay = (id) => enc({ requestRelay: { id, uuid: 'uuid-1' } });
const hc = enc({ hc: { token: 'oracle' } });
const hex = (b) => b.toString('hex');

const S = [];
const scenario = (name, steps, opts = {}) => S.push({ name, steps, ...opts });
const admin = { userId: 1, user: { id: 1, username: 'admin', role: 'admin' } };
const operator = { userId: 2, user: { id: 2, username: 'op', role: 'operator' } };
const guestQ = (token, session = 's1', peer = P, extra = '') => `guest=${token}&peer_id=${peer}&guest_session=${session}${extra}`;

scenario('checks', [
    { open: 'a', path: '/ws/rendezvous' },
    { open: 'b', path: '/ws/relay' },
    { open: 'c', path: '/ws/rendezvous', session: {} },
    { open: 'd', path: '/ws/rendezvous', session: { userId: 3, user: { role: 'viewer' } } },
    { open: 'e', path: '/ws/relay', session: { userId: 3, user: { role: 'viewer' } } },
    { open: 'f', path: '/ws/rendezvous', session: { userId: 3, user: { role: 'throw' } } },
    { open: 'g', path: '/ws/rendezvous', session: admin, headers: { Origin: 'https://evil.example', Cookie: 'bd.sid=x' } },
    { open: 'h', path: '/ws/rendezvous', session: admin, headers: { Cookie: 'bd.sid=x' } },
    { open: 'i', path: '/ws/rendezvous?guest=tok-valid' },
    { open: 'j', path: '/ws/rendezvous?guest=tok-valid&peer_id=ab&guest_session=s' },
    { open: 'k', path: '/ws/rendezvous?guest=tok-valid&peer_id=TARGET01' },
    { open: 'l', path: `/ws/rendezvous?${guestQ('tok-valid', 'x'.repeat(129))}` },
    { open: 'm', path: `/ws/rendezvous?${guestQ('tok-invalid', 'x'.repeat(128))}` },
    { open: 'n', path: `/ws/relay?${guestQ('tok-throw')}` },
    { open: 'o', path: '/ws/rendezvous?t=tok-invalid&peer_id=TARGET01&guest_session=s' },
    { open: 'p', path: '/ws/rendezvous?guest=&t=tok-invalid&peer_id=TARGET01&guest_session=s' },
    { open: 'q', path: '/ws/rendezvous?guest=%20%20&t=tok-invalid&peer_id=TARGET01&guest_session=s' },
    { open: 'r', path: '/ws/rendezvous?peer_id=TARGET01&guest_session=s', headers: { Cookie: 'bd.guest=tok-invalid' } },
    { open: 's', path: '/ws/rendezvous?peer_id=TARGET01&guest_session=s', headers: { Cookie: 'bd.guest=%E0%A4%A; betterdesk.guest=tok-invalid' } },
    { open: 't', path: '/ws/rendezvous?peer_id=TARGET01&guest_session=s', headers: { Cookie: 'bd.guest=; betterdesk.guest=tok-invalid' } },
    { open: 'u', path: '/ws/rendezvous?peer_id=%20TARGET01%20&guest_session=s', headers: { Cookie: 'x=1;  bd.guest=%20tok-invalid%20 ' } },
    { open: 'v', path: '/ws/rendezvous', session: { userId: 0, user: { role: 'admin' } } },
    { open: 'w', path: `/ws/rendezvous?${guestQ('tok-invalid', 'x'.repeat(63) + encodeURIComponent('\u{1F600}').repeat(33))}` },
    { open: 'w2', path: `/ws/rendezvous?${guestQ('tok-invalid', 'x'.repeat(62) + encodeURIComponent('\u{1F600}').repeat(33))}` },
    { open: 'x', path: '/ws/rendezvous', session: admin, headers: { 'Sec-WebSocket-Version': '12' } },
    { open: 'y', path: '/ws/relay', session: admin, headers: { 'Sec-WebSocket-Key': null } },
    { open: 'z', path: '/ws/rendezvous?guest=tok-valid&peer_id=TARGET%2001&guest_session=s' },
    { open: 'aa', path: '/ws/rendezvous', session: { userId: 3, user: { role: 'admin' } }, headers: { Origin: 'http://console.test' } },
    { open: 'ab', path: '/ws/rendezvous', session: { userId: 3, role: 'admin' } },
]);

scenario('signal bridge', [
    { open: 'a', path: '/ws/rendezvous', session: operator },
    { tcp: 'hbbs', hex: '0a0b0c' },
    { send: 'a', hex: '010203' },
    { send: 'a', text: 'text goes on as bytes' },
    { tcp: 'hbbs', hex: '00' },
    { tcp: 'hbbs', end: true },
    { open: 'b', path: `/ws/rendezvous?${guestQ('tok-valid')}`, session: admin },
    { send: 'b', hex: hex(frame(punch('OTHER01'))) },
    { send: 'b', close: 1000 },
    { open: 'c', path: '/ws/rendezvous', session: admin },
    { tcp: 'hbbs', destroy: true },
    { open: 'd', path: '/ws/rendezvous', session: admin },
    { send: 'd', drop: true },
    { open: 'e', path: '/ws/relay', session: admin },
    { send: 'e', hex: hex(frame(reqRelay('X'))) },
    { tcp: 'hbbr', hex: hex(frame(hc)) + '05' },
    { send: 'e', close: 4000 },
]);

scenario('relay messages', [
    { open: 'a', path: '/ws/relay?transport=message', session: admin },
    { send: 'a', hex: '010203' },
    { send: 'a', hex: '' },
    { send: 'a', repeat: 100, byte: 0x41 },
    { send: 'a', repeat: 20000, byte: 0x42 },
    { send: 'a', text: 'hi' },
    { tcp: 'hbbr', hex: '0c414243' + '00' + '08ffff' },
    { tcp: 'hbbr', hex: '0d01' },
    { tcp: 'hbbr', hex: '5a' },
    { tcp: 'hbbr', hex: '0e' },
    { tcp: 'hbbr', hex: '0000' + '01' },
    { tcp: 'hbbr', hex: '0d' },
    { tcp: 'hbbr', hex: '0d05' + '41'.repeat(321) },
    { tcp: 'hbbr', hex: '0700000' + '1' },
    { open: 'b', path: '/ws/relay?transport=message&transport=raw', session: admin },
    { tcp: 'hbbr', hex: '0d05' },
    { open: 'c', path: '/ws/relay?transport=raw&transport=message', session: admin },
    { tcp: 'hbbr', hex: '0d05' },
    { send: 'c', hex: '0d05' },
    { open: 'd', path: '/ws/relay?transport=Message', session: admin },
    { send: 'd', hex: '0d05' },
    { open: 'e', path: '/ws/rendezvous?transport=message', session: admin },
    { send: 'e', hex: '0d05' },
]);

scenario('relay frame limits', [
    { open: 'a', path: '/ws/relay?transport=message', session: admin },
    { send: 'a', repeat: 64 * 1024 * 1024, byte: 0x61 },
    { send: 'a', repeat: 64 * 1024 * 1024 + 1, byte: 0x62 },
    { open: 'b', path: '/ws/relay?transport=message', session: admin },
    { tcp: 'hbbr', hex: '07000010' },
    { open: 'c', path: '/ws/relay?transport=message', session: admin },
    { tcp: 'hbbr', hex: '03000010' },
    { tcp: 'hbbr', repeat: 4 * 1024 * 1024, byte: 0x63 },
]);

const many = [];
for (let i = 1; i <= 5; i++) many.push({ open: `s${i}`, path: i % 2 ? '/ws/rendezvous' : '/ws/relay', session: admin, headers: { 'X-Forwarded-For': '10.0.0.1' } });
scenario('per-address limit', [
    ...many,
    { open: 'over', path: '/ws/relay', session: admin, headers: { 'X-Forwarded-For': '10.0.0.1' } },
    { open: 'other', path: '/ws/rendezvous', session: admin, headers: { 'X-Forwarded-For': '10.0.0.2' } },
    { open: 'list', path: '/ws/rendezvous', session: admin, headers: { 'X-Forwarded-For': ' 10.0.0.1 , 10.0.0.9' } },
    { send: 's1', close: 1000 },
    { open: 'again', path: '/ws/rendezvous', session: admin, headers: { 'X-Forwarded-For': '10.0.0.1' } },
    { open: 'again2', path: '/ws/rendezvous', session: admin, headers: { 'X-Forwarded-For': '10.0.0.1' } },
    { open: 'socket1', path: '/ws/rendezvous', session: admin },
    { open: 'socket2', path: '/ws/rendezvous', session: admin, headers: { 'X-Forwarded-For': '' } },
]);

scenario('idle', [
    { open: 'a', path: '/ws/rendezvous', session: admin },
    { advance: 119999 },
    { advance: 1 },
    { open: 'b', path: '/ws/relay', session: admin },
    { advance: 100000 },
    { send: 'b', hex: '01' },
    { advance: 100000 },
    { tcp: 'hbbr', hex: '02' },
    { advance: 119999 },
    { advance: 1 },
]);

scenario('guest signal', [
    { open: 'a', path: `/ws/rendezvous?${guestQ('tok-valid')}` },
    { send: 'a', hex: hex(frame(hc)) },
    { send: 'a', hex: hex(frame(punch(P))) },
    { send: 'a', hex: hex(frame(punch(P))) + hex(frame(hc)) },
    { tcp: 'hbbs', hex: '0a0b' },
    { send: 'a', hex: hex(frame(punch('OTHER01'))) },
    { open: 'b', path: `/ws/rendezvous?${guestQ('tok-valid', 's2')}` },
    { send: 'b', hex: hex(frame(reqRelay(P))).slice(0, 10) },
    { send: 'b', hex: hex(frame(reqRelay(P))).slice(10) },
    { send: 'b', hex: hex(frame(Buffer.from('ff', 'hex'))) },
    { open: 'c', path: `/ws/rendezvous?${guestQ('tok-valid', 's3')}` },
    { send: 'c', hex: hex(frame(Buffer.concat([punch(P), reqRelay('OTHER01')]))) },
    { send: 'c', hex: hex(frame(Buffer.concat([reqRelay('OTHER01'), punch(P)]))) },
    { send: 'c', hex: hex(frame(Buffer.concat([Buffer.from([0x40, 0x16]), Buffer.from('0a08' + Buffer.from(P).toString('hex') + '920109' + '0a074f544845523031', 'hex')]))) },
    { send: 'c', hex: hex(frame(reqRelay('OTHER01'))) },
    { open: 'd', path: `/ws/rendezvous?${guestQ('tok-valid', 's4')}` },
    { send: 'd', hex: '07000010' },
    { open: 'e', path: `/ws/rendezvous?${guestQ('tok-valid', 's5')}` },
    { send: 'e', hex: hex(punch(P)) },
    { open: 'f', path: `/ws/rendezvous?${guestQ('tok-valid', 's6')}` },
    { send: 'f', hex: hex(frame(hc)) },
    { send: 'f', close: 1000 },
    { open: 'g', path: `/ws/rendezvous?${guestQ('tok-valid', 's7')}` },
    { send: 'g', hex: hex(frame(punch(P))) },
    { tcp: 'hbbs', end: true },
]);

scenario('guest relay', [
    { open: 'a', path: `/ws/relay?transport=message&${guestQ('tok-valid')}` },
    { send: 'a', hex: hex(reqRelay(P)) },
    { send: 'a', hex: hex(reqRelay('OTHER01')) },
    { tcp: 'hbbr', hex: hex(frame(hc)) },
    { open: 'b', path: `/ws/relay?transport=message&${guestQ('tok-valid', 's2')}` },
    { send: 'b', hex: hex(reqRelay('OTHER01')) },
    { open: 'c', path: `/ws/relay?${guestQ('tok-valid', 's3')}` },
    { send: 'c', hex: hex(frame(reqRelay(P))) },
    { send: 'c', repeat: 1024 * 1024 - 100, byte: 0xff },
    { send: 'c', repeat: 100, byte: 0xff },
    { open: 'd', path: `/ws/relay?transport=message&${guestQ('tok-valid', 's4')}` },
    { send: 'd', hex: hex(reqRelay(P)).slice(0, 12) },
    { send: 'd', hex: hex(reqRelay(P)).slice(12) },
    { send: 'd', hex: '0102' },
    { open: 'e', path: `/ws/relay?${guestQ('tok-valid', 's5')}` },
    { send: 'e', hex: hex(reqRelay(P)) },
    { open: 'f', path: `/ws/relay?transport=message&${guestQ('tok-valid', 's6')}` },
    { send: 'f', hex: '' },
    { open: 'g', path: `/ws/relay?transport=message&${guestQ('tok-valid', 's7')}` },
    { send: 'g', hex: hex(Buffer.concat([reqRelay(P), punch('OTHER01')])) },
]);

scenario('guest uses', [
    { open: 'a', path: `/ws/rendezvous?${guestQ('tok-valid')}` },
    { send: 'a', hex: hex(frame(punch(P))) },
    { open: 'b', path: `/ws/relay?transport=message&${guestQ('tok-valid')}` },
    { send: 'b', hex: hex(reqRelay(P)) },
    { open: 'c', path: `/ws/rendezvous?${guestQ('tok-second')}` },
    { send: 'c', hex: hex(frame(punch(P))) },
    { open: 'd', path: `/ws/rendezvous?${guestQ('tok-valid', 'other')}` },
    { send: 'd', hex: hex(frame(punch(P))) },
    { open: 'e', path: `/ws/rendezvous?${guestQ('tok-consume-fail')}` },
    { send: 'e', hex: hex(frame(punch(P))) },
    { open: 'f', path: `/ws/rendezvous?${guestQ('tok-consume-fail')}` },
    { send: 'f', hex: hex(frame(punch(P))) },
    { open: 'g', path: `/ws/rendezvous?${guestQ('tok-num')}` },
    { send: 'g', hex: hex(frame(punch(P))) },
    { open: 'h', path: `/ws/rendezvous?${guestQ('tok-noid')}`, headers: { 'X-Forwarded-For': '10.9.9.7' } },
    { send: 'h', hex: hex(frame(punch(P))) },
    { open: 'i', path: `/ws/rendezvous?${guestQ('tok-noid', 's2')}`, headers: { 'X-Forwarded-For': '10.9.9.8' } },
    { send: 'i', hex: hex(frame(punch(P))) },
    // A link without an id counts its uses under "unknown"
    { call: 'invalidate', args: ['unknown'] },
    { open: 'j', path: `/ws/rendezvous?${guestQ('tok-noid', 's2')}`, headers: { 'X-Forwarded-For': '10.9.9.9' } },
    { send: 'j', hex: hex(frame(punch(P))) },
]);

// A counted use is remembered until the link's end: tok-short and
// tok-short-long are the same link (g3) with different ends.
scenario('guest use expires', [
    { open: 'a', path: `/ws/rendezvous?${guestQ('tok-short')}` },
    { send: 'a', hex: hex(frame(punch(P))) },
    { advance: 15000 },
    { open: 'b', path: `/ws/rendezvous?${guestQ('tok-short-long')}` },
    { send: 'b', hex: hex(frame(punch(P))) },
    { advance: 5000 },
    // b has used the link once: it does not count again
    { send: 'b', hex: hex(frame(punch(P))) },
    { open: 'c', path: `/ws/rendezvous?${guestQ('tok-short-long')}` },
    { send: 'c', hex: hex(frame(punch(P))) },
]);

// Every advance ends on the time a check is due, as the old service armed
// the next check only after the answer, when the made-up clock had moved
// on.
scenario('guest revalidation', [
    { open: 'a', path: `/ws/rendezvous?${guestQ('tok-valid')}` },
    { send: 'a', hex: hex(frame(punch(P))) },
    { open: 'b', path: `/ws/rendezvous?${guestQ('tok-short', 's2')}` },
    { open: 'c', path: `/ws/rendezvous?${guestQ('tok-other', 's3')}` },
    { advance: 14999 },
    { advance: 1 },
    { down: true },
    { advance: 5000 },
    { advance: 10000 },
    { down: false },
    { revoke: 'tok-valid' },
    { advance: 14999 },
    { advance: 1 },
    { open: 'd', path: `/ws/rendezvous?${guestQ('tok-noexp', 's4')}` },
    { open: 'e', path: `/ws/rendezvous?${guestQ('tok-null', 's5')}` },
    { advance: 15000 },
]);

scenario('guest revoked', [
    { open: 'a', path: `/ws/rendezvous?${guestQ('tok-valid')}` },
    { send: 'a', hex: hex(frame(punch(P))) },
    { open: 'b', path: `/ws/relay?transport=message&${guestQ('tok-second', 's2')}` },
    { open: 'c', path: `/ws/rendezvous?${guestQ('tok-other', 's3')}` },
    { open: 'd', path: `/ws/rendezvous?${guestQ('tok-num', 's4')}` },
    { call: 'invalidate', args: ['g1'] },
    { call: 'invalidate', args: ['g1'] },
    { call: 'invalidate', args: ['nope'] },
    { call: 'invalidate', args: ['5'] },
    { call: 'invalidate', args: [''] },
    { open: 'e', path: `/ws/rendezvous?${guestQ('tok-valid')}` },
    { send: 'e', hex: hex(frame(punch(P))) },
    { call: 'invalidate', args: ['g1'] },
    { open: 'f', path: `/ws/rendezvous?${guestQ('tok-valid')}` },
    { send: 'f', hex: hex(frame(punch(P))) },
]);

scenario('signal refused', [
    { open: 'a', path: '/ws/rendezvous', session: admin },
    { open: 'b', path: '/ws/relay', session: admin },
    { send: 'b', hex: '01' },
], { signalClosed: true });

// Node lets a later frame through while an earlier one waits for the use
// of the link to be counted; the Go bridge keeps the order. Not replayed:
// TestGuestFramesKeepTheirOrder pins the Go side.
scenario('guest ordering', [
    { open: 'a', path: `/ws/rendezvous?${guestQ('tok-hold')}` },
    { send: 'a', hex: hex(frame(punch(P))) },
    { send: 'a', hex: hex(frame(hc)) },
    { release: true },
], { differs: true });

// ---- The relay address: loopback replaced by another interface ----

const hostCases = [
    { host: 'localhost', ifaces: { lo: [{ family: 'IPv4', address: '127.0.0.1', internal: true }], eth0: [{ family: 'IPv6', address: 'fe80::1', internal: false }, { family: 'IPv4', address: '10.1.2.3', internal: false }] } },
    { host: 'LocalHost', ifaces: { eth0: [{ family: 'IPv4', address: '10.1.2.3', internal: false }], eth1: [{ family: 'IPv4', address: '10.9.9.9', internal: false }] } },
    { host: '127.0.0.1', ifaces: { lo: [{ family: 'IPv4', address: '127.0.0.1', internal: true }] } },
    { host: '127.5.6.7', ifaces: { docker0: [{ family: 'IPv4', address: '172.17.0.1', internal: false }] } },
    { host: '::1', ifaces: { eth0: [{ family: 'IPv4', address: '192.168.1.5', internal: false }] } },
    { host: '[::1]', ifaces: { eth0: [{ family: 'IPv4', address: '192.168.1.5', internal: false }] } },
    { host: '0:0:0:0:0:0:0:1', ifaces: { eth0: [{ family: 'IPv4', address: '192.168.1.5', internal: false }] } },
    { host: '127example', ifaces: { eth0: [{ family: 'IPv4', address: '192.168.1.5', internal: false }] } },
    { host: 'relay.example', ifaces: { eth0: [{ family: 'IPv4', address: '192.168.1.5', internal: false }] } },
    { host: '10.0.0.7', ifaces: {} },
    { host: '', ifaces: { eth0: [{ family: 'IPv4', address: '192.168.1.5', internal: false }] } },
    { host: 'localhost', ifaces: {} },
];

async function hostTargets() {
    const out = [];
    const origLog = console.log;
    const origWarn = console.warn;
    const origIfaces = os.networkInterfaces;
    for (const c of hostCases) {
        const lines = [];
        console.log = (...a) => lines.push(a.join(' '));
        console.warn = (...a) => lines.push(a.join(' '));
        os.networkInterfaces = () => c.ifaces;
        stub('config/config.js', { wsProxy: { hbbsHost: 'sig.example', hbbsPort: 21116, hbbrHost: c.host, hbbrPort: 21117 } });
        fresh('services/wsRelay.js');
        fresh('services/wsUpgradeRouter.js');
        require(path.join(WEB, 'services/wsRelay.js')).initWsProxy(http.createServer(), () => {});
        out.push({ host: c.host, ifaces: c.ifaces, lines: lines.filter((l) => l.includes('WebSocket proxy')) });
    }
    console.log = origLog;
    console.warn = origWarn;
    os.networkInterfaces = origIfaces;
    return out;
}

async function main() {
    const hosts = await hostTargets();
    const recorded = [];
    for (const sc of S) {
        clock = START;
        timers.clear();
        events = [];
        api = newApi();
        sockets.clear();
        const hbbs = fakeServer('hbbs', '127.0.0.1');
        const hbbr = fakeServer('hbbr', RELAY_HOST);
        let hbbsPort = await hbbs.listen();
        const hbbrPort = await hbbr.listen();
        if (sc.signalClosed) {
            await new Promise((r) => hbbs.server.close(r));
            hbbsPort = await new Promise((r) => { const s = net.createServer(); s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => r(p)); }); });
        }
        stub('config/config.js', { wsProxy: { hbbsHost: '127.0.0.1', hbbsPort, hbbrHost: '127.0.0.1', hbbrPort } });
        os.networkInterfaces = () => ({ lo: [{ family: 'IPv4', address: '127.0.0.1', internal: true }], eth0: [{ family: 'IPv4', address: RELAY_HOST, internal: false }] });
        fresh('services/wsRelay.js');
        fresh('services/wsUpgradeRouter.js');
        const wsRelay = require(path.join(WEB, 'services/wsRelay.js'));
        const server = http.createServer((req, res) => { res.writeHead(404); res.end(); });
        const sessionMiddleware = (req, _res, next) => {
            const raw = req.headers['x-oracle-session'];
            req.session = raw ? JSON.parse(raw) : null;
            next();
        };
        const quietLog = console.log;
        console.log = () => {};
        wsRelay.initWsProxy(server, sessionMiddleware);
        await new Promise((r) => server.listen(0, '127.0.0.1', r));
        const env = { port: server.address().port, servers: { hbbs, hbbr }, wsRelay };
        const steps = [];
        for (const step of sc.steps) steps.push({ step, ...(await run(step, env)) });
        for (const s of sockets.values()) s.sock.destroy();
        await quiet();
        console.log = quietLog;
        if (api.held.length) for (const r of api.held.splice(0)) r();
        await quiet();
        for (const srv of [hbbs, hbbr]) { for (const c of srv.conns) c.destroy(); srv.server.close(); }
        server.close();
        events = [];
        recorded.push({ name: sc.name, differs: !!sc.differs, signalClosed: !!sc.signalClosed, steps });
        console.error(`${sc.name}: ${steps.length} steps`);
    }
    fs.writeFileSync(path.join(__dirname, '..', 'node_relay.json'),
        `${JSON.stringify({ start: START, relayHost: RELAY_HOST, hosts, scenarios: recorded }, null, 1)}\n`);
    process.exit(0);
}

main().catch((err) => { console.error(err); process.exit(1); });
