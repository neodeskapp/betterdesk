'use strict';

/**
 * Records what the Node console's LAN discovery responder
 * (services/lanDiscovery.js) answers: the announcement for many settings,
 * host names, interface lists and public key files, which of thousands of
 * probes it answers at all, one real exchange over UDP, and what it does
 * with a DISCOVERY_PORT that is no port. Writes ../lan.json.gz for the Go
 * tests. See README.md.
 *
 *   node lan.js
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const dgram = require('dgram');
const { spawnSync } = require('child_process');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'lan-oracle-'));

// The host name and interfaces the module sees
let fakeHost = 'oracle-host';
let fakeInterfaces = {};
os.hostname = () => fakeHost;
os.networkInterfaces = () => fakeInterfaces;

// A seeded generator, so a second run writes the same file
let seed = 0x5eed1234;
function rand() {
    seed ^= seed << 13; seed >>>= 0;
    seed ^= seed >>> 17;
    seed ^= seed << 5; seed >>>= 0;
    return seed / 0x100000000;
}
const pick = (list) => list[Math.floor(rand() * list.length)];

const envKeys = ['HOST', 'PORT', 'HTTPS_ENABLED', 'API_PORT', 'PANEL_PUBLIC_HOST', 'PANEL_PUBLIC_URL', 'DISCOVERY_PORT', 'PUB_KEY_PATH', 'KEYS_PATH'];

function load(env) {
    for (const key of envKeys) delete process.env[key];
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.KEYS_PATH = scratch;
    Object.assign(process.env, env);
    for (const id of Object.keys(require.cache)) {
        if (id.startsWith(path.join(WEB, 'config')) || id.startsWith(path.join(WEB, 'services', 'lanDiscovery'))) delete require.cache[id];
    }
    const config = require(path.join(WEB, 'config', 'config'));
    const lan = require(path.join(WEB, 'services', 'lanDiscovery'));
    return { config, lan };
}

// Builds an interface object the way os.networkInterfaces does: the
// names in the order the system lists them
function interfaces(list) {
    const out = {};
    for (const [name, entries] of list) {
        if (out[name] === undefined) out[name] = [];
        out[name].push(...entries);
    }
    return out;
}
const v4 = (address, internal = false, mac = '02:42:ac:11:00:02') => ({ address, netmask: '255.255.0.0', family: 'IPv4', mac, internal, cidr: `${address}/16` });
const v6 = (address, internal = false, mac = '02:42:ac:11:00:02') => ({ address, netmask: 'ffff:ffff:ffff:ffff::', family: 'IPv6', mac, internal, cidr: `${address}/64`, scopeid: 0 });
const interfaceSets = [
    [],
    [['lo', [v4('127.0.0.1', true), v6('::1', true)]], ['eth0', [v4('172.17.0.2'), v6('fe80::42:acff:fe11:2')]]],
    [['eth0', [v4('10.0.0.5'), v4('10.0.0.6')]], ['wlan0', [v6('fe80::1')]], ['docker0', [v4('172.17.0.1')]]],
    [['eth1', [v4('192.168.1.2')]], ['10', [v4('10.1.1.1')]], ['2', [v4('10.2.2.2')]], ['eth0', [v4('192.168.0.2')]], ['01', [v4('10.0.1.1')]], ['4294967295', [v4('10.4.4.4')]], ['4294967294', [v4('10.3.3.3')]]],
    [['tun0', [v4('10.8.0.1', false, '00:00:00:00:00:00')]], ['lo', [v4('127.0.0.1', true)]], ['ens3', [v6('2001:db8::1'), v4('203.0.113.9')]]],
];

const hostNames = ['oracle-host', 'server.example.com', 'Ünïcode-hôst', 'with "quotes" and \\ backslash', 'tab\there', 'line sep', '<script>&amp;', 'emoji-\u{1F600}', 'nul\u0000byte', 'ctl\u0001\u001f\u007f'];

const keyFiles = [
    null, // no file
    'dir', // a folder where the file should be
    Buffer.from('w0oqWkG8kU4mnqYV2Qoay2CIHb4TbvCN0dVDYNAsgYg='),
    Buffer.from('  key-with-space\n\n'),
    Buffer.from('﻿key-with-bom\r\n'),
    Buffer.from(' 　key '),
    Buffer.from([0x6b, 0x65, 0x79, 0xff, 0xfe, 0x20, 0xe2, 0x82, 0x41, 0xed, 0xa0, 0x80, 0xf0, 0x9f, 0x98, 0x0a]),
    Buffer.from('"quoted"\\\u0007'),
    Buffer.from(''),
];

const settings = [
    {},
    { HOST: '0.0.0.0', PORT: '5000' },
    { HOST: '192.168.1.10', PORT: '80' },
    { HOST: 'panel.local', PORT: '443', HTTPS_ENABLED: 'true' },
    { HOST: 'panel.local', PORT: '80', HTTPS_ENABLED: 'TRUE' },
    { HOST: 'panel.local', PORT: '5443', HTTPS_ENABLED: 'yes' },
    { PANEL_PUBLIC_HOST: 'public.example.com' },
    { PANEL_PUBLIC_HOST: 'public.example.com', PORT: '8080', HTTPS_ENABLED: 'true' },
    { PANEL_PUBLIC_URL: 'https://panel.example.com/' },
    { PANEL_PUBLIC_URL: 'not a url', PANEL_PUBLIC_HOST: 'ignored' },
    { API_PORT: '21122', DISCOVERY_PORT: '31119' },
    { API_PORT: 'abc', DISCOVERY_PORT: 'abc' },
    { DISCOVERY_PORT: '0' },
    { HOST: '::1', PORT: '5000' },
    { HOST: ' spaced ', PORT: '5000' },
    { PANEL_PUBLIC_HOST: 'hüst', PANEL_PUBLIC_URL: '' },
    { HOST: 'h"q', PANEL_PUBLIC_URL: 'http://x/ y' },
];

const announcements = [];
let caseNo = 0;
for (const env of settings) {
    for (let k = 0; k < keyFiles.length; k++) {
        const file = path.join(scratch, `key-${caseNo++}.pub`);
        const key = keyFiles[k];
        if (key === 'dir') fs.mkdirSync(file);
        else if (key !== null) fs.writeFileSync(file, key);
        const iset = interfaceSets[(caseNo + k) % interfaceSets.length];
        fakeInterfaces = interfaces(iset);
        fakeHost = hostNames[(caseNo * 7 + k) % hostNames.length];
        const { config, lan } = load({ ...env, PUB_KEY_PATH: file });
        announcements.push({
            env,
            key: key === null ? null : key === 'dir' ? 'dir' : key.toString('base64'),
            hostname: fakeHost,
            interfaces: iset.map(([name, entries]) => ({ name, entries: entries.map((e) => ({ family: e.family, address: e.address, internal: e.internal, mac: e.mac })) })),
            config: { port: config.port, apiPort: config.apiPort, httpsEnabled: config.httpsEnabled, host: config.host, appVersion: config.appVersion, discoveryPort: lan.DISCOVERY_PORT },
            reply: Buffer.from(JSON.stringify(lan.buildAnnouncement()), 'utf8').toString('base64'),
        });
    }
}

// Object key order of interface names: what Object.keys gives for names
// inserted in this order
const keyOrders = [];
const namePool = ['eth0', '0', '1', '10', '2', '01', '-1', '1.5', '4294967294', '4294967295', '9007199254740993', 'lo', '00', ' 1', '1 ', 'Infinity', 'NaN', '١'];
for (let i = 0; i < 300; i++) {
    const names = [];
    const n = 1 + Math.floor(rand() * 8);
    for (let j = 0; j < n; j++) names.push(pick(namePool));
    const obj = {};
    for (const name of names) if (obj[name] === undefined) obj[name] = true;
    keyOrders.push({ names, keys: Object.keys(obj) });
}

// Probes: which ones get an answer at all
const valid = '{"type":"betterdesk-discover","version":1}';
const probes = [];
const add = (buf) => probes.push(Buffer.isBuffer(buf) ? buf : Buffer.from(buf, 'utf8'));
const fixed = [
    valid, '{"type":"betterdesk-discover"}', ' \t\r\n{"type" : "betterdesk-discover" } \n', '{"version":1,"type":"betterdesk-discover","x":[1,{"y":null}]}',
    '{"type":"betterdesk-discover","type":"other"}', '{"type":"other","type":"betterdesk-discover"}', '{"Type":"betterdesk-discover"}',
    '{"type":"Betterdesk-discover"}', '{"type":"betterdesk-discover "}', '{"type":"betterdesk-announce","version":1}',
    '{"\\u0074ype":"betterdesk\\u002ddiscover"}', '{"type":"betterdesk-discover\\u0000"}', '{"type":"\\ud800"}', '{"type":"betterdesk-discover","x":"\\ud83d\\ude00"}',
    '{"type":["betterdesk-discover"]}', '{"type":null}', '{"type":1}', '{"type":true}', '{"__proto__":{"type":"betterdesk-discover"}}',
    '{"x":{"type":"betterdesk-discover"}}', '[{"type":"betterdesk-discover"}]', '"betterdesk-discover"', 'null', '1', 'true', '', ' ', '{}', '[]',
    '{"type":"betterdesk-discover",}', "{'type':'betterdesk-discover'}", '{"type":"betterdesk-discover"} x', '{"type":"betterdesk-discover"}{}',
    '{"type":"betterdesk-discover"}\u0000', '﻿' + valid, valid + '﻿', '{"type":"betterdesk-discover","n":01}', '{"type":"betterdesk-discover","n":-0}',
    '{"type":"betterdesk-discover","n":.5}', '{"type":"betterdesk-discover","n":1.}', '{"type":"betterdesk-discover","n":1e}', '{"type":"betterdesk-discover","n":1e+400}',
    '{"type":"betterdesk-discover","n":-1.5E-3}', '{"type":"betterdesk-discover","n":+1}', '{"type":"betterdesk-discover","n":NaN}', '{"type":"betterdesk-discover","n":Infinity}',
    '{"type":"betterdesk-discover","s":"a\tb"}', '{"type":"betterdesk-discover","s":"a\u007fb"}', '{"type":"betterdesk-discover","s":"\\x41"}',
    '{"type":"betterdesk-discover","s":"\\u12"}', '{"type":"betterdesk-discover","s":"\\/\\b\\f\\n\\r\\t\\"\\\\"}', '{"type":"betterdesk-discover","s":"\\U0041"}',
    '{"type":"betterdesk-discover" /* c */}', '{"type":"betterdesk-discover", "v":1}', '{"type":"betterdesk-discover","v":tru}',
    '{"type":"betterdesk-discover","v":nulll}', '{"type":"betterdesk-discover","v":[1,]}', '{"type":"betterdesk-discover","v":[,1]}',
    '{"type":"betterdesk-discover","v":{"a"}}', '{"type":"betterdesk-discover","v":{"a":}}', '{"type":"betterdesk-discover","v":{1:2}}',
    '{"type":"betterdesk-discover"', '{"type":"betterdesk-discover', '{"type":', '{', '}', '{"type" "betterdesk-discover"}',
    '{"type":"betterdesk-discover"," ":1}', '{"type":"betterdesk-discover","k":"é中\u{1F600}"}',
];
fixed.forEach(add);
// Bytes that are no UTF-8, inside and outside strings
const bytes = (...parts) => Buffer.concat(parts.map((p) => (typeof p === 'string' ? Buffer.from(p, 'utf8') : Buffer.from(p))));
add(bytes('{"type":"betterdesk-discover","s":"', [0xff, 0xfe, 0xe2, 0x82, 0xed, 0xa0, 0x80, 0xc0, 0xaf, 0xf4, 0x90, 0x80, 0x80], '"}'));
add(bytes('{"type":"betterdesk-discover"', [0xff], '}'));
add(bytes('{"type":"betterdesk-discover', [0xe2, 0x82], '"}'));
add(bytes('{"type":"betterdesk-discover"', [0xc2, 0xa0], '}'));
add(bytes([0xef, 0xbb, 0xbf], valid));
// Deep nesting, inside the probe
for (const d of [10, 100, 1000, 5000, 9999, 10000, 10001, 15000, 20000, 32000]) {
    add(`{"type":"betterdesk-discover","x":${'['.repeat(d)}${']'.repeat(d)}}`);
    add(`{"type":"betterdesk-discover","x":${'{"a":'.repeat(Math.min(d, 13000))}1${'}'.repeat(Math.min(d, 13000))}}`);
}
add(`${'['.repeat(30000)}${']'.repeat(30000)}`);
add(`{"x":${'['.repeat(30000)}${']'.repeat(29999)},"type":"betterdesk-discover"}`);
// Long values
add(`{"type":"betterdesk-discover","pad":"${'x'.repeat(60000)}"}`);
add(`{"pad":"${'\\u0041'.repeat(10000)}","type":"betterdesk-discover"}`);
// Random changes to a valid probe
const base = Buffer.from('{"type":"betterdesk-discover","version":1,"x":[1,2.5e3,"s\\n",true,null,{}]}');
const alphabet = Buffer.from('{}[]":,\\ \t\n0123456789.eE+-truefalsenuléx\u0000\u001f\u007f');
for (let i = 0; i < 4000; i++) {
    const b = Buffer.from(base);
    const out = [];
    const edits = 1 + Math.floor(rand() * 3);
    let arr = Array.from(b);
    for (let e = 0; e < edits; e++) {
        const at = Math.floor(rand() * (arr.length + 1));
        const kind = rand();
        if (kind < 0.33 && arr.length) arr.splice(Math.min(at, arr.length - 1), 1);
        else if (kind < 0.66) arr.splice(at, 0, rand() < 0.2 ? Math.floor(rand() * 256) : alphabet[Math.floor(rand() * alphabet.length)]);
        else if (arr.length) arr[Math.min(at, arr.length - 1)] = rand() < 0.2 ? Math.floor(rand() * 256) : alphabet[Math.floor(rand() * alphabet.length)];
    }
    out.push(...arr);
    add(Buffer.from(out));
}
// Random bytes and random JSON-ish text
for (let i = 0; i < 500; i++) {
    const n = Math.floor(rand() * 64);
    const b = Buffer.alloc(n);
    for (let j = 0; j < n; j++) b[j] = Math.floor(rand() * 256);
    add(b);
}
for (let i = 0; i < 1500; i++) {
    const n = Math.floor(rand() * 40);
    const b = Buffer.alloc(n);
    for (let j = 0; j < n; j++) b[j] = alphabet[Math.floor(rand() * alphabet.length)];
    add(Buffer.concat([Buffer.from('{"type":"betterdesk-discover","v":'), b, Buffer.from('}')]));
}

// Drive the module's message handler, with send recorded instead of sent
fakeInterfaces = interfaces(interfaceSets[1]);
fakeHost = 'oracle-host';
const { lan } = load({ DISCOVERY_PORT: '41119', PUB_KEY_PATH: path.join(scratch, 'none') });
const { server } = lan.startDiscoveryService();
const handler = server.listeners('message')[0];
const sent = [];
const realSend = server.send.bind(server);
server.send = (buf, off, len, port, address) => { sent.push({ buf, port, address }); };
const answered = [];
for (const probe of probes) {
    const before = sent.length;
    handler(probe, { address: '198.51.100.7', port: 40000 });
    answered.push(sent.length > before);
    if (sent.length > before) {
        const s = sent[sent.length - 1];
        if (s.port !== 40000 || s.address !== '198.51.100.7') throw new Error('reply went elsewhere');
    }
}
const expectedReply = Buffer.from(JSON.stringify(lan.buildAnnouncement()), 'utf8');
for (const s of sent) if (!s.buf.equals(expectedReply)) throw new Error('a reply differs from the announcement');
server.send = realSend;

// One real exchange over UDP
function exchange() {
    return new Promise((resolve, reject) => {
        const client = dgram.createSocket('udp4');
        const timer = setTimeout(() => { client.close(); reject(new Error('no reply')); }, 5000);
        client.on('message', (msg, rinfo) => { clearTimeout(timer); client.close(); resolve({ msg, rinfo }); });
        client.send(Buffer.from(valid), 41119, '127.0.0.1');
    });
}

// What a DISCOVERY_PORT that is no port does to the start
function badPort(value) {
    const script = `
        const os = require('os'); os.networkInterfaces = () => ({});
        process.env.DISCOVERY_PORT = ${JSON.stringify(value)};
        process.env.DATA_DIR = ${JSON.stringify(scratch)};
        process.chdir(${JSON.stringify(WEB)});
        const lan = require('./services/lanDiscovery');
        let sync = null;
        let handle = null;
        try { handle = lan.startDiscoveryService(); } catch (err) { sync = err.code + ': ' + err.message; }
        process.on('uncaughtException', (err) => { console.log(JSON.stringify({ sync, uncaught: err.code + ': ' + err.message })); process.exit(0); });
        setTimeout(() => {
            let bound = null;
            try { bound = handle.server.address().port; } catch (err) { bound = err.code || err.message; }
            console.log(JSON.stringify({ sync, port: lan.DISCOVERY_PORT, bound }));
            process.exit(0);
        }, 300);
    `;
    const run = spawnSync(process.execPath, ['-e', script], { encoding: 'utf8', cwd: WEB });
    const lines = run.stdout.trimEnd().split('\n');
    const result = JSON.parse(lines.pop());
    const logs = [...lines, ...run.stderr.trimEnd().split('\n')].filter((l) => l && !l.startsWith('[DB]'));
    return { value, ...result, logs };
}

(async () => {
    const { msg, rinfo } = await exchange();
    lan.stopDiscoveryService();
    const ports = ['70000', '65535', '65536', '-5', '1e9', '99999999999999999999', '1.5', '0x50'].map(badPort);
    const out = {
        announcements,
        keyOrders,
        probes: probes.map((p, i) => ({ probe: p.toString('base64'), answered: answered[i] })),
        exchange: { reply: msg.toString('base64'), from: rinfo.port, expected: expectedReply.toString('base64') },
        ports,
    };
    const file = path.join(__dirname, '..', 'lan.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(out)));
    const yes = answered.filter(Boolean).length;
    console.log(`${file}: ${announcements.length} announcements, ${keyOrders.length} key orders, ${probes.length} probes (${yes} answered)`);
    console.log(JSON.stringify(ports));
})().catch((err) => { console.error(err); process.exit(1); });
