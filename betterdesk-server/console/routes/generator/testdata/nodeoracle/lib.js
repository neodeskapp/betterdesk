'use strict';

/**
 * Runs a route file of the Node console (web-nodejs) with the real database
 * adapter on a scratch database and records what it answers. See README.md
 * in this folder.
 *
 *   record({ routes, mounts, seed, steps, out, prepare, keep })
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
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'route-oracle-'));
const schema = `oracle_${process.pid}_${Date.now() % 1000000}`;

/** The accounts a step can act as, by role. Ids as the Go test gives them. */
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

function web(rel) {
    return require(path.join(WEB, rel));
}

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

function rebind(sql) {
    let n = 0;
    return sql.replace(/\?/g, () => `$${++n}`);
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
    // The accounts of USERS, with the ids the Go test gives them
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
    return { adapter, exec, close };
}

function send(port, step) {
    return new Promise((resolve, reject) => {
        const headers = { Accept: 'application/json' };
        if (step.role) headers['X-Oracle-Role'] = step.role;
        for (const [name, value] of Object.entries(step.headers || {})) headers[name] = value;
        let payload = null;
        if (step.bodyBase64 !== undefined || step.body !== undefined) {
            payload = step.bodyBase64 !== undefined ? Buffer.from(step.bodyBase64, "base64") : Buffer.from(step.body);
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
 * @param {string} o.routes   route file below web-nodejs, e.g. 'routes/tenants.routes'
 * @param {string[]} o.mounts where the console mounted it
 * @param {Array<[string, any[]]>} [o.seed] statements with ? placeholders, run before the first step
 * @param {object[]} o.steps  { name, role, method, path, body (text), headers }, or
 *        { name, sql, params }: a statement instead of a request, its rows are recorded
 * @param {string} o.out      name of the recorded file, without dialect and extension
 * @param {function} [o.prepare] called with { web, stub, scratch, WEB, dialect } before the route file loads
 * @param {function} [o.keep] (step, answer) => more fields to record for a step
 * @param {object} [o.vars] texts to put in place of marks in statement parameters, e.g. { '<UPLOADS>': dir }
 * @param {function} [o.finish] called after the last step, before the database closes
 */
async function record(o) {
    const db = await openDatabase();
    if (o.prepare) await o.prepare({ web, stub, scratch, WEB, dialect, exec: db.exec });
    const vars = o.vars || {};
    const filled = (params) => (params || []).map((p) => (typeof p === 'string'
        ? Object.keys(vars).reduce((s, mark) => s.split(mark).join(vars[mark]), p) : p));
    for (const [sql, params] of o.seed || []) await db.exec(sql, filled(params));

    const express = web('node_modules/express');
    const app = express();
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use(web('middleware/i18n').initI18n());
    app.use((req, res, next) => {
        if (!req.path.match(/\.(js|css|png|jpg|jpeg|gif|svg|ico|woff2?|ttf|eot|map|proto)$/)) res.set('Cache-Control', 'no-cache, no-store, must-revalidate');
        next();
    });
    app.use(web('lib/requireJsonContentType').requireJsonContentType);
    app.use((req, _res, next) => {
        const user = USERS[req.headers['x-oracle-role']];
        req.session = user ? { userId: user.id, user: { ...user } } : {};
        next();
    });
    const router = web(o.routes);
    for (const mount of o.mounts) app.use(mount, router);
    app.use((req, res) => res.status(404).json({ oracle: 'no route' }));
    // eslint-disable-next-line no-unused-vars
    // The console's error handler, as it answers the API in production
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ success: false, error: 'Internal Server Error' }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;

    const realError = console.error;
    const realLog = console.log;
    console.error = () => {};
    console.log = () => {};
    const steps = [];
    const ids = {};
    try {
        for (const step of o.steps) {
            if (step.sql) {
                const rows = await db.exec(step.sql, filled(step.params));
                steps.push({ ...step, rows: JSON.stringify(rows) });
                continue;
            }
            if (step.path) step.path = step.path.replace(/<ID:([^>]+)>/g, (m, slug) => ids[slug] || 'unknown');
            const answer = await send(port, step);
            try {
                const b = JSON.parse(answer.body.toString('utf8'));
                const bundle = b && b.data && b.data.bundle;
                if (bundle && bundle.slug) ids[bundle.slug] = bundle.bundle_id;
            } catch (_) { /* not JSON */ }
            const text = answer.body.toString('utf8');
            const recorded = { ...step, status: answer.status, answer: o.scrub ? o.scrub(text) : text };
            if (o.keep) recorded.extra = o.keep(step, answer) || {};
            steps.push(recorded);
        }
        // What finish returns is recorded as steps after the last request
        const more = o.finish ? await o.finish({ exec: db.exec }) : null;
        if (more) steps.push(...more);
    } finally {
        console.error = realError;
        console.log = realLog;
        await new Promise((resolve) => server.close(resolve));
        await db.close();
    }
    const file = path.join(__dirname, '..', `${o.out}_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed: o.seed || [], steps }, null, 1)}\n`);
    realLog(`${file}: ${steps.length} steps`);
}

module.exports = { record, USERS, dialect, WEB };
