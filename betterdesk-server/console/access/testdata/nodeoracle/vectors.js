'use strict';

/**
 * Dumps test vectors from the console's real Node modules (web-nodejs) for
 * the Go package console/access. See README.md in this folder.
 *
 *   node vectors.js            writes ../*.json.gz and ../counts.json
 *
 * Nothing here talks to a database or to the Go server: services/database
 * and services/betterdeskApi are replaced by in-memory fakes before the
 * modules under test load.
 */

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const zlib = require('zlib');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const GO_AUTH = process.env.GO_AUTH || '/repo/betterdesk-server/auth';
const OUT = path.join(__dirname, '..');
process.chdir(WEB);
process.env.NODE_ENV = 'test';

// ---------------------------------------------------------------- fakes

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

/** What the fake database and the fake Go API answer for the current case */
const world = {
    policies: {},        // "<type>:<id>" -> { sections: [...], features: [...] }
    groups: [],          // user groups of the current user
    rolePermissions: {}, // role -> array of permissions, or { error }
};

const fakeDb = new Proxy({
    getPanelAccessPolicies: async (type, id) => (world.policies[`${type}:${id}`] || {}).sections || [],
    getClientFeaturePolicies: async (type, id) => (world.policies[`${type}:${id}`] || {}).features || [],
    getUserGroupsForUser: async () => world.groups,
}, {
    get(target, prop) {
        if (prop in target) return target[prop];
        if (prop === 'then') return undefined;
        return async () => null;
    },
});

// The fake Go API. apiTarget is kept so a test can swap a method.
const apiTarget = {
    getRolePermissions: async (role) => {
        const answer = world.rolePermissions[role];
        if (!answer || answer.error) return { success: false, error: (answer && answer.error) || 'Request failed with status code 400' };
        return { success: true, data: { role, permissions: answer } };
    },
};
const fakeApi = new Proxy(apiTarget, {
    get(target, prop) {
        if (prop in target) return target[prop];
        if (prop === 'then') return undefined;
        return async () => ({ success: false, error: 'not available in the oracle' });
    },
});

stub('services/database.js', fakeDb);
stub('services/betterdeskApi.js', fakeApi);

const auth = require(path.join(WEB, 'middleware/auth'));
const panelAccess = require(path.join(WEB, 'middleware/panelAccess'));
const catalog = require(path.join(WEB, 'lib/panelAccessCatalog'));
const policyService = require(path.join(WEB, 'services/panelAccessPolicyService'));
const rolePermissionService = require(path.join(WEB, 'services/rolePermissionService'));
const consoleNav = require(path.join(WEB, 'lib/consoleNav'));
const consoleRoutes = require(path.join(WEB, 'routes/console.routes'));
const deviceGroupService = require(path.join(WEB, 'services/deviceGroupService'));
const userScopeService = require(path.join(WEB, 'services/userScopeService'));

/**
 * A second copy of a module with some of its private functions exported.
 * The copy shares the stubs above, because its requires go through the same
 * module cache.
 */
function internals(rel, names) {
    const Module = require('module');
    const file = require.resolve(path.join(WEB, rel));
    const copy = new Module(file, module);
    copy.filename = file;
    copy.paths = Module._nodeModulePaths(path.dirname(file));
    copy._compile(`${fs.readFileSync(file, 'utf8')}\nmodule.exports.__internals = { ${names.join(', ')} };\n`, file);
    return copy.exports.__internals;
}

const policyInternals = internals('services/panelAccessPolicyService.js', ['clampToCeiling']);

// ---------------------------------------------------------------- helpers

function write(name, data) {
    const file = path.join(OUT, `${name}.gz`);
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(data), { level: 9 }));
    return fs.statSync(file).size;
}

function sha(text) {
    return crypto.createHash('sha256').update(text).digest('hex');
}

/** Small deterministic generator, so the vectors are the same on every run */
function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return {
        next,
        int: (n) => Math.floor(next() * n),
        pick: (list) => list[Math.floor(next() * list.length)],
        chance: (p) => next() < p,
    };
}

/** A table of distinct outcomes; vectors refer to them by index */
function outcomeTable() {
    const list = [];
    const index = new Map();
    return {
        list,
        add(outcome) {
            const key = JSON.stringify(outcome);
            if (!index.has(key)) {
                index.set(key, list.length);
                list.push(outcome);
            }
            return index.get(key);
        },
    };
}

/** A response object that records what the middleware did with it */
function fakeRes(withErrorPage) {
    const out = { status: null };
    const res = {
        status(code) { out.status = code; return res; },
        json(body) { out.json = body; return res; },
        type(t) { out.type = t; return res; },
        send(body) { out.text = body; return res; },
        redirect(location) { out.status = 302; out.redirect = location; return res; },
    };
    if (withErrorPage) {
        res.sendErrorPage = (kind, opts) => {
            out.errorPage = { kind, opts: opts || {} };
            // lib/webUi.js: the status of the page is opts.status || kind
            out.status = (opts && opts.status) || kind;
            return true;
        };
    }
    return { res, out };
}

function runMiddleware(mw, req, withErrorPage) {
    const { res, out } = fakeRes(withErrorPage);
    let next = false;
    mw(req, res, () => { next = true; });
    const result = { next };
    for (const key of ['status', 'json', 'type', 'text', 'redirect', 'errorPage']) {
        if (out[key] !== null && out[key] !== undefined) result[key] = out[key];
    }
    return result;
}

const counts = {};

// ---------------------------------------------------------------- 1. role map

const ROLES = ['super_admin', 'admin', 'server_admin', 'global_admin', 'operator', 'viewer', 'guest_issuer', 'pro',
    'device', 'unknown', 'ADMIN', 'Operator', ''];

function permissionsInCode() {
    const found = new Set();
    const goSource = fs.readFileSync(path.join(GO_AUTH, 'permissions.go'), 'utf8');
    for (const m of goSource.matchAll(/\bPerm\w+\s*=\s*"([^"]+)"/g)) found.add(m[1]);
    const files = [
        'middleware/auth.js', 'lib/consoleNav.js', 'lib/panelAccessCatalog.js',
        ...fs.readdirSync(path.join(WEB, 'routes')).filter((f) => f.endsWith('.js')).map((f) => `routes/${f}`),
        ...fs.readdirSync(path.join(WEB, 'services')).filter((f) => f.endsWith('.js')).map((f) => `services/${f}`),
    ];
    const call = /(?:requirePermission|requireRdClientAuth|requireAnyPermission|requestHasPermission|roleHasPermission|roleHasEffectivePermission|perm|anyPerm|can)\(([^)]*)\)/g;
    for (const file of files) {
        const source = fs.readFileSync(path.join(WEB, file), 'utf8');
        for (const m of source.matchAll(call)) {
            for (const s of m[1].matchAll(/['"]([a-z_]+\.[a-z_]+)['"]/g)) found.add(s[1]);
        }
    }
    for (const entry of catalog.PANEL_ACCESS_CATALOG) {
        if (entry.readPermission) found.add(entry.readPermission);
        if (entry.writePermission) found.add(entry.writePermission);
    }
    for (const p of consoleRoutes.UI_PERMISSIONS) found.add(p);
    // Names no role map knows, and the edges of the pro rule
    for (const p of ['nothing.at_all', 'device.', 'device.unknown', 'remote_target.unknown', 'devicex.view',
        'org.manage_devicesx', 'DEVICE.VIEW', 'device', '.', 'remote_target.', 'org.manage_device']) found.add(p);
    return [...found].sort();
}

const PERMISSIONS = permissionsInCode();

{
    const rows = {};
    for (const role of ROLES) {
        rows[role] = PERMISSIONS.map((p) => (auth.roleHasPermission(role, p) ? '1' : '0')).join('');
    }
    const superAdmin = {};
    for (const role of ROLES) superAdmin[role] = auth.isSuperAdminRole(role);
    write('role_has_permission.json', { roles: ROLES, permissions: PERMISSIONS, rows, isSuperAdmin: superAdmin });
    counts.roleHasPermission = ROLES.length * PERMISSIONS.length;
}

// ---------------------------------------------------------------- 2. findRule

function guardPaths() {
    const paths = new Set(['', '/', '//', '/?', '/index', '/api', '/api/', '/api/panel', '/api/panel/',
        '/api/panel/accessories', '/api/panel/access/users/1', '/api/panel/billing/x', '/api/panel/fleet/x',
        '/api/panel/org/1', '/api/panel/roles', '/api/panel/role-permissions', '/api/panel/tokens',
        '/api/panel/scaling/x', '/api/panel/security-audit/x', '/api/panel/languages/x',
        '/api/panel/resource-control/x', '/api/panel/cross-platform/x', '/api/panel/strategies/1',
        '/api/panel/user-groups/abc/members', '/api/remote-targets', '/api/remote-targets/1',
        '/api/remote/viewer/abc', '/remote/login', '/remote/guest', '/api/cdap/devices', '/api/chat/history',
        '/api/bd/commands', '/api/auth/login', '/api/console/bootstrap', '/api/inventory', '/api/tenants',
        '/api/keys', '/api/keys/api', '/api/settings/audit', '/api/settings/server-info',
        '/api/reports/generate', '/api/reports/trends', '/api/users/42', '/api/users/42/avatar',
        '/api/devices/ABC/rename', '/login', '/logout', '/portal', '/pairing', '/server-attestation',
        '/generator', '/unknown', '/favicon.ico', '/console-boot.js', '/app/users', '/health']);
    for (const rule of panelAccess.ROUTE_RULES) {
        const p = rule.prefix || rule.path;
        if (p === '/') continue;
        for (const variant of [p, `${p}/`, `${p}/x`, `${p}/x/y/z`, `${p}X`, `${p}-extra`, `${p}.json`, `${p}//`,
            p.toUpperCase(), p.slice(0, -1), `/x${p}`, `${p}%2Fx`, `${p}?tab=1`, p.replace(/^\//, ''),
            `${p}/..`, ` ${p}`, `${p} `]) {
            paths.add(variant);
        }
        if (!p.startsWith('/api/')) paths.add(`/api${p}`);
    }
    return [...paths];
}

const GUARD_PATHS = guardPaths();

{
    const cases = GUARD_PATHS.map((p) => {
        const rule = panelAccess.findRule(p);
        return { path: p, rule: rule ? { ...rule } : null };
    });
    const rules = panelAccess.ROUTE_RULES.map((rule) => ({ ...rule }));

    // No rule lies below another one, so the longest-match rule and the
    // order among equally long rules are recorded with a few rules added to
    // the table for the moment.
    const extra = [
        { path: '/api/users', section: 'exact, listed later' },
        { prefix: '/api/users/me', section: 'self' },
        { prefix: '/api/users/me/keys', section: 'keys' },
        { path: '/api/users/me/keys/1', section: 'one key' },
        { prefix: '/api', section: 'api' },
        { prefix: '/users', section: 'users again' },
    ];
    panelAccess.ROUTE_RULES.push(...extra);
    const nested = ['/api/users', '/api/users/7', '/api/users/me', '/api/users/me/', '/api/users/mex', '/api/users/me/keys',
        '/api/users/me/keys/2', '/api/users/me/keys/1', '/api/users/me/keys/1/x', '/api/other', '/api', '/apix', '/users',
        '/users/1', '/api/devices/1', '/api/cdap/x'].map((p) => ({ path: p, section: (panelAccess.findRule(p) || {}).section || null }));
    panelAccess.ROUTE_RULES.length -= extra.length;

    write('find_rule.json', { rules, cases, extra, nested });
    counts.findRule = cases.length + nested.length;
}

// ---------------------------------------------------------------- 3. guard

const METHODS = ['GET', 'HEAD', 'OPTIONS', 'POST', 'PUT', 'PATCH', 'DELETE'];
const SESSIONS = [
    { name: 'none', session: undefined },
    { name: 'empty', session: {} },
    { name: 'user id only', session: { userId: 7 } },
    { name: 'user only', session: { user: { role: 'viewer' } } },
    { name: 'viewer', session: { userId: 7, user: { role: 'viewer' } } },
    { name: 'admin', session: { userId: 1, user: { role: 'admin' } } },
];
// The policy a request carries; "section" is the section of the path's rule
const POLICY_STATES = ['no policy', 'load error', 'none', 'read', 'write', 'section missing', ' WRITE ', 'bogus',
    'load error with write'];

function policyFor(state, section) {
    const key = section || 'devices';
    switch (state) {
    case 'no policy': return {};
    case 'load error': return { panelAccessPolicyLoadError: new Error('boom') };
    case 'section missing': return { panelAccessPolicy: { sections: {} } };
    case 'load error with write':
        return { panelAccessPolicyLoadError: new Error('boom'), panelAccessPolicy: { sections: { [key]: { access: 'write' } } } };
    default: return { panelAccessPolicy: { sections: { [key]: { access: state } } } };
    }
}

{
    const table = outcomeTable();
    const cases = [];
    let n = 0;
    for (const p of GUARD_PATHS) {
        const rule = panelAccess.findRule(p);
        const results = [];
        for (const method of METHODS) {
            for (const s of SESSIONS) {
                for (const state of POLICY_STATES) {
                    const outcome = {};
                    for (const withPage of [true, false]) {
                        const req = { path: p, method, session: s.session, ...policyFor(state, rule && rule.section) };
                        const result = runMiddleware(panelAccess.panelAccessGuard, req, withPage);
                        result.section = req.panelAccessSection === undefined ? null : req.panelAccessSection;
                        outcome[withPage ? 'page' : 'plain'] = result;
                    }
                    results.push(table.add(outcome));
                    n++;
                }
            }
        }
        cases.push({ path: p, r: results });
    }
    const levels = METHODS.map((method) => panelAccess.getRequiredLevel({ method }));
    write('guard.json', {
        methods: METHODS, requiredLevels: levels, sessions: SESSIONS.map((s) => s.session || null),
        policyStates: POLICY_STATES, outcomes: table.list, cases,
    });
    counts.guard = n;
}

// ---------------------------------------------------------------- 4. requestHasPermission

{
    const roles = ['super_admin', 'admin', 'server_admin', 'global_admin', 'operator', 'viewer', 'guest_issuer', 'pro', 'unknown', null];
    const sections = [null, 'devices', 'dashboard', 'guests', 'fleet', 'toolkit', 'reports', 'network', 'security-audit',
        'server-attestation', 'users', 'registrations', 'settings', 'nope'];
    const policyStates = ['no policy', 'section missing', 'none', 'read', 'write', 'READ', ' write ', 'bogus', 'no access value'];
    const permissions = ['device.view', 'device.edit', 'device.connect', 'guest.create', 'org.manage_devices',
        'server.config', 'audit.view', 'metrics.view', 'user.view', 'user.edit', 'branding.edit',
        'server.attestation', 'remote_target.view', 'enrollment.approve', 'enrollment.manage', 'nothing.at_all'];
    const effectiveStates = ['not loaded', 'empty', 'has it', 'has others'];
    const loadStates = [
        { loaded: false, error: false }, { loaded: true, error: false },
        { loaded: true, error: true }, { loaded: false, error: true },
    ];
    let bits = '';
    for (const role of roles) {
        for (const section of sections) {
            for (const state of policyStates) {
                for (const permission of permissions) {
                    for (const effective of effectiveStates) {
                        for (const load of loadStates) {
                            const req = {};
                            if (role !== null) req.session = { userId: 5, user: { role } };
                            if (section !== null) req.panelAccessSection = section;
                            const key = section || 'devices';
                            if (state === 'section missing') req.panelAccessPolicy = { sections: {} };
                            else if (state === 'no access value') req.panelAccessPolicy = { sections: { [key]: {} } };
                            else if (state !== 'no policy') req.panelAccessPolicy = { sections: { [key]: { access: state } } };
                            if (effective === 'empty') req.effectivePermissions = new Set();
                            else if (effective === 'has it') req.effectivePermissions = new Set([permission, 'x.y']);
                            else if (effective === 'has others') {
                                req.effectivePermissions = new Set(permissions.filter((p) => p !== permission));
                            }
                            if (load.loaded) req.permissionContextLoaded = true;
                            if (load.error) req.permissionLoadError = new Error('boom');
                            bits += auth.requestHasPermission(req, permission) ? '1' : '0';
                        }
                    }
                }
            }
        }
    }
    write('request_has_permission.json', { roles, sections, policyStates, permissions, effectiveStates, loadStates, bits });
    counts.requestHasPermission = bits.length;
}

// ---------------------------------------------------------------- 5. levels, normalisers, validation

{
    const levelInputs = ['none', 'read', 'write', 'WRITE', ' Read ', '\twrite\n', 'bogus', '', ' ', 'writer', 'rea d',
        0, 1, 2, 3, -1, 1.5, 0.5, 2.5, 100, null, true, false, '1', '2', '0', ['write'], ['read', 'write'], [],
        {}, { access: 'write' }, 'Write', 'nOnE', ' read ', '﻿write', 'read\u0085', 'wrİte',
        'READ ', 1e21, -0];
    const levels = levelInputs.map((input) => ({ input, level: catalog.normalizeAccessLevel(input) }));

    const compare = [];
    for (const left of ['none', 'read', 'write', 'bogus', 2, 'WRITE']) {
        for (const right of ['none', 'read', 'write', 'bogus', 1, ' read ']) {
            compare.push({ left, right, result: Math.sign(catalog.compareAccessLevels(left, right)) });
        }
    }

    const keys = [...new Set(catalog.PANEL_ACCESS_CATALOG.map((e) => e.key)), 'nope', ''];
    const perms = [...new Set(catalog.PANEL_ACCESS_CATALOG.flatMap((e) => [e.readPermission, e.writePermission]).filter(Boolean))];
    const sets = [[], perms, ...perms.map((p) => [p])];
    const r = rng(11);
    for (let i = 0; i < 40; i++) sets.push(perms.filter(() => r.chance(0.4)));
    const ceilings = [];
    for (const set of sets) {
        const has = (p) => set.includes(p);
        ceilings.push({
            permissions: set,
            levels: keys.map((k) => catalog.getLegacySectionCeiling(k, has)),
            superAdmin: keys.map((k) => catalog.getLegacySectionCeiling(k, has, true)),
        });
    }

    const sectionInputs = [
        null, 'devices', 7, {}, [],
        [{ resource_key: 'devices', access_level: 'write' }],
        [{ key: 'devices', access: 'read' }],
        [{ resource_key: 'devices', access_level: 'write' }, { resource_key: 'devices', access_level: 'none' }],
        [{ resource_key: ' users ', access_level: ' READ ' }],
        [{ resource_key: 'group:core', access_level: 2 }, { resource_key: 'group:nope', access_level: 2 }],
        [{ resource_key: 'nope', access_level: 'write' }, { key: 'toolkit', access: 1 }],
        [{ resource_key: '', key: 'chat', access_level: null, access: 'write' }],
        [{ resource_key: 'chat', access_level: '', access: 'write' }],
        [{ resource_key: 'chat', access_level: 0, access: 'write' }],
        [{ resource_key: 'chat', access_level: false, access: 'write' }],
        [{ resource_key: 'chat' }],
        [{ resource_key: ['fleet'], access_level: ['write'] }],
        [{ resource_key: 5, access_level: 'write' }, { resource_key: { a: 1 }, access_level: 'write' }],
        [null, 'devices', 3, ['devices'], { resource_key: 'reports', access_level: 'bogus' }],
        [{ resource_key: 'Devices', access_level: 'write' }, { resource_key: 'devices ', access_level: 'write' }],
        [{ resource_key: 0, key: 'folders', access_level: 3 }, { resource_key: false, key: 'files', access_level: -1 }],
        [{ key: 'group:system', access: 'write' }, { key: 'settings', access: 'none' }, { key: 'group:system', access: 'read' }],
        catalog.PANEL_ACCESS_CATALOG.map((e, i) => ({ resource_key: e.key, access_level: ['none', 'read', 'write'][i % 3] })),
        catalog.PANEL_ACCESS_GROUPS.map((g, i) => ({ key: `group:${g.key}`, access: i })),
        [{ resource_key: ' devices﻿', access_level: 'write' }],
        [{ resource_key: 'devices', access_level: true }],
        [{ resource_key: 'devices', access_level: 1.5 }, { resource_key: 'users', access_level: '2' }],
    ];
    const sections = sectionInputs.map((input) => ({ input, rows: policyService.normalizeSectionPolicies(input) }));

    const featureInputs = [
        null, 'clipboard', {}, [],
        [{ feature_key: 'clipboard', enabled: true }],
        [{ key: 'audio', enabled: 1 }, { key: 'tunnel', enabled: 'true' }, { key: 'terminal', enabled: 'TRUE' }],
        [{ feature_key: 'clipboard', enabled: 'yes' }, { feature_key: 'reboot', enabled: 2 }, { feature_key: 'privacy_mode', enabled: null }],
        [{ feature_key: 'client_actions', enabled: true }],
        [{ feature_key: 'nope', enabled: true }, { feature_key: ' file_transfer ', enabled: true }],
        [{ feature_key: 'clipboard', enabled: true }, { feature_key: 'clipboard', enabled: false }],
        [{ feature_key: '', key: 'remote_control' }, { feature_key: ['port_forwarding'], enabled: [true] }],
        [null, 5, 'audio', ['audio'], { feature_key: 'audio', enabled: 1.0 }],
        [{ feature_key: 'Clipboard', enabled: true }, { feature_key: 'audio', enabled: {} }, { feature_key: 'tunnel', enabled: '1' }],
        catalog.CLIENT_FEATURES.map((f, i) => ({ feature_key: f.key, enabled: i % 2 === 0 })),
    ];
    const features = featureInputs.map((input) => ({ input, rows: policyService.normalizeFeaturePolicies(input) }));

    const subjectInputs = [
        ['user', '7'], ['group', 'ops'], ['user', ' 7 '], ['user', ''], ['user', '   '], ['team', '7'], ['', '7'],
        ['USER', '7'], ['group', 'a b'], ['group', 'a/b'], ['group', 'a.b:c@d-e_f'], ['group', 'x'.repeat(120)],
        ['group', 'x'.repeat(121)], ['group', 'äbc'], ['user', '0'], ['group', '3f2c9d1e-8a7b-4c6d-9e0f-1a2b3c4d5e6f'],
        ['group', 'a\nb'], ['group', '\ta\t'], ['user', '7;drop'], ['group', '%41'], ['group', 'a+b'],
    ];
    const subjects = subjectInputs.map(([type, id]) => {
        try {
            return { type, id, value: policyService.validateSubject(type, id) };
        } catch (error) {
            return { type, id, error: error.message, status: error.status };
        }
    });

    // clampToCeiling is not exported; "undefined" stands for a missing value
    const clampInputs = ['none', 'read', 'write', 'WRITE', ' read ', 'bogus', '', 'undefined', null,
        0, 1, 2, 3, -1, 1.5, true, false, '2', {}, ['write']];
    const clamp = [];
    for (const level of clampInputs) {
        for (const ceiling of clampInputs) {
            const arg = (v) => (v === 'undefined' ? undefined : v);
            clamp.push({ level, ceiling, result: policyInternals.clampToCeiling(arg(level), arg(ceiling)) });
        }
    }

    write('normalize.json', {
        levels, compare, ceilingKeys: keys, ceilings, sections, features, subjects, clamp, catalog: policyService.getCatalog(),
    });
    counts.normalize = levels.length + compare.length + ceilings.length * keys.length * 2 + sections.length + features.length
        + subjects.length + clamp.length;
}

// ---------------------------------------------------------------- 6. accessAllows

{
    const accessValues = ['none', 'read', 'write', 'WRITE', ' Read ', 'bogus', '', '0', null];
    const required = ['none', 'read', 'write', 'READ', 'bogus', null];
    const shapes = ['no policy', 'no sections', 'other section', 'section'];
    let bits = '';
    for (const shape of shapes) {
        for (const access of accessValues) {
            for (const level of required) {
                let policy;
                if (shape === 'no sections') policy = {};
                else if (shape === 'other section') policy = { sections: { users: { access } } };
                else if (shape === 'section') policy = { sections: { devices: access === null ? {} : { access } } };
                const args = level === null ? [policy, 'devices'] : [policy, 'devices', level];
                bits += policyService.accessAllows(...args) ? '1' : '0';
            }
        }
    }
    write('access_allows.json', { shapes, accessValues, required, bits });
    counts.accessAllows = bits.length;
}

// ---------------------------------------------------------------- 7. policy merge

async function policyMergeVectors() {
    const r = rng(20261110);
    const sectionKeys = [...new Set(catalog.PANEL_ACCESS_CATALOG.map((e) => e.key)),
        ...catalog.PANEL_ACCESS_GROUPS.map((g) => `group:${g.key}`)];
    const storedLevels = ['none', 'read', 'write', 'write', 'read', 'WRITE', 'bogus', ''];
    const featureKeys = catalog.CLIENT_FEATURES.map((f) => f.key);
    const allPermissions = PERMISSIONS.filter((p) => /^[a-z_]+\.[a-z_]+$/.test(p));
    const roles = ['operator', 'viewer', 'guest_issuer', 'global_admin', 'server_admin', 'pro', 'admin', 'super_admin', null, 'broken'];

    const randomSections = (density) => sectionKeys.filter(() => r.chance(density))
        .map((key) => ({ resource_key: key, access_level: r.pick(storedLevels) }))
        .sort((a, b) => (a.resource_key < b.resource_key ? -1 : 1));
    const randomFeatures = (density) => featureKeys.filter(() => r.chance(density))
        .map((key) => ({ feature_key: key, enabled: r.chance(0.5) }))
        .sort((a, b) => (a.feature_key < b.feature_key ? -1 : 1));

    const cases = [];
    const FULL = 30;
    for (let i = 0; i < 700; i++) {
        const role = r.pick(roles);
        const lookupRole = role || 'viewer';
        const rolePermissions = role === 'broken'
            ? { error: 'Request failed with status code 400' }
            : allPermissions.filter(() => r.chance(r.pick([0.1, 0.4, 0.8])));
        const groups = [];
        const groupCount = r.pick([0, 0, 1, 1, 2, 3]);
        for (let g = 0; g < groupCount; g++) groups.push({ id: g + 1, guid: `g${g + 1}`, name: `Group ${g + 1}` });
        const userId = 1 + r.int(900);
        const policies = {};
        policies[`user:${userId}`] = {
            sections: r.chance(0.25) ? [] : randomSections(r.pick([0.05, 0.2, 0.6])),
            features: r.chance(0.4) ? [] : randomFeatures(0.4),
        };
        for (const group of groups) {
            policies[`group:${group.guid}`] = {
                sections: randomSections(r.pick([0.05, 0.2, 0.5])),
                features: r.chance(0.4) ? [] : randomFeatures(0.4),
            };
        }
        world.policies = policies;
        world.groups = groups;
        world.rolePermissions = { [lookupRole]: rolePermissions };
        rolePermissionService.clearCache();

        const input = { role, rolePermissions: Array.isArray(rolePermissions) ? rolePermissions : null, userId, groups, policies };
        const expected = {};
        try {
            const policy = await policyService.resolveForUser({ id: userId, role: role === null ? undefined : role });
            const text = JSON.stringify(policy);
            expected.user = i < FULL ? { json: text } : { sha256: sha(text) };
        } catch (error) {
            expected.user = { error: true, status: error.status || null };
        }
        expected.groups = [];
        for (const group of groups) {
            const text = JSON.stringify(await policyService.resolveForGroup(group));
            expected.groups.push(i < FULL ? { json: text } : { sha256: sha(text) });
        }
        cases.push({ ...input, expected });
    }

    // Edges of the "who is this" checks
    const edges = [];
    for (const user of [null, {}, { id: 0, role: 'viewer' }, { id: '', role: 'viewer' }]) {
        try {
            await policyService.resolveForUser(user);
            edges.push({ fn: 'resolveForUser', user, ok: true });
        } catch (error) {
            edges.push({ fn: 'resolveForUser', user, error: error.message, status: error.status || null });
        }
    }
    for (const group of [null, {}, { guid: '' }]) {
        try {
            await policyService.resolveForGroup(group);
            edges.push({ fn: 'resolveForGroup', group, ok: true });
        } catch (error) {
            edges.push({ fn: 'resolveForGroup', group, error: error.message, status: error.status || null });
        }
    }
    write('policy_merge.json', { cases, edges });
    counts.policyMerge = cases.reduce((n, c) => n + 1 + c.groups.length, 0) + edges.length;
}

// ---------------------------------------------------------------- 8. the require* guards

function guardVectors() {
    const table = outcomeTable();
    const guards = [
        { name: 'requireAuth', make: () => auth.requireAuth },
        { name: 'requireAdmin', make: () => auth.requireAdmin },
        { name: 'requireRole', arg: 'operator', make: () => auth.requireRole('operator') },
        { name: 'requireRole', arg: 'super_admin', make: () => auth.requireRole('super_admin') },
        { name: 'requireRole', arg: 'viewer', make: () => auth.requireRole('viewer') },
        { name: 'requirePermission', arg: 'device.view', make: () => auth.requirePermission('device.view') },
        { name: 'requirePermission', arg: 'device.edit', make: () => auth.requirePermission('device.edit') },
        { name: 'requirePermission', arg: 'server.config', make: () => auth.requirePermission('server.config') },
        { name: 'requirePermission', arg: 'guest.create', make: () => auth.requirePermission('guest.create') },
        { name: 'requireRdClientAuth', arg: '', make: () => auth.requireRdClientAuth() },
        { name: 'requireRdClientAuth', arg: 'device.connect', make: () => auth.requireRdClientAuth('device.connect') },
        { name: 'requireRdClientAuth', arg: 'device.view', make: () => auth.requireRdClientAuth('device.view') },
    ];
    const sessions = [
        null, {}, { userId: 0, user: { role: 'admin' } }, { userId: 9 },
        ...['super_admin', 'admin', 'server_admin', 'global_admin', 'operator', 'viewer', 'guest_issuer', 'pro', 'unknown']
            .map((role) => ({ userId: 9, user: { role } })),
    ];
    const urls = [
        { originalUrl: '/api/thing/open' }, { originalUrl: '/api/files/1?x=1' }, { originalUrl: '/users' },
        { originalUrl: '/remote/abc?transport=cdap' }, { originalUrl: '/remote' }, { originalUrl: '/apix' },
        { originalUrl: '/API/users' }, { originalUrl: '/api' }, { originalUrl: '/remote/login' },
        { originalUrl: '/remote/../login', query: { return: '/remote/dev1' } },
        { originalUrl: '/pairing', query: { return: '/remote/dev 1?a=b#c' } },
        { originalUrl: '/pairing', query: { return: 'https://evil.example/remote' } },
        { originalUrl: '/pairing', query: { return: ['/remote/a', '/remote/b'] } },
        { url: '/api/only-url' }, {},
    ];
    const loadStates = [
        {}, { permissionContextLoaded: true }, { permissionContextLoaded: true, permissionLoadError: true },
        { permissionLoadError: true },
        { permissionContextLoaded: true, effective: [] },
        { permissionContextLoaded: true, effective: ['device.view', 'guest.create'] },
        { permissionContextLoaded: true, effective: ['server.config', 'device.edit', 'device.connect'] },
    ];
    const contexts = [
        {}, { section: 'devices', level: 'read' }, { section: 'devices', level: 'write' }, { section: 'devices', level: 'none' },
        { section: 'guests', level: 'read' }, { section: 'guests', level: 'write' }, { section: 'settings', level: 'write' },
    ];
    const results = [];
    for (const guard of guards) {
        const mw = guard.make();
        for (const session of sessions) {
            for (const url of urls) {
                for (const load of loadStates) {
                    for (const context of contexts) {
                        const outcome = {};
                        for (const withPage of [true, false]) {
                            let destroyed = false;
                            const req = { method: 'GET', headers: {}, query: url.query || {} };
                            if (url.originalUrl !== undefined) req.originalUrl = url.originalUrl;
                            if (url.url !== undefined) req.url = url.url;
                            if (session) req.session = { ...session, destroy() { destroyed = true; } };
                            if (load.permissionContextLoaded) req.permissionContextLoaded = true;
                            if (load.permissionLoadError) req.permissionLoadError = new Error('boom');
                            if (load.effective) req.effectivePermissions = new Set(load.effective);
                            if (context.section) {
                                req.panelAccessSection = context.section;
                                req.panelAccessPolicy = { sections: { [context.section]: { access: context.level } } };
                            }
                            const result = runMiddleware(mw, req, withPage);
                            if (destroyed) result.destroyed = true;
                            outcome[withPage ? 'page' : 'plain'] = result;
                        }
                        results.push(table.add(outcome));
                    }
                }
            }
        }
    }
    write('require_guards.json', {
        guards: guards.map((g) => ({ name: g.name, arg: g.arg === undefined ? null : g.arg })),
        sessions, urls, loadStates, contexts, outcomes: table.list, results,
    });
    counts.requireGuards = results.length;
}

// ---------------------------------------------------------------- 9. remote client return addresses

{
    const inputs = ['/remote', '/remote/', '/remote/abc123', '/remote?transport=cdap', '/remote/dev1?transport=cdap',
        '/remote#x', '/remote?', '/remote#', '/remote?#', '/remote/a/b/../c', '/remote/./a', '/remote/..',
        '/remote/.', '/remote/../remote/x', '/remote/a/..', '/remote/a/../..', '/remote/%2e%2e/login',
        '/remote/%2E%2e/x', '/remote/.%2e/remote/y', '/remote/%2e/x', '/remote/%2e', '/remote/x/%2E',
        '/login', '/remoteevil', '/remote/login', '/remote/login/', '/remote/login/x', '/remote/loginx',
        '/remote/login?return=%2Fremote', '/remote/x/../login', '/REMOTE', '/Remote/x', '//evil.com/remote',
        '/remote/%0aevil', '/remote/%0Aevil', '/remote/%0devil', '/remote/%00', '/remote/%5c', '/remote/%5Cx',
        '/remote\\x', '/remote\nx', '/remote\rx', '/remote\u0000x', 'https://evil.example/remote', 'remote',
        '', ' /remote', '/remote ', '/remote\t', '/re\tmote', '/\t/evil.example/remote',
        '/\t/betterdesk.local/remote/x', '/\t/BETTERDESK.local:443/remote/x', '/\t/betterdesk.local:8443/remote',
        '/\t/betterdesk.local:/remote', '/\t/betterdesk.local:0443/remote', '/\t/u:p@betterdesk.local/remote',
        '/\t/%62etterdesk.local/remote', '/\t/betterdesk.local./remote', '/\t//betterdesk.local/remote',
        '/\t/betterdesk.local', '/\t/betterdesk.local?x', '/\t/betterdesk.local:abc/remote', '/\t/', '/\t/?x',
        '/\t/betterdesk.local:99999/remote', '/\t/evil@betterdesk.local:443/remote/q?a=1#b',
        '/remote/a b', '/remote/a%20b', '/remote/a"b', '/remote/a<b>c', '/remote/a`b', '/remote/a{b}c',
        '/remote/a^b', '/remote/a|b', '/remote/a[b]c', '/remote/a%zzb', '/remote/a%', '/remote/a%2',
        '/remote/é', '/remote/中文', '/remote/😀', '/remote/\u007f', '/remote/\u0001a',
        '/remote/\u001fa', '/remote?a=b c', '/remote?a="b"', '/remote?a=\'b\'', '/remote?a=<b>', '/remote?a=b#c d',
        '/remote?a=é', '/remote#é', '/remote#a`b', '/remote#a"b<c>d', '/remote?a=b?c', '/remote#a#b',
        '/remote?a=#', '/remote/x?a=1&b=2#frag', '/remote/a;b=c', '/remote/a:b@c', '/remote/a=b&c', '/remote/a+b',
        '/remote/~x', '/remote/$x', "/remote/'x'", '/remote/(x)', '/remote/*', '/remote/!', '/remote/a,b',
        '/remote//x', '/remote///', '/remote/x//..', '/./remote', '/../remote', '/x/../remote', '/%2e%2e/remote',
        '/remote/..%2f', '/remote/%2f..', '/remote\u0001', '\u0001/remote', '/remote/ ', '/remote ',
        '/remote/x y', '/remote/x﻿', '/', '/?', '/#', '/remote?return=/remote/login',
        '/remote/x?' + 'a'.repeat(300), '/remote/' + 'ä'.repeat(40)];
    const cases = inputs.map((input) => ({ input, output: auth.normalizeRdClientReturnUrl(input), safe: auth.isSafeRdClientReturnUrl(input) }));
    for (const input of [null, 5, ['/remote'], { a: 1 }]) {
        if (auth.normalizeRdClientReturnUrl(input) !== null) throw new Error('a value that is not text was accepted');
    }
    write('rdclient.json', { cases });
    counts.rdClient = cases.length;
}

// ---------------------------------------------------------------- 10. navigation and bootstrap

async function bootstrapVectors() {
    const layer = consoleRoutes.stack.find((l) => l.route && l.route.path === '/api/console/bootstrap');
    const handler = layer.route.stack[layer.route.stack.length - 1].handle;
    const r = rng(77);
    const sectionKeys = [...new Set(catalog.PANEL_ACCESS_CATALOG.map((e) => e.key))];
    const roles = ['super_admin', 'admin', 'server_admin', 'global_admin', 'operator', 'viewer', 'guest_issuer', 'unknown'];
    const cases = [];
    const FULL = 12;

    const call = async (req) => {
        let body = null;
        const res = { locals: {}, setHeader() {}, json(b) { body = b; } };
        await handler(req, res);
        return body;
    };

    let i = 0;
    for (const role of roles) {
        for (const variant of ['static map', 'effective', 'policy from role', 'random policy', 'all none', 'load error']) {
            const req = { session: { userId: 3, user: { id: 3, username: 'u', role } }, t: (k) => k, lang: 'en', headers: {} };
            const input = { role, variant };
            if (variant !== 'static map') req.permissionContextLoaded = true;
            if (variant === 'effective' || variant === 'policy from role' || variant === 'random policy') {
                const effective = PERMISSIONS.filter((p) => auth.roleHasPermission(role, p) ? r.chance(0.9) : r.chance(0.1));
                if (!auth.isSuperAdminRole(role)) {
                    req.effectivePermissions = new Set(effective);
                    input.effective = effective;
                }
            }
            if (variant === 'load error') req.permissionLoadError = new Error('boom');
            if (variant === 'policy from role' || variant === 'random policy' || variant === 'all none') {
                const sections = {};
                for (const key of sectionKeys) {
                    let access;
                    if (variant === 'all none') access = 'none';
                    else if (variant === 'random policy') access = r.pick(['none', 'read', 'write', 'WRITE', 'bogus']);
                    else {
                        access = catalog.getLegacySectionCeiling(key, (p) => auth.isSuperAdminRole(role)
                            || (req.effectivePermissions ? req.effectivePermissions.has(p) : auth.roleHasPermission(role, p)),
                        auth.isSuperAdminRole(role));
                    }
                    sections[key] = { access, source: 'role', role_ceiling: 'none' };
                }
                req.panelAccessPolicy = { subject_type: 'user', subject_id: '3', sections, features: {}, groups: [] };
                input.policy = Object.fromEntries(Object.entries(sections).map(([k, v]) => [k, v.access]));
            }
            const body = await call(req);
            const expected = {
                permissions: body.permissions,
                isSuperAdmin: body.isSuperAdmin,
                access: i < FULL ? { json: JSON.stringify(body.access) } : { sha256: sha(JSON.stringify(body.access)) },
                nav: i < FULL ? { json: JSON.stringify(body.nav) } : { sha256: sha(JSON.stringify(body.nav)) },
            };
            cases.push({ ...input, expected });
            i++;
        }
    }

    // buildNav on its own: the active page, and a caller without a role
    const nav = [];
    const pages = ['', 'dashboard', 'devices', 'settings', 'server-management', 'commercialization', 'generator', 'agents', 'nope'];
    const tabs = ['', 'branding', 'sessions', 'reports', 'terminal'];
    for (const role of ['admin', 'operator', 'viewer', '']) {
        for (const currentPage of pages) {
            for (const currentTab of tabs) {
                for (const activateAgentNav of [false, true]) {
                    const built = consoleNav.buildNav({
                        can: (p) => auth.roleHasPermission(role, p), isSuperAdmin: auth.isSuperAdminRole(role),
                        role: role || undefined, t: (k) => `T(${k})`, currentPage, currentTab, activateAgentNav,
                    });
                    nav.push({
                        role, currentPage, currentTab, activateAgentNav,
                        key: consoleNav.pageKey(currentPage, currentTab, activateAgentNav),
                        activeSection: built.activeSection ? built.activeSection.id : null,
                        activePage: built.activePage ? built.activePage.page : null,
                        pages: built.sections.map((s) => `${s.id}:${s.pages.map((p) => p.page + (p.active ? '*' : '')).join(',')}`),
                        texts: sha(JSON.stringify(built.sections.map((s) => [s.text, s.pages.map((p) => [p.text, p.groupText])]))),
                    });
                }
            }
        }
    }
    const noRole = consoleNav.buildNav({ can: () => true, isSuperAdmin: false, t: (k) => k, currentPage: '', currentTab: '' });
    const noCan = consoleNav.buildNav({ isSuperAdmin: true, role: 'admin', t: (k) => k, currentPage: '', currentTab: '' });
    write('bootstrap.json', {
        cases, nav,
        noRole: noRole.sections.flatMap((s) => s.pages.map((p) => p.page)),
        noCan: noCan.sections.flatMap((s) => s.pages.map((p) => p.page)),
        uiPermissions: consoleRoutes.UI_PERMISSIONS,
    });
    counts.bootstrap = cases.length + nav.length + 2;
}

// ---------------------------------------------------------------- 11. scope helpers without a database

async function scopeVectors() {
    const tagInputs = [null, '', 'a,b', ' a , b ,, ', '["x"," y ",""]', '[1,2,null]', '{"a":1}', '123', 'null', '"q"',
        ['a', ' b ', ''], [1, null, 'c'], 'x', '[', 'a,[b]'];
    const tags = tagInputs.map((input) => ({ input, output: deviceGroupService.normalizeTags(input) }));
    const nameInputs = [null, '', 'a,b', ' a , b ,, a', ['a', ' a ', 'b', ''], [1, null, 0, 'x', true], 5,
        Array.from({ length: 130 }, (_, i) => `u${i}`).join(','), { a: 1 }, [['a', 'b'], { c: 1 }]];
    const usernames = nameInputs.map((input) => ({ input, output: deviceGroupService.normalizeUsernames(input) }));
    const guidInputs = [null, '', 'g1,g2', ['g1', ' g1 ', 'g2'], [{ guid: 'g3', name: 'x' }, { guid: ' g4 ' }, { name: 'no guid' }, null, 7],
        Array.from({ length: 120 }, (_, i) => ({ guid: `g${i}` })), [{ guid: 0 }, { guid: 5 }]];
    const guids = guidInputs.map((input) => ({ input, output: deviceGroupService.normalizeGroupGuids(input) }));
    const folderGuidInputs = ['folder_1', 'folder_0', 'FOLDER_12', ' folder_3 ', 'folder_', 'folder_1a', 'xfolder_1',
        'folder_-1', 'folder_007', 'folder_99999999999999999999', '', null, 'folder 1', 'folder_1\n'];
    const folderGuids = folderGuidInputs.map((input) => ({ input, output: deviceGroupService.folderIdFromGroupGuid(input) }));
    const folderIdInputs = [null, '1,2', [], [1, 2, 2, '3', ' 4', '5x', 'x6', 7.9, -1, '-2', '+3', null, true, [8], ['9', 1], {}, '0', -0, 1e21, '99999999999999999999'],
        Array.from({ length: 250 }, (_, i) => i)];
    const folderIds = folderIdInputs.map((input) => ({ input, output: userScopeService.normalizeFolderIds(input) }));
    const peerIdInputs = [null, '', 'a, b ,,a', [' a ', 'b', 'a', ''], [1, 2.5, null, 0, true, 'x', ['p', 'q'], { z: 1 }], 12345,
        Array.from({ length: 520 }, (_, i) => `p${i}`), 1e21, [1e21, 1e-7, 123456789012, -5]];
    const peerIds = peerIdInputs.map((input) => ({ input, output: userScopeService.normalizePeerIds(input) }));

    // groupAllowedForUser
    const groupShapes = [
        { allowed_users: [], allowed_groups: [] }, { allowed_users: ['op1'], allowed_groups: [] },
        { allowed_users: ['other'], allowed_groups: [] }, { allowed_users: [], allowed_groups: ['g1'] },
        { allowed_users: ['other'], allowed_groups: ['g2', 'g1'] }, { allowed_users: [' op1 '], allowed_groups: [] },
        { allowed_users: ['OP1'], allowed_groups: ['G1'] }, { allowed_users: [''], allowed_groups: [' '] },
    ];
    const users = [null, { id: 5, username: 'op1', role: 'operator', user_groups: [] },
        { id: 5, username: 'op1', role: 'operator', user_groups: ['g1'] },
        { id: 5, username: 'op2', role: 'viewer', user_groups: ['g2', 'g3'] },
        { id: 5, username: 'op2', role: 'viewer' },
        ...['super_admin', 'admin', 'global_admin', 'server_admin', 'guest_issuer', 'pro'].map((role) => ({ id: 5, username: 'zz', role, user_groups: [] }))];
    const allowed = [];
    for (const group of groupShapes) {
        for (const user of users) allowed.push({ group, user, allowed: deviceGroupService.groupAllowedForUser(group, user) });
    }

    // getDeviceScopeForUser over a fake database
    const r = rng(4242);
    const scopes = [];
    const config = require(path.join(WEB, 'config/config'));
    for (let i = 0; i < 1200; i++) {
        const deviceCount = r.pick([0, 1, 3, 6, 10]);
        const devices = [];
        for (let d = 0; d < deviceCount; d++) {
            const device = { id: `D${d}` };
            if (r.chance(0.6)) device.folder_id = r.pick([1, 2, 3]);
            if (r.chance(0.5)) device.tags = r.pick(['lab', 'Lab,prod', '["prod","x"]', 'x', ' LAB ']);
            devices.push(device);
        }
        const groupCount = r.pick([0, 1, 2, 4]);
        const groups = [];
        const members = {};
        for (let g = 0; g < groupCount; g++) {
            const kind = r.pick(['manual', 'tag', 'folder', 'manual']);
            const group = {
                guid: kind === 'folder' ? `folder_${r.pick([1, 2, 3])}` : `dg${g}`,
                name: `Group ${g}`,
                source_type: kind === 'tag' ? 'tag' : 'manual',
                tag_filter: kind === 'tag' ? r.pick(['lab', 'PROD', 'x', '']) : '',
                allowed_users: r.pick([[], [], ['op1'], ['other'], ['op1', 'other']]),
                allowed_groups: r.pick([[], [], ['ug1'], ['ug2'], ['ug1', 'ug2']]),
            };
            group.allowed_user_groups = group.allowed_groups.map((guid) => ({ guid, name: guid }));
            members[group.guid] = devices.filter(() => r.chance(0.35)).map((d) => d.id);
            if (r.chance(0.15)) members[group.guid].push('GONE1');
            groups.push(group);
        }
        const grants = r.pick([[], [], [], ['D0'], ['D1', 'ZZ9'], ['D2', 'D3', 'D4']]);
        const user = r.pick([
            { id: 5, username: 'op1', role: 'operator' }, { id: 5, username: 'op1', role: 'viewer' },
            { id: 5, username: 'op1', role: 'operator', user_groups: ['ug1'] }, { id: 5, username: 'op1', role: 'guest_issuer', user_groups: [] },
            { id: 6, username: 'nobody', role: 'operator' }, { id: 0, username: 'op1', role: 'operator' },
            { id: 5, username: 'op1', role: 'global_admin' }, { id: 5, username: 'op1', role: 'server_admin' },
            { id: 5, username: 'op1', role: 'admin' }, null,
        ]);
        const userGroupRows = r.pick([[], [{ guid: 'ug1' }], [{ guid: 'ug2' }, { guid: 'ug3' }]]);
        const setting = r.pick([null, null, 'open', 'restricted', 'RESTRICTED', '']);
        const env = r.pick(['open', 'open', 'restricted']);
        const failing = r.pick([null, null, null, null, 'grants', 'userGroups', 'members', 'setting']);
        const db = {
            getSetting: async () => { if (failing === 'setting') throw new Error('boom'); return setting; },
            getAllDeviceGroups: async () => groups.map((g) => ({ ...g })),
            getUserGroupsForUser: async () => { if (failing === 'userGroups') throw new Error('boom'); return userGroupRows; },
            getUserPeerGrants: async () => { if (failing === 'grants') throw new Error('boom'); return grants; },
            getDeviceGroupMembers: async (guid) => { if (failing === 'members') throw new Error('boom'); return members[guid] || []; },
        };
        config.deviceScopeDefault = env;
        deviceGroupService.invalidateDeviceScopeDefaultCache();
        const scope = await deviceGroupService.getDeviceScopeForUser(db, user, devices);
        const target = r.chance(0.8) && devices.length ? r.pick(devices) : (r.chance(0.5) ? { id: 'ZZ9' } : null);
        deviceGroupService.invalidateDeviceScopeDefaultCache();
        const alone = await deviceGroupService.userCanAccessDevice(db, user, target);
        deviceGroupService.invalidateDeviceScopeDefaultCache();
        const amongAll = await deviceGroupService.userCanAccessDevice(db, user, target, devices);
        deviceGroupService.invalidateDeviceScopeDefaultCache();
        const count = await userScopeService.countEffectiveScope(db, user, devices);
        const filtered = deviceGroupService.filterDevicesByScope(devices, scope).map((d) => d.id);
        deviceGroupService.invalidateDeviceScopeDefaultCache();
        const enriched = (await deviceGroupService.enrichGroups(db, groups, devices)).map((g) => g.member_count);
        scopes.push({
            devices, groups, members, grants, user, userGroupRows, setting, env, failing, target,
            expected: {
                scope: scope === null ? null : [...scope].sort(),
                canAccessAlone: !!alone, canAccessAmongAll: !!amongAll, count, filtered, memberCounts: enriched,
            },
        });
    }
    config.deviceScopeDefault = 'open';
    write('scope.json', { tags, usernames, guids, folderGuids, folderIds, peerIds, allowed, scopes });
    counts.scope = tags.length + usernames.length + guids.length + folderGuids.length + folderIds.length + peerIds.length
        + allowed.length + scopes.length * 6;
}

// ---------------------------------------------------------------- 12. the role cache and its failures

async function roleServiceVectors() {
    const steps = [];
    let calls = 0;
    const realGet = apiTarget.getRolePermissions;
    const record = async (label, fn) => {
        const before = calls;
        try {
            const value = await fn();
            steps.push({ label, value: value instanceof Set ? [...value].sort() : value, calls: calls - before });
        } catch (error) {
            steps.push({ label, error: true, calls: calls - before });
        }
    };
    const answers = { operator: ['device.view', 'user.view', 5, null], viewer: ['device.view'] };
    world.rolePermissions = answers;
    const counted = async (role) => { calls++; return realGet(role); };
    apiTarget.getRolePermissions = counted;
    rolePermissionService.clearCache();
    await record('first lookup', () => rolePermissionService.getEffectivePermissions('operator'));
    await record('second lookup is cached', () => rolePermissionService.getEffectivePermissions('operator'));
    rolePermissionService.invalidateRole('operator');
    answers.operator = [];
    await record('after invalidate', () => rolePermissionService.getEffectivePermissions('operator'));
    await record('no role', () => rolePermissionService.getEffectivePermissions(''));
    await record('unknown role fails', () => rolePermissionService.getEffectivePermissions('nobody'));
    await record('a failure is not cached', () => rolePermissionService.getEffectivePermissions('nobody'));
    await record('two at once share one call', async () => {
        rolePermissionService.clearCache();
        const [a, b] = await Promise.all([
            rolePermissionService.getEffectivePermissions('viewer'),
            rolePermissionService.getEffectivePermissions('viewer'),
        ]);
        return [[...a], [...b]];
    });
    for (const [role, permission] of [['pro', 'device.view'], ['pro', 'chat.access'], ['admin', 'anything.at_all'],
        ['super_admin', 'device.view'], ['viewer', 'device.view'], ['viewer', 'device.edit'], ['nobody', 'device.view']]) {
        rolePermissionService.clearCache();
        await record(`roleHasEffectivePermission ${role} ${permission}`, () => auth.roleHasEffectivePermission(role, permission));
    }
    // loadEffectivePermissions for each kind of session
    const loads = [];
    for (const session of [undefined, {}, { userId: 3 }, { userId: 3, user: {} }, { user: { role: 'viewer' } },
        { userId: 3, user: { role: 'viewer' } }, { userId: 3, user: { role: 'admin' } },
        { userId: 3, user: { role: 'super_admin' } }, { userId: 3, user: { role: 'nobody' } }]) {
        rolePermissionService.clearCache();
        const req = { session };
        const before = calls;
        await new Promise((resolve) => rolePermissionService.loadEffectivePermissions(req, {}, resolve));
        loads.push({
            session: session || null, loaded: req.permissionContextLoaded === true,
            effective: req.effectivePermissions ? [...req.effectivePermissions].sort() : null,
            error: !!req.permissionLoadError, calls: calls - before,
        });
    }
    apiTarget.getRolePermissions = realGet;
    write('role_service.json', { steps, loads });
    counts.roleService = steps.length + loads.length;
}

(async () => {
    await policyMergeVectors();
    guardVectors();
    await bootstrapVectors();
    await scopeVectors();
    await roleServiceVectors();

    const total = Object.values(counts).reduce((a, b) => a + b, 0);
    fs.writeFileSync(path.join(OUT, 'counts.json'), `${JSON.stringify({ counts, total }, null, 2)}\n`);
    console.log(JSON.stringify({ counts, total }, null, 2));
    process.exit(0);
})().catch((error) => {
    console.error(error);
    process.exit(1);
});
