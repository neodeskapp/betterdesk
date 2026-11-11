'use strict';

/**
 * Records the capacity badge of GET /api/auth/login-page for many stored
 * test results and badge texts: what services/serverAttestation.js makes of
 * the two settings, and what the route then sends as "attestation". See
 * README.md.
 *
 *   node badge.js   (in the dev image, from this folder)
 *
 * Writes ../badge.json.gz.
 */

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';

function stub(rel, exports) {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

// The settings table, as db.getSetting reads it
let settings = {};
let failing = {};
stub('services/database', {
    async getSetting(key) {
        if (failing[key]) throw new Error('database is locked');
        return Object.prototype.hasOwnProperty.call(settings, key) ? settings[key] : null;
    },
    async setSetting() {},
});
stub('services/serverManagement', { getResourceSnapshot: async () => ({}) });
stub('services/betterdeskApi', {});

const ATTESTATION = require.resolve(path.join(WEB, 'services/serverAttestation'));

/** routes/auth.routes.js: attestationSummary and the route's "attestation" */
async function attestation(t, brandName) {
    // A fresh module each time: the module keeps the badge texts it read
    delete require.cache[ATTESTATION];
    const serverAttestation = require(ATTESTATION);
    let badge;
    try {
        const last = await serverAttestation.getLastResult();
        const presentation = await serverAttestation.getBadgePresentation({
            tier: last && last.tier,
            translate: t,
            brandName,
        });
        badge = serverAttestation.buildPublicSummary(last, presentation);
    } catch (_) {
        badge = { tier: null, maxConnections: null };
    }
    const out = badge && badge.valid && badge.tier
        ? {
            tier: String(badge.tier),
            brand: badge.brand || '',
            badgeTitle: badge.badgeTitle || '',
            tierLabel: badge.tierLabel || '',
        }
        : null;
    return JSON.stringify(out);
}

const J = JSON.stringify;
const tiers = ['bronze', 'iron', 'platinum', 'titanium', 'obsidian'];
const results = [
    null, '', 'not json', 'null', '0', '1', '"iron"', 'true', '[]', '["iron"]', '{}',
    ...tiers.map((tier) => J({ tier, maxConnections: 30, testedAt: '2026-11-10T10:00:00.000Z', valid: true })),
    J({ tier: 'iron' }), J({ tier: 'iron', valid: false }), J({ tier: 'iron', valid: 0 }), J({ tier: 'iron', valid: null }),
    J({ tier: 'iron', valid: 'false' }), J({ tier: null }), J({ tier: '' }), J({ tier: 0 }), J({ tier: 7 }),
    J({ tier: 'IRON' }), J({ tier: 'unknown' }), J({ tier: ['iron'] }), J({ tier: [['platinum']] }), J({ tier: [] }),
    J({ tier: ['iron', 'bronze'] }), J({ tier: {} }), J({ tier: { a: 1 } }), J({ tier: { toString: 1 } }),
    J({ tier: [{ toString: 'x' }] }), J({ tier: true }), J({ tier: 'constructor' }), J({ tier: 'toString' }),
    J({ tier: '__proto__' }), J({ tier: 'hasOwnProperty' }), J({ tier: 'valueOf' }), J({ tier: 1.5 }),
    '{"tier":"iron","tier":"bronze"}', '{"valid":false,"tier":"obsidian","valid":true}',
];
const configs = [
    null, '', 'not json', 'null', '"text"', '[]', '{}', '[{"badgeTitle":"x"}]',
    J({ badgeTitle: 'Our capacity', tierLabels: { iron: 'Eisen', bronze: 'Bronze!' } }),
    J({ badgeTitle: '  padded \u0001\u007f title  ', tierLabels: { iron: '\tlabel\n' } }),
    J({ badgeTitle: 'x'.repeat(95) + ' y', tierLabels: { iron: 'a'.repeat(31) + ' b' } }),
    J({ badgeTitle: 'é'.repeat(120), tierLabels: { iron: '€'.repeat(40) } }),
    J({ badgeTitle: 12345, tierLabels: { iron: 7, bronze: true, platinum: null, titanium: ['a', 'b'], obsidian: {} } }),
    J({ badgeTitle: ['t', 1], tierLabels: [] }),
    J({ badgeTitle: '', tierLabels: 'iron' }),
    J({ badgeTitle: { toString: 'x' } }),
    J({ tierLabels: { iron: { toString: 1 } } }),
    J({ badgeTitle: { toString: 1 }, tierLabels: { iron: 'Eisen' } }),
    J({ tierLabels: { bronze: 'Bronze', iron: { toString: 1 } } }),
    J({ badgeTitle: ' ﻿ spaced  ' }),
    J({ tierLabels: { constructor: 'c', iron: '  ' } }),
    J({ badgeTitle: `a${String.fromCharCode(0x1f)}b${String.fromCharCode(0x20)}`, tierLabels: { iron: `${String.fromCharCode(0x1f, 0x1e)} x` } }),
];
const brands = ['BetterDesk', '', 'Contract Console', '  Brand\u0001  ', 'b'.repeat(120)];
const translations = {
    none: {},
    some: {
        'server_attestation.badge_tooltip': 'Tested capacity',
        'server_attestation.tier_iron': 'Iron tier',
        'server_attestation.tier_obsidian': 'Obsidian tier',
    },
    empty: { 'server_attestation.badge_tooltip': '', 'server_attestation.tier_iron': '' },
};

async function main() {
    const vectors = [];
    for (const result of results) {
        for (const config of configs) {
            for (const brand of brands) {
                for (const [name, texts] of Object.entries(translations)) {
                    settings = {};
                    failing = {};
                    if (result !== null) settings.server_attestation_result = result;
                    if (config !== null) settings.server_attestation_badge_config = config;
                    const t = (key) => (Object.prototype.hasOwnProperty.call(texts, key) ? texts[key] : key);
                    vectors.push({ result, config, brand, texts: name, out: await attestation(t, brand) });
                }
            }
        }
    }
    // A database that cannot be read
    for (const key of ['server_attestation_result', 'server_attestation_badge_config']) {
        settings = { server_attestation_result: J({ tier: 'iron' }), server_attestation_badge_config: J({ badgeTitle: 'Ours' }) };
        failing = { [key]: true };
        vectors.push({
            result: settings.server_attestation_result, config: settings.server_attestation_badge_config,
            failing: key, brand: 'BetterDesk', texts: 'none', out: await attestation((k) => k, 'BetterDesk'),
        });
    }
    const file = path.join(__dirname, '..', 'badge.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(`${JSON.stringify({ translations, vectors }, null, 1)}\n`));
    console.log(`${file}: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
