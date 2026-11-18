'use strict';

/**
 * Rebuilds ../bootstrap_with_policy.json from the Node console's real
 * modules: the access, navigation and permissions parts of
 * GET /api/console/bootstrap for four roles under one account policy.
 *
 *   WEB_NODEJS=/path/to/web-nodejs GO_AUTH=/path/to/betterdesk-server/auth node bootstrap_policy.js
 *
 * The policy goes through savePolicy and resolveForUser as the routes use
 * them (PUT /api/panel/access/users/:id, then the policy loader of every
 * request). The database and the Go API are stand-ins: rows are kept in
 * memory, and a role's effective permissions are the built-in role map
 * (middleware/auth.js, the same map as the Go server's auth package) over
 * every permission the Go server defines.
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const GO_AUTH = process.env.GO_AUTH || '/repo/betterdesk-server/auth';
const OUT = process.env.OUT || path.join(__dirname, '..', 'bootstrap_with_policy.json');
process.chdir(WEB);
process.env.NODE_ENV = 'test';

const POLICY = [['devices', 'none'], ['tickets', 'read'], ['reports', 'write'], ['chat', 'none']];
const USERS = {
    viewer: { id: 6, username: 'e2e_viewer', role: 'viewer' },
    operator: { id: 5, username: 'e2e_operator', role: 'operator' },
    global_admin: { id: 3, username: 'e2e_global_admin', role: 'global_admin' },
    admin: { id: 1, username: 'e2e_admin', role: 'admin' },
};

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

const rows = {};
const fakeDb = new Proxy({
    getUserById: async (id) => Object.values(USERS).find((u) => u.id === id) || null,
    getPanelAccessPolicies: async (type, id) => (rows[`${type}:${id}`] || {}).sections || [],
    getClientFeaturePolicies: async (type, id) => (rows[`${type}:${id}`] || {}).features || [],
    setPanelAccessPolicies: async (type, id, sections) => {
        rows[`${type}:${id}`] = { ...rows[`${type}:${id}`], sections: [...sections].sort((a, b) => (a.resource_key < b.resource_key ? -1 : 1)) };
    },
    setClientFeaturePolicies: async (type, id, features) => {
        rows[`${type}:${id}`] = { ...rows[`${type}:${id}`], features };
    },
    getUserGroupsForUser: async () => [],
}, {
    get(target, prop) {
        if (prop in target) return target[prop];
        if (prop === 'then') return undefined;
        return async () => null;
    },
});

const goPermissions = [...fs.readFileSync(path.join(GO_AUTH, 'permissions.go'), 'utf8')
    .matchAll(/\bPerm\w+\s*=\s*"([^"]+)"/g)].map((m) => m[1]);
let auth;
stub('services/database.js', fakeDb);
stub('services/betterdeskApi.js', new Proxy({
    getRolePermissions: async (role) => ({
        success: true,
        data: { role, permissions: goPermissions.filter((p) => auth.roleHasPermission(role, p)) },
    }),
}, {
    get(target, prop) {
        if (prop in target) return target[prop];
        if (prop === 'then') return undefined;
        return async () => ({ success: false, error: 'not available in the oracle' });
    },
}));

auth = require(path.join(WEB, 'middleware/auth'));
const policyService = require(path.join(WEB, 'services/panelAccessPolicyService'));
const { getEffectivePermissions } = require(path.join(WEB, 'services/rolePermissionService'));
const { manager } = require(path.join(WEB, 'services/i18nService'));
const consoleRoutes = require(path.join(WEB, 'routes/console.routes'));

(async () => {
    const quiet = console.log;
    console.log = () => {};
    manager.init();
    console.log = quiet;

    const layer = consoleRoutes.stack.find((l) => l.route && l.route.path === '/api/console/bootstrap');
    const handler = layer.route.stack[layer.route.stack.length - 1].handle;
    const body = { sections: POLICY.map(([key, level]) => ({ resource_key: key, access_level: level })), features: [] };

    const roles = {};
    for (const [name, user] of Object.entries(USERS)) {
        await policyService.savePolicy('user', String(user.id), body);
        const req = {
            session: { userId: user.id, user },
            t: (key, vars) => manager.translate('en', key, vars),
            lang: 'en',
            headers: {},
            permissionContextLoaded: true,
            panelAccessPolicy: await policyService.resolveForUser(user),
        };
        if (!auth.isSuperAdminRole(user.role)) req.effectivePermissions = await getEffectivePermissions(user.role);
        let answer = null;
        await handler(req, { locals: {}, setHeader() {}, json(b) { answer = b; } });
        roles[name] = { access: answer.access, nav: answer.nav, permissions: answer.permissions };
    }

    const sortKeys = (value) => {
        if (Array.isArray(value)) return value.map(sortKeys);
        if (value && typeof value === 'object') {
            return Object.fromEntries(Object.keys(value).sort().map((k) => [k, sortKeys(value[k])]));
        }
        return value;
    };
    const fixture = {
        note: 'What the Node console answers to GET /api/console/bootstrap with the account policy devices none, tickets read, '
            + 'reports write, chat none: access, nav and permissions per role. Made by testdata/nodeoracle/bootstrap_policy.js.',
        policy: POLICY,
        roles: sortKeys(roles),
    };
    fs.writeFileSync(OUT, `${JSON.stringify(fixture, null, 1)}\n`);
    console.log(`wrote ${OUT}`);
    process.exit(0);
})().catch((error) => {
    console.error(error);
    process.exit(1);
});
