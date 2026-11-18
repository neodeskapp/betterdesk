'use strict';

/**
 * Runs the periodic clean-ups of the Node console's database adapter on
 * rows of every age: runIntegrationHousekeeping (heartbeat metrics older
 * than 7 days, client audit rows older than 90 days) and
 * cleanupStaleOnlinePeers (devices whose last contact is too old are set
 * offline), and reads the two settings the way server.js read them.
 * Records the seed statements, which rows are left or changed, and the
 * timer delays Node used, for the Go tests. See README.md.
 *
 *   node housekeeping.js                                ../housekeeping_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node housekeeping.js   ../housekeeping_postgres.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const pg = dialect === 'postgres';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'housekeeping-oracle-'));
const schema = `oracle_housekeeping_${process.pid}`;
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

// A time some hours or seconds before now, in the forms the tables hold
const ago = {
    hours: (h) => (pg ? `NOW() - INTERVAL '${h} hours'` : `datetime('now', '-${h} hours')`),
    seconds: (s) => (pg ? `NOW() - INTERVAL '${s} seconds'` : `datetime('now', '-${s} seconds')`),
};
// SQLite only: other text forms the column can hold
const sqliteForms = [
    (h) => `strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-${h} hours')`,
    (h) => `CAST(strftime('%s', 'now', '-${h} hours') AS INTEGER)`,
    (h) => `strftime('%Y-%m-%d', 'now', '-${h} hours')`,
];
const odd = pg ? ['NULL'] : ['NULL', "''", "'garbage'", "'9999-12-31 23:59:59'", "'0000'", "' 2000-01-01'"];

const insert = {
    peer_metrics: (at) => `INSERT INTO peer_metrics (peer_id, cpu_usage, created_at) VALUES ('m', 1, ${at})`,
    audit_connections: (at) => `INSERT INTO audit_connections (host_id, action, created_at) VALUES ('h', 'new', ${at})`,
    audit_files: (at) => `INSERT INTO audit_files (host_id, created_at) VALUES ('h', ${at})`,
    audit_alarms: (at) => `INSERT INTO audit_alarms (alarm_type, created_at) VALUES (1, ${at})`,
};
const ages = { peer_metrics: [1, 100, 167, 169, 500, 3000], audit_connections: [1, 169, 2159, 2161, 5000], audit_files: [1, 2159, 2161], audit_alarms: [1, 2159, 2161, 9000] };

function rowsSeed() {
    const seed = [];
    for (const table of Object.keys(insert)) {
        for (const h of ages[table]) {
            seed.push(insert[table](ago.hours(h)));
            if (!pg) for (const form of sqliteForms) seed.push(insert[table](form(h)));
        }
        for (const value of odd) seed.push(insert[table](value));
    }
    return seed;
}

const leftQuery = `SELECT 'peer_metrics' AS t, id FROM peer_metrics UNION ALL SELECT 'audit_connections', id FROM audit_connections
    UNION ALL SELECT 'audit_files', id FROM audit_files UNION ALL SELECT 'audit_alarms', id FROM audit_alarms ORDER BY t, id`;

// Devices of the console's copy: online or not, last contact some seconds ago
// Far from every threshold read below, so a slow run lands on the same side
const peerAges = [45, 200, 100000, -300];
function peersSeed() {
    const seed = [pg ? 'DELETE FROM peer' : 'DELETE FROM peer'];
    let n = 0;
    for (const online of [true, false]) {
        for (const s of peerAges) {
            const at = s < 0 ? (pg ? `NOW() + INTERVAL '${-s} seconds'` : `datetime('now', '+${-s} seconds')`) : ago.seconds(s);
            seed.push(`INSERT INTO peer (id, status_online, last_online) VALUES ('d${++n}', ${online ? (pg ? 'TRUE' : 1) : (pg ? 'FALSE' : 0)}, ${at})`);
        }
        seed.push(`INSERT INTO peer (id, status_online, last_online) VALUES ('d${++n}', ${online ? (pg ? 'TRUE' : 1) : (pg ? 'FALSE' : 0)}, NULL)`);
        if (!pg) {
            for (const text of ["''", "'garbage'", `strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-1000 seconds')`, "CAST(strftime('%s', 'now', '-1000 seconds') AS INTEGER)"]) {
                seed.push(`INSERT INTO peer (id, status_online, last_online) VALUES ('d${++n}', ${online ? 1 : 0}, ${text})`);
            }
        }
    }
    if (!pg) seed.push("INSERT INTO peer (id, status_online, last_online) VALUES ('d-two', 2, datetime('now', '-1000 seconds'))");
    return seed;
}
const peerQuery = 'SELECT id, status_online FROM peer ORDER BY id';

// Settings as server.js read them, and the delay its timer used
const settingValues = ['', '15', '90', '30', '5', '1', '0', '-0', '-5', '+7', ' 12', '\t\n 8', ' 9', ' 9', '﻿10', '1e3', '0x10',
    '3.9', '12abc', 'abc', ' ', '-', '+', '2147483', '2147484', '99999999999', '999999999999999999999', '9'.repeat(400), '٣', '007'];

(async () => {
    const db = await open();
    await db.adapter.init();
    const out = { dialect, integration: [], stale: [], settings: [] };

    // The hourly clean-up on a full set of rows
    const seed = rowsSeed();
    for (const sql of seed) await db.exec(sql);
    let error = null;
    try { await db.adapter.runIntegrationHousekeeping(); } catch (err) { error = err.message; }
    out.integration.push({ seed, error, left: await db.exec(leftQuery) });

    // A table that is gone ends the run there: the tables after it keep their rows
    const second = [...Object.keys(insert).map((t) => `DELETE FROM ${t}`), ...rowsSeed(), 'DROP TABLE audit_files'];
    for (const sql of second) await db.exec(sql);
    error = null;
    try { await db.adapter.runIntegrationHousekeeping(); } catch (err) { error = err.message; }
    const leftWithout = `SELECT 'peer_metrics' AS t, id FROM peer_metrics UNION ALL SELECT 'audit_connections', id FROM audit_connections
        UNION ALL SELECT 'audit_alarms', id FROM audit_alarms ORDER BY t, id`;
    out.integration.push({ seed: second, error: error !== null, left: await db.exec(leftWithout) });

    // The stale device clean-up with each setting
    for (const value of settingValues) {
        const threshold = parseInt(value, 10) || 90;
        const pseed = peersSeed();
        for (const sql of pseed) await db.exec(sql);
        let result = null;
        let failed = null;
        try { result = await db.adapter.cleanupStaleOnlinePeers(threshold); } catch (err) { failed = err.message; }
        out.stale.push({ value, threshold: String(threshold), seed: pseed, changes: result ? result.changes : null, error: failed, rows: await db.exec(peerQuery) });
    }

    // The interval of the status sync: setInterval(fn, seconds * 1000)
    const quiet = process.emitWarning;
    process.emitWarning = () => {};
    for (const value of settingValues) {
        const seconds = parseInt(value, 10) || 15;
        const timer = setInterval(() => {}, seconds * 1000);
        out.settings.push({ value, seconds: String(seconds), delayMs: timer._idleTimeout, threshold: String(parseInt(value, 10) || 90) });
        clearInterval(timer);
    }
    process.emitWarning = quiet;

    await db.close();
    const file = path.join(__dirname, '..', `housekeeping_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify(out, null, 1)}\n`);
    console.log(`${file}: ${out.stale.length} stale runs, ${out.settings.length} settings`);
})().catch((err) => { console.error(err); process.exit(1); });
