'use strict';

/**
 * Runs the Node console's routes/avatars.routes.js on a scratch SQLite
 * database with pictures in it and records every answer, for the Go
 * package console/routes/avatars to replay. See README.md in this folder.
 *
 *   node routes.js      writes ../routes.json.gz
 *
 * The requests are sent as the signed-in user 2. The route's rate limiter
 * is replaced by one that lets everything through: its headers belong to
 * the limiter, not to the route.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const http = require('http');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'avatar-routes-oracle-'));
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = scratch;
process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
process.env.DB_TYPE = 'sqlite';
delete process.env.DATABASE_URL;
process.chdir(WEB);

const limiterFile = require.resolve(path.join(WEB, 'middleware/rateLimiter'));
const realLimiter = require(limiterFile);
require.cache[limiterFile].exports = { ...realLimiter, panelReadLimiter: (_req, _res, next) => next() };

const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC', 'base64');
const GIF = Buffer.from('GIF89a\x01\x00\x01\x00\x00\x00\x00;', 'latin1');
const JPEG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 0x4a, 0x46, 0x49, 0x46, 0, 1]);
const WEBP = Buffer.concat([Buffer.from('RIFF'), Buffer.from([0x1a, 0, 0, 0]), Buffer.from('WEBPVP8 ')]);

// Users 1 to 9 and the picture rows the scenario starts from. A row's
// version is free text here, to show the route sends what is stored.
const USERS = ['alice', 'bob', 'carol', 'dave', 'erin', 'frank', 'gina', 'hugo', 'ines'];
const PICTURES = [
    [1, 'image/png', PNG, 'c47dd9465c00e9a0', 'oidc', ''],
    [3, 'IMAGE/GIF', GIF, '1f19970f056cd116', 'oidc', ''],
    [4, 'image/svg+xml', Buffer.from('<svg/>'), 'aaaaaaaaaaaaaaaa', 'oidc', ''],
    [5, 'image/png', Buffer.alloc(0), 'bbbbbbbbbbbbbbbb', 'oidc', ''],
    [6, 'image/webp', WEBP, 'abc', 'profile', 'http://pictures.example/frank.webp'],
    [7, 'image/jpeg', JPEG, 'with space', 'oidc', ''],
    [8, '', PNG, 'cccccccccccccccc', 'oidc', ''],
    [9, 'image/png', PNG, '', 'oidc', ''],
];

function send(port, method, target, headers) {
    return new Promise((resolve, reject) => {
        const req = http.request({ host: '127.0.0.1', port, method, path: target, headers, agent: false }, (res) => {
            const chunks = [];
            res.on('data', (c) => chunks.push(c));
            res.on('end', () => {
                const kept = {};
                for (const [name, value] of Object.entries(res.headers)) {
                    if (['date', 'connection', 'keep-alive', 'x-powered-by', 'set-cookie'].includes(name)) continue;
                    kept[name] = value;
                }
                resolve({ status: res.statusCode, headers: kept, body: Buffer.concat(chunks).toString('base64') });
            });
        });
        req.on('error', reject);
        req.end();
    });
}

async function main() {
    const config = require(path.join(WEB, 'config/config'));
    const db = require(path.join(WEB, 'services/database'));
    const { getAdapter } = require(path.join(WEB, 'services/dbAdapter'));
    const quiet = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {};
    console.warn = () => {};
    await db.init();
    const sqlite = getAdapter(config).getSqliteAuthDb();
    for (const name of USERS) sqlite.prepare('INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)').run(name, 'x', 'viewer');
    for (const row of PICTURES) {
        sqlite.prepare('INSERT INTO user_avatars (user_id, mime_type, data, version, source, source_url) VALUES (?, ?, ?, ?, ?, ?)').run(...row);
    }

    const { createTestApp } = require(path.join(WEB, 'tests/helpers'));
    const routes = require(path.join(WEB, 'routes/avatars.routes'));
    const app = createTestApp();
    app.use((req, _res, next) => { req.session.userId = 2; req.session.user = { id: 2, username: 'bob', role: 'viewer' }; next(); });
    app.use('/', routes);
    const anon = createTestApp();
    anon.use('/', routes);
    const servers = { user: app.listen(0, '127.0.0.1'), anon: anon.listen(0, '127.0.0.1') };
    await new Promise((r) => setTimeout(r, 100));

    const requests = [];
    const add = (method, target, headers = {}, as = 'user') => requests.push({ as, method, target, headers });
    add('GET', '/api/users/avatars');
    add('HEAD', '/api/users/avatars');
    add('GET', '/api/users/avatars', {}, 'anon');
    add('GET', '/api/users/1/avatar', {}, 'anon');
    for (let id = 1; id <= 10; id++) {
        add('GET', `/api/users/${id}/avatar`);
        add('HEAD', `/api/users/${id}/avatar`);
    }
    for (const id of ['0', '01', '0000000001', '00000000001', '9999999999', 'x', '1x', '-1', '1.0', '0x1', '%31', '1%0A', '1%20', '+1', '%D9%A1']) {
        add('GET', `/api/users/${id}/avatar`);
    }
    const version = { 1: 'c47dd9465c00e9a0', 3: '1f19970f056cd116', 6: 'abc', 7: 'with space', 9: '' };
    for (const id of [1, 3, 6, 7, 9]) {
        const v = version[id];
        const enc = encodeURIComponent(v);
        for (const query of [`v=${enc}`, `v=${enc}x`, 'v=', 'v', `v=${enc}&v=${enc}`, `v[]=${enc}`, `v[x]=${enc}`, `x=1&v=${enc}`, `V=${enc}`,
            `%76=${enc}`, `v=${enc.replace(/%20/g, '+')}`, `v=${enc}&v[]=1`, `w=${enc}`, `v=${enc.toUpperCase()}`, '']) {
            add('GET', `/api/users/${id}/avatar?${query}`);
        }
        for (const inm of [`"${v}"`, `W/"${v}"`, `"x", "${v}"`, `"${v}" ,"y"`, `"x","y"`, '*', v, `w/"${v}"`, `W/W/"${v}"`, ` "${v}" `,
            `\t"${v}"`, `"${v}" `, `"${v.toUpperCase()}"`, `"${v}`, `'${v}'`, '']) {
            add('GET', `/api/users/${id}/avatar`, { 'If-None-Match': inm });
        }
        add('HEAD', `/api/users/${id}/avatar`, { 'If-None-Match': `"${v}"` });
        add('GET', `/api/users/${id}/avatar?v=${enc}`, { 'If-None-Match': `"${v}"` });
        add('GET', `/api/users/${id}/avatar`, { 'If-None-Match': ['"x"', `"${v}"`] });
        add('GET', `/api/users/${id}/avatar`, { 'If-Modified-Since': 'Wed, 21 Oct 2099 07:28:00 GMT' });
        add('GET', `/api/users/${id}/avatar`, { Range: 'bytes=0-3' });
    }
    add('GET', '/api/users/2/avatar', { 'If-None-Match': '"c47dd9465c00e9a0"' });
    add('GET', '/api/users/4/avatar', { 'If-None-Match': '"aaaaaaaaaaaaaaaa"' });
    add('GET', '/API/Users/1/Avatar/');
    add('GET', '/api/users/1/avatar/');

    const results = [];
    for (const request of requests) {
        const headers = {};
        for (const [name, value] of Object.entries(request.headers)) {
            headers[name] = Array.isArray(value) ? value : Buffer.from(value, 'latin1').toString('latin1');
        }
        results.push({ ...request, answer: await send(servers[request.as].address().port, request.method, request.target, headers) });
    }
    Object.assign(console, quiet);

    const pictures = PICTURES.map(([userId, mime, data, v, source, sourceUrl]) => ({ userId, mime, data: data.toString('base64'), version: v, source, sourceUrl }));
    const file = path.join(OUT, 'routes.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify({ users: USERS, pictures, requests: results }), { level: 9 }));
    const byStatus = {};
    for (const r of results) byStatus[r.answer.status] = (byStatus[r.answer.status] || 0) + 1;
    console.log(`${results.length} requests: ${JSON.stringify(byStatus)}`);

    servers.user.close();
    servers.anon.close();
    await db.close();
    fs.rmSync(scratch, { recursive: true, force: true });
}

main().then(() => process.exit(0), (err) => { console.error(err); process.exit(1); });
