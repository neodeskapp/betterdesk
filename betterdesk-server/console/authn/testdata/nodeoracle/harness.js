'use strict';

// What db.js runs its scenarios on: the Node console's auth service on a
// real database, with everything around it under control.
//
//   - The Go server is an HTTP listener in this process. A step says what
//     it answers, and every request it gets is recorded.
//   - The clock is fixed per step (Date.now() and new Date()).
//   - crypto.randomBytes returns a repeatable stream per step, so hashes,
//     tokens and codes can be compared letter by letter.
//   - bcrypt hashes with cost 4 instead of 12. Nothing else about bcrypt
//     changes; cost 12 is covered by pure.js.
//   - The logger records the lines as the service passes them, before the
//     console's logger shortens names in them.

const http = require('http');
const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const lib = require('./lib');
const { WEB, web } = lib;

const RealDate = Date;
const realSetTimeout = global.setTimeout;
const realRandomBytes = crypto.randomBytes;
// How long the service waits for the Go server. Long enough that a busy
// machine does not make an answered request look like a timeout; a step
// where that happens anyway is noticed and its scenario run again.
const API_TIMEOUT_MS = 400;

// ---- clock

let fakeNow = null;
class FakeDate extends RealDate {
    constructor(...args) {
        if (args.length === 0 && fakeNow !== null) super(fakeNow);
        else super(...args);
    }
    static now() {
        return fakeNow !== null ? fakeNow : RealDate.now();
    }
}
global.Date = FakeDate;

// ---- random

// The stream of a seed: mulberry32, one byte per number (its top 8 bits).
function stream(seed) {
    let a = seed >>> 0;
    return () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) >>> 24;
    };
}

let nextByte = null;
// The sizes the auth service asks for: codes, salts, secrets, tokens. Any
// other size is somebody else's (the PostgreSQL driver) and stays random.
const OWN_SIZES = new Set([4, 16, 20, 32]);
crypto.randomBytes = (size, cb) => {
    if (nextByte === null || !OWN_SIZES.has(size)) return realRandomBytes(size, cb);
    const out = Buffer.alloc(size);
    for (let i = 0; i < size; i++) out[i] = nextByte();
    if (cb) {
        process.nextTick(cb, null, out);
        return undefined;
    }
    return out;
};

// ---- bcrypt at cost 4

const bcryptPath = require.resolve(path.join(WEB, 'node_modules/bcrypt'));
const realBcrypt = require(bcryptPath);
require.cache[bcryptPath].exports = {
    ...realBcrypt,
    hash: (data, rounds, cb) => realBcrypt.hash(data, typeof rounds === 'number' ? 4 : rounds, cb),
};

// ---- logger

let logs = [];
const recorder = {};
for (const level of ['error', 'warn', 'info', 'debug']) {
    recorder[level] = (...args) => logs.push([level, ...args.map((a) => String(a))]);
}
const loggerPath = require.resolve(path.join(WEB, 'lib/logger'));
require(loggerPath);
require.cache[loggerPath].exports = { level: 'info', ...recorder, child: () => recorder };

// ---- the Go server

const DEFAULT_SSO = { status: 200, body: { ldap_enabled: false, oidc_enabled: false, any_enabled: false } };
const REFUSED = { status: 401, body: { error: 'Invalid credentials' } };
let script = { sso: DEFAULT_SSO, login: [], ldap: [] };
let calls = [];
let hanging = [];
// What the listener answered and what the service got, to notice a
// request that was answered too late for the service's timeout.
let answered = 0;
let sent = [];

const realRequest = http.request;
http.request = function request(...args) {
    const req = realRequest.apply(this, args);
    const entry = { responded: false };
    sent.push(entry);
    req.on('response', () => { entry.responded = true; });
    return req;
};

function answerFor(method, url) {
    if (method === 'GET' && url === '/api/auth/sso/status') return script.sso === undefined ? DEFAULT_SSO : script.sso;
    if (method === 'POST' && url === '/api/auth/login') return (script.login || []).shift() || REFUSED;
    if (method === 'POST' && url === '/api/auth/ldap/verify') return (script.ldap || []).shift() || REFUSED;
    return { status: 404, body: { error: 'not found' } };
}

const server = http.createServer((req, res) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        const call = { method: req.method, path: req.url };
        if (text) {
            try {
                call.body = JSON.parse(text);
            } catch (_) {
                call.text = text;
            }
        }
        calls.push(call);
        const answer = answerFor(req.method, req.url);
        if (answer === 'timeout') {
            hanging.push(res);
            return;
        }
        if (answer === 'down') {
            req.socket.destroy();
            return;
        }
        answered += 1;
        res.statusCode = answer.status;
        if (answer.text !== undefined) {
            res.setHeader('Content-Type', 'text/plain');
            res.end(answer.text);
        } else {
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify(answer.body));
        }
    });
});

function releaseHanging() {
    for (const res of hanging) res.socket && res.socket.destroy();
    hanging = [];
}

// ---- database

const dialect = process.argv[2];
if (dialect !== 'sqlite' && dialect !== 'postgres') {
    console.error('usage: node db.js sqlite|postgres');
    process.exit(2);
}

let exec; // (sql, params) => Promise<void>
let all; // (sql, params) => Promise<row[]>
let closeDb = async () => {};
let db; // the console's database facade
let config;

function rebind(sql) {
    let n = 0;
    return sql.replace(/\?/g, () => `$${++n}`);
}

async function start() {
    // Relative paths of the scenarios land in the scratch folder.
    process.chdir(lib.SCRATCH);
    process.umask(0o022);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${server.address().port}/api`;
    process.env.BETTERDESK_API_TIMEOUT = String(API_TIMEOUT_MS);
    process.env.NODE_ENV = 'test';
    delete process.env.DOCKER;

    if (dialect === 'postgres') {
        const dsn = process.env.BETTERDESK_TEST_POSTGRES_DSN;
        if (!dsn) throw new Error('BETTERDESK_TEST_POSTGRES_DSN is not set');
        const { Pool } = web('node_modules/pg');
        const schema = `authn_oracle_${process.pid}_${RealDate.now() % 100000}`;
        const admin = new Pool({ connectionString: dsn, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        const url = `${dsn}${dsn.includes('?') ? '&' : '?'}options=${encodeURIComponent(`-c search_path=${schema}`)}`;
        process.env.DB_TYPE = 'postgres';
        process.env.DATABASE_URL = url;
        const pool = new Pool({ connectionString: url, max: 2 });
        exec = async (sql, params = []) => {
            await pool.query(rebind(sql), params);
        };
        all = async (sql, params = []) => (await pool.query(rebind(sql), params)).rows;
        closeDb = async () => {
            await pool.end();
            await admin.query(`DROP SCHEMA ${schema} CASCADE`);
            await admin.end();
        };
    } else {
        process.env.DB_TYPE = 'sqlite';
        process.env.DB_PATH = path.join(lib.SCRATCH, 'db_v2.sqlite3');
        delete process.env.DATABASE_URL;
    }

    config = web('config/config');
    db = web('services/database');
    await db.init();

    if (dialect === 'sqlite') {
        const raw = web('services/dbAdapter').getAdapter(config).getSqliteAuthDb();
        // Thousands of small writes; nothing here has to survive a power cut.
        raw.pragma('synchronous = OFF');
        exec = async (sql, params = []) => {
            raw.prepare(sql).run(...params);
        };
        all = async (sql, params = []) => raw.prepare(sql).all(...params);
    }
}

async function stop() {
    releaseHanging();
    try {
        await db.close();
    } catch (_) { /* closing twice is fine */ }
    await closeDb();
    await new Promise((resolve) => server.close(resolve));
    server.closeAllConnections && server.closeAllConnections();
}

const SQLITE_RESET = ['DELETE FROM access_tokens', 'DELETE FROM login_attempts', 'DELETE FROM account_lockouts', 'DELETE FROM audit_log', 'DELETE FROM users',
    "DELETE FROM sqlite_sequence WHERE name IN ('access_tokens', 'login_attempts', 'account_lockouts', 'audit_log', 'users')", 'DROP TABLE IF EXISTS device_tokens'];
const POSTGRES_RESET = ['DELETE FROM access_tokens', 'DELETE FROM login_attempts', 'DELETE FROM account_lockouts', 'DELETE FROM audit_log', 'DELETE FROM users',
    "SELECT setval(pg_get_serial_sequence('access_tokens', 'id'), 1, false), setval(pg_get_serial_sequence('login_attempts', 'id'), 1, false), " +
    "setval(pg_get_serial_sequence('account_lockouts', 'id'), 1, false), setval(pg_get_serial_sequence('users', 'id'), 1, false)",
    'DROP TABLE IF EXISTS device_tokens'];

async function reset() {
    for (const sql of dialect === 'sqlite' ? SQLITE_RESET : POSTGRES_RESET) await exec(sql);
}

const flag = (value) => (dialect === 'sqlite' ? (value ? 1 : 0) : !!value);

async function seedUsers(users) {
    for (const u of users || []) {
        await exec(
            'INSERT INTO users (username, password_hash, role, auth_provider, preferred_language, totp_secret, totp_enabled, totp_recovery_codes) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
            [u.username, u.password_hash, u.role === undefined ? 'admin' : u.role, u.auth_provider === undefined ? 'local' : u.auth_provider,
                u.preferred_language === undefined ? null : u.preferred_language, u.totp_secret === undefined ? null : u.totp_secret,
                flag(u.totp_enabled), u.totp_recovery_codes === undefined ? null : u.totp_recovery_codes]);
    }
}

// The tables as both sides compare them: flags as booleans, times as the
// distance in seconds from the database's clock, rounded to ten.
const seconds = (column) => (dialect === 'sqlite'
    ? `CAST(ROUND((julianday(${column}) - julianday('now')) * 8640) AS INTEGER) * 10`
    : `CAST(ROUND(EXTRACT(EPOCH FROM (${column} - NOW())) / 10) AS INTEGER) * 10`);
const truth = (column) => (dialect === 'sqlite' ? `(${column} IS NOT NULL AND ${column} <> 0)` : `COALESCE(${column}, FALSE)`);

const DUMPS = {
    users: () => `SELECT id, username, password_hash, role, auth_provider, ${seconds('last_login')} AS last_login, preferred_language, totp_secret,
        ${truth('totp_enabled')} AS totp_enabled, totp_recovery_codes FROM users ORDER BY id`,
    login_attempts: () => `SELECT id, username, ip_address, ${truth('success')} AS success, ${seconds('created_at')} AS created_at FROM login_attempts ORDER BY id`,
    account_lockouts: () => `SELECT username, ${seconds('locked_until')} AS locked_until, attempt_count FROM account_lockouts ORDER BY username`,
    access_tokens: () => `SELECT id, token, token_hash, user_id, client_id, client_uuid, ${seconds('expires_at')} AS expires_at, ${seconds('last_used')} AS last_used,
        ip_address, ${truth('revoked')} AS revoked FROM access_tokens ORDER BY id`,
};

async function dump() {
    const out = {};
    for (const [table, sql] of Object.entries(DUMPS)) {
        const rows = await all(sql());
        if (!rows.length) continue;
        out[table] = rows.map((row) => {
            for (const key of ['totp_enabled', 'success', 'revoked']) if (key in row) row[key] = !!row[key];
            for (const key of ['id', 'user_id', 'attempt_count', 'last_login', 'created_at', 'locked_until', 'expires_at', 'last_used']) {
                if (key in row && row[key] !== null) row[key] = Number(row[key]);
            }
            return row;
        });
    }
    return out;
}

// ---- files (ensureDefaultAdmin)

const ADMIN_BASE = '/tmp/authn-admin';
const WATCHED = [`${ADMIN_BASE}/keys`, `${ADMIN_BASE}/keys/data`, `${ADMIN_BASE}/data`, `${ADMIN_BASE}/other`, '/opt/rustdesk', '/opt/rustdesk/data', '/opt/betterdesk',
    '/opt/betterdesk/data', '/app/data', 'C:\\BetterDesk', 'C:\\RustDesk'];
const WATCHED_FILES = ['.admin_credentials', '.force_password_update', 'auth.db'];

function wipeFiles() {
    fs.rmSync(ADMIN_BASE, { recursive: true, force: true });
    for (const dir of WATCHED) fs.rmSync(dir, { recursive: true, force: true });
    fs.rmSync('/.dockerenv', { force: true });
    fs.rmSync('.admin_credentials', { force: true });
}

function placeFile(file) {
    if (file.dir) {
        fs.mkdirSync(file.path, { recursive: true });
        return;
    }
    fs.mkdirSync(path.dirname(file.path), { recursive: true });
    fs.writeFileSync(file.path, file.content || '', { mode: file.mode === undefined ? 0o644 : file.mode });
    if (file.mode !== undefined) fs.chmodSync(file.path, file.mode);
}

// listFiles is the state of the watched files. The time in a credentials
// file is written as its distance from the scenario's start, {{+25000}}.
function listFiles(base) {
    const relative = (text) => text.replace(/Timestamp: (\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z)/g,
        (_, iso) => `Timestamp: {{+${RealDate.parse(iso) - base}}}`);
    const out = {};
    const look = (p) => {
        let st;
        try {
            st = fs.statSync(p);
        } catch (_) {
            return;
        }
        if (st.isDirectory()) {
            out[p] = { dir: true };
            return;
        }
        out[p] = { content: relative(fs.readFileSync(p, 'utf8')), mode: st.mode & 0o777 };
    };
    for (const dir of WATCHED) for (const name of WATCHED_FILES) look(`${dir}/${name}`);
    look('/.dockerenv');
    look('.admin_credentials');
    return out;
}

// ---- running scenarios

function freshAuthService() {
    delete require.cache[require.resolve(path.join(WEB, 'services/authService'))];
    return web('services/authService');
}

const scenarios = [];
let seedCounter = 1000;

// scenario runs build with a recorder for its steps on an empty database
// and keeps what happened.
async function scenario(name, setup, build) {
    const firstSeed = seedCounter;
    for (let attempt = 1; ; attempt++) {
        const record = await attemptScenario(name, setup, build);
        if (record) {
            scenarios.push(record);
            return record;
        }
        if (attempt === 5) throw new Error(`scenario ${name}: the Go server's answers kept arriving late`);
        console.error(`scenario ${name}: an answer of the Go server arrived late, running it again`);
        seedCounter = firstSeed;
    }
}

// attemptScenario returns the scenario's record, or null when a request
// to the Go server was answered but the service had stopped waiting.
async function attemptScenario(name, setup, build) {
    let late = false;
    const record = { name, env: setup.env || {}, seed: setup.seed || {}, steps: [] };
    if (setup.files) record.files = setup.files;
    if (setup.dirs) record.dirs = setup.dirs;
    if (setup.platform) record.platform = setup.platform;

    await reset();
    wipeFilesIfUsed(setup);
    for (const sql of (record.seed.sql || [])) await exec(sql[dialect]);
    await seedUsers(record.seed.users);
    for (const sql of (record.seed.after || [])) await exec(sql[dialect]);

    const savedEnv = {};
    for (const [key, value] of Object.entries(record.env)) {
        savedEnv[key] = process.env[key];
        // null takes the variable away for the scenario
        if (value === null) delete process.env[key];
        else process.env[key] = value;
    }
    const savedConfig = { keysPath: config.keysPath, dataDir: config.dataDir, authDbPath: config.authDbPath };
    if (setup.dirs) Object.assign(config, setup.dirs);
    const savedPlatform = Object.getOwnPropertyDescriptor(process, 'platform');
    if (setup.platform) Object.defineProperty(process, 'platform', { value: setup.platform, configurable: true });

    const auth = freshAuthService();
    const deviceAuth = web('middleware/deviceAuth');
    const deviceTokenAuth = web('lib/deviceTokenAuth');
    const base = Math.floor(RealDate.now() / 1000) * 1000;

    const api = {
        base,
        // sql runs a statement outside the service, the same on both sides.
        async sql(statements) {
            await exec(statements[dialect]);
            record.steps.push({ sql: statements });
        },
        // call runs one function of the service.
        async call(fn, args, opts = {}) {
            const step = { fn, args, at: opts.at || { rel: 0 }, seed: seedCounter++ };
            if (opts.go) step.go = JSON.parse(JSON.stringify(opts.go));
            if (opts.onSleep) step.onSleep = opts.onSleep;
            script = { sso: opts.go && opts.go.sso, login: [...((opts.go && opts.go.login) || [])], ldap: [...((opts.go && opts.go.ldap) || [])] };
            calls = [];
            answered = 0;
            sent = [];
            logs = [];
            const sleeps = [];
            fakeNow = step.at.abs !== undefined ? step.at.abs : base + step.at.rel;
            nextByte = stream(step.seed);
            global.setTimeout = (callback, ms, ...rest) => {
                if (!String(new Error().stack).includes('authService.js')) return realSetTimeout(callback, ms, ...rest);
                sleeps.push(ms);
                fakeNow += ms;
                const hooks = (opts.onSleep && opts.onSleep[String(sleeps.length)]) || [];
                return realSetTimeout(async () => {
                    for (const hook of hooks) {
                        if (hook.file) placeFile(hook.file);
                        if (hook.sql) await exec(hook.sql[dialect]);
                        if (hook.users) await seedUsers(hook.users);
                    }
                    callback(...rest);
                }, 0);
            };

            let result;
            try {
                result = await run(fn, args);
                step.result = result === undefined ? null : result;
            } catch (err) {
                step.thrown = String(err && err.message);
            } finally {
                global.setTimeout = realSetTimeout;
                nextByte = null;
                // Let the listener see every request the service sent.
                for (let waited = 0; calls.length < sent.length && waited < 3000; waited += 5) {
                    await new Promise((resolve) => realSetTimeout(resolve, 5));
                }
                releaseHanging();
            }
            if (calls.length !== sent.length || answered !== sent.filter((entry) => entry.responded).length) late = true;
            if (step.result && typeof step.result === 'object' && typeof step.result.qrCode === 'string') {
                if (!step.result.qrCode.startsWith('data:image/png;base64,')) throw new Error('unexpected QR code');
                step.result = { ...step.result, qrCode: `QR:${step.result.otpauthUrl}` };
            }
            if (calls.length) step.goCalls = calls;
            if (logs.length) step.logs = logs;
            if (sleeps.length) step.sleeps = sleeps;
            if (setup.files || setup.dirs) step.files = listFiles(base);
            fakeNow = null;
            record.steps.push(JSON.parse(JSON.stringify(step)));
            return step;
        },
    };

    async function run(fn, args) {
        switch (fn) {
        case 'requireDeviceToken': {
            const req = { headers: {} };
            if (args[0] !== null) req.headers.authorization = args[0];
            let answer = null;
            const res = { status(code) { answer = { status: code }; return this; }, json(body) { answer.body = body; return this; } };
            await deviceAuth.requireDeviceToken(req, res, () => { answer = { next: true, deviceId: req.deviceId, tokenUserId: Number(req.deviceToken.user_id) }; });
            return answer;
        }
        case 'verifyDeviceWsAuth':
            return deviceTokenAuth.verifyDeviceWsAuth(args[0], args[1], db);
        case 'verifyEnrollmentToken':
            return deviceTokenAuth.verifyEnrollmentToken(args[0], args[1]);
        default:
            return auth[fn](...args);
        }
    }

    try {
        await build(api);
        record.tables = await dump();
    } finally {
        for (const [key, value] of Object.entries(savedEnv)) {
            if (value === undefined) delete process.env[key];
            else process.env[key] = value;
        }
        Object.assign(config, savedConfig);
        if (setup.platform) Object.defineProperty(process, 'platform', savedPlatform);
    }
    return late ? null : record;
}

function wipeFilesIfUsed(setup) {
    if (!setup.files && !setup.dirs) return;
    wipeFiles();
    for (const file of setup.files || []) placeFile(file);
}

// codeAt is the time-based code of a secret at a time, from the library
// the console uses.
function codeAt(secret, ms) {
    const { authenticator } = web('node_modules/otplib');
    const saved = fakeNow;
    fakeNow = ms;
    try {
        return authenticator.generate(secret);
    } finally {
        fakeNow = saved;
    }
}

module.exports = {
    dialect, start, stop, scenario, scenarios, ADMIN_BASE, realBcrypt, RealDate, codeAt,
    all: (...a) => all(...a),
    exec: (...a) => exec(...a),
};
