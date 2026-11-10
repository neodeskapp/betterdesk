'use strict';

/**
 * Runs the folder, device group and user group methods of the Node
 * console's database adapter (web-nodejs/services/dbAdapter.js) on a
 * scratch database and records every result, for the Go store of
 * console/devicegroups to replay (store_oracle_test.go). See README.md.
 *
 *   node store.js                          ../store_sqlite.json
 *   ORACLE_PG_DSN=postgres://... node store.js   ../store_postgres.json
 *
 * Arguments are JSON; {"$":"undefined"} and {"$":"NaN"} stand for the two
 * values JSON has no spelling for.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'devicegroups-store-oracle-'));
const schema = `oracle_dg_${process.pid}`;

const web = (rel) => require(path.join(WEB, rel));
const rebind = (sql) => { let n = 0; return sql.replace(/\?/g, () => `$${++n}`); };

function decode(v) {
    if (v && typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 1 && '$' in v) {
        return v.$ === 'NaN' ? NaN : undefined;
    }
    return v;
}

async function open() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
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
        try { await adapter.close(); } catch (_) { /* closed */ }
        if (pool) await pool.end();
        if (admin) { await admin.query(`DROP SCHEMA ${schema} CASCADE`); await admin.end(); }
    };
    return { adapter, exec, close };
}

// The rows every run starts from
const seed = [
    ['DELETE FROM device_groups', []],
    ['DELETE FROM user_groups', []],
    ['DELETE FROM users', []],
    ['INSERT INTO users (id, username, password_hash, role) VALUES (1, ?, ?, ?)', ['bob', 'x', 'operator']],
    ['INSERT INTO users (id, username, password_hash, role) VALUES (2, ?, ?, ?)', ['alice', 'x', 'viewer']],
    ['INSERT INTO users (id, username, password_hash, role) VALUES (3, ?, ?, ?)', ['carol', 'x', 'admin']],
    ["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-b', 'Beta', '', 'org-1')", []],
    ["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-a', 'Alpha', NULL, '')", []],
];

const U = { $: 'undefined' };
const NAN = { $: 'NaN' };
const steps = [];
const step = (op, ...args) => steps.push({ op, args });

// Folders, with every kind of value where a request can put one
step('createFolder', 'Office', '#22c55e', U);
step('createFolder', 'Annex', '', 'box');
step('createFolder', 'Odd', 5, 1.5);
step('createFolder', 'Flag', true, U);
step('createFolder', 'Nulls', null, null);
step('createFolder', null, U, U);
step('createFolder', 7, U, U);
step('getAllFolders');
for (const id of [1, 2, 3, '1', ' 2 ', 'abc', NAN, 1.5, true, false, null, U, 0, -1, 1e20, 2147483648, 9007199254740993]) {
    step('getFolderById', id);
}
step('updateFolder', 1, 'Office 2', U, 'star');
step('updateFolder', 2, U, null, U);
step('updateFolder', 3, U, U, U);
step('updateFolder', 'abc', 'x', U, U);
step('updateFolder', NAN, 'x', U, U);
step('updateFolder', 1, U, true, U);
step('updateFolder', 1, 77, U, U);
step('getAllFolders');

// Devices into folders
step('assignDevicesToFolder', ['A', 'B', 7, 1.5], 1);
step('assignDevicesToFolder', ['C', true], 2);
step('assignDevicesToFolder', ['D', null], 2);
step('getAllFolderAssignments');
step('assignDeviceToFolder', 'A', 2);
step('assignDeviceToFolder', 'B', null);
step('assignDeviceToFolder', 'E', U);
step('assignDeviceToFolder', 'F', '3');
step('assignDeviceToFolder', 'G', 'abc');
step('assignDeviceToFolder', 'H', 2.5);
step('getAllFolderAssignments');
step('assignDevicesToFolder', ['A', '7.0', 7], null);
step('getAllFolderAssignments');
step('unassignDevicesFromFolder', 'abc');
step('unassignDevicesFromFolder', 2);
step('getAllFolderAssignments');
step('deleteFolder', 1);
step('deleteFolder', 'abc');
step('getAllFolders');
step('getAllFolderAssignments');

// Device groups
step('createDeviceGroup', { guid: 'g-tag', name: 'Laptops', source_type: 'tag', tag_filter: 'laptop', team_id: 'org-1', note: 'n' });
step('createDeviceGroup', { guid: 'folder_3', name: 'Annex', source_type: 'dynamic', tag_filter: 'dropped' });
step('createDeviceGroup', { name: 'Random guid', note: U, team_id: null, source_type: 'tag', tag_filter: '' });
step('createDeviceGroup', { guid: 'g-num', name: 5, note: 1.5, team_id: 7 });
step('createDeviceGroup', { guid: 'g-flag', name: 'Flag', team_id: true });
step('createDeviceGroup', { guid: 'g-tag', name: 'Again' });
step('createDeviceGroup', { guid: 'g-noname', name: null });
step('setDeviceGroupUserAccess', 'folder_3', ['carol', ' alice', 'nobody', 'alice', '']);
step('setDeviceGroupUserAccess', 'no-such-group', ['bob']);
step('setDeviceGroupUserGroupAccess', 'folder_3', ['ug-b', ' ug-a ', 'missing', 'ug-a', '']);
step('setDeviceGroupUserGroupAccess', 'no-such-group', ['ug-a']);
step('getDeviceGroupByGuid', 'folder_3');
step('getDeviceGroupByGuid', 'nothing');
step('getAllDeviceGroups');
step('updateDeviceGroup', 'g-tag', { name: 'Laptops 2', tag_filter: 'x'.repeat(60) });
step('updateDeviceGroup', 'g-tag', { source_type: 'manual', tag_filter: 0, note: null });
step('updateDeviceGroup', 'g-tag', { source_type: 'tag', tag_filter: 12, team_id: '' });
step('updateDeviceGroup', 'g-tag', {});
step('updateDeviceGroup', 'nothing', { name: 'x' });
step('updateDeviceGroup', 'g-tag', { name: true });
step('updateDeviceGroup', 'g-tag', { name: null });
step('getAllDeviceGroups');
step('deleteDeviceGroup', 'folder_3');
step('deleteDeviceGroup', 'nothing');
step('getAllDeviceGroups');
step('getAllUserGroups');
for (const id of [1, 3, 4, 0, NAN, '2', 2.5]) step('getUserById', id);

// What a step records: what the Go store returns for it
async function run(db, s) {
    const a = s.args.map((v) => (Array.isArray(v) ? v.map(decode) : (v && typeof v === 'object' && !('$' in v))
        ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, decode(x)])) : decode(v)));
    const ad = db.adapter;
    switch (s.op) {
        case 'createFolder': return (await ad.createFolder({ name: a[0], color: a[1], icon: a[2] })).id;
        case 'getAllFolders': return ad.getAllFolders();
        case 'getFolderById': return ad.getFolderById(a[0]);
        case 'updateFolder': await ad.updateFolder(a[0], { name: a[1], color: a[2], icon: a[3] }); return null;
        case 'deleteFolder': await ad.deleteFolder(a[0]); return null;
        case 'assignDevicesToFolder': await ad.assignDevicesToFolder(a[0], a[1]); return null;
        case 'assignDeviceToFolder': await ad.assignDeviceToFolder(a[0], a[1]); return null;
        case 'unassignDevicesFromFolder': await ad.unassignDevicesFromFolder(a[0]); return null;
        case 'getAllFolderAssignments': return ad.getAllFolderAssignments();
        case 'createDeviceGroup': return ad.createDeviceGroup(a[0]);
        case 'updateDeviceGroup': return ad.updateDeviceGroup(a[0], a[1]);
        case 'deleteDeviceGroup': await ad.deleteDeviceGroup(a[0]); return null;
        case 'setDeviceGroupUserAccess': return ad.setDeviceGroupUserAccess(a[0], a[1]);
        case 'setDeviceGroupUserGroupAccess': return ad.setDeviceGroupUserGroupAccess(a[0], a[1]);
        case 'getDeviceGroupByGuid': return ad.getDeviceGroupByGuid(a[0]);
        case 'getAllDeviceGroups': return ad.getAllDeviceGroups();
        case 'getAllUserGroups': return ad.getAllUserGroups();
        case 'getUserById': { const u = await ad.getUserById(a[0]); return u ? u.id : null; }
    }
    throw new Error(`unknown op ${s.op}`);
}

(async () => {
    const db = await open();
    for (const [sql, params] of seed) await db.exec(sql, params);
    const out = [];
    for (const s of steps) {
        try {
            const result = await run(db, s);
            out.push({ ...s, result: JSON.stringify(result === undefined ? null : result) });
        } catch (err) {
            out.push({ ...s, error: err.message });
        }
    }
    await db.close();
    const file = path.join(__dirname, '..', `store_${dialect}.json`);
    fs.writeFileSync(file, `${JSON.stringify({ dialect, seed, steps: out }, null, 1)}\n`);
    console.log(`${file}: ${out.length} steps`);
})().catch((err) => { console.error(err); process.exit(1); });
