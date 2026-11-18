'use strict';

/**
 * Records what routes/auth.routes.js answers for the account typeahead
 * (GET /login/lookup) and for sign-ins that the lockout check decides
 * (POST /api/auth/login), with the real database adapter, translations,
 * limiter and authService, against a made-up API server that answers 404.
 * See README.md.
 *
 *   node auth.js                                  ../lookup_sqlite.json, ../lockout_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node auth.js     ../lookup_postgres.json, ../lockout_postgres.json
 */

const fs = require('fs');
const http = require('http');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/spec/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const pg = Boolean(PG_DSN);
const dialect = pg ? 'postgres' : 'sqlite';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'auth-oracle-'));
const schema = `oracle_auth_${process.pid}_${Date.now() % 1000000}`;

const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

// The test password and its bcrypt hash with the lowest cost (the Go
// tests' passwordHash).
const PASSWORD = 'Auth-Test-1!pass';
const HASH = '$2b$04$dc.l7nhzKvP8b8PwtYT8l.FDZS7Yuc7QUJEihx.CgXH6mUu3MnMBy';

// ---- The accounts the typeahead looks through ----

const NAMES = [
    'admin', 'Admin2', 'ADMINISTRATOR', 'adm', 'ad', 'a', 'operator', 'Operator.Two', 'oper@example.com',
    'e2e_viewer', 'e2e-viewer', 'E2E_Admin', 'user.name', 'user_name', 'username', 'UserName2',
    'ΟΔΟΣ', 'ΟΣ.ΑΒ', 'ΣΑΣ', 'σας', 'ΑΣ́Β', 'İstanbul', 'istanbul', 'ISTANBUL', 'Straße', 'STRASSE',
    'KelvinK', 'ǅemal', 'ǄEMAL', 'ﬀoo', 'ⅠV', 'Ⓐbc', '\u{1F600}smile', 'smile\u{1F600}',
    'two words', ' padded', 'tab\tname', 'Ü̈ber', 'ÜBER', 'über', 'Éclair', 'ÉCLAIR', 'x'.repeat(70),
    'X'.repeat(64), 'zz', 'ZZtop', 'null', 'undefined', '0', '00', 'ǈj', 'ΑΣ',
];
for (let i = 0; i < 12; i++) NAMES.push(`op_${String(i).padStart(2, '0')}`);
const ROLES = ['admin', 'operator', null, '', 'viewer', 'super_admin', 'global_admin', 'pro'];

// ---- The made-up API server ----

function startApi() {
    const server = http.createServer((req, res) => {
        req.on('data', () => {});
        req.on('end', () => {
            res.writeHead(404, { 'Content-Type': 'application/json' });
            res.end('{"error":"not found"}');
        });
    });
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

// ---- The database ----

async function openDatabase() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    let admin = null;
    if (pg) {
        const { Pool } = web('node_modules/pg');
        admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE DATABASE ${schema}`);
        const url = new URL(PG_DSN);
        url.pathname = `/${schema}`;
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
    if (pg) {
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
    const close = async () => {
        try { await adapter.close(); } catch (_) { /* closed */ }
        if (pool) await pool.end();
        if (admin) {
            await admin.query(`DROP DATABASE ${schema} WITH (FORCE)`);
            await admin.end();
        }
    };
    return { exec, close };
}

// The time that is a number of seconds from now, by the database's clock.
const fromNow = (seconds) => (pg
    ? `NOW() + INTERVAL '${seconds} seconds'`
    : `strftime('%Y-%m-%d %H:%M:%f', 'now', '${seconds >= 0 ? '+' : ''}${seconds} seconds')`);

// ---- The lookup requests ----

const enc = encodeURIComponent;
function lookupQueries() {
    const out = new Set(['', 'q', 'q=', 'Q=ad', 'q[]=ad', 'q=ad&q=op', 'q[a]=ad', 'q[0]=ad', 'q=%', 'q=ad%', 'q=%E0%A4%A',
        'q=a+d', 'q=%20ad%20', 'q=+ad+', 'q=ad%00', 'q=op&x=1', 'x=1&q=op', 'q=OP', 'q=op_0', 'q=op_1', 'q=o',
        `q=${'x'.repeat(64)}`, `q=${'x'.repeat(65)}`, `q=${'x'.repeat(63)}`, `q=%20${'x'.repeat(64)}%20`,
        `q=${'X'.repeat(64)}`, `q=${enc('İ'.repeat(32))}`, `q=${enc('İ'.repeat(33))}`, `q=${enc('\u{1F600}'.repeat(32))}`,
        `q=${enc('\u{1F600}'.repeat(33))}`, `q=${enc('\u{1F600}')}`, `q=${enc('İ')}`]);
    const pads = ['', ' ', ' ', '﻿', ' ', '\t', '　', '​'];
    for (const name of NAMES) {
        const forms = [name, name.toUpperCase(), name.toLowerCase()];
        for (const form of forms) {
            const chars = Array.from(form);
            for (let k = 1; k <= Math.min(chars.length, 7); k++) {
                out.add(`q=${enc(chars.slice(0, k).join(''))}`);
            }
        }
        out.add(`q=${enc(name)}`);
        pads.forEach((pad, i) => out.add(`q=${enc(pad + Array.from(name).slice(0, 3).join('') + pads[(i + 3) % pads.length])}`));
    }
    for (const extra of ['ΟΣ', 'ος', 'οσ', 'ΣΑ', 'σα', 'Σ', 'ς', 'σ', 'i̇', 'i̇s', 'İS', 'ss', 'SS', 'ß', 'k', 'K',
        'K', 'ǆ', 'ǅ', 'Ǆ', 'ff', 'ⅰ', 'Ⅰ', 'ⓐ', 'Ⓐ', 'ü', 'ü', 'é', 'É', 'two w', 'two%20w', ' pad', 'pad',
        'tab\t', 'nu', 'un', '00', '0', 'ΑΣ', 'ας', 'ασ', 'ά', 'ǉ', 'ǈ']) {
        out.add(`q=${enc(extra)}`);
    }
    return [...out];
}

// ---- The sign-ins the lockout check decides ----

const lockoutBodies = () => {
    const steps = [];
    const at = (ip, body, label) => steps.push({ label, ip, body: typeof body === 'string' ? body : JSON.stringify(body) });
    let n = 0;
    const ip = () => { n += 1; return `10.30.${Math.floor(n / 250)}.${(n % 250) + 1}`; };
    at(ip(), { username: 'alice', password: PASSWORD }, 'locked account, right password');
    at(ip(), { username: 'alice', password: 'not-the-password' }, 'locked account, wrong password');
    at(ip(), { username: 'Alice', password: 'not-the-password' }, 'other letter case');
    at(ip(), { username: 'alice' }, 'locked account, no password');
    at(ip(), { username: 'alice', password: 'p'.repeat(129) }, 'locked account, long password');
    at(ip(), { username: 'bob', password: 'not-the-password' }, 'unlocked account');
    at(ip(), { username: 'expired', password: 'x' }, 'lockout that ended');
    at(ip(), { username: 'soon', password: 'x' }, 'end that is no time');
    for (const username of [12345, 1.5, 0.1, 1e21, -0, true, false, ['abc'], ['zzz'], [], ['a', 'b'], [null], [5], [['x']], [{}],
        {}, { a: 1 }, { length: 3 }, { length: 200 }, 'x'.repeat(129), 'x'.repeat(128), ' ', 'İ', 'ΟΔΟΣ']) {
        at(ip(), { username, password: 'x' }, `name ${JSON.stringify(username)}`);
    }
    for (const password of [12345, true, ['x'], {}]) {
        at(ip(), { username: 'alice', password }, `locked account, password ${JSON.stringify(password)}`);
        at(ip(), { username: 'bob', password }, `unlocked account, password ${JSON.stringify(password)}`);
    }
    at('198.51.100.7', { username: 'bob', password: PASSWORD }, 'address with 30 failures');
    at('198.51.100.7', { username: 'nobody', password: 'x' }, 'address with 30 failures, unknown name');
    at('198.51.100.8', { username: 'bob', password: 'x' }, 'address with 29 failures');
    at('198.51.100.8', { username: 'bob', password: 'x' }, 'address with 30 failures now');
    for (let i = 1; i <= 11; i++) at(ip(), { username: 'carol', password: `guess-${i}` }, `carol guess ${i}`);
    for (let i = 1; i <= 11; i++) at(ip(), { username: 12345.5, password: `guess-${i}` }, `number guess ${i}`);
    at(ip(), '[1,2]', 'list as body');
    at(ip(), '"text"', 'text as body');
    return steps;
};

// ---- Running ----

function send(port, method, target, body, headers = {}) {
    return new Promise((resolve, reject) => {
        const all = { Accept: 'application/json', 'User-Agent': 'auth-test/1', ...headers };
        let payload = null;
        if (body !== undefined) {
            payload = Buffer.from(body);
            all['Content-Type'] = 'application/json';
            all['Content-Length'] = payload.length;
        }
        const r = http.request({ host: '127.0.0.1', port, method, path: target, headers: all }, (res) => {
            const chunks = [];
            res.on('data', (c) => chunks.push(c));
            res.on('end', () => resolve({
                status: res.statusCode,
                retryAfter: res.headers['retry-after'] || '',
                body: Buffer.concat(chunks).toString('utf8'),
            }));
        });
        r.on('error', reject);
        if (payload) r.write(payload);
        r.end();
    });
}

(async () => {
    process.env.LOGIN_RATE_LIMIT_MAX = '1000000';
    const rejections = [];
    // recordAttempt runs without await; a value SQLite cannot store would
    // end the process. The oracle notes it and goes on.
    process.on('unhandledRejection', (err) => rejections.push(String(err && err.message)));
    const apiServer = await startApi();
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${apiServer.address().port}/api`;
    process.env.HBBS_API_URL = process.env.BETTERDESK_API_URL;
    process.env.SERVER_BACKEND = 'betterdesk';
    process.env.API_KEY_PATH = path.join(scratch, '.api_key');
    fs.writeFileSync(process.env.API_KEY_PATH, 'oracle-api-key');
    const db = await openDatabase();

    const express = web('node_modules/express');
    const cookieParser = web('node_modules/cookie-parser');
    const app = express();
    app.set('trust proxy', true);
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use(cookieParser());
    app.use((r, _res, next) => {
        r.session = { regenerate(cb) { cb(); }, destroy(cb) { if (cb) cb(); } };
        next();
    });
    app.use(web('middleware/i18n').initI18n());
    app.use('/', web('routes/auth.routes'));
    app.use((r, res) => res.status(404).json({ oracle: 'no route' }));
    // eslint-disable-next-line no-unused-vars
    app.use((err, r, res, _next) => res.status(err.status || 500).json({ success: false, error: 'Internal Server Error' }));
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;

    const realLog = console.log;
    console.error = () => {};
    console.log = () => {};
    console.warn = () => {};
    try {
        // The typeahead
        const seed = [['DELETE FROM users', []]];
        NAMES.forEach((name, i) => seed.push([
            'INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)',
            [i + 1, name, 'x', ROLES[i % ROLES.length]],
        ]));
        for (const [sql, params] of seed) await db.exec(sql, params);
        const lookups = [];
        for (const query of lookupQueries()) {
            const answer = await send(port, 'GET', `/login/lookup${query ? `?${query}` : ''}`);
            lookups.push({ query, status: answer.status, answer: answer.body });
        }
        const lookupFile = path.join(__dirname, '..', `lookup_${dialect}.json`);
        fs.writeFileSync(lookupFile, `${JSON.stringify({ dialect, seed, steps: lookups }, null, 1)}\n`);
        realLog(`${lookupFile}: ${lookups.length} requests`);

        // The lockouts
        const failure = pg ? 'false' : '0';
        const lockoutSeed = [
            ['DELETE FROM audit_log', []],
            ['DELETE FROM login_attempts', []],
            ['DELETE FROM account_lockouts', []],
            ['DELETE FROM users', []],
            ['INSERT INTO users (id, username, password_hash, role) VALUES (1, \'alice\', ?, \'operator\')', [HASH]],
            ['INSERT INTO users (id, username, password_hash, role) VALUES (2, \'bob\', ?, \'operator\')', [HASH]],
        ];
        const locked = ['alice', pg ? '12345' : '12345.0', 'abc', '{"abc"}', 'true', '{"a":1}', ' ', 'ΟΔΟΣ', '0.1'];
        for (const name of locked) {
            lockoutSeed.push([`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES (?, ${fromNow(600)}, 10)`, [name]]);
        }
        lockoutSeed.push([`INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES ('expired', ${fromNow(-60)}, 10)`, []]);
        if (!pg) lockoutSeed.push(['INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES (\'soon\', \'soon\', 10)', []]);
        for (let i = 0; i < 30; i++) {
            lockoutSeed.push([`INSERT INTO login_attempts (username, ip_address, success) VALUES (?, '198.51.100.7', ${failure})`, [`guess-${i}`]]);
        }
        for (let i = 0; i < 29; i++) {
            lockoutSeed.push([`INSERT INTO login_attempts (username, ip_address, success) VALUES (?, '198.51.100.8', ${failure})`, [`guess-${i}`]]);
        }
        lockoutSeed.push([`UPDATE login_attempts SET created_at = ${fromNow(-16 * 60)} WHERE username = 'guess-0' AND ip_address = '198.51.100.8'`, []]);
        lockoutSeed.push([`INSERT INTO login_attempts (username, ip_address, success) VALUES ('old', '198.51.100.8', ${failure})`, []]);
        for (const [sql, params] of lockoutSeed) await db.exec(sql, params);
        const steps = [];
        for (const step of lockoutBodies()) {
            rejections.length = 0;
            const answer = await send(port, 'POST', '/api/auth/login', step.body, { 'X-Forwarded-For': step.ip });
            await new Promise((resolve) => setTimeout(resolve, 30));
            steps.push({ ...step, status: answer.status, retryAfter: answer.retryAfter, answer: answer.body, rejections: [...rejections] });
        }
        const selects = {};
        for (const [name, sql] of [
            ['attempts', `SELECT username, ip_address, CASE WHEN ${pg ? 'success' : 'success = 1'} THEN 1 ELSE 0 END AS success FROM login_attempts ORDER BY id`],
            ['lockouts', 'SELECT username, attempt_count FROM account_lockouts ORDER BY username'],
            ['audit', 'SELECT action, details, ip_address FROM audit_log ORDER BY id'],
        ]) {
            selects[name] = { sql, rows: await db.exec(sql) };
        }
        const lockoutFile = path.join(__dirname, '..', `lockout_${dialect}.json`);
        fs.writeFileSync(lockoutFile, `${JSON.stringify({ dialect, seed: lockoutSeed, steps, selects }, null, 1)}\n`);
        realLog(`${lockoutFile}: ${steps.length} requests`);
    } finally {
        await new Promise((resolve) => server.close(resolve));
        apiServer.close();
        await db.close();
    }
    process.exit(0);
})().catch((err) => { process.stderr.write(`${err && err.stack}\n`); process.exit(1); });
