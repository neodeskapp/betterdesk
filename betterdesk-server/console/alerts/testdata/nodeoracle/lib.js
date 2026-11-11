'use strict';

/**
 * Shared helpers of the oracle scripts of console/alerts: the scratch
 * database (SQLite, or PostgreSQL in a schema of its own when ORACLE_PG_DSN
 * names a server), a seeded random source, a writer for values of every
 * JavaScript type, and the writer of the vector files. See README.md.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const Module = require('module');

const WEB = process.env.ORACLE_WEB || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const OUT = path.join(__dirname, '..');
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'alerts-oracle-'));
const schema = `oracle_alerts_${process.pid}_${Date.now() % 1000000}`;

// The variables the mail service and config.js read. The scripts set them
// per step; nothing of the machine's own environment gets in.
const ENV_KEYS = ['SMTP_HOST', 'SMTP_PORT', 'SMTP_SECURE', 'SMTP_USER', 'SMTP_PASS', 'SMTP_FROM',
    'SMTP_ALERT_EMAIL', 'SMTP_TLS_VERIFY', 'PANEL_PUBLIC_URL', 'PUBLIC_URL', 'HOST', 'PORT',
    'HTTP_REDIRECT_HTTPS', 'ALLOW_SELF_SIGNED_CERTS'];

process.env.NODE_ENV = 'test';
process.env.SESSION_SECRET = 'oracle';
process.env.DATA_DIR = scratch;
process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
for (const key of ENV_KEYS) delete process.env[key];

function web(rel) {
    return require(path.join(WEB, rel));
}

/** Puts an object in place of a module before anything requires it. */
function fakeModule(file, exports) {
    const filename = require.resolve(file);
    const m = new Module(filename, null);
    m.filename = filename;
    m.loaded = true;
    m.exports = exports;
    require.cache[filename] = m;
    return exports;
}

function rebind(sql) {
    let n = 0;
    return sql.replace(/\?/g, () => `$${++n}`);
}

async function openDatabase() {
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

/** mulberry32: the same numbers on every run. */
function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return {
        next,
        int: (n) => Math.floor(next() * n),
        pick: (list) => list[Math.floor(next() * list.length)],
        chance: (p) => next() < p,
    };
}

/**
 * A value of any JavaScript type as text, so that the Go tests can tell
 * undefined from null, NaN from null and -0 from 0: JSON with the words
 * undefined, NaN, Infinity, -Infinity and -0 where JSON has none.
 */
function dump(v) {
    if (v === undefined) return 'undefined';
    if (v === null) return 'null';
    if (typeof v === 'number') {
        if (Number.isNaN(v)) return 'NaN';
        if (Object.is(v, -0)) return '-0';
        return String(v);
    }
    if (typeof v === 'string') return JSON.stringify(v);
    if (typeof v === 'boolean') return String(v);
    if (typeof v === 'function') return '"[function]"';
    if (Array.isArray(v)) return `[${v.map(dump).join(',')}]`;
    if (v instanceof Date) return `{"$date":${dump(v.getTime())}}`;
    if (Buffer.isBuffer(v)) return `{"$buffer":${JSON.stringify(v.toString('hex'))}}`;
    return `{${Object.keys(v).map((k) => `${JSON.stringify(k)}:${dump(v[k])}`).join(',')}}`;
}

/** Collects what the code under test prints, by level. */
function captureConsole() {
    const real = { log: console.log, warn: console.warn, error: console.error };
    const lines = [];
    const join = (args) => args.map((a) => (typeof a === 'string' ? a : require('util').inspect(a))).join(' ');
    console.log = (...args) => lines.push(['info', join(args)]);
    console.warn = (...args) => lines.push(['warn', join(args)]);
    console.error = (...args) => lines.push(['error', join(args)]);
    return {
        take: () => lines.splice(0, lines.length),
        restore: () => Object.assign(console, real),
        print: (...args) => real.log(...args),
    };
}

function setEnv(env) {
    for (const key of ENV_KEYS) delete process.env[key];
    for (const [key, value] of Object.entries(env || {})) process.env[key] = value;
}

function writeVectors(name, data, count) {
    const file = path.join(OUT, `${name}.json.gz`);
    fs.writeFileSync(file, zlib.gzipSync(Buffer.from(JSON.stringify(data)), { level: 9 }));
    return `${path.basename(file)}: ${count}`;
}

module.exports = { WEB, OUT, dialect, scratch, web, fakeModule, openDatabase, rng, dump, captureConsole, setEnv, writeVectors, ENV_KEYS };
