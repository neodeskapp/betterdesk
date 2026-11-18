'use strict';

/**
 * Makes and restores full backups with the Node console's backup service,
 * for the two-way test of console/backup (roundtrip_test.go). The Go test
 * prepares the databases and folders and calls this script:
 *
 *   node roundtrip.js make    <spec.json>   writes spec.out, prints {filename, appVersion}
 *   node roundtrip.js restore <spec.json>   restores spec.in, prints the result
 *   node roundtrip.js snapshot-make <spec.json>     prints the settings snapshot (createBackup)
 *   node roundtrip.js snapshot-restore <spec.json>  restores spec.data (restoreBackup)
 *   node roundtrip.js import <spec.json>            importAllTables(spec.dump)
 *
 * spec: {dbType, dbPath, databaseUrl, dataDir, keysPath, envFile, out, in, opts}
 *
 * The API server is replaced by the answers in goserver.json, and the old
 * console's .env (web-nodejs/.env) by spec.envFile.
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const [mode, specFile] = process.argv.slice(2);
const spec = JSON.parse(fs.readFileSync(specFile, 'utf8'));

process.env.NODE_ENV = 'test';
process.env.DATA_DIR = spec.dataDir;
process.env.KEYS_PATH = spec.keysPath;
process.env.DB_PATH = spec.dbPath || path.join(spec.keysPath, 'db_v2.sqlite3');
process.env.PUB_KEY_PATH = path.join(spec.keysPath, 'id_ed25519.pub');
process.env.API_KEY_PATH = path.join(spec.keysPath, '.api_key');
process.env.AUTH_DB_PATH = path.join(spec.dataDir, 'auth.db');
process.env.SESSION_SECRET = 'roundtrip-secret';
if (spec.dbType === 'postgres') {
    process.env.DB_TYPE = 'postgres';
    process.env.DATABASE_URL = spec.databaseUrl;
} else {
    process.env.DB_TYPE = 'sqlite';
    delete process.env.DATABASE_URL;
}
process.chdir(WEB);

// web-nodejs/.env stands for the console's own settings file; point it
// at the test's file.
const envPath = path.join(WEB, '.env');
const redirect = (p) => (path.resolve(String(p)) === envPath ? spec.envFile : p);
for (const name of ['existsSync', 'statSync', 'readFileSync', 'writeFileSync']) {
    const original = fs[name];
    fs[name] = function (p, ...rest) { return original.call(fs, redirect(p), ...rest); };
}

const goserver = JSON.parse(fs.readFileSync(path.join(__dirname, 'goserver.json'), 'utf8'));
const api = require(path.join(WEB, 'services/betterdeskApi'));
api.getAllPeers = async () => JSON.parse(JSON.stringify(goserver.peers));
api.getBlocklist = async () => JSON.parse(JSON.stringify(goserver.blocklist));
api.getAuditEvents = async () => JSON.parse(JSON.stringify(goserver.auditEvents));
api.getHealth = async () => JSON.parse(JSON.stringify(goserver.serverHealth));

const config = require(path.join(WEB, 'config/config'));
const backupService = require(path.join(WEB, 'services/backupService'));
const db = require(path.join(WEB, 'services/database'));
const brandingService = require(path.join(WEB, 'services/brandingService'));

(async () => {
    if (mode === 'make') {
        const { buffer, filename } = await backupService.createFullBackup();
        fs.writeFileSync(spec.out, buffer);
        console.log(JSON.stringify({ filename, appVersion: config.appVersion }));
    } else if (mode === 'restore') {
        try {
            const result = await backupService.restoreFullBackup(fs.readFileSync(spec.in), spec.opts || {});
            console.log(JSON.stringify({ result }));
        } catch (err) {
            console.log(JSON.stringify({ error: err.message }));
        }
    } else if (mode === 'snapshot-make') {
        await brandingService.loadBranding();
        const backup = await backupService.createBackup();
        console.log(JSON.stringify({ text: JSON.stringify(backup, null, 2), appVersion: config.appVersion }));
    } else if (mode === 'snapshot-restore') {
        await brandingService.loadBranding();
        const result = await backupService.restoreBackup(spec.data, spec.opts || {});
        console.log(JSON.stringify({ result }));
    } else if (mode === 'import') {
        try {
            console.log(JSON.stringify({ result: await db.importAllTables(spec.dump) }));
        } catch (err) {
            console.log(JSON.stringify({ error: err.message }));
        }
    } else {
        throw new Error(`unknown mode ${mode}`);
    }
    await db.close?.();
})().then(() => process.exit(0), (err) => { console.error(err); process.exit(1); });
