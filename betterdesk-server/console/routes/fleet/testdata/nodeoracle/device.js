'use strict';

// Vectors for the two routes agents report to: POST
// /api/bd/fleet/task-result and /api/bd/fleet/software of
// routes/fleet.routes.js, mounted at / and at /api/bd as routes/index.js
// mounts it, with the real device token checks on a scratch SQLite
// database and the real API client in front of a scripted API server.
// Writes ../device_vectors.json.

const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));
const { load } = require(path.join(oracleDir, 'lib'));

// The token rows, the same as in device_test.go
const TOKENS = [['tok-device-a', 'device-a'], ['tok-device-b', 'device-b'], ['tok-numeric', '123'], ['tok-unbound', '']];
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');

async function tokens() {
    const db = load('./services/database');
    await db.init();
    const raw = load('./services/dbAdapter').getAdapter().getSqliteAuthDb();
    raw.prepare("INSERT INTO users (id, username, password_hash, role) VALUES (1, 'device-owner', 'x', 'operator')").run();
    for (const [token, client] of TOKENS) {
        raw.prepare("INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at) VALUES (?, ?, 1, ?, '', '2999-01-01 00:00:00')").run(token, sha(token), client);
    }
}

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true}' };
const auth = (token) => ({ Authorization: `Bearer ${token}` });

async function main() {
    process.env.DB_TYPE = 'sqlite';
    process.env.DB_PATH = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'fleet-device-')), 'db_v2.sqlite3');
    delete process.env.DATABASE_URL;
    const oracle = await start((app, loadRoute) => {
        const router = loadRoute('./routes/fleet.routes');
        app.use('/', router);
        app.use('/api/bd', router);
    });
    await tokens();
    const vectors = [];
    const send = async (request, answer = ok) => vectors.push(await oracle.call(request, answer));
    const report = '{"device_id":"device-a","task_id":"t-1","status":"done","output":"ok","exit_code":0}';

    for (const mount of ['', '/api/bd']) {
        for (const route of ['/api/bd/fleet/task-result', '/api/bd/fleet/software']) {
            const p = mount + route;
            const post = (more) => send({ method: 'POST', path: p, type: json, body: report, headers: auth('tok-device-a'), ...more });
            await post();
            // Who may report
            await post({ headers: undefined });
            await post({ headers: { 'X-Device-Id': 'device-a' } });
            await post({ headers: auth('nope') });
            await post({ headers: auth('tok-unbound') });
            await post({ headers: { Authorization: 'bearer tok-device-a' } });
            await post({ headers: undefined, session: accounts.admin });
            await post({ session: accounts.admin });
            // The device it names
            await post({ body: '{"device_id":"device-b","task_id":"t-1"}' });
            await post({ body: '{"task_id":"t-1"}' });
            await post({ body: '{"task_id":"t-1"}', headers: { ...auth('tok-device-a'), 'X-Device-Id': 'device-b' } });
            await post({ path: `${p}?device_id=device-b`, body: '{"task_id":"t-1"}' });
            await post({ path: `${p}?device_id=device-b`, body: report });
            await post({ body: '{"device_id":123}', headers: auth('tok-numeric') });
            await post({ body: '{"device_id":["device-a"]}' });
            await post({ body: '{"device_id":{"toString":1}}' });
            // What goes on to the API server
            await post({ body: '["device-a"]' });
            await post({ body: '{}' });
            await post({ body: undefined, type: undefined });
            await post({ body: '{"device_id":"device-a","__proto__":{"x":1},"constructor":2,"nested":{"prototype":3,"keep":4},"list":[{"__proto__":5}]}' });
            await post({ body: '{"device_id":"device-a","n":1.50,"big":12345678901234567890,"u":"\\u00e9\\u2028"}' });
            await post({ body: 'device_id=device-a', type: 'application/x-www-form-urlencoded' });
        }
    }
    // Every kind of answer of the API server
    for (const route of ['/api/bd/fleet/task-result', '/api/bd/fleet/software']) {
        for (const answer of apiAnswers) {
            await send({ method: 'POST', path: route, type: json, body: report, headers: auth('tok-device-a') }, answer);
        }
    }
    // Other methods are no route
    await send({ method: 'GET', path: '/api/bd/fleet/task-result', headers: auth('tok-device-a') });
    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'device_vectors.json'), JSON.stringify(vectors));
    console.log(`device_vectors.json: ${vectors.length} vectors`);
}

main().then(() => process.exit(0), (err) => {
    console.error(err);
    process.exit(1);
});
