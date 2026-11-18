'use strict';

/**
 * Records what the old console's services/wsRelay.js does with bytes, for
 * the Go tests to replay (see README.md):
 *
 *   - the RustDesk frame header it writes (encodeRelayFrame) for payloads
 *     of every header size and over the limit;
 *   - how its frame decoder cuts streams into frames (createRelayFrameDecoder),
 *     fed in random chunks, with headers of every form, and the two limits;
 *   - how it decodes rendezvous messages for the guest check
 *     (decodeRendezvousPayload, guestTargetMatches) with protobufjs: valid
 *     messages, both requests in one message, wrong wire types, strings
 *     longer than their message, nested groups at the depth limit, long
 *     varints near the end, every truncation and many one-byte changes of
 *     some messages, and random bytes;
 *   - the schema protobufjs reads from protos/rendezvous.proto, which the Go
 *     decoder takes from the server's generated code.
 *
 *   node codec.js     writes ../node_codec.json
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('middleware/auth.js', { roleHasEffectivePermission: async () => false });
stub('services/betterdeskApi.js', { apiClient: { get: async () => ({ data: {} }), post: async () => ({ data: {} }) } });

const wsRelay = require(path.join(WEB, 'services/wsRelay.js'));
const protobuf = require(require.resolve('protobufjs', { paths: [WEB] }));
const root = protobuf.loadSync(path.join(WEB, 'protos/rendezvous.proto'));
const RV = root.lookupType('hbb.RendezvousMessage');
const { encodeRelayFrame, createRelayFrameDecoder, MAX_RELAY_FRAME_SIZE } = wsRelay._relayFraming;
const { decodeRendezvousPayload, guestTargetMatches } = wsRelay._guestProtocol;

// ---- A fixed random source ----

let seed = 0x9e3779b9;
function rnd() {
    seed ^= (seed << 13) >>> 0;
    seed ^= seed >>> 17;
    seed ^= (seed << 5) >>> 0;
    seed >>>= 0;
    return seed;
}
const pick = (n) => rnd() % n;
const randomBytes = (n) => Buffer.from(Array.from({ length: n }, () => pick(256)));

// ---- Frames ----

// A payload too big to keep is written as {repeat, byte}.
const big = (n, b) => ({ repeat: n, byte: b });
const bytesOf = (spec) => (typeof spec === 'string' ? Buffer.from(spec, 'hex') : Buffer.alloc(spec.repeat, spec.byte));
const show = (buf) => (buf.length > 256
    ? { length: buf.length, sha256: require('crypto').createHash('sha256').update(buf).digest('hex') }
    : buf.toString('hex'));

const encode = [];
for (const n of [0, 1, 2, 3, 62, 63, 64, 65, 255, 256, 16382, 16383, 16384, 16385, 65535, 65536,
    0x3ffffe, 0x3fffff, 0x400000, 0x400001, MAX_RELAY_FRAME_SIZE - 1, MAX_RELAY_FRAME_SIZE, MAX_RELAY_FRAME_SIZE + 1]) {
    const payload = Buffer.alloc(n, (n % 251) + 1);
    try {
        const frame = encodeRelayFrame(payload);
        encode.push({ length: n, header: frame.subarray(0, frame.length - n).toString('hex'), total: frame.length });
    } catch (err) {
        encode.push({ length: n, error: err.message });
    }
}

// header writes a frame header of the given form (1 to 4 bytes) for a
// payload length, also forms longer than needed.
function header(len, form) {
    const value = len * 4 + (form - 1);
    const out = Buffer.alloc(form);
    for (let i = 0; i < form; i++) out[i] = Math.floor(value / 2 ** (8 * i)) & 0xff;
    return out;
}

const decode = [];
function feedAll(chunks) {
    const d = createRelayFrameDecoder();
    const steps = [];
    for (const spec of chunks) {
        try {
            steps.push({ chunk: spec, frames: d.feed(bytesOf(spec)).map(show) });
        } catch (err) {
            steps.push({ chunk: spec, error: err.message });
            break;
        }
    }
    decode.push(steps);
}

// Every header form for small lengths, whole and byte by byte.
for (const len of [0, 1, 5, 63, 64, 100]) {
    for (let form = 1; form <= 4; form++) {
        if (form === 1 && len > 63) continue;
        if (form === 2 && len > 0x3fff) continue;
        const frame = Buffer.concat([header(len, form), Buffer.alloc(len, 0x41 + form)]);
        feedAll([frame.toString('hex')]);
        feedAll([...frame].map((b) => Buffer.from([b]).toString('hex')));
    }
}
// Random streams of random frames in random chunks.
for (let i = 0; i < 150; i++) {
    const frames = [];
    const count = 1 + pick(6);
    for (let k = 0; k < count; k++) {
        const len = [0, 1, 2, pick(64), pick(300), pick(5000)][pick(6)];
        let form = 1 + pick(4);
        if (form === 1 && len > 63) form = 2;
        if (form === 2 && len > 0x3fff) form = 3;
        frames.push(Buffer.concat([header(len, form), randomBytes(len)]));
    }
    let stream = Buffer.concat(frames);
    if (pick(4) === 0) stream = stream.subarray(0, pick(stream.length + 1));
    const chunks = [];
    for (let at = 0; at < stream.length;) {
        const n = 1 + pick([1, 3, 17, 256, 5000, 70000][pick(6)]);
        chunks.push(stream.subarray(at, at + n).toString('hex'));
        at += n;
    }
    feedAll(chunks.length ? chunks : ['']);
}
// The limits: a header that announces more than the largest frame, and
// more bytes held than a largest frame and its header.
feedAll([header(MAX_RELAY_FRAME_SIZE + 1, 4).toString('hex')]);
feedAll([header(MAX_RELAY_FRAME_SIZE, 4).toString('hex'), '00']);
feedAll(['ffffffff']);
feedAll(['03', '00', '00']);
feedAll([header(MAX_RELAY_FRAME_SIZE, 4).toString('hex'), big(MAX_RELAY_FRAME_SIZE, 0x5a)]);
feedAll([header(MAX_RELAY_FRAME_SIZE, 4).toString('hex'), big(MAX_RELAY_FRAME_SIZE - 1, 0x5a), '5a01']);
feedAll(['01', big(MAX_RELAY_FRAME_SIZE + 4, 0)]);
feedAll([big(MAX_RELAY_FRAME_SIZE + 5, 0)]);

// ---- Rendezvous messages ----

const varint = (n) => {
    const out = [];
    let v = BigInt(n);
    if (v < 0n) v += 1n << 64n;
    do {
        let b = Number(v & 0x7fn);
        v >>= 7n;
        if (v > 0n) b |= 0x80;
        out.push(b);
    } while (v > 0n);
    return Buffer.from(out);
};
const tag = (field, wt) => varint(field * 8 + wt);
const ld = (field, buf) => Buffer.concat([tag(field, 2), varint(buf.length), buf]);
const str = (field, s) => ld(field, Buffer.from(s));
const enc = (obj) => Buffer.from(RV.encode(RV.create(obj)).finish());
const cat = (...parts) => Buffer.concat(parts.map((p) => (Buffer.isBuffer(p) ? p : Buffer.from(p))));

const inputs = [];
const add = (buf) => inputs.push(buf);

const P = 'TARGET01';
const bases = [
    enc({ punchHoleRequest: { id: P } }),
    enc({ punchHoleRequest: { id: P, natType: 1, token: 'tok', connType: 1, udpPort: 21116, forceRelay: true, version: '1.4.2' } }),
    enc({ requestRelay: { id: P, uuid: 'uuid-1', relayServer: 'relay', secure: true, controlPermissions: { permissions: 123456789 } } }),
    enc({ requestRelay: { id: 'OTHER01', uuid: 'u' } }),
    enc({ registerPk: { id: P, uuid: Buffer.from('u'), pk: Buffer.from('k') } }),
    enc({ hc: { token: 'x' } }),
    enc({ httpProxyRequest: { headers: [{ name: 'a', value: 'b' }, { name: 'c', value: 'd' }] } }),
    enc({ onlineRequest: { id: P, peers: ['a', 'b'] } }),
    enc({ configureUpdate: { serial: 1, rendezvousServers: ['x', 'y'] } }),
    Buffer.alloc(0),
];
for (const b of bases) add(b);

// Both requests, in both orders, and repeated requests.
add(cat(enc({ punchHoleRequest: { id: P } }), enc({ requestRelay: { id: 'OTHER01' } })));
add(cat(enc({ requestRelay: { id: 'OTHER01' } }), enc({ punchHoleRequest: { id: P } })));
add(cat(enc({ requestRelay: { id: P } }), enc({ punchHoleRequest: { id: 'OTHER01' } })));
add(cat(enc({ punchHoleRequest: { id: 'OTHER01' } }), enc({ punchHoleRequest: { id: P } })));
add(cat(enc({ punchHoleRequest: { id: P } }), enc({ punchHoleRequest: { natType: 1 } })));
add(cat(enc({ punchHoleRequest: { id: P } }), enc({ punchHoleRequest: { id: P } }), enc({ hc: { token: 'x' } })));
add(cat(enc({ requestRelay: { id: P } }), enc({ requestRelay: { uuid: 'u' } })));
add(cat(enc({ registerPk: { id: 'x' } }), enc({ punchHoleRequest: { id: P } })));
// A request tagged as a varint, whose bytes the signal server reads as
// fields of the top level: request_relay for another device.
{
    const inner = cat(str(1, P), ld(18, str(1, 'OTHER01')));
    add(cat(tag(8, 0), varint(inner.length), inner));
}
// The id twice inside one request.
add(ld(8, cat(str(1, 'OTHER01'), str(1, P))));
add(ld(8, cat(str(1, P), str(1, 'OTHER01'))));
add(ld(18, cat(str(1, P), str(1, ''))));
// Wrong wire types for known fields.
add(cat(tag(8, 0), varint(10), str(1, P)));
add(cat(tag(8, 5), varint(10), str(1, P)));
add(cat(tag(8, 1), varint(10), str(1, P)));
add(cat(tag(8, 3), varint(10), str(1, P)));
add(ld(8, cat(tag(1, 0), varint(8), Buffer.from(P))));
add(ld(8, cat(tag(1, 5), varint(8), Buffer.from(P))));
add(ld(18, cat(tag(1, 0), varint(8), Buffer.from(P))));
add(ld(8, cat(tag(2, 2), varint(1))));
add(ld(18, cat(tag(5, 2), varint(1))));
// A string longer than its message.
add(ld(8, cat(tag(1, 2), varint(100), Buffer.from(P))));
add(ld(8, cat(tag(1, 2), varint(9), Buffer.from(P))));
add(ld(18, cat(tag(1, 2), varint(0xffffffff), Buffer.from(P))));
add(cat(tag(8, 2), varint(10), tag(1, 2), varint(100), Buffer.from(P)));
// Bytes longer than their message.
add(ld(18, cat(tag(3, 2), varint(50), Buffer.from('abc'))));
// Sub-messages longer than the message.
add(cat(tag(8, 2), varint(50), str(1, P)));
add(cat(tag(18, 2), varint(0xffffffff)));
// Unknown fields of every wire type, at the top and inside a request.
for (const wt of [0, 1, 2, 3, 4, 5, 6, 7]) {
    for (const where of ['top', 'punch', 'relay']) {
        let unknown;
        switch (wt) {
            case 0: unknown = cat(tag(99, 0), varint(300)); break;
            case 1: unknown = cat(tag(99, 1), Buffer.alloc(8, 1)); break;
            case 2: unknown = ld(99, Buffer.from('xyz')); break;
            case 3: unknown = cat(tag(99, 3), tag(1, 0), varint(5), tag(99, 4)); break;
            case 5: unknown = cat(tag(99, 5), Buffer.alloc(4, 1)); break;
            default: unknown = cat(tag(99, wt), Buffer.from([1, 2, 3])); break;
        }
        if (where === 'top') add(cat(unknown, enc({ punchHoleRequest: { id: P } })));
        else if (where === 'punch') add(ld(8, cat(unknown, str(1, P))));
        else add(ld(18, cat(str(1, P), unknown)));
    }
}
// Nested groups around the depth limit, at the top and inside a request.
for (const n of [1, 98, 99, 100, 101, 102]) {
    const open = Buffer.concat(Array.from({ length: n }, () => tag(50, 3)));
    const close = Buffer.concat(Array.from({ length: n }, () => tag(50, 4)));
    add(cat(open, close, enc({ punchHoleRequest: { id: P } })));
    add(ld(8, cat(str(1, P), open, close)));
    add(ld(18, cat(str(1, P), ld(9, cat(open, close)))));
}
add(cat(tag(50, 3), tag(51, 4)));
add(cat(tag(50, 3), tag(1, 0)));
add(cat(tag(50, 3)));
add(cat(tag(50, 4)));
// Varints: ten bytes, near the end, and the 64-bit field (permissions).
const tenByte = varint(-1);
add(ld(8, cat(str(1, P), tag(2, 0), tenByte)));
add(ld(8, cat(tag(2, 0), tenByte, str(1, P))));
add(ld(8, cat(str(1, P), tag(2, 0), Buffer.from([0x80, 0x80, 0x80, 0x80, 0x80, 0x01]))));
add(ld(8, cat(str(1, P), tag(2, 0), Buffer.from([0xff, 0xff, 0xff, 0xff, 0x7f]))));
add(ld(8, cat(str(1, P), tag(2, 0), Buffer.from([0xff, 0xff, 0xff, 0xff]))));
add(ld(8, cat(str(1, P), tag(2, 0), Buffer.from([0xff, 0xff, 0xff]))));
add(cat(tag(8, 2), Buffer.from([0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00])));
add(cat(Buffer.from([0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01])));
add(Buffer.from([0x80]));
add(Buffer.from([0x80, 0x80, 0x80, 0x80]));
add(Buffer.from([0x80, 0x80, 0x80, 0x80, 0x80]));
add(Buffer.from([0x80, 0x80, 0x80, 0x80, 0x80, 0x80]));
for (const perm of [[0x01], [0x81, 0x01], [0xff, 0xff, 0xff, 0x7f], [0xff, 0xff, 0xff, 0xff, 0x01], [0xff, 0xff, 0xff, 0xff, 0xff, 0x01],
    [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01],
    [0xff], [0xff, 0xff], [0xff, 0xff, 0xff], [0xff, 0xff, 0xff, 0xff], [0xff, 0xff, 0xff, 0xff, 0xff], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]]) {
    const cp = cat(tag(1, 0), Buffer.from(perm));
    add(ld(18, cat(str(1, P), ld(9, cp))));
    add(ld(18, cat(str(1, P), ld(9, cp), str(2, 'u'))));
    add(ld(18, cat(ld(9, cp), str(1, P))));
    // the 64-bit varint runs over the end of its message into the request
    add(ld(18, cat(str(1, P), tag(9, 2), varint(cp.length - 1), cp)));
    add(ld(18, cat(tag(9, 2), varint(Math.max(0, cp.length - 2)), cp, str(1, P))));
}
// Text that is not UTF-8 as the id.
add(ld(8, cat(tag(1, 2), varint(3), Buffer.from([0xc3, 0x28, 0x41]))));
add(ld(8, cat(tag(1, 2), varint(4), Buffer.from([0xf0, 0x9f, 0x98, 0x80]))));
add(ld(8, cat(tag(1, 2), varint(2), Buffer.from([0xc1, 0x81]))));
add(ld(8, cat(tag(1, 2), varint(3), Buffer.from([0xed, 0xa0, 0x80]))));
// Field numbers at the edges.
add(cat(tag(0, 0), varint(1), enc({ punchHoleRequest: { id: P } })));
add(cat(varint((2 ** 29 - 1) * 8), varint(1), enc({ punchHoleRequest: { id: P } })));
add(cat(Buffer.from([0xc2, 0x80, 0x80, 0x80, 0x10]), str(1, P)));

// Every truncation and many one-byte changes of a few messages.
const values = [0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
    0x12, 0x1a, 0x42, 0x7f, 0x80, 0x81, 0x92, 0xff];
for (const base of [bases[1], bases[2], cat(enc({ punchHoleRequest: { id: P } }), enc({ requestRelay: { id: 'OTHER01' } }))]) {
    for (let n = 0; n < base.length; n++) add(base.subarray(0, n));
    for (let at = 0; at < base.length; at++) {
        for (const v of values) {
            if (base[at] === v) continue;
            const b = Buffer.from(base);
            b[at] = v;
            add(b);
        }
    }
}
// Random bytes: uniform, and made of tags and lengths.
for (let i = 0; i < 2500; i++) add(randomBytes(pick(48)));
const pieces = [() => tag(8, pick(8)), () => tag(18, pick(8)), () => tag(1 + pick(12), pick(8)), () => varint(pick(20)),
    () => Buffer.from(P), () => varint(pick(300)), () => Buffer.from([0xff, 0xff, 0xff, 0xff, 0x0f]), () => randomBytes(1 + pick(4))];
for (let i = 0; i < 2500; i++) {
    const parts = [];
    for (let k = pick(10); k >= 0; k--) parts.push(pieces[pick(pieces.length)]());
    add(Buffer.concat(parts));
}

const vectors = inputs.map((buf) => {
    const out = { hex: buf.toString('hex') };
    try {
        RV.decode(buf);
    } catch (err) {
        out.error = err.message;
    }
    const m = decodeRendezvousPayload(buf);
    out.ok = m !== null;
    if (m) {
        out.punch = m.punchHoleRequest ? { id: String(m.punchHoleRequest.id || '') } : null;
        out.relay = m.requestRelay ? { id: String(m.requestRelay.id || '') } : null;
        out.matches = guestTargetMatches(m, P);
    }
    return out;
});

// ---- The schema ----

const schema = {};
(function walk(type) {
    if (schema[type.fullName]) return;
    schema[type.fullName] = type.fieldsArray.map((f) => {
        f.resolve();
        return { id: f.id, name: f.name, type: f.resolvedType ? (f.resolvedType instanceof protobuf.Enum ? 'enum' : f.resolvedType.fullName) : f.type,
            repeated: f.repeated, map: !!f.map };
    });
    for (const f of type.fieldsArray) if (f.resolvedType instanceof protobuf.Type) walk(f.resolvedType);
}(RV));

fs.writeFileSync(path.join(__dirname, '..', 'node_codec.json'),
    `${JSON.stringify({ peer: P, max: MAX_RELAY_FRAME_SIZE, encode, decode, vectors, schema })}\n`);
console.log(`encode ${encode.length}, decoder streams ${decode.length}, rendezvous vectors ${vectors.length}, types ${Object.keys(schema).length}`);
