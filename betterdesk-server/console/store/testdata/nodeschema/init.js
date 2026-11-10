'use strict';
// Runs the Node console's own database start code, for the tests behind the
// build tag "nodeschema". See README.md next to this file.
//
//   node init.js init                 create or upgrade the schema: adapter.init()
//   node init.js snapshot <out.json>  init(), then write what the console reads
//
// The database comes from the environment, as in the console itself:
//   SQLite:      NODESCHEMA_SQLITE_DIR=<dir>   (the file is <dir>/db_v2.sqlite3, no auth.db)
//   PostgreSQL:  DB_TYPE=postgres DATABASE_URL=postgres://...
const path = require('path');
const { createRequire } = require('module');

const webRoot = process.env.BETTERDESK_NODE_ROOT || path.resolve(__dirname, '../../../../../web-nodejs');
const webRequire = createRequire(path.join(webRoot, 'package.json'));
const postgres = (process.env.DB_TYPE || '').toLowerCase() === 'postgres';

// Reads without arguments that most pages start with.
const READS = [
    'getAllUsers', 'getAllUsersForBackup', 'getAllFolders', 'getAllFolderAssignments', 'getAllSettings',
    'getBrandingConfig', 'listBrandingProfiles', 'getAuditLogs', 'getAllTickets', 'getAllInventories',
    'getAlertRules', 'getAlertHistory', 'getRemoteCommands', 'getNetworkTargets', 'getDlpPolicies',
    'getDlpEvents', 'getSavedReports', 'listStudioFlows', 'getTenants', 'getPendingRegistrations',
    'getAllPeerSysinfo', 'getAuditConnections', 'getAuditFiles', 'getAuditAlarms', 'getAllUserGroups',
    'getAllDeviceGroups', 'getAllStrategies', 'listAgentBundles',
];

function sortRows(rows) {
    return rows.map((r) => JSON.stringify(r)).sort().map((s) => JSON.parse(s));
}

async function rawTables(adapter) {
    const out = {};
    if (postgres) {
        const { Pool } = webRequire('pg');
        const pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 2 });
        try {
            const tables = await pool.query(
                "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY 1");
            for (const { table_name: table } of tables.rows) {
                out[table] = sortRows((await pool.query(`SELECT * FROM "${table}"`)).rows);
            }
        } finally {
            await pool.end();
        }
        return out;
    }
    const db = adapter.getSqliteMainDb();
    const tables = db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\' ORDER BY 1").all();
    for (const { name } of tables) {
        out[name] = sortRows(db.prepare(`SELECT * FROM "${name}"`).all());
    }
    return out;
}

async function main() {
    const [command, outFile] = process.argv.slice(2);
    if (command !== 'init' && command !== 'snapshot') {
        throw new Error('usage: node init.js init | snapshot <out.json>');
    }
    const { getAdapter } = require(path.join(webRoot, 'services', 'dbAdapter.js'));
    let config;
    if (!postgres) {
        const dir = process.env.NODESCHEMA_SQLITE_DIR;
        if (!dir) throw new Error('NODESCHEMA_SQLITE_DIR is not set');
        config = { dbPath: path.join(dir, 'db_v2.sqlite3'), dataDir: dir, authDbPath: path.join(dir, 'auth.db') };
    }
    const adapter = getAdapter(config);
    await adapter.init();
    if (command === 'snapshot') {
        const reads = {};
        for (const name of READS) {
            try {
                reads[name] = await adapter[name]();
            } catch (err) {
                reads[name] = { error: String(err && err.message) };
            }
        }
        const tables = await rawTables(adapter);
        require('fs').writeFileSync(outFile, JSON.stringify({ reads, tables }, null, 1));
    }
    await adapter.close();
}

main().catch((err) => {
    console.error('nodeschema:', err);
    process.exit(1);
});
