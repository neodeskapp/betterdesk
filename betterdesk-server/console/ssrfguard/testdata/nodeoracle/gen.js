'use strict';

// Records what web-nodejs/lib/ssrfGuard.js answers for a few thousand
// hosts, addresses and URLs, and what Node's URL parser makes of the
// URLs. Writes ../vectors.jsonl.gz. See README.md.

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const dns = require('dns');

const repo = path.resolve(__dirname, '../../../../..');
const guard = require(path.join(repo, 'web-nodejs/lib/ssrfGuard'));

// One lookup scenario at a time stands in for the resolver.
let scenario = null;
let lookups = [];
dns.promises.lookup = async (host, options) => {
    lookups.push({ host, all: options && options.all === true, verbatim: options && options.verbatim === true });
    if (scenario.error) throw new Error(scenario.error);
    if (scenario.none) return null;
    return scenario.records.map((address) => ({ address, family: address.includes(':') ? 6 : 4 }));
};

// mulberry32: the same vectors on every run.
function prng(seed) {
    let a = seed >>> 0;
    return () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}
const rand = prng(20261110);
const pick = (list) => list[Math.floor(rand() * list.length)];

const scenarios = [
    { name: 'public', records: ['93.184.216.34'] },
    { name: 'public-two', records: ['93.184.216.34', '1.1.1.1'] },
    { name: 'private', records: ['10.0.0.5'] },
    { name: 'private-192', records: ['192.168.1.20'] },
    { name: 'private-172', records: ['172.20.1.1'] },
    { name: 'cgnat', records: ['100.64.0.1'] },
    { name: 'benchmark', records: ['198.18.0.1'] },
    { name: 'zero', records: ['0.0.0.0'] },
    { name: 'loopback', records: ['127.0.0.1'] },
    { name: 'loopback-other', records: ['127.9.9.9'] },
    { name: 'public-then-loopback', records: ['8.8.8.8', '127.0.0.1'] },
    { name: 'loopback-then-public', records: ['127.0.0.1', '8.8.8.8'] },
    { name: 'public-then-private', records: ['8.8.8.8', '10.1.2.3'] },
    { name: 'metadata', records: ['169.254.169.254'] },
    { name: 'v6-public', records: ['2606:4700:4700::1111'] },
    { name: 'v6-loopback', records: ['::1'] },
    { name: 'v6-loopback-long', records: ['0:0:0:0:0:0:0:1'] },
    { name: 'v6-link-local', records: ['fe80::1'] },
    { name: 'v6-link-local-upper', records: ['FE80::1'] },
    { name: 'v6-link-local-high', records: ['febf::1'] },
    { name: 'v6-unique-local', records: ['fd00::1'] },
    { name: 'v6-unique-local-fc', records: ['fc00::1'] },
    { name: 'v6-mapped-loopback', records: ['::ffff:127.0.0.1'] },
    { name: 'v6-mapped-loopback-hex', records: ['::ffff:7f00:1'] },
    { name: 'v6-mapped-private', records: ['::ffff:10.0.0.1'] },
    { name: 'v6-mapped-public', records: ['::ffff:8.8.8.8'] },
    { name: 'v6-compatible-loopback', records: ['::127.0.0.1'] },
    { name: 'v6-unspecified', records: ['::'] },
    { name: 'both-families', records: ['2606:4700:4700::1111', '93.184.216.34'] },
    { name: 'v6-then-loopback', records: ['2606:4700:4700::1111', '::1'] },
    { name: 'no-records', records: [] },
    { name: 'no-answer', none: true },
    { name: 'not-found', error: 'getaddrinfo ENOTFOUND host.example' },
    { name: 'try-again', error: 'getaddrinfo EAI_AGAIN host.example' },
    { name: 'not-an-address', records: ['not-an-ip'] },
    { name: 'empty-address', records: [''] },
    { name: 'public-then-empty', records: ['8.8.8.8', ''] },
];

const names = [
    'localhost', 'LOCALHOST', 'LocalHost', 'localhost.', 'localhost.localdomain', 'Localhost.LocalDomain',
    'metadata.google.internal', 'METADATA.GOOGLE.INTERNAL', 'metadata.goog', 'metadata.goog.', 'Metadata.Goog',
    'example.com', 'EXAMPLE.COM', ' example.com ', '\texample.com\n', '\u00a0example.com\u3000', '\ufeffexample.com',
    'sub.example.co.uk', 'a', '-', '_', '.', '..', '...', 'a..b', 'xn--bcher-kva.de', 'b\u00fccher.de', 'B\u00dcCHER.DE',
    '', ' ', '\t\n', '[localhost]', '[LOCALHOST]', '[example.com]', '[[::1]]', '[', ']', '[]', '[ ]', ' [::1] ', '[ ::1 ]',
    '\u0391\u03a3', '\u0391\u03a3.gr', '\u03a3', 'a\u03a3b', 'a\u03a3', 'a\u03a3.', "a'\u03a3", '\u0130stanbul', 'I\u0307', '\u01c5', '\u{1F600}.example',
    'internal', 'intranet.corp', 'router.lan', 'host.docker.internal', 'kubernetes.default.svc',
    '2130706433', '017700000001', '0x7f000001', '0x7f.1', '0177.0.0.1', '127.1', '127.0.1', '10.1', '0', '00', '0x',
    '127.0.0.1.', '127.0.0.1 x', '127.0.0.1:80', 'http://127.0.0.1', '127.0.0.1/', '127.0.0.1#', '127.0.0.1%00',
    '\uff11\uff12\uff17.0.0.1', '127\u30020\u30020\u30021', '\u2460\u2461\u2466.0.0.1', '1.1.1.1\u0000', 'local\u0000host',
];

// Names in other scripts, for the lower-casing rules
const letters = ['Σ', 'Σ', 'σ', 'ʕ', 'İ', 'I', 'i', 'ǅ', "'", '.', ':', '́', 'ʰ', 'ª', 'Ａ', 'ａ', 'ß', 'ẞ', 'K',
    'Å', 'Ω', 'Ⴀ', 'Ꭰ', '\u{10400}', 'ᲀ', 'Ⅰ', 'Ⓐ', '­', '‍', 'ͅ', 'A', 'z', '-', 'Α', '中', 'א', '\u{1E900}', 'և', 'ﬀ'];
for (let i = 0; i < 1500; i++) {
    let name = '';
    const n = 1 + Math.floor(rand() * 6);
    for (let j = 0; j < n; j++) name += pick(letters);
    names.push(name);
}

const v4Literals = [];
for (let a = 0; a <= 255; a++) {
    for (const b of [0, 1, 15, 16, 17, 18, 19, 20, 31, 32, 63, 64, 100, 127, 128, 168, 169, 254, 255]) {
        v4Literals.push(`${a}.${b}.${a % 7}.${(a + b) % 256}`);
    }
}
const v4Odd = [
    '01.1.1.1', '1.01.1.1', '001.1.1.1', '1.1.1.01', '1.1.1', '1.1', '1', '1.1.1.1.1', '256.1.1.1', '1.256.1.1', '1.1.1.256',
    '300.300.300.300', '-1.1.1.1', '+1.1.1.1', '1.1.1.1 ', ' 1.1.1.1', '1.1.1.1\n', '1.1.1.1\r\n', '1..1.1', '.1.1.1.1', '1.1.1.1.',
    '0x7f.0.0.1', '0X7F.0.0.1', '0177.0.0.1', '127.0.0.1e0', '1e2.0.0.1', '127.0.0.0x1', '127,0,0,1', '127\u30020.0.1',
    '\uff11.1.1.1', '1.1.1.1%eth0', '1.1.1.1/32', '1.1.1.1:80', '127.0.0.1\u0000', '\u0000127.0.0.1', '0.0.0.0', '255.255.255.255',
    '127.000.000.001', '0127.0.0.1', '127.0.0.1.example.com', '169.254.169.254', '169.254.0.1', '169.253.1.1', '169.255.1.1',
    '100.63.255.255', '100.64.0.0', '100.127.255.255', '100.128.0.0', '172.15.255.255', '172.16.0.0', '172.31.255.255', '172.32.0.0',
    '192.167.1.1', '192.168.0.0', '192.169.0.0', '198.17.0.1', '198.18.0.1', '198.19.255.255', '198.20.0.1', '9.255.255.255', '11.0.0.0',
    '126.255.255.255', '128.0.0.0', '0.1.2.3', '224.0.0.1', '240.0.0.1', '192.0.2.1', '192.0.0.1', '203.0.113.5', '198.51.100.7',
];
const v6Literals = [
    '::1', '::', '0:0:0:0:0:0:0:1', '0:0:0:0:0:0:0:0', '0000:0000:0000:0000:0000:0000:0000:0001', '::0001', '::01', '0::1', '0:0::1', '::0:1',
    '1::', '::2', 'fe80::1', 'FE80::1', 'Fe80::abcd', 'fe80:0:0:0:0:0:0:1', 'fe80::', 'fe80::1%eth0', 'fe80::1%25eth0', 'fe80::1%', 'fe80::1%0',
    'fe80::1%a.b:c-d', 'fe80::1%eth 0', 'fe81::1', 'fe8f::1', 'fe90::1', 'febf::1', 'fec0::1', 'fe7f::1', 'fe8::1', 'fe800::1', '0fe80::1',
    'fc00::1', 'FC00::1', 'fc::1', 'fcff::', 'fd00::1', 'FD12:3456:789a::1', 'fd::', 'fb00::1', 'fe00::1', 'f::1', 'fc', 'fd', 'fc00', 'fcab:cdef::',
    'ff02::1', 'ff00::', '2001:db8::1', '2001:DB8::1', '2606:4700:4700::1111', '2002:7f00:1::', '2002:a00:1::1', '64:ff9b::7f00:1', '64:ff9b::127.0.0.1',
    '::ffff:127.0.0.1', '::FFFF:127.0.0.1', '::ffff:127.1', '::ffff:7f00:1', '::ffff:7f00:0001', '0:0:0:0:0:ffff:127.0.0.1', '0:0:0:0:0:ffff:7f00:1',
    '::ffff:0:127.0.0.1', '::ffff:10.0.0.1', '::ffff:192.168.1.1', '::ffff:172.16.0.1', '::ffff:100.64.0.1', '::ffff:8.8.8.8', '::ffff:169.254.169.254',
    '::ffff:0.0.0.0', '::ffff:255.255.255.255', '::ffff:01.1.1.1', '::ffff:256.1.1.1', '::ffff:1.1.1', '::ffff:', '::ffff', '::ffff:127.0.0.1%eth0',
    '::127.0.0.1', '::10.0.0.1', '::0.0.0.1', '::1.2.3.4', '1:2:3:4:5:6:127.0.0.1', '1:2:3:4:5:6:7:127.0.0.1', '::1%lo', '::1%', '::1 ', ' ::1', '::1\n',
    '[::1]', '[::ffff:127.0.0.1]', ':::', ':::1', '1:2:3', '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7:8:9', '1:2:3:4:5:6:7::', '::2:3:4:5:6:7:8', '1::8',
    '12345::', 'g::1', '::g', '1::2::3', ':1', '1:', '::1::', '0x1::', '::0x1', '-1::', '::1/128', '::%eth0', '%eth0', '::1%eth0%eth1', 'fe80::1%\u00e9',
];
const randomAlphabet = ['0', '1', '2', '5', '7', '9', 'a', 'c', 'd', 'e', 'f', 'F', 'x', ':', ':', '::', '.', '.', '%', '[', ']', ' ', '-', 'ffff', 'fe80', '127', '255', '169.254', 'fc', 'fd', '10', '192.168', '::ffff:', 'g', '\u00e9'];
function randomText(max) {
    let out = '';
    const n = 1 + Math.floor(rand() * max);
    for (let i = 0; i < n; i++) out += pick(randomAlphabet);
    return out;
}
const randomAddresses = [];
for (let i = 0; i < 3000; i++) randomAddresses.push(randomText(9));
// Addresses that are valid by construction, so the blocked ranges are hit often
for (let i = 0; i < 1500; i++) {
    const a = pick([0, 10, 100, 127, 169, 172, 192, 198, 8, 1, 224]);
    const b = pick([0, 16, 18, 19, 31, 32, 64, 127, 128, 168, 254, 255, Math.floor(rand() * 256)]);
    const v4 = `${a}.${b}.${Math.floor(rand() * 256)}.${Math.floor(rand() * 256)}`;
    const form = pick(['plain', 'mapped', 'MAPPED', 'compat', 'long-mapped', 'zone', 'nat64']);
    randomAddresses.push({
        plain: v4, mapped: `::ffff:${v4}`, MAPPED: `::FFFF:${v4}`, compat: `::${v4}`, 'long-mapped': `0:0:0:0:0:ffff:${v4}`,
        zone: `::ffff:${v4}%eth0`, nat64: `64:ff9b::${v4}`,
    }[form]);
}
for (let i = 0; i < 800; i++) {
    const groups = [];
    const count = 1 + Math.floor(rand() * 8);
    for (let j = 0; j < count; j++) groups.push(pick(['0', '1', 'fe80', 'FE80', 'fc00', 'fd12', 'ffff', '7f00', 'abcd', '0000', '00fe', 'febf', '2001', 'db8']));
    let text = groups.join(':');
    if (rand() < 0.5) text = pick(['::', '']) + text + pick(['', '::', '%eth0', '']);
    randomAddresses.push(text);
}

const out = [];
const addresses = [...v4Literals, ...v4Odd, ...v6Literals, ...randomAddresses];

for (const allowPrivate of [false, true]) {
    for (const ip of addresses) {
        out.push({ fn: 'isBlockedIp', ip, allowPrivate, blocked: guard.isBlockedIp(ip, { allowPrivate }) });
    }
}
// Called without options, as most callers do
for (const ip of [...v4Odd, ...v6Literals]) {
    out.push({ fn: 'isBlockedIp', ip, allowPrivate: false, blocked: guard.isBlockedIp(ip) });
}

const hosts = [...names, ...v4Odd, ...v6Literals, ...v4Literals.filter((_, i) => i % 37 === 0), ...randomAddresses.filter((_, i) => i % 9 === 0)];
for (const allowPrivate of [false, true]) {
    for (const host of hosts) {
        const v = { fn: 'hostname', host, allowPrivate };
        try { v.ok = guard.assertSafeHostname(host, { allowPrivate }); } catch (e) { v.err = e.message; v.code = e.code; v.name = e.name; }
        out.push(v);
    }
}

async function resolved(host, allowPrivate, sc) {
    scenario = sc; lookups = [];
    const v = { fn: 'resolved', host, allowPrivate, scenario: sc };
    try { v.ok = await guard.assertSafeResolvedHost(host, { allowPrivate }); } catch (e) { v.err = e.message; v.code = e.code; }
    v.lookups = lookups;
    return v;
}

const urlHosts = [
    'example.com', 'EXAMPLE.com', 'localhost', 'LOCALHOST', 'localhost.', 'lo%63alhost', 'metadata.google.internal', 'metadata.goog',
    '127.0.0.1', '127.1', '127.0.1', '2130706433', '017700000001', '0x7f000001', '0x7f.1', '0X7F.0.0.1', '0177.0.0.1', '0177.1', '127.0.0.1.',
    '127.0.0.1..', '1.1.1.1.1', '256.1.1.1', '0x100000000', '4294967295', '4294967296', '1.2.3.0x100', '1.2.0x10000', '0', '0x', '0x.0x', '08.1.1.1', '1.1.1.1e1',
    '%31%32%37.0.0.1', '%31%32%37%2e0%2e0%2e1', '127%2e0%2e0%2e1', '10.0.0.5', '192.168.1.1', '172.16.0.9', '100.64.0.1', '169.254.169.254', '8.8.8.8', '1.1.1.1', '0.0.0.0',
    '[::1]', '[0:0:0:0:0:0:0:1]', '[::ffff:127.0.0.1]', '[::ffff:7f00:1]', '[::FFFF:127.0.0.1]', '[::ffff:10.0.0.1]', '[::ffff:8.8.8.8]', '[::127.0.0.1]', '[::]', '[fe80::1]',
    '[FE80::1]', '[fe80::1%25eth0]', '[fe80::1%eth0]', '[fc00::1]', '[fd00::1]', '[2606:4700:4700::1111]', '[2001:db8:0:0:1:0:0:1]', '[1:0:0:2:0:0:0:3]', '[1:2:3:4:5:6:7:8]',
    '[::1', '::1]', '[::1]x', '[]', '[:]', '[1:2:3]', '[::1::]', '[1.2.3.4]', '[::1.2.3]', '[::01.2.3.4]', '[::1.2.3.4.5]', '[1:2:3:4:5:6:7:1.2.3.4]', '[g::1]',
    'exa mple.com', 'exa%20mple.com', 'exa<mple.com', 'exa^mple.com', 'exa|mple.com', 'exa%mple.com', 'exa%2mple.com', 'a.b.c', 'a..b', '.', '..', '.example.com', 'example.com.',
    'xn--bcher-kva.de', 'XN--BCHER-KVA.DE', 'xn--a', 'xn--', 'foxn--x.com', 'a.xn--pokxncvks', 'b\u00fccher.de', '\u2460\u2461\u2466.0.0.1', '\uff11\uff12\uff17.0.0.1',
    '127\u30020\u30020\u30021', 'ex\u00adample.com', '%E2%80%8Bexample.com', '%00', 'a%00b', '%', '%zz', '%4', 'a_b.example', '-a.example', 'a-.example', '1-1', '1_1', '+1',
    '\u{1F600}.example', 'A\u0308.example', 'fa\u00df.de', '\u0130.com', 'a\u200db.com',
];
const urlShapes = [
    (h) => `http://${h}/`, (h) => `https://${h}/`, (h) => `http://${h}`, (h) => `HTTP://${h}/path?q=1#frag`, (h) => `http://${h}:80/`, (h) => `https://${h}:443/x`,
    (h) => `http://${h}:8080/a/../b/./c`, (h) => `http://${h}:0/`, (h) => `http://${h}:65535/`, (h) => `http://${h}:65536/`, (h) => `http://${h}:/`, (h) => `http://${h}:x/`,
    (h) => `http://user@${h}/`, (h) => `http://user:pass@${h}/`, (h) => `http://:@${h}/`, (h) => `http://@${h}/`, (h) => `http://:pw@${h}/`, (h) => `http://a@b@${h}/`,
    (h) => `http:\\\\${h}\\x`, (h) => `http:/${h}/`, (h) => `http:${h}/`, (h) => `http:///${h}/`, (h) => `//${h}/`, (h) => `${h}`, (h) => ` http://${h}/ `, (h) => `ht\ttp://${h}/\n`,
    (h) => `http://${h}#@evil.example`, (h) => `http://evil.example#@${h}`, (h) => `http://evil.example\\@${h}`, (h) => `http://${h}\\@evil.example`, (h) => `http://${h}%2f@evil.example/`,
    (h) => `http://evil.example/@${h}`, (h) => `http://evil.example?@${h}`, (h) => `http://${h}?x=1`, (h) => `http://${h}/%2e%2e/%2E/x^y{z}|"<>\``, (h) => `http://${h}/?a b'"<>#c d\`"`,
    (h) => `ftp://${h}/`, (h) => `file://${h}/etc/passwd`, (h) => `ws://${h}/`, (h) => `wss://${h}/`, (h) => `gopher://${h}/`, (h) => `foo://${h}:99/x`, (h) => `foo://u:p@${h}/`,
    (h) => `javascript:alert('${h}')`, (h) => `data:text/plain,${h}`, (h) => `mailto:a@${h}`, (h) => `blob:http://${h}/x`, (h) => `view-source:http://${h}/`, (h) => `HtTpS://${h}/`,
    (h) => `http://${h}:00080/`, (h) => `http://${h}:99999999999999999999/`, (h) => `https://${h}:80/`, (h) => `http://${h}:443/`, (h) => `http://${h}/\u00e9?\u00e9#\u00e9`,
];
const urlOdd = [
    '', ' ', 'http://', 'https://', 'http:', 'http', 'http:/', 'http:///', 'http://?', 'http://#', 'http://:80', 'http://@', 'http://:@', 'file:///etc/passwd', 'file://', 'file:',
    'file:///C:/x', 'file://C:/x', 'file://C|/x', 'file://c:', 'file://localhost/x', 'file://exa mple/x', 'file://a:b/x', 'file:c:\\x', 'file:/c|/x/../y',
    'about:blank', 'foo:', 'foo:/', 'foo://', 'foo:///', 'foo://:80', 'foo://h:65536', 'foo://h:x', 'foo://exa mple/', 'foo://ex%20ample/', 'foo://[::1]/', 'foo://[::1/', 'foo://\u00e9/',
    'foo:bar', 'foo:/bar', 'foo:?x', 'foo:#x', '1http://example.com', '+http://example.com', 'h+t-t.p://example.com', 'http\u0000://example.com', '://example.com', ':', '/', '#', '?',
    'http://example.com/a b', 'http://example.com/\\..\\x', 'http://example.com/a/./b/../c', 'http://example.com/..', 'http://example.com/.', 'http://example.com/%2E%2e/x',
    'http://example.com?', 'http://example.com#', 'http://example.com/?#', 'http://example.com/??', 'http://example.com/##', 'http://example.com/#?', 'http://example.com/\u0000',
    'http://example.com/\ud83d\ude00', 'http://example.com:80:80/', 'http://example.com:-1/', 'http://example.com: 80/', 'http://example.com:8 0/', 'http://[::1]:80/', 'http://[::1]:/',
    'http://[::1]80/', 'http://1.1.1.1:80@evil.example/', 'http://1.1.1.1:80:@evil.example/', 'http://evil.example:80@1.1.1.1/', 'http://a:b:c@1.1.1.1/', 'http://a%40b@1.1.1.1/',
    '\u0000http://1.1.1.1/', '\u001fhttp://1.1.1.1/\u0020', 'h\nt\rt\tp://1.1.1.1/', 'http://1.1.1.1\t/', 'http://1.\n1.1.1/', 'http://\t127.0.0.1/', 'http:// 127.0.0.1/',
];
const urls = [...urlOdd];
for (const h of urlHosts) for (const shape of urlShapes) urls.push(shape(h));
const urlPieces = ['http', 'https', 'ftp', 'file', 'foo', 'ws', ':', ':', '/', '/', '//', '\\', '@', '#', '?', '[', ']', '%', '%2e', '%2E', '%41', '%00', '..', '.', '127.0.0.1', '0x7f', '017',
    'localhost', 'example.com', '1', '0', '9', 'a', 'A', 'Z', ' ', '\t', '::', '::1', 'ffff', '-', '_', '+', '~', '^', '|', '{', '}', '"', '<', '>', '`', "'", ';', '=', '&', '80', '65536', 'c|', 'C:'];
for (let i = 0; i < 6000; i++) {
    let text = rand() < 0.8 ? pick(['http:', 'https:', 'http://', 'https://', 'file:', 'file://', 'foo:', 'foo://', 'ftp://', 'ws://', 'HTTP://', 'http:/', 'http:\\\\']) : '';
    const n = Math.floor(rand() * 9);
    for (let j = 0; j < n; j++) text += pick(urlPieces);
    urls.push(text);
}

// Paths with dots, which Node's parser resolves by rules of its own
const dotPieces = ['/', '/', '.', '.', '..', 'a', '.a', 'a.', '/.', '/..', '/./', '/../', '...', '%2e', '\\', ' ', '?', '#', 'b'];
for (let i = 0; i < 3000; i++) {
    let text = pick(['http://example.com/', 'http://example.com', 'https://1.1.1.1/', 'http://example.com/.', 'http://example.com/.a', 'ws://example.com/', 'file:///', 'foo://h/']);
    const n = 1 + Math.floor(rand() * 8);
    for (let j = 0; j < n; j++) text += pick(dotPieces);
    urls.push(text);
}

function parse(url) {
    const v = { fn: 'parse', url };
    try {
        const u = new URL(url);
        v.ok = { href: u.href, protocol: u.protocol, username: u.username, password: u.password, hostname: u.hostname, port: u.port, pathname: u.pathname, search: u.search, hash: u.hash };
    } catch (e) { v.fail = true; }
    return v;
}

async function httpUrl(url, allowPrivate, sc, monitoring) {
    scenario = sc; lookups = [];
    const v = { fn: monitoring ? 'monitoringUrl' : 'httpUrl', url, allowPrivate, scenario: sc };
    try {
        const u = monitoring ? await guard.assertSafeMonitoringUrl(url) : await guard.assertSafeHttpUrl(url, { allowPrivate });
        v.ok = { href: u.href, hostname: u.hostname };
    } catch (e) { v.err = e.message; v.code = e.code; }
    v.lookups = lookups;
    return v;
}

(async () => {
    const resolvedHosts = [...names, ...v4Odd.filter((_, i) => i % 3 === 0), ...v6Literals.filter((_, i) => i % 3 === 0)];
    let n = 0;
    for (const host of resolvedHosts) {
        for (const allowPrivate of [false, true]) {
            // Every scenario for the plain names, a rotating third of them for the rest
            for (let s = 0; s < scenarios.length; s++) {
                if (host !== 'example.com' && host !== 'internal' && (s + n) % 3 !== 0) continue;
                out.push(await resolved(host, allowPrivate, scenarios[s]));
            }
            n++;
        }
    }
    for (const url of urls) out.push(parse(url));
    n = 0;
    for (const url of urls) {
        const sc = scenarios[n % scenarios.length];
        out.push(await httpUrl(url, n % 2 === 0, sc, false));
        if (n % 5 === 0) out.push(await httpUrl(url, true, scenarios[(n + 7) % scenarios.length], true));
        n++;
    }
    for (const sc of scenarios) {
        for (const allowPrivate of [false, true]) {
            out.push(await httpUrl('http://host.example/status', allowPrivate, sc, false));
            out.push(await httpUrl('https://HOST.example:8443/', allowPrivate, sc, false));
        }
        out.push(await httpUrl('http://host.example/status', true, sc, true));
    }

    const counts = {};
    for (const v of out) counts[v.fn] = (counts[v.fn] || 0) + 1;
    const body = out.map((v) => JSON.stringify(v)).join('\n') + '\n';
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.jsonl.gz'), zlib.gzipSync(body, { level: 9 }));
    console.log(JSON.stringify(counts), out.length, 'vectors');
})();
