'use strict';

// Records the redirect of a console in HTTPS mode as upstream 3.5.264 has
// it (audit H5): lib/redirectHost.js on generated input, and the plain
// port of the real server.js over sockets with different public hosts.
// Runs on a tree whose server.js has the redirect host (REPO=/spec with
// the spec tree mounted there). See README.md.
//
//   REPO=/spec node redirect.js     writes ../node_redirect.json

const fs = require('fs');
const os = require('os');
const path = require('path');
const { compactParts, exchange, startNode, makeCerts, logLines, sleep, b64 } = require('./lib');

const REPO = process.env.REPO || '/repo';
const OUT = process.argv[2] || path.join(__dirname, '..', 'node_redirect.json');
const PORT = 25080;
const HTTPS_PORT = 25443;
const { normalizeConfiguredHost, resolveRedirectHost, buildSafeRedirectUrl } = require(path.join(REPO, 'web-nodejs', 'lib', 'redirectHost.js'));

function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return { int: (n) => Math.floor(next() * n), pick: (l) => l[Math.floor(next() * l.length)] };
}

// ---- lib/redirectHost.js ----

const HOSTS = [
    'desk.example.com', 'DESK.Example.COM', '  desk.example.com  ', 'a', 'a-b', '-a', 'a-', 'a..b', 'a.b.', '.a', 'a_b', 'a.-b', 'a-.b',
    'xn--bcher-kva.de', 'XN--BCHER-KVA.DE', 'bücher.de', '\u212Aelvin.com', '\u0130stanbul.com', '\u017Ftar.com', 'ǅ.com',
    '127.0.0.1', '127.1', '0x7f.0.0.1', '256.0.0.1', '01.2.3.4', '1.2.3.4.5', '0', '1', '999999999',
    '::1', '[::1]', '[::1', '::1]', '[[::1]]', '2001:db8::1', '[2001:DB8::1]', 'fe80::1%eth0', '[fe80::1%eth0]', '::ffff:127.0.0.1',
    '::FFFF:1.2.3.4', '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7:8:9', ':::', '::', '[::]', '0.0.0.0', 'fe80::1%', 'fe80::1%e\u00e9',
    'desk.example.com:5443', 'user@desk', 'desk/x', 'desk?x', 'desk#x', 'a b', 'a\tb', '\u00a0desk.example.com\u00a0', '\ufeffdesk',
    '\u2028desk\u2029', 'attacker.example\r\nX-Injected: 1', 'evil.example"><script>alert(1)</script>', '', ' ', '\t\n', 'localhost', 'LOCALHOST',
    '-', '*', '$', 'a$b', 'a%zz', '%41.com', 'a'.repeat(63) + '.com', 'a'.repeat(64) + '.com',
    `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`, `${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(62)}`,
    `[${'a'.repeat(253)}]`, `[${'a'.repeat(254)}]`, `${'1.'.repeat(126)}1`, `${'1.'.repeat(127)}1`,
    'https://desk.example.com', 'https://desk.example.com:5443/login', 'HTTPS://Desk.Example.COM', 'http://[::1]:8443/', 'http://[2001:db8::1]',
    'https://[2001:DB8:0:0::1]/', 'https://user:pass@desk.example.com/', 'https://0x7f.1/', 'https://127.1', 'http://ex%41mple.com',
    'https://bücher.de', 'https://xn--bcher-kva.de', 'https://xn--a.com', 'https://\u212Aelvin.com', 'foo://Host:1/x', 'foo://', 'https://',
    'https:///x', 'http://a b', 'mailto://x', 'file:///etc/passwd', 'file://host/x', 'file://Host.Example/x', 'ws://desk', 'git+ssh://desk.example.com/x',
    'a1+.-://h', '1http://h', 'https:desk.example.com', 'https://desk.example.com\\evil', 'https://%5B::1%5D', 'https://[::1', 'https://a..b',
    'https://-a.com', 'https://a_b.com', 'https://desk.example.com.', 'https://1.2.3.4.5', 'https://999999999', 'https://0', 'http://[fe80::1%25eth0]',
    'https://desk.example.com:99999', 'https://desk.example.com:abc', 'https://evil.com@desk.example.com', 'https://desk.example.com#frag',
    'https://desk.example.com?q', '  https://desk.example.com  ', 'https://desk.example.com\r\nX: y', 'https://a%2eb', 'https://A%2Eb.COM',
    'https://[::ffff:127.0.0.1]', 'https://[1:2:3:4:5:6:7:8:9]', 'http://192.168.0.257', 'http://0300.0250.0.1', 'https://\u00a0desk',
    'ftp://Desk.Example.com:21', 'urn://x', 'x://[::1]', 'x://a b', 'x://%zz', 'https://ab\u0000c',
];

// Space characters around a host: JavaScript's trim removes all but U+0085 and U+180E
HOSTS.push(...[0x2000, 0x2003, 0x200a, 0x202f, 0x205f, 0x3000, 0x1680, 0x85, 0x180e].map((c) => String.fromCharCode(c) + 'desk.example.com' + String.fromCharCode(c)));

function randomHosts() {
    const r = rng(7);
    const pieces = ['a', 'b', 'z', 'K', '0', '9', '.', '-', '_', ':', '[', ']', '/', '%', '@', ' ', '\t', 'x', 'n', '1', '2', 'f', 'e', '\u00e9',
        '\u212A', '::', '%41', '%25', '..', 'xn--', '\u0130', '#', '?', '\\'];
    const prefixes = ['', '', '', 'https://', 'http://', 'foo://', 'HTTPS://', '['];
    const out = [];
    for (let i = 0; i < 4000; i++) {
        let s = r.pick(prefixes);
        const n = 1 + r.int(12);
        for (let j = 0; j < n; j++) s += r.pick(pieces);
        if (r.int(4) === 0) s += r.pick([']', ':443', ':0', '/', '.com', ' ']);
        out.push(s);
    }
    return out;
}

const LISTEN = ['', ' ', '0.0.0.0', '::', '[::]', '127.0.0.1', '::1', '[::1]', ' 10.0.0.1 ', ' [::1] ', 'localhost', 'host.example', 'fe80::1%eth0',
    '::ffff:127.0.0.1', '::FFFF:1.2.3.4', '[::ffff:1.2.3.4]', '0', '1.2.3', '256.1.1.1', '0.0.0.00', '00.0.0.0', '[0.0.0.0]', '::0', '0::', '[::', '::]'];
const PORTS = [443, 5443, 25443, 1, 0, -1, 80, 65535, 65536];
const TARGETS = ['/', '/x', '/login?x=1', '/a/b?c=d&e', '//evil.example/login', '///x', '/\\evil.com', '\\\\evil', 'http://evil.example/login',
    'https://evil.com:444/y?z', '*', '', 'x', '?q', '#f', '/%2F%2Fevil.com', '/a b', '/ä', '/a"<>', '/a?x=<script>', '/path#frag', '/%zz', '/%E4',
    ' /x', '\t/x', '/x\r\nSet-Cookie: a=b', '/' + 'x'.repeat(3000)];

function unitVectors() {
    const normalize = [];
    for (const raw of [...HOSTS, ...randomHosts()]) normalize.push([raw, normalizeConfiguredHost(raw)]);
    normalize.push([null, normalizeConfiguredHost(undefined)]);
    const resolve = [];
    const configured = ['', ...HOSTS.filter((_, i) => i % 3 === 0)];
    for (const c of configured) {
        for (const l of LISTEN) {
            const res = resolveRedirectHost({ configuredHost: c, listenHost: l });
            resolve.push([c, l, res.host, res.source]);
        }
    }
    const build = [];
    const hosts = ['desk.example.com', '127.0.0.1', '::1', '2001:db8::1', 'fe80::1%eth0', 'localhost', 'Desk.Example.COM', 'evil.com\r\nX: 1',
        'https://desk.example.com:8443/x', '[::1]', '', 'a_b', 'xn--bcher-kva.de'];
    for (const host of hosts) {
        for (const port of PORTS) {
            for (const target of TARGETS) build.push([host, port, target, buildSafeRedirectUrl({ host, port, urlPath: target })]);
        }
    }
    return { normalize, resolve, build };
}

// ---- the redirect listener of server.js ----

function part(text) {
    return { data: b64(Buffer.from(text, 'latin1')), delay: 0 };
}

// The requests of the old recording (record.js, redirectCases) and a few
// more: every Host header is one a client chose, so none may reach the
// answer.
function redirectCases(all) {
    const cases = [];
    const r = (id, text) => ({ id: `redirect ${id}`, listener: 'redirect', parts: [part(text)] });
    const hosts = all
        ? ['example.com', 'example.com:80', 'example.com:5000', 'EXAMPLE.com', '[::1]', '[::1]:80', '[::1', '127.0.0.1:99', 'a:b:c', 'evil.com@good.com',
            'good.com/evil', 'good.com?x', 'good.com#x', 'good.com\\evil', 'good.com evil', 'xn--nxasmq6b', 'a$b', 'a%zz', '$', 'a$b%zz', 'a!b', "a'b",
            'a(b)', 'a+b', 'a;b', 'a=b', 'a~b', 'a&b', 'a*b', 'gööd.com', 'a"b', 'a<b>', '', ' spaced ', 'host,other', '%41.com', 'example.com.',
            '-', '_x_', '*', 'attacker.example:5443']
        : ['good.test', 'evil.com', 'attacker.example:5443', '[::1]'];
    for (const host of hosts) {
        cases.push(r(`host ${JSON.stringify(host)}`, `GET /x HTTP/1.1\r\nHost: ${host}\r\nConnection: close\r\n\r\n`));
    }
    const targets = all
        ? ['/', '/a/b?c=d&e', '//evil.com/x', '/\\evil.com', '/%2F%2Fevil.com', '/a%20b', '/a b', '/ä', '/a"<>', '/a?x=<script>',
            '/../x', '/./x', '*', 'http://evil.com/x', 'https://evil.com:444/y?z', '/api/login', '/path#frag', '/%zz', '/%E4', '/' + 'x'.repeat(3000),
            '///x', 'http://good.test/x?y']
        : ['/', '//evil.com/x', 'http://evil.com/x', '/a?x=<script>'];
    for (const t of targets) {
        cases.push(r(`target ${JSON.stringify(t.slice(0, 40))}`, `GET ${t} HTTP/1.1\r\nHost: good.test\r\nConnection: close\r\n\r\n`));
    }
    if (!all) return cases;
    for (const m of ['HEAD', 'POST', 'PUT', 'DELETE', 'OPTIONS', 'PATCH', 'TRACE', 'PROPFIND']) {
        cases.push(r(`method ${m}`, `${m} /m HTTP/1.1\r\nHost: good.test\r\nContent-Length: 0\r\nConnection: close\r\n\r\n`));
    }
    for (const accept of ['text/html', 'application/json', 'text/html, text/plain;q=0.5', 'text/plain;q=0, text/html;q=0', '*/*', 'image/png', 'text/*']) {
        cases.push(r(`accept ${accept}`, `GET /a HTTP/1.1\r\nHost: good.test\r\nAccept: ${accept}\r\nConnection: close\r\n\r\n`));
    }
    cases.push(r('http 1.0 without host', 'GET /a HTTP/1.0\r\n\r\n'));
    cases.push(r('http 1.0 with host', 'GET /a HTTP/1.0\r\nHost: good.test\r\n\r\n'));
    cases.push(r('two hosts', 'GET /a HTTP/1.1\r\nHost: one.test\r\nHost: two.test\r\nConnection: close\r\n\r\n'));
    cases.push(r('forwarded host ignored', 'GET /a HTTP/1.1\r\nHost: good.test\r\nX-Forwarded-Host: evil.com\r\nX-Forwarded-Proto: http\r\nConnection: close\r\n\r\n'));
    cases.push(r('connect', 'CONNECT evil.com:443 HTTP/1.1\r\nHost: evil.com:443\r\n\r\n'));
    cases.push(r('post with body', 'POST /a HTTP/1.1\r\nHost: good.test\r\nContent-Type: application/json\r\nContent-Length: 7\r\nConnection: close\r\n\r\n{"a":1}'));
    cases.push(r('keep-alive', 'GET /a HTTP/1.1\r\nHost: good.test\r\n\r\n'));
    return cases;
}

async function main() {
    const certDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-oracle-certs-'));
    const certs = makeCerts(certDir);
    const tls = { PORT: String(PORT), HTTPS_PORT: String(HTTPS_PORT), API_ENABLED: 'false', HTTPS_ENABLED: 'true',
        SSL_CERT_PATH: certs.rsa.cert, SSL_KEY_PATH: certs.rsa.key, BETTERDESK_API_URL: 'http://127.0.0.1:25114/api' };
    const scenarios = [];
    // durable is the public endpoints file written into DATA_DIR before the start
    const scenario = async (name, env, all, durable) => {
        process.stderr.write(`[oracle] ${name}\n`);
        let dataDir = null;
        const extra = {};
        if (durable !== undefined) {
            dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-oracle-data-'));
            fs.writeFileSync(path.join(dataDir, 'public-endpoints.env'), durable);
            extra.DATA_DIR = dataDir;
        }
        const node = await startNode({ ...tls, ...env, ...extra }, [PORT, HTTPS_PORT]);
        const exchanges = [];
        try {
            for (const c of redirectCases(all)) {
                const res = await exchange({ port: PORT, secure: false, parts: c.parts.map((p) => ({ delay: 0, data: Buffer.from(p.data, 'base64') })), wait: 6000 });
                exchanges.push({ ...c, secure: false, ...res, parts: compactParts(c.parts) });
            }
        } finally {
            await sleep(200);
            await node.stop();
            if (dataDir) fs.rmSync(dataDir, { recursive: true, force: true });
        }
        const recorded = { ...env };
        for (const key of ['SSL_CERT_PATH', 'SSL_KEY_PATH']) recorded[key] = tls[key].replace(/\/tmp\/[^/]+\//, '<certs>/');
        recorded.HTTPS_ENABLED = env.HTTPS_ENABLED || tls.HTTPS_ENABLED;
        scenarios.push({ name, env: recorded, durable: durable === undefined ? null : durable, exchanges, logs: logLines(node.lines) });
    };
    await scenario('redirect listen host', { HOST: '127.0.0.1' }, true);
    await scenario('redirect HTTPS_ENABLED=True', { HOST: '127.0.0.1', HTTPS_ENABLED: 'True', HTTP_REDIRECT_HTTPS: 'TRUE' }, false);
    await scenario('redirect public host', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: 'Desk.Example.COM' }, true);
    await scenario('redirect public url', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: 'https://desk.example.com:8443/login' }, false);
    await scenario('redirect public ipv6', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: '[2001:DB8::1]' }, false);
    await scenario('redirect public host refused', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: 'evil.example"><b>' }, false);
    await scenario('redirect public host blank', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: '   ' }, false);
    await scenario('redirect public host with port', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: 'desk.example.com:8443' }, false);
    await scenario('redirect durable file', { HOST: '127.0.0.1' }, false, 'PANEL_PUBLIC_HOST=durable.example\n');
    await scenario('redirect environment over durable file', { HOST: '127.0.0.1', PANEL_PUBLIC_HOST: 'env.example' }, false, 'PANEL_PUBLIC_HOST=durable.example\n');
    await scenario('redirect wildcard host', { HOST: '0.0.0.0' }, false);
    fs.rmSync(certDir, { recursive: true, force: true });
    const unit = unitVectors();
    const out = { node: process.version, ports: { PORT, HTTPS_PORT }, ...unit, scenarios };
    fs.writeFileSync(OUT, JSON.stringify(out) + '\n');
    const count = scenarios.reduce((n, s) => n + s.exchanges.length, 0);
    process.stderr.write(`[oracle] normalize ${unit.normalize.length}, resolve ${unit.resolve.length}, build ${unit.build.length}, ${scenarios.length} scenarios with ${count} exchanges written to ${OUT}\n`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
