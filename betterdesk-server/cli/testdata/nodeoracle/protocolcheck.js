'use strict';

// Records what scripts/installer-protocol-check.js does, into
// ../protocolcheck.json.gz (replayed by cli/check_test.go):
//   - parseArgs for many argument lists,
//   - checkCertificateHostname and Node's tls.checkServerIdentity for many
//     names and hosts,
//   - whole runs of the script against servers started here: HTTP
//     answers, redirects, HTTPS with the certificates of ../certs (the test
//     CA is trusted through NODE_EXTRA_CA_CERTS), closed and open ports.
// Addresses in the runs are written as {NAME} placeholders.
//
//   node protocolcheck.js   (in the dev image)

const fs = require('fs');
const http = require('http');
const https = require('https');
const net = require('net');
const path = require('path');
const tls = require('tls');
const { spawn } = require('child_process');
const { writeVectors } = require('./lib');

// runAsync is lib.run without blocking: the servers of the runs live in
// this process and must answer while the script runs.
function runAsync(script, args, { cwd, env }) {
    return new Promise((resolve, reject) => {
        const child = spawn(process.execPath, [script, ...args], { cwd, env: { PATH: process.env.PATH, ...env } });
        const out = []; const err = [];
        child.stdout.on('data', (d) => out.push(d));
        child.stderr.on('data', (d) => err.push(d));
        child.on('error', reject);
        child.on('close', (status) => resolve({ status, stdout: Buffer.concat(out).toString('utf8'), stderr: Buffer.concat(err).toString('utf8') }));
    });
}

const SCRIPT = '/repo/scripts/installer-protocol-check.js';
const CERTS = path.join(__dirname, '..', 'certs');
const { parseArgs, checkCertificateHostname } = require(SCRIPT);

const out = { parseArgs: [], hostname: [], identity: [], runs: [] };

// parseArgs
const values = ['http://x/a', '', '5', '250', '249', '1e3', '0x200', ' 300 ', 'abc', '127.0.0.1:21116', '--insecure', '-h', 'Infinity', '300.5', '-300'];
const flags = ['--api-url', '--panel-url', '--proxy-url', '--port', '--timeout-ms', '--insecure', '--help', '-h', '--api', '--bogus', 'positional'];
let seed = 7;
const rnd = (n) => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
const argLists = [[], ['--api-url'], ['--port'], ['--timeout-ms'], ['--insecure'], ['--help'], ['--timeout-ms', '100', '--help'], ['--help', '--bogus']];
for (let i = 0; i < 1500; i++) {
    const n = 1 + rnd(5);
    const args = [];
    for (let j = 0; j < n; j++) {
        args.push(flags[rnd(flags.length)]);
        if (rnd(3) > 0) args.push(values[rnd(values.length)]);
    }
    argLists.push(args);
}
for (const args of argLists) {
    try {
        const o = parseArgs(args);
        out.parseArgs.push({
            args,
            apiUrl: o.apiUrl === undefined ? null : o.apiUrl, panelUrl: o.panelUrl === undefined ? null : o.panelUrl,
            proxyUrl: o.proxyUrl === undefined ? null : o.proxyUrl,
            ports: o.ports.map((p) => (p === undefined ? null : p)), timeoutMs: o.timeoutMs, insecure: o.insecure, help: !!o.help,
        });
    } catch (err) {
        out.parseArgs.push({ args, error: err.message });
    }
}

// checkCertificateHostname and tls.checkServerIdentity
const sans = [
    '', 'DNS:localhost', 'DNS:localhost, IP Address:127.0.0.1', 'IP Address:127.0.0.1', 'DNS:*.example.test',
    'DNS:panel.example.test, DNS:*.panel.example.test', 'DNS:a.b.example.test', 'DNS:PANEL.Example.Test', 'DNS:*example.test',
    'DNS:*.test', 'DNS:x*.example.test', 'DNS:*x.example.test', 'DNS:f*o.example.test', 'DNS:*.*.example.test', 'DNS:xn--*.example.test',
    'IP Address:0:0:0:0:0:0:0:1', 'IP Address:10.0.0.1, IP Address:192.0.2.10', 'DNS:example.test.', 'email:a@b.test, DNS:mail.test',
    'URI:https://x.test, DNS:x.test', 'DNS:localhost, IP Address:0:0:0:0:0:FFFF:7F00:1',
    'DNS:ab*ba.example.test', 'DNS:*xn--a.example.test', 'DNS:ä.example.test', 'IP Address:0:0:0:0:0:0:0:1',
];
const hosts = ['localhost', 'LOCALHOST', '127.0.0.1', '::1', '[::1]', 'example.test', 'a.example.test', 'b.a.example.test',
    'panel.example.test', 'x.panel.example.test', 'panel.example.test.', 'fxo.example.test', 'fo.example.test', 'xyz.example.test',
    'zx.example.test', 'xn--abc.example.test', '192.0.2.10', '10.0.0.1', '10.0.0.2', 'a.test', 'test', '', 'mail.test', 'x.test',
    '::ffff:127.0.0.1', 'example.test..', '0127.0.0.1',
    'aba.example.test', 'abba.example.test', 'bxn--a.example.test', 'ä.example.test', '08.0.0.1', '0x7f.1', '127.1',
    '0400.0.0.1', '1.2.3.4.5', '0x100.0.0.1'];
const cns = [undefined, 'localhost', '*.example.test', 'example.test'];
for (const san of sans) {
    for (const host of hosts) {
        out.hostname.push({ san, host, match: checkCertificateHostname({ subjectaltname: san }, host) });
        for (const cn of cns) {
            const cert = { subjectaltname: san === '' ? undefined : san, subject: cn === undefined ? {} : { CN: cn } };
            const err = tls.checkServerIdentity(host, cert);
            out.identity.push({ san, host, cn: cn === undefined ? null : cn, error: err ? err.message : null });
        }
    }
}

// Whole runs
function listen(server, host = '127.0.0.1') {
    return new Promise((resolve) => server.listen(0, host, () => resolve(server.address().port)));
}
function pem(name) { return fs.readFileSync(path.join(CERTS, name + '.pem')); }

async function runs() {
    // A name with two addresses, in this throwaway container: Node tries
    // each and gives an error without a message when all fail
    fs.appendFileSync('/etc/hosts', '127.0.0.2 multi.test\n127.0.0.3 multi.test\n');
    const servers = [];
    const ports = {};
    const handler = (req, res) => {
        switch (req.url) {
            case '/redirect': res.writeHead(302, { Location: '/health' }); res.end(); return;
            case '/redirect-nolocation': res.writeHead(301); res.end(); return;
            case '/error': res.writeHead(500); res.end('no'); return;
            case '/notfound': res.writeHead(404); res.end('no'); return;
            case '/created': res.writeHead(201); res.end(); return;
            case '/nocontent': res.writeHead(204); res.end(); return;
            case '/hang': return;
            case '/close': req.socket.destroy(); return;
            default: res.writeHead(200, { 'Content-Type': 'application/json' }); res.end('{"status":"ok"}');
        }
    };
    const plain = http.createServer(handler);
    ports.HTTP = await listen(plain);
    servers.push(plain);
    for (const name of ['good', 'wronghost', 'expired', 'self', 'cnonly']) {
        const s = https.createServer({ cert: pem(name), key: pem(name + '-key') }, handler);
        ports[name.toUpperCase()] = await listen(s);
        servers.push(s);
    }
    const tcp = net.createServer((socket) => socket.end());
    ports.TCP = await listen(tcp);
    servers.push(tcp);
    const closer = net.createServer();
    ports.CLOSED = await listen(closer);
    await new Promise((resolve) => closer.close(resolve));
    const garbage = net.createServer((socket) => socket.end('NOT HTTP\r\n\r\n'));
    ports.GARBAGE = await listen(garbage);
    servers.push(garbage);

    const fill = (s) => s.replace(/\{([A-Z]+)\}/g, (_, k) => String(ports[k]));
    const hide = (s) => {
        let o = s.split(`0x${ports.TCPHEX}`).join('0x{TCPHEX}');
        for (const [k, v] of Object.entries(ports)) {
            if (k !== 'TCPHEX') o = o.split(`:${v}`).join(`:{${k}}`).split(`: ${v} `).join(': {' + k + '} ').split(` ${v}\n`).join(` {${k}}\n`);
        }
        return o;
    };
    const cases = [
        ['--api-url', 'http://127.0.0.1:{HTTP}/api/health', '--panel-url', 'http://127.0.0.1:{HTTP}/health'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/redirect', '--panel-url', 'http://127.0.0.1:{HTTP}/redirect-nolocation'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/error', '--panel-url', 'http://127.0.0.1:{HTTP}/notfound', '--proxy-url', 'http://127.0.0.1:{HTTP}/created'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/nocontent', '--panel-url', 'http://127.0.0.1:{CLOSED}/health'],
        ['--api-url', 'http://localhost:{HTTP}/health', '--panel-url', 'http://localhost:{CLOSED}/health'],
        ['--api-url', 'http://LOCALHOST:{HTTP}/health', '--panel-url', ''],
        ['--api-url', 'http://127.0.0.1:{HTTP}/hang', '--panel-url', 'http://127.0.0.1:{HTTP}/health', '--timeout-ms', '400'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/close', '--panel-url', 'http://127.0.0.1:{GARBAGE}/health'],
        ['--api-url', 'http://no-such-host.invalid/health', '--panel-url', 'http://127.0.0.1:{HTTP}/health'],
        ['--api-url', 'not a url', '--panel-url', 'http://127.0.0.1:{HTTP}/health'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/health', '--panel-url', 'localhost:{HTTP}'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/health', '--panel-url', 'ftp://127.0.0.1:{HTTP}/'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/health', '--panel-url', 'http://'],
        ['--api-url', 'https://localhost:{GOOD}/health', '--panel-url', 'https://127.0.0.1:{GOOD}/health'],
        ['--api-url', 'https://localhost:{GOOD}/redirect', '--panel-url', 'https://LocalHost:{GOOD}/health'],
        ['--api-url', 'https://localhost:{WRONGHOST}/health', '--panel-url', 'https://127.0.0.1:{WRONGHOST}/health'],
        ['--api-url', 'https://localhost:{EXPIRED}/health', '--panel-url', 'https://localhost:{SELF}/health'],
        ['--api-url', 'https://localhost:{CNONLY}/health', '--panel-url', 'https://127.0.0.1:{CNONLY}/health'],
        ['--api-url', 'https://localhost:{WRONGHOST}/health', '--panel-url', 'https://localhost:{SELF}/health', '--insecure'],
        ['--api-url', 'https://localhost:{EXPIRED}/error', '--panel-url', 'https://localhost:{CNONLY}/health', '--insecure'],
        ['--api-url', 'https://127.0.0.1:{HTTP}/health', '--panel-url', 'http://127.0.0.1:{GOOD}/health'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/health', '--panel-url', 'http://127.0.0.1:{HTTP}/health', '--port', '127.0.0.1:{TCP}', '--port', '{TCP}', '--port', '127.0.0.1:{CLOSED}'],
        ['--api-url', '', '--panel-url', '', '--port', 'localhost:{TCP}', '--port', 'localhost:{CLOSED}'],
        ['--api-url', '', '--panel-url', '', '--port', 'no-such-host.invalid:{TCP}'],
        ['--api-url', '', '--panel-url', '', '--port', 'abc'],
        ['--api-url', '', '--panel-url', '', '--port', '127.0.0.1:'],
        ['--api-url', '', '--panel-url', '', '--port', '127.0.0.1:70000'],
        ['--api-url', '', '--panel-url', '', '--port', '127.0.0.1:1.5'],
        ['--api-url', '', '--panel-url', '', '--port', '127.0.0.1:{TCP}', '--port'],
        ['--api-url', '', '--panel-url', '', '--port', ':{TCP}'],
        ['--api-url', '', '--panel-url', '', '--port', '127.0.0.1: {TCP} '],
        ['--api-url', '', '--panel-url', '', '--port', '127.0.0.1:0x{TCPHEX}'],
        ['--api-url', '', '--panel-url'],
        ['--api-url', 'http://127.0.0.1:{HTTP}/health'],
        ['--bogus'],
        ['--timeout-ms', '100'],
        ['--help'],
        ['--api-url', '', '--panel-url', '', '--port', '::1:{TCP}'],
        ['--api-url', 'http://[::1]:{CLOSED}/health', '--panel-url', ''],
        ['--api-url', 'http://multi.test:{CLOSED}/health', '--panel-url', '', '--port', 'multi.test:{CLOSED}'],
        ['--api-url', 'https://localhost:{WRONGHOST}/health', '--panel-url', 'https://[::1]:{WRONGHOST}/health'],
    ];
    ports.TCPHEX = ports.TCP.toString(16);
    for (const args of cases) {
        const res = await runAsync(SCRIPT, args.map(fill), { cwd: '/tmp', env: { NODE_EXTRA_CA_CERTS: path.join(CERTS, 'ca.pem') } });
        out.runs.push({ args, status: res.status, stdout: hide(res.stdout), stderr: hide(res.stderr) });
    }
    for (const s of servers) s.close();
}

runs().then(() => {
    writeVectors('protocolcheck', out);
    console.log(`parseArgs ${out.parseArgs.length}, hostname ${out.hostname.length}, identity ${out.identity.length}, runs ${out.runs.length}`);
    process.exit(0);
}).catch((err) => { console.error(err); process.exit(1); });
