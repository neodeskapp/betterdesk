'use strict';

// Vectors for the remote routes: routes/remote.routes.js of the Node
// console (with the dev mock of lib/devMockRemote) and POST
// /api/bd/remote-agent-token of routes/bd-api.routes.js, behind the real
// guards (middleware/auth.js, middleware/deviceAuth.js on a scratch SQLite
// database), the real rate limiter, cookie parser, request language,
// guest link helpers (middleware/guestAccess.js) and API client, in front
// of a scripted API server that answers by path. Writes ../vectors.json.
//
// What the routes ask of other services is scripted per vector and
// recorded: the device of the console's table (db.getDevice; "devices"),
// the server's key (keyService.resolvePublicKey; "serverKey"), the
// connection mode of the settings (getConnectionMode; "connectionMode"),
// the branding (brandingService.getBranding; "branding") and the remote
// relay's sessions and tokens (services/remoteRelay.js; "relay"). A
// scripted value {"throws": text} makes the call fail.
//
// Left out, because the Node console stops at them (BUGS.md MC1): a
// query value with a toString member in guest, t, mesh_share, transport,
// mock and scenario.

const crypto = require('crypto');
const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');
const { load } = require(path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle', 'lib'));

process.env.RDCLIENT_PAGE_RATE_LIMIT_MAX = '100000000';
process.env.DB_TYPE = 'sqlite';
process.env.DB_PATH = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'remote-oracle-')), 'db_v2.sqlite3');
process.env.DATA_DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'remote-data-'));
process.env.NODE_ENV = 'development';
delete process.env.DATABASE_URL;
delete process.env.BD_DEV_MOCK_REMOTE;

const json = 'application/json';
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');

// The token rows, the same as in routes_test.go
const TOKENS = [['tok-device-a', 'dev-a'], ['tok-device-b', 'dev-b'], ['tok-numeric', '123456'], ['tok-unbound', ''], ['tok-seeded', '900100001']];

// The scripted world of one vector
let state = {};
let seen = [];
let relayCalls = [];
let deviceCalls = [];

function listen(server) {
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server.address().port)));
}

const scripted = (value) => {
    if (value && typeof value === 'object' && value.throws !== undefined) throw new Error(value.throws);
    return value;
};

async function main() {
    const stub = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            seen.push({ method: req.method, url: req.url, body: Buffer.concat(chunks).toString('utf8') });
            const key = `${req.method} ${req.url.split('?')[0]}`;
            const a = (state.api && state.api[key]) || { status: 404, type: json, body: '{"error":"not found"}' };
            if (a.hang) {
                req.socket.destroy();
                return;
            }
            const headers = {};
            if (a.type) headers['Content-Type'] = a.type;
            res.writeHead(a.status, headers);
            res.end(a.body || '');
        });
    });
    const apiPort = await listen(stub);
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${apiPort}/api`;

    const db = load('./services/database');
    await db.init();
    const raw = load('./services/dbAdapter').getAdapter().getSqliteAuthDb();
    raw.prepare("INSERT INTO users (id, username, password_hash, role) VALUES (900, 'device-owner', 'x', 'operator')").run();
    for (const [token, client] of TOKENS) {
        raw.prepare("INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at) VALUES (?, ?, 900, ?, '', '2999-01-01 00:00:00')").run(token, sha(token), client);
    }

    db.getDevice = async (id) => {
        deviceCalls.push(id);
        const devices = state.devices || {};
        return scripted(Object.prototype.hasOwnProperty.call(devices, id) ? devices[id] : null);
    };
    const keyService = load('./services/keyService');
    keyService.resolvePublicKey = async () => scripted(state.serverKey === undefined ? 'server-key-base64=' : state.serverKey);
    const connection = load('./services/serverConnectionConfigService');
    connection.getConnectionMode = async () => scripted(state.connectionMode === undefined
        ? { mode: 'p2p_first', p2p_fallback_ms: 2000, same_nat_relay: true, allow_shared_nat_initiator: false } : state.connectionMode);
    const brandingService = load('./services/brandingService');
    const defaults = brandingService.getBranding();
    brandingService.getBranding = () => scripted(state.branding === undefined ? defaults : state.branding);
    const remoteRelay = load('./services/remoteRelay');
    let issued = 0;
    remoteRelay.issueRemoteAgentToken = (id) => {
        issued++;
        relayCalls.push({ call: 'issue', id });
        return { token: crypto.createHash('sha256').update(`token-${issued}`).digest('hex'), expires_in: 60 };
    };
    remoteRelay.getActiveSessions = () => {
        relayCalls.push({ call: 'sessions' });
        return (state.relay && state.relay.sessions) || [];
    };
    remoteRelay.getSessionState = (id) => {
        relayCalls.push({ call: 'state', id });
        const states = (state.relay && state.relay.states) || {};
        return Object.prototype.hasOwnProperty.call(states, id) ? states[id] : null;
    };

    const express = load('express');
    const app = express();
    app.use(load('./lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use(load('cookie-parser')());
    app.use((req, _res, next) => {
        const header = req.headers['x-test-session'];
        req.session = header ? JSON.parse(header) : {};
        req.session.destroy = () => { state.destroyed = true; };
        next();
    });
    app.use('/api/bd', load('./routes/bd-api.routes'));
    app.use(load('./middleware/i18n').initI18n());
    app.use('/', load('./routes/remote.routes'));
    app.use((req, res) => {
        res.status(404).type('text/plain').send('no route');
    });
    app.use((err, req, res, _next) => {
        res.status(err.status || 500).json({ success: false, error: err.message });
    });
    const server = http.createServer(app);
    const port = await listen(server);

    function call(request, world) {
        state = { ...(world || {}) };
        seen = [];
        relayCalls = [];
        deviceCalls = [];
        if (state.mock) process.env.BD_DEV_MOCK_REMOTE = state.mock;
        else delete process.env.BD_DEV_MOCK_REMOTE;
        return new Promise((resolve, reject) => {
            const headers = { ...(request.headers || {}) };
            if (request.session) headers['x-test-session'] = JSON.stringify(request.session);
            if (request.type) headers['Content-Type'] = request.type;
            const body = request.body === undefined ? null : Buffer.from(request.body, 'utf8');
            if (body) headers['Content-Length'] = body.length;
            const req = http.request({ host: '127.0.0.1', port, method: request.method || 'GET', path: request.path, headers }, (res) => {
                const chunks = [];
                res.on('data', (c) => chunks.push(c));
                res.on('end', () => {
                    const got = Buffer.concat(chunks);
                    const h = res.headers;
                    const vector = {
                        request: { method: request.method || 'GET', path: request.path },
                        world: world || {},
                        status: res.statusCode,
                        type: h['content-type'] || '',
                        cacheControl: h['cache-control'] || '',
                        location: h.location || '',
                        cookies: [].concat(h['set-cookie'] || []),
                        rateLimit: h['ratelimit-limit'] || '',
                        destroyed: !!state.destroyed,
                        calls: seen,
                        relayCalls,
                        deviceCalls,
                    };
                    if (request.session) vector.request.session = request.session;
                    if (request.type) vector.request.type = request.type;
                    if (request.body !== undefined) vector.request.body = request.body;
                    if (request.headers) vector.request.headers = request.headers;
                    if (request.binary) vector.base64 = got.toString('base64');
                    else vector.body = got.toString('utf8');
                    resolve(vector);
                });
            });
            req.on('error', reject);
            if (body) req.write(body);
            req.end();
        });
    }

    const vectors = [];
    const send = async (request, world) => vectors.push(await call(request, world));
    const accounts = {
        admin: { userId: 1, user: { id: 1, username: 'e2e_admin', role: 'admin' } },
        super_admin: { userId: 2, user: { id: 2, username: 'e2e_super_admin', role: 'super_admin' } },
        server_admin: { userId: 3, user: { id: 3, username: 'e2e_server_admin', role: 'server_admin' } },
        global_admin: { userId: 4, user: { id: 4, username: 'e2e_global_admin', role: 'global_admin' } },
        operator: { userId: 5, user: { id: 5, username: 'e2e_operator', role: 'operator' } },
        viewer: { userId: 6, user: { id: 6, username: 'e2e_viewer', role: 'viewer' } },
        guest_issuer: { userId: 7, user: { id: 7, username: 'e2e_guest_issuer', role: 'guest_issuer' } },
        pro: { userId: 8, user: { id: 8, username: 'e2e_pro', role: 'pro' } },
    };
    const everyone = [undefined, ...Object.values(accounts)];
    const { operator, viewer, admin } = accounts;

    const answer = (status, body, type = json) => ({ status, type, body: typeof body === 'string' ? body : JSON.stringify(body) });
    const goodLink = (peers, more = {}) => answer(200, { valid: true, id: 'g1', peer_ids: peers, view_only: false, expires_at: '2099-01-01T00:00:00Z', ...more });
    const peerOf = (id, body) => ({ [`GET /api/peers/${id}`]: answer(200, body) });
    const health = (connection) => ({ 'GET /api/health/detail': answer(200, connection === undefined ? { status: 'ok' } : { status: 'ok', connection }) });

    // Who may call what, with an ordinary world
    const world = {
        devices: { 'dev-1': { id: 'dev-1', hostname: 'Front desk', platform: 'Windows', tags: [] } },
        api: { ...peerOf('dev-1', { id: 'dev-1', device_type: 'rustdesk' }), ...health({ p2p_first: false, relay_servers: 'relay.example' }) },
        relay: { sessions: ['dev-1', 'dev-2'], states: { 'dev-1': { agentConnected: true, viewerCount: 2, streaming: true } } },
    };
    const target = { 'GET /api/remote-targets/rt_abcdefgh': answer(200, { id: 'rt_abcdefgh', name: 'Server', protocol: 'RDP', host: '10.0.0.5', port: 3389, username: 'adm', tls_mode: 'strict', enabled: true }) };
    const paths = [
        '/api/remote/login-info', '/api/remote/login-info?return=/remote/dev-1', '/api/remote/guest-info', '/api/remote/desk',
        '/remote-dashboard', '/api/remote/viewer/dev-1', '/api/remote/viewer/rt_abcdefgh', '/api/remote/viewer/dev-9',
        '/remote-cdap/dev-1', '/remote-desktop/dev-1', '/api/remote/sessions', '/api/remote/session/dev-1', '/api/remote/session/dev-2', '/dev/mock-remote.js',
    ];
    for (const p of paths) {
        for (const session of everyone) {
            await send({ path: p, session }, { ...world, api: { ...world.api, ...target } });
        }
    }

    // The way back after sign-in
    const returns = ['/remote', '/remote/', '/remote/dev-1?transport=cdap#x', '/remote/login', '/remote/login/x', '/remote/../devices', '/remote/%2e%2e/devices', '//evil.example/remote',
        '/remotex', '/devices', 'remote', '', '/remote/a%0d%0ab', '/remote/a\\b', '/remote/%5c', 'https://evil.example/remote', '/remote/%E4%B8%96', '/remote?return=/x', '/REMOTE', '/remote/./a'];
    for (const value of returns) {
        await send({ path: `/api/remote/login-info?return=${encodeURIComponent(value)}`, session: operator });
        await send({ path: `/api/remote/login-info?return=${encodeURIComponent(value)}` });
    }
    for (const q of ['return[]=/remote/a', 'return=/remote/a&return=/remote/b', 'return[x]=/remote', 'return', 'return=%zz']) {
        await send({ path: `/api/remote/login-info?${q}`, session: operator });
    }

    // The branding and the languages of the pages
    const brandings = [
        { appName: 'Acme', logoType: 'svg', logoIcon: 'cloud', logoSvg: '<svg/>', logoUrl: '/u/logo.png', logoText: 'Ac', logoTextAccent: 'me', extra: 1 },
        { appName: '', logoType: '', logoIcon: 0, logoSvg: null, logoUrl: false, logoText: [], logoTextAccent: {} },
        { appName: 7, logoType: 0 }, {}, null, { throws: 'branding broken' },
    ];
    for (const b of brandings) {
        await send({ path: '/api/remote/login-info' }, { branding: b });
        await send({ path: '/api/remote/guest-info' }, { branding: b });
    }
    for (const lang of ['de', 'fr', 'xx', 'pl-PL']) {
        await send({ path: '/api/remote/guest-info', headers: { 'Accept-Language': lang } });
        await send({ path: `/api/remote/guest-info?lang=${lang}` });
    }

    // The guest page
    const guestAnswers = [
        goodLink(['dev-1'], { label: 'Front desk', view_only: true, devices: [{ id: 'dev-1', name: 'A' }] }),
        goodLink(['dev-1'], { label: 0, expires_at: '', devices: 'x' }),
        goodLink(['dev-1'], { expires_at: 4102444800000, view_only: 'yes' }),
        goodLink(['dev-1'], { expires_at: '2001-01-01T00:00:00Z' }),
        goodLink(['dev-1'], { expires_at: 'not a date', label: ['a'] }),
        answer(200, { valid: false, error: 'invalid or expired guest link' }),
        answer(200, { valid: false, error: 17 }),
        answer(200, { valid: false }),
        answer(200, { valid: 0, error: '' }),
        answer(200, 'null'), answer(200, '"text"'), answer(200, '[]'), answer(200, ''),
        answer(200, 'plain', 'text/plain'),
        answer(403, { error: 'forbidden' }), answer(404, { error: { nested: true } }), answer(500, '""'), answer(500, 'boom', 'text/plain'), answer(502, { message: 'x' }),
        { hang: true },
    ];
    for (const a of guestAnswers) {
        for (const query of ['?t=tok', '?guest=tok']) {
            await send({ path: `/api/remote/guest-info${query}` }, { api: { 'GET /api/guest/access-links/peers': a } });
        }
        await send({ path: '/api/remote/guest-info', headers: { Cookie: 'bd.guest=tok' } }, { api: { 'GET /api/guest/access-links/peers': a } });
    }
    const tokens = ['?t=%20tok%20', '?t=', '?guest=&t=tok', '?guest=a&guest=b', '?t[]=a', '?t[x]=1', '?t=a%26b', '?guest=%E4%B8%96'];
    for (const q of tokens) {
        await send({ path: `/api/remote/guest-info${q}` }, { api: { 'GET /api/guest/access-links/peers': goodLink(['dev-1']) } });
    }
    for (const cookie of ['bd.guest=', 'bd.guest=%20x', 'betterdesk.guest=tok', 'bd.guest=j:"json"', 'bd.guest=j:5', 'other=1; bd.guest=c']) {
        await send({ path: '/api/remote/guest-info', headers: { Cookie: cookie } }, { api: { 'GET /api/guest/access-links/peers': goodLink(['dev-1']) } });
    }

    // Who reaches the viewer with a guest link or a share link
    const validate = 'GET /api/guest/access-links/validate';
    const share = 'GET /api/mesh/share/validate';
    const links = [
        goodLink(['dev-1', 'dev-2']), goodLink(['dev-2']), goodLink([]), goodLink('xx dev-1 yy'), goodLink('dev'), goodLink(7), goodLink({ a: 1 }),
        answer(200, { valid: true, allowed_peer_ids: ['dev-1'], view_only: 1, expires_at: '2099-01-01T00:00:00Z' }),
        answer(200, { valid: true, peer_ids: ['other'], allowed_peer_ids: ['dev-1'] }),
        answer(200, { valid: true, peer_ids: ['dev-1'], expires_at: 1 }),
        answer(200, { valid: true, peer_ids: ['dev-1'] }),
        answer(200, { valid: false, peer_ids: ['dev-1'] }),
        answer(500, { error: 'down' }), { hang: true },
    ];
    const guestRequests = [
        { path: '/api/remote/viewer/dev-1?guest=tok' }, { path: '/api/remote/viewer/dev-1?t=tok' },
        { path: '/api/remote/viewer/dev-1', headers: { Cookie: 'bd.guest=tok' } },
        { path: '/api/remote/viewer/dev-1?guest=tok', session: viewer }, { path: '/api/remote/viewer/dev-1', headers: { Cookie: 'bd.guest=tok' }, session: viewer },
        { path: '/api/remote/viewer/dev-1?guest=tok', session: operator }, { path: '/api/remote/viewer/dev-1?guest=tok', session: accounts.pro },
        { path: '/api/remote/viewer/dev-1?t=tok&mesh_share=s' }, { path: '/api/remote/viewer/dev-1?mesh_share=s', headers: { Cookie: 'bd.guest=tok' } },
        { path: '/api/remote/viewer/rt_abcdefgh?guest=tok' },
    ];
    for (const link of links) {
        for (const request of guestRequests) {
            await send(request, { ...world, api: { ...world.api, ...target, [validate]: link, [share]: answer(200, { valid: true, view_only: true }) } });
        }
    }
    const shares = [answer(200, { valid: true }), answer(200, { valid: true, view_only: 'x' }), answer(200, { valid: false }), answer(200, 'null'), answer(200, '"s"'), answer(403, { valid: true }), { hang: true }];
    for (const a of shares) {
        for (const q of ['?mesh_share=s', '?mesh_share=%20s%20', '?mesh_share=', '?mesh_share=a&mesh_share=b', '?mesh_share[x]=1']) {
            await send({ path: `/api/remote/viewer/dev-1${q}` }, { ...world, api: { ...world.api, [share]: a } });
            await send({ path: `/api/remote/viewer/dev-1${q}`, session: viewer }, { ...world, api: { ...world.api, [share]: a } });
        }
    }

    // What the viewer shows about a device
    const peers = [
        { device_type: 'os_agent' }, { device_type: 'OS_AGENT', cdap_connected: true }, { device_type: 'mesh_agent', mesh_connected: true }, { device_type: 'mesh_agent', mesh_connected: false },
        { device_type: 'rustdesk', cdap_connected: 1 }, { device_type: 5 }, { device_type: ['os_agent'] }, { device_type: '' }, { mesh_connected: 'yes' }, {},
    ];
    for (const peer of peers) {
        for (const q of ['', '?transport=mesh', '?transport=CDAP', '?transport=rd', '?transport=other', '?transport=a&transport=b']) {
            await send({ path: `/api/remote/viewer/dev-1${q}`, session: operator }, { ...world, api: { ...world.api, ...peerOf('dev-1', { id: 'dev-1', ...peer }) } });
        }
    }
    await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, api: { ...health() } });
    await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, api: { ...health(), 'GET /api/peers/dev-1': answer(500, { error: 'x' }) } });
    const devices = [
        { id: 'dev-1', display_name: 'Display', hostname: 'Host', platform: 'Linux', os: 'Ubuntu' },
        { id: 'dev-1', hostname: '', platform: '', os: 'macOS' }, { id: 'dev-1', hostname: 0, platform: 5 }, { id: 'dev-1', hostname: ['a'], platform: { x: 1 } },
        { id: 'dev-1', name: 'only name' }, null, { throws: 'database is locked' },
    ];
    for (const device of devices) {
        await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, devices: { 'dev-1': device } });
    }
    const targets = [
        { id: 'rt_abcdefgh', name: 'Srv', protocol: 'VNC', enabled: true },
        { id: 'rt_abcdefgh', host: 'h', protocol: 'rdp', remote_protocol: 'x', username: 'u', credential_mode: 'stored', cert_fingerprint: 'ab:cd' },
        { id: 'rt_abcdefgh', protocol: '', platform: 'Windows' }, { id: 'rt_abcdefgh', protocol: 'RDP', enabled: false, tls_mode: 0 },
    ];
    for (const t of targets) {
        for (const session of [operator, admin, viewer, undefined]) {
            await send({ path: '/api/remote/viewer/rt_abcdefgh?transport=mesh', session }, { ...world, api: { ...health(), 'GET /api/remote-targets/rt_abcdefgh': answer(200, t) } });
        }
    }
    for (const a of [answer(404, { error: 'not found' }), answer(200, 'null'), { hang: true }]) {
        await send({ path: '/api/remote/viewer/rt_abcdefgh', session: operator }, { ...world, api: { ...health(), 'GET /api/remote-targets/rt_abcdefgh': a } });
    }
    for (const id of ['ab', 'login', 'a%20b', 'a.b', 'x'.repeat(65), 'x'.repeat(64), 'rt_', 'rt_a', 'RT_abcdefgh', '%E4%B8%96', 'dev-1%2F2', '..', 'dev_1']) {
        await send({ path: `/api/remote/viewer/${id}`, session: operator }, world);
        await send({ path: `/remote-cdap/${id}` });
        await send({ path: `/remote-desktop/${id}` });
        await send({ path: `/api/remote/session/${id}`, session: operator }, world);
    }

    // The server's key and the connection policy
    for (const key of ['abc', '', null, { throws: 'no key' }]) {
        await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, serverKey: key });
    }
    const connections = [{ p2p_first: false }, { relay_servers: 'r1,r2', extra: [1], p2p_fallback_ms: '5' }, [], ['a', 'b'], {}, null, 'text', 5, { 0: 'zero', z: 1 }];
    for (const c of connections) {
        await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, api: { ...world.api, ...health(c) } });
    }
    const healthFails = { 'GET /api/health/detail': answer(500, { error: 'x' }) };
    const modes = [
        undefined, { mode: 'relay_only', p2p_fallback_ms: 500, same_nat_relay: false, allow_shared_nat_initiator: true },
        { mode: 'p2p_first', p2p_fallback_ms: -1, same_nat_relay: true, allow_shared_nat_initiator: false }, { throws: 'unit unreadable' },
        { mode: 'p2p_first', p2p_fallback_ms: 0, same_nat_relay: true, allow_shared_nat_initiator: false },
    ];
    for (const mode of modes) {
        await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, connectionMode: mode, api: { ...peerOf('dev-1', {}), ...healthFails } });
        await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, connectionMode: mode, api: { ...peerOf('dev-1', {}), ...healthFails, 'GET /api/health': answer(200, { status: 'ok', connection: { p2p_first: false } }) } });
        await send({ path: '/api/remote/viewer/dev-1', session: operator }, { ...world, connectionMode: mode, api: { ...peerOf('dev-1', {}), ...healthFails, 'GET /api/health': { hang: true } } });
    }

    // The dev mock
    for (const mock of [undefined, '1', ' 1 ', '0', 'true', '11']) {
        await send({ path: '/dev/mock-remote.js' }, { mock });
        await send({ method: 'HEAD', path: '/dev/mock-remote.js', session: operator }, { mock });
        for (const id of ['dev-1', '900100001', '900100099', '9001000100', '950000000', '95000000']) {
            for (const q of ['', '?mock=1', '?mock=0', '?mock=1&scenario=SLOW', '?scenario=2fa', '?mock=1&scenario=nope', '?mock=1&scenario=', '?mock=1&mock=1', '?mock[]=1', '?mock=yes&scenario=ended']) {
                await send({ path: `/api/remote/viewer/${id}${q}`, session: operator }, { ...world, mock, devices: { [id]: { id } } });
            }
        }
    }
    await send({ path: '/dev/mock-remote.js', headers: { Range: 'bytes=0-9' } }, { mock: '1' });
    await send({ method: 'POST', path: '/dev/mock-remote.js', type: json, body: '{}' }, { mock: '1' });

    // The redirects
    for (const accept of [undefined, 'text/html', 'text/plain', 'application/json', 'image/png']) {
        const headers = accept ? { Accept: accept } : undefined;
        await send({ path: '/remote-dashboard', headers });
        await send({ method: 'HEAD', path: '/remote-dashboard', headers });
        await send({ path: '/remote-cdap/dev-1', headers });
        await send({ path: '/remote-desktop/a', headers });
    }
    await send({ path: '/remote-dashboard/' });
    await send({ path: '/REMOTE-DASHBOARD?x=1' });
    await send({ method: 'POST', path: '/remote-dashboard', type: json, body: '{}' });

    // The relay's sessions
    for (const relay of [{}, { sessions: [] }, { sessions: ['a', 'b'], states: { a: { agentConnected: false, viewerCount: 0, streaming: false } } }]) {
        for (const session of [undefined, viewer, operator, accounts.pro]) {
            await send({ path: '/api/remote/sessions', session }, { relay });
            await send({ path: '/api/remote/session/a', session }, { relay });
        }
    }

    // The agent's single-use token
    const auth = (token) => ({ Authorization: `Bearer ${token}` });
    const tokenRoute = '/api/bd/remote-agent-token';
    const bodies = [undefined, '{}', '{"device_id":"dev-a"}', '{"device_id":"dev-b"}', '{"device_id":""}', '{"device_id":0}', '{"device_id":null}', '{"device_id":false}',
        '{"device_id":["dev-a"]}', '{"device_id":{"a":1}}', '{"device_id":true}', '{"device_id":"DEV-A"}', '[]'];
    for (const body of bodies) {
        await send({ method: 'POST', path: tokenRoute, type: body === undefined ? undefined : json, body, headers: auth('tok-device-a') });
    }
    await send({ method: 'POST', path: tokenRoute, type: json, body: '{"device_id":123456}', headers: auth('tok-numeric') });
    await send({ method: 'POST', path: tokenRoute, type: json, body: '{"device_id":"123456"}', headers: auth('tok-numeric') });
    await send({ method: 'POST', path: tokenRoute, type: json, body: '{"device_id":[123456]}', headers: auth('tok-numeric') });
    await send({ method: 'POST', path: tokenRoute, type: json, body: '{}', headers: auth('tok-seeded') });
    for (const headers of [undefined, auth('nope'), auth('tok-unbound'), { Authorization: 'Basic dG9rLWRldmljZS1hOng=' }, { 'X-Device-Id': 'dev-a' }, { Authorization: 'bearer tok-device-a' },
        { ...auth('tok-device-a'), 'X-Device-Id': 'dev-b' }, { ...auth('tok-device-a'), 'X-Device-Id': 'dev-a' }]) {
        await send({ method: 'POST', path: tokenRoute, type: json, body: '{}', headers });
    }
    await send({ method: 'POST', path: `${tokenRoute}?device_id=dev-b`, type: json, body: '{}', headers: auth('tok-device-a') });
    await send({ method: 'POST', path: `${tokenRoute}?device_id=dev-a`, type: json, body: '{"device_id":"dev-a"}', headers: auth('tok-device-a') });
    await send({ method: 'POST', path: tokenRoute, type: 'application/x-www-form-urlencoded', body: 'device_id=dev-a', headers: auth('tok-device-a') });
    await send({ method: 'POST', path: tokenRoute, type: json, body: '{}', headers: auth('tok-device-a'), session: admin });
    await send({ method: 'POST', path: tokenRoute, type: json, body: '{}', session: admin });
    await send({ method: 'POST', path: '/API/BD/Remote-Agent-Token/', type: json, body: '{}', headers: auth('tok-device-a') });
    await send({ method: 'GET', path: tokenRoute, headers: auth('tok-device-a') });

    server.close();
    stub.close();
    server.closeAllConnections();
    stub.closeAllConnections();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().then(() => process.exit(0), (err) => {
    console.error(err);
    process.exit(1);
});
