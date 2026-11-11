'use strict';

/**
 * Runs routes/rustdesk-api.routes.js of the Node console (web-nodejs) with
 * the real database adapter on a scratch database, behind a scripted API
 * server, and records what it answers. See README.md in this folder.
 *
 *   record({ seed, steps, out, api })
 *
 * The database is SQLite in a temp folder, or PostgreSQL in a schema of its
 * own when ORACLE_PG_DSN names a server. The recorded file is
 * <out>_sqlite.json or <out>_postgres.json beside this folder.
 */

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'rdapi-oracle-'));
const schema = `oracle_${process.pid}_${Date.now() % 1000000}`;

/** The accounts of the Go test (routes/internal/replay), by role. */
const USERS = {
    admin: { id: 1, username: 'e2e_admin', role: 'admin' },
    super_admin: { id: 2, username: 'e2e_super_admin', role: 'super_admin' },
    server_admin: { id: 3, username: 'e2e_server_admin', role: 'server_admin' },
    global_admin: { id: 4, username: 'e2e_global_admin', role: 'global_admin' },
    operator: { id: 5, username: 'e2e_operator', role: 'operator' },
    viewer: { id: 6, username: 'e2e_viewer', role: 'viewer' },
    guest_issuer: { id: 7, username: 'e2e_guest_issuer', role: 'guest_issuer' },
    pro: { id: 8, username: 'e2e_pro', role: 'pro' },
};

/** The address the Go test's requests come from. */
const CLIENT_IP = '203.0.113.7';

function web(rel) {
    return require(path.join(WEB, rel));
}

function rebind(sql) {
    let n = 0;
    return sql.replace(/\?/g, () => `$${++n}`);
}

function listen(server) {
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server.address().port)));
}

/** The scripted API server: answers by method and path, keeps the writes. */
async function startAPI(answer) {
    const state = { calls: [] };
    const server = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            const body = Buffer.concat(chunks).toString('utf8');
            if (req.method !== 'GET') state.calls.push({ method: req.method, url: req.url, body });
            const a = answer(req.method, req.url.split('?')[0], body) || { status: 404, body: '{"error":"not found"}' };
            res.writeHead(a.status, { 'Content-Type': 'application/json' });
            res.end(a.body);
        });
    });
    const port = await listen(server);
    return { port, state, close: () => { server.close(); server.closeAllConnections(); } };
}

async function openDatabase() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
    let admin = null;
    if (PG_DSN) {
        const { Pool } = web('node_modules/pg');
        admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        const url = new URL(PG_DSN);
        url.searchParams.set('options', `-c search_path=${schema}`);
        process.env.DB_TYPE = 'postgres';
        process.env.DATABASE_URL = url.toString();
    } else {
        process.env.DB_TYPE = 'sqlite';
        delete process.env.DATABASE_URL;
    }
    process.chdir(WEB);
    const adapter = web('services/dbAdapter').getAdapter();
    await adapter.init();

    let exec;
    let pool = null;
    if (PG_DSN) {
        const { Pool } = web('node_modules/pg');
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
        exec = async (sql, params = []) => (await pool.query(rebind(sql), params)).rows;
    } else {
        const raw = adapter.getSqliteMainDb();
        exec = async (sql, params = []) => {
            const stmt = raw.prepare(sql);
            if (stmt.reader) return stmt.all(...params);
            stmt.run(...params);
            return [];
        };
    }
    await exec('DELETE FROM users');
    for (const user of Object.values(USERS)) {
        await exec('INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [user.id, user.username, 'x', user.role]);
    }
    const close = async () => {
        try {
            await adapter.close();
        } catch (_) { /* closing twice is fine */ }
        if (pool) await pool.end();
        if (admin) {
            await admin.query(`DROP SCHEMA ${schema} CASCADE`);
            await admin.end();
        }
    };
    return { exec, close };
}

function send(port, step) {
    return new Promise((resolve, reject) => {
        const headers = { Accept: 'application/json', 'X-Forwarded-For': CLIENT_IP };
        if (step.role) headers['X-Oracle-Role'] = step.role;
        for (const [name, value] of Object.entries(step.headers || {})) headers[name] = value;
        let payload = null;
        if (step.body !== undefined) {
            payload = Buffer.from(step.body);
            if (!headers['Content-Type']) headers['Content-Type'] = 'application/json';
            headers['Content-Length'] = payload.length;
        }
        const req = http.request({ host: '127.0.0.1', port, method: step.method || 'GET', path: step.path, headers }, (res) => {
            const chunks = [];
            res.on('data', (c) => chunks.push(c));
            res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
        });
        req.on('error', reject);
        if (payload) req.write(payload);
        req.end();
    });
}

/**
 * @param {object} o
 * @param {Array<[string, any[]]>} o.seed statements with ? placeholders, run before the first step
 * @param {object[]} o.steps { name, role, method, path, body (text), headers }, or
 *        { name, sql, params }: a statement instead of a request, its rows are recorded
 * @param {string} o.out name of the recorded file, without dialect and extension
 * @param {function} o.api (method, path, body) => { status, body } of the API server
 */
async function record(o) {
    const api = await startAPI(o.api);
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${api.port}/api`;
    process.env.HBBS_API_URL = process.env.BETTERDESK_API_URL;
    const db = await openDatabase();
    for (const [sql, params] of o.seed || []) await db.exec(sql, params);

    const express = web('node_modules/express');
    const app = express();
    app.set('trust proxy', true);
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use((req, _res, next) => {
        const user = USERS[req.headers['x-oracle-role']];
        req.session = user ? { userId: user.id, user: { ...user } } : {};
        next();
    });
    app.use(web('routes/rustdesk-api.routes'));
    app.use((req, res) => res.status(404).json({ oracle: 'no route' }));
    // eslint-disable-next-line no-unused-vars
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ success: false, error: 'Internal Server Error' }));
    const server = http.createServer(app);
    const port = await listen(server);

    const realLog = console.log;
    const realWarn = console.warn;
    const realError = console.error;
    console.log = () => {};
    console.warn = () => {};
    console.error = () => {};
    const steps = [];
    try {
        for (const step of o.steps) {
            if (step.sql) {
                const rows = await db.exec(step.sql, step.params || []);
                steps.push({ ...step, rows: JSON.stringify(rows) });
                continue;
            }
            api.state.calls = [];
            const answer = await send(port, step);
            steps.push({
                ...step,
                status: answer.status,
                answer: answer.body.toString('utf8'),
                extra: { type: answer.headers['content-type'] || '', calls: api.state.calls },
            });
        }
    } finally {
        console.log = realLog;
        console.warn = realWarn;
        console.error = realError;
        await new Promise((resolve) => server.close(resolve));
        api.close();
        await db.close();
    }
    const file = path.join(__dirname, '..', `${o.out}_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed: o.seed || [], steps }, null, 1)}\n`);
    realLog(`${file}: ${steps.length} steps`);
}

module.exports = { record, USERS, dialect, web };
