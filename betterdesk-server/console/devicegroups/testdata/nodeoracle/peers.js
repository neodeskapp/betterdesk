'use strict';

/**
 * Runs the reads of the Node console's database adapter that copy the
 * signal server's devices (peers) into the console's own table (peer)
 * first: getPeerStats and getAllPeers, with syncGoPeers and the id
 * changes it carries over. Records what they return and the rows of peer
 * and its side tables afterwards, for TestPeerSyncAgainstNode. See
 * README.md.
 *
 *   node peers.js                                ../peers_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node peers.js   ../peers_postgres.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'devicegroups-peers-oracle-'));
const schema = `oracle_peers_${process.pid}`;
const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

async function open() {
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
    }
    process.chdir(WEB);
    const adapter = web('services/dbAdapter').getAdapter();
    let exec;
    let pool = null;
    if (pg) {
        const { Pool } = web('node_modules/pg');
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
        exec = async (sql, params = []) => (await pool.query(rebind(sql), params)).rows;
    } else {
        exec = async (sql, params = []) => {
            const stmt = adapter.getSqliteMainDb().prepare(sql);
            if (stmt.reader) return stmt.all(...params);
            stmt.run(...params);
            return [];
        };
    }
    const close = async () => {
        try { await adapter.close(); } catch (_) { /* closed */ }
        if (pool) await pool.end();
        if (admin) { await admin.query(`DROP SCHEMA ${schema} CASCADE`); await admin.end(); }
    };
    return { adapter, exec, close };
}

// The signal server's tables as it creates them (betterdesk-server/db)
const serverTables = pg ? [
    `CREATE TABLE IF NOT EXISTS peers (
        id TEXT PRIMARY KEY, uuid TEXT NOT NULL DEFAULT '', pk BYTEA DEFAULT NULL, ip TEXT NOT NULL DEFAULT '',
        "user" TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '',
        version TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'OFFLINE', nat_type INTEGER NOT NULL DEFAULT 0,
        last_online TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), disabled BOOLEAN NOT NULL DEFAULT FALSE,
        banned BOOLEAN NOT NULL DEFAULT FALSE, ban_reason TEXT NOT NULL DEFAULT '', banned_at TIMESTAMPTZ,
        soft_deleted BOOLEAN NOT NULL DEFAULT FALSE, deleted_at TIMESTAMPTZ, note TEXT NOT NULL DEFAULT '',
        tags TEXT NOT NULL DEFAULT '', heartbeat_seq BIGINT NOT NULL DEFAULT 0, device_type TEXT NOT NULL DEFAULT '',
        linked_peer_id TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '', guid TEXT NOT NULL DEFAULT '')`,
    `CREATE TABLE IF NOT EXISTS id_change_history (
        id BIGSERIAL PRIMARY KEY, old_id TEXT NOT NULL, new_id TEXT NOT NULL,
        changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), reason TEXT NOT NULL DEFAULT '')`,
] : [
    `CREATE TABLE IF NOT EXISTS peers (
        id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', pk BLOB DEFAULT NULL, ip TEXT DEFAULT '', user TEXT DEFAULT '',
        hostname TEXT DEFAULT '', os TEXT DEFAULT '', version TEXT DEFAULT '', status TEXT DEFAULT 'OFFLINE',
        nat_type INTEGER DEFAULT 0, last_online TEXT DEFAULT '', created_at TEXT DEFAULT (datetime('now')),
        disabled INTEGER DEFAULT 0, banned INTEGER DEFAULT 0, ban_reason TEXT DEFAULT '', banned_at TEXT DEFAULT NULL,
        soft_deleted INTEGER DEFAULT 0, deleted_at TEXT DEFAULT NULL, note TEXT DEFAULT '', tags TEXT DEFAULT '',
        heartbeat_seq INTEGER DEFAULT 0, guid TEXT DEFAULT '')`,
    `CREATE TABLE IF NOT EXISTS id_change_history (
        id INTEGER PRIMARY KEY AUTOINCREMENT, old_id TEXT NOT NULL, new_id TEXT NOT NULL,
        changed_at TEXT DEFAULT (datetime('now')), reason TEXT DEFAULT '')`,
];

const ts = (text) => (pg ? text + '+00' : text);
const yes = pg ? true : 1;
const no = pg ? false : 0;
const blob = pg ? "decode('0102ff', 'hex')" : "X'0102FF'";
const insertPeer = `INSERT INTO peers (id, uuid, pk, ip, "user", hostname, os, version, status, last_online, created_at, banned, ban_reason, banned_at, soft_deleted, tags)
    VALUES (?, ?, ${'?'}, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`;
const seed = [
    ...serverTables.map((s) => [s, []]),
    // The server's devices
    [insertPeer, ['P1', 'uuid-1', null, '10.0.0.1', 'ann', 'host-1', 'Windows 11', '1.4.2', 'ONLINE', ts('2020-01-01 10:00:00'), ts('2019-01-01 00:00:00'), no, '', null, no, 'a,b']],
    [insertPeer, ['P2', '', null, '', '', '', 'Linux', '', 'OFFLINE', ts('2020-01-02 10:00:00'), ts('2019-01-02 00:00:00'), yes, 'stolen', ts('2020-02-02 10:00:00'), no, '']],
    [insertPeer, ['P3', 'uuid-3', null, '10.0.0.3', 'cy', 'host-3', 'macOS', '1.0', 'ONLINE', ts('2020-01-03 10:00:00'), ts('2019-01-03 00:00:00'), no, '', null, yes, 'gone']],
    [insertPeer, ['NEW2', 'uuid-n2', null, '10.0.0.4', 'dee', 'host-n2', 'Android', '1.1', 'ONLINE', null, ts('2019-01-04 00:00:00'), no, '', null, no, 'x']],
    [insertPeer, ['OLD3', 'uuid-o3', null, '', '', 'host-o3', '', '', 'OFFLINE', ts('2020-01-05 10:00:00'), ts('2019-01-05 00:00:00'), no, '', null, no, '']],
    [insertPeer, ['NEW3', 'uuid-n3', null, '', '', 'host-n3', '', '', 'ONLINE', ts('2020-01-06 10:00:00'), ts('2019-01-06 00:00:00'), no, '', null, no, 'n3']],
    [`UPDATE peers SET pk = ${blob} WHERE id = 'P1'`, []],
    ["INSERT INTO id_change_history (old_id, new_id) VALUES ('OLD2', 'NEW2')", []],
    ["INSERT INTO id_change_history (old_id, new_id) VALUES ('OLD3', 'NEW3')", []],
    // The console's copy: rows the copy refreshes, keeps, or carries to a new id
    [`INSERT INTO peer (id, uuid, info, ip, "user", status_online, last_online, created_at, is_deleted, is_banned, banned_reason${pg ? '' : ', tags'})
      VALUES ('P1', 'old-uuid', '{}', '', '', ${no}, ?, ?, ${yes}, ${no}, ''${pg ? '' : ", 'old'"})`, [ts('2018-01-01 00:00:00'), ts('2018-01-01 00:00:00')]],
    [`INSERT INTO peer (id, uuid, info, ip, "user", status_online, last_online, created_at, is_deleted, is_banned, banned_reason, note${pg ? '' : ', tags'})
      VALUES ('P2', '', '{"hostname":"kept","os":"Kept OS"}', '1.1.1.1', 'keep', ${yes}, NULL, ?, ${no}, ${no}, '', 'a note'${pg ? '' : ", 'keep'"})`, [ts('2018-01-02 00:00:00')]],
    [`INSERT INTO peer (id, uuid, info, status_online, created_at) VALUES ('OLD2', 'uuid-o2', '{"hostname":"old two"}', ${no}, ?)`, [ts('2018-01-03 00:00:00')]],
    [`INSERT INTO peer (id, uuid, info, status_online, created_at) VALUES ('OLD3', 'uuid-o3', '{"hostname":"old three"}', ${no}, ?)`, [ts('2018-01-04 00:00:00')]],
    [`INSERT INTO peer (id, uuid, info, status_online, created_at, folder_id) VALUES ('P5', 'uuid-5', ?, ${yes}, ?, 3)`, [pg ? '{"ip":"9.9.9.9","platform":"Plan 9"}' : '{"ip":"9.9.9.9","platform":"Plan 9"}', ts('2018-01-05 00:00:00')]],
    // A flag of 2: online and banned only for the PostgreSQL adapter's test
    [`INSERT INTO peer (id, uuid, info, status_online, is_banned, created_at) VALUES ('P7', 'u7', '{}', ${pg ? 'TRUE' : 2}, ${pg ? 'TRUE' : 2}, ?)`, [ts('2018-01-07 00:00:00')]],
    ["INSERT INTO peer_sysinfo (peer_id, hostname) VALUES ('OLD3', 'sys-old3')", []],
    ["INSERT INTO peer_metrics (peer_id, cpu_usage) VALUES ('OLD3', 5)", []],
    ["INSERT INTO folders (id, name) VALUES (3, 'Three')", []],
    ["INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('OLD3', 3)", []],
];

const rowsOf = {
    peer: `SELECT id, uuid, ${pg ? "encode(pk, 'hex')" : 'hex(pk)'} AS pk, note, CAST(created_at AS TEXT) AS created_at, status_online,
        CAST(last_online AS TEXT) AS last_online, is_deleted, CAST(info AS TEXT) AS info, ip, "user", is_banned,
        CAST(banned_at AS TEXT) AS banned_at, banned_reason, folder_id${pg ? '' : ', tags'} FROM peer ORDER BY id`,
    side: `SELECT 'sysinfo' AS t, peer_id AS id FROM peer_sysinfo UNION ALL SELECT 'metrics', peer_id FROM peer_metrics
        UNION ALL SELECT 'folder', device_id FROM device_folder_assignments ORDER BY t, id`,
};

(async () => {
    const db = await open();
    await db.adapter.init();
    for (const [sql, params] of seed) await db.exec(sql, params);
    const steps = [];
    const record = async (op, fn, sql) => {
        try {
            steps.push({ op, ...(sql ? { sql } : {}), result: JSON.stringify(await fn()) });
        } catch (err) {
            steps.push({ op, error: err.message });
        }
    };
    const rows = (name) => record('rows', () => db.exec(rowsOf[name]), rowsOf[name]);
    const quiet = console.warn;
    console.warn = () => {};
    await record('getPeerStats', () => db.adapter.getPeerStats());
    await rows('peer');
    await rows('side');
    await record('getAllPeers', () => db.adapter.getAllPeers());
    // Within 30 seconds of the copy a read does not copy again
    const change = "UPDATE peers SET status = 'OFFLINE', tags = 'changed' WHERE id = 'P1'";
    await db.exec(change);
    steps.push({ op: 'exec', sql: change });
    await record('getAllPeers', () => db.adapter.getAllPeers());
    await rows('peer');
    console.warn = quiet;
    await db.close();
    const file = path.join(__dirname, '..', `peers_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed, steps }, null, 1)}\n`);
    console.log(`${file}: ${steps.length} steps`);
})().catch((err) => { console.error(err); process.exit(1); });
