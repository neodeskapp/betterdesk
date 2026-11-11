'use strict';

/**
 * Records what the routes of routes/server-attestation.routes.js answer
 * for many stored settings, bodies, brand names and translations, without
 * starting a capacity test. See README.md.
 *
 *   node routes.js   (in the dev image, from this folder)
 *
 * Writes ../routes.json.gz.
 */

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const http = require('http');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const express = require(path.join(WEB, 'node_modules/express'));

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

// The settings table and the audit log, as services/database has them.
// A key in failing makes the read throw; failWrite and failLog make the
// writes throw.
const db = {
    settings: {},
    failing: {},
    failWrite: false,
    failLog: false,
    writes: [],
    logs: [],
    reset() {
        this.settings = {};
        this.failing = {};
        this.failWrite = false;
        this.failLog = false;
        this.writes = [];
        this.logs = [];
    },
};
stub('services/database', {
    async getSetting(key) {
        if (db.failing[key]) throw new Error('database is locked');
        return Object.prototype.hasOwnProperty.call(db.settings, key) ? db.settings[key] : null;
    },
    async setSetting(key, value) {
        if (db.failWrite) throw new Error('SQLITE_READONLY: attempt to write a readonly database');
        db.writes.push([key, value]);
        db.settings[key] = value;
    },
    async logAction(userId, action, details) {
        if (db.failLog) throw new Error('no such table: audit_log');
        db.logs.push([userId, action, details]);
    },
});
stub('services/serverManagement', { getResourceSnapshot: () => ({}) });
stub('services/betterdeskApi', {});
let brand;
stub('services/brandingService', { getBranding: () => (brand === undefined ? {} : { appName: brand }) });
stub('middleware/auth', {
    requireAuth: (req, res, next) => next(),
    requirePermission: () => (req, res, next) => next(),
});

const ROUTES = require.resolve(path.join(WEB, 'routes/server-attestation.routes'));
const SERVICE = require.resolve(path.join(WEB, 'services/serverAttestation'));

// A fresh route file and service for every vector: the service keeps the
// badge texts it read and the last result it loaded.
let router = null;
function freshModules() {
    delete require.cache[ROUTES];
    delete require.cache[SERVICE];
    router = require(ROUTES);
}

const translations = {
    none: {},
    some: {
        'server_attestation.badge_tooltip': 'Tested capacity',
        'server_attestation.tier_bronze': 'Bronze tier',
        'server_attestation.tier_obsidian': 'Obsidian tier',
    },
    empty: { 'server_attestation.badge_tooltip': '', 'server_attestation.tier_iron': '' },
};
let texts = translations.none;

const app = express();
app.use(express.json({ limit: '2mb' }));
app.use((req, res, next) => {
    req.t = (key) => (Object.prototype.hasOwnProperty.call(texts, key) ? texts[key] : key);
    req.session = { userId: 7 };
    next();
});
app.use((req, res, next) => router(req, res, next));
app.use((err, req, res, next) => {
    res.status(err.status || 500).json({ parserError: true, status: err.status || 500 });
});

let base = '';
async function call(method, url, body) {
    const res = await fetch(base + url, {
        method,
        headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
        body,
    });
    return { status: res.status, body: await res.text() };
}

const J = JSON.stringify;
const cc = (...codes) => String.fromCharCode(...codes);
const tiers = ['bronze', 'iron', 'platinum', 'titanium', 'obsidian'];

// Stored results: the JSON text of server_attestation_result
const results = [
    null, '', 'not json', 'null', '0', '1', '"iron"', 'true', 'false', '[]', '["iron"]', '{}',
    ...tiers.map((tier) => J({ valid: true, mode: 'loadtest', tier, maxConnections: 30, testedAt: '2026-11-10T10:00:00.000Z', completedAt: '2026-11-10T10:00:01.000Z', rampSteps: [{ cpu: 1 }], baseline: { hostname: 'h' } })),
    J({ tier: 'iron' }), J({ tier: 'iron', valid: false }), J({ tier: 'iron', valid: 0 }), J({ tier: 'iron', valid: null }),
    J({ tier: 'iron', maxConnections: 0 }), J({ tier: 'iron', maxConnections: '12' }), J({ tier: 'iron', maxConnections: { n: 1 } }),
    J({ tier: 'iron', testedAt: '', completedAt: 'done' }), J({ tier: 'iron', testedAt: 0, completedAt: 0 }),
    J({ tier: 'iron', testedAt: { at: 1 } }), J({ tier: null, maxConnections: 99 }), J({ tier: '' }), J({ tier: 0 }),
    J({ tier: 7 }), J({ tier: 1.5 }), J({ tier: 'IRON' }), J({ tier: 'unknown' }), J({ tier: ['iron'] }),
    J({ tier: [['platinum']] }), J({ tier: [] }), J({ tier: ['iron', 'bronze'] }), J({ tier: {} }), J({ tier: { a: 1 } }),
    J({ tier: { toString: 1 } }), J({ tier: [{ toString: 'x' }] }), J({ tier: true }), J({ tier: 'constructor' }),
    J({ tier: 'toString' }), J({ tier: '__proto__' }), J({ tier: 'hasOwnProperty' }), J({ tier: 'valueOf' }),
    J({ tier: 'toLocaleString' }), J({ tier: '__lookupGetter__' }), J({ tier: 'isPrototypeOf' }),
    '{"tier":"iron","tier":"bronze"}', '{"__proto__":{"tier":"iron"}}', '{"tier":"obsidian","__proto__":1}',
    '{"tier":"iron","maxConnections":1e2,"big":12345678901234567890,"tiny":1e-7,"text":"a' + cc(0xe9) + '\\u2028\\ud800"}',
];

// Stored badge texts: the JSON text of server_attestation_badge_config
const configs = [
    null, '', 'not json', 'null', '"text"', '[]', '{}', '[{"badgeTitle":"x"}]',
    J({ badgeTitle: 'Our capacity', tierLabels: { iron: 'Eisen', bronze: 'Bronze!' } }),
    J({ badgeTitle: `  padded ${cc(1)}${cc(0x7f)} title  `, tierLabels: { iron: '\tlabel\n' } }),
    J({ badgeTitle: 'x'.repeat(95) + ' y', tierLabels: { iron: 'a'.repeat(31) + ' b' } }),
    J({ badgeTitle: cc(0xe9).repeat(120), tierLabels: { iron: cc(0x20ac).repeat(40) } }),
    J({ badgeTitle: 12345, tierLabels: { iron: 7, bronze: true, platinum: null, titanium: ['a', 'b'], obsidian: {} } }),
    J({ badgeTitle: ['t', 1], tierLabels: [] }),
    J({ badgeTitle: '', tierLabels: 'iron' }),
    J({ badgeTitle: { toString: 'x' } }),
    J({ tierLabels: { iron: { toString: 1 } } }),
    J({ tierLabels: { bronze: 'Bronze', iron: { toString: 1 } } }),
    J({ badgeTitle: ` ${cc(0xfeff)} spaced  ` }),
    J({ tierLabels: { constructor: 'c', iron: '  ' } }),
    '{"badgeTitle":"half \\ud83d of \\ud83d\\ude00","tierLabels":{"iron":"' + 'a'.repeat(31) + '\\ud83d\\ude00"}}',
    J({ badgeTitle: 'x', tierLabels: { obsidian: 'O'.repeat(40) } }),
];

const brands = ['BetterDesk', '', 'Contract Console', `  Brand${cc(1)}  `, 'b'.repeat(120), undefined, null, 42];

// PUT bodies, as JSON text
const bodies = [
    '{}', '[]', '[{"badgeTitle":"x"}]', '[1,2]',
    J({ badgeTitle: 'Custom title', tierLabels: { bronze: 'Starter' } }),
    J({ badgeTitle: `  Custom${cc(0)} title ${'x'.repeat(120)}  `, tierLabels: { bronze: `  Bronze${cc(7)} ${'y'.repeat(60)}  `, iron: '<b>Iron</b>' }, unexpected: 'ignored' }),
    J({ badgeTitle: 12345, tierLabels: { iron: 7, bronze: true, platinum: null, titanium: ['a', ['b', 'c'], null], obsidian: {} } }),
    J({ badgeTitle: ['t', 1], tierLabels: ['a', 'b'] }),
    J({ badgeTitle: '', tierLabels: 'iron' }),
    J({ badgeTitle: null, tierLabels: null }),
    J({ badgeTitle: false, tierLabels: false }),
    J({ badgeTitle: 0, tierLabels: 0 }),
    J({ badgeTitle: true, tierLabels: true }),
    J({ badgeTitle: { a: 1 }, tierLabels: { iron: { a: 1 } } }),
    J({ badgeTitle: { toString: 1 } }),
    J({ badgeTitle: { toString: 'x' }, tierLabels: { iron: 'fine' } }),
    J({ tierLabels: { iron: { toString: 1 } } }),
    J({ tierLabels: { bronze: 'ok', titanium: ['a', { toString: 1 }] } }),
    J({ tierLabels: { obsidian: [[{ valueOf: 1 }]] } }),
    J({ tierLabels: { obsidian: [[{ toString: 1 }]] } }),
    J({ badgeTitle: 'x'.repeat(95) + ' y', tierLabels: { iron: 'a'.repeat(31) + ' b', bronze: 'c'.repeat(32) + 'd' } }),
    J({ badgeTitle: cc(0xe9).repeat(120), tierLabels: { iron: cc(0x20ac).repeat(40) } }),
    J({ badgeTitle: `${cc(0xa0)}${cc(0xfeff)}${cc(0x2003)} spaced ${cc(0x3000)}`, tierLabels: { iron: `${cc(0x1f, 0x1e)} x ${cc(0x85)}` } }),
    '{"badgeTitle":"half \\ud83d of an emoji \\ud83d\\ude00 and \\u2028","tierLabels":{"iron":"' + 'a'.repeat(31) + '\\ud83d\\ude00"}}',
    '{"badgeTitle":"' + 'e'.repeat(95) + '\\ud83d\\ude00"}',
    '{"badgeTitle":"first","tierLabels":{"iron":"a"},"badgeTitle":"second"}',
    '{"tierLabels":{"iron":"a"},"tierLabels":{"bronze":"b"}}',
    '{"__proto__":{"badgeTitle":"proto"},"tierLabels":{"__proto__":{"iron":"p"}}}',
    '{"badgeTitle":"' + '\\u0000'.repeat(5) + 'kept' + '\\u001f\\u007f' + '"}',
    J({ badgeTitle: 1e21, tierLabels: { iron: 1e-7, bronze: -0, platinum: 0.1 + 0.2, titanium: 123456789012345680000 } }),
    J({ badgeTitle: [], tierLabels: { iron: [] } }),
    J({ badgeTitle: [null, undefined, 'x'], tierLabels: { iron: [[], [[]]] } }),
];

async function main() {
    const server = http.createServer(app);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    base = `http://127.0.0.1:${server.address().port}`;
    const out = { translations, put: [], get: [], public: [], status: [], abort: [] };

    // PUT /api/server-attestation/badge-config, then GET from the same module
    for (const body of bodies) {
        for (const b of ['BetterDesk', 'Contract Console', undefined]) {
            for (const [name, set] of Object.entries(translations)) {
                for (const mode of ['ok', 'failWrite', 'failLog']) {
                    if (mode !== 'ok' && (b !== 'BetterDesk' || name !== 'none')) continue;
                    db.reset();
                    db.failWrite = mode === 'failWrite';
                    db.failLog = mode === 'failLog';
                    brand = b;
                    texts = set;
                    freshModules();
                    const put = await call('PUT', '/api/server-attestation/badge-config', body);
                    const writes = db.writes;
                    const logs = db.logs;
                    db.failWrite = false;
                    const get = await call('GET', '/api/server-attestation/badge-config');
                    out.put.push({ body, brand: b === undefined ? { undefined: true } : b, texts: name, mode, put, writes, logs, get });
                }
            }
        }
    }

    // GET /api/server-attestation/badge-config with the texts stored
    for (const config of configs) {
        for (const b of brands) {
            for (const [name, set] of Object.entries(translations)) {
                for (const failing of [false, true]) {
                    if (failing && (b !== 'BetterDesk' || name !== 'none')) continue;
                    db.reset();
                    if (config !== null) db.settings.server_attestation_badge_config = config;
                    if (failing) db.failing.server_attestation_badge_config = true;
                    brand = b;
                    texts = set;
                    freshModules();
                    const first = await call('GET', '/api/server-attestation/badge-config');
                    // The texts are read once: a change of the row is not seen
                    db.settings.server_attestation_badge_config = J({ badgeTitle: 'changed behind its back' });
                    delete db.failing.server_attestation_badge_config;
                    const second = await call('GET', '/api/server-attestation/badge-config');
                    out.get.push({ config, failing, brand: b === undefined ? { undefined: true } : b, texts: name, first, second });
                }
            }
        }
    }

    // GET /api/public/server-attestation
    for (const result of results) {
        for (const config of configs) {
            for (const b of ['BetterDesk', '', 'b'.repeat(120), undefined, 42]) {
                for (const [name, set] of Object.entries(translations)) {
                    db.reset();
                    if (result !== null) db.settings.server_attestation_result = result;
                    if (config !== null) db.settings.server_attestation_badge_config = config;
                    brand = b;
                    texts = set;
                    freshModules();
                    const answer = await call('GET', '/api/public/server-attestation');
                    out.public.push({ result, config, brand: b === undefined ? { undefined: true } : b, texts: name, answer });
                }
            }
        }
    }
    for (const key of ['server_attestation_result', 'server_attestation_badge_config']) {
        db.reset();
        db.settings.server_attestation_result = J({ tier: 'iron', maxConnections: 40 });
        db.settings.server_attestation_badge_config = J({ badgeTitle: 'Ours' });
        db.failing[key] = true;
        brand = 'BetterDesk';
        texts = translations.none;
        freshModules();
        const answer = await call('GET', '/api/public/server-attestation');
        out.public.push({
            result: db.settings.server_attestation_result, config: db.settings.server_attestation_badge_config,
            failing: key, brand: 'BetterDesk', texts: 'none', answer,
        });
    }

    // GET status and result
    for (const result of results) {
        for (const failing of [false, true]) {
            db.reset();
            if (result !== null) db.settings.server_attestation_result = result;
            if (failing) db.failing.server_attestation_result = true;
            freshModules();
            const status = await call('GET', '/api/server-attestation/status');
            const res = await call('GET', '/api/server-attestation/result');
            out.status.push({ result, failing, status, result_answer: res });
        }
    }

    // POST abort with no capacity test running
    db.reset();
    freshModules();
    out.abort.push(await call('POST', '/api/server-attestation/abort', '{}'));

    server.close();
    const file = path.join(__dirname, '..', 'routes.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(`${JSON.stringify(out, null, 1)}\n`, { level: 9 }));
    const n = out.put.length + out.get.length + out.public.length + out.status.length + out.abort.length;
    console.log(`${file}: ${n} vectors (put ${out.put.length}, get ${out.get.length}, public ${out.public.length}, status ${out.status.length})`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
