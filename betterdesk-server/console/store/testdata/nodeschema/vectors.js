'use strict';
// Writes the test vectors in ../vectors from the Node console's own code and
// drivers. See README.md next to this file.
//
//   node vectors.js                      backfills.json and values-sqlite.json
//   DATABASE_URL=postgres://... TZ=UTC node vectors.js
//                                        also values-postgres-utc.json
//   DATABASE_URL=postgres://... TZ=Europe/Berlin node vectors.js
//                                        also values-postgres-berlin.json
const fs = require('fs');
const os = require('os');
const path = require('path');
const { createRequire } = require('module');

const webRoot = process.env.BETTERDESK_NODE_ROOT || path.resolve(__dirname, '../../../../../web-nodejs');
const webRequire = createRequire(path.join(webRoot, 'package.json'));
const outDir = path.resolve(__dirname, '../vectors');

// The two safeJsonParse helpers of services/dbAdapter.js, which are not
// exported: the SQLite adapter's and the PostgreSQL adapter's.
function safeJsonParseSqlite(str, fallback) {
    try { return JSON.parse(str); } catch { return fallback; }
}
function safeJsonParsePostgres(val, fallback) {
    if (val && typeof val === 'object') return val;
    try { return JSON.parse(val); } catch { return fallback; }
}

function describe(value) {
    if (value === null) return 'null';
    if (Buffer.isBuffer(value)) return 'Buffer';
    if (value instanceof Date) return 'Date';
    if (Array.isArray(value)) return 'array';
    return typeof value;
}

// ---- value cases ----
// go: the Go value type that has to reproduce the JSON.
// For JSONParsed, "fallback" is the JSON text passed to safeJsonParse.

const TRICKY_TEXT = 'a "quoted" \\ back\nline\ttab <b>&amp;</b> é 😀\u0001';

const sqliteCases = [
    ['Time', 'c_text', "'2026-11-10 01:05:50'", "text as datetime('now') writes it"],
    ['Time', 'c_text', "'2026-11-10T01:05:50.930Z'", 'text as toISOString writes it'],
    ['Time', 'c_text', "'2026-01-01T11:00:00Z'", 'text as a device sent it'],
    ['Time', 'c_text', "'2026-11-17 01:05:51.694'", 'expiry text built in JavaScript'],
    ['Time', 'c_text', "''", 'empty text'],
    ['Time', 'c_text', "'not a time'", 'text that is no time'],
    ['Time', 'c_text', 'NULL', 'NULL'],
    ['Time', 'c_datetime', "'2026-11-10 01:05:50'", 'DATETIME column'],
    ['Time', 'c_datetime', "datetime('2026-03-04 05:06:07')", 'DATETIME column filled by datetime()'],
    ['Time', 'c_datetime', 'NULL', 'DATETIME column, NULL'],
    ['Bool', 'c_integer', '0', 'flag 0'],
    ['Bool', 'c_integer', '1', 'flag 1'],
    ['Bool', 'c_integer', '5', 'flag with another number'],
    ['Bool', 'c_integer', 'NULL', 'flag NULL'],
    ['Int64String', 'c_integer', '0', 'integer 0'],
    ['Int64String', 'c_integer', '42', 'integer'],
    ['Int64String', 'c_integer', '-7', 'negative integer'],
    ['Int64String', 'c_integer', '5000000000', 'integer above 32 bits'],
    ['Int64String', 'c_integer', 'NULL', 'integer NULL'],
    ['Float', 'c_real', '0.1', 'real'],
    ['Float', 'c_real', '0', 'real 0'],
    ['Float', 'c_real', '2', 'real with an integer value'],
    ['Float', 'c_real', '99.99', 'real with two decimals'],
    ['Float', 'c_real', '1e21', 'large real'],
    ['Float', 'c_real', '1e-7', 'small real'],
    ['Float', 'c_real', '-0.0', 'negative zero'],
    ['Float', 'c_real', 'NULL', 'real NULL'],
    ['JSONText', 'c_text', `'{"b": 2, "a": 1}'`, 'JSON object in text'],
    ['JSONText', 'c_text', "'[]'", 'JSON array in text'],
    ['JSONText', 'c_text', "''", 'empty text'],
    ['JSONText', 'c_text', 'NULL', 'NULL'],
    ['JSONParsed', 'c_text', `'{"b": 2, "a": 1, "10": [1.0, 1e2, 12345678901234567890], "2": {"x": null}}'`, 'parsed object', '{}'],
    ['JSONParsed', 'c_text', `'[1, "two", true, null, 1.50]'`, 'parsed array', '[]'],
    ['JSONParsed', 'c_text', `'{"a": 1, "a": 2}'`, 'parsed object with a key twice', '{}'],
    ['JSONParsed', 'c_text', "''", 'empty text falls back', '[]'],
    ['JSONParsed', 'c_text', "'not json'", 'broken text falls back', '{}'],
    ['JSONParsed', 'c_text', "'5'", 'a number in text', '{}'],
    ['JSONParsed', 'c_text', `'"text"'`, 'a JSON string in text', '{}'],
    ['JSONParsed', 'c_text', 'NULL', 'NULL is null, not the fallback', '[]'],
    ['NullString', 'c_text', "'plain'", 'text'],
    ['NullString', 'c_text', "''", 'empty text'],
    ['NullString', 'c_text', 'NULL', 'NULL'],
    ['NullString', 'c_text', 'TRICKY', 'text with quotes, control characters and non-ASCII'],
    ['none', 'c_blob', "x'deadbeef'", 'blob'],
];

const sqliteQueries = [
    ['Int64String', 'SELECT COUNT(*) AS v FROM vec', 'COUNT(*)'],
    ['Int64String', 'SELECT SUM(c_integer) AS v FROM vec', 'SUM of integers'],
    ['Float', 'SELECT AVG(c_real) AS v FROM vec WHERE c_real IN (0.1, 2)', 'AVG'],
    ['Time', 'SELECT MAX(c_datetime) AS v FROM vec', 'MAX of a DATETIME column'],
];

const postgresCases = [
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.694123+00'", 'timestamptz with microseconds'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51+00'", 'timestamptz without fraction'],
    ['Time', 'c_timestamptz', "'2026-11-10 03:05:51.000001+02'", 'timestamptz given in another zone'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.999999+00'", 'timestamptz just before the next second'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.57+00'", 'fraction .57'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.58+00'", 'fraction .58'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.289+00'", 'fraction .289'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.829009+00'", 'fraction .829009'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.001999+00'", 'fraction .001999'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.0005+00'", 'fraction .0005'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.035+00'", 'fraction .035'],
    ['Time', 'c_timestamptz', "'2026-11-10 01:05:51.145+00'", 'fraction .145'],
    ['Time', 'c_timestamptz', "'1969-12-31 23:59:59.5+00'", 'before 1970'],
    ['Time', 'c_timestamptz', "'0099-01-01 00:00:00+00'", 'year below 100'],
    ['Time', 'c_timestamptz', "'infinity'", 'infinity'],
    ['Time', 'c_timestamptz', "'-infinity'", '-infinity'],
    ['Time', 'c_timestamptz', 'NULL', 'timestamptz NULL'],
    ['Time', 'c_timestamp', "'2026-11-10 01:05:51.694'", 'timestamp without zone, winter'],
    ['Time', 'c_timestamp', "'2026-07-01 12:00:00'", 'timestamp without zone, summer'],
    ['Time', 'c_timestamp', 'NULL', 'timestamp without zone, NULL'],
    ['Time', 'c_date', "'2026-11-10'", 'date'],
    ['Time', 'c_text', "'2026-11-10 01:05:50'", 'time kept as text'],
    ['Bool', 'c_boolean', 'TRUE', 'boolean true'],
    ['Bool', 'c_boolean', 'FALSE', 'boolean false'],
    ['Bool', 'c_boolean', 'NULL', 'boolean NULL'],
    ['Bool', 'c_integer', '1', 'flag in an integer column, 1'],
    ['Bool', 'c_integer', '0', 'flag in an integer column, 0'],
    ['Int64String', 'c_integer', '42', 'integer'],
    ['Int64String', 'c_integer', '-7', 'negative integer'],
    ['Int64String', 'c_integer', 'NULL', 'integer NULL'],
    ['Int64String', 'c_smallint', '7', 'smallint'],
    ['Int64String', 'c_bigint', '42', 'bigint'],
    ['Int64String', 'c_bigint', '5000000000', 'bigint above 32 bits'],
    ['Int64String', 'c_bigint', '-9007199254740993', 'bigint beyond what a double holds'],
    ['Int64String', 'c_bigint', 'NULL', 'bigint NULL'],
    ['Int64String', 'c_numeric', '12.50', 'numeric'],
    ['Int64String', 'c_numeric', '7', 'numeric with an integer value'],
    ['Float', 'c_real', '0.1', 'real'],
    ['Float', 'c_real', '0', 'real 0'],
    ['Float', 'c_real', '99.99', 'real with two decimals'],
    ['Float', 'c_real', '16777217', 'real that four bytes cannot hold exactly'],
    ['Float', 'c_real', '1e21', 'large real'],
    ['Float', 'c_real', '1e-7', 'small real'],
    ['Float', 'c_real', "'NaN'", 'real NaN'],
    ['Float', 'c_real', "'Infinity'", 'real Infinity'],
    ['Float', 'c_real', 'NULL', 'real NULL'],
    ['Float', 'c_double', '0.1', 'double precision'],
    ['Float', 'c_double', '99.99', 'double precision with two decimals'],
    ['Float', 'c_double', '1e21', 'large double precision'],
    ['Float', 'c_double', '-0.0', 'negative zero'],
    ['JSONText', 'c_jsonb', `'{"b": 2, "a": 1, "10": [1.0, 1e2, 12345678901234567890], "2": {"x": null}, "aa": "<b>&</b>"}'`, 'jsonb object'],
    ['JSONText', 'c_jsonb', `'[1, "two", true, null, 1.50]'`, 'jsonb array'],
    ['JSONText', 'c_jsonb', `'"text"'`, 'jsonb string'],
    ['JSONText', 'c_jsonb', "'5'", 'jsonb number'],
    ['JSONText', 'c_jsonb', "'null'", 'jsonb null'],
    ['JSONText', 'c_jsonb', 'NULL', 'jsonb NULL'],
    ['JSONText', 'c_json', `'{"b": 1,  "a": 1.0, "b": 2}'`, 'json object, stored as written'],
    ['JSONText', 'c_text', `'{"b": 2, "a": 1}'`, 'JSON in a text column'],
    ['JSONText', 'c_text', 'NULL', 'text NULL'],
    ['JSONParsed', 'c_jsonb', `'{"b": 2, "a": 1}'`, 'parsed jsonb object', '{}'],
    ['JSONParsed', 'c_jsonb', `'"text"'`, 'jsonb string falls back', '[]'],
    ['JSONParsed', 'c_jsonb', `'"5"'`, 'jsonb string that holds a number', '[]'],
    ['JSONParsed', 'c_jsonb', "'5'", 'jsonb number', '[]'],
    ['JSONParsed', 'c_jsonb', "'true'", 'jsonb true', '[]'],
    ['JSONParsed', 'c_jsonb', "'null'", 'jsonb null', '[]'],
    ['JSONParsed', 'c_jsonb', 'NULL', 'jsonb NULL is null', '[]'],
    ['JSONParsed', 'c_text', `'{"b": 2, "a": 1}'`, 'parsed text', '{}'],
    ['JSONParsed', 'c_text', "'not json'", 'broken text falls back', '{}'],
    ['JSONParsed', 'c_text', "''", 'empty text falls back', '[]'],
    ['JSONParsed', 'c_text', 'NULL', 'text NULL is null', '[]'],
    ['NullString', 'c_text', "'plain'", 'text'],
    ['NullString', 'c_text', "''", 'empty text'],
    ['NullString', 'c_text', 'NULL', 'NULL'],
    ['NullString', 'c_text', 'TRICKY', 'text with quotes, control characters and non-ASCII'],
    ['none', 'c_bytea', "'\\xdeadbeef'", 'bytea'],
];

const postgresQueries = [
    ['Int64String', 'SELECT id AS v FROM vec WHERE id = 1', 'serial id'],
    ['Int64String', 'SELECT COUNT(*) AS v FROM vec', 'COUNT(*)'],
    ['Int64String', 'SELECT COUNT(*)::INTEGER AS v FROM vec', 'COUNT(*)::INTEGER'],
    ['Int64String', 'SELECT SUM(c_integer) AS v FROM vec', 'SUM of integers'],
    ['Int64String', 'SELECT SUM(c_bigint) AS v FROM vec WHERE c_bigint < 100', 'SUM of bigints'],
    ['Int64String', 'SELECT AVG(c_integer) AS v FROM vec WHERE c_integer IN (42, -7)', 'AVG of integers'],
    ['Float', 'SELECT AVG(c_double) AS v FROM vec WHERE c_double IN (0.1, 99.99)', 'AVG of double precision'],
    ['Time', 'SELECT MAX(c_timestamptz) AS v FROM vec WHERE c_timestamptz < TIMESTAMPTZ \'2026-11-10 01:05:51.5+00\'', 'MAX of timestamptz'],
    ['Time', 'SELECT NOW() - NOW() + TIMESTAMPTZ \'2026-02-03 04:05:06.789+00\' AS v', 'timestamptz expression'],
    ['Bool', 'SELECT c_integer > 0 AS v FROM vec WHERE c_integer = 42', 'boolean expression'],
];

function buildCases(cases, queries, literal, parse) {
    const out = [];
    let id = 0;
    for (const [go, column, value, name, fallback] of cases) {
        id += 1;
        const sql = value === 'TRICKY' ? literal(TRICKY_TEXT) : value;
        out.push({
            id, name, go, column, fallback,
            insert: `INSERT INTO vec (${column}) VALUES (${sql})`,
            query: `SELECT ${column} AS v FROM vec WHERE id = ${id}`,
        });
    }
    for (const [go, query, name] of queries) {
        out.push({ name, go, query });
    }
    return out.map((c) => ({ ...c, parse }));
}

function record(c, value) {
    const rec = { name: c.name, go: c.go, js: describe(value), json: JSON.stringify(value) };
    if (c.insert) rec.insert = c.insert;
    rec.query = c.query;
    if (c.go === 'JSONParsed') {
        rec.fallback = c.fallback;
        rec.parsed = JSON.stringify(c.parse(value, JSON.parse(c.fallback)));
    }
    return rec;
}

function sqliteValues() {
    const Database = webRequire('better-sqlite3');
    const db = new Database(':memory:');
    const create = 'CREATE TABLE vec (id INTEGER PRIMARY KEY AUTOINCREMENT, c_text TEXT, c_integer INTEGER, c_real REAL, c_blob BLOB, c_datetime DATETIME)';
    db.exec(create);
    const quote = (s) => "'" + s.replace(/'/g, "''").replace(/\u0001/g, "' || char(1) || '") + "'";
    const cases = buildCases(sqliteCases, sqliteQueries, quote, safeJsonParseSqlite);
    for (const c of cases) if (c.insert) db.exec(c.insert);
    const out = {
        dialect: 'sqlite',
        driver: 'better-sqlite3 ' + webRequire('better-sqlite3/package.json').version,
        create,
        cases: cases.map((c) => record(c, db.prepare(c.query).get().v)),
    };
    db.close();
    fs.writeFileSync(path.join(outDir, 'values-sqlite.json'), JSON.stringify(out, null, 1) + '\n');
}

async function postgresValues() {
    const { Client } = webRequire('pg');
    const client = new Client({ connectionString: process.env.DATABASE_URL });
    await client.connect();
    const create = 'CREATE TEMP TABLE vec (id SERIAL PRIMARY KEY, c_text TEXT, c_integer INTEGER, c_smallint SMALLINT, c_bigint BIGINT, c_boolean BOOLEAN, ' +
        'c_real REAL, c_double DOUBLE PRECISION, c_numeric NUMERIC, c_timestamptz TIMESTAMPTZ, c_timestamp TIMESTAMP, c_date DATE, ' +
        'c_jsonb JSONB, c_json JSON, c_bytea BYTEA)';
    await client.query(create);
    const quote = (s) => "E'" + s.replace(/\\/g, '\\\\').replace(/'/g, "''").replace(/\n/g, '\\n').replace(/\t/g, '\\t').replace(/\u0001/g, '\\x01') + "'";
    const cases = buildCases(postgresCases, postgresQueries, quote, safeJsonParsePostgres);
    for (const c of cases) if (c.insert) await client.query(c.insert);
    const records = [];
    for (const c of cases) {
        records.push(record(c, (await client.query(c.query)).rows[0].v));
    }
    const zone = process.env.TZ || 'UTC';
    const out = {
        dialect: 'postgres',
        tz: zone,
        driver: 'pg ' + webRequire('pg/package.json').version + ', pg-types ' + webRequire('pg-types/package.json').version,
        create: create.replace('CREATE TEMP TABLE', 'CREATE TABLE'),
        cases: records,
    };
    await client.end();
    const file = 'values-postgres-' + zone.split('/').pop().toLowerCase() + '.json';
    fs.writeFileSync(path.join(outDir, file), JSON.stringify(out, null, 1) + '\n');
}

// ---- backfills: run the console's real migration on prepared rows ----

const BUNDLES = [
    { bundle_id: 'a1b2c3d4e5f6', name: 'Acme', slug: 'acme' },
    { bundle_id: 'b1b2c3d4e5f6', name: 'Acme', slug: null },
    { bundle_id: 'c1b2c3d4e5f6', name: 'ACME!', slug: '' },
    { bundle_id: 'd1b2c3d4e5f6', name: 'Büro Köln', slug: null },
    { bundle_id: 'e1b2c3d4e5f6', name: '', slug: null },
    { bundle_id: 'f1b2c3d4e5f6', name: '!!!', slug: null },
    { bundle_id: 'a2b2c3d4e5f6', name: 'A very long bundle name that does not fit the limit', slug: null },
    { bundle_id: 'b2b2c3d4e5f6', name: 'A very long bundle name that does not fit the limit', slug: null },
    { bundle_id: 'c2b2c3d4e5f6', name: 'İstanbul Ofis', slug: null },
    { bundle_id: 'd2b2c3d4e5f6', name: 'x', slug: null },
    { bundle_id: 'e2b2c3d4e5f6', name: 'Zażółć gęślą jaźń', slug: null },
    { bundle_id: 'f2b2c3d4e5f6', name: 'Straße', slug: null },
    { bundle_id: 'Z', name: '', slug: null },
    { bundle_id: 'a3b2c3d4e5f6', name: 'Support  --  Team #1', slug: null },
    { bundle_id: 'b3b2c3d4e5f6', name: 'acme 2', slug: null },
];

const SLUG_NAMES = [
    'Acme', '  Acme Corp  ', 'Büro Köln', 'Zażółć gęślą jaźń', 'Straße', 'Æther Øresund Å',
    'İstanbul', 'İzmir', 'Москва', '東京 office', 'a', 'ab', '--x--', '!!!', '', 'UPPER lower 123',
    'A very long bundle name that does not fit the limit', 'exactly-thirty-two-characters-ab', 'thirty-one characters and a dash -x',
    'tab\tand\nnewline', 'emoji 😀 name', 'čďěňřšťůýž', 'K elvin', 'under_score.dot',
    // Around the limit of 32 characters
    'a'.repeat(31), 'a'.repeat(32), 'a'.repeat(33), 'a'.repeat(31) + '-b', 'a'.repeat(32) + '-b', 'a'.repeat(30) + '--b',
];

async function backfills() {
    // The adapter picks its database when the module loads.
    const databaseUrl = process.env.DATABASE_URL;
    delete process.env.DATABASE_URL;
    process.env.DB_TYPE = 'sqlite';
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-vectors-'));
    const { getAdapter } = require(path.join(webRoot, 'services', 'dbAdapter.js'));
    const service = webRequire('./services/agentBundleService');
    const { hashAccessToken } = webRequire('./lib/tokenHash');
    const adapter = getAdapter({ dbPath: path.join(dir, 'db_v2.sqlite3'), dataDir: dir, authDbPath: path.join(dir, 'auth.db') });
    await adapter.init();
    const db = adapter.getSqliteMainDb();

    const scenarios = [];
    for (const columnMissing of [false, true]) {
        db.exec('DELETE FROM agent_bundles');
        const rows = BUNDLES.map((b) => ({ ...b, slug: columnMissing ? null : b.slug }));
        const insert = db.prepare('INSERT INTO agent_bundles (bundle_id, name, slug) VALUES (?, ?, ?)');
        for (const b of rows) insert.run(b.bundle_id, b.name, b.slug);
        if (columnMissing) {
            db.exec('DROP INDEX idx_agent_bundles_slug');
            db.exec('ALTER TABLE agent_bundles DROP COLUMN slug');
        }
        await adapter.init();
        const want = {};
        for (const r of db.prepare('SELECT bundle_id, slug FROM agent_bundles').all()) want[r.bundle_id] = r.slug;
        scenarios.push({
            name: columnMissing ? 'the table has no slug column' : 'some bundles have a slug',
            column_missing: columnMissing,
            rows,
            want,
        });
    }

    const tokens = ['abc', 'token-2', 'token-3', '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', 'töken €'];
    const tokenHashes = {};
    for (const t of tokens) tokenHashes[t] = hashAccessToken(t);

    // What the PostgreSQL driver's date parser makes of a fraction of a
    // second: [microseconds, milliseconds of the Date].
    const parseDate = webRequire('postgres-date');
    const fractions = [];
    const addFraction = (micros) => {
        const digits = String(micros).padStart(6, '0').replace(/0+$/, '');
        const text = '2026-01-01 00:00:00' + (digits ? '.' + digits : '') + '+00';
        fractions.push([micros, parseDate(text).getTime() - Date.UTC(2026, 0, 1)]);
    };
    for (let ms = 0; ms < 1000; ms++) {
        addFraction(ms * 1000);
        addFraction(ms * 1000 + 999);
    }
    let seed = 12345;
    for (let i = 0; i < 2000; i++) {
        seed = (seed * 1103515245 + 12345) % 2147483648;
        addFraction(seed % 1000000);
    }

    // Date.toISOString and the console's expiry format for some instants.
    const instants = [0, 1, 999, 1762736751694, -1, -62135596800000, -62198755200000, 253402300799999, 253402300800000, 8640000000000000]
        .map((ms) => ({ ms, iso: new Date(ms).toISOString(), expiry: new Date(ms).toISOString().replace('T', ' ').replace('Z', '') }));

    fs.writeFileSync(path.join(outDir, 'fractions.json'), JSON.stringify(fractions) + '\n');

    const out = {
        instants,
        slugify: SLUG_NAMES.map((name) => ({ name, slug: service.slugifyName(name) })),
        scenarios,
        token_hashes: tokenHashes,
    };
    await adapter.close();
    if (databaseUrl) process.env.DATABASE_URL = databaseUrl;
    fs.rmSync(dir, { recursive: true, force: true });
    fs.writeFileSync(path.join(outDir, 'backfills.json'), JSON.stringify(out, null, 1) + '\n');
}

(async () => {
    fs.mkdirSync(outDir, { recursive: true });
    await backfills();
    sqliteValues();
    if (process.env.DATABASE_URL) await postgresValues();
})().catch((err) => {
    console.error('vectors:', err);
    process.exit(1);
});
