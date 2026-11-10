'use strict';

// Vectors for the route devices fetch their resource policy from: GET
// /api/bd/resource-policy of routes/resource-control.routes.js, mounted
// at / and at /api/bd as routes/index.js mounts it, with the real device
// token checks on a scratch SQLite database and the real API client in
// front of a scripted API server. Writes ../device_vectors.json.

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
const ok = { status: 200, type: json, body: '{"usb":{"mode":"block"},"quotas":[]}' };
const auth = (token) => ({ Authorization: `Bearer ${token}` });
const A = auth('tok-device-a');

async function main() {
    process.env.DB_TYPE = 'sqlite';
    process.env.DB_PATH = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'resources-device-')), 'db_v2.sqlite3');
    delete process.env.DATABASE_URL;
    const oracle = await start((app, loadRoute) => {
        const router = loadRoute('./routes/resource-control.routes');
        app.use('/', router);
        app.use('/api/bd', router);
    });
    await tokens();
    const vectors = [];
    const send = async (request, answer = ok) => vectors.push(await oracle.call(request, answer));
    // A GET with a body: the match looks at the body first
    const own = { type: json, body: '{"device_id":"device-a"}' };

    for (const mount of ['', '/api/bd']) {
        const p = `${mount}/api/bd/resource-policy`;
        const get = (more) => send({ method: 'GET', path: p, headers: A, ...more });
        await get();
        await get({ headers: undefined });
        await get({ headers: { 'X-Device-Id': 'device-a' } });
        await get({ headers: auth('nope') });
        await get({ headers: auth('tok-unbound') });
        await get({ headers: undefined, session: accounts.admin });
        await get({ path: `${p}?device_id=device-a` });
        await get({ path: `${p}?device_id=device-b` });
        await get({ headers: { ...A, 'X-Device-Id': 'device-a' } });
        await get({ headers: { ...A, 'X-Device-Id': 'device-b' } });
        await get({ headers: auth('tok-numeric') });
        // The query names the device, the header another one: the header counts here
        await get({ path: `${p}?device_id=device-a`, headers: { ...A, 'X-Device-Id': 'device-b' } });
        await get({ path: `${p}?device_id=device-a`, headers: { ...A, 'X-Device-Id': 'bad id' } });
        await get({ path: `${p}?device_id=device-a`, headers: { ...A, 'X-Device-Id': '' } });
        await get({ path: `${p}?device_id=device-a`, headers: { ...A, 'X-Device-Id': ' device-c ' } });
        await get({ path: `${p}?device_id=device-a`, headers: { ...A, 'X-Device-Id': 'a:b@c' } });
        // The body names the device, the query another one
        await get({ ...own, path: `${p}?device_id=device-c` });
        await get({ ...own, path: `${p}?device_id[]=device-c` });
        await get({ ...own, path: `${p}?device_id[]=a&device_id[]=b` });
        await get({ ...own, path: `${p}?device_id[x]=device-c` });
        await get({ ...own, path: `${p}?device_id[toString]=x` });
        await get({ ...own, path: `${p}?device_id=%2E%2E` });
        await get({ ...own, path: `${p}?device_id=${'d'.repeat(257)}` });
    }
    for (const answer of apiAnswers) await send({ method: 'GET', path: '/api/bd/resource-policy', headers: A }, answer);
    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'device_vectors.json'), JSON.stringify(vectors));
    console.log(`device_vectors.json: ${vectors.length} vectors`);
}

main().then(() => process.exit(0), (err) => {
    console.error(err);
    process.exit(1);
});
