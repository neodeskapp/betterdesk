'use strict';

/**
 * Records db.getPeerById(id) of the old console's real database adapter
 * (services/dbAdapter.js) on a scratch database: the device parsePeer
 * made, or the error, for devices whose columns hold every kind of value,
 * devices only the signal server's table has (which the lookup copies),
 * and what the console's table holds after the lookups. SQLite in a temp
 * folder, or PostgreSQL in a schema of its own when ORACLE_PG_DSN names a
 * server. See README.md.
 *
 *   node peer.js        writes ../node_peer_sqlite.json or ../node_peer_postgres.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const pg = !!PG_DSN;
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'peer-oracle-'));
const schema = `oracle_${process.pid}_${Date.now() % 1000000}`;
const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

async function main() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
    let admin = null;
    if (pg) {
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
    const warnings = [];
    console.warn = (...a) => warnings.push(a.join(' '));
    console.log = () => {};
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
    const yes = pg ? true : 1;
    const no = pg ? false : 0;
    const peer = (id, cols) => {
        const names = Object.keys(cols);
        return exec(`INSERT INTO peer (id, ${names.map((n) => `"${n}"`).join(', ')}) VALUES (?, ${names.map(() => '?').join(', ')})`,
            [id, ...names.map((n) => cols[n])]);
    };
    const rows = [
        ['P1', { note: 'note one', info: '{"hostname":"host one","os":"Linux","ip":"10.0.0.1"}', tags: 'a,b', folder_id: 4, status_online: 1, is_banned: 0, user: 'alice', uuid: 'u-1', ip: '1.2.3.4' }],
        ['P2', { note: '', info: '{"hostname":"host two","platform":"Windows"}', tags: ',a,,b,', folder_id: 0 }],
        ['P3', { info: '{"hostname":5,"os":{"x":1}}', tags: '' }],
        ['P4', { note: 'n4', info: '[1]' }],
        ['P5', { info: '"text"' }],
        ['P6', { info: '"{\\"hostname\\":\\"in a text\\"}"' }],
        ['P7', { info: '5' }],
        ['P8', { info: 'null', note: 'note over null' }],
        ['P9', { info: '"null"' }],
        ['P10', { info: null, note: null }],
        ['P11', { info: '{"hostname":"","os":0,"platform":false,"ip":""}', ip: '' }],
        ['P12', { info: '{"hostname":"deleted"}', is_deleted: yes }],
        ['P14', { info: '{"hostname":"first","hostname":"last"}' }],
        ['P15', { info: 'true' }],
    ];
    if (!pg) {
        // A JSON column refuses half of a surrogate pair
        rows.push(['P13', { info: '{"hostname":"\\ud800 half","os":"x\\u2028y"}' }]);
        rows.push(['P16', { info: 'not json', note: 'broken' }]);
        rows.push(['P17', { info: '' }]);
        rows.push(['P18', { info: 7 }]);
    }
    for (const [id, cols] of rows) {
        if (pg) delete cols.tags;
        if (pg && cols.status_online !== undefined) cols.status_online = !!cols.status_online;
        if (pg && cols.is_banned !== undefined) cols.is_banned = !!cols.is_banned;
        await peer(id, cols);
    }
    const lookup = async (id) => {
        try {
            const device = await adapter.getPeerById(id);
            if (!device) return { device: null };
            const out = {};
            for (const key of ['id', 'uuid', 'hostname', 'username', 'platform', 'ip', 'note', 'online', 'banned', 'ban_reason', 'folder_id', 'tags']) {
                out[key] = device[key] === undefined ? '<undefined>' : device[key];
            }
            if (!pg) out.info = device.info;
            out.pk = device.pk === null ? null : Buffer.isBuffer(device.pk) ? `buffer:${device.pk.toString('hex')}` : device.pk;
            return { device: out };
        } catch (err) {
            return { error: err.message };
        }
    };
    const steps = [];
    const ids = rows.map(([id]) => id).concat(['NOPE', 'S1', 'S2', 'S3']);
    // Before the signal server's table exists
    for (const id of ['NOPE', 'S1']) steps.push({ name: `no peers table ${id}`, id, ...(await lookup(id)) });
    if (pg) {
        await exec(`CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', pk BYTEA, hostname TEXT DEFAULT '', os TEXT DEFAULT '', version TEXT DEFAULT '',
            ip TEXT DEFAULT '', "user" TEXT DEFAULT '', status TEXT DEFAULT 'OFFLINE', last_online TIMESTAMPTZ, created_at TIMESTAMPTZ DEFAULT now(),
            banned BOOLEAN DEFAULT FALSE, banned_at TIMESTAMPTZ, ban_reason TEXT DEFAULT '', soft_deleted BOOLEAN DEFAULT FALSE, tags TEXT DEFAULT '')`);
    } else {
        await exec(`CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', pk BLOB, hostname TEXT DEFAULT '', os TEXT DEFAULT '', version TEXT DEFAULT '',
            ip TEXT DEFAULT '', "user" TEXT DEFAULT '', status TEXT DEFAULT 'OFFLINE', last_online TEXT, created_at TEXT DEFAULT '2019-03-04 08:00:00',
            banned INTEGER DEFAULT 0, banned_at TEXT, ban_reason TEXT DEFAULT '', soft_deleted INTEGER DEFAULT 0, tags TEXT DEFAULT '')`);
    }
    await exec(`INSERT INTO peers (id, uuid, hostname, os, version, ip, "user", status, banned, ban_reason, tags) VALUES ('S1', 'su-1', 'from-signal', 'Linux', '1.4', '9.9.9.9', 'bob', 'ONLINE', ?, 'r', 'x,y')`, [no]);
    await exec(`INSERT INTO peers (id, hostname, soft_deleted) VALUES ('S2', 'deleted there', ?)`, [yes]);
    await exec(`INSERT INTO peers (id, hostname, os, banned) VALUES ('P12', 'back from deleted', 'BSD', ?)`, [yes]);
    for (const id of ids) steps.push({ name: id, id, ...(await lookup(id)) });
    const table = await exec(`SELECT id, note, ${pg ? 'info::text AS info' : 'info'}, status_online, is_banned, is_deleted, ${pg ? '' : 'tags, '}"user", uuid FROM peer ORDER BY id`);
    await adapter.close();
    if (pool) await pool.end();
    if (admin) {
        await admin.query(`DROP SCHEMA ${schema} CASCADE`);
        await admin.end();
    }
    const file = path.join(__dirname, '..', `node_peer_${pg ? 'postgres' : 'sqlite'}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ rows, steps, table, warnings }, null, 1)}\n`);
    process.stdout.write(`${file}: ${steps.length} lookups\n`);
    process.exit(0);
}

main().catch((err) => { process.stderr.write(`${err.stack}\n`); process.exit(1); });
