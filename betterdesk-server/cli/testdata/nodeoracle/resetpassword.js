'use strict';

// Records what scripts/reset-password.js of the old console does, run as
// the installers ran it, on SQLite files and (with
// BETTERDESK_TEST_POSTGRES_DSN) on PostgreSQL schemas, into
// ../resetpassword.json.gz (replayed by cli/resetpassword_test.go).
//
//   node resetpassword.js   (in the dev image on the network of the test
//                            PostgreSQL: tools/gotest.sh's DEV_DOCKER_ARGS)

const fs = require('fs');
const path = require('path');
const { REPO, tmpdir, run, replace, writeVectors } = require('./lib');

const bcrypt = require(path.join(REPO, 'node_modules', 'bcrypt'));
const Database = require(path.join(REPO, 'node_modules', 'better-sqlite3'));
const { Client } = require(path.join(REPO, 'node_modules', 'pg'));

const OLD_HASH = bcrypt.hashSync('the-old-password', 4);

// Users tables as the database may already have them
const tables = {
    console: fs.readFileSync('/repo/betterdesk-server/console/store/schema/sqlite/users.sql', 'utf8'),
    server: `CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'viewer',
			auth_provider TEXT NOT NULL DEFAULT 'local',
			totp_secret TEXT DEFAULT '',
			totp_enabled INTEGER DEFAULT 0,
			totp_recovery_codes TEXT DEFAULT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			last_login TEXT DEFAULT ''
		)`,
};

function setupSQLite(file, kind) {
    if (kind === 'nodir') return;
    fs.mkdirSync(path.dirname(file), { recursive: true });
    if (kind === 'none') return;
    if (kind === 'emptyfile') { fs.writeFileSync(file, ''); return; }
    if (kind === 'notadb') { fs.writeFileSync(file, 'this is not a database, it is long enough to have a header...........................'); return; }
    const db = new Database(file);
    db.exec(tables[kind]);
    db.prepare('INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)').run('admin', OLD_HASH, 'admin');
    db.prepare('INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)').run('operator1', OLD_HASH, 'operator');
    db.close();
}

function sqliteState(file, password) {
    if (!fs.existsSync(file)) return { exists: false };
    let db;
    try {
        db = new Database(file, { readonly: true, fileMustExist: true });
        const table = db.prepare("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'").get();
        const rows = table ? db.prepare('SELECT username, role, password_hash FROM users ORDER BY id').all() : null;
        return { exists: true, table: table ? table.sql : null, rows: rows && rows.map((r) => describe(r, password)) };
    } catch (err) {
        return { exists: true, error: err.message };
    } finally {
        if (db) db.close();
    }
}

function describe(row, password) {
    const hash = String(row.password_hash);
    return {
        username: row.username, role: row.role, prefix: hash.slice(0, 7), length: hash.length,
        old: hash === OLD_HASH, verifies: password != null && hash.startsWith('$2') && bcrypt.compareSync(password, hash),
    };
}

const passwords = {
    plain: 'Neu-Passw0rd',
    short: 'short',
    six: 'sixsix',
    accent5: 'abécd',
    accent6: 'abécde',
    emoji6: 'a\u{1F600}bcd',
    emoji4: '\u{1F600}\u{1F600}',
    long: 'x'.repeat(80),
    long72: 'y'.repeat(71) + 'éz',
    spaces: ' pass word 1 ',
    quotes: 'p@$$"w\'rd`',
};

const vectors = [];

function recordSQLite(name, { setup = 'console', db = '{ROOT}/db/db_v2.sqlite3', env = {}, dotenv = null, argv, cwdData = false, makeDirs = [] }) {
    const root = tmpdir('bd-reset-');
    const consoleDir = path.join(root, 'console');
    fs.mkdirSync(path.join(consoleDir, 'scripts'), { recursive: true });
    fs.copyFileSync(path.join(REPO, 'scripts', 'reset-password.js'), path.join(consoleDir, 'scripts', 'reset-password.js'));
    const work = path.join(root, 'work');
    fs.mkdirSync(work);
    if (cwdData) fs.mkdirSync(path.join(work, 'data'));
    for (const dir of makeDirs) fs.mkdirSync(dir.split('{ROOT}').join(root), { recursive: true });
    const dbFile = db.split('{ROOT}').join(root);
    setupSQLite(dbFile, setup);
    if (dotenv !== null) fs.writeFileSync(path.join(consoleDir, '.env'), dotenv.split('{ROOT}').join(root));
    const realEnv = Object.fromEntries(Object.entries(env).map(([k, v]) => [k, v.split('{ROOT}').join(root)]));
    const realArgv = argv.map((a) => passwords[a] ?? a);
    const res = run(path.join(consoleDir, 'scripts', 'reset-password.js'), realArgv, { cwd: work, env: realEnv });
    const pairs = [[root, '{ROOT}']];
    vectors.push({
        name, database: 'sqlite', setup, db, env, dotenv, argv, cwdData, makeDirs,
        status: res.status, stdout: replace(res.stdout, pairs), stderr: replace(res.stderr, pairs),
        state: sqliteState(dbFile, realArgv[0] && passwords[argv[0]] !== undefined ? realArgv[0] : null),
    });
    fs.rmSync(root, { recursive: true, force: true });
}

const DB = { DB_PATH: '{ROOT}/db/db_v2.sqlite3' };
for (const setup of ['console', 'server', 'none', 'emptyfile', 'notadb']) {
    for (const argv of [['plain'], ['plain', 'admin'], ['plain', 'operator1'], ['plain', 'newadmin'], ['plain', ''], ['short'], [], ['six', 'x y']]) {
        recordSQLite(`sqlite/${setup}/${argv.join('+') || 'none'}`, { setup, env: DB, argv });
    }
}
for (const pw of Object.keys(passwords)) {
    recordSQLite(`sqlite/password/${pw}`, { env: DB, argv: [pw] });
    recordSQLite(`sqlite/password/${pw}/new`, { env: DB, argv: [pw, 'fresh-user'] });
}
// Where the database is looked for
const where = [
    { name: 'db_url', env: { DB_URL: '{ROOT}/db/db_v2.sqlite3' } },
    { name: 'db_path_wins', env: { DB_PATH: '{ROOT}/db/db_v2.sqlite3', DB_URL: '{ROOT}/other/db_v2.sqlite3' } },
    { name: 'keys_path', env: { KEYS_PATH: '{ROOT}/db' } },
    { name: 'rustdesk_dir', env: { RUSTDESK_DIR: '{ROOT}/db' } },
    { name: 'keys_wins', env: { KEYS_PATH: '{ROOT}/db', RUSTDESK_DIR: '{ROOT}/other' } },
    { name: 'data_dir', env: { DATA_DIR: '{ROOT}/db' } },
    { name: 'rustdesk_data', env: { RUSTDESK_DATA: '{ROOT}/db' } },
    { name: 'data_dir_wins', env: { DATA_DIR: '{ROOT}/db', RUSTDESK_DATA: '{ROOT}/other' }, makeDirs: ['{ROOT}/other'] },
    { name: 'cwd_data', env: {}, cwdData: true, db: '{ROOT}/work/data/db_v2.sqlite3' },
    { name: 'nothing', env: {}, db: '{ROOT}/work/data/db_v2.sqlite3', setup: 'nodir' },
    { name: 'missing_dir', env: { DB_PATH: '{ROOT}/nope/db_v2.sqlite3' }, db: '{ROOT}/nope/db_v2.sqlite3', setup: 'nodir' },
    { name: 'relative', env: { DB_PATH: 'rel.sqlite3' }, db: '{ROOT}/work/rel.sqlite3', setup: 'none' },
    { name: 'dotenv', env: {}, dotenv: '# from the file\nDB_PATH={ROOT}/db/db_v2.sqlite3\n' },
    { name: 'dotenv_spaces', env: {}, dotenv: '  DB_PATH = {ROOT}/db/db_v2.sqlite3  \r\nDB_TYPE= sqlite \n' },
    { name: 'dotenv_env_wins', env: { DB_PATH: '{ROOT}/db/db_v2.sqlite3' }, dotenv: 'DB_PATH={ROOT}/other/db_v2.sqlite3\n' },
    { name: 'dotenv_empty_env', env: { DB_PATH: '' }, dotenv: 'DB_PATH={ROOT}/db/db_v2.sqlite3\n' },
    { name: 'dotenv_first', env: {}, dotenv: 'DB_PATH={ROOT}/db/db_v2.sqlite3\nDB_PATH={ROOT}/other/db_v2.sqlite3\n' },
    { name: 'type_case', env: { DB_TYPE: 'SQLite', DB_PATH: '{ROOT}/db/db_v2.sqlite3' } },
    { name: 'type_other', env: { DB_TYPE: 'mysql', DB_PATH: '{ROOT}/db/db_v2.sqlite3' } },
    { name: 'postgres_no_url', env: { DB_TYPE: 'postgres', DB_PATH: '{ROOT}/db/db_v2.sqlite3' } },
    { name: 'postgresql_no_url', env: { DB_TYPE: 'PostgreSQL' } },
    { name: 'postgres_bad_url', env: { DB_TYPE: 'postgres', DATABASE_URL: 'host=localhost dbname=x' } },
];
for (const w of where) {
    for (const argv of [['plain'], ['plain', 'operator1'], []]) {
        recordSQLite(`where/${w.name}/${argv.join('+') || 'none'}`, { ...w, argv });
    }
}

async function postgres() {
    const dsn = process.env.BETTERDESK_TEST_POSTGRES_DSN;
    if (!dsn) {
        console.log('no BETTERDESK_TEST_POSTGRES_DSN: PostgreSQL cases left out');
        return;
    }
    const admin = new Client({ connectionString: dsn });
    await admin.connect();
    const pgTables = {
        console: fs.readFileSync('/repo/betterdesk-server/console/store/schema/postgres/users.sql', 'utf8'),
        none: null,
    };
    let n = 0;
    for (const setup of Object.keys(pgTables)) {
        for (const argv of [['plain'], ['plain', 'operator1'], ['plain', 'newadmin'], ['short'], [], ['emoji6', 'x']]) {
            const schema = `bd_reset_${process.pid}_${n++}`;
            await admin.query(`CREATE SCHEMA ${schema}`);
            await admin.query(`SET search_path TO ${schema}`);
            if (pgTables[setup]) {
                await admin.query(pgTables[setup]);
                await admin.query('INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3), ($4, $2, $5)', ['admin', OLD_HASH, 'admin', 'operator1', 'operator']);
            }
            const url = new URL(dsn);
            url.searchParams.set('options', `-c search_path=${schema}`);
            const realArgv = argv.map((a) => passwords[a] ?? a);
            const work = tmpdir('bd-reset-pg-');
            const consoleDir = path.join(work, 'console', 'scripts');
            fs.mkdirSync(consoleDir, { recursive: true });
            fs.copyFileSync(path.join(REPO, 'scripts', 'reset-password.js'), path.join(consoleDir, 'reset-password.js'));
            const res = run(path.join(consoleDir, 'reset-password.js'), realArgv, { cwd: work, env: { DB_TYPE: 'postgres', DATABASE_URL: url.toString() } });
            const exists = (await admin.query("SELECT to_regclass($1) AS t", [`${schema}.users`])).rows[0].t;
            let rows = null;
            if (exists) {
                rows = (await admin.query(`SELECT username, role, password_hash FROM ${schema}.users ORDER BY id`)).rows
                    .map((r) => describe(r, passwords[argv[0]] !== undefined ? realArgv[0] : null));
            }
            const columns = exists ? (await admin.query('SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position', [schema, 'users'])).rows.map((r) => r.column_name) : null;
            vectors.push({
                name: `postgres/${setup}/${argv.join('+') || 'none'}`, database: 'postgres', setup, argv, env: { DB_TYPE: 'postgres' },
                status: res.status, stdout: res.stdout, stderr: res.stderr,
                state: { exists: !!exists, columns, rows },
            });
            await admin.query(`DROP SCHEMA ${schema} CASCADE`);
            fs.rmSync(work, { recursive: true, force: true });
        }
    }
    await admin.end();
}

postgres().then(() => writeVectors('resetpassword', vectors)).catch((err) => { console.error(err); process.exit(1); });
