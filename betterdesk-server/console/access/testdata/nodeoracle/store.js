'use strict';

/**
 * Runs the console's real policy and scope services (web-nodejs) on a
 * scratch database with fixed rows and records every answer, for the Go
 * package console/access to replay. See README.md in this folder.
 *
 *   node store.js                       SQLite in a temp folder, writes ../store_sqlite.json.gz
 *   ORACLE_PG_DSN=postgres://... node store.js
 *                                       PostgreSQL in a scratch schema, writes ../store_postgres.json.gz
 *
 * The Go server is not asked for role permissions: services/betterdeskApi
 * is replaced by a fake that answers from the list in this file.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'access-oracle-'));
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = scratch;
process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
process.env.DEVICE_SCOPE_DEFAULT = 'open';
const schema = `oracle_${process.pid}_${Date.now()}`;
if (PG_DSN) {
    process.env.DB_TYPE = 'postgres';
    const url = new URL(PG_DSN);
    url.searchParams.set('options', `-c search_path=${schema}`);
    process.env.DATABASE_URL = url.toString();
} else {
    process.env.DB_TYPE = 'sqlite';
    delete process.env.DATABASE_URL;
}
process.chdir(WEB);

// What the fake Go API answers for each role: the effective permissions
const ROLE_PERMISSIONS = {
    operator: ['device.view', 'device.connect', 'device.edit', 'device.connection_mode', 'remote_target.view',
        'remote_target.connect', 'remote_target.test', 'guest.create', 'user.view', 'audit.view', 'metrics.view',
        'cdap.view', 'cdap.command', 'mesh.power', 'chat.access', 'org.manage_devices', 'billing.view', 'billing.reports'],
    viewer: ['device.view', 'remote_target.view', 'audit.view', 'metrics.view', 'cdap.view', 'chat.access'],
    guest_issuer: ['device.view', 'guest.create'],
    global_admin: ['user.view', 'user.edit', 'org.edit', 'device.view', 'device.edit', 'audit.view', 'billing.view', 'billing.manage'],
    server_admin: ['server.config', 'server.keys', 'server.attestation', 'user.view', 'device.view', 'audit.view', 'metrics.view'],
    pro: [],
};

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

stub('services/betterdeskApi.js', new Proxy({
    getRolePermissions: async (role) => (ROLE_PERMISSIONS[role]
        ? { success: true, data: { role, permissions: ROLE_PERMISSIONS[role] } }
        : { success: false, error: 'Request failed with status code 400' }),
}, {
    get(target, prop) {
        if (prop in target) return target[prop];
        if (prop === 'then') return undefined;
        return async () => ({ success: false, error: 'not available in the oracle' });
    },
}));

// The rows both sides start from. Ids are assigned by the database in this
// order, starting after the "Default" user group and device group the
// console creates on a fresh database (their guids are set to fixed values).
const SEED = [
    ['UPDATE user_groups SET guid = ? WHERE name = ?', ['default-ug', 'Default']],
    ['UPDATE device_groups SET guid = ? WHERE name = ?', ['default-dg', 'Default']],
    ...[['admin1', 'admin'], ['op1', 'operator'], ['op2', 'operator'], ['view1', 'viewer'], ['guest1', 'guest_issuer'],
        ['ga1', 'global_admin'], ['sa1', 'server_admin'], ['pro1', 'pro'], ['norole', null], ['odd1', 'made_up_role']]
        .map(([username, role]) => ['INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)', [username, 'x', role]]),
    // user groups 2..4
    ...[['ug-ops', 'Operators'], ['ug-lab', 'Lab staff'], ['ug-empty', 'Nobody']]
        .map(([guid, name]) => ['INSERT INTO user_groups (guid, name, note, team_id) VALUES (?, ?, ?, ?)', [guid, name, '', '']]),
    // members: op1 in Operators and Lab staff, op2 in Operators, view1 in Lab staff
    ...[[2, 2], [3, 2], [2, 3], [3, 4]]
        .map(([group, user]) => ['INSERT INTO user_group_members (user_group_id, user_id) VALUES (?, ?)', [group, user]]),
    // folders 1..3
    ...[['Lab', 2], ['Office', 1], ['Archive', 1]]
        .map(([name, order]) => ['INSERT INTO folders (name, sort_order) VALUES (?, ?)', [name, order]]),
    // device groups 2..6
    ...[['dg-open', 'Open group', 'manual', ''], ['dg-ops', 'Ops only', 'manual', ''], ['dg-lab', 'Lab by tag', 'tag', 'lab'],
        ['folder_2', 'Office', 'manual', ''], ['dg-view', 'Viewer and guest', 'manual', '']]
        .map(([guid, name, source, tag]) => ['INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES (?, ?, ?, ?, ?, ?)',
            [guid, name, '', '', source, tag]]),
    ...[[2, 'D1'], [2, 'D2'], [3, 'D2'], [3, 'D3'], [3, 'GONE'], [6, 'D6'], [5, 'D1']]
        .map(([group, peer]) => ['INSERT INTO device_group_members (device_group_id, peer_id) VALUES (?, ?)', [group, peer]]),
    // access lists: Ops only -> user group Operators; Lab by tag -> user group Lab staff;
    // folder Office -> op2; Viewer and guest -> view1 and guest1
    ...[[3, 2], [4, 3]]
        .map(([group, userGroup]) => ['INSERT INTO device_group_user_group_access (device_group_id, user_group_id) VALUES (?, ?)', [group, userGroup]]),
    ...[[5, 3], [6, 4], [6, 5]]
        .map(([group, user]) => ['INSERT INTO device_group_user_access (device_group_id, user_id) VALUES (?, ?)', [group, user]]),
    ['INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?)', [5, 'D7']],
    ['INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, ?)', [5, 'D0']],
    // stored policies: a row with a level nobody would write through the API
    ['INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES (?, ?, ?, ?)', ['group', 'ug-lab', 'reports', 'WRITE']],
    ['INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES (?, ?, ?, ?)', ['group', 'ug-lab', 'group:system', 'read']],
    // group rows above what the members' roles reach
    ...[['group:operations', 'write'], ['users', 'write'], ['settings', 'write'], ['registrations', 'write']]
        .map(([key, level]) => ['INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES (?, ?, ?, ?)',
            ['group', 'ug-ops', key, level]]),
    ['INSERT INTO panel_access_policies (subject_type, subject_id, resource_key, access_level) VALUES (?, ?, ?, ?)', ['user', '4', 'chat', 'bogus']],
];

const DEVICES = [
    { id: 'D0' }, { id: 'D1', folder_id: 1, tags: 'lab' }, { id: 'D2', folder_id: 2, tags: '["Lab","x"]' },
    { id: 'D3', folder_id: 2 }, { id: 'D4', folder_id: 3, tags: 'prod, LAB' }, { id: 'D5', tags: 'prod' },
    { id: 'D6', folder_id: 1 }, { id: 'D7', folder_id: 3 },
];

const USERS = {
    admin1: { id: 1, username: 'admin1', role: 'admin' },
    op1: { id: 2, username: 'op1', role: 'operator' },
    op2: { id: 3, username: 'op2', role: 'operator' },
    view1: { id: 4, username: 'view1', role: 'viewer' },
    guest1: { id: 5, username: 'guest1', role: 'guest_issuer' },
    ga1: { id: 6, username: 'ga1', role: 'global_admin' },
    sa1: { id: 7, username: 'sa1', role: 'server_admin' },
    pro1: { id: 8, username: 'pro1', role: 'pro' },
};

function scopeSteps() {
    const steps = [];
    for (const name of Object.keys(USERS)) {
        steps.push({ op: 'scope', user: USERS[name], devices: DEVICES });
        steps.push({ op: 'count', user: USERS[name], devices: DEVICES });
        steps.push({ op: 'accessContext', user: USERS[name] });
    }
    for (const name of ['op1', 'op2', 'view1', 'guest1']) {
        for (const device of [DEVICES[0], DEVICES[2], DEVICES[3], DEVICES[6], DEVICES[7], { id: 'NOPE' }]) {
            steps.push({ op: 'canAccess', user: USERS[name], device });
            steps.push({ op: 'canAccess', user: USERS[name], device, all: DEVICES });
        }
    }
    steps.push({ op: 'scope', user: USERS.op1, devices: [] });
    steps.push({ op: 'scope', user: { ...USERS.op1, user_groups: [] }, devices: DEVICES });
    steps.push({ op: 'scope', user: { ...USERS.view1, user_groups: ['ug-ops'] }, devices: DEVICES });
    steps.push({ op: 'enrich', devices: DEVICES });
    return steps;
}

/**
 * A save of every section and group key at write (and every feature on) for
 * each account, then a save of every key at read, each followed by what was
 * stored and what the account resolves to.
 */
function saveEverySubject() {
    const keys = [...new Set(require(path.join(WEB, 'lib/panelAccessCatalog')).PANEL_ACCESS_CATALOG.map((e) => e.key)),
        'group:core', 'group:administration', 'group:operations', 'group:commercialization', 'group:security', 'group:system'];
    const features = ['remote_control', 'clipboard', 'client_actions'];
    const steps = [];
    const ids = ['1', '2', '4', '5', '6', '7', '8', '9', '10', '99', '0', '-3', '6abc', '7.9', 'abc', '0x7',
        '5000000000', '99999999999999999999'];
    for (const id of ids) {
        for (const level of ['write', 'read']) {
            steps.push({ op: 'savePolicy', type: 'user', id, body: {
                sections: keys.map((key) => ({ resource_key: key, access_level: level })),
                features: features.map((key) => ({ feature_key: key, enabled: level === 'write' })),
            } });
            steps.push({ op: 'rawPolicies', type: 'user', id });
        }
        if (/^\d+$/.test(id) && id.length < 4) steps.push({ op: 'resolveUser', id: Number(id) });
    }
    // only the group rows, the account's own the role level
    steps.push({ op: 'savePolicy', type: 'user', id: '4', body: { sections: [] } });
    steps.push({ op: 'resolveUser', id: 4 });
    steps.push({ op: 'resolveUser', id: 2 });
    return steps;
}

const STEPS = [
    { op: 'groups' },
    { op: 'group', guid: 'dg-ops' },
    { op: 'group', guid: 'folder_2' },
    { op: 'group', guid: 'missing' },
    ...[1, 2, 3, 4, 5, 9, 10, 99].map((userId) => ({ op: 'userGroups', userId })),

    // policies before any write
    ...[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 99].map((id) => ({ op: 'resolveUser', id })),
    ...['ug-ops', 'ug-lab', 'ug-empty', 'missing'].map((guid) => ({ op: 'resolveGroup', guid })),
    ...[[2, 'operator'], [4, 'viewer'], [1, 'admin'], [10, 'made_up_role'], [2, undefined], [0, 'operator']]
        .map(([userId, role]) => ({ op: 'loadPolicy', userId, role })),
    { op: 'rawPolicies', type: 'group', id: 'ug-lab' },

    // writes
    { op: 'savePolicy', type: 'user', id: '2', body: {
        sections: [{ resource_key: 'devices', access_level: 'read' }, { key: 'group:system', access: 2 },
            { resource_key: 'settings', access_level: 'none' }, { resource_key: 'nope', access_level: 'write' },
            { resource_key: 'devices', access_level: 'write' }, { resource_key: 'toolkit', access_level: ' READ ' }],
        features: [{ feature_key: 'clipboard', enabled: false }, { key: 'audio', enabled: 'true' },
            { feature_key: 'client_actions', enabled: true }, { feature_key: 'nope', enabled: true }],
    } },
    { op: 'rawPolicies', type: 'user', id: '2' },
    { op: 'resolveUser', id: 2 },
    { op: 'savePolicy', type: 'group', id: 'ug-ops', body: {
        sections: [{ resource_key: 'group:operations', access_level: 'read' }, { resource_key: 'chat', access_level: 'none' },
            { resource_key: 'users', access_level: 'write' }, { resource_key: 'devices', access_level: 'none' }],
        features: [{ feature_key: 'clipboard', enabled: true }, { feature_key: 'tunnel', enabled: false }, { feature_key: 'terminal', enabled: 0 }],
    } },
    { op: 'savePolicy', type: 'group', id: 'ug-lab', body: {
        sections: [{ resource_key: 'group:operations', access_level: 'write' }, { resource_key: 'chat', access_level: 'read' },
            { resource_key: 'group:commercialization', access_level: 'read' }],
        features: [{ feature_key: 'tunnel', enabled: true }, { feature_key: 'terminal', enabled: false }, { feature_key: 'reboot', enabled: false }],
    } },
    ...[2, 3, 4, 1].map((id) => ({ op: 'resolveUser', id })),
    ...['ug-ops', 'ug-lab'].map((guid) => ({ op: 'resolveGroup', guid })),
    { op: 'rawPolicies', type: 'group', id: 'ug-ops' },
    { op: 'savePolicy', type: 'user', id: '1', body: { sections: [{ resource_key: 'users', access_level: 'none' }, { resource_key: 'group:core', access_level: 'read' }] } },
    { op: 'resolveUser', id: 1 },
    { op: 'savePolicy', type: 'user', id: ' 4 ', body: { sections: 'nope', features: null } },
    { op: 'rawPolicies', type: 'user', id: '4' },
    { op: 'savePolicy', type: 'team', id: '4', body: {} },
    { op: 'savePolicy', type: 'user', id: 'a b', body: {} },
    { op: 'savePolicy', type: 'user', id: '2', body: null },
    { op: 'rawPolicies', type: 'user', id: '2' },
    { op: 'resolveUser', id: 2 },

    // what a write may store: every key at write, for every account and for
    // ids that name no account or no number; groups fail after validation
    ...saveEverySubject(),
    { op: 'savePolicy', type: 'group', id: 'ug-empty', body: {} },
    { op: 'savePolicy', type: 'group', id: 'missing', body: { sections: [{ resource_key: 'users', access_level: 'read' }] } },
    { op: 'savePolicy', type: 'group', id: 'a b', body: {} },
    { op: 'savePolicy', type: 'group', id: '', body: {} },
    { op: 'rawPolicies', type: 'group', id: 'ug-empty' },
    { op: 'rawPolicies', type: 'group', id: 'ug-lab' },

    // device scope, default open
    ...scopeSteps(),

    // restricted through the stored setting
    { op: 'setSetting', value: 'restricted' },
    ...scopeSteps(),

    // an empty setting falls back to the environment
    { op: 'setSetting', value: '' },
    { op: 'setEnv', value: 'restricted' },
    { op: 'scope', user: USERS.op1, devices: DEVICES },
    { op: 'scope', user: USERS.pro1, devices: DEVICES },
    { op: 'setSetting', value: 'open' },
    { op: 'scope', user: USERS.pro1, devices: DEVICES },
    { op: 'setEnv', value: 'open' },

    // folder access and grants
    ...['op1', 'op2', 'view1', 'nobody', ''].map((username) => ({ op: 'userFolderIds', username })),
    { op: 'syncFolders', username: 'op1', folderIds: [1, '3', 'x', 3, 99] },
    { op: 'groups' },
    ...['op1', 'op2'].map((username) => ({ op: 'userFolderIds', username })),
    { op: 'scope', user: USERS.op1, devices: DEVICES },
    { op: 'scope', user: USERS.op2, devices: DEVICES },
    { op: 'syncFolders', username: 'op2', folderIds: [] },
    { op: 'syncFolders', username: 'op1', folderIds: [2] },
    { op: 'syncFolders', username: 'ghost', folderIds: [1] },
    { op: 'syncFolders', username: '', folderIds: [1] },
    { op: 'syncFolders', username: 'view1', folderIds: 'nope' },
    { op: 'groups' },
    ...['op1', 'op2', 'ghost'].map((username) => ({ op: 'userFolderIds', username })),
    ...[5, 2, 99, 0].map((userId) => ({ op: 'grantIds', userId })),
    { op: 'syncGrants', userId: 2, peerIds: [' D5 ', 'D5', '', 'NOPE', 'D4'] },
    { op: 'syncGrants', userId: 5, peerIds: 'D1, D2,,D1' },
    { op: 'syncGrants', userId: 4, peerIds: [] },
    { op: 'syncGrants', userId: 0, peerIds: ['D1'] },
    { op: 'syncGrants', userId: 99, peerIds: ['D1'] },
    ...[5, 2, 4, 99].map((userId) => ({ op: 'grantIds', userId })),
    { op: 'scope', user: USERS.op1, devices: DEVICES },
    { op: 'scope', user: USERS.guest1, devices: DEVICES },
    { op: 'count', user: USERS.op1, devices: DEVICES },
    { op: 'enrich', devices: DEVICES },
];

function plainGroup(group) {
    if (!group) return null;
    return {
        id: Number(group.id), guid: group.guid, name: group.name, note: group.note, team_id: group.team_id,
        source_type: group.source_type, tag_filter: group.tag_filter,
        member_count: group.member_count === undefined ? null : group.member_count,
        allowed_users: group.allowed_users, allowed_groups: group.allowed_groups, allowed_user_groups: group.allowed_user_groups,
        created_at_type: group.created_at instanceof Date ? 'date' : typeof group.created_at,
    };
}

async function main() {
    let pool = null;
    if (PG_DSN) {
        const { Pool } = require(path.join(WEB, 'node_modules/pg'));
        const admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        await admin.end();
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
    }

    const config = require(path.join(WEB, 'config/config'));
    const db = require(path.join(WEB, 'services/database'));
    const { getAdapter } = require(path.join(WEB, 'services/dbAdapter'));
    const policyService = require(path.join(WEB, 'services/panelAccessPolicyService'));
    const rolePermissionService = require(path.join(WEB, 'services/rolePermissionService'));
    const deviceGroupService = require(path.join(WEB, 'services/deviceGroupService'));
    const userScopeService = require(path.join(WEB, 'services/userScopeService'));

    const quiet = { log: console.log, warn: console.warn, error: console.error };
    console.log = () => {};
    console.warn = () => {};
    await db.init();

    const exec = async (sql, args) => {
        if (pool) {
            let n = 0;
            await pool.query(sql.replace(/\?/g, () => `$${++n}`), args);
        } else {
            getAdapter(config).getSqliteMainDb().prepare(sql).run(...args);
        }
    };
    for (const [sql, args] of SEED) await exec(sql, args);

    const scopeResult = (scope) => (scope === null ? null : [...scope].sort());
    const results = [];
    for (const step of STEPS) {
        const out = { ...step };
        rolePermissionService.clearCache();
        deviceGroupService.invalidateDeviceScopeDefaultCache();
        try {
            switch (step.op) {
            case 'groups':
                out.result = (await db.getAllDeviceGroups()).map(plainGroup);
                break;
            case 'group':
                out.result = plainGroup(await db.getDeviceGroupByGuid(step.guid));
                break;
            case 'userGroups':
                out.result = (await db.getUserGroupsForUser(step.userId)).map((g) => ({ guid: g.guid, name: g.name }));
                break;
            case 'resolveUser': {
                const user = await db.getUserById(step.id);
                out.found = !!user;
                out.result = user ? JSON.stringify(await policyService.resolveForUser(user)) : null;
                break;
            }
            case 'resolveGroup': {
                const group = await db.getUserGroupByGuid(step.guid);
                out.found = !!group;
                out.result = group ? JSON.stringify(await policyService.resolveForGroup(group)) : null;
                break;
            }
            case 'loadPolicy': {
                const req = { session: { userId: step.userId, user: { role: step.role } } };
                await policyService.loadEffectivePolicy(req, {}, () => {});
                out.result = req.panelAccessPolicy ? JSON.stringify(req.panelAccessPolicy) : null;
                out.loadError = !!req.panelAccessPolicyLoadError;
                break;
            }
            case 'rawPolicies':
                out.result = {
                    sections: await db.getPanelAccessPolicies(step.type, step.id),
                    features: await db.getClientFeaturePolicies(step.type, step.id),
                };
                break;
            case 'savePolicy':
                out.result = await policyService.savePolicy(step.type, step.id, step.body);
                break;
            case 'setSetting':
                await db.setSetting('device_scope_default', step.value);
                break;
            case 'setEnv':
                config.deviceScopeDefault = step.value;
                break;
            case 'scope':
                out.result = scopeResult(await deviceGroupService.getDeviceScopeForUser(db, step.user, step.devices));
                break;
            case 'count':
                out.result = await userScopeService.countEffectiveScope(db, step.user, step.devices);
                break;
            case 'accessContext':
                out.result = (await deviceGroupService.getUserAccessContext(db, step.user)).user_groups || null;
                break;
            case 'canAccess':
                out.result = !!(await deviceGroupService.userCanAccessDevice(db, step.user, step.device, step.all));
                break;
            case 'enrich': {
                const groups = await deviceGroupService.enrichGroups(db, await db.getAllDeviceGroups(), step.devices);
                out.result = Object.fromEntries(groups.map((g) => [g.guid, g.member_count]));
                break;
            }
            case 'userFolderIds':
                out.result = await userScopeService.getUserFolderIds(db, step.username);
                break;
            case 'syncFolders':
                out.result = await userScopeService.syncUserFolderAccess(db, step.username, step.folderIds);
                break;
            case 'grantIds':
                out.result = await userScopeService.getUserPeerGrantIds(db, step.userId);
                break;
            case 'syncGrants':
                out.result = await userScopeService.syncUserPeerGrants(db, step.userId, step.peerIds);
                break;
            default:
                throw new Error(`unknown step ${step.op}`);
            }
        } catch (error) {
            out.error = true;
            out.status = error.status || null;
            out.message = error.status ? error.message : null;
        }
        results.push(out);
    }
    Object.assign(console, quiet);

    const file = path.join(OUT, `store_${dialect}.json.gz`);
    const body = JSON.stringify({ dialect, rolePermissions: ROLE_PERMISSIONS, seed: SEED, steps: results });
    fs.writeFileSync(file, zlib.gzipSync(body, { level: 9 }));
    console.log(`${dialect}: ${results.length} steps, ${results.filter((r) => r.error).length} with an error, ${fs.statSync(file).size} bytes`);

    await db.close();
    if (pool) {
        await pool.query(`DROP SCHEMA ${schema} CASCADE`);
        await pool.end();
    }
    fs.rmSync(scratch, { recursive: true, force: true });
    process.exit(0);
}

main().catch((error) => {
    console.error(error);
    process.exit(1);
});
