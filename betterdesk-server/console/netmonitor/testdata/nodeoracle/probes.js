'use strict';

// Runs the checks of services/networkMonitor.js against servers this
// script starts on 0.0.0.0, with a ping command of its own, and records
// what they answer into ../probes.json. See README.md.
//
// Run it in a container without a network (--network none), so that no
// check can leave it and an address outside is unreachable at once.

const fs = require('fs');
const os = require('os');
const path = require('path');
const http = require('http');
const https = require('https');
const net = require('net');
const { execFileSync } = require('child_process');
const { table, lookups, install } = require('./lib');

const consoleDir = process.argv[2] || '/repo/web-nodejs';

// Node reads the certificate authorities it trusts besides its own when it
// starts. So the script makes an authority and a certificate signed by it,
// and runs itself again with the authority trusted.
if (!process.env.NETMON_ORACLE_CA) {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-netmon-ca-'));
    const at = (name) => path.join(dir, name);
    const openssl = (...args) => execFileSync('openssl', args, { stdio: 'ignore' });
    openssl('req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', at('ca.key'), '-out', at('ca.pem'), '-days', '3650', '-subj', '/CN=Oracle CA');
    openssl('req', '-newkey', 'rsa:2048', '-nodes', '-keyout', at('leaf.key'), '-out', at('leaf.csr'), '-subj', '/CN=oracle.test');
    fs.writeFileSync(at('ext.cnf'), 'subjectAltName=DNS:zero.test,DNS:*.oracle.test,IP:192.0.2.1\n');
    openssl('x509', '-req', '-in', at('leaf.csr'), '-CA', at('ca.pem'), '-CAkey', at('ca.key'), '-CAcreateserial', '-out', at('leaf.pem'), '-days', '3650', '-extfile', at('ext.cnf'));
    const child = require('child_process').spawnSync(process.execPath, process.argv.slice(1), {
        stdio: 'inherit',
        env: { ...process.env, NETMON_ORACLE_CA: dir, NODE_EXTRA_CA_CERTS: at('ca.pem') },
    });
    fs.rmSync(dir, { recursive: true, force: true });
    process.exit(child.status === null ? 1 : child.status);
}

table['other.test'] = ['0.0.0.0'];
table['www.oracle.test'] = ['0.0.0.0'];

// ---- the ping command -----------------------------------------------------------

// What the ping command prints and how it ends, by the host it is given.
const pings = {
    'ping-ok.test': { stdout: 'PING ping-ok.test (192.0.2.10) 56(84) bytes of data.\n64 bytes from 192.0.2.10: icmp_seq=1 ttl=64 time=12.3 ms\n\n--- ping-ok.test ping statistics ---\n1 packets transmitted, 1 received, 0% packet loss, time 0ms\nrtt min/avg/max/mdev = 12.300/12.300/12.300/0.000 ms\n', code: 0 },
    'ping-int.test': { stdout: '64 bytes from 192.0.2.10: icmp_seq=1 ttl=64 time=7 ms\n', code: 0 },
    'ping-small.test': { stdout: '64 bytes from 192.0.2.10: icmp_seq=1 ttl=64 time=0.045 ms\n', code: 0 },
    'ping-nospace.test': { stdout: 'time=3.25ms\n', code: 0 },
    'ping-upper.test': { stdout: 'TIME=5.5 MS\n', code: 0 },
    'ping-dot.test': { stdout: 'time=12. ms\n', code: 0 },
    'ping-twice.test': { stdout: 'time=1.5 ms\ntime=9.5 ms\n', code: 0 },
    'ping-less.test': { stdout: 'time<1 ms\n', code: 0 },
    'ping-windows.test': { stdout: 'Reply from 192.0.2.10: bytes=32 time=14ms TTL=57\n', code: 0 },
    'ping-notime.test': { stdout: '1 packets transmitted, 1 received\n', code: 0 },
    'ping-silent.test': { stdout: '', code: 0 },
    'ping-fail.test': { stdout: 'From 192.0.2.1 icmp_seq=1 Destination Host Unreachable\n', code: 1 },
    'ping-fail-with-time.test': { stdout: 'time=4.2 ms\n', code: 2 },
    'ping-slow.test': { stdout: 'time=1.0 ms\n', code: 0, sleep: 30 },
    '10.1.2.3': { stdout: '64 bytes from 10.1.2.3: icmp_seq=1 ttl=64 time=0.5 ms\n', code: 0 },
    '0.0.0.0': { stdout: '64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=0.021 ms\n', code: 0 },
    'fd00::1': { stdout: '64 bytes from fd00::1: icmp_seq=1 ttl=64 time=2.75 ms\n', code: 0 },
};
for (const name of Object.keys(pings)) {
    if (name.endsWith('.test')) table[name] = ['192.0.2.10'];
}
install();

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-netmon-oracle-'));
const pingLog = path.join(tmp, 'ping.log');
{
    let script = `#!/bin/bash\nprintf '%s\\n' "$*" >> '${pingLog}'\nhost="\${@: -1}"\ncase "$host" in\n`;
    for (const [host, p] of Object.entries(pings)) {
        script += `  '${host}')${p.sleep ? ` exec sleep ${p.sleep};` : ''} printf '%s' '${p.stdout.replace(/'/g, `'\\''`)}'; exit ${p.code};;\n`;
    }
    script += `  *) echo "ping: $host: Name or service not known" >&2; exit 2;;\nesac\n`;
    fs.writeFileSync(path.join(tmp, 'ping'), script, { mode: 0o755 });
    process.env.PATH = tmp + ':' + process.env.PATH;
}

const config = require(path.join(consoleDir, 'config/config'));
const monitor = require(path.join(consoleDir, 'services/networkMonitor'));

// ---- the servers ------------------------------------------------------------------

let requests = [];

function handler(req, res) {
    requests.push({ method: req.method, url: req.url, headers: req.rawHeaders });
    const p = req.url.split('?')[0];
    const status = /^\/status\/(\d+)$/.exec(p);
    if (status) {
        res.writeHead(Number(status[1]), { 'Content-Type': 'text/plain' });
        return res.end(Number(status[1]) === 204 || Number(status[1]) === 304 ? undefined : 'status');
    }
    switch (p) {
    case '/ok':
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        return res.end('ok');
    case '/redirect':
        res.writeHead(302, { Location: '/ok' });
        return res.end();
    case '/big':
        res.writeHead(200, { 'Content-Type': 'application/octet-stream' });
        return res.end(Buffer.alloc(1 << 20, 'x'));
    case '/trickle': {
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        let n = 0;
        const timer = setInterval(() => {
            res.write('part\n');
            if (++n === 6) { clearInterval(timer); res.end(); }
        }, 100);
        return undefined;
    }
    case '/slow-headers':
        return undefined;
    case '/slow-body':
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        res.write('part');
        return undefined;
    case '/hangup':
        return req.socket.destroy();
    case '/reset-body':
        res.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Length': '100' });
        res.write('part');
        return setTimeout(() => req.socket.destroy(), 50);
    default:
        res.writeHead(404, { 'Content-Type': 'text/plain' });
        return res.end('not found');
    }
}

function listen(server, host) {
    return new Promise((resolve, reject) => {
        server.once('error', reject);
        server.listen(0, host, () => resolve(server.address().port));
    });
}

function settle(promise, ms) {
    return Promise.race([
        promise.then((value) => ({ value }), (err) => ({ thrown: err.message })),
        new Promise((resolve) => setTimeout(() => resolve({ never: true }), ms)),
    ]);
}

async function main() {
    const uncaught = [];
    process.on('uncaughtException', (err) => uncaught.push(err.message));

    execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', path.join(tmp, 'key.pem'), '-out', path.join(tmp, 'cert.pem'),
        '-days', '3650', '-subj', '/CN=oracle.test', '-addext', 'subjectAltName=DNS:oracle.test,DNS:*.oracle.test,IP:192.0.2.1'], { stdio: 'ignore' });
    const key = fs.readFileSync(path.join(tmp, 'key.pem'));
    const cert = fs.readFileSync(path.join(tmp, 'cert.pem'));

    const servers = [];
    const ports = {};
    const httpServer = http.createServer(handler);
    ports.http = await listen(httpServer, '0.0.0.0');
    const tlsServer = https.createServer({ key, cert }, handler);
    ports.tls = await listen(tlsServer, '0.0.0.0');
    const garbage = net.createServer((s) => { s.on('error', () => {}); s.end('SSH-2.0-OpenSSH_9.6\r\n'); });
    ports.garbage = await listen(garbage, '0.0.0.0');
    const sockets = [];
    const silent = net.createServer((s) => { s.on('error', () => {}); sockets.push(s); });
    ports.silent = await listen(silent, '0.0.0.0');
    const closing = net.createServer();
    ports.closed = await listen(closing, '0.0.0.0');
    await new Promise((resolve) => closing.close(resolve));
    servers.push(httpServer, tlsServer, garbage, silent);

    const caDir = process.env.NETMON_ORACLE_CA;
    const trusted = https.createServer({ key: fs.readFileSync(path.join(caDir, 'leaf.key')), cert: fs.readFileSync(path.join(caDir, 'leaf.pem')) }, handler);
    ports.tlsca = await listen(trusted, '0.0.0.0');
    servers.push(trusted);

    const fill = (v) => (typeof v === 'string' ? v.replace(/\{(\w+)\}/g, (all, name) => (name in ports ? String(ports[name]) : all)) : v);
    const mask = (text) => {
        if (typeof text !== 'string') return text;
        for (const [name, port] of Object.entries(ports)) text = text.split(String(port)).join(`{${name}}`);
        return text;
    };
    const maskResult = (r) => {
        if (!r || typeof r !== 'object') return r;
        const out = {};
        for (const [k, v] of Object.entries(r)) out[k] = k === 'rtt_ms' && typeof v === 'number' ? 'number' : mask(v);
        return out;
    };

    const records = [];
    async function run(name, kind, args, opts = {}) {
        requests = [];
        lookups.length = 0;
        fs.writeFileSync(pingLog, '');
        config.allowSelfSignedCerts = !!opts.selfSigned;
        const real = args.map(fill);
        const fn = { http: monitor.checkHttp, tcp: monitor.checkTcpPort, ping: monitor.pingHost, resolve: monitor.resolveHost }[kind];
        const started = Date.now();
        const settled = await settle(fn(...real), opts.cap || 5000);
        const rec = {
            name, kind,
            args: args.map((a) => (typeof a === 'number' && !Number.isFinite(a) ? String(a) : a)),
            self_signed: !!opts.selfSigned,
            inject: opts.inject || null,
        };
        if (settled.never) rec.never = true;
        else if (settled.thrown !== undefined) rec.thrown = mask(settled.thrown);
        else rec.result = maskResult(settled.value);
        rec.requests = requests.map((r) => ({ method: r.method, url: r.url, headers: r.headers.map(mask) }));
        rec.lookups = lookups.slice();
        const pingLines = fs.readFileSync(pingLog, 'utf8').split('\n').filter(Boolean);
        if (pingLines.length) rec.ping_args = pingLines;
        if (opts.slow) rec.took_at_least = opts.slow;
        if (opts.slow && Date.now() - started < opts.slow) throw new Error(`${name} came back after ${Date.now() - started} ms`);
        records.push(rec);
        for (const s of sockets.splice(0)) s.destroy();
    }

    // ---- HTTP ---------------------------------------------------------------------
    await run('http ok', 'http', ['http://0.0.0.0:{http}/ok', 5000]);
    await run('http ok again on the kept connection', 'http', ['http://0.0.0.0:{http}/ok', 5000]);
    for (const status of [200, 201, 204, 301, 302, 304, 307, 399, 400, 401, 403, 404, 418, 500, 503]) {
        await run(`http status ${status}`, 'http', [`http://0.0.0.0:{http}/status/${status}`, 5000]);
    }
    await run('http redirect is not followed', 'http', ['http://0.0.0.0:{http}/redirect', 5000]);
    await run('http no path', 'http', ['http://0.0.0.0:{http}', 5000]);
    await run('http by name', 'http', ['http://zero.test:{http}/ok', 5000]);
    await run('http in capitals with spaces and a fragment', 'http', ['  HTTP://ZERO.TEST:{http}/ok?a=1&b=two#frag  ', 5000]);
    await run('http path with spaces', 'http', ['http://0.0.0.0:{http}/ok/a b?x y=1 2', 5000]);
    await run('http path with odd characters', 'http', ['http://0.0.0.0:{http}/ok/a|b^c[d]e~f;g=h,i\'j"k<l>m`n{o}p?q=r|s^t[u]v\'w"x<y>z`{}', 5000]);
    await run('http path with dots', 'http', ['http://0.0.0.0:{http}/a/../b/./c/%2e%2e/d', 5000]);
    await run('http path with letters outside ASCII', 'http', ['http://0.0.0.0:{http}/\u00e4\u20ac\u{1F600}?\u00f6=\u00fc', 5000]);
    await run('http path with backslashes', 'http', ['http:\\\\0.0.0.0:{http}\\ok\\x', 5000]);
    await run('http path with percent signs', 'http', ['http://0.0.0.0:{http}/ok%2Fa%zz%', 5000]);
    await run('http two slashes', 'http', ['http://0.0.0.0:{http}//ok', 5000]);
    await run('http empty query', 'http', ['http://0.0.0.0:{http}/ok?', 5000]);
    await run('http big body', 'http', ['http://0.0.0.0:{http}/big', 5000]);
    await run('http body in parts within the timeout', 'http', ['http://0.0.0.0:{http}/trickle', 300], { slow: 500 });
    await run('http no answer', 'http', ['http://0.0.0.0:{http}/slow-headers', 300], { slow: 250 });
    await run('http body stops', 'http', ['http://0.0.0.0:{http}/slow-body', 300], { slow: 250 });
    await run('http connection closed before the answer', 'http', ['http://0.0.0.0:{http}/hangup', 5000]);
    await run('http connection closed in the body', 'http', ['http://0.0.0.0:{http}/reset-body', 5000], { cap: 1500 });
    await run('http server is not HTTP', 'http', ['http://0.0.0.0:{garbage}/', 5000]);
    await run('http server says nothing', 'http', ['http://0.0.0.0:{silent}/', 300], { slow: 250 });
    await run('http closed port', 'http', ['http://0.0.0.0:{closed}/', 5000]);
    await run('http closed port by name', 'http', ['http://zero.test:{closed}/', 5000]);
    await run('http default port', 'http', ['http://0.0.0.0/', 5000]);
    await run('https default port', 'http', ['https://0.0.0.0/', 5000]);
    await run('https self-signed', 'http', ['https://0.0.0.0:{tls}/ok', 5000]);
    await run('https self-signed by name', 'http', ['https://zero.test:{tls}/ok', 5000]);
    await run('https self-signed allowed', 'http', ['https://0.0.0.0:{tls}/ok', 5000], { selfSigned: true });
    await run('https self-signed allowed by name', 'http', ['https://zero.test:{tls}/status/404', 5000], { selfSigned: true });
    await run('https trusted by name', 'http', ['https://zero.test:{tlsca}/ok', 5000]);
    await run('https trusted by a name under the wildcard', 'http', ['https://www.oracle.test:{tlsca}/ok', 5000]);
    await run('https trusted, another name', 'http', ['https://other.test:{tlsca}/ok', 5000]);
    await run('https trusted, by address', 'http', ['https://0.0.0.0:{tlsca}/ok', 5000]);
    await run('https trusted, another name, allowed', 'http', ['https://other.test:{tlsca}/ok', 5000], { selfSigned: true });
    await run('https to a plain server', 'http', ['https://0.0.0.0:{http}/ok', 5000]);
    await run('http to a TLS server', 'http', ['http://0.0.0.0:{tls}/ok', 5000]);
    await run('https closed port', 'http', ['https://0.0.0.0:{closed}/', 5000]);
    await run('http loopback', 'http', ['http://127.0.0.1:{http}/ok', 5000]);
    await run('http name of loopback', 'http', ['http://loop.test:{http}/ok', 5000]);
    await run('http name with a loopback address among others', 'http', ['http://mixed.test:{http}/ok', 5000]);
    await run('http unknown name', 'http', ['http://unknown.test:{http}/ok', 5000]);
    await run('http name the lookup fails for', 'http', ['http://again.test/', 5000]);
    await run('http name without addresses', 'http', ['http://empty.test/', 5000]);
    await run('http credentials', 'http', ['http://user:pw@0.0.0.0:{http}/ok', 5000]);
    await run('http other scheme', 'http', ['ftp://0.0.0.0:{http}/ok', 5000]);
    await run('http not a URL', 'http', ['0.0.0.0:{http}/ok', 5000]);
    await run('http empty', 'http', ['', 5000]);
    await run('http unreachable network', 'http', ['http://10.255.255.1/', 5000], { inject: 'ENETUNREACH' });
    await run('http v6 address', 'http', ['http://[2001:db8::1]:8080/x', 5000], { inject: 'ENETUNREACH' });
    await run('http v6 any address', 'http', ['http://[::]:{http}/ok', 5000]);
    await run('http name with a v6 and a v4 address', 'http', ['http://dual.test:8080/', 5000], { inject: 'ENETUNREACH' });
    await run('http timeout below zero', 'http', ['http://0.0.0.0:{http}/ok', -1]);
    await run('http timeout far below zero', 'http', ['http://0.0.0.0:{http}/ok', -5000000000]);
    await run('http timeout minus 1e23', 'http', ['http://0.0.0.0:{http}/ok', -1e23]);
    await run('http timeout with a fraction below zero', 'http', ['http://0.0.0.0:{http}/ok', -0.5]);
    await run('http timeout zero', 'http', ['http://0.0.0.0:{http}/ok', 0]);
    await run('http timeout too long for a timer', 'http', ['http://0.0.0.0:{http}/ok', 2147483648]);
    await run('http timeout 1e23', 'http', ['http://0.0.0.0:{http}/ok', 1e23]);
    await run('http timeout infinite', 'http', ['http://0.0.0.0:{http}/ok', Infinity]);
    await run('http timeout NaN', 'http', ['http://0.0.0.0:{http}/ok', NaN]);
    await run('http timeout checked after the guard', 'http', ['http://127.0.0.1:{http}/ok', -1]);

    // ---- TCP -----------------------------------------------------------------------
    await run('tcp open', 'tcp', ['0.0.0.0', '{http}', 5000]);
    await run('tcp open by name', 'tcp', ['zero.test', '{http}', 5000]);
    await run('tcp open, server says nothing', 'tcp', ['0.0.0.0', '{silent}', 300]);
    await run('tcp closed', 'tcp', ['0.0.0.0', '{closed}', 5000]);
    await run('tcp closed by name', 'tcp', ['zero.test', '{closed}', 5000]);
    await run('tcp port 0', 'tcp', ['0.0.0.0', 0, 5000]);
    await run('tcp port minus 0', 'tcp', ['0.0.0.0', -0, 5000]);
    await run('tcp port 65535', 'tcp', ['0.0.0.0', 65535, 5000]);
    await run('tcp port 65536', 'tcp', ['0.0.0.0', 65536, 5000]);
    await run('tcp port below zero', 'tcp', ['0.0.0.0', -1, 5000]);
    await run('tcp port NaN', 'tcp', ['0.0.0.0', NaN, 5000]);
    await run('tcp port with a fraction', 'tcp', ['0.0.0.0', 80.5, 5000]);
    await run('tcp port 1e21', 'tcp', ['0.0.0.0', 1e21, 5000]);
    await run('tcp port 4294967376', 'tcp', ['0.0.0.0', 4294967376, 5000]);
    await run('tcp port infinite', 'tcp', ['0.0.0.0', Infinity, 5000]);
    await run('tcp loopback', 'tcp', ['127.0.0.1', '{http}', 5000]);
    await run('tcp name of loopback', 'tcp', ['loop.test', '{http}', 5000]);
    await run('tcp name without addresses', 'tcp', ['empty.test', 80, 5000]);
    await run('tcp unknown name', 'tcp', ['unknown.test', 80, 5000]);
    await run('tcp not a host', 'tcp', ['not a host', 80, 5000]);
    await run('tcp bad port on a blocked host', 'tcp', ['127.0.0.1', -1, -1]);
    await run('tcp unreachable network', 'tcp', ['10.255.255.1', 80, 5000], { inject: 'ENETUNREACH' });
    await run('tcp unreachable network by name', 'tcp', ['internal.corp', 443, 5000], { inject: 'ENETUNREACH' });
    await run('tcp v6 address', 'tcp', ['2001:db8::1', 443, 5000], { inject: 'ENETUNREACH' });
    await run('tcp name with a v6 address first', 'tcp', ['dual.test', 443, 5000], { inject: 'ENETUNREACH' });
    await run('tcp timeout below zero', 'tcp', ['0.0.0.0', '{http}', -1]);
    await run('tcp timeout far below zero', 'tcp', ['0.0.0.0', '{http}', -5000000000]);
    await run('tcp timeout below zero and a bad port', 'tcp', ['0.0.0.0', -1, -1]);
    await run('tcp timeout zero', 'tcp', ['0.0.0.0', '{http}', 0]);
    await run('tcp timeout too long for a timer', 'tcp', ['0.0.0.0', '{http}', 2147483648]);
    await run('tcp timeout infinite', 'tcp', ['0.0.0.0', '{http}', Infinity]);
    await run('tcp timeout NaN', 'tcp', ['0.0.0.0', '{http}', NaN]);
    for (const r of records) {
        if (r.kind === 'tcp' && typeof r.args[1] === 'string') r.port_is_number = true;
    }

    // ---- ping ----------------------------------------------------------------------
    for (const host of Object.keys(pings)) {
        if (host === 'ping-slow.test') continue;
        await run(`ping ${host}`, 'ping', [host, 5000]);
    }
    await run('ping stopped after the timeout', 'ping', ['ping-slow.test', 100], { slow: 2000, cap: 8000 });
    await run('ping a name the command does not know', 'ping', ['example.com', 5000]);
    await run('ping in capitals', 'ping', ['PING-OK.TEST', 5000]);
    await run('ping loopback', 'ping', ['127.0.0.1', 5000]);
    await run('ping name of loopback', 'ping', ['loop.test', 5000]);
    await run('ping unknown name', 'ping', ['unknown.test', 5000]);
    await run('ping not a host', 'ping', ['-c 5', 5000]);
    for (const timeout of [1, 999, 1000, 1001, 1500, 2500, 60000, -1, -999, -1000, -1999, -2000, -2001, -3000, -5000000000, -1e23, 0.5, 2147481647]) {
        await run(`ping timeout ${timeout}`, 'ping', ['ping-ok.test', timeout]);
    }
    await run('ping timeout too long for a timer', 'ping', ['ping-slow.test', 2147481648], { cap: 8000 });
    await run('ping timeout 1e23', 'ping', ['ping-slow.test', 1e23], { cap: 8000 });
    await run('ping timeout infinite', 'ping', ['ping-ok.test', Infinity]);
    await run('ping timeout NaN', 'ping', ['ping-ok.test', NaN]);

    // ---- resolve -------------------------------------------------------------------
    for (const host of ['zero.test', 'dual.test', 'example.com', 'EXAMPLE.COM', 'empty.test', 'fail.test', 'again.test', 'loop.test', '10.1.2.3', 'FD00::1', '0.0.0.0', '127.1', '010.0.0.1', '256']) {
        await run(`resolve ${host}`, 'resolve', [host]);
    }

    // ---- which targets are due ----------------------------------------------------
    const now = Date.UTC(2026, 10, 10, 12, 0, 0, 123);
    const due = [];
    const m = new monitor.NetworkMonitor({});
    const intervals = [null, 0, 1, 1000, 5000, 30000, 60000, 300000, -5, 1500.5, 1e12];
    const offsets = [0, 1, 999, 1000, 4999, 5000, 5001, 24999, 25000, 25001, 29999, 30000, 54999, 55000, 55001, 59999, 60000, 61000, 120000, 294999, 295000, 300000, -1000, -3600000];
    const iso = (ms) => new Date(ms).toISOString();
    const sqliteText = (ms) => new Date(ms).toISOString().slice(0, 19).replace('T', ' ');
    for (const poll of [60000, 30000, 5000, 1000, 120000, 1, 8000, 10000, 10001, -4]) {
        m.pollInterval = poll;
        for (const interval of intervals) {
            for (const offset of offsets) {
                due.push([poll, interval, 'iso', iso(now - offset), m.isDue({ interval_ms: interval, last_check_at: iso(now - offset) }, now)]);
            }
            for (const text of [null, '', 'never', '2026-11-10', sqliteText(now - 30000), sqliteText(now - 3600000), iso(now - 30000).replace('Z', '+01:00'), '2026-11-10T11:59:00Z', '2026-11-10T11:59:00.5Z', '1762776000000']) {
                due.push([poll, interval, 'text', text, m.isDue({ interval_ms: interval, last_check_at: text }, now)]);
            }
        }
    }

    // ---- a target's check ----------------------------------------------------------
    const checks = [];
    const targets = [
        { name: 'ping up', check_type: 'ping', host: 'ping-ok.test', timeout_ms: 3000 },
        { name: 'ping down', check_type: 'ping', host: 'ping-fail.test' },
        { name: 'ping without host', check_type: 'ping', host: null },
        { name: 'ping empty host', check_type: 'ping', host: '' },
        { name: 'ping loopback', check_type: 'ping', host: '127.0.0.1' },
        { name: 'ping timeout zero means default', check_type: 'ping', host: 'ping-ok.test', timeout_ms: 0 },
        { name: 'ping timeout null means default', check_type: 'ping', host: 'ping-ok.test', timeout_ms: null },
        { name: 'ping timeout 1e23', check_type: 'ping', host: 'ping-slow.test', timeout_ms: 1e23 },
        { name: 'tcp up', check_type: 'tcp', host: '0.0.0.0', port: '{http}' },
        { name: 'tcp down', check_type: 'tcp', host: '0.0.0.0', port: '{closed}', timeout_ms: 1000 },
        { name: 'tcp without port', check_type: 'tcp', host: '0.0.0.0', port: null },
        { name: 'tcp port zero', check_type: 'tcp', host: '0.0.0.0', port: 0 },
        { name: 'tcp port too large', check_type: 'tcp', host: '0.0.0.0', port: 5000000000 },
        { name: 'tcp without host', check_type: 'tcp', host: null, port: 80 },
        { name: 'tcp loopback', check_type: 'tcp', host: '127.0.0.1', port: '{http}' },
        { name: 'http url', check_type: 'http', host: '', url: 'http://0.0.0.0:{http}/ok' },
        { name: 'http url wins over host', check_type: 'http', host: '127.0.0.1', port: 1, url: 'http://0.0.0.0:{http}/status/503' },
        { name: 'http url of another scheme than the type', check_type: 'https', host: '', url: 'http://0.0.0.0:{http}/status/204' },
        { name: 'http host and port', check_type: 'http', host: '0.0.0.0', port: '{http}' },
        { name: 'http host without port', check_type: 'http', host: '0.0.0.0', port: null },
        { name: 'http host and port, url empty', check_type: 'http', host: 'zero.test', port: '{http}', url: '' },
        { name: 'https host without port', check_type: 'https', host: '0.0.0.0', port: null },
        { name: 'https host and port', check_type: 'https', host: '0.0.0.0', port: '{tls}' },
        { name: 'http without host', check_type: 'http', host: null, port: null },
        { name: 'http empty host', check_type: 'http', host: '', port: null },
        { name: 'http host with a path', check_type: 'http', host: '0.0.0.0/ok?x', port: '{http}' },
        { name: 'http loopback', check_type: 'http', host: '', url: 'http://127.0.0.1:{http}/ok' },
        { name: 'http no answer', check_type: 'http', host: '', url: 'http://0.0.0.0:{http}/slow-headers', timeout_ms: 200 },
        { name: 'http port 1e21', check_type: 'http', host: '0.0.0.0', port: 1e21 },
        { name: 'unknown type', check_type: 'icmp', host: '0.0.0.0' },
        { name: 'empty type', check_type: '', host: '0.0.0.0' },
        { name: 'type in capitals', check_type: 'TCP', host: '0.0.0.0', port: '{http}' },
    ];
    let id = 0;
    for (const t of targets) {
        id += 1;
        const calls = [];
        const db = {
            insertNetworkCheck: async (data) => { calls.push({ insert: maskResult(data) }); },
            updateNetworkTarget: async (tid, data) => {
                const ok = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/.test(data.last_check_at) && Math.abs(new Date(data.last_check_at) - Date.now()) < 5000;
                calls.push({ update: { id: tid, last_status: data.last_status, last_check_at: ok ? 'now' : data.last_check_at, last_rtt_ms: typeof data.last_rtt_ms === 'number' ? 'number' : data.last_rtt_ms, keys: Object.keys(data) } });
            },
        };
        const mon = new monitor.NetworkMonitor(db);
        config.allowSelfSignedCerts = false;
        fs.writeFileSync(pingLog, '');
        const real = { id, timeout_ms: 5000, url: null, port: null, ...t };
        const stored = { ...real };
        if (typeof real.port === 'string') real.port = Number(fill(real.port));
        real.url = fill(real.url);
        const settled = await settle(mon.checkTarget(real), 8000);
        const pingLines = fs.readFileSync(pingLog, 'utf8').split('\n').filter(Boolean);
        checks.push({ target: stored, result: settled.value ? maskResult(settled.value) : settled, keys: settled.value ? Object.keys(settled.value) : null, calls, ping_args: pingLines });
        for (const s of sockets.splice(0)) s.destroy();
    }
    // A result that cannot be stored is returned all the same
    {
        const db = { insertNetworkCheck: async () => { throw new Error('disk full'); }, updateNetworkTarget: async () => { throw new Error('not reached'); } };
        const settled = await settle(new monitor.NetworkMonitor(db).checkTarget({ id: 99, check_type: 'icmp', host: 'x' }), 3000);
        checks.push({ target: { id: 99, name: 'store fails', check_type: 'icmp', host: 'x', store_fails: true }, result: maskResult(settled.value), keys: Object.keys(settled.value), calls: [], ping_args: [] });
    }

    const out = { dns: table, pings, records, due_now: now, due, checks, uncaught };
    fs.writeFileSync(path.join(__dirname, '..', 'probes.json'), JSON.stringify(out, null, 1) + '\n');
    console.log('wrote probes.json:', records.length, 'checks,', due.length, 'due vectors,', checks.length, 'target checks,', uncaught.length, 'uncaught errors');
    for (const s of servers) s.close();
    for (const s of servers) if (s.closeAllConnections) s.closeAllConnections();
    fs.rmSync(tmp, { recursive: true, force: true });
    process.exit(0);
}

main().catch((err) => { console.error(err); process.exit(1); });
