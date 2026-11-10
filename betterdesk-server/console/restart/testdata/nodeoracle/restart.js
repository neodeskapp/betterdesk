'use strict';

// Runs services/restartCoordinator.js of the Node console through scripted
// lives of a restart transaction and writes ../vectors.json.gz: for every
// step what the function returned or threw, the transaction file
// afterwards, the rollbacks and restarts it asked for, and whether it
// scheduled the exit of the process.
//
// The services the coordinator calls are fakes whose answers each step
// sets. The clock and the random bytes are counters, so the Go test can
// give its service the same and compare the file byte for byte.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');
const Module = require('module');

const WEB = process.env.ORACLE_WEB || '/repo/web-nodejs';
const SCRATCH = fs.mkdtempSync(path.join(os.tmpdir(), 'restart-oracle-'));
const DATA = path.join(SCRATCH, 'data');
process.env.NODE_ENV = 'production';
process.env.SESSION_SECRET = 'oracle-secret-0123456789abcdef0123456789abcdef';
process.env.DATA_DIR = DATA;

function fakeModule(rel, exports) {
    const filename = require.resolve(path.join(WEB, rel));
    const m = new Module(filename, null);
    m.filename = filename;
    m.loaded = true;
    m.exports = exports;
    require.cache[filename] = m;
}

// mulberry32: the same numbers on every run.
function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return { next, int: (n) => Math.floor(next() * n), pick: (list) => list[Math.floor(next() * list.length)], chance: (p) => next() < p };
}

// Random bytes: byte number i of the run is (i * 73 + 41) mod 256.
let randomAt = 0;
function nextBytes(n) {
    const buf = Buffer.alloc(n);
    for (let i = 0; i < n; i++) buf[i] = (randomAt++ * 73 + 41) & 0xff;
    return buf;
}
crypto.randomBytes = (n) => nextBytes(n);
crypto.randomUUID = () => {
    const b = nextBytes(16);
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    const h = b.toString('hex');
    return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
};

// The clock: every new Date() inside a step is one second after the one
// before, from 2026-01-01T00:00:00.000Z on.
const RealDate = Date;
let tick = 0;
class StepDate extends RealDate {
    constructor(...args) {
        if (args.length) super(...args);
        else super(RealDate.UTC(2026, 0, 1) + 1000 * tick++);
    }
}

// What the fakes answer, set per step, and what they were asked.
const script = { restart: null, rollbackErrors: {}, setConfig: { success: true }, health: 'running' };
let calls = [];

function rollbackFake(kind) {
    return () => {
        calls.push([kind]);
        if (script.rollbackErrors[kind]) throw new Error(script.rollbackErrors[kind]);
    };
}

fakeModule('services/serverConnectionConfigService.js', {
    setConnectionMode: async (settings) => {
        // The real function reads settings.mode first
        void settings.mode;
        rollbackFake('connection-mode')();
    },
    restartServer: () => {
        calls.push(['restartServer']);
        if (script.restart.throw !== undefined) throw new Error(script.restart.throw);
        return JSON.parse(JSON.stringify(script.restart.result));
    },
});
fakeModule('services/advancedConfigService.js', { restoreBackup: async () => rollbackFake('advanced-file')() });
fakeModule('services/billingClockConfigService.js', { restoreClockSettings: rollbackFake('billing-clock') });
fakeModule('lib/managementCapabilities.js', { restoreEnvBackup: rollbackFake('env-config') });
fakeModule('lib/privilegedUpdateHelper.js', { canUsePrivilegedUpdate: () => false, invokePrivilegedUpdate: () => {} });
fakeModule('services/betterdeskApi.js', {
    setConfig: async (key, value) => {
        calls.push(['setConfig', key, value === undefined ? null : value]);
        return JSON.parse(JSON.stringify(script.setConfig));
    },
    getHealth: async () => {
        calls.push(['getHealth']);
        return { status: script.health, backend: 'betterdesk' };
    },
});

const coordinatorFile = require.resolve(path.join(WEB, 'services/restartCoordinator.js'));
let coordinator;
function boot() {
    delete require.cache[coordinatorFile];
    coordinator = require(coordinatorFile);
}

function fileText() {
    try {
        return fs.readFileSync(coordinator.STATE_PATH, 'utf8');
    } catch (err) {
        return null;
    }
}

function currentState() {
    try {
        return JSON.parse(fs.readFileSync(coordinator.STATE_PATH, 'utf8'));
    } catch (err) {
        return null;
    }
}

const restartResults = [
    { result: { source: 'systemd', daemonReload: { success: true }, restarts: [{ component: 'server', service: 'betterdesk-server', success: true }] } },
    { result: { source: 'systemd', daemonReload: { success: true, skipped: true }, restarts: [{ component: 'server', service: 'BetterDeskServer', success: true }] } },
    { result: { source: 'docker', daemonReload: null, restarts: [{ component: 'server', service: 'betterdesk-server', success: true }] } },
    { result: { source: 'systemd', daemonReload: { success: false, error: 'sudo: a password is required' }, restarts: [] } },
    { result: { source: 'systemd', daemonReload: { success: true }, restarts: [{ component: 'server', service: 'betterdesk-server', success: false, service2: 'x', error: 'Job failed', nonCritical: false }] } },
    { result: { source: 'docker', daemonReload: null, restarts: [{ component: 'server', service: 'betterdesk-server', success: false, error: 'Command failed: docker compose up -d server\n/bin/sh: 1: docker: not found\n' }] } },
    { result: { source: 'docker', daemonReload: null, restarts: [] } },
    { throw: 'no_restart' },
    { throw: '' },
    { throw: 'restart server: no service control configured' },
];

script.restart = restartResults[0];

const rollbacks = [
    { type: 'go-config', key: 'cdap_enabled', value: 'N' },
    { type: 'go-config', key: 'cdap_enabled', value: 'Y' },
    { type: 'go-config', key: 'weird key/ä', value: null },
    { type: 'go-config', key: 'n', value: 5 },
    { type: 'go-config', key: 'o', value: { a: [1, 'b'] } },
    { type: 'go-config', key: 'missing-value' },
    { type: 'connection-mode', settings: { mode: 'relay_only', p2p_fallback_ms: 1500, same_nat_relay: true, allow_shared_nat_initiator: false, logged_in_only_initiator: false, operator_only_outbound: false } },
    { type: 'advanced-file', fileId: 'server-env', backupPath: '/tmp/backup-1' },
    { type: 'billing-clock', settings: { ntpServers: 'pool.ntp.org', maxSkewMs: 2000 } },
    { type: 'env-config', backupPath: '/opt/BetterDeskConsole/.env.bak', options: { envPath: '/opt/BetterDeskConsole/.env' } },
    { type: 'something-else' },
    { type: 5 },
    {},
    { type: '' },
];

const keys = ['cdap-enabled', 'connection-mode', 'billing-clock', 'advanced:server-env', 'env:PORT', 'k', 'ключ', '<b>&"key"</b>', 'line sep'];
const labels = ['CDAP devices', 'Connection mode', '', undefined, 'Zażółć gęślą jaźń', '<i>label</i> & "quotes"', 'tab\there', 'emoji \u{1F600}'];
const users = [1, 1, 1, 2, 7, 0];

// Hand-made file contents: what the checks do with a file nobody wrote
// that way.
function corruptions(r, state) {
    const base = state || {
        id: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', userId: '1', phase: 'pending',
        createdAt: '2025-12-31T23:00:00.000Z', updatedAt: '2025-12-31T23:00:01.000Z',
        changes: [{ key: 'k', label: 'K', rollback: { type: 'go-config', key: 'k', value: 'v' } }],
    };
    const variants = [
        (s) => { s.phase = r.pick(['pending', 'restarting', 'ready', 'failed', 'done', '', null, 5, ['pending']]); },
        (s) => { s.userId = r.pick([1, null, '', '01', '1 ', 2, '2', true, ['1']]); },
        (s) => { delete s.userId; },
        (s) => { delete s.phase; },
        (s) => { delete s.id; },
        (s) => { s.id = r.pick([5, null, '', ['x'], { a: 1 }]); },
        (s) => { s.changes = r.pick([null, 0, '', 'abc', 5, {}, { length: 1 }, true]); },
        (s) => { delete s.changes; },
        (s) => { s.changes = [null]; },
        (s) => { s.changes = [5, 'text', [], {}, { key: 5 }, { key: 'k' }, { label: 'only label' }, { key: 'x', label: null, rollback: null }]; },
        (s) => { s.changes = [{ key: 'a', label: 'A', rollback: 'text' }, { key: 'b', label: 'B', rollback: 7 }, { key: 'c', label: 'C' }, { key: 'd', label: 'D', rollback: { type: 'go-config', key: 'd', value: 'v' } }]; },
        (s) => { s.changes = Array.from({ length: r.pick([31, 32, 33]) }, (_, i) => ({ key: `k${i}`, label: `L${i}`, rollback: { type: 'go-config', key: `k${i}`, value: 'v' } })); },
        (s) => { s.phase = 'restarting'; s.token = r.pick(['tok', 5, null, '']); s.bootId = r.pick(['other-boot', null, 5, undefined]); },
        (s) => { s.phase = 'failed'; s.error = r.pick(['rollback_failed', '', null, 0, { a: 1 }, ['x']]); s.rollbackFailures = [{ key: 'k', error: 'x' }]; },
        (s) => { s.startedAt = r.pick(['2025-12-31T23:10:00.000Z', '', 0, 12345, null, false]); s.updatedAt = r.pick(['', 0, null, 12345, { a: 1 }]); },
        (s) => { s.extra = { nested: [1, 2, { deep: true }], n: 1.5, big: 12345678901234567890, neg: -0, text: 'ä<>&' }; },
        (s) => { s['10'] = 'ten'; s['2'] = 'two'; s.__proto__x = 1; },
    ];
    const s = JSON.parse(JSON.stringify(base));
    const n = 1 + r.int(3);
    for (let i = 0; i < n; i++) r.pick(variants)(s);
    return JSON.stringify(s, null, r.pick([2, 0, 1, '\t']));
}

const rawFiles = ['', ' ', 'null', 'false', '0', '123', '"text"', '[]', '[1,2]', '{}', '{"a":', 'not json', '﻿{"id":"x"}', '{"phase":"pending","userId":"1"}',
    '{"id":"dup","id":"second","userId":"1","phase":"failed","phase":"pending","changes":[]}', ' \n\t{"id":"spaced","userId":"1","phase":"ready"} \r\n',
    '{"id":"x","userId":"1","phase":"pending","changes":[]}trailing'];

async function step(spec) {
    const record = { ...spec };
    calls = [];
    let exitDelay = null;
    if (spec.script) Object.assign(script, JSON.parse(JSON.stringify(spec.script)));
    // The record holds the whole script the step ran under
    record.script = JSON.parse(JSON.stringify(script));
    const req = spec.user === undefined ? undefined : { session: spec.user ? { userId: spec.user } : {} };
    const realSetTimeout = global.setTimeout;
    global.Date = StepDate;
    global.setTimeout = (fn, ms, ...rest) => {
        if (ms === 1500) {
            exitDelay = ms;
            return null;
        }
        return realSetTimeout(fn, ms, ...rest);
    };
    try {
        let out;
        switch (spec.op) {
        case 'boot': boot(); break;
        case 'write':
            fs.mkdirSync(DATA, { recursive: true });
            fs.writeFileSync(coordinator.STATE_PATH, spec.content);
            break;
        case 'rmdir': fs.rmSync(DATA, { recursive: true, force: true }); break;
        case 'read': out = coordinator.readState(); break;
        case 'pending': out = coordinator.getPending(req); break;
        case 'register': out = coordinator.registerChange(req, spec.change); break;
        case 'cancel': out = await coordinator.cancel(req, spec.id); break;
        case 'confirm': out = await coordinator.confirm(req, spec.id); break;
        case 'status': out = await coordinator.getPublicStatus(spec.id, spec.token); break;
        case 'complete': out = coordinator.complete(req, spec.id); break;
        case 'dismiss': out = coordinator.dismissFailed(req, spec.key); break;
        default: throw new Error(`unknown op ${spec.op}`);
        }
        if (out !== undefined) record.out = JSON.stringify(out);
    } catch (err) {
        record.err = { message: String(err.message), type: err.constructor.name };
        if (err.statusCode !== undefined) record.err.statusCode = err.statusCode;
        if (err.details !== undefined) record.err.details = JSON.stringify(err.details);
    } finally {
        global.Date = RealDate;
        global.setTimeout = realSetTimeout;
    }
    record.file = fileText();
    record.calls = calls;
    record.exit = exitDelay !== null;
    try {
        record.dir = fs.readdirSync(DATA).sort();
    } catch (err) {
        record.dir = null;
    }
    return record;
}

async function main() {
    const r = rng(20261110);
    const scenarios = [];
    let total = 0;

    function randomChange() {
        const change = { key: r.pick(keys) };
        const label = r.pick(labels);
        if (label !== undefined) change.label = label;
        change.rollback = r.chance(0.04) ? null : r.pick(rollbacks);
        return change;
    }

    // idFor picks the id a step asks for: mostly the transaction's own.
    function idFor() {
        const state = currentState();
        const own = state && typeof state.id === 'string' ? state.id : '';
        return r.chance(0.85) ? own : r.pick(['', 'nope', '00000000-0000-4000-8000-000000000000', own.toUpperCase()]);
    }

    function tokenFor() {
        const state = currentState();
        const own = state && typeof state.token === 'string' ? state.token : '';
        return r.chance(0.85) ? own : r.pick(['', 'wrong', 'undefined', 'null']);
    }

    function randomScript() {
        const errors = {};
        for (const kind of ['connection-mode', 'advanced-file', 'billing-clock', 'env-config']) {
            if (r.chance(0.2)) errors[kind] = r.pick(['EACCES: permission denied', 'not_configurable', 'backup missing']);
        }
        return {
            restart: r.pick(restartResults),
            rollbackErrors: errors,
            setConfig: r.pick([{ success: true, data: { ok: true } }, { success: true, data: { ok: true } }, { success: true, data: { ok: true } }, { success: false, error: 'config store is read-only' }, { success: false, error: '' }]),
            health: r.pick(['running', 'running', 'unreachable']),
        };
    }

    async function scenario(name, build) {
        const steps = [];
        const add = async (spec) => {
            const record = await step(spec);
            steps.push(record);
            total++;
            return record;
        };
        await add({ op: 'rmdir' });
        await add({ op: 'boot' });
        await build(add);
        scenarios.push({ name, steps });
    }

    // The straight lives of a transaction
    for (const restart of restartResults) {
        for (const health of ['running', 'unreachable']) {
            await scenario(`confirm ${JSON.stringify(restart).slice(0, 60)} ${health}`, async (add) => {
                await add({ op: 'pending', user: 1 });
                await add({ op: 'register', user: 1, change: { key: 'cdap-enabled', label: 'CDAP devices', rollback: rollbacks[0] }, script: { restart, rollbackErrors: {}, setConfig: { success: true, data: {} }, health } });
                await add({ op: 'register', user: 1, change: { key: 'connection-mode', label: 'Connection mode', rollback: rollbacks[6] } });
                await add({ op: 'register', user: 1, change: { key: 'cdap-enabled', label: 'CDAP again', rollback: rollbacks[1] } });
                await add({ op: 'pending', user: 1 });
                await add({ op: 'pending', user: 2 });
                await add({ op: 'pending', user: 0 });
                await add({ op: 'register', user: 2, change: { key: 'x', rollback: rollbacks[0] } });
                await add({ op: 'complete', user: 1, id: idFor() });
                const confirmed = await add({ op: 'confirm', user: 1, id: currentState().id });
                await add({ op: 'read' });
                const state = currentState() || {};
                await add({ op: 'status', id: state.id || '', token: state.token || '' });
                await add({ op: 'status', id: state.id || '', token: 'wrong' });
                await add({ op: 'boot' });
                await add({ op: 'status', id: state.id || '', token: state.token || '' });
                await add({ op: 'status', id: state.id || '', token: state.token || '', script: { health: 'running' } });
                await add({ op: 'cancel', user: 1, id: state.id || '' });
                await add({ op: 'dismiss', user: 2, key: '' });
                await add({ op: 'complete', user: 2, id: state.id || '' });
                if (confirmed.err || r.chance(0.5)) await add({ op: 'confirm', user: 1, id: state.id || '', script: { restart: restartResults[0] } });
                await add({ op: 'complete', user: 1, id: state.id || '' });
                await add({ op: 'pending', user: 1 });
            });
        }
    }

    for (const setConfig of [{ success: true, data: {} }, { success: false, error: 'config store is read-only' }, { success: false, error: '' }]) {
        for (const failing of [[], ['connection-mode'], ['advanced-file', 'env-config'], ['billing-clock']]) {
            await scenario(`cancel ${JSON.stringify(setConfig)} ${failing.join(',')}`, async (add) => {
                const rollbackErrors = {};
                for (const kind of failing) rollbackErrors[kind] = `${kind} cannot be undone`;
                await add({ op: 'register', user: 5, change: { key: 'a', label: 'A', rollback: rollbacks[0] }, script: { restart: restartResults[0], rollbackErrors, setConfig, health: 'running' } });
                for (const [i, rollback] of rollbacks.entries()) {
                    await add({ op: 'register', user: 5, change: { key: `key-${i}`, label: `Label ${i}`, rollback } });
                }
                await add({ op: 'cancel', user: 2, id: currentState().id });
                await add({ op: 'cancel', user: 0, id: currentState().id });
                await add({ op: 'cancel', user: 5, id: 'other' });
                await add({ op: 'cancel', user: 5, id: currentState().id });
                await add({ op: 'pending', user: 5 });
                await add({ op: 'dismiss', user: 5, key: 'nothing' });
                await add({ op: 'dismiss', user: 2, key: 'a' });
                await add({ op: 'dismiss', user: 5, key: 'a' });
                await add({ op: 'dismiss', user: 5, key: 'a' });
            });
        }
    }

    await scenario('too many changes', async (add) => {
        for (let i = 0; i < 34; i++) {
            await add({ op: 'register', user: 3, change: { key: `k${i}`, label: i % 2 ? `L${i}` : '', rollback: rollbacks[i % rollbacks.length] } });
        }
        await add({ op: 'register', user: 3, change: { key: 'k5', label: 'again', rollback: rollbacks[1] } });
        await add({ op: 'register', user: 0, change: { key: 'k', rollback: rollbacks[0] } });
        await add({ op: 'register', user: 3, change: { key: 'k', label: 'no rollback', rollback: null } });
    });

    // Files nobody wrote that way
    for (const content of rawFiles) {
        await scenario(`raw file ${JSON.stringify(content).slice(0, 40)}`, async (add) => {
            await add({ op: 'write', content, script: { restart: restartResults[0], rollbackErrors: {}, setConfig: { success: true, data: {} }, health: 'running' } });
            await add({ op: 'read' });
            await add({ op: 'pending', user: 1 });
            await add({ op: 'pending', user: 0 });
            await add({ op: 'status', id: 'x', token: '' });
            await add({ op: 'dismiss', user: 1, key: '' });
            await add({ op: 'cancel', user: 1, id: 'x' });
            await add({ op: 'confirm', user: 1, id: 'x' });
            await add({ op: 'complete', user: 1, id: 'x' });
            await add({ op: 'write', content });
            await add({ op: 'register', user: 1, change: { key: 'k', label: 'K', rollback: rollbacks[0] } });
            await add({ op: 'read' });
        });
    }

    for (let i = 0; i < 420; i++) {
        await scenario(`edited file ${i}`, async (add) => {
            if (r.chance(0.5)) {
                await add({ op: 'register', user: 1, change: randomChange(), script: randomScript() });
                await add({ op: 'register', user: 1, change: randomChange() });
            }
            const content = corruptions(r, r.chance(0.5) ? currentState() : null);
            await add({ op: 'write', content, script: randomScript() });
            const probe = r.pick(['pending', 'register', 'cancel', 'confirm', 'status', 'complete', 'dismiss']);
            const user = r.pick(users);
            if (probe === 'pending') await add({ op: 'pending', user });
            if (probe === 'register') await add({ op: 'register', user, change: randomChange() });
            if (probe === 'cancel') await add({ op: 'cancel', user, id: idFor() });
            if (probe === 'confirm') await add({ op: 'confirm', user, id: idFor() });
            if (probe === 'status') await add({ op: 'status', id: idFor(), token: tokenFor() });
            if (probe === 'complete') await add({ op: 'complete', user, id: idFor() });
            if (probe === 'dismiss') await add({ op: 'dismiss', user, key: r.pick(['', 'k', 'x', 'k3']) });
            await add({ op: 'pending', user: 1 });
            await add({ op: 'read' });
        });
    }

    // Random lives
    for (let i = 0; i < 300; i++) {
        await scenario(`random ${i}`, async (add) => {
            const n = 6 + r.int(18);
            await add({ op: 'pending', user: r.pick(users), script: randomScript() });
            for (let j = 0; j < n; j++) {
                const op = r.pick(['register', 'register', 'register', 'pending', 'cancel', 'confirm', 'confirm', 'status', 'status', 'complete', 'dismiss', 'boot', 'read']);
                const user = r.pick(users);
                const spec = { op };
                if (r.chance(0.3)) spec.script = randomScript();
                if (op === 'register') Object.assign(spec, { user, change: randomChange() });
                if (op === 'pending') spec.user = user;
                if (op === 'cancel' || op === 'confirm' || op === 'complete') Object.assign(spec, { user, id: idFor() });
                if (op === 'status') Object.assign(spec, { id: idFor(), token: tokenFor() });
                if (op === 'dismiss') Object.assign(spec, { user, key: r.pick(['', '', ...keys]) });
                await add(spec);
            }
        });
    }

    const json = JSON.stringify({ node: process.version, scenarios });
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json.gz'), zlib.gzipSync(json, { level: 9 }));
    console.log(`${scenarios.length} scenarios, ${total} steps, ${json.length} bytes of JSON on ${process.version}`);
    fs.rmSync(SCRATCH, { recursive: true, force: true });
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
