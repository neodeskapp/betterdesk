'use strict';

// Helpers of record.js: start the Node console (server.js) with an
// environment, send raw bytes over plain or TLS sockets and keep what came
// back, and a scripted API server that echoes what reached it.

const { spawn, execFileSync } = require('child_process');
const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const net = require('net');
const os = require('os');
const path = require('path');
const tls = require('tls');

const REPO = process.env.REPO || '/repo';
const NODE_DIR = path.join(REPO, 'web-nodejs');

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function b64(buf) {
    return Buffer.from(buf).toString('base64');
}

// request builds one HTTP/1.x request. Host and Connection: close are added
// unless the headers name them or the options say otherwise.
function request(method, target, headers = [], body = null, opts = {}) {
    const version = opts.version || '1.1';
    const lines = [`${method} ${target} HTTP/${version}`];
    const has = (name) => headers.some(([k]) => k.toLowerCase() === name);
    if (!opts.noHost && !has('host')) lines.push('Host: oracle.test');
    for (const [k, v] of headers) lines.push(`${k}: ${v}`);
    const data = body === null ? Buffer.alloc(0) : Buffer.from(body);
    if (body !== null && !has('content-length') && !has('transfer-encoding')) lines.push(`Content-Length: ${data.length}`);
    if (opts.close !== false && !has('connection')) lines.push('Connection: close');
    return Buffer.concat([Buffer.from(lines.join('\r\n') + '\r\n\r\n', 'latin1'), data]);
}

// exchange opens a connection, sends the parts (each after its delay) and
// collects what comes back until the server closes or wait ms have passed.
function exchange({ port, secure = false, parts, wait = 5000, tlsOptions = {} }) {
    return new Promise((resolve) => {
        const started = Date.now();
        const chunks = [];
        let firstByte = null;
        let closedAt = null;
        let error = null;
        let done = false;
        const sock = secure
            ? tls.connect({ port, host: '127.0.0.1', rejectUnauthorized: false, servername: 'localhost', ...tlsOptions })
            : net.connect({ port, host: '127.0.0.1' });
        const finish = () => {
            if (done) return;
            done = true;
            clearTimeout(timer);
            sock.destroy();
            resolve({
                got: b64(Buffer.concat(chunks)),
                closed: closedAt !== null,
                closedAfter: closedAt,
                firstByteAfter: firstByte,
                error,
            });
        };
        const timer = setTimeout(finish, wait);
        sock.once(secure ? 'secureConnect' : 'connect', async () => {
            for (const part of parts) {
                if (part.delay) await sleep(part.delay);
                if (sock.destroyed || done) break;
                try { sock.write(part.data); } catch (e) { error = e.code || e.message; }
            }
        });
        sock.on('data', (d) => {
            if (firstByte === null) firstByte = Date.now() - started;
            chunks.push(d);
        });
        sock.on('error', (e) => { error = e.code || e.message; });
        sock.on('close', () => { closedAt = Date.now() - started; finish(); });
    });
}

function portOpen(port) {
    return new Promise((resolve) => {
        const s = net.connect({ port, host: '127.0.0.1' });
        s.on('connect', () => { s.destroy(); resolve(true); });
        s.on('error', () => resolve(false));
    });
}

// startNode runs server.js with env (on top of a small base) and waits until
// every port in waitPorts accepts connections or the process ends.
async function startNode(env, waitPorts, { timeout = 60000 } = {}) {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-oracle-'));
    fs.mkdirSync(path.join(tmp, 'data'));
    fs.mkdirSync(path.join(tmp, 'keys'));
    fs.writeFileSync(path.join(tmp, 'keys', '.api_key'), crypto.randomBytes(32).toString('hex'));
    const base = {
        PATH: process.env.PATH,
        HOME: process.env.HOME || '/root',
        NODE_ENV: 'production',
        DATA_DIR: path.join(tmp, 'data'),
        RUSTDESK_PATH: path.join(tmp, 'keys'),
        DB_PATH: path.join(tmp, 'keys', 'db_v2.sqlite3'),
        DB_TYPE: 'sqlite',
        DEFAULT_ADMIN_PASSWORD: crypto.randomBytes(18).toString('base64') + 'Aa1!',
        SESSION_SECRET: crypto.randomBytes(32).toString('hex'),
        AGENT_BUILD_WORKER: 'off',
        HOST: '127.0.0.1',
        API_HOST: '127.0.0.1',
    };
    const proc = spawn(process.execPath, ['server.js'], { cwd: NODE_DIR, env: { ...base, ...env } });
    const lines = [];
    const collect = (stream) => {
        let rest = '';
        stream.on('data', (d) => {
            rest += d.toString('utf8');
            const parts = rest.split('\n');
            rest = parts.pop();
            lines.push(...parts);
        });
    };
    collect(proc.stdout);
    collect(proc.stderr);
    let exitCode = null;
    const exited = new Promise((resolve) => proc.on('exit', (code) => { exitCode = code; resolve(code); }));
    const deadline = Date.now() + timeout;
    for (;;) {
        if (exitCode !== null) break;
        const open = await Promise.all(waitPorts.map(portOpen));
        if (open.every(Boolean)) break;
        if (Date.now() > deadline) throw new Error(`server.js did not open ${waitPorts}: ${lines.slice(-30).join('\n')}`);
        await sleep(200);
    }
    // the client API listener opens after the console's start-up work
    await sleep(300);
    return {
        proc, lines, exited, tmp,
        get exitCode() { return exitCode; },
        async stop() {
            if (exitCode === null) {
                proc.kill('SIGTERM');
                await Promise.race([exited, sleep(12000)]);
                if (exitCode === null) proc.kill('SIGKILL');
            }
            fs.rmSync(tmp, { recursive: true, force: true });
        },
    };
}

// logLines keeps the lines about listeners, certificates and the client API.
function logLines(lines) {
    const keep = /SSL|TLS|HTTPS|HTTP mode|redirect|Redirect|Client API|API Port|API_PORT|client API|RustDesk API|port|Port|Fix:|cookies|Failed to start/;
    return lines.filter((l) => keep.test(l)).map((l) => l.replace(/\s+$/, ''));
}

// The scripted API server. Requests with an X-Oracle header get an echo of
// what arrived; everything else (the console's own calls at start) has its
// connection closed, as if no API server were running.
function startUpstream(port) {
    const server = http.createServer((req, res) => {
        if (!req.headers['x-oracle']) {
            req.socket.destroy();
            return;
        }
        const body = [];
        req.on('data', (d) => body.push(d));
        req.on('end', () => {
            const data = Buffer.concat(body);
            const query = new URL(req.url, 'http://x').searchParams;
            const mode = query.get('upstream') || '';
            const names = [...new Set(req.rawHeaders.filter((_, i) => i % 2 === 0).map((n) => n.toLowerCase()))].sort();
            const h = (n) => (req.headers[n] === undefined ? null : String(req.headers[n]));
            const echo = {
                method: req.method,
                url: req.url,
                host: req.headers.host === `127.0.0.1:${port}` ? 'upstream' : h('host'),
                headers: names,
                xff: h('x-forwarded-for'),
                xri: h('x-real-ip'),
                xfp: h('x-forwarded-proto'),
                xfh: h('x-forwarded-host'),
                authorization: h('authorization'),
                cookie: h('cookie'),
                contentType: h('content-type'),
                contentLength: h('content-length'),
                transferEncoding: h('transfer-encoding'),
                acceptEncoding: h('accept-encoding'),
                userAgent: h('user-agent'),
                expect: h('expect'),
                bodyLength: data.length,
                bodySha: crypto.createHash('sha256').update(data).digest('hex').slice(0, 16),
            };
            const text = JSON.stringify(echo);
            res.sendDate = false;
            res.shouldKeepAlive = false;
            const headers = { 'Content-Type': 'application/json; charset=utf-8', 'X-Upstream': 'echo', 'Cache-Control': 'upstream' };
            let status = 200;
            if (mode.startsWith('status:')) status = parseInt(mode.slice(7), 10);
            if (mode === 'close') { req.socket.destroy(); return; }
            if (mode === 'cookies') headers['Set-Cookie'] = ['a=1; Path=/', 'b=2; Path=/'];
            const send = () => {
                if (mode === 'chunked') {
                    res.writeHead(status, headers);
                    res.write(text.slice(0, 10));
                    res.end(text.slice(10));
                    return;
                }
                headers['Content-Length'] = Buffer.byteLength(text);
                res.writeHead(status, headers);
                res.end(text);
            };
            if (mode.startsWith('slow:')) setTimeout(send, parseInt(mode.slice(5), 10));
            else send();
        });
    });
    return new Promise((resolve) => server.listen(port, '127.0.0.1', () => resolve(server)));
}

// makeCerts writes test certificates to dir with openssl: an RSA and an
// ECDSA certificate for localhost, a chain root > intermediate > leaf, a
// file that is not a certificate, and a key that does not belong to the RSA
// certificate. They exist only for the run.
function makeCerts(dir) {
    const run = (args) => execFileSync('openssl', args, { cwd: dir, stdio: 'pipe' });
    const subj = (cn) => ['-subj', `/CN=${cn}`];
    run(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'rsa.key', '-out', 'rsa.crt', '-days', '2', ...subj('localhost')]);
    run(['ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', 'ec.key']);
    run(['req', '-x509', '-key', 'ec.key', '-out', 'ec.crt', '-days', '2', ...subj('localhost')]);
    run(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'root.key', '-out', 'root.crt', '-days', '2', ...subj('Oracle Root')]);
    fs.writeFileSync(path.join(dir, 'ca.ext'), 'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\n');
    run(['req', '-new', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'int.key', '-out', 'int.csr', ...subj('Oracle Intermediate')]);
    run(['x509', '-req', '-in', 'int.csr', '-CA', 'root.crt', '-CAkey', 'root.key', '-CAcreateserial', '-out', 'int.crt', '-days', '2', '-extfile', 'ca.ext']);
    run(['req', '-new', '-newkey', 'rsa:2048', '-nodes', '-keyout', 'leaf.key', '-out', 'leaf.csr', ...subj('localhost')]);
    run(['x509', '-req', '-in', 'leaf.csr', '-CA', 'int.crt', '-CAkey', 'int.key', '-CAcreateserial', '-out', 'leaf.crt', '-days', '2']);
    fs.writeFileSync(path.join(dir, 'cabundle.crt'), Buffer.concat([fs.readFileSync(path.join(dir, 'int.crt')), fs.readFileSync(path.join(dir, 'root.crt'))]));
    fs.writeFileSync(path.join(dir, 'garbage.crt'), 'this is not a certificate\n');
    run(['genrsa', '-out', 'other.key', '2048']);
    return {
        rsa: { cert: path.join(dir, 'rsa.crt'), key: path.join(dir, 'rsa.key') },
        ec: { cert: path.join(dir, 'ec.crt'), key: path.join(dir, 'ec.key') },
        leaf: { cert: path.join(dir, 'leaf.crt'), key: path.join(dir, 'leaf.key'), ca: path.join(dir, 'cabundle.crt'), intermediate: path.join(dir, 'int.crt') },
        garbage: path.join(dir, 'garbage.crt'),
        otherKey: path.join(dir, 'other.key'),
        dir,
    };
}

// chainSent asks openssl which certificates the server sends.
function chainSent(port) {
    try {
        const out = execFileSync('openssl', ['s_client', '-connect', `127.0.0.1:${port}`, '-servername', 'localhost', '-showcerts'],
            { input: '', stdio: ['pipe', 'pipe', 'pipe'], timeout: 10000 }).toString();
        return [...out.matchAll(/^\s*\d+ s:(.*)$/gm)].map((m) => m[1].trim());
    } catch (e) {
        return [`error: ${e.message.split('\n')[0]}`];
    }
}

// tlsProbe reports whether a TLS handshake with these client options works.
function tlsProbe(port, options) {
    return new Promise((resolve) => {
        let sock;
        try {
            sock = tls.connect({ port, host: '127.0.0.1', rejectUnauthorized: false, servername: 'localhost', ...options });
        } catch (e) {
            // The client's OpenSSL cannot offer this at all
            resolve({ ok: false, error: `client: ${e.code || e.message}` });
            return;
        }
        const timer = setTimeout(() => { sock.destroy(); resolve({ ok: false, error: 'timeout' }); }, 5000);
        sock.once('secureConnect', () => {
            clearTimeout(timer);
            const info = { ok: true, protocol: sock.getProtocol(), cipher: sock.getCipher().standardName };
            sock.destroy();
            resolve(info);
        });
        sock.once('error', (e) => { clearTimeout(timer); resolve({ ok: false, error: e.code || e.message }); });
    });
}

// compactParts writes a long run of one byte in a part as {head, fill,
// byte, tail}, so large bodies do not bloat the recording.
function compactParts(parts) {
    return parts.map((p) => {
        if (p.data === undefined) return p;
        const buf = Buffer.from(p.data, 'base64');
        if (buf.length < 2048) return p;
        let best = { start: 0, len: 0 };
        for (let i = 0; i < buf.length;) {
            let j = i;
            while (j < buf.length && buf[j] === buf[i]) j++;
            if (j - i > best.len) best = { start: i, len: j - i };
            i = j;
        }
        if (best.len < 1024) return p;
        return {
            delay: p.delay || 0,
            head: b64(buf.subarray(0, best.start)),
            fill: best.len,
            byte: buf[best.start],
            tail: b64(buf.subarray(best.start + best.len)),
        };
    });
}

module.exports = { compactParts, request, exchange, startNode, startUpstream, makeCerts, chainSent, tlsProbe, logLines, portOpen, sleep, b64 };
