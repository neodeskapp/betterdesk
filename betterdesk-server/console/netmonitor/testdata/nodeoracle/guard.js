'use strict';

// Records what lib/ssrfGuard.js and the host check of
// services/networkMonitor.js answer, into ../guard.json.gz. See README.md.

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const { table, install } = require('./lib');

const consoleDir = process.argv[2] || '/repo/web-nodejs';
install();

const guard = require(path.join(consoleDir, 'lib/ssrfGuard'));
const monitor = require(path.join(consoleDir, 'services/networkMonitor'));

function outcome(fn) {
    try {
        return { ok: fn() };
    } catch (err) {
        return { error: err.message, blocked: err.name === 'SsrfBlockedError' };
    }
}

async function outcomeAsync(fn) {
    try {
        return { ok: await fn() };
    } catch (err) {
        return { error: err.message, blocked: err.name === 'SsrfBlockedError' };
    }
}

// ---- addresses -------------------------------------------------------------

const seconds = [0, 1, 15, 16, 17, 18, 19, 20, 31, 32, 63, 64, 65, 100, 127, 128, 168, 169, 254, 255];
const ips = [];
for (let a = 0; a <= 255; a++) {
    for (const b of seconds) ips.push(`${a}.${b}.${(a * 7 + b) % 256}.${(a + b * 3) % 256}`);
}
ips.push(
    '', ' ', 'abc', '1.2.3', '1.2.3.4.5', '01.2.3.4', '1.02.3.4', '256.1.1.1', '1.2.3.256', ' 127.0.0.1', '127.0.0.1 ',
    '127.0.0.1\n', '1.2.3.4%eth0', '127.1', '2130706433', '0x7f.0.0.1', '0177.0.0.1', '127.0.0.01', '1.2.3.', '.1.2.3.4',
    '::1', '::', '::0', '::2', '0::1', '::0001', '0:0:0:0:0:0:0:1', '0000:0000:0000:0000:0000:0000:0000:0001',
    '::1%lo', '::1%1', '::1%', 'fe80::1', 'FE80::1', 'fe80::', 'fe80::1%eth0', 'fe8::1', 'fe81::1', 'febf::1', 'fec0::1',
    'fe80:0:0:0:0:0:0:1', '0fe80::1', 'fc00::1', 'FC00::1', 'fd00::1', 'fdff:ffff::1', 'fc::', 'fd::', 'fb00::1', 'fe00::1',
    'f::1', 'fc', 'fd', 'fe80:', 'fcxyz', 'fd00', '::ffff:127.0.0.1', '::FFFF:127.0.0.1', '::ffff:10.0.0.1',
    '::ffff:8.8.8.8', '::ffff:169.254.1.1', '::ffff:192.168.1.1', '::ffff:7f00:1', '::ffff:7f00:0001', '::ffff:0:127.0.0.1',
    '0:0:0:0:0:ffff:127.0.0.1', '0:0:0:0:0:ffff:7f00:1', '::127.0.0.1', '::ffff:1.2.3', '::ffff:256.1.1.1',
    '::ffff:01.2.3.4', '64:ff9b::7f00:1', '64:ff9b::127.0.0.1', '2001:db8::1', '2001:DB8::1', '2001:db8:0:0:0:0:0:1',
    '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7', '1:2:3:4:5:6:7:8:9', '1::2::3', ':::1', '1:2:3:4:5:6:1.2.3.4', '1::1.2.3.4',
    '12345::1', 'g::1', '::1 ', ' ::1', '[::1]', '::ffff:127.0.0.1%lo', '2002:7f00:1::', '100::', 'ff02::1',
);
const blockedIp = [];
for (const ip of ips) {
    for (const allowPrivate of [false, true]) {
        blockedIp.push([ip, allowPrivate, guard.isBlockedIp(ip, { allowPrivate })]);
    }
}
blockedIp.push(['127.0.0.1', null, guard.isBlockedIp('127.0.0.1')], ['10.0.0.1', null, guard.isBlockedIp('10.0.0.1')]);

// ---- host names --------------------------------------------------------------

const hostnames = [
    'localhost', 'LOCALHOST', ' localhost ', '\tLocalHost\n', 'localhost.', 'localhost.localdomain', 'Localhost.LocalDomain',
    'metadata.google.internal', 'METADATA.GOOGLE.INTERNAL', 'metadata.goog', 'metadata.goog.', 'xlocalhost', 'localhost.example',
    '', ' ', '\u00a0', '\ufeff', '[', ']', '[]', '[ ]', '[::1]', '[::1', '::1]', '[fe80::1]', '[FE80::1]', '[127.0.0.1]',
    '[10.0.0.1]', '[[::1]]', ' [::1] ', '[localhost]', '[example.com]', 'example.com', 'EXAMPLE.com', '\tExample.COM\n',
    ...Object.keys(table), 'unknown.test', 'UNKNOWN.Test', 'a', '0', '-', '.', '..', 'a..b', 'exa mple.com', 'ex\u00e4mple.com',
    '127.1', '2130706433', '0x7f.1', '0x7f.0.0.1', '0177.0.0.1', '127.000.000.001', '010.0.0.1', '1.2.3', '999.1.1.1',
    '1.2.3.4.5', '256', '0', '0x', '0x.1', '4294967295', '4294967296', '0x100000000', '1.16777215', '1.16777216', '1.2.65535',
    '1.2.65536', '08.1.1.1', '0xg.1.1.1', '127.0.0.1.', '169.254.169.254', '0xa9.0xfe.0xa9.0xfe', '2852039166', '0251.0376.0251.0376',
];
for (const ip of ips) if (ips.indexOf(ip) % 37 === 0 || ip.includes(':')) hostnames.push(ip);

async function main() {
    const hostname = [];
    const resolved = [];
    for (const host of hostnames) {
        for (const allowPrivate of [false, true]) {
            hostname.push([host, allowPrivate, outcome(() => guard.assertSafeHostname(host, { allowPrivate }))]);
            resolved.push([host, allowPrivate, await outcomeAsync(() => guard.assertSafeResolvedHost(host, { allowPrivate }))]);
        }
    }

    // ---- URLs ---------------------------------------------------------------
    const urlHosts = [
        'example.com', 'EXAMPLE.COM', 'example.com.', 'internal.corp', 'loop.test', 'mixed.test', 'v6.test', 'empty.test',
        'fail.test', 'again.test', 'zero.test', 'localhost', 'LocalHost', 'localhost.', 'metadata.google.internal',
        '127.0.0.1', '127.1', '2130706433', '0x7f.0.0.1', '0x7f000001', '0177.0.0.1', '017700000001', '127.0.0.1.', '1.2.3.4',
        '10.0.0.1', '192.168.1.1', '172.16.0.1', '169.254.169.254', '0.0.0.0', '0', '0x0', '256.1.1.1', '1.2.3.4.5', '999',
        '4294967295', '4294967296', '[::1]', '[::]', '[0:0:0:0:0:0:0:1]', '[::0001]', '[fe80::1]', '[FE80::1]', '[fc00::1]',
        '[fd00::1]', '[2001:db8::1]', '[2001:DB8:0:0:0:0:0:1]', '[::ffff:127.0.0.1]', '[::ffff:7f00:1]', '[::ffff:8.8.8.8]',
        '[::127.0.0.1]', '[::1%25lo]', '[::1', '::1', '[1::2::3]', '[g::1]', 'm\u00fcnchen.de', 'M\u00dcNCHEN.de',
        'xn--mnchen-3ya.de', 'xn--', 'xn--a', '\u4f8b\u3048.\u30c6\u30b9\u30c8', 'ex\u3002ample.com', '%31%32%37.0.0.1', '%6c%6fcalhost', 'exa%20mple.com',
        'exa mple.com', 'exa\tmple.com', 'exa\nmple.com', 'a_b.example', '-a.example', 'a-.example', 'a..b', '.', '',
        'example.com:80:80', 'ex^ample.com', 'ex|ample.com', 'ex<ample.com', 'ex%ample.com', 'a.b.c.d.e.f.example',
        'unknown.test', 'a-b.c-d.example', '1.2.3.4a', '0x7g.1', '1e3', '0x1e3', '09', '0.0.0.0x', '1..2',
    ];
    const urlPorts = ['', ':', ':80', ':443', ':8080', ':0', ':65535', ':65536', ':99999', ':080', ':8a', ':-1', ': 80', ':+80', ':00000000000080'];
    const urlPaths = ['', '/', '/health', '/a/../b', '/a/./b/', '/%2e%2e/x', '/a b', '/a"b<c>d`e{f}g', '/\u00e4', '/a\\b', '/.well-known/x/../y', '//x', '/a?b=1', '/a?b=1#c', '?q=1', '#f', '/a?', '/a?x y&z="\'<>', '/a%', '/a%zz', '/;p=1', '/a|b^c[d]e'];
    const urls = new Set();
    for (const scheme of ['http://', 'https://']) {
        for (const host of urlHosts) {
            urls.add(scheme + host);
            for (const port of urlPorts) urls.add(scheme + host + port + '/x');
        }
        for (const p of urlPaths) {
            urls.add(scheme + 'example.com' + p);
            urls.add(scheme + 'example.com:8080' + p);
            urls.add(scheme + '10.0.0.1' + p);
        }
    }
    const creds = ['user@', 'user:pw@', ':pw@', ':@', '@', 'user:@', 'a@b@', 'a:b:c@', '%40@', 'user%3Apw@', ' @'];
    for (const c of creds) {
        for (const host of ['example.com', '10.0.0.1', '127.0.0.1', '[2001:db8::1]', 'unknown.test', '']) {
            urls.add('http://' + c + host + '/');
            urls.add('https://' + c + host + ':8443/p?q');
        }
    }
    for (const u of [
        '', ' ', 'x', 'http', 'http:', 'http:/', 'http://', 'http:///', 'http:////example.com/', 'http:example.com',
        'http:/example.com', 'http:\\\\example.com\\a\\b', 'HTTP://EXAMPLE.COM/A', 'hTtPs://example.com', 'https:example.com/x',
        '  http://example.com/  ', '\thttp://example.com\n', 'ht\ttp://exa\nmple.com/a\rb', 'http://example.com/ a ',
        'ftp://example.com/', 'ftp://127.0.0.1/', 'file:///etc/passwd', 'file://localhost/etc/passwd', 'file://example.com/x',
        'file:', 'file:/c:/x', 'javascript:alert(1)', 'data:text/plain,hi', 'mailto:a@b.c', 'ws://example.com/', 'wss://example.com/',
        'gopher://example.com/', 'x://example.com/', 'x://exa mple.com/', 'x://[::1]/', 'x://[zz]/', 'x://host:99999/', 'x:/a/../..',
        'x:', 'x:y', '1x://example.com', '+x://example.com', 'x+y-z.1://example.com/', '//example.com/', '/path', '?q', '#f',
        'example.com', 'example.com:80', 'example.com:80/x', 'localhost:5000', '0.0.0.0:5000/health', 'http://example.com:80',
        'https://example.com:443/x', 'http://example.com:443/', 'https://example.com:80/', 'http://127.0.0.1:5000/health',
        'http://0.0.0.0:5000/health', 'http://[::ffff:0.0.0.0]/', 'http://[::ffff:0:0]/', 'http://[64:ff9b::7f00:1]/',
        'http://example.com/\u{1F600}?\u{1F600}#\u{1F600}', 'http://\u{1F600}.example/', 'http://example.com\\@evil.test/',
        'http://evil.test#@example.com/', 'http://evil.test?@example.com/', 'http://example.com:80@evil.test/',
        'http://127.0.0.1\\@example.com/', 'http://example.com/%00', 'http://exam\u0000ple.com/', 'http://example.com:8080/a?b#c',
        'https://user:pass@127.0.0.1/', 'http://169.254.169.254/latest/meta-data/', 'http://metadata.google.internal/computeMetadata/v1/',
        'http://[fe80::1%25eth0]/', 'http://0/', 'http://0x7f.1/', 'http://127.0.1/', 'http://1.1/', 'http://017700000001/',
        'http://localhost\t/', 'http://loc\nalhost/', 'http://LOCALHOST./', 'http://localhost.localdomain/', 'http://metadata.goog/',
    ]) urls.add(u);

    const url = [];
    for (const u of urls) {
        url.push([u, await outcomeAsync(async () => {
            const parsed = await guard.assertSafeMonitoringUrl(u);
            return { protocol: parsed.protocol, hostname: parsed.hostname, port: parsed.port, pathname: parsed.pathname, search: parsed.search };
        })]);
    }
    const urlOther = [];
    for (const u of [undefined, null, 0, 5, true, {}, [], ['http://example.com/']]) {
        urlOther.push([JSON.stringify(u) === undefined ? null : u, u === undefined, await outcomeAsync(() => guard.assertSafeMonitoringUrl(u))]);
    }

    // ---- the host check of the tools -----------------------------------------
    const toolHosts = new Set([
        ...hostnames, 'a;b', 'a&b', 'a|b', 'a`b', 'a$b', 'a(b', 'a)b', 'a{b', 'a}b', 'a[b', 'a]b', 'a<b', 'a>b', 'a\\b', 'a!b',
        'a#b', 'a*b', 'a?b', "a'b", 'a"b', 'a~b', 'a b', 'a/b', 'a%b', 'a=b', 'a,b', 'a@b', 'a+b', 'a_b', 'a:b', '-c5', '-a', 'a-',
        'a-b', 'a--b', '-', 'a.-b.c', 'a.b-.c', 'A.B.C', '1.2.3.4', '999.999.999.999', '1234.1.1.1', '1.2.3', '1.2.3.4.5',
        'abcdef:0123', 'ABCDEF', ':', '::', ':::', 'g::1', '::g', 'x'.repeat(63), 'x'.repeat(64), ('x'.repeat(63) + '.').repeat(3) + 'x'.repeat(61),
        ('x'.repeat(63) + '.').repeat(3) + 'x'.repeat(62), ('a.').repeat(126) + 'a', ('a.').repeat(127) + 'a', 'a'.repeat(254),
        '1'.repeat(253), '1'.repeat(254), ':'.repeat(253), ':'.repeat(254), '::1%lo', 'fe80::1%eth0', '::ffff:127.0.0.1',
        '::ffff:1.2.3.4', '1::1.2.3.4', 'example.com\n', '\nexample.com', 'exam\u00adple.com', '\u{1F600}', 'a.b.', '.a.b',
    ]);
    const resolve = [];
    for (const host of toolHosts) resolve.push([host, await outcomeAsync(() => monitor.resolveHost(host))]);
    const resolveOther = [];
    for (const host of [undefined, null, 0, 5, true, {}, [], ['example.com']]) {
        resolveOther.push([host === undefined ? null : host, await outcomeAsync(() => monitor.resolveHost(host))]);
    }

    const out = { dns: table, blocked_ip: blockedIp, hostname, resolved, url, url_other: urlOther, resolve, resolve_other: resolveOther };
    const file = path.join(__dirname, '..', 'guard.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(out), { level: 9 }));
    const counts = Object.fromEntries(Object.entries(out).map(([k, v]) => [k, Array.isArray(v) ? v.length : Object.keys(v).length]));
    console.log('wrote', file, JSON.stringify(counts));
}

main().catch((err) => { console.error(err); process.exit(1); });
