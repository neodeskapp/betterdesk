// Writes ../securityaudit.json: what the security audit routes of the Node
// console answer when the API server (a made-up one on a local port,
// reached through the console's real client) answers this or that, for
// the Go tests of package securityaudit to replay.
//
//   cd /repo/web-nodejs && node ../betterdesk-server/console/routes/securityaudit/testdata/nodeoracle/securityaudit.js
'use strict';

const fs = require('fs');
const http = require('http');
const path = require('path');

const root = path.join('/repo', 'web-nodejs');

let scenario = null;
let calls = [];
const server = http.createServer((req, res) => {
    calls.push(req.method + ' ' + req.url);
    const api = scenario.api || {};
    let answer = api[req.url];
    if (answer === undefined && req.url.startsWith('/api/audit/events')) answer = api.events;
    if (answer === undefined) answer = { status: 404, body: '{"error":"not found"}' };
    if (answer.hang) {
        req.socket.destroy();
        return;
    }
    res.writeHead(answer.status, { 'Content-Type': 'application/json' });
    res.end(answer.body);
});

function stub(rel, exports) {
    const id = require.resolve(path.join(root, rel));
    require.cache[id] = { id, filename: id, loaded: true, exports };
}

async function main() {
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${server.address().port}/api`;
    process.env.BETTERDESK_API_KEY = 'oracle-key';
    process.env.BETTERDESK_API_TIMEOUT = '2000';
    stub('middleware/auth.js', {
        requireAuth: (req, res, next) => next(),
        requirePermission: () => (req, res, next) => next(),
    });
    const { compileQueryParser } = require(path.join(root, 'node_modules', 'express', 'lib', 'utils'));
    const parseQuery = compileQueryParser('extended');
    const router = require(path.join(root, 'routes', 'security-audit.routes.js'));

    async function run(s) {
        scenario = s;
        calls = [];
        const layer = router.stack.find((l) => l.route && l.route.path === '/api/panel/security-audit/' + s.route);
        const handler = layer.route.stack[layer.route.stack.length - 1].handle;
        const answer = { status: 200, text: null };
        let done;
        const finished = new Promise((resolve) => { done = resolve; });
        const res = {
            status(code) { answer.status = code; return this; },
            json(body) { answer.text = JSON.stringify(body); done(); return this; },
        };
        const req = {
            query: parseQuery(s.query || ''),
            headers: s.forwardedProto === undefined ? {} : { 'x-forwarded-proto': s.forwardedProto },
            secure: !!s.secure,
            protocol: s.secure ? 'https' : 'http',
        };
        const saved = process.env.SESSION_MAX_PER_USER;
        if (s.maxPerUser === undefined) delete process.env.SESSION_MAX_PER_USER;
        else process.env.SESSION_MAX_PER_USER = s.maxPerUser;
        const now = Date.now();
        handler(req, res);
        await finished;
        if (saved === undefined) delete process.env.SESSION_MAX_PER_USER;
        else process.env.SESSION_MAX_PER_USER = saved;
        return { ...s, now, answer, calls: calls.slice().sort() };
    }

    const NOW = Date.now();
    const iso = (msAgo) => new Date(NOW - msAgo).toISOString();
    const dbTime = (msAgo) => new Date(NOW - msAgo).toISOString().slice(0, 19).replace('T', ' ');
    const offsetTime = (msAgo) => {
        const local = new Date(NOW - msAgo + 2 * 3600 * 1000).toISOString();
        return local.slice(0, 23) + '456789+02:00';
    };
    const HOUR = 3600 * 1000;
    const DAY = 24 * HOUR;
    const json = (v) => ({ status: 200, body: JSON.stringify(v) });

    const health = json({ status: 'ok', version: '3.5.224', tls: false, uptime: '1h' });
    const keys = json([{ id: 1, name: 'console-bridge', role: 'admin', created_at: dbTime(2 * DAY) }]);
    const audit = json({ enabled: true, total: 2, count: 2, events: [{ timestamp: iso(HOUR), action: 'peer_deleted', actor: 'admin' }, { timestamp: iso(2 * HOUR), action: 'config_changed', actor: 'admin' }] });
    const blocklist = json({ enabled: true, count: 0, entries: [] });
    const base = { '/api/health': health, '/api/keys': keys, events: audit, '/api/blocklist': blocklist };

    const scenarios = [];
    const overview = (name, api, extra) => scenarios.push({ name, route: 'overview', api: { ...base, ...api }, ...extra });
    const failed = (n, action = 'login_failed', ago = HOUR) => Array.from({ length: n }, () => ({ timestamp: iso(ago), action, actor: '10.0.0.9' }));
    const events = (list) => ({ events: json({ enabled: true, total: list.length, count: list.length, events: list }) });

    overview('a quiet server', {});
    overview('TLS on', { '/api/health': json({ status: 'ok', tls: true }) });
    for (const tls of ['yes', 1, 0, null, '', [], {}]) overview('health says tls ' + JSON.stringify(tls), { '/api/health': json({ tls }) });
    overview('health is a list', { '/api/health': json([{ tls: true }]) });
    overview('health is text', { '/api/health': { status: 200, body: 'ok' } });

    overview('failed logins of every kind', events([
        ...failed(1, 'login_failed'), ...failed(1, 'auth_failed'), ...failed(1, 'auth_login_failed'), ...failed(1, 'LOGIN_FAILED'),
        ...failed(1, 'login_failed', 23 * HOUR), ...failed(1, 'login_failed', 25 * HOUR), ...failed(1, 'login_failed', 40 * DAY),
        { action: 'login_failed' }, { action: 'login_failed', timestamp: 'some day' }, { action: 'login_failed', timestamp: '' },
        { action: 'login_failed', created_at: iso(HOUR) }, { action: 'login_failed', created_at: iso(3 * DAY) },
        { action: 'login_failed', timestamp: null, created_at: iso(3 * DAY) }, { action: 'login_failed', timestamp: iso(HOUR), created_at: iso(3 * DAY) },
        { action: 'login_failed', timestamp: offsetTime(HOUR) }, { action: 'login_failed', timestamp: offsetTime(26 * HOUR) },
        { action: 'login_failed', timestamp: dbTime(HOUR) }, { action: 'login_failed', timestamp: dbTime(30 * HOUR) },
        { action: 'login_failed', timestamp: NOW - 3 * DAY }, { action: 'login_failed', timestamp: true }, { action: 'login_failed', timestamp: [iso(3 * DAY)] },
        { action: 'login_failed', timestamp: { at: 1 } }, { action: ['login_failed'], timestamp: iso(HOUR) }, { action: 5 },
        { action: 'config_changed', timestamp: iso(HOUR) }, { action: 'config_changed', timestamp: iso(90 * DAY) }, { action: 'setting_changed' },
        { action: 'settings_changed' }, 'text', 5, true, [1], {},
    ]));
    for (const n of [5, 6, 9, 10, 20, 21, 40]) overview(n + ' failed logins', events(failed(n)));
    overview('failed logins and everything else wrong', {
        ...events(failed(30)),
        '/api/blocklist': json({ enabled: true, count: 3, entries: [1, 2, 3] }),
        '/api/keys': json([{ created_at: dbTime(200 * DAY) }]),
    });
    overview('audit events as a bare list', { events: json(failed(7)) });
    overview('audit events as text', { events: json({ events: 'abc' }) });
    overview('audit events as an object', { events: json({ events: { 0: { action: 'login_failed' } } }) });
    overview('audit events as a number', { events: json({ events: 5 }) });
    overview('audit events null', { events: json({ events: null }) });
    overview('an audit event that is null', events([{ action: 'login_failed' }, null]));
    overview('audit answer is text', { events: { status: 200, body: 'plain text' } });
    overview('audit answer is a number', { events: { status: 200, body: '5' } });
    overview('audit switched off', { events: json({ enabled: false, events: [] }) });

    const key = (created) => ({ id: 1, created_at: created });
    overview('keys of every age', {
        '/api/keys': json([
            key(dbTime(89 * DAY)), key(dbTime(91 * DAY)), key(iso(91 * DAY)), key(iso(DAY)), key(offsetTime(100 * DAY)), key(''), key(null), {},
            key('long ago'), key(NOW - 100 * DAY), key(NOW - DAY), key(true), key([iso(100 * DAY)]), key([NOW - 100 * DAY]), key({ at: 1 }),
            key(iso(100 * DAY).slice(0, 10)), key(1), 'text', 5, [key(dbTime(100 * DAY))],
        ]),
    });
    overview('keys under a name', { '/api/keys': json({ keys: [key(dbTime(100 * DAY)), key(dbTime(DAY))] }) });
    overview('keys as text', { '/api/keys': json({ keys: 'abc' }) });
    overview('keys as an object', { '/api/keys': json({ keys: { a: 1 } }) });
    overview('keys zero', { '/api/keys': json({ keys: 0 }) });
    overview('a key that is null', { '/api/keys': json([key(dbTime(DAY)), null]) });
    overview('keys answer is text', { '/api/keys': { status: 200, body: 'plain' } });

    for (const count of [2, 0, -1, 1.5, null, '5', '', ' 7 ', 'abc', true, false, [], [7], [1, 2], {}, '0x10', '1e2', 1e21]) {
        overview('blocklist count ' + JSON.stringify(count), { '/api/blocklist': json({ enabled: true, count, entries: ['a', 'b', 'c'] }) });
    }
    overview('blocklist without a count', { '/api/blocklist': json({ enabled: false, entries: ['a', 'b'] }) });
    overview('blocklist without a count and with entries of another kind', { '/api/blocklist': json({ entries: 'ab' }) });
    overview('blocklist as a list', { '/api/blocklist': json(['a', 'b']) });
    overview('blocklist as text', { '/api/blocklist': { status: 200, body: 'plain' } });

    const refused = { status: 500, body: '{"error":"internal"}' };
    for (const name of ['/api/health', '/api/keys', 'events', '/api/blocklist']) {
        overview('refused: ' + name, { [name]: refused });
        overview('no answer: ' + name, { [name]: { hang: true } });
        overview('empty answer: ' + name, { [name]: { status: 200, body: '' } });
        overview('null answer: ' + name, { [name]: { status: 200, body: 'null' } });
        overview('zero answer: ' + name, { [name]: { status: 200, body: '0' } });
        overview('false answer: ' + name, { [name]: { status: 200, body: 'false' } });
        overview('not found: ' + name, { [name]: { status: 404, body: 'gone' } });
    }
    overview('everything refused', { '/api/health': refused, '/api/keys': refused, events: refused, '/api/blocklist': refused });

    for (const proto of ['https', 'HTTPS', 'http', ' https ', 'https, http', 'http, https', 'https,http', '', 'wss', 'https;x']) {
        overview('forwarded proto ' + JSON.stringify(proto), {}, { forwardedProto: proto });
    }
    overview('over HTTPS', {}, { secure: true });
    overview('over HTTPS behind a proxy that says http', {}, { secure: true, forwardedProto: 'http' });

    const eventsRoute = (name, query, api) => scenarios.push({ name, route: 'events', query, api: { events: api || audit } });
    for (const query of ['', 'limit=10&offset=5', 'limit=abc&offset=x', 'limit=0&offset=0', 'limit=-5&offset=-2', 'limit=0x10', 'limit=7.9&offset=3.7',
        'limit=1e3', 'limit=99999999999999999999999', 'limit=10&limit=20', 'limit[]=30', 'limit[a]=1', 'offset=5', 'offset=%207', 'limit=+8',
        'action=login_failed', 'action=a%20b%26c%3Dd', 'action=x&action=y', 'action[]=z', 'action[k]=v', 'action=', 'action[]=', 'action=%E2%9C%93',
        'action=a/b?c#d', "action=it's(1)*!~", 'limit=5&action=peer_deleted&offset=1', 'limit=00012', 'limit=12abc', 'LIMIT=3']) {
        eventsRoute('query ?' + query, query);
    }
    eventsRoute('refused', '', refused);
    eventsRoute('refused with text', '', { status: 403, body: 'Forbidden\n' });
    eventsRoute('refused without a body', '', { status: 503, body: '' });
    eventsRoute('no answer', '', { hang: true });
    eventsRoute('an empty answer', '', { status: 200, body: '' });
    eventsRoute('a null answer', '', { status: 200, body: 'null' });
    eventsRoute('a zero answer', '', { status: 200, body: '0' });
    eventsRoute('a false answer', '', { status: 200, body: 'false' });
    eventsRoute('an empty text answer', '', { status: 200, body: '""' });
    eventsRoute('a text answer', '', { status: 200, body: 'plain' });
    eventsRoute('an empty list', '', { status: 200, body: '[]' });
    eventsRoute('an empty object', '', { status: 200, body: '{}' });
    eventsRoute('a number', '', { status: 200, body: '7' });
    eventsRoute('another good status', '', { status: 201, body: '{"made":true}' });
    eventsRoute('keys keep their order', '', { status: 200, body: '{"z":1,"a":{"y":2,"b":3},"10":4,"9":5}' });

    for (const maxPerUser of [undefined, '0', '1', '5', 'abc', '2000', ' 3 ', '-1', '1.5', '', '007']) {
        scenarios.push({ name: 'session limit ' + JSON.stringify(maxPerUser), route: 'hardening', maxPerUser });
    }

    const out = [];
    for (const s of scenarios) out.push(await run(s));
    const file = path.join(__dirname, '..', 'securityaudit.json');
    fs.writeFileSync(file, JSON.stringify(out));
    console.log(`wrote ${out.length} scenarios to ${file}`);
    server.close();
    process.exit(0);
}

main().catch((err) => { console.error(err); process.exit(1); });
