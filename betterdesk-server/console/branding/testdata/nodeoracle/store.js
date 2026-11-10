'use strict';

/**
 * Runs the console's real services/brandingService.js on a scratch
 * database and records every step of a long scenario: saving generated
 * settings, resets, theme imports, rows written straight into the table,
 * and the profile functions. After each step it records the stored rows,
 * the cached settings and everything derived from them (the stylesheet,
 * the appearance model, the readability check, the public appearance, the
 * export, the favicon). See README.md in this folder.
 *
 *   node store.js                       SQLite in a temp folder, writes ../store_sqlite.json.gz
 *   ORACLE_PG_DSN=postgres://... node store.js
 *                                       PostgreSQL in a scratch schema, writes ../store_postgres.json.gz
 *   ORACLE_STEPS=200                    a shorter scenario
 */

const fs = require('fs');
const path = require('path');
const lib = require('./lib');

const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const STEPS = Number(process.env.ORACLE_STEPS || 3200);

const root = lib.scratchConsole('branding-store');
process.env.NODE_ENV = 'test';
process.env.TZ = 'UTC';
process.env.DATA_DIR = path.join(root, 'data');
process.env.DB_PATH = path.join(root, 'data', 'db_v2.sqlite3');
process.env.AUTH_DB_PATH = path.join(root, 'data', 'auth.db');
const schema = `oracle_branding_${process.pid}_${Date.now()}`;
if (PG_DSN) {
    process.env.DB_TYPE = 'postgres';
    const url = new URL(PG_DSN);
    url.searchParams.set('options', `-c search_path=${schema}`);
    process.env.DATABASE_URL = url.toString();
} else {
    process.env.DB_TYPE = 'sqlite';
    delete process.env.DATABASE_URL;
}
process.chdir(root);

// Installed fonts, so the stylesheet has @import lines for them. The Go
// test creates the same files.
const FONT_FILES = {
    'inter/font.css': "@font-face { font-family: 'Inter'; }",
    'inter/inter-400.woff2': 'wOF2',
    'inter/inter-700.woff2': 'wOF2',
    'custom-upload/font.css': "@font-face { font-family: 'Custom Upload'; }",
    'custom-upload/custom-upload.ttf': 'ttf',
    'custom-upload/meta.json': '{"family":"Custom Upload","category":"custom","source":"upload"}',
    'my-font/readme.txt': 'no stylesheet here',
};
for (const [rel, text] of Object.entries(FONT_FILES)) {
    const file = path.join(root, 'public', 'fonts', rel);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, text);
}

const MASK = /"revision":"[^"]*"/g;
const mask = (text) => text.replace(MASK, '"revision":"<REV>"');

function generateSteps() {
    const r = lib.rng(dialect === 'postgres' ? 2002 : 2001);
    // PostgreSQL has no text with a zero byte in it and no place for half a
    // surrogate pair; such rows cannot exist there.
    const rowText = (v) => {
        let s = typeof v === 'string' ? v : JSON.stringify(v);
        s = Buffer.from(String(s), 'utf8').toString('utf8');
        return dialect === 'postgres' ? s.replace(/\u0000/g, '') : s;
    };
    // Names every JavaScript object answers to. A row with such a key ends
    // up as a key of the settings, in the order the database returns the
    // rows, which PostgreSQL does not promise. One per set keeps the
    // recording the same on every run.
    const INHERITED = ['constructor', '__proto__', 'toString', 'valueOf', 'hasOwnProperty'];
    const rawRows = () => {
        const rows = new Map();
        const n = r.pick([0, 1, 2, 4, 8, 16]);
        let inherited = false;
        for (let i = 0; i < n; i++) {
            let key = r.chance(0.9) ? r.pick(lib.FLAT_KEYS) : r.pick([...INHERITED, 'unknownKey', 'faviconUrl', 'appearance', '']);
            if (INHERITED.includes(key)) {
                if (inherited) key = 'unknownKey';
                inherited = true;
            }
            let value;
            if (key === 'colors') value = r.chance(0.8) ? rowText(lib.colorsValue(r)) : rowText(lib.anyText(r));
            else if (key === 'loginSlides') value = r.chance(0.8) ? rowText(lib.slidesValue(r)) : rowText(lib.anyText(r));
            else value = rowText(r.chance(0.6) ? lib.settingValue(r, key) : lib.anyText(r));
            rows.set(key, value);
        }
        return [...rows.entries()];
    };
    const preset = () => {
        const k = r.int(10);
        if (k === 0) return r.pick([null, 'x', 5, [], {}, { type: 'betterdesk-theme' }, { type: 'other', branding: {} }, { type: 'betterdesk-theme', branding: 'x' }, { type: 'betterdesk-theme', appearance: 'x' }]);
        const p = { version: r.pick(['1.0', '2.0']), type: r.chance(0.95) ? 'betterdesk-theme' : 'theme' };
        if (k < 6) p.branding = lib.settingsObject(r);
        if (k >= 4) p.appearance = lib.settingsObject(r);
        return p;
    };
    const profileName = () => (r.chance(0.8) ? r.pick(['Default', 'default', 'Night', 'Day', 'Corporate', 'x'.repeat(85), 'Zebra', 'apple', '\u00c4rger', ' padded ', 'N\ud83d\udc4d']) : lib.anyValue(r));
    const id = () => r.pick([1, 1, 2, 2, 3, 4, 5, 6, 8, 12, 99, 0, -1, 3000000000]);

    const steps = [{ op: 'state' }];
    for (let i = 0; i < STEPS; i++) {
        const k = r.int(100);
        if (k < 78) steps.push({ op: 'save', updates: lib.settingsObject(r) });
        else if (k < 81) steps.push({ op: 'reset' });
        else if (k < 86) steps.push({ op: 'import', preset: preset() });
        else if (k < 91) steps.push({ op: 'rows', rows: rawRows() });
        else if (k < 92) steps.push({ op: 'load' });
        else if (k < 94) steps.push({ op: 'profileCreate', name: profileName(), description: r.pick(['', 'A description', 'd'.repeat(205), 7, null]), branding: r.chance(0.5) ? null : preset() });
        else if (k < 95) steps.push({ op: 'profileUpdate', id: id(), updates: { name: r.chance(0.5) ? profileName() : undefined, description: r.pick([undefined, null, '', 'changed', 5, { toString: 1 }]), branding: r.chance(0.5) ? lib.settingsObject(r) : r.pick([undefined, null, 0, '']) } });
        else if (k < 97) steps.push({ op: 'profileApply', id: id() });
        else if (k < 98) steps.push({ op: 'profileDelete', id: id() });
        else if (k < 99) steps.push({ op: 'profileDuplicate', id: id(), newName: r.pick(['', '', 'Copy of it', 'Default', ' ', 5, null, ['x'], { a: 1 }, 'y'.repeat(90)]) });
        else steps.push({ op: 'profileList' });
        if (i % 40 === 39) steps.push({ op: 'profileList' });
    }
    // values that came through JSON, as they do on the Go side
    return steps.map((s) => {
        const out = {};
        for (const [key, value] of Object.entries(s)) {
            out[key] = key === 'updates' && s.op === 'profileUpdate'
                ? Object.fromEntries(Object.entries(value).map(([k2, v]) => [k2, lib.jsonClone(v)]))
                : lib.jsonClone(value);
        }
        return out;
    });
}

async function main() {
    let pool = null;
    let admin = null;
    if (PG_DSN) {
        const { Pool } = require(path.join(lib.WEB, 'node_modules/pg'));
        admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
    }
    const db = require(path.join(root, 'services/database'));
    const branding = require(path.join(root, 'services/brandingService'));
    await db.init();

    let sqlite = null;
    if (!PG_DSN) {
        const Database = require(path.join(lib.WEB, 'node_modules/better-sqlite3'));
        const file = fs.existsSync(process.env.AUTH_DB_PATH) ? process.env.AUTH_DB_PATH : process.env.DB_PATH;
        sqlite = new Database(file);
        if (!sqlite.prepare("SELECT name FROM sqlite_master WHERE name = 'branding_config'").get()) {
            sqlite.close();
            sqlite = new Database(process.env.DB_PATH);
        }
    }
    const sql = async (text, args = []) => {
        if (pool) {
            let n = 0;
            return (await pool.query(text.replace(/\?/g, () => `$${++n}`), args)).rows;
        }
        const stmt = sqlite.prepare(text);
        return stmt.reader ? stmt.all(...args) : (stmt.run(...args), []);
    };
    const now = pool ? 'NOW()' : "datetime('now')";
    const defaultFavicon = fs.readFileSync(path.join(root, 'public', 'favicon.svg'), 'utf8');

    async function state() {
        const rows = await sql('SELECT key, value FROM branding_config ORDER BY key');
        const newest = (await sql('SELECT MAX(updated_at) AS rev FROM branding_config'))[0].rev;
        const current = branding.getBranding();
        const text = (fn) => {
            try {
                return mask(JSON.stringify(fn()));
            } catch (err) {
                return `throws ${err && err.name}: ${err && err.message}`;
            }
        };
        const derived = [
            text(() => branding.getAppearanceModel(current)),
            text(() => branding.assessAppearanceReadability(current)),
            text(() => branding.getPublicAppearance()),
            text(() => branding.exportPreset()),
            // The default favicon is a file whose line ends depend on the
            // checkout; only its being the default is recorded.
            text(() => (branding.generateFavicon() === defaultFavicon ? '<default favicon>' : branding.generateFavicon())),
        ];
        return {
            rows: lib.textOrHash(JSON.stringify(rows.map((row) => [row.key, row.value])), 300),
            branding: lib.textOrHash(JSON.stringify(current), 300),
            css: lib.record(() => branding.generateThemeCss(), 300),
            // appearance model, readability, public appearance, export, favicon
            derived: lib.FULL ? derived : lib.sha256(derived.join('\n')),
            revision: branding.getBrandingRevision(),
            // the newest change as the database has it: SQLite's text, PostgreSQL's time in ISO form
            newest: newest == null ? null : (newest instanceof Date ? newest.toISOString() : String(newest)),
            now: Date.now(),
        };
    }

    async function profiles() {
        const list = await sql('SELECT id, name, description, data, is_active, created_at, updated_at FROM branding_profiles ORDER BY id');
        return list.map((p) => ({
            id: p.id, name: p.name, description: p.description, is_active: p.is_active,
            data: lib.textOrHash(mask(String(p.data)), 200),
            created_at: p.created_at instanceof Date ? p.created_at.toISOString() : p.created_at,
            updated_at: p.updated_at instanceof Date ? p.updated_at.toISOString() : p.updated_at,
        }));
    }

    const answer = async (fn) => {
        try {
            const value = await fn();
            const text = JSON.stringify(value);
            return text === undefined ? { u: true } : { r: mask(text) };
        } catch (err) {
            const out = { e: String(err && err.message), n: String(err && err.name) };
            // an error of the database driver: its wording is the driver's own
            if (err && err.code !== undefined) out.db = String(err.code);
            return out;
        }
    };

    const steps = generateSteps();
    const out = [];
    for (const step of steps) {
        let result = { u: true };
        switch (step.op) {
            case 'state':
                break;
            case 'save':
                result = await answer(() => branding.saveBranding(step.updates));
                break;
            case 'reset':
                result = await answer(() => branding.resetBranding());
                break;
            case 'import':
                result = await answer(() => branding.importPreset(step.preset));
                break;
            case 'rows':
                await sql('DELETE FROM branding_config');
                for (const [key, value] of step.rows) {
                    await sql(`INSERT INTO branding_config (key, value, updated_at) VALUES (?, ?, ${now})`, [key, value]);
                }
                branding.invalidateCache();
                result = await answer(async () => { await branding.loadBranding(); });
                break;
            case 'load':
                branding.invalidateCache();
                result = await answer(async () => { await branding.loadBranding(); });
                break;
            case 'profileCreate':
                // as the route calls it: name and description turned into trimmed text first
                result = await answer(() => branding.createProfile(String(step.name || '').trim(), String(step.description || '').trim(), step.branding));
                break;
            case 'profileUpdate':
                result = await answer(() => branding.updateProfile(step.id, step.updates));
                break;
            case 'profileApply':
                result = await answer(() => branding.applyProfile(step.id));
                break;
            case 'profileDelete':
                result = await answer(() => branding.deleteProfile(step.id));
                break;
            case 'profileDuplicate':
                result = await answer(() => branding.duplicateProfile(step.id, step.newName));
                break;
            case 'profileList':
                result = await answer(() => branding.listProfiles());
                break;
            default:
                throw new Error(`unknown step ${step.op}`);
        }
        const entry = { step, result, state: await state() };
        if (step.op.startsWith('profile')) entry.profiles = await profiles();
        out.push(entry);
    }

    const counts = {};
    for (const entry of out) counts[entry.step.op] = (counts[entry.step.op] || 0) + 1;
    const info = lib.writeVectors(`store_${dialect}`, { dialect, fonts: FONT_FILES, steps: out });
    console.log(JSON.stringify({ ...info, steps: out.length, counts }, null, 1));

    if (sqlite) sqlite.close();
    if (pool) {
        await pool.end();
        const adapter = require(path.join(root, 'services/dbAdapter'));
        try { await adapter.getAdapter().close?.(); } catch (_) { /* the adapter may have no close */ }
        await admin.query(`DROP SCHEMA ${schema} CASCADE`);
        await admin.end();
    }
    fs.rmSync(root, { recursive: true, force: true });
    process.exit(0);
}

main().catch((err) => { console.error(err); process.exit(1); });
