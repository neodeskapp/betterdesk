'use strict';

/**
 * Runs the Node console's real services/avatarService.js (web-nodejs) on a
 * scratch database and records every answer of a scenario, for the Go
 * package console/avatars to replay. See README.md in this folder.
 *
 *   node store.js                       SQLite in a temp folder, writes ../store_sqlite.json.gz
 *   ORACLE_PG_DSN=postgres://... node store.js
 *                                       PostgreSQL in a scratch schema, writes ../store_postgres.json.gz
 *
 * No request leaves the process: http.request is replaced by a fake that
 * plays the answer a step names. The clock of the service is Date.now with
 * an offset the steps move.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const http = require('http');
const https = require('https');
const { EventEmitter } = require('events');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'avatars-oracle-'));
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = scratch;
process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
const schema = `oracle_${process.pid}_${Date.now()}`;
if (PG_DSN) {
    process.env.DB_TYPE = 'postgres';
    const url = new URL(PG_DSN);
    url.searchParams.set('options', `-c search_path=${schema}`);
    process.env.DATABASE_URL = url.toString();
} else {
    process.env.DB_TYPE = 'sqlite';
    delete process.env.DATABASE_URL;
}
process.chdir(WEB);

let answer = null;
function fakeRequest(u, options, cb) {
    const req = new EventEmitter();
    const scripted = answer;
    req.destroy = () => {};
    req.end = () => setImmediate(() => {
        if (!scripted) return req.emit('error', new Error('no scripted answer'));
        const res = new EventEmitter();
        res.statusCode = scripted.status;
        res.headers = scripted.headers || {};
        res.resume = () => {};
        res.destroy = () => { res.destroyed = true; };
        cb(res);
        setImmediate(() => {
            if (res.destroyed) return;
            if (scripted.body) res.emit('data', Buffer.from(scripted.body, 'base64'));
            res.emit('end');
        });
    });
    return req;
}
http.request = fakeRequest;
https.request = fakeRequest;

const realNow = Date.now;
let clockOffset = 0;
Date.now = () => realNow() + clockOffset;

const PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC';
const GIF = Buffer.from('GIF89a\x01\x00\x01\x00\x00\x00\x00;', 'latin1').toString('base64');
const JPEG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 0x4a, 0x46, 0x49, 0x46, 0, 1]).toString('base64');
const png = { status: 200, headers: { 'content-type': 'image/png' }, body: PNG };
const gif = { status: 200, headers: { 'content-type': 'image/gif' }, body: GIF };

// Users 1 to 4 on a fresh database.
const SEED = [
    ...[['alice', 'operator'], ['bob', 'viewer'], ['carol', 'admin'], ['dave', 'viewer']]
        .map(([username, role]) => ['INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)', [username, 'x', role]]),
];

const all = [{ op: 'list' }, { op: 'versions' }, { op: 'avatar', userId: 1 }, { op: 'avatar', userId: 2 }, { op: 'avatar', userId: 3 }];
const STEPS = [
    ...all,
    { op: 'version', userId: 1 }, { op: 'version', userId: 0 }, { op: 'version', userId: -1 }, { op: 'version', userId: 99 },

    // Single sign-on pictures
    { op: 'oidc', userId: 1, exchange: {} },
    { op: 'oidc', userId: 0, exchange: { picture: `data:image/png;base64,${PNG}` } },
    { op: 'oidc', userId: 1, exchange: null },
    { op: 'oidc', userId: 1, exchange: { picture: `data:image/png;base64,${PNG}` } },
    ...all,
    { op: 'version', userId: 1 }, { op: 'version', userId: 2 },
    { op: 'oidc', userId: 1, exchange: { picture: `  data:image/gif;base64,${GIF}  ` } },
    ...all,
    { op: 'oidc', userId: 1, exchange: { picture: 'data:image/png;base64,PGh0bWw+' } },
    { op: 'oidc', userId: 1, exchange: { picture: `data:image/png;base64,${JPEG}` } },
    { op: 'oidc', userId: 1, exchange: { picture: 'data:image/svg+xml;base64,PHN2Zy8+' } },
    { op: 'oidc', userId: 1, exchange: { picture: 'not a picture' } },
    { op: 'oidc', userId: 1, exchange: { picture: 'http://127.0.0.1/a.png' } },
    { op: 'oidc', userId: 1, exchange: { picture: 'ftp://example.com/a.png' } },
    { op: 'avatar', userId: 1 },
    { op: 'oidc', userId: 2, exchange: { picture: 'http://idp.example/bob.png' }, answer: png },
    { op: 'oidc', userId: 3, exchange: { picture: 'https://idp.example/carol.png' }, answer: { status: 404, headers: {} } },
    { op: 'oidc', userId: 3, exchange: { picture: 'https://idp.example/carol.png' }, answer: { status: 200, headers: { 'content-type': 'text/html' }, body: PNG } },
    ...all,
    { op: 'oidc', userId: 99, exchange: { picture: `data:image/png;base64,${PNG}` } },
    { op: 'oidc', userId: 99, exchange: { picture: '' } },
    { op: 'oidc', userId: 2, exchange: { picture: 42 } },
    ...all,
    { op: 'oidc', userId: 1, exchange: { picture: null } },
    { op: 'oidc', userId: 1, exchange: { picture: '   ' } },
    ...all,

    // Pictures from the profile
    { op: 'profile', userId: 1, url: '' },
    { op: 'profile', userId: 1, url: 'not a url' },
    { op: 'profile', userId: 1, url: `data:image/png;base64,${PNG}` },
    { op: 'profile', userId: 1, url: 'ftp://example.com/a.png' },
    { op: 'profile', userId: 1, url: '  HTTP://pictures.example/alice.gif  ', answer: gif },
    ...all,
    { op: 'profile', userId: 1, url: 'https://pictures.example/big.png', answer: { status: 200, headers: { 'content-type': 'image/png', 'content-length': '9999999' }, body: PNG } },
    { op: 'profile', userId: 1, url: 'http://localhost/a.png' },
    { op: 'profile', userId: 1, url: 'http://u:p@pictures.example/a.png' },
    { op: 'avatar', userId: 1 },
    { op: 'profile', userId: 99, url: 'http://pictures.example/a.png', answer: png },
    { op: 'profile', userId: 99, url: '' },
    // An empty profile address removes a profile picture and keeps one from single sign-on
    { op: 'oidc', userId: 2, exchange: { picture: `data:image/png;base64,${PNG}` } },
    { op: 'profile', userId: 2, url: '   ' },
    { op: 'avatar', userId: 2 },
    { op: 'profile', userId: 1, url: '' },
    { op: 'profile', userId: 1, url: '' },
    ...all,
    // A sign-in without the claim removes only what came from single sign-on
    { op: 'profile', userId: 3, url: 'http://pictures.example/carol.png', answer: png },
    { op: 'oidc', userId: 3, exchange: { picture: '' } },
    { op: 'avatar', userId: 3 },
    { op: 'oidc', userId: 3, exchange: { picture: `data:image/gif;base64,${GIF}` } },
    { op: 'avatar', userId: 3 },
    ...all,

    // The list is kept for 30 seconds; a write by someone else shows after that
    { op: 'list' },
    { op: 'sql', sql: 'DELETE FROM user_avatars WHERE user_id = ?', args: [2] },
    { op: 'list' }, { op: 'version', userId: 2 }, { op: 'avatar', userId: 2 },
    { op: 'advance', ms: 29000 },
    { op: 'list' }, { op: 'versions' },
    { op: 'advance', ms: 2000 },
    { op: 'list' }, { op: 'versions' }, { op: 'version', userId: 2 },
    { op: 'sql', sql: 'DELETE FROM user_avatars WHERE user_id = ?', args: [3] },
    { op: 'list' },
    { op: 'reset' },
    { op: 'list' },
    // A picture goes with its user
    { op: 'oidc', userId: 4, exchange: { picture: `data:image/png;base64,${PNG}` } },
    { op: 'list' },
    { op: 'sql', sql: 'DELETE FROM users WHERE id = ?', args: [4] },
    { op: 'avatar', userId: 4 },
    { op: 'reset' },
    { op: 'list' },
    { op: 'fields', users: [1, 2, 3, 4, 99] },
];

function plainAvatar(row) {
    if (!row) return null;
    return {
        user_id: Number(row.user_id), mime_type: row.mime_type, data: Buffer.from(row.data).toString('base64'),
        version: row.version, source: row.source, source_url: row.source_url,
        updated_at_type: row.updated_at instanceof Date ? 'date' : typeof row.updated_at,
        updated_at: row.updated_at instanceof Date ? row.updated_at.toISOString() : row.updated_at,
    };
}

async function main() {
    let pool = null;
    if (PG_DSN) {
        const { Pool } = require(path.join(WEB, 'node_modules/pg'));
        const admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        await admin.end();
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
    }

    const config = require(path.join(WEB, 'config/config'));
    const db = require(path.join(WEB, 'services/database'));
    const { getAdapter } = require(path.join(WEB, 'services/dbAdapter'));
    const avatarService = require(path.join(WEB, 'services/avatarService'));

    const quiet = { log: console.log, warn: console.warn, error: console.error };
    const warnings = [];
    console.log = () => {};
    console.warn = (...args) => { warnings.push(args.join(' ')); };
    await db.init();

    const exec = async (sql, args) => {
        if (pool) {
            let n = 0;
            await pool.query(sql.replace(/\?/g, () => `$${++n}`), args);
        } else {
            getAdapter(config).getSqliteAuthDb().prepare(sql).run(...args);
        }
    };
    for (const [sql, args] of SEED) await exec(sql, args);

    const results = [];
    for (const step of STEPS) {
        const out = { ...step };
        answer = step.answer || null;
        warnings.length = 0;
        try {
            switch (step.op) {
            case 'oidc':
                out.result = await avatarService.syncOidcPicture(step.userId, step.exchange);
                break;
            case 'profile':
                out.result = await avatarService.syncProfileAvatarUrl(step.userId, step.url);
                break;
            case 'avatar':
                out.result = plainAvatar(await avatarService.getAvatar(step.userId));
                break;
            case 'version':
                out.result = await avatarService.getAvatarVersion(step.userId);
                break;
            case 'versions':
                out.result = Object.fromEntries(await avatarService.getVersionMap());
                break;
            case 'list':
                out.result = await avatarService.listDirectory();
                break;
            case 'fields': {
                const map = await avatarService.getVersionMap();
                out.result = step.users.map((id) => avatarService.withAvatarFields({ id }, map));
                break;
            }
            case 'sql':
                await exec(step.sql, step.args);
                break;
            case 'advance':
                clockOffset += step.ms;
                break;
            case 'reset':
                avatarService.resetCache();
                break;
            default:
                throw new Error(`unknown step ${step.op}`);
            }
        } catch (error) {
            out.error = String(error && error.message);
        }
        out.warned = warnings.length;
        if (warnings.length) out.warning = warnings[0];
        results.push(out);
    }
    Object.assign(console, quiet);

    const file = path.join(OUT, `store_${dialect}.json.gz`);
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify({ dialect, seed: SEED, steps: results }), { level: 9 }));
    console.log(`${dialect}: ${results.length} steps, ${results.filter((r) => r.error).length} with an error, ${fs.statSync(file).size} bytes`);

    await db.close();
    if (pool) {
        await pool.query(`DROP SCHEMA ${schema} CASCADE`);
        await pool.end();
    }
    fs.rmSync(scratch, { recursive: true, force: true });
}

main().then(() => process.exit(0), (err) => { console.error(err); process.exit(1); });
