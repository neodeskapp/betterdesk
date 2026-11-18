'use strict';

/**
 * Records what the Node console's mDNS announcement
 * (services/panelDiscovery.js with the optional package bonjour-service)
 * sends: the probe queries, the announcements and their schedule, the
 * goodbye after a name conflict, and the answer to each of thousands of
 * datagrams that arrive, together with how dns-packet decodes each of
 * them. The socket and the timers are replaced, so nothing goes on the
 * network and the schedule runs on a virtual clock. Writes
 * ../mdns.json.gz for the Go tests. See README.md.
 *
 *   BONJOUR_MODULES=/npmb/node_modules node mdns.js
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const dgram = require('dgram');
const Module = require('module');
const { EventEmitter } = require('events');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const MODULES = process.env.BONJOUR_MODULES || '/npmb/node_modules';
process.env.NODE_PATH = MODULES;
Module._initPaths();
const dnsPacket = require(path.join(MODULES, 'dns-packet'));
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'mdns-oracle-'));

// ---- The host the module sees ----
let fakeHost = 'oracle-host';
let fakeInterfaces = {};
os.hostname = () => fakeHost;
os.networkInterfaces = () => fakeInterfaces;
let randomValue = 0.5;
Math.random = () => randomValue;

// ---- A virtual clock for every timer ----
let now = 0;
let seq = 0;
let timers = [];
const realSetImmediate = setImmediate;
function addTimer(fn, delay, repeat, args) {
    let ms = Number(delay);
    if (!(ms >= 1 && ms <= 2147483647)) ms = 1;
    const t = { id: ++seq, due: now + ms, ms, fn, repeat, args, active: true, unref() { return this; }, ref() { return this; }, hasRef() { return false; } };
    timers.push(t);
    return t;
}
global.setTimeout = (fn, delay, ...args) => addTimer(fn, delay, false, args);
global.setInterval = (fn, delay, ...args) => addTimer(fn, delay, true, args);
global.clearTimeout = global.clearInterval = (t) => { if (t) t.active = false; };
const flush = () => new Promise((resolve) => realSetImmediate(resolve));
async function settle() { for (let i = 0; i < 5; i++) await flush(); }
// Runs every timer due up to `until`, in order
async function advance(until) {
    await settle();
    for (;;) {
        timers = timers.filter((t) => t.active);
        const next = timers.filter((t) => t.due <= until).sort((a, b) => a.due - b.due || a.id - b.id)[0];
        if (!next) break;
        now = next.due;
        if (next.repeat) next.due = now + next.ms; else next.active = false;
        next.fn(...next.args);
        await settle();
    }
    now = until;
}

// ---- A socket that records instead of sending ----
let events = [];
let socket = null;
class FakeSocket extends EventEmitter {
    bind(port, address, cb) {
        events.push({ t: now, op: 'bind', port, address: address === undefined ? null : address });
        process.nextTick(() => { this.emit('listening'); if (cb) cb(); });
    }
    address() { return { address: '0.0.0.0', port: 5353, family: 'IPv4' }; }
    send(msg, offset, length, port, address, cb) {
        events.push({ t: now, op: 'send', port, address, bytes: Buffer.from(msg.subarray(offset, offset + length)).toString('base64') });
        if (cb) process.nextTick(cb, null);
    }
    addMembership(group, iface) { events.push({ t: now, op: 'addMembership', group, iface }); }
    dropMembership() {}
    setMulticastTTL(ttl) { events.push({ t: now, op: 'setMulticastTTL', ttl }); }
    setMulticastLoopback(on) { events.push({ t: now, op: 'setMulticastLoopback', on }); }
    setMulticastInterface(addr) { events.push({ t: now, op: 'setMulticastInterface', addr }); }
    close(cb) { if (cb) process.nextTick(cb); }
}
dgram.createSocket = () => { socket = new FakeSocket(); return socket; };

// ---- Console output ----
let logs = [];
console.log = (...args) => logs.push(args.map((a) => (a instanceof Error ? `${a.name}: ${a.message}` : String(a))).join(' '));
console.warn = console.log;
console.error = console.log;

// ---- Interfaces as os.networkInterfaces gives them ----
const v4 = (address, internal = false, mac = '02:42:ac:11:00:02') => ({ address, netmask: '255.255.0.0', family: 'IPv4', mac, internal, cidr: `${address}/16` });
const v6 = (address, internal = false, mac = '02:42:ac:11:00:02') => ({ address, netmask: 'ffff:ffff:ffff:ffff::', family: 'IPv6', mac, internal, cidr: `${address}/64`, scopeid: 0 });
const interfaceSets = [
    [['lo', [v4('127.0.0.1', true, '00:00:00:00:00:00'), v6('::1', true, '00:00:00:00:00:00')]], ['eth0', [v4('172.17.0.2'), v6('fe80::42:acff:fe11:2')]]],
    [],
    [['lo', [v4('127.0.0.1', true, '00:00:00:00:00:00')]]],
    [['eth0', [v6('2001:db8::5'), v4('10.0.0.5'), v4('10.0.0.6')]], ['tun0', [v4('10.8.0.1', false, '00:00:00:00:00:00')]], ['2', [v4('10.2.2.2'), v6('fd00::2')]], ['wlan0', [v6('fe80::1')]]],
    [['eth0', [v4('10.0.0.5'), v4('10.0.0.5')]], ['eth1', [v4('10.0.0.5')]]],
];
function interfaces(list) {
    const out = {};
    for (const [name, entries] of list) {
        if (out[name] === undefined) out[name] = [];
        out[name].push(...entries);
    }
    return out;
}
const describe = (list) => list.map(([name, entries]) => ({ name, entries: entries.map((e) => ({ family: e.family, address: e.address, internal: e.internal, mac: e.mac })) }));

const envKeys = ['HOST', 'PORT', 'HTTPS_ENABLED', 'API_PORT', 'PANEL_PUBLIC_HOST', 'PANEL_PUBLIC_URL', 'DISCOVERY_PORT', 'PUB_KEY_PATH', 'KEYS_PATH', 'PANEL_MDNS'];
function load(env) {
    for (const key of envKeys) delete process.env[key];
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.KEYS_PATH = scratch;
    process.env.PUB_KEY_PATH = path.join(scratch, 'none');
    Object.assign(process.env, env);
    for (const id of Object.keys(require.cache)) {
        if (id.startsWith(WEB) || id.startsWith(MODULES)) delete require.cache[id];
    }
    const config = require(path.join(WEB, 'config', 'config'));
    const panel = require(path.join(WEB, 'services', 'panelDiscovery'));
    return { config, panel };
}

// Starts the module as server.js does, with its try and catch
function start(env, ifaceSet, host, random) {
    events = [];
    logs = [];
    timers = [];
    now = 0;
    socket = null;
    fakeHost = host;
    fakeInterfaces = interfaces(ifaceSet);
    randomValue = random;
    const { config, panel } = load(env);
    try {
        panel.startPanelMdns();
    } catch (err) {
        console.warn('[server] mDNS panel discovery disabled:', err.message);
    }
    return { config, panel };
}

const HOUR = 60 * 60 * 1000;
const scenarios = [];
const settingsList = [
    { env: {}, set: 0, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'https://panel.example.com/' }, set: 3, host: 'server.example.com', random: 0 },
    { env: { PANEL_PUBLIC_URL: 'https://panel.example.com:8443/x/' }, set: 0, host: 'Mixed-Case', random: 0.999 },
    { env: { PANEL_PUBLIC_HOST: 'public.example.com', PORT: '80' }, set: 4, host: 'h', random: 0.123 },
    { env: { HOST: '0.0.0.0', PORT: '443', HTTPS_ENABLED: 'true' }, set: 1, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'ws://ws.example:80' }, set: 2, host: 'with space', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'ftp://f.example' }, set: 0, host: 'oracle-host.local', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'http://[::1]:5000' }, set: 0, host: 'hüst', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'http://h:0' }, set: 0, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'not a url' }, set: 0, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_MDNS: 'off' }, set: 0, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_MDNS: '0' }, set: 0, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_MDNS: 'OFF' }, set: 0, host: 'oracle-host', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'http://a.example:65535/' + 'x'.repeat(300) }, set: 0, host: 'a.b.c.d', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'http://dots.example', }, set: 0, host: '.lead.trail.', random: 0.5 },
    { env: { PANEL_PUBLIC_URL: 'http://x.example:1' }, set: 0, host: 'x'.repeat(70), random: 0.5 },
];

// A datagram that claims the service's name, sent while probing
function conflictPacket(fqdn) {
    return dnsPacket.encode({ type: 'response', answers: [{ name: fqdn.toUpperCase(), type: 'SRV', ttl: 120, data: { port: 1, target: 'other' } }] });
}

// ---- Datagrams that arrive after the announcement ----
let rseed = 0x13579bdf;
function rand() {
    rseed ^= rseed << 13; rseed >>>= 0;
    rseed ^= rseed >>> 17;
    rseed ^= rseed << 5; rseed >>>= 0;
    return rseed / 0x100000000;
}
const pick = (list) => list[Math.floor(rand() * list.length)];
function datagrams(host) {
    const fqdn = 'BetterDesk Console._betterdesk._tcp.local';
    const names = [fqdn, fqdn.toLowerCase(), fqdn.toUpperCase(), '_betterdesk._tcp.local', '_BETTERDESK._TCP.LOCAL', '_services._dns-sd._udp.local',
        host, host.toUpperCase(), `${host}.local`, 'BetterDesk Console', 'betterdesk console', '_betterdesk', 'other.local', '.', '', 'a..b',
        'BetterDesk Console._betterdesk._tcp.local.', 'x.' + fqdn, 'été.local', 'BetterDesk Console._betterdesk._tcp.local'];
    const qtypes = ['ANY', 'PTR', 'SRV', 'TXT', 'A', 'AAAA', 'CNAME', 'NSEC', 'UNKNOWN_65000', 'OPT', 'MX'];
    const out = [];
    for (const name of names) {
        for (const type of qtypes) {
            out.push(dnsPacket.encode({ type: 'query', questions: [{ name, type }] }));
        }
    }
    out.push(dnsPacket.encode({ type: 'query', questions: [{ name: '_betterdesk._tcp.local', type: 'PTR' }, { name: fqdn, type: 'ANY' }, { name: host, type: 'A' }] }));
    out.push(dnsPacket.encode({ type: 'response', answers: [{ name: '_betterdesk._tcp.local', type: 'PTR', data: fqdn }] }));
    out.push(dnsPacket.encode({ type: 'query', questions: [{ name: '_betterdesk._tcp.local', type: 'PTR', class: 'IN' }], answers: [{ name: '_betterdesk._tcp.local', type: 'PTR', ttl: 4500, data: fqdn }] }));
    // Records of every kind dns-packet knows, as known answers
    const rr = () => pick([
        { name: pick(names), type: 'A', ttl: 120, data: '10.1.2.3' },
        { name: pick(names), type: 'AAAA', ttl: 120, data: 'fe80::1' },
        { name: pick(names), type: 'PTR', ttl: 1, data: pick(names) },
        { name: pick(names), type: 'CNAME', data: 'c.example' },
        { name: pick(names), type: 'DNAME', data: 'd.example' },
        { name: pick(names), type: 'NS', data: 'ns.example' },
        { name: pick(names), type: 'TXT', data: ['a=1', Buffer.from([0, 255])] },
        { name: pick(names), type: 'NULL', data: Buffer.from('nul') },
        { name: pick(names), type: 'SRV', flush: true, data: { port: 9, target: 't.example', priority: 1, weight: 2 } },
        { name: pick(names), type: 'HINFO', data: { cpu: 'x86', os: 'linux' } },
        { name: pick(names), type: 'CAA', data: { flags: 128, tag: 'issue', value: 'ca.example' } },
        { name: pick(names), type: 'SOA', data: { mname: 'm.example', rname: 'r.example', serial: 1 } },
        { name: pick(names), type: 'MX', data: { preference: 5, exchange: 'mx.example' } },
        { name: '.', type: 'OPT', udpPayloadSize: 1440, options: [{ code: 8, family: 1, sourcePrefixLength: 24, ip: '1.2.3.0' }, { code: 11, timeout: 5 }, { code: 14, tags: [1, 2] }, { code: 12, length: 3 }] },
        { name: pick(names), type: 'DNSKEY', data: { flags: 257, algorithm: 8, key: Buffer.from('key') } },
        { name: pick(names), type: 'RRSIG', data: { typeCovered: 'A', algorithm: 8, labels: 2, originalTTL: 1, expiration: 2, inception: 3, keyTag: 4, signersName: 's.example', signature: Buffer.from('sig') } },
        { name: pick(names), type: 'RP', data: { mbox: 'm.example', txt: 't.example' } },
        { name: pick(names), type: 'NSEC', data: { nextDomain: 'n.example', rrtypes: ['A', 'TXT', 'RRSIG'] } },
        { name: pick(names), type: 'NSEC3', data: { algorithm: 1, flags: 0, iterations: 2, salt: Buffer.from('s'), nextDomain: Buffer.from('nd'), rrtypes: ['A', 'AAAA'] } },
        { name: pick(names), type: 'SSHFP', data: { algorithm: 1, hash: 1, fingerprint: 'A'.repeat(40) } },
        { name: pick(names), type: 'DS', data: { keyTag: 1, algorithm: 8, digestType: 2, digest: Buffer.from('dig') } },
        { name: pick(names), type: 'NAPTR', data: { order: 1, preference: 2, flags: 'S', services: 'SIP', regexp: '', replacement: 'r.example' } },
        { name: pick(names), type: 'TLSA', data: { usage: 3, selector: 1, matchingType: 1, certificate: Buffer.from('cert') } },
        { name: pick(names), type: 'UNKNOWN_4000', data: Buffer.from('raw') },
    ]);
    const valid = [];
    for (let i = 0; i < 700; i++) {
        const questions = [];
        const nq = Math.floor(rand() * 3);
        for (let j = 0; j < nq; j++) questions.push({ name: pick(names), type: pick(qtypes), class: pick(['IN', 'ANY', 'UNKNOWN_32769']) });
        const list = () => { const n = Math.floor(rand() * 3); const l = []; for (let j = 0; j < n; j++) l.push(rr()); return l; };
        let buf;
        try {
            buf = dnsPacket.encode({ type: rand() < 0.8 ? 'query' : 'response', id: Math.floor(rand() * 65536), flags: Math.floor(rand() * 32768), questions, answers: list(), authorities: list(), additionals: list() });
        } catch (_) { continue; }
        valid.push(buf);
        out.push(buf);
    }
    // Changed bytes, cut packets, wrong counts, pointers
    for (let i = 0; i < 4000; i++) {
        const base = Buffer.from(pick(valid.length ? valid : out));
        const b = Array.from(base);
        const kind = rand();
        if (kind < 0.25) {
            for (let k = 0; k < 1 + Math.floor(rand() * 3); k++) b[Math.floor(rand() * b.length)] = Math.floor(rand() * 256);
        } else if (kind < 0.45) {
            b.length = Math.floor(rand() * b.length);
        } else if (kind < 0.6) {
            if (b.length >= 12) { const at = 4 + 2 * Math.floor(rand() * 4); b[at] = 0; b[at + 1] = Math.floor(rand() * 6); }
        } else if (kind < 0.75) {
            const at = 12 + Math.floor(rand() * Math.max(1, b.length - 12));
            b.splice(at, 0, 0xc0, Math.floor(rand() * Math.max(1, at)));
        } else if (kind < 0.9) {
            // A length field (rdlength or label) set to something else
            const at = 12 + Math.floor(rand() * Math.max(1, b.length - 13));
            b[at] = Math.floor(rand() * 256);
            if (rand() < 0.5) b[at + 1] = Math.floor(rand() * 256);
        } else {
            b.splice(Math.floor(rand() * b.length), Math.floor(rand() * 8));
        }
        out.push(Buffer.from(b.filter((x) => x !== undefined)));
    }
    for (let i = 0; i < 400; i++) {
        const n = Math.floor(rand() * 48);
        const b = Buffer.alloc(n);
        for (let j = 0; j < n; j++) b[j] = Math.floor(rand() * 256);
        if (n >= 4 && rand() < 0.7) { b[2] = 0; b[3] = 0; }
        out.push(b);
    }
    return out;
}

const configOf = (config) => ({ port: config.port, apiPort: config.apiPort, httpsEnabled: config.httpsEnabled, host: config.host, appVersion: config.appVersion });

function decoded(buf) {
    try {
        const p = dnsPacket.decode(buf);
        return {
            ok: true,
            type: p.type,
            questions: p.questions.map((q) => ({ name: q.name, type: q.type })),
            answers: p.answers.map((a) => a.name),
            additionals: p.additionals.map((a) => a.name),
        };
    } catch (err) {
        return { ok: false };
    }
}

(async () => {
    for (const s of settingsList) {
        const { config } = start(s.env, interfaceSets[s.set], s.host, s.random);
        const startLogs = [...logs];
        await advance(2 * HOUR);
        const scenario = { env: s.env, config: configOf(config), interfaces: describe(interfaceSets[s.set]), hostname: s.host, random: s.random, logs: startLogs, laterLogs: logs.slice(startLogs.length), events };
        scenarios.push(scenario);
    }

    // A name conflict while probing
    start({}, interfaceSets[0], 'oracle-host', 0.5);
    await advance(125);
    const conflictAt = events.filter((e) => e.op === 'send').length;
    socket.emit('message', conflictPacket('BetterDesk Console._betterdesk._tcp.local'), { address: '10.9.9.9', port: 5353 });
    await settle();
    await advance(2 * HOUR);
    const conflict = { sentBefore: conflictAt, events, logs, packet: conflictPacket('BetterDesk Console._betterdesk._tcp.local').toString('base64') };

    // A conflict that comes before the first query went out is ignored
    start({}, interfaceSets[0], 'oracle-host', 0.5);
    socket.emit('message', conflictPacket('BetterDesk Console._betterdesk._tcp.local'), { address: '10.9.9.9', port: 5353 });
    await advance(2 * HOUR);
    const early = { events, logs };

    // Answers to datagrams once announced
    const answers = [];
    for (const [set, host] of [[0, 'oracle-host'], [3, 'Server.Example.COM']]) {
        const { config } = start({ PANEL_PUBLIC_URL: 'http://panel.example:5000/' }, interfaceSets[set], host, 0.5);
        await advance(2 * HOUR);
        const list = [];
        for (const buf of datagrams(host)) {
            events = [];
            socket.emit('message', buf, { address: '10.9.9.9', port: 5353 });
            await settle();
            list.push({ packet: buf.toString('base64'), decoded: decoded(buf), sent: events.filter((e) => e.op === 'send').map((e) => e.bytes) });
        }
        answers.push({ config: configOf(config), interfaces: describe(interfaceSets[set]), hostname: host, packets: list });
    }

    const out = { scenarios, conflict, early, answers };
    const file = path.join(__dirname, '..', 'mdns.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(out)));
    const n = answers.reduce((a, b) => a + b.packets.length, 0);
    const answered = answers.reduce((a, b) => a + b.packets.filter((p) => p.sent.length).length, 0);
    const okCount = answers.reduce((a, b) => a + b.packets.filter((p) => p.decoded.ok).length, 0);
    process.stdout.write(`${file}: ${scenarios.length} scenarios, ${n} datagrams (${okCount} decoded, ${answered} answered)\n`);
    process.exit(0);
})().catch((err) => { process.stderr.write(`${err.stack}\n`); process.exit(1); });
