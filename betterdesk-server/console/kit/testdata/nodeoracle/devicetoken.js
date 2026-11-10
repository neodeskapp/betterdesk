'use strict';

/**
 * Runs requireDeviceToken and requireTokenDeviceMatch of the Node console
 * (web-nodejs/middleware/deviceAuth.js, with the real database facade) in
 * a small Express app on a scratch database, sends it raw HTTP requests and
 * records what it answered. See README.md in this folder.
 *
 *   node devicetoken.js                      SQLite in a temp folder
 *   ORACLE_PG_DSN=postgres://... node devicetoken.js   PostgreSQL, in a schema of its own
 *
 * Writes ../device_sqlite.json or ../device_postgres.json.
 */

const crypto = require('crypto');
const fs = require('fs');
const net = require('net');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'kit-oracle-'));
const schema = `kit_oracle_${process.pid}_${Date.now() % 1000000}`;

function web(rel) {
    return require(path.join(WEB, rel));
}

function rebind(sql) {
    let n = 0;
    return sql.replace(/\?/g, () => `$${++n}`);
}

// mulberry32, so every run sends the same requests
function prng(seed) {
    let a = seed >>> 0;
    return () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}
const random = prng(20261110);
const pick = (list) => list[Math.floor(random() * list.length)];

const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');
const FUTURE = '2999-01-01 00:00:00';
const PAST = '2001-01-01 00:00:00';
const flag = (on) => (dialect === 'sqlite' ? (on ? 1 : 0) : on);

// The token rows. token is what a device sends; stored is what the token
// column holds (a row from before the hash column has no hash, a row
// written since has the hash and the token).
const TOKENS = [
    { name: 'valid', token: 'tok-valid-0001', client: 'device-a' },
    { name: 'other-device', token: 'tok-valid-0002', client: 'device-b' },
    { name: 'numeric-device', token: 'tok-numeric-01', client: '123' },
    { name: 'zeros-device', token: 'tok-zeros-0001', client: '000' },
    { name: 'longest-device', token: 'tok-longest-01', client: 'd'.repeat(64) },
    { name: 'expired', token: 'tok-expired-01', client: 'device-a', expires: PAST },
    { name: 'revoked', token: 'tok-revoked-01', client: 'device-a', revoked: true },
    { name: 'revoked-and-expired', token: 'tok-gone-00001', client: 'device-a', revoked: true, expires: PAST },
    { name: 'unbound', token: 'tok-unbound-01', client: '' },
    { name: 'client-null', token: 'tok-nullclient', client: null },
    { name: 'client-too-short', token: 'tok-short-0001', client: 'ab' },
    { name: 'client-with-space', token: 'tok-space-0001', client: 'dev ice' },
    { name: 'client-too-long', token: 'tok-toolong-01', client: 'd'.repeat(65) },
    { name: 'client-with-dot', token: 'tok-dotted-001', client: 'device.a' },
    { name: 'hash-only', token: 'tok-hashonly-1', stored: 'legacy-placeholder-10', client: 'device-a' },
    { name: 'hash-only-expired', token: 'tok-hashonly-2', stored: 'legacy-placeholder-11', client: 'device-a', expires: PAST },
    { name: 'token-only', token: 'tok-rawonly-01', hash: null, client: 'device-c' },
    { name: 'hash-of-another', token: 'tok-mismatch-1', hash: sha('tok-elsewhere-1'), client: 'device-d' },
    { name: 'non-ascii', token: 'tök-device-001', client: 'device-e' },
    { name: 'touch-fails', token: 'tok-locked-001', client: 'device-f' },
    { name: 'with-comma', token: 'tok,comma-0001', client: 'device-a' },
    { name: 'long-token', token: 'L'.repeat(300), client: 'device-a' },
];

function seedStatements() {
    const out = [
        ['DELETE FROM access_tokens', []],
        ['DELETE FROM users', []],
        ['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [1, 'device-owner', 'x', 'operator']],
        ['INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)', [2, 'other-owner', 'x', 'viewer']],
    ];
    TOKENS.forEach((t, i) => {
        const stored = t.stored || t.token;
        const hash = t.hash !== undefined ? t.hash : sha(t.token);
        out.push([
            'INSERT INTO access_tokens (id, token, token_hash, user_id, client_id, client_uuid, expires_at, ip_address, revoked) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [i + 1, stored, hash, (i % 2) + 1, t.client, `uuid-${i + 1}`, t.expires || FUTURE, '', flag(!!t.revoked)],
        ]);
    });
    // Noting the use of one token fails; its check must still pass
    if (dialect === 'sqlite') {
        out.push(["CREATE TRIGGER oracle_locked BEFORE UPDATE OF last_used ON access_tokens WHEN OLD.client_id = 'device-f' BEGIN SELECT RAISE(ABORT, 'locked'); END", []]);
    } else {
        out.push(["CREATE FUNCTION oracle_locked() RETURNS trigger AS $$ BEGIN IF OLD.client_id = 'device-f' THEN RAISE EXCEPTION 'locked'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql", []]);
        out.push(['CREATE TRIGGER oracle_locked BEFORE UPDATE ON access_tokens FOR EACH ROW EXECUTE FUNCTION oracle_locked()', []]);
    }
    return out;
}

// A request as the bytes that go on the wire, one character per byte.
function utf8Bytes(text) {
    return Buffer.from(text, 'utf8').toString('latin1');
}

function raw({ method = 'GET', target, headers = [], body, type }) {
    let head = `${method} ${target} HTTP/1.1\r\nHost: oracle\r\nConnection: close\r\n`;
    for (const [name, value] of headers) head += `${name}: ${value}\r\n`;
    if (body !== undefined) {
        head += `Content-Type: ${type || 'application/json'}\r\nContent-Length: ${body.length}\r\n`;
    }
    return `${head}\r\n${body === undefined ? '' : body}`;
}

function send(port, bytes) {
    return new Promise((resolve, reject) => {
        const socket = net.connect(port, '127.0.0.1', () => socket.write(Buffer.from(bytes, 'latin1')));
        const chunks = [];
        socket.on('data', (c) => chunks.push(c));
        socket.on('end', () => {
            const text = Buffer.concat(chunks).toString('latin1');
            const split = text.indexOf('\r\n\r\n');
            const status = Number(text.slice(9, 12));
            resolve({ status, body: Buffer.from(text.slice(split + 4), 'latin1').toString('utf8') });
        });
        socket.on('error', reject);
    });
}

async function openDatabase() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
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
    const db = web('services/database');
    await db.init();

    let exec;
    let pool = null;
    if (PG_DSN) {
        const { Pool } = web('node_modules/pg');
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
        exec = async (sql, params = []) => (await pool.query(rebind(sql), params)).rows;
    } else {
        const rawDb = web('services/dbAdapter').getAdapter().getSqliteAuthDb();
        exec = async (sql, params = []) => {
            const stmt = rawDb.prepare(sql);
            if (stmt.reader) return stmt.all(...params);
            stmt.run(...params);
            return [];
        };
    }
    const close = async () => {
        try {
            await db.close();
        } catch (_) { /* closing twice is fine */ }
        if (pool) await pool.end();
        if (admin) {
            await admin.query(`DROP SCHEMA ${schema} CASCADE`);
            await admin.end();
        }
    };
    return { exec, close };
}

// ---- the requests

const byName = Object.fromEntries(TOKENS.map((t) => [t.name, t.token]));
const VALID = byName.valid;
const bearer = (token) => ['Authorization', `Bearer ${utf8Bytes(token)}`];

function requests() {
    const out = [];
    const add = (name, r) => out.push({ name, raw: raw(r) });

    // Every token row, and tokens that have none
    for (const t of TOKENS) add(`token ${t.name}`, { target: '/api/probe/token', headers: [bearer(t.token)] });
    add('token hash-of-another by the other text', { target: '/api/probe/token', headers: [bearer('tok-elsewhere-1')] });
    add('token hash-only by the stored text', { target: '/api/probe/token', headers: [bearer('legacy-placeholder-10')] });
    add('token unknown', { target: '/api/probe/token', headers: [bearer('tok-unknown-01')] });
    add('token unknown 64 hex', { target: '/api/probe/token', headers: [bearer('0123456789abcdef'.repeat(4))] });
    add('token is a hash', { target: '/api/probe/token', headers: [bearer(sha(VALID))] });
    add('token non-ascii sent as latin1', { target: '/api/probe/token', headers: [['Authorization', 'Bearer t\xf6k-device-001']] });
    add('token non-ascii sent as utf-8', { target: '/api/probe/token', headers: [['Authorization', `Bearer ${utf8Bytes('tök-device-001')}`]] });

    // The forms of the header
    const forms = {
        'no header': null,
        plain: `Bearer ${VALID}`,
        'two spaces': `Bearer  ${VALID}`,
        tab: `Bearer\t${VALID}`,
        'two tabs': `Bearer\t\t${VALID}`,
        'space and tab': `Bearer \t${VALID}`,
        lower: `bearer ${VALID}`,
        upper: `BEARER ${VALID}`,
        'no space': `Bearer${VALID}`,
        'second word': `Bearer ${VALID} extra`,
        'token and comma': `Bearer ${VALID},x`,
        'other scheme': `Token ${VALID}`,
        basic: `Basic ${VALID}`,
        'token alone': VALID,
        'word alone': 'Bearer',
        'word and space': 'Bearer ',
        'space in front': ` Bearer ${VALID}`,
        'space behind': `Bearer ${VALID} `,
        'tab behind': `Bearer ${VALID}\t`,
        'bearer twice': `Bearer Bearer ${VALID}`,
        'non-breaking space': `Bearer\xa0${VALID}`,
        empty: '',
    };
    for (const [form, value] of Object.entries(forms)) {
        add(`form ${form}`, { target: '/api/probe/token', headers: value === null ? [] : [['Authorization', value]] });
    }
    add('form two headers, the good one first', { target: '/api/probe/token', headers: [['Authorization', `Bearer ${VALID}`], ['Authorization', 'Bearer tok-unknown-01']] });
    add('form two headers, the good one second', { target: '/api/probe/token', headers: [['Authorization', 'Bearer tok-unknown-01'], ['Authorization', `Bearer ${VALID}`]] });
    add('form lower-case name', { target: '/api/probe/token', headers: [['authorization', `Bearer ${VALID}`]] });
    add('form X-Device-Id alone', { target: '/api/probe/token', headers: [['X-Device-Id', 'device-a']] });
    for (const method of ['POST', 'PUT', 'PATCH', 'DELETE', 'HEAD']) {
        const body = method === 'HEAD' ? undefined : '{}';
        add(`method ${method}`, { method, target: '/api/probe/token', headers: [bearer(VALID)], body });
        add(`method ${method} without a token`, { method, target: '/api/probe/token', body });
    }

    // The claims, with the token of device-a
    const bodies = {
        none: undefined,
        'key missing': '{"other":"device-b"}',
        null: '{"device_id":null}',
        'empty text': '{"device_id":""}',
        same: '{"device_id":"device-a"}',
        other: '{"device_id":"device-b"}',
        'upper case': '{"device_id":"DEVICE-A"}',
        'space in front': '{"device_id":" device-a"}',
        'space behind': '{"device_id":"device-a "}',
        zero: '{"device_id":0}',
        'minus zero': '{"device_id":-0}',
        one: '{"device_id":1}',
        fraction: '{"device_id":1.5}',
        'large number': '{"device_id":1e21}',
        true: '{"device_id":true}',
        false: '{"device_id":false}',
        'empty list': '{"device_id":[]}',
        'list of same': '{"device_id":["device-a"]}',
        'list of two': '{"device_id":["device-a","x"]}',
        'list of null': '{"device_id":[null]}',
        'list in list': '{"device_id":[["device-a"]]}',
        'list with null first': '{"device_id":[null,"device-a"]}',
        'empty object': '{"device_id":{}}',
        object: '{"device_id":{"a":1}}',
        'object with toString': '{"device_id":{"toString":1}}',
        'list with toString': '{"device_id":[{"toString":1}]}',
        'list in list with toString': '{"device_id":[[{"toString":"device-a"}]]}',
        'object with valueOf': '{"device_id":{"valueOf":1}}',
        'text zero': '{"device_id":"0"}',
        'text false': '{"device_id":"false"}',
        'key twice': '{"device_id":"device-b","device_id":"device-a"}',
        'member __proto__': '{"__proto__":{"device_id":"device-b"}}',
        'escaped text': '{"device_id":"device\\u002da"}',
        'non-ascii': `{"device_id":"${utf8Bytes('dévice-a')}"}`,
        list: '[{"device_id":"device-b"}]',
        'form same': ['device_id=device-a', 'application/x-www-form-urlencoded'],
        'form other': ['device_id=device-b', 'application/x-www-form-urlencoded'],
        'form twice': ['device_id=device-a&device_id=x', 'application/x-www-form-urlencoded'],
        'form empty': ['device_id=', 'application/x-www-form-urlencoded'],
        'form brackets': ['device_id[toString]=x', 'application/x-www-form-urlencoded'],
        'form escaped name': ['%64evice_id=device-b', 'application/x-www-form-urlencoded'],
    };
    const headerClaims = {
        none: [],
        same: [['X-Device-Id', 'device-a']],
        other: [['X-Device-Id', 'device-b']],
        empty: [['X-Device-Id', '']],
        twice: [['X-Device-Id', 'device-a'], ['X-Device-Id', 'device-b']],
        'twice, one empty': [['X-Device-Id', 'device-a'], ['X-Device-Id', '']],
        'with spaces': [['X-Device-Id', '  device-a  ']],
        'upper case': [['X-Device-Id', 'DEVICE-A']],
        latin1: [['X-Device-Id', 'd\xe9vice-a']],
    };
    const queries = {
        none: '',
        same: 'device_id=device-a',
        other: 'device_id=device-b',
        empty: 'device_id=',
        'name only': 'device_id',
        'list of same': 'device_id[]=device-a',
        'list of two': 'device_id[]=device-a&device_id[]=x',
        'empty list': 'device_id[]=',
        object: 'device_id[x]=device-a',
        'object with toString': 'device_id[toString]=x',
        'list with toString': 'device_id[0][toString]=x',
        'object with valueOf': 'device_id[valueOf]=device-a',
        twice: 'device_id=device-a&device_id=device-b',
        'twice, first empty': 'device_id=&device_id=device-a',
        'escaped name': '%64evice_id=device-a',
        'escaped value': 'device_id=device%2Da',
        'bad escape': 'device_id=%zz',
        plus: 'device_id=device+a',
        'upper case': 'device_id=DEVICE-A',
        'non-ascii': 'device_id=d%C3%A9vice-a',
        index: 'device_id[0]=device-a',
    };
    const withBody = (spec) => {
        if (spec === undefined) return {};
        if (Array.isArray(spec)) return { body: spec[0], type: spec[1] };
        return { body: spec };
    };
    const headerSets = ['none', 'same', 'other', 'empty'];
    for (const [b, spec] of Object.entries(bodies)) {
        for (const h of headerSets) {
            add(`match body ${b}, header ${h}`, { method: 'POST', target: '/api/probe/match', headers: [bearer(VALID), ...headerClaims[h]], ...withBody(spec) });
        }
    }
    for (const [q, query] of Object.entries(queries)) {
        for (const h of ['none', 'same', 'other', 'empty', 'twice']) {
            add(`match query ${q}, header ${h}`, { target: `/api/probe/match${query ? `?${query}` : ''}`, headers: [bearer(VALID), ...headerClaims[h]] });
        }
    }
    for (const [h, list] of Object.entries(headerClaims)) {
        add(`match header ${h}`, { target: '/api/probe/match', headers: [bearer(VALID), ...list] });
    }
    // A GET with a body: the body still counts first
    add('match GET with a body of another device', { target: '/api/probe/match?device_id=device-a', headers: [bearer(VALID)], body: '{"device_id":"device-b"}' });
    add('match GET with a body of the same device', { target: '/api/probe/match?device_id=device-b', headers: [bearer(VALID), ['X-Device-Id', 'device-b']], body: '{"device_id":"device-a"}' });

    // Mixes, with devices whose ids look like other values
    const devices = ['valid', 'other-device', 'numeric-device', 'zeros-device', 'longest-device'];
    const numeric = {
        'number 123': '{"device_id":123}', 'text 123': '{"device_id":"123"}', 'list 123': '{"device_id":[123]}', 'fraction 123.0': '{"device_id":123.0}',
        'exponent 1.23e2': '{"device_id":1.23e2}', 'zeros as number': '{"device_id":0}', 'zeros as text': '{"device_id":"000"}', 'long id': `{"device_id":"${'d'.repeat(64)}"}`,
    };
    const bodyPool = { ...bodies, ...numeric };
    const queryPool = { ...queries, 'query 123': 'device_id=123', 'query zeros': 'device_id=000', 'query b': 'device_id=device-b' };
    const headerPool = { ...headerClaims, 'header 123': [['X-Device-Id', '123']], 'header zeros': [['X-Device-Id', '000']] };
    for (let i = 0; i < 160; i++) {
        const device = pick(devices);
        const b = pick(Object.keys(bodyPool));
        const q = pick(Object.keys(queryPool));
        const h = pick(Object.keys(headerPool));
        const query = queryPool[q];
        add(`mix ${i}: ${device}, body ${b}, query ${q}, header ${h}`, {
            method: pick(['GET', 'POST', 'PUT', 'DELETE']),
            target: `/api/probe/match${query ? `?${query}` : ''}`,
            headers: [bearer(byName[device]), ...headerPool[h]],
            ...withBody(bodyPool[b]),
        });
    }
    // The match without the token check in front: no device id at all
    for (const [b, spec] of Object.entries({ none: undefined, 'empty text': bodies['empty text'], same: bodies.same, null: bodies.null })) {
        for (const h of ['none', 'same', 'empty']) {
            add(`match alone body ${b}, header ${h}`, { method: 'POST', target: '/api/probe/match-alone', headers: headerClaims[h], ...withBody(spec) });
        }
    }
    add('match alone query empty', { target: '/api/probe/match-alone?device_id=' });
    add('match alone query same', { target: '/api/probe/match-alone?device_id=device-a' });
    // The token check comes first
    add('match without a token, body of another device', { method: 'POST', target: '/api/probe/match', body: bodies.other });
    add('match with an expired token, body of the same device', { method: 'POST', target: '/api/probe/match', headers: [bearer(byName.expired)], body: bodies.same });
    return out;
}

async function main() {
    const db = await openDatabase();
    const seed = seedStatements();
    for (const [sql, params] of seed) await db.exec(sql, params);

    const express = web('node_modules/express');
    const { requireDeviceToken, requireTokenDeviceMatch } = web('middleware/deviceAuth');
    const app = express();
    app.use(web('lib/jsonBody').jsonBody());
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    const answer = (req, res) => {
        const out = { next: true };
        if (req.deviceId !== undefined) {
            out.deviceId = req.deviceId;
            out.clientId = req.deviceToken.client_id;
            out.userId = Number(req.deviceToken.user_id);
        }
        res.json(out);
    };
    app.all('/api/probe/token', requireDeviceToken, answer);
    app.all('/api/probe/match', requireDeviceToken, requireTokenDeviceMatch, answer);
    app.all('/api/probe/match-alone', requireTokenDeviceMatch, answer);
    // The console's error handler, as it answers the API in production
    // eslint-disable-next-line no-unused-vars
    app.use((err, req, res, _next) => res.status(500).json({ success: false, error: 'Internal Server Error' }));
    const server = app.listen(0, '127.0.0.1');
    await new Promise((resolve) => server.once('listening', resolve));
    const { port } = server.address();

    const steps = [];
    const touched = async () => (await db.exec('SELECT id FROM access_tokens WHERE last_used IS NOT NULL ORDER BY id')).map((row) => Number(row.id));
    for (const request of requests()) {
        await db.exec('UPDATE access_tokens SET last_used = NULL WHERE last_used IS NOT NULL');
        const got = await send(port, request.raw);
        steps.push({ ...request, status: got.status, answer: got.body, touched: await touched() });
    }
    // The lookup itself fails
    const broken = ['ALTER TABLE access_tokens RENAME TO access_tokens_gone', []];
    await db.exec(...broken);
    steps.push({ name: 'the token table is gone', sql: broken[0], params: broken[1] });
    for (const [name, r] of [
        ['lookup fails, good token', { target: '/api/probe/token', headers: [bearer(VALID)] }],
        ['lookup fails, unknown token', { target: '/api/probe/token', headers: [bearer('tok-unknown-01')] }],
        ['lookup fails, no token', { target: '/api/probe/token' }],
        ['lookup fails, bad header', { target: '/api/probe/token', headers: [['Authorization', 'Basic x']] }],
        ['lookup fails, match', { method: 'POST', target: '/api/probe/match', headers: [bearer(VALID)], body: '{"device_id":"device-a"}' }],
    ]) {
        const bytes = raw(r);
        const got = await send(port, bytes);
        steps.push({ name, raw: bytes, status: got.status, answer: got.body });
    }

    server.close();
    await db.close();
    const file = path.join(__dirname, '..', `device_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed, steps }, null, 1)}\n`);
    console.log(`${file}: ${steps.length} steps`);
}

main().then(() => process.exit(0), (err) => {
    console.error(err);
    process.exit(1);
});
