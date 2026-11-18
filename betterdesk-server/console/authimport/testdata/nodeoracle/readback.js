'use strict';

/**
 * Reads an install through the Node console's database facade and prints
 * what it sees as JSON, keyed by names and guids instead of ids: every
 * account with its profile, two-factor settings, password hash, groups,
 * panel access, client features, address books, read notifications,
 * device grants, picture, device group access, strategy and desktop
 * layout; the groups, folders, strategies, settings and the audit log.
 * The console picks the layout itself: split while auth.db has no marker
 * in the main file, the main file alone once it has.
 *
 *   node readback.js <db_v2.sqlite3> <auth.db>
 */

const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const [mainPath, authPath] = process.argv.slice(2);
process.env.NODE_ENV = 'test';
process.env.DB_TYPE = 'sqlite';
process.env.DATA_DIR = path.dirname(mainPath);
process.env.DB_PATH = mainPath;
process.env.AUTH_DB_PATH = authPath;
delete process.env.SQLITE_AUTH_DB_MODE;
process.chdir(WEB);
console.log = console.warn = () => {};
const db = require(path.join(WEB, 'services', 'database'));

// Drops ids and times, which differ between the layouts by design
function clean(value) {
    if (Array.isArray(value)) return value.map(clean);
    if (value && typeof value === 'object' && !Buffer.isBuffer(value)) {
        const out = {};
        for (const key of Object.keys(value).sort()) {
            if (key === 'id' || key.endsWith('_id') || key.endsWith('_at') || key === 'userId' || key === 'created' || key === 'updated') continue;
            out[key] = clean(value[key]);
        }
        return out;
    }
    if (Buffer.isBuffer(value)) return value.toString('hex');
    return value;
}

// A list or set in order; anything else as it is
const sorted = (x) => (Array.isArray(x) || x instanceof Set ? [...x].sort() : x);

(async () => {
    await db.init();
    const out = { users: {}, userGroups: {}, deviceGroups: {}, folders: {}, strategies: {}, settings: {}, audit: [] };
    const users = (await db.getAllUsers()).map((u) => u.username).sort();
    for (const name of users) {
        const u = await db.getUserByUsername(name);
        const id = u.id;
        const avatar = await db.getUserAvatar(id);
        out.users[name] = clean({
            role: u.role,
            email: u.email, first_name: u.first_name, last_name: u.last_name, phone: u.phone, role_display: u.role_display, avatar_url: u.avatar_url,
            preferred_language: u.preferred_language,
            totp_enabled: !!u.totp_enabled, totp_secret: u.totp_secret || null, recovery: u.totp_recovery_codes || null,
            password_hash: u.password_hash, guid: u.guid || '',
            groups: (await db.getUserGroupsForUser(id)).map((g) => g.guid).sort(),
            panel: await db.getPanelAccessPolicies('user', String(id)),
            features: await db.getClientFeaturePolicies('user', String(id)),
            legacyBook: await db.getAddressBook(id, 'legacy'),
            personalBook: await db.getAddressBook(id, 'personal'),
            reads: sorted(await db.getReadNotificationIds(id)),
            grants: sorted(await db.getUserPeerGrants(id)),
            avatar: avatar ? { mime: avatar.mime_type || avatar.mimeType, version: avatar.version } : null,
            deviceAccess: await db.getDeviceGroupAccessForUser(id),
            strategy: await db.getUserStrategyGuid(id),
            layout: await db.getSetting(`desktop_layout_${id}`),
        });
    }
    for (const g of await db.getAllUserGroups()) out.userGroups[g.guid] = clean(g);
    for (const g of await db.getAllDeviceGroups()) {
        out.deviceGroups[g.guid] = clean({ ...g, members: (await db.getDeviceGroupMembers(g.guid)).map((m) => (typeof m === 'string' ? m : m.peer_id)).sort() });
    }
    const folders = await db.getAllFolders();
    const names = {};
    for (const f of folders) { names[f.id] = f.name; out.folders[f.name] = clean(f); }
    const assignments = await db.getAllFolderAssignments();
    out.assignments = Object.fromEntries(Object.entries(assignments).map(([device, folder]) => [device, names[folder]]).sort());
    for (const s of await db.getAllStrategies()) out.strategies[s.guid] = clean(s);
    const settings = await db.getAllSettings();
    for (const [key, value] of Object.entries(settings).sort()) {
        if (!/^desktop_(layout|wallpaper)_\d+$/.test(key)) out.settings[key] = value;
    }
    out.audit = (await db.getAuditLogs(100, 0)).map((e) => ({ action: e.action, details: e.details, username: e.username || null })).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    process.stdout.write(JSON.stringify(out));
    process.exit(0);
})().catch((err) => { process.stderr.write(`${err.stack}\n`); process.exit(1); });
