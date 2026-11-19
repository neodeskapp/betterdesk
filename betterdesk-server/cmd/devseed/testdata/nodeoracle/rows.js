#!/usr/bin/env node
// Records the rows web-nodejs/scripts/dev/seed-ui-data.js writes straight
// into the database, for oracle_test.go. The script runs unchanged on a
// recording stand-in for better-sqlite3, at fixed times, and every INSERT
// it runs is written as {table, row: {column: value}}, with the literals of
// its VALUES list filled in. An UPDATE is kept with its parameters.
//
//   node rows.js > ../rows.json.gz   (gzip is applied by the script)
'use strict';
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const script = path.resolve(__dirname, '../../../../../web-nodejs/scripts/dev/seed-ui-data.js');
const source = fs.readFileSync(script, 'utf8').replace(/^#!.*\n/, '');
const body = source.slice(0, source.lastIndexOf('(async () => {'));

const TABLES = [
    'peers', 'server_config', 'help_requests', 'chat_messages', 'chat_groups', 'device_inventory', 'device_telemetry',
    'activity_sessions', 'activity_summaries', 'peer_availability_events', 'peer_connection_sessions', 'peer_metrics',
    'users', 'pending_registrations', 'tickets', 'ticket_comments', 'audit_log', 'login_attempts', 'panel_sessions',
    'billing_sessions', 'billing_work_reports', 'billing_session_ledger', 'billing_contracts', 'billing_period_totals',
];

// splitTop splits a list at the commas outside parentheses and quotes.
function splitTop(text) {
    const out = [];
    let depth = 0; let quote = false; let cur = '';
    for (const ch of text) {
        if (ch === "'" ) quote = !quote;
        if (!quote && ch === '(') depth++;
        if (!quote && ch === ')') depth--;
        if (!quote && depth === 0 && ch === ',') { out.push(cur.trim()); cur = ''; continue; }
        cur += ch;
    }
    if (cur.trim()) out.push(cur.trim());
    return out;
}

function literal(text) {
    if (/^'.*'$/s.test(text)) return text.slice(1, -1).replace(/''/g, "'");
    if (/^-?\d+(\.\d+)?$/.test(text)) return Number(text);
    if (/^null$/i.test(text)) return null;
    return { sql: text };
}

function record(state) {
    return function Database() {
        return {
            pragma() {},
            close() {},
            transaction(fn) { return (...a) => fn(...a); },
            prepare(sql) {
                const flat = sql.replace(/\s+/g, ' ').trim();
                return {
                    all(...params) {
                        if (/FROM sqlite_master/.test(flat)) return TABLES.map((name) => ({ name }));
                        if (/PRAGMA table_info\(peers\)/.test(flat)) return [{ name: 'id' }, { name: 'device_type' }];
                        if (/PRAGMA table_info\(panel_sessions\)/.test(flat)) return [{ name: 'is_active' }, { name: 'end_reason' }];
                        if (/^SELECT id, username FROM users/.test(flat)) return state.users.map(([id, username]) => ({ id, username }));
                        return [];
                    },
                    get(...params) {
                        if (/SUM\(overage_minutes\)/.test(flat)) {
                            const rows = state.rows.filter((r) => r.table === 'billing_sessions' && r.row.contract_id === params[0]);
                            return { m: rows.reduce((s, r) => s + r.row.overage_minutes, 0), a: rows.reduce((s, r) => s + r.row.amount_overage, 0) };
                        }
                        return undefined;
                    },
                    run(...params) {
                        const insert = /^INSERT INTO (\w+) \(([^)]*)\) VALUES \((.*?)\)( ON CONFLICT.*)?$/.exec(flat);
                        if (insert) {
                            const [, table, colText, valText] = insert;
                            const cols = colText.split(',').map((c) => c.trim());
                            const vals = splitTop(valText);
                            const named = params.length === 1 && params[0] && typeof params[0] === 'object' && !Array.isArray(params[0]) ? params[0] : null;
                            let next = 0;
                            const row = {};
                            cols.forEach((c, i) => {
                                const v = vals[i];
                                if (v === '?') row[c] = params[next++];
                                else if (v.startsWith('@')) row[c] = named[v.slice(1)];
                                else row[c] = literal(v);
                                if (row[c] === undefined) row[c] = null;
                            });
                            state.rows.push({ table, row });
                            state.ids[table] = (state.ids[table] || 0) + 1;
                            return { lastInsertRowid: state.ids[table], changes: 1 };
                        }
                        if (/^UPDATE /.test(flat)) state.updates.push({ sql: flat, params });
                        return { changes: 0 };
                    },
                };
            },
        };
    };
}

function run(now, users) {
    const state = { rows: [], updates: [], ids: {}, users };
    const realNow = Date.now;
    Date.now = () => now;
    try {
        const req = (name) => (name === 'better-sqlite3' ? record(state) : require(name));
        const proc = { argv: ['node', script], env: { ADMIN_PASSWORD: 'x' }, exit() { throw new Error('exit'); } };
        const quiet = { log() {}, warn() {}, error() {} };
        // eslint-disable-next-line no-new-func
        const api = new Function('require', 'process', 'console', `${body}\nreturn { seedGoDb, seedAuthDb, seedBillingSessions };`)(req, proc, quiet);
        api.seedGoDb();
        api.seedAuthDb();
        const orgIds = { northwind: 'org-northwind', harbor: 'org-harbor', lab: 'org-lab' };
        const pkg = (id, included, rate, rounding) => ({ id, name: id, currency: 'EUR', included_minutes: included, overage_rate: rate, rounding_minutes: rounding });
        api.seedBillingSessions(orgIds, {
            northwind: { id: 'contract-northwind', pkg: pkg('pkg-business', 1200, 48, 10) },
            harbor: { id: 'contract-harbor', pkg: pkg('pkg-basic', 300, 60, 15) },
            lab: { id: 'contract-lab', pkg: pkg('pkg-payg', 0, 75, 5) },
        });
    } finally {
        Date.now = realNow;
    }
    return { now, users, rows: state.rows, updates: state.updates };
}

const ADMIN = [[1, 'admin']];
const DEMO = [[1, 'admin'], [2, 'demo_operator'], [3, 'demo_viewer'], [4, 'demo_serveradmin']];
const cases = [
    run(Date.parse('2026-11-19T00:31:00.000Z'), ADMIN),
    run(Date.parse('2026-11-21T12:00:00.123Z'), ADMIN),
    run(Date.parse('2026-11-22T23:59:59.999Z'), DEMO),
    run(Date.parse('2026-11-23T07:45:30.500Z'), ADMIN),
    run(Date.parse('2026-12-31T18:30:00.000Z'), ADMIN),
    run(Date.parse('2027-03-01T09:00:00.007Z'), DEMO),
];
// At many more times only a SHA-256 per table: the JSON of its rows as
// lists of values in the order of the INSERT's columns.
const crypto = require('crypto');
const FINGERPRINTED = ['activity_sessions', 'activity_summaries', 'peer_availability_events', 'peer_metrics',
    'peer_connection_sessions', 'device_inventory', 'device_telemetry', 'tickets', 'billing_sessions'];
const fingerprints = [];
for (let k = 0; k < 200; k++) {
    const now = Date.parse('2026-11-16T00:00:00.000Z') + k * (97 * 60000 + 13371);
    const c = run(now, ADMIN);
    const sums = {};
    for (const table of FINGERPRINTED) {
        const rows = c.rows.filter((r) => r.table === table).map((r) => Object.values(r.row));
        sums[table] = crypto.createHash('sha256').update(JSON.stringify(rows)).digest('hex');
    }
    fingerprints.push({ now, sums });
}
process.stdout.write(zlib.gzipSync(JSON.stringify({ script: 'web-nodejs/scripts/dev/seed-ui-data.js', cases, fingerprints })));
