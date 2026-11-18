'use strict';

/**
 * Records what the old console made of an upgrade's request target and
 * Origin, for the Go tests of console/ws:
 *
 *  - targets: new URL(req.url, 'http://' + host): its pathname, whether it
 *    threw, and searchParams.get() for a few names (services/wsUpgradeRouter.js
 *    and the services read them so);
 *  - origins: validateOrigin of middleware/wsOrigin.js under several
 *    settings of WS_ALLOWED_ORIGINS and BD_ALLOW_MISSING_ORIGIN.
 *
 *   node targets.js     writes ../node_targets.json
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

// A deterministic pseudo-random source.
let seed = 7;
const rnd = (n) => { seed = (Math.imul(seed, 1103515245) + 12345) & 0x7fffffff; return (seed >>> 8) % n; };
const pick = (list) => list[rnd(list.length)];

const handpicked = [
    '/ws/device-status', '/ws/device-status/', '/ws/Device-Status', '/ws//device-status', '/ws/./device-status',
    '/ws/x/../device-status', '/ws/%2e/device-status', '/ws/%2E%2E/ws/device-status', '/ws/.%2e/ws/device-status',
    '/ws\\device-status', '/ws/device%2Dstatus', '/ws/device-status?x=1', '/ws/device-status#frag', '/ws/device-status?#',
    '//evil/ws/device-status', '///ws/device-status', '/\\evil/ws/device-status', 'http://other/ws/device-status',
    'HTTP://other:80/ws/device-status?a=b', 'https://h/ws/x', 'ws://h/ws/x', 'foo://h/ws/device-status', 'foo:/ws/x',
    '*', '/', '', '/..', '/../..', '/a/b/../../..', '/ws/chat/abc', '/ws/chat/a%2Fb', '/ws/chat/a b', '/ws/chat/"x"',
    '/ws/chat/<x>', '/ws/chat/{x}', '/ws/chat/`x`', '/ws/chat/^|', "/ws/chat/'x'", '/ws/chat/%zz', '/ws/chat/%', '/ws/chat/%4',
    '/ws/bd-signal?device_id=dev1&token=abc', '/ws/bd-signal?device_id=a+b&token=%20x', '/ws/bd-signal?device_id=&token',
    '/ws/bd-signal?device_id=%E2%82%AC&token=%FF%FE', '/ws/bd-signal?device_id=%C3%28&token=%E2%82', '/ws/bd-signal?token=a&token=b',
    '/ws/bd-signal?=x&device_id', '/ws/bd-signal?a&&b=1&device_id=x=y', "/ws/bd-signal?q='\"<>`", '/ws/bd-signal?q=%2B+%2b',
    'http://[::1]:5000/ws/x', 'http://[::1/ws/x', 'http://a:b@h/ws/x', 'http://h:99999/ws/x', 'http://h:/ws/x', 'http://h:12a/ws/x',
    'http://h%20x/ws/x', 'http://h<x/ws/x', 'http:///ws/x', 'http:h/ws/x', 'http:/ws/x', 'http:ws/x',
];
const parts = ['/', '//', '\\', '.', '..', '%2e', '%2E', '%2e%2e', 'ws', 'device-status', 'chat', 'a', '?', '#', '&', '=', '+',
    '%', '%41', '%zz', '%C3%A9', '%FF', ' ', '"', '<', '>', '`', '{', '}', '|', '^', "'", '@', ':', '[', ']', 'token', 'device_id'];
const targets = new Set(handpicked);
while (targets.size < 6000) {
    let t = pick(['/', '/ws/', '//', 'http://h/', '/ws/chat/']);
    const n = 1 + rnd(8);
    for (let i = 0; i < n; i++) t += pick(parts);
    targets.add(t);
}
const NAMES = ['device_id', 'token', 'session', 'role', 'a', 'q', '', 'x'];
const out = [];
for (const t of targets) {
    for (const host of ['console.test:5000']) {
        try {
            const u = new URL(t, `http://${host}`);
            const params = {};
            for (const name of NAMES) {
                const v = u.searchParams.get(name);
                if (v !== null) params[name] = v;
            }
            out.push({ target: t, path: u.pathname, search: u.search, params });
        } catch (err) {
            out.push({ target: t, throws: true });
        }
    }
}

// ---- Origins ----

const originCases = [];
const origins = ['', 'http://console.test:5000', 'https://console.test:5000', 'http://console.test', 'HTTP://console.test:5000',
    'http://console.test:5000/', 'https://evil.example', 'null', 'http://allowed.example', ' http://allowed.example', 'http://other.test'];
const cookies = ['', 'bd.sid=x', 'betterdesk.sid=s%3Ax', 'xbd.sid=1', 'bd.sid', 'a=1; bd.sid=2', 'BD.SID=1', 'betterdesk_sid=1'];
const hosts = ['console.test:5000', 'console.test', ''];
const envs = [{}, { WS_ALLOWED_ORIGINS: 'http://allowed.example, ,https://x.test ' }, { BD_ALLOW_MISSING_ORIGIN: 'true' },
    { BD_ALLOW_MISSING_ORIGIN: 'TRUE' }, { WS_ALLOWED_ORIGINS: 'null' }];
for (const env of envs) {
    for (const k of ['WS_ALLOWED_ORIGINS', 'BD_ALLOW_MISSING_ORIGIN']) delete process.env[k];
    Object.assign(process.env, env);
    const file = require.resolve(path.join(WEB, 'middleware/wsOrigin.js'));
    delete require.cache[file];
    const { validateOrigin } = require(file);
    for (const origin of origins) {
        for (const cookie of cookies) {
            for (const host of hosts) {
                const headers = {};
                if (origin) headers.origin = origin;
                if (cookie) headers.cookie = cookie;
                if (host) headers.host = host;
                const r = validateOrigin({ headers });
                originCases.push({ env, origin, cookie, host, ok: r.ok, reason: r.reason || '' });
            }
        }
    }
}

const file = path.join(__dirname, '..', 'node_targets.json');
fs.writeFileSync(file, `${JSON.stringify({ targets: out, origins: originCases }, null, 1)}\n`);
console.log(`${file}: ${out.length} targets, ${originCases.length} origins`);
