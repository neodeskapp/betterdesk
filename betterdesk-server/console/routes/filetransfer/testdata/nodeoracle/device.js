'use strict';

// Vectors for the two routes a device calls with its token, POST
// /file-transfer and GET /file-transfer/:id of routes/fileTransfer.routes.js,
// at /api/files and at /api/bd as routes/index.js mounts the file, with the
// real device token checks (middleware/deviceAuth.js) on a scratch SQLite
// database, the real services/fileTransferService.js, and the routes for
// people of the same file in front of the same registry, so one transfer
// goes from start to download. Writes ../device_vectors.json.
//
// A vector is a request with the status, the headers that matter and the
// body of the answer, as in ../vectors.json. A request sent with a session
// names the role ("operator", "admin"); the Go test signs that role in.
// Ids are those of the Node run; the Go test maps each to the id its own
// service gave the same transfer.

const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const http = require('http');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { load } = require(path.join(oracleDir, 'lib'));
const { accounts } = require(path.join(oracleDir, 'harness'));

process.env.NODE_ENV = 'development';
process.env.DB_TYPE = 'sqlite';
process.env.DB_PATH = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'ft-device-')), 'db_v2.sqlite3');
process.env.DATA_DIR = path.dirname(process.env.DB_PATH);
delete process.env.DATABASE_URL;
process.env.UPLOAD_RATE_LIMIT_MAX = '100000000';
process.env.FILE_ACCESS_RATE_LIMIT_MAX = '100000000';

// The token rows, the same as in device_test.go: client id, expiry, revoked
const TOKENS = [
    ['tok-device-a', 'device-a', '2999-01-01 00:00:00', 0],
    ['tok-device-b', 'device-b', '2999-01-01 00:00:00', 0],
    ['tok-numeric', '123', '2999-01-01 00:00:00', 0],
    ['tok-unbound', '', '2999-01-01 00:00:00', 0],
    ['tok-expired', 'device-a', '2001-01-01 00:00:00', 0],
    ['tok-revoked', 'device-a', '2999-01-01 00:00:00', 1],
];
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');

const kept = ['content-type', 'content-disposition', 'content-length', 'accept-ranges', 'cache-control', 'content-range', 'etag', 'last-modified', 'x-content-type-options'];

async function main() {
    const db = load('./services/database');
    await db.init();
    const raw = load('./services/dbAdapter').getAdapter().getSqliteAuthDb();
    raw.prepare("INSERT INTO users (id, username, password_hash, role) VALUES (5, 'e2e_operator', 'x', 'operator')").run();
    for (const [token, client, expires, revoked] of TOKENS) {
        raw.prepare("INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, 5, ?, '', ?, ?)")
            .run(token, sha(token), client, expires, revoked);
    }

    const express = load('express');
    const { requireJsonContentType } = load('./lib/requireJsonContentType');
    const app = express();
    app.use(express.json({ limit: '2mb' }));
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use((req, _res, next) => {
        const role = req.headers['x-test-role'];
        req.session = role ? JSON.parse(JSON.stringify(accounts[role])) : {};
        req.session.destroy = () => {};
        next();
    });
    app.use(requireJsonContentType);
    const router = load('./routes/fileTransfer.routes');
    app.use('/api/files', router);
    app.use('/api/bd', router);
    app.use((req, res) => res.status(404).type('text/plain').send('no route'));
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ success: false, error: err.message }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const { port } = server.address();

    const vectors = [];
    function send(request) {
        return new Promise((resolve, reject) => {
            const headers = { ...(request.headers || {}) };
            if (request.role) headers['x-test-role'] = request.role;
            const body = request.body === undefined ? null : Buffer.from(request.body, 'utf8');
            if (body) {
                headers['Content-Type'] = request.type || 'application/json';
                headers['Content-Length'] = body.length;
            }
            const req = http.request({ host: '127.0.0.1', port, method: request.method || 'GET', path: request.path, headers }, (res) => {
                const chunks = [];
                res.on('data', (c) => chunks.push(c));
                res.on('end', () => {
                    const got = {};
                    for (const name of kept) if (res.headers[name] !== undefined) got[name] = res.headers[name];
                    const vector = { request: { method: request.method || 'GET', ...request }, status: res.statusCode, headers: got, body: Buffer.concat(chunks).toString('base64') };
                    vectors.push(vector);
                    resolve(vector);
                });
            });
            req.on('error', reject);
            if (body) req.write(body);
            req.end();
        });
    }
    const json = (vector) => JSON.parse(Buffer.from(vector.body, 'base64').toString('utf8'));
    const auth = (token) => ({ Authorization: `Bearer ${token}` });
    const A = auth('tok-device-a');
    const B = auth('tok-device-b');
    const post = (body, headers = A, mount = '/api/bd', more = {}) => send({ method: 'POST', path: `${mount}/file-transfer`, headers, body: typeof body === 'string' ? body : JSON.stringify(body), ...more });
    const get = (id, headers = A, mount = '/api/bd', more = {}) => send({ path: `${mount}/file-transfer/${id}`, headers, ...more });
    const chunk = (id, index, text, headers = A, mount = '/api/bd') => post({ action: 'chunk', transfer_id: id, chunk_index: index, data: Buffer.from(text).toString('base64') }, headers, mount);

    // Who may call, at both mounts
    for (const mount of ['/api/bd', '/api/files']) {
        for (const headers of [{}, { Authorization: 'Basic tok-device-a' }, auth('nope'), auth('tok-unbound'), auth('tok-expired'), auth('tok-revoked'),
            { 'X-Device-Id': 'device-a' }, { Authorization: 'bearer tok-device-a' }, { Authorization: 'Bearer  tok-device-a' }]) {
            await post({ action: 'browse', path: '/' }, headers, mount);
            await get('nope', headers, mount);
        }
        await post({ action: 'browse', path: '/' }, {}, mount, { role: 'admin' });
        await post({ action: 'browse', path: '/' }, A, mount, { role: 'admin' });
        await get('nope', {}, mount, { role: 'admin' });
    }

    // The device it names
    await post({ action: 'browse', device_id: 'device-b' });
    await post({ action: 'browse', device_id: 'device-a' });
    await post({ action: 'browse' }, { ...A, 'X-Device-Id': 'device-b' });
    await post({ action: 'browse', device_id: '' }, { ...A, 'X-Device-Id': 'device-b' });
    await send({ method: 'POST', path: '/api/bd/file-transfer?device_id=device-b', headers: A, body: '{"action":"browse"}' });
    await post({ action: 'browse', device_id: 123 }, auth('tok-numeric'));
    await post({ action: 'browse', device_id: ['device-a'] });

    // Every action, and none
    const messages = [
        {}, [], ['init'], { action: 'nope' }, { action: 5 }, { action: ['init'] }, { action: null }, { action: '' },
        { action: 'browse' }, { action: 'browse', path: 'C:\\Users' }, { action: 'browse', path: { deep: [1] } },
        { action: 'cancel' }, { action: 'cancel', transfer_id: 'nope' }, { action: 'cancel', transfer_id: 7 },
        { action: 'chunk' }, { action: 'chunk', transfer_id: 'nope', data: 'aGk=' },
        { action: 'init', size: 4294967297 }, { action: 'init', filename: { toString: 1 } },
    ];
    for (const message of messages) await post(message);
    await post('action=init&filename=form.txt', A, '/api/bd', { type: 'application/x-www-form-urlencoded' });

    // A device announces a file and sends it; the console downloads it
    const announced = json(await post({ action: 'init', filename: '../reports/q3 report.pdf', size: 12, mime_type: 'application/pdf' })).transfer_id;
    await post({ action: 'init' });
    await post({ action: 'init', filename: 'size-as-text.bin', size: '7', mime_type: 0 });
    await get(announced);
    await get(announced, B);
    await get(announced, auth('tok-numeric'));
    await get(announced, A, '/api/files');
    // A read is not checked against the device the request names
    await send({ path: `/api/bd/file-transfer/${announced}?device_id=device-b`, headers: { ...A, 'X-Device-Id': 'device-b' } });
    await send({ method: 'HEAD', path: `/api/bd/file-transfer/${announced}`, headers: A });
    await send({ path: `/API/BD/File-Transfer/${announced}/`, headers: A });
    await chunk(announced, 0, 'hello ');
    await send({ path: `/api/files/transfers/${announced}`, role: 'operator' });
    await chunk(announced, 1, 'world!');
    await get(announced);
    await send({ path: `/api/files/transfers/${announced}`, role: 'operator' });
    await send({ path: `/api/files/transfers/${announced}/download`, role: 'operator' });
    await send({ path: '/api/files/transfers?device_id=device-a', role: 'operator' });

    // Another device writes into the transfer and cancels it (BUGS FT2)
    await chunk(announced, 2, '!!', B);
    await post({ action: 'cancel', transfer_id: announced }, B);
    await get(announced);
    await chunk(announced, 3, 'late');
    await send({ path: `/api/files/transfers/${announced}`, role: 'operator' });

    // Someone at the console starts a transfer; the device answers it
    const started = json(await send({ method: 'POST', path: '/api/files/transfer', role: 'operator', body: JSON.stringify({ direction: 'download', device_id: 'device-a', filename: 'log.txt', size: 5 }) })).id;
    await get(started);
    await get(started, B);
    await chunk(started, 0, 'hel', A, '/api/files');
    await chunk(started, 1, 'lo');
    await get(started, A, '/api/files');
    await send({ path: `/api/bd/transfers/${started}`, role: 'admin' });
    await send({ path: `/api/files/transfers/${started}/download`, role: 'admin' });
    await post({ action: 'cancel', transfer_id: started });
    await send({ path: `/api/files/transfers/${started}/download`, role: 'admin' });

    // A transfer for a device whose id is a number is no device's
    const numbered = json(await send({ method: 'POST', path: '/api/bd/transfer', role: 'admin', body: JSON.stringify({ direction: 'upload', device_id: 123, filename: 'n.txt', size: 1 }) })).id;
    await get(numbered, auth('tok-numeric'));
    await chunk(numbered, 0, 'x', auth('tok-numeric'));
    await send({ path: `/api/files/transfers/${numbered}`, role: 'admin' });

    server.close();
    server.closeAllConnections();
    const service = load('./services/fileTransferService');
    for (const id of [announced, started, numbered]) {
        const t = service.getTransfer(id);
        if (t) fs.rmSync(t.tempPath, { force: true });
    }
    fs.writeFileSync(path.join(__dirname, '..', 'device_vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`device_vectors.json: ${vectors.length} vectors`);
    process.exit(0);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
