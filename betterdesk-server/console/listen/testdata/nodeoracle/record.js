'use strict';

// Records what the Node console's listeners answer (server.js, before its
// client API listener was removed): the client API port in both of its
// modes and with TLS, the console on HTTPS with its redirect listener, the
// fallback when certificates are missing, start failures, and slow clients.
// Everything goes over real sockets to the real server.js. See README.md.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const net = require('net');
const http = require('http');
const { compactParts, request, exchange, startNode, startUpstream, makeCerts, chainSent, tlsProbe, logLines, portOpen, sleep, b64 } = require('./lib');

const OUT = process.argv[2] || path.join(__dirname, '..', 'node_listeners.json');
const PORT = 25080;
const HTTPS_PORT = 25443;
const API_PORT = 25121;
const UPSTREAM_PORT = 25114;
const ELSEWHERE_PORT = 25115;

// The client API port's path list (middleware/wanSecurity.js)
const METHODS = {
    '/api/login': 'POST', '/api/logout': 'POST', '/api/currentUser': ['GET', 'POST'], '/api/login-options': 'GET',
    '/api/oidc/auth': 'POST', '/api/oidc/auth-query': 'GET', '/api/oidc/callback': 'GET', '/api/auth/oidc/callback': 'GET',
    '/api/heartbeat': 'POST', '/api/sysinfo': 'POST', '/api/sysinfo_ver': 'POST', '/api/telemetry/key': 'GET',
    '/api/branding': 'GET', '/api/devices/register': 'POST', '/api/devices/register/status': 'GET',
    '/api/pairing/redeem': 'POST', '/api/pairing/config': 'POST', '/api/peers': 'GET', '/api/audit': 'GET',
    '/api/audit/conn': '*', '/api/audit/file': '*', '/api/audit/alarm': '*', '/api/device-group': '*',
    '/api/device-group/accessible': 'GET', '/api/user/group': 'GET', '/api/user-groups': '*', '/api/strategies': '*',
    '/api/strategies/assign': 'POST', '/api/devices': 'GET', '/api/ab': '*', '/api/ab/personal': '*', '/api/ab/tags': 'GET',
    '/api/users': 'GET', '/api/software': 'GET', '/api/software/client-download-link': 'GET', '/api/health': 'GET',
    '/api/server/pubkey': 'GET', '/api/server-key': 'GET', '/api/server-key/fingerprint': 'GET', '/api/group': '*',
    '/api/group/get': '*', '/api/peers/list': '*', '/api/bd/register-request': 'POST', '/api/bd/register-status': 'GET',
};
const LIMITS = {
    '/api/login': 4096, '/api/heartbeat': 8192, '/api/oidc/auth': 4096, '/api/sysinfo': 8192, '/api/sysinfo_ver': 512,
    '/api/pairing/redeem': 4096, '/api/pairing/config': 512, '/api/ab': 524288, '/api/ab/personal': 524288,
    '/api/audit/conn': 2048, '/api/audit/file': 4096, '/api/audit/alarm': 2048, '/api/device-group': 2048,
    '/api/user-groups': 2048, '/api/strategies': 4096, '/api/group': 2048, '/api/group/get': 2048, '/api/peers/list': 2048,
};
const UUID = '0123abcd-0123-4567-89ab-0123456789ab';

let ipCounter = 0;
function nextIP() {
    ipCounter += 1;
    return `10.${(ipCounter >> 16) & 255}.${(ipCounter >> 8) & 255}.${ipCounter & 255}`;
}

// A case is sent as parts; a part is {data} or {head, fill, tail} (fill
// bytes "a" between), each after its delay in ms.
function part(buf, delay = 0) {
    return { data: b64(buf), delay };
}
function materialize(parts) {
    return parts.map((p) => ({
        delay: p.delay || 0,
        data: p.fill !== undefined
            ? Buffer.concat([Buffer.from(p.head, 'base64'), Buffer.alloc(p.fill, p.byte), Buffer.from(p.tail || '', 'base64')])
            : Buffer.from(p.data, 'base64'),
    }));
}

// jsonOfLength is a JSON object exactly n bytes long.
function jsonOfLength(n) {
    const fill = Math.max(0, n - 8);
    return `{"a":"${'x'.repeat(fill)}"}`;
}

function apiCase(id, method, target, { headers = [], body = null, ip = nextIP(), extra = [], opts = {} } = {}) {
    const hs = [];
    if (ip) hs.push(['X-Forwarded-For', ip]);
    hs.push(['X-Oracle', '1']);
    if (body !== null && !headers.some(([k]) => k.toLowerCase() === 'content-type')) hs.push(['Content-Type', 'application/json']);
    return { id, listener: 'api', parts: [part(request(method, target, [...hs, ...headers], body, opts))], ...extra };
}

function methodCases(prefix) {
    const cases = [];
    const methods = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'];
    for (const p of Object.keys(METHODS)) {
        if (p.startsWith('/api/bd/')) continue;
        for (const m of methods) {
            const body = ['POST', 'PUT', 'PATCH', 'DELETE'].includes(m) ? '{}' : null;
            cases.push(apiCase(`${prefix}method ${m} ${p}`, m, p, { body }));
        }
    }
    const patterns = [
        '/api/peer-key/abc_-9', `/api/peer-key/${'a'.repeat(32)}`, `/api/peer-key/${'a'.repeat(33)}`, '/api/peer-key/a.b',
        '/api/peer-key/%41', '/api/peer-key/', `/api/strategies/${UUID}`, `/api/strategies/${UUID.toUpperCase()}`,
        `/api/strategies/${UUID}/status`, `/api/strategies/${UUID.slice(1)}`, `/api/strategies/${UUID}0`,
        `/api/strategies/${'-'.repeat(36)}`, `/api/devices/${UUID}/assign`, `/api/devices/${UUID}/assign/`,
    ];
    for (const p of patterns) {
        for (const m of ['GET', 'POST', 'HEAD', 'OPTIONS', 'DELETE', 'PUT']) {
            cases.push(apiCase(`${prefix}pattern ${m} ${p}`, m, p, { body: ['POST', 'PUT', 'DELETE'].includes(m) ? '{}' : null }));
        }
    }
    const others = [
        '/', '/api', '/api/', '/api/login/', '/API/login', '/api/Login', '/api//login', '//api/login', '/api/./login',
        '/api/x/../login', '/api/%6Cogin', '/api/login%2F', '/api/login;x', '/api/login?x=1', '/health', '/api/health/',
        '/api/bd/register-request/', '/api/bd/../login', '/api/bd/REGISTER-REQUEST', '/api/BD/register-request',
        '/api/bd//register-request', '/api/bd/register-request%00', '/api/bd/register-status/..', '/api/bd/api/registrations',
        '/api/registrations', '/api/strategies/x', '/api/peers/list/', '/api/audit/', '/api/ab/tags/x', '/favicon.ico',
        '/api/health%20', '/api/heal%74h', '/api/health/.', '/api/login%3Fx', '/api/LOGIN',
    ];
    for (const p of others) {
        for (const m of ['GET', 'POST', 'OPTIONS']) {
            cases.push(apiCase(`${prefix}other ${m} ${p}`, m, p, { body: m === 'POST' ? '{}' : null }));
        }
    }
    return cases;
}

function limitCases(prefix) {
    const cases = [];
    for (const [p, limit] of Object.entries(LIMITS)) {
        for (const n of [limit, limit + 1]) {
            const body = jsonOfLength(n);
            cases.push(apiCase(`${prefix}size ${n} POST ${p}`, 'POST', p, { body }));
        }
    }
    for (const p of ['/api/logout', '/api/currentUser', '/api/devices/register', '/api/bd/register-request']) {
        for (const n of [1024, 1025]) cases.push(apiCase(`${prefix}size ${n} POST ${p}`, 'POST', p, { body: jsonOfLength(n) }));
    }
    for (const p of ['/api/audit/conn', '/api/ab', '/api/strategies']) {
        cases.push(apiCase(`${prefix}size over PUT ${p}`, 'PUT', p, { body: jsonOfLength(LIMITS[p] + 1) }));
        cases.push(apiCase(`${prefix}size over DELETE ${p}`, 'DELETE', p, { body: jsonOfLength(LIMITS[p] + 1) }));
    }
    // A body on a path that takes GET only, and a body far over the limit
    cases.push(apiCase(`${prefix}size GET with body /api/health`, 'GET', '/api/health', { body: jsonOfLength(2000) }));
    cases.push(apiCase(`${prefix}size far over POST /api/login`, 'POST', '/api/login', { body: jsonOfLength(100000) }));
    // Chunked bodies at and over the limit
    for (const n of [4096, 4097]) {
        const body = jsonOfLength(n);
        const chunked = `${Buffer.byteLength(body).toString(16)}\r\n${body}\r\n0\r\n\r\n`;
        cases.push(apiCase(`${prefix}size chunked ${n} POST /api/login`, 'POST', '/api/login',
            { headers: [['Transfer-Encoding', 'chunked'], ['Content-Type', 'application/json']], body: chunked }));
    }
    // Content types on POST
    for (const ct of [null, 'text/plain', 'application/jsonx', 'text/x; a=application/json', 'APPLICATION/JSON',
        'application/json; charset=latin1', 'application/json; charset=utf-16', 'application/vnd.api+json', '']) {
        const headers = ct === null ? [] : [['Content-Type', ct]];
        const c = apiCase(`${prefix}type ${JSON.stringify(ct)} POST /api/login`, 'POST', '/api/login', { headers, body: '{"a":1}' });
        if (ct === null) {
            c.parts = [part(request('POST', '/api/login', [['X-Forwarded-For', nextIP()], ['X-Oracle', '1']], '{"a":1}'))];
        }
        cases.push(c);
    }
    return cases;
}

function limiterCases(prefix) {
    const cases = [];
    const seq = (name, list) => list.forEach((c, i) => { c.id = `${prefix}limiter ${name} #${i + 1} ${c.id}`; cases.push(c); });
    let ip = nextIP();
    seq('login', Array.from({ length: 7 }, () => apiCase('POST /api/login', 'POST', '/api/login', { ip, body: '{}' })));
    ip = nextIP();
    seq('login and logout', [
        ...Array.from({ length: 3 }, () => apiCase('POST /api/login', 'POST', '/api/login', { ip, body: '{}' })),
        ...Array.from({ length: 3 }, () => apiCase('POST /api/logout', 'POST', '/api/logout', { ip, body: '{}' })),
        apiCase('GET /api/currentUser', 'GET', '/api/currentUser', { ip }),
    ]);
    ip = nextIP();
    seq('global', Array.from({ length: 62 }, () => apiCase('GET /api/health', 'GET', '/api/health', { ip })));
    ip = nextIP();
    seq('audit', [
        ...Array.from({ length: 21 }, () => apiCase('POST /api/audit/conn', 'POST', '/api/audit/conn', { ip, body: '{}' })),
        apiCase('PUT /api/audit/conn', 'PUT', '/api/audit/conn', { ip, body: '{}' }),
        apiCase('GET /api/audit/conn', 'GET', '/api/audit/conn', { ip }),
    ]);
    ip = nextIP();
    seq('not counted', [
        ...Array.from({ length: 5 }, () => apiCase('GET /api/unknown', 'GET', '/api/unknown', { ip })),
        ...Array.from({ length: 3 }, () => apiCase('GET /api/login', 'GET', '/api/login', { ip })),
        ...Array.from({ length: 6 }, () => apiCase('POST /api/login', 'POST', '/api/login', { ip, body: '{}' })),
    ]);
    ip = nextIP();
    seq('preflight', Array.from({ length: 64 }, () => apiCase('OPTIONS /api/login', 'OPTIONS', '/api/login', { ip })));
    ip = nextIP();
    seq('oversized counts', [
        ...Array.from({ length: 5 }, () => apiCase('POST /api/login too large', 'POST', '/api/login', { ip, body: jsonOfLength(5000) })),
        apiCase('POST /api/login', 'POST', '/api/login', { ip, body: '{}' }),
    ]);
    return cases;
}

function headerCases(prefix) {
    const cases = [];
    const raw = (id, text) => ({ id: `${prefix}${id}`, listener: 'api', parts: [part(Buffer.from(text, 'latin1'))] });
    const ip = () => nextIP();
    cases.push(apiCase(`${prefix}fwd authorization and cookie`, 'GET', '/api/currentUser', { headers: [['Authorization', 'Bearer abc'], ['Cookie', 'a=b; c=d']] }));
    cases.push(apiCase(`${prefix}fwd x-real-ip alone`, 'GET', '/api/health', { ip: null, headers: [['X-Real-IP', '198.51.100.9']] }));
    cases.push(apiCase(`${prefix}fwd no address headers`, 'GET', '/api/health', { ip: null }));
    cases.push(apiCase(`${prefix}fwd xff list`, 'GET', '/api/health', { ip: '192.0.2.1, 192.0.2.2' }));
    cases.push(apiCase(`${prefix}fwd xff twice`, 'GET', '/api/health', { ip: '192.0.2.3', headers: [['X-Forwarded-For', '192.0.2.4']] }));
    cases.push(apiCase(`${prefix}fwd xff empty first`, 'GET', '/api/health', { ip: ', 192.0.2.5' }));
    cases.push(apiCase(`${prefix}fwd xff and x-real-ip`, 'GET', '/api/health', { ip: '192.0.2.6', headers: [['X-Real-IP', '198.51.100.10']] }));
    cases.push(apiCase(`${prefix}fwd forwarded host and proto`, 'GET', '/api/health', { headers: [['X-Forwarded-Host', 'evil.example'], ['X-Forwarded-Proto', 'https'], ['Forwarded', 'for=1.2.3.4']] }));
    cases.push(apiCase(`${prefix}fwd connection names a header`, 'GET', '/api/health', { headers: [['Connection', 'close, X-Hop'], ['X-Hop', 'yes']] }));
    cases.push(apiCase(`${prefix}fwd hop by hop`, 'GET', '/api/health', { headers: [['TE', 'trailers'], ['Proxy-Authorization', 'Basic eA=='], ['Keep-Alive', '5']] }));
    cases.push(apiCase(`${prefix}fwd upgrade`, 'GET', '/api/health', { headers: [['Connection', 'Upgrade'], ['Upgrade', 'websocket'], ['Sec-WebSocket-Key', 'dGhlIHNhbXBsZSBub25jZQ=='], ['Sec-WebSocket-Version', '13']] }));
    cases.push(apiCase(`${prefix}fwd accept-encoding and agent`, 'GET', '/api/health', { headers: [['Accept-Encoding', 'gzip, br'], ['User-Agent', 'oracle-agent/1']] }));
    cases.push(apiCase(`${prefix}fwd post body`, 'POST', '/api/heartbeat', { body: '{"id":"123","uuid":"x"}' }));
    cases.push(apiCase(`${prefix}fwd malformed json body`, 'POST', '/api/heartbeat', { body: '{not json' }));
    const chunked = '7\r\n{"a":1}\r\n0\r\n\r\n';
    cases.push(apiCase(`${prefix}fwd chunked body`, 'POST', '/api/heartbeat', { headers: [['Transfer-Encoding', 'chunked'], ['Content-Type', 'application/json']], body: chunked }));
    cases.push(apiCase(`${prefix}fwd query`, 'GET', '/api/health?a=1&b=%41&c=%zz&d=a+b&e=', {}));
    cases.push(apiCase(`${prefix}fwd query odd characters`, 'GET', `/api/health?a=<>"'\`{}|\\^`, {}));
    cases.push(apiCase(`${prefix}fwd query fragment`, 'GET', '/api/health?a=1#frag', {}));
    cases.push(apiCase(`${prefix}fwd fragment`, 'GET', '/api/health#frag', {}));
    cases.push(apiCase(`${prefix}fwd query utf8`, 'GET', '/api/health?q=Ã¤', {}));
    cases.push(apiCase(`${prefix}fwd only question mark`, 'GET', '/api/health?', {}));
    const expect = request('POST', '/api/heartbeat', [['X-Forwarded-For', ip()], ['X-Oracle', '1'], ['Content-Type', 'application/json'], ['Expect', '100-continue'], ['Content-Length', '7']], null);
    cases.push({ id: `${prefix}fwd expect continue`, listener: 'api', parts: [part(expect), part(Buffer.from('{"a":1}'), 400)] });
    const expect404 = request('POST', '/api/nope', [['X-Forwarded-For', ip()], ['X-Oracle', '1'], ['Content-Type', 'application/json'], ['Expect', '100-continue'], ['Content-Length', '7']], null);
    cases.push({ id: `${prefix}fwd expect continue 404`, listener: 'api', parts: [part(expect404), part(Buffer.from('{"a":1}'), 400)] });
    for (const mode of ['status:500', 'status:204', 'status:304', 'status:404', 'chunked', 'cookies', 'close']) {
        cases.push(apiCase(`${prefix}upstream ${mode}`, 'GET', `/api/health?upstream=${mode}`, {}));
    }
    cases.push(apiCase(`${prefix}upstream status:302 post`, 'POST', '/api/login?upstream=status:302', { body: '{}' }));
    cases.push(raw('absolute form elsewhere', `GET http://127.0.0.1:${ELSEWHERE_PORT}/api/health HTTP/1.1\r\nHost: oracle.test\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\nConnection: close\r\n\r\n`));
    cases.push(raw('absolute form upstream', `GET http://127.0.0.1:${UPSTREAM_PORT}/api/health?x=1 HTTP/1.1\r\nHost: oracle.test\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\nConnection: close\r\n\r\n`));
    cases.push(raw('options star', `OPTIONS * HTTP/1.1\r\nHost: oracle.test\r\nX-Forwarded-For: ${ip()}\r\nConnection: close\r\n\r\n`));
    cases.push(raw('get star', `GET * HTTP/1.1\r\nHost: oracle.test\r\nX-Forwarded-For: ${ip()}\r\nConnection: close\r\n\r\n`));
    cases.push(raw('connect', `CONNECT oracle.test:443 HTTP/1.1\r\nHost: oracle.test:443\r\nX-Forwarded-For: ${ip()}\r\n\r\n`));
    cases.push(raw('http 1.0 without host', `GET /api/health HTTP/1.0\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\n\r\n`));
    cases.push(raw('http 1.0 keep-alive', `GET /api/health HTTP/1.0\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\nConnection: keep-alive\r\n\r\n`));
    cases.push(raw('malformed header line', `GET /api/health HTTP/1.1\r\nHost: oracle.test\r\nBad Header\r\nConnection: close\r\n\r\n`));
    cases.push(raw('two content lengths', `POST /api/heartbeat HTTP/1.1\r\nHost: oracle.test\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\nContent-Type: application/json\r\nContent-Length: 2\r\nContent-Length: 3\r\nConnection: close\r\n\r\n{}`));
    cases.push(raw('length and chunked', `POST /api/heartbeat HTTP/1.1\r\nHost: oracle.test\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\nContent-Type: application/json\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n2\r\n{}\r\n0\r\n\r\n`));
    cases.push(raw('two hosts', `GET /api/health HTTP/1.1\r\nHost: a.test\r\nHost: b.test\r\nX-Forwarded-For: ${ip()}\r\nX-Oracle: 1\r\nConnection: close\r\n\r\n`));
    cases.push(raw('bad method token', `G@T /api/health HTTP/1.1\r\nHost: oracle.test\r\nConnection: close\r\n\r\n`));
    cases.push(raw('http 2 preface', 'PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n'));
    cases.push(raw('not http', 'hello\r\n\r\n'));
    for (const size of [15000, 17000, 70000]) {
        cases.push(apiCase(`${prefix}header size ${size}`, 'GET', '/api/health', { headers: [['X-Big', 'b'.repeat(size)]] }));
    }
    cases.push(apiCase(`${prefix}many headers`, 'GET', '/api/health', { headers: Array.from({ length: 2100 }, (_, i) => [`X-H${i}`, '1']) }));
    // Two requests on one connection
    const first = request('GET', '/api/health', [['X-Forwarded-For', ip()], ['X-Oracle', '1']], null, { close: false });
    const second = request('GET', '/api/nope', [['X-Forwarded-For', ip()], ['X-Oracle', '1']], null);
    cases.push({ id: `${prefix}pipelined`, listener: 'api', parts: [part(Buffer.concat([first, second]))] });
    return cases;
}

function registrationCases(prefix) {
    const cases = [];
    const body = JSON.stringify({ device_id: 'ORACLE01', hostname: 'oracle-host', platform: 'linux', version: '1.0', uuid: 'u-1' });
    cases.push(apiCase(`${prefix}registration request`, 'POST', '/api/bd/register-request', { body }));
    cases.push(apiCase(`${prefix}registration request again`, 'POST', '/api/bd/register-request', { body }));
    cases.push(apiCase(`${prefix}registration status`, 'GET', '/api/bd/register-status?device_id=ORACLE01', {}));
    cases.push(apiCase(`${prefix}registration status unknown`, 'GET', '/api/bd/register-status?device_id=NOPE99', {}));
    cases.push(apiCase(`${prefix}registration status without id`, 'GET', '/api/bd/register-status', {}));
    cases.push(apiCase(`${prefix}registration status two ids`, 'GET', '/api/bd/register-status?device_id=A&device_id=B', {}));
    cases.push(apiCase(`${prefix}registration request short id`, 'POST', '/api/bd/register-request', { body: '{"device_id":"ab"}' }));
    cases.push(apiCase(`${prefix}registration request array`, 'POST', '/api/bd/register-request', { body: '[]' }));
    cases.push(apiCase(`${prefix}registration request malformed`, 'POST', '/api/bd/register-request', { body: '{x' }));
    cases.push(apiCase(`${prefix}registration request text`, 'POST', '/api/bd/register-request', { headers: [['Content-Type', 'text/plain']], body }));
    cases.push(apiCase(`${prefix}registration request get`, 'GET', '/api/bd/register-request', {}));
    cases.push(apiCase(`${prefix}registration status post`, 'POST', '/api/bd/register-status', { body: '{}' }));
    cases.push(apiCase(`${prefix}registration request ip from x-real-ip`, 'POST', '/api/bd/register-request', { ip: null, headers: [['X-Real-IP', '198.51.100.20']], body: JSON.stringify({ device_id: 'ORACLE02' }) }));
    cases.push(apiCase(`${prefix}registration status 2`, 'GET', '/api/bd/register-status?device_id=ORACLE02', {}));
    return cases;
}

function jsonGateCases(prefix) {
    const cases = [];
    const bodies = ['{"a":1}', '[]', '', '  ', 'null', '1', '"x"', 'true', '{bad', '{"a":1}x', '﻿{}', '{"a":"\\ud800"}',
        ' {"a":1} ', '{"a":1}\n', '{"__proto__":{"x":1}}', '[1,2', '{"a":1,}', "{'a':1}", '{"a":NaN}', '{"a":1e999}'];
    for (const [i, body] of bodies.entries()) {
        for (const p of ['/api/login', '/api/ab']) {
            cases.push(apiCase(`${prefix}json body ${i} ${JSON.stringify(body)} POST ${p}`, 'POST', p, { body }));
        }
    }
    for (const ct of ['application/json; charset=utf-8', 'application/json; charset=UTF-8', 'application/json; charset=latin1',
        'application/json; charset=foo', 'application/json; charset="utf-8"', 'application/json;charset=utf-16le',
        'application/vnd.api+json', 'application/json-patch+json', 'application/json, text/plain']) {
        cases.push(apiCase(`${prefix}json type ${ct} POST /api/login`, 'POST', '/api/login', { headers: [['Content-Type', ct]], body: '{"a":1}' }));
    }
    const utf16 = Buffer.from('﻿{"a":1}', 'utf16le');
    cases.push({ id: `${prefix}json utf-16 body`, listener: 'api', parts: [part(request('POST', '/api/login', [['X-Forwarded-For', nextIP()], ['X-Oracle', '1'], ['Content-Type', 'application/json; charset=utf-16']], utf16))] });
    const gz = zlib.gzipSync(Buffer.from('{"a":1}'));
    const df = zlib.deflateSync(Buffer.from('{"a":1}'));
    const bomb = zlib.gzipSync(Buffer.from(jsonOfLength(70000)));
    for (const [name, enc, body] of [['gzip', 'gzip', gz], ['deflate', 'deflate', df], ['broken gzip', 'gzip', Buffer.from('nogzip')],
        ['br', 'br', Buffer.from('{}')], ['identity', 'identity', Buffer.from('{"a":1}')], ['GZIP', 'GZIP', gz], ['gzip big', 'gzip', bomb]]) {
        for (const p of ['/api/login', '/api/ab']) {
            cases.push({ id: `${prefix}json encoding ${name} POST ${p}`, listener: 'api', parts: [part(request('POST', p, [['X-Forwarded-For', nextIP()], ['X-Oracle', '1'], ['Content-Type', 'application/json'], ['Content-Encoding', enc]], body))] });
        }
    }
    for (const n of [65536, 65537, 100000]) {
        cases.push(apiCase(`${prefix}json size ${n} POST /api/ab`, 'POST', '/api/ab', { body: jsonOfLength(n) }));
        cases.push(apiCase(`${prefix}json size ${n} PUT /api/ab`, 'PUT', '/api/ab', { body: jsonOfLength(n) }));
    }
    cases.push(apiCase(`${prefix}json text body PUT /api/ab`, 'PUT', '/api/ab', { headers: [['Content-Type', 'text/plain']], body: '{bad' }));
    cases.push(apiCase(`${prefix}json body on GET /api/ab`, 'GET', '/api/ab', { body: '{bad' }));
    cases.push(apiCase(`${prefix}json body on DELETE /api/ab`, 'DELETE', '/api/ab', { body: '{bad' }));
    cases.push(apiCase(`${prefix}json body on GET /api/health`, 'GET', '/api/health', { body: '{bad' }));
    cases.push(apiCase(`${prefix}json deep nesting`, 'POST', '/api/heartbeat', { body: '['.repeat(4000) + ']'.repeat(4000) }));
    return cases;
}

// slowCases run at the same time, each on its own connection.
function slowCases() {
    const head = (lines) => Buffer.from(lines.join('\r\n') + '\r\n', 'latin1');
    const full = (method, target, more) => head([`${method} ${target} HTTP/1.1`, 'Host: oracle.test', `X-Forwarded-For: ${nextIP()}`, 'X-Oracle: 1', ...more, '']);
    const drip = [];
    for (let i = 0; i < 25; i++) drip.push(part(Buffer.from('a'), 2000));
    const dripHeaders = [part(Buffer.from('GET /api/health HTTP/1.1\r\nHost: oracle.test\r\n'))];
    for (let i = 0; i < 25; i++) dripHeaders.push(part(Buffer.from(`X-D${i}: 1\r\n`), 2000));
    return [
        { id: 'slow nothing sent', listener: 'api', parts: [], wait: 50000 },
        { id: 'slow partial request line', listener: 'api', parts: [part(Buffer.from('GET /api/hea'))], wait: 50000 },
        { id: 'slow partial headers', listener: 'api', parts: [part(Buffer.from('GET /api/health HTTP/1.1\r\nHost: oracle.test\r\n'))], wait: 50000 },
        { id: 'slow headers dripped', listener: 'api', parts: dripHeaders, wait: 50000 },
        { id: 'slow body stalls', listener: 'api', parts: [part(full('POST', '/api/heartbeat', ['Content-Type: application/json', 'Content-Length: 100', 'Connection: close'])), part(Buffer.from('{"a":"'))], wait: 50000 },
        { id: 'slow body dripped', listener: 'api', parts: [part(full('POST', '/api/heartbeat', ['Content-Type: application/json', 'Content-Length: 100', 'Connection: close'])), ...drip], wait: 50000 },
        { id: 'slow body stalls 404', listener: 'api', parts: [part(full('POST', '/api/nope', ['Content-Type: application/json', 'Content-Length: 100', 'Connection: close'])), part(Buffer.from('{"a":"'))], wait: 50000 },
        { id: 'slow keep-alive idle', listener: 'api', parts: [part(full('GET', '/api/health', []))], wait: 30000 },
        { id: 'slow keep-alive then partial', listener: 'api', parts: [part(full('GET', '/api/health', [])), part(Buffer.from('GET /api/health HTTP/1.1\r\n'), 1000)], wait: 50000 },
        { id: 'slow upstream', listener: 'api', parts: [part(full('GET', '/api/health?upstream=slow:12000', ['Connection: close']))], wait: 30000 },
        { id: 'slow upstream post', listener: 'api', parts: [part(full('POST', '/api/heartbeat?upstream=slow:12000', ['Content-Type: application/json', 'Content-Length: 2', 'Connection: close'])), part(Buffer.from('{}'))], wait: 30000 },
    ];
}

function redirectCases() {
    const cases = [];
    const r = (id, text) => ({ id: `redirect ${id}`, listener: 'redirect', parts: [part(Buffer.from(text, 'latin1'))] });
    const hosts = ['example.com', 'example.com:80', 'example.com:5000', 'EXAMPLE.com', '[::1]', '[::1]:80', '[::1', '127.0.0.1:99',
        'a:b:c', 'evil.com@good.com', 'good.com/evil', 'good.com?x', 'good.com#x', 'good.com\\evil', 'good.com evil', 'xn--nxasmq6b', 'a$b', 'a%zz', '$', 'a$b%zz', 'a!b', "a'b", 'a(b)', 'a+b', 'a;b', 'a=b', 'a~b', 'a&b', 'a*b',
        'gööd.com', 'a"b', 'a<b>', '', ' spaced ', 'host,other', '%41.com', 'example.com.', '-', '_x_', '*'];
    for (const host of hosts) {
        cases.push(r(`host ${JSON.stringify(host)}`, `GET /x HTTP/1.1\r\nHost: ${host}\r\nConnection: close\r\n\r\n`));
    }
    const targets = ['/', '/a/b?c=d&e', '//evil.com/x', '/\\evil.com', '/%2F%2Fevil.com', '/a%20b', '/a b', '/ä', '/a"<>', '/a?x=<script>',
        '/../x', '/./x', '*', `http://evil.com/x`, `https://evil.com:444/y?z`, '/api/login', '/path#frag', '/%zz', '/%E4', '/' + 'x'.repeat(3000)];
    for (const t of targets) {
        cases.push(r(`target ${JSON.stringify(t.slice(0, 40))}`, `GET ${t} HTTP/1.1\r\nHost: good.test\r\nConnection: close\r\n\r\n`));
    }
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

// Plain bytes sent to a TLS port: what the client API port answers (the
// hint) and what the console's TLS port does.
function plainOnTLSCases(listener) {
    const r = (id, bytes, extra = {}) => ({ id: `${listener} plain ${id}`, listener, parts: [part(bytes)], wait: 4000, ...extra });
    return [
        r('get', Buffer.from('GET /api/health HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n')),
        r('post', Buffer.from('POST /api/login HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\n\r\n{}')),
        r('head', Buffer.from('HEAD / HTTP/1.1\r\nHost: x\r\n\r\n')),
        r('put', Buffer.from('PUT /x HTTP/1.1\r\nHost: x\r\n\r\n')),
        r('delete', Buffer.from('DELETE /x HTTP/1.1\r\nHost: x\r\n\r\n')),
        r('options', Buffer.from('OPTIONS /x HTTP/1.1\r\nHost: x\r\n\r\n')),
        r('connect', Buffer.from('CONNECT x:443 HTTP/1.1\r\nHost: x\r\n\r\n')),
        r('get five bytes', Buffer.from('GET /')),
        r('get four bytes', Buffer.from('GET ')),
        r('one byte', Buffer.from('G')),
        r('zeros', Buffer.alloc(8)),
        r('text', Buffer.from('hello world\r\n')),
        r('record too long', Buffer.from([0x16, 0x03, 0x01, 0xff, 0xff, 1, 2, 3])),
        r('record max', Buffer.from([0x16, 0x03, 0x01, 0x40, 0x00, 1, 2, 3])),
        r('record max plus', Buffer.from([0x16, 0x03, 0x01, 0x48, 0x01, 1, 2, 3])),
        r('wrong version handshake', Buffer.from([0x16, 0x09, 0x09, 0x00, 0x05, 1, 2, 3, 4, 5])),
        r('application data first', Buffer.from([0x17, 0x03, 0x03, 0x00, 0x05, 1, 2, 3, 4, 5])),
        r('alert first', Buffer.from([0x15, 0x03, 0x03, 0x00, 0x02, 2, 40])),
        r('sslv2 hello', Buffer.from([0x80, 0x2e, 0x01, 0x03, 0x01, 0x00, 0x15, 0x00, 0x00, 0x00, 0x10])),
        r('handshake garbage', Buffer.from([0x16, 0x03, 0x01, 0x00, 0x05, 9, 9, 9, 9, 9])),
        r('nothing', Buffer.alloc(0), { wait: 8000 }),
    ];
}

const TLS12 = [
    'ECDHE-RSA-AES128-GCM-SHA256', 'ECDHE-RSA-AES256-GCM-SHA384', 'ECDHE-RSA-CHACHA20-POLY1305', 'ECDHE-RSA-AES128-SHA256',
    'ECDHE-RSA-AES128-SHA', 'ECDHE-RSA-AES256-SHA', 'ECDHE-RSA-DES-CBC3-SHA', 'ECDHE-RSA-RC4-SHA',
    'ECDHE-ECDSA-AES128-GCM-SHA256', 'ECDHE-ECDSA-AES256-GCM-SHA384', 'ECDHE-ECDSA-CHACHA20-POLY1305', 'ECDHE-ECDSA-AES128-SHA256',
    'ECDHE-ECDSA-AES128-SHA', 'ECDHE-ECDSA-AES256-SHA', 'ECDHE-ECDSA-RC4-SHA',
    'AES128-GCM-SHA256', 'AES256-GCM-SHA384', 'AES128-SHA256', 'AES128-SHA', 'AES256-SHA', 'DES-CBC3-SHA', 'RC4-SHA',
];
const TLS13 = ['TLS_AES_128_GCM_SHA256', 'TLS_AES_256_GCM_SHA384', 'TLS_CHACHA20_POLY1305_SHA256'];

async function cipherProbes(port, cert) {
    const out = [];
    for (const name of TLS12) {
        const res = await tlsProbe(port, { ciphers: `${name}@SECLEVEL=0`, minVersion: 'TLSv1.2', maxVersion: 'TLSv1.2' });
        out.push({ cert, version: 'TLSv1.2', suite: name, ok: res.ok, error: res.error || null });
    }
    for (const name of TLS13) {
        const res = await tlsProbe(port, { ciphersuites: name, minVersion: 'TLSv1.3', maxVersion: 'TLSv1.3' });
        out.push({ cert, version: 'TLSv1.3', suite: name, ok: res.ok, error: res.error || null });
    }
    for (const version of ['TLSv1', 'TLSv1.1']) {
        const res = await tlsProbe(port, { ciphers: 'DEFAULT@SECLEVEL=0', minVersion: version, maxVersion: version });
        out.push({ cert, version, suite: '', ok: res.ok, error: res.error || null });
    }
    const def = await tlsProbe(port, {});
    out.push({ cert, version: 'default', suite: def.cipher || '', ok: def.ok, protocol: def.protocol || null });
    return out;
}

async function runCases(cases, ports, { parallel = false } = {}) {
    const one = async (c) => {
        const port = ports[c.listener];
        const secure = c.secure === true || (c.secure === undefined && ports.secure && ports.secure.includes(c.listener));
        const res = await exchange({ port, secure, parts: materialize(c.parts), wait: c.wait || 6000 });
        return { ...c, secure, ...res };
    };
    if (parallel) return Promise.all(cases.map(one));
    const out = [];
    for (const c of cases) out.push(await one(c));
    return out;
}

function envFor(extra) {
    return {
        PORT: String(PORT), HTTPS_PORT: String(HTTPS_PORT), API_PORT: String(API_PORT), API_ENABLED: 'true',
        BETTERDESK_API_URL: `http://127.0.0.1:${UPSTREAM_PORT}/api`, TRUST_PROXY: 'true', ...extra,
    };
}

async function scenario(name, env, waitPorts, body) {
    process.stderr.write(`[oracle] ${name}\n`);
    const node = await startNode(env, waitPorts);
    const result = { name, env: Object.fromEntries(Object.entries(env).map(([k, v]) => [k, v.replace(/\/tmp\/[^/]+\//, '<certs>/')])) };
    try {
        if (body) Object.assign(result, await body(node));
    } finally {
        await sleep(300);
        result.exit = node.exitCode;
        await node.stop();
        result.logs = logLines(node.lines);
    }
    return result;
}

async function main() {
    const certDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-oracle-certs-'));
    const certs = makeCerts(certDir);
    const upstream = await startUpstream(UPSTREAM_PORT);
    const elsewhere = http.createServer((req, res) => { res.setHeader('Content-Type', 'text/plain'); res.end(`elsewhere saw ${req.method} ${req.url}`); });
    await new Promise((resolve) => elsewhere.listen(ELSEWHERE_PORT, '127.0.0.1', resolve));
    const scenarios = [];
    const ports = { api: API_PORT, console: PORT, redirect: PORT, https: HTTPS_PORT };

    scenarios.push(await scenario('proxy', envFor({}), [PORT, API_PORT], async () => {
        const cases = [...methodCases(''), ...limitCases(''), ...limiterCases(''), ...headerCases(''), ...registrationCases('')];
        const exchanges = await runCases(cases, ports);
        const slow = await runCases(slowCases(), ports, { parallel: true });
        return { exchanges: [...exchanges, ...slow] };
    }));

    scenarios.push(await scenario('legacy', envFor({ RUSTDESK_API_PROXY: 'false' }), [PORT, API_PORT], async () => {
        const cases = [...methodCases(''), ...limitCases(''), ...limiterCases(''), ...jsonGateCases(''), ...registrationCases('')];
        const exchanges = await runCases(cases, ports);
        const slow = await runCases(slowCases().filter((c) => !c.id.includes('upstream')), ports, { parallel: true });
        return { exchanges: [...exchanges, ...slow] };
    }));

    const tlsEnv = (cert, extra = {}) => envFor({ SSL_CERT_PATH: cert.cert, SSL_KEY_PATH: cert.key, ...extra });
    scenarios.push(await scenario('api tls', tlsEnv(certs.rsa), [PORT, API_PORT], async () => {
        const tlsPorts = { ...ports, secure: ['api'] };
        const over = await runCases([
            apiCase('tls GET /api/health', 'GET', '/api/health'),
            apiCase('tls OPTIONS /api/login', 'OPTIONS', '/api/login'),
            apiCase('tls GET /nope', 'GET', '/nope'),
            apiCase('tls POST /api/login', 'POST', '/api/login', { body: '{}' }),
        ], tlsPorts);
        const plain = await runCases(plainOnTLSCases('api').map((c) => ({ ...c, secure: false })), ports);
        const ciphers = await cipherProbes(API_PORT, 'rsa');
        return { exchanges: [...over, ...plain], ciphers, chain: chainSent(API_PORT) };
    }));
    scenarios.push(await scenario('api tls ecdsa', tlsEnv(certs.ec), [PORT, API_PORT], async () => ({ ciphers: await cipherProbes(API_PORT, 'ecdsa') })));
    for (const mode of ['false', 'FALSE', 'off', '0', 'http', 'true', 'on', 'https', 'auto', 'maybe']) {
        scenarios.push(await scenario(`api tls mode ${mode}`, tlsEnv(certs.rsa, { RUSTDESK_API_TLS: mode }), [PORT, API_PORT], async () => {
            const res = await exchange({ port: API_PORT, secure: false, parts: materialize([part(request('OPTIONS', '/api/login'))]), wait: 3000 });
            return { exchanges: [{ id: `mode ${mode} plain OPTIONS`, listener: 'api', secure: false, parts: [part(request('OPTIONS', '/api/login'))], ...res }] };
        }));
    }
    scenarios.push(await scenario('api tls mode true without certificates', envFor({ RUSTDESK_API_TLS: 'true' }), [PORT, API_PORT], async () => {
        const res = await exchange({ port: API_PORT, secure: false, parts: materialize([part(request('OPTIONS', '/api/login'))]), wait: 3000 });
        return { exchanges: [{ id: 'plain OPTIONS', listener: 'api', secure: false, parts: [part(request('OPTIONS', '/api/login'))], ...res }] };
    }));

    scenarios.push(await scenario('https', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'true' }), [PORT, HTTPS_PORT, API_PORT], async () => {
        const redirects = await runCases(redirectCases(), ports);
        const consoleTLS = await runCases([
            { id: 'console tls GET /api/nothing', listener: 'https', secure: true, parts: [part(request('GET', '/api/nothing'))] },
            { id: 'console tls GET /', listener: 'https', secure: true, parts: [part(request('GET', '/', [['Accept', 'text/html']]))] },
        ], ports);
        const plain = await runCases(plainOnTLSCases('https').map((c) => ({ ...c, secure: false })), ports);
        const ciphers = await cipherProbes(HTTPS_PORT, 'rsa');
        return { exchanges: [...redirects, ...consoleTLS, ...plain], ciphers, chain: chainSent(HTTPS_PORT), apiChain: chainSent(API_PORT) };
    }));
    scenarios.push(await scenario('https HTTPS_ENABLED=True', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'True', HTTP_REDIRECT_HTTPS: 'TRUE' }), [PORT, HTTPS_PORT], async () => {
        const res = await runCases([{ id: 'redirect', listener: 'redirect', parts: [part(request('GET', '/x', [['Host', 'good.test']]))] }], ports);
        return { exchanges: res };
    }));
    scenarios.push(await scenario('https chain from SSL_CA_PATH', { ...envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.leaf.cert, SSL_KEY_PATH: certs.leaf.key, SSL_CA_PATH: certs.leaf.ca }) }, [PORT, HTTPS_PORT, API_PORT],
        async () => ({ chain: chainSent(HTTPS_PORT), apiChain: chainSent(API_PORT) })));
    scenarios.push(await scenario('https chain from SSL_CA_PATH intermediate only', { ...envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.leaf.cert, SSL_KEY_PATH: certs.leaf.key, SSL_CA_PATH: certs.leaf.intermediate }) }, [PORT, HTTPS_PORT],
        async () => ({ chain: chainSent(HTTPS_PORT) })));
    scenarios.push(await scenario('https missing SSL_CA_PATH file', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'true', SSL_CA_PATH: '/nonexistent/ca.crt' }), [HTTPS_PORT],
        async () => ({ chain: chainSent(HTTPS_PORT) })));
    scenarios.push(await scenario('https without redirect', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'true', HTTP_REDIRECT_HTTPS: 'false' }), [HTTPS_PORT, API_PORT],
        async () => ({ plainPortOpen: await portOpen(PORT) })));
    scenarios.push(await scenario('https redirect yes', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'true', HTTP_REDIRECT_HTTPS: 'yes' }), [HTTPS_PORT, API_PORT],
        async () => ({ plainPortOpen: await portOpen(PORT) })));

    const fallback = async () => {
        const res = await runCases([
            { id: 'console GET /api/nothing', listener: 'console', parts: [part(request('GET', '/api/nothing'))] },
            { id: 'api plain OPTIONS', listener: 'api', parts: [part(request('OPTIONS', '/api/login'))] },
        ], ports);
        return { exchanges: res, httpsPortOpen: await portOpen(HTTPS_PORT) };
    };
    scenarios.push(await scenario('fallback missing files', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: '/nonexistent/c.crt', SSL_KEY_PATH: '/nonexistent/c.key' }), [PORT, API_PORT], fallback));
    scenarios.push(await scenario('fallback missing key', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.rsa.cert, SSL_KEY_PATH: '/nonexistent/c.key' }), [PORT, API_PORT], fallback));
    scenarios.push(await scenario('fallback no paths', envFor({ HTTPS_ENABLED: 'true' }), [PORT, API_PORT], fallback));
    scenarios.push(await scenario('fallback only cert path', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.rsa.cert }), [PORT, API_PORT], fallback));
    scenarios.push(await scenario('fallback cert is a folder', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.dir, SSL_KEY_PATH: certs.rsa.key }), [PORT, API_PORT], fallback));
    scenarios.push(await scenario('http with certificates', tlsEnv(certs.rsa, {}), [PORT, API_PORT], fallback));

    const exits = async (node) => {
        await Promise.race([node.exited, sleep(8000)]);
        return { exitCode: node.exitCode };
    };
    scenarios.push(await scenario('fail garbage certificate', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.garbage, SSL_KEY_PATH: certs.rsa.key }), [PORT], exits));
    scenarios.push(await scenario('fail key mismatch', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.rsa.cert, SSL_KEY_PATH: certs.otherKey }), [PORT], exits));
    scenarios.push(await scenario('fail garbage certificate http mode', envFor({ SSL_CERT_PATH: certs.garbage, SSL_KEY_PATH: certs.rsa.key }), [PORT], exits));
    scenarios.push(await scenario('garbage certificate api tls off', envFor({ SSL_CERT_PATH: certs.garbage, SSL_KEY_PATH: certs.rsa.key, RUSTDESK_API_TLS: 'false' }), [PORT, API_PORT], async (node) => ({ exitCode: node.exitCode })));
    scenarios.push(await scenario('fail garbage ca', envFor({ HTTPS_ENABLED: 'true', SSL_CERT_PATH: certs.rsa.cert, SSL_KEY_PATH: certs.rsa.key, SSL_CA_PATH: certs.garbage }), [PORT], exits));

    const hold = async (port) => {
        const s = net.createServer();
        await new Promise((resolve) => s.listen(port, '127.0.0.1', resolve));
        return s;
    };
    let held = await hold(API_PORT);
    scenarios.push(await scenario('api port in use', envFor({}), [PORT], async (node) => {
        await sleep(1500);
        return { exitCode: node.exitCode, consoleOpen: await portOpen(PORT) };
    }));
    held.close();
    held = await hold(PORT);
    scenarios.push(await scenario('console port in use', envFor({}), [API_PORT], exits));
    scenarios.push(await scenario('redirect port in use', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'true' }), [HTTPS_PORT], exits));
    held.close();
    held = await hold(HTTPS_PORT);
    scenarios.push(await scenario('https port in use', tlsEnv(certs.rsa, { HTTPS_ENABLED: 'true' }), [PORT], exits));
    held.close();
    scenarios.push(await scenario('api disabled', envFor({ API_ENABLED: 'false' }), [PORT], async () => ({ apiPortOpen: await portOpen(API_PORT) })));
    scenarios.push(await scenario('api enabled empty', envFor({ API_ENABLED: '' }), [PORT], async () => ({ apiPortOpen: await portOpen(API_PORT) })));
    scenarios.push(await scenario('api proxy empty', envFor({ RUSTDESK_API_PROXY: '' }), [PORT, API_PORT], async () => {
        const res = await runCases([apiCase('POST /api/login malformed', 'POST', '/api/login', { body: '{x' })], ports);
        return { exchanges: res };
    }));

    // A Trailer header on a forwarded request: Node's request to the API
    // server throws outside any handler and the process ends.
    scenarios.push(await scenario('proxy trailer header', envFor({}), [PORT, API_PORT], async (node) => {
        const chunked = '2\r\n{}\r\n0\r\n\r\n';
        const first = await runCases([
            apiCase('chunked POST with Trailer', 'POST', '/api/heartbeat', { headers: [['Transfer-Encoding', 'chunked'], ['Content-Type', 'application/json'], ['Trailer', 'X-T']], body: chunked }),
            apiCase('GET with Trailer', 'GET', '/api/health', { headers: [['Trailer', 'X-T']] }),
        ], ports);
        await sleep(1000);
        return { exchanges: first, exitCode: node.exitCode, apiPortOpen: await portOpen(API_PORT) };
    }));

    upstream.close();
    elsewhere.close();
    fs.rmSync(certDir, { recursive: true, force: true });
    for (const s of scenarios) for (const e of s.exchanges || []) e.parts = compactParts(e.parts);
    const out = { node: process.version, openssl: process.versions.openssl, ports: { PORT, HTTPS_PORT, API_PORT, UPSTREAM_PORT, ELSEWHERE_PORT }, scenarios };
    fs.writeFileSync(OUT, JSON.stringify(out) + '\n');
    const count = scenarios.reduce((n, s) => n + (s.exchanges ? s.exchanges.length : 0) + (s.ciphers ? s.ciphers.length : 0), 0);
    process.stderr.write(`[oracle] ${scenarios.length} scenarios, ${count} answers written to ${OUT}\n`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
