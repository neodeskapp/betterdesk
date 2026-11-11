'use strict';

/**
 * Runs capacity tests of the Node console's services/serverAttestation.js
 * in virtual time and records everything they did. See README.md.
 *
 *   node run.js                         SQLite in a temp folder, writes ../runs_sqlite.json.gz
 *   ORACLE_PG_DSN=postgres://... node run.js
 *                                       PostgreSQL in a scratch schema, writes ../runs_postgres.json.gz
 *
 * The service is compiled with a fake clock in place of setTimeout,
 * setInterval and Date, a fake WebSocket that plays the connections a
 * scenario scripts, and scripted host readings and API statistics. The
 * settings are written to a real database.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const vm = require('vm');
const zlib = require('zlib');
const Module = require('module');
const { EventEmitter } = require('events');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'attestation-oracle-'));
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = scratch;
process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
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

const FakeTimers = require(path.join(WEB, 'node_modules/@sinonjs/fake-timers'));
const protobuf = require(path.join(WEB, 'node_modules/protobufjs'));

const SERVICE = path.join(WEB, 'services/serverAttestation.js');
const compiled = vm.runInThisContext(
    `(function (exports, require, module, __filename, __dirname, setTimeout, clearTimeout, setInterval, clearInterval, Date) {${fs.readFileSync(SERVICE, 'utf8')}\n})`,
    { filename: SERVICE },
);

const START = Date.UTC(2026, 10, 10, 9, 0, 0);
const RESULT_KEY = 'server_attestation_result';
const ENV_KEYS = ['WS_USE_TLS', 'WS_HBBS_PORT', 'WS_HBBR_PORT', 'WS_HBBS_HOST', 'WS_HBBR_HOST'];

let pendingIO = 0;
function track(promise) {
    pendingIO++;
    return promise.finally(() => { pendingIO--; });
}

/** A world is one scenario: the clock, the scripts and what happened. */
function makeWorld(scenario, realDb) {
    const clock = FakeTimers.createClock(START, 1e7);
    const world = { clock, log: [], sockets: 0, connects: {}, snapshotCalls: 0, statsCalls: 0, randomCount: 0, failSave: !!scenario.failSave };
    const at = () => clock.now - START;
    const record = (op, fields) => world.log.push({ t: at(), op, ...fields });

    const rule = (url) => {
        const list = scenario.ws[url] || scenario.ws['*'] || [{ connect: 'error', delay: 1 }];
        const n = world.connects[url] || 0;
        world.connects[url] = n + 1;
        return list[Math.min(n, list.length - 1)];
    };

    class FakeWebSocket extends EventEmitter {
        constructor(url, options) {
            super();
            this.id = ++world.sockets;
            this.closed = false;
            this.rule = rule(url);
            record('connect', { socket: this.id, url, timeout: options.handshakeTimeout, verify: options.rejectUnauthorized });
            const r = this.rule;
            if (r.connect === 'open' || r.connect === 'error') {
                clock.setTimeout(() => {
                    if (this.closed) return;
                    record(r.connect, { socket: this.id });
                    if (r.connect === 'open') this.emit('open');
                    else this.emit('error', new Error('connect ECONNREFUSED'));
                }, r.delay);
            }
        }

        send(data) {
            record('send', { socket: this.id, data: Buffer.from(data).toString('hex') });
            const r = this.rule;
            if (r.reply === 'message' || r.reply === 'error') {
                clock.setTimeout(() => {
                    if (this.closed) return;
                    record(r.reply, { socket: this.id });
                    if (r.reply === 'message') this.emit('message', Buffer.from('reply'));
                    else this.emit('error', new Error('socket hang up'));
                }, r.replyDelay);
            }
        }

        close() {
            record('close', { socket: this.id });
            this.closed = true;
        }

        terminate() {
            record('terminate', { socket: this.id });
            this.closed = true;
        }
    }

    const snapshot = () => {
        const list = scenario.snapshots;
        const s = list[Math.min(world.snapshotCalls, list.length - 1)];
        world.snapshotCalls++;
        record('snapshot', {});
        return JSON.parse(JSON.stringify(s));
    };
    const getServerStats = () => {
        const list = scenario.stats;
        const s = list[Math.min(world.statsCalls, list.length - 1)];
        world.statsCalls++;
        record('stats', {});
        if (s === 'throw') return Promise.reject(new Error('stats failed'));
        return Promise.resolve(JSON.parse(JSON.stringify(s)));
    };
    const randomBytes = (n) => {
        const out = Buffer.alloc(n);
        for (let i = 0; i < n; i++) out[i] = (world.randomCount++ * 167 + 13) & 0xff;
        return out;
    };
    const database = {
        getSetting: (key) => track(realDb.getSetting(key)),
        setSetting: (key, value) => {
            if (world.failSave) return Promise.reject(new Error('database is locked'));
            return track(realDb.setSetting(key, value));
        },
    };
    const requires = {
        ws: FakeWebSocket,
        protobufjs: { load: (file) => Promise.resolve(protobuf.loadSync(file)) },
        '../config/config': scenario.config,
        './database': database,
        './serverManagement': { getResourceSnapshot: snapshot },
        './betterdeskApi': { getServerStats },
        crypto: { ...require('crypto'), randomBytes },
    };
    const realRequire = Module.createRequire(SERVICE);
    const mod = { exports: {} };
    compiled.call(mod.exports, mod.exports, (id) => (Object.prototype.hasOwnProperty.call(requires, id) ? requires[id] : realRequire(id)),
        mod, SERVICE, path.dirname(SERVICE),
        clock.setTimeout, clock.clearTimeout, clock.setInterval, clock.clearInterval, clock.Date);
    world.service = mod.exports;
    world.at = at;
    return world;
}

/** Waits until the database work in flight is done and promises have run. */
async function settle() {
    for (let i = 0; i < 100000; i++) {
        await new Promise((resolve) => setImmediate(resolve));
        if (pendingIO === 0) {
            await new Promise((resolve) => setImmediate(resolve));
            if (pendingIO === 0) return;
        }
    }
    throw new Error('database work did not end');
}

/** Moves the clock timer by timer until done() holds. */
async function drive(world, done) {
    for (let i = 0; i < 5e6; i++) {
        await settle();
        if (done()) return;
        if (world.clock.countTimers() === 0) throw new Error('nothing left to wait for');
        world.clock.next();
    }
    throw new Error('the run did not end');
}

// --- Scenario building blocks ---

const GiB = 1024 * 1024 * 1024;
function snap(cpu, memPercent, diskPct, extra = {}) {
    return {
        cpu,
        mem: { total: 8 * GiB, percent: memPercent },
        disks: [{ size: 100e9, used: diskPct * 1e9 }],
        hostname: 'bench-host',
        cpuCount: 4,
        ...extra,
    };
}
const ok = (delay = 3, replyDelay = 2) => ({ connect: 'open', delay, reply: 'message', replyDelay });
const silent = (delay = 3) => ({ connect: 'open', delay, reply: 'silent' });
const refused = (delay = 3) => ({ connect: 'error', delay });
const hang = () => ({ connect: 'hang' });
const STATS = { success: true, data: { peers_total: 3, peers_online: 1, uptime_seconds: 812 } };
const defaultConfig = () => ({
    betterdeskApiUrl: 'http://127.0.0.1:21114/api',
    hbbsApiUrl: 'http://127.0.0.1:21114/api',
    wsProxy: { hbbsHost: 'localhost', hbbsPort: 21116, hbbrHost: 'localhost', hbbrPort: 21117 },
    allowSelfSignedCerts: false,
});
const S1 = 'ws://127.0.0.1:21118';
const S2 = 'ws://localhost:21118';
const R1 = 'ws://127.0.0.1:21119';
const R2 = 'ws://localhost:21119';
const reachable = (extra = {}) => ({ [S1]: [ok()], [R1]: [ok()], ...extra });
const rising = (from, step, n, mem = 40, disk = 30) => Array.from({ length: n }, (_, i) => snap(from + step * i, mem, disk));

function scenario(name, fields) {
    return {
        name,
        config: defaultConfig(),
        env: {},
        ws: {},
        snapshots: [snap(10, 40, 30)],
        stats: [STATS],
        runs: [{ aborts: [] }],
        ...fields,
    };
}

const scenarios = [
    scenario('unreachable-estimate', { ws: { '*': [refused()] }, snapshots: [snap(12.5, 40.2, 33.3)] }),
    scenario('unreachable-hang', { ws: { '*': [hang()] }, snapshots: [snap(20, 55.5, 10)] }),
    scenario('probe-reply-error', { ws: { '*': [{ connect: 'open', delay: 3, reply: 'error', replyDelay: 7 }] } }),
    scenario('cpu-threshold', {
        ws: reachable(),
        snapshots: [...rising(10, 9, 10), snap(60, 40, 30), snap(61, 41, 30)],
    }),
    scenario('cpu-threshold-not-calm', {
        ws: reachable(),
        snapshots: [...rising(10, 15, 6), snap(75, 40, 30), snap(82, 40, 30), snap(74, 40, 30), snap(73, 40, 30), snap(90, 40, 30)],
    }),
    scenario('cpu-threshold-with-failures', {
        ws: { [S1]: [ok(), ...Array.from({ length: 14 }, (_, i) => (i === 13 ? refused(4) : ok(4))), ok(4)], [R1]: [ok()] },
        snapshots: [snap(10, 40, 30), snap(30, 40, 30), snap(50, 40, 30), snap(85, 40, 30), snap(60, 40, 30)],
    }),
    scenario('relay-only-attempts', {
        ws: { [S1]: [refused()], [S2]: [refused()], [R1]: [ok(), refused()] },
        snapshots: [snap(10, 40, 30)],
    }),
    scenario('estimate-from-processor', {
        ws: { '*': [refused()] },
        snapshots: [snap(60, 20, 10)],
    }),
    scenario('memory-threshold', {
        ws: reachable(),
        snapshots: [snap(5, 50, 30), snap(6, 60, 30), snap(7, 79.9, 30), snap(7, 80, 30), snap(7, 72, 30), snap(7, 69.9, 30)],
    }),
    scenario('disk-threshold', {
        ws: reachable(),
        snapshots: [snap(5, 40, 50), snap(6, 40, 70), snap(6, 40, 80.04), snap(6, 40, 69)],
    }),
    scenario('disk-baseline-high', {
        ws: reachable(),
        snapshots: [snap(5, 40, 76), snap(6, 40, 95), snap(7, 40, 95), snap(85, 40, 95), snap(65, 40, 99)],
    }),
    scenario('max-connections', { ws: reachable(), snapshots: [snap(20, 30, 20)] }),
    scenario('error-rate', {
        ws: {
            [S1]: [ok(), ...Array.from({ length: 200 }, (_, i) => (i % 4 === 3 ? refused(5) : ok(4)))],
            [R1]: [ok()],
        },
        snapshots: [snap(15, 35, 25)],
    }),
    scenario('all-fail-after-probe', {
        ws: { [S1]: [ok(), refused()], [R1]: [ok(), refused()] },
        snapshots: [snap(30, 50, 40)],
    }),
    scenario('relay-silent', {
        ws: { [S1]: [ok()], [R1]: [silent()], [R2]: [silent()] },
        snapshots: [...rising(10, 20, 4), snap(50, 40, 30)],
    }),
    scenario('relay-pair-half-open', {
        ws: { [S1]: [ok()], [R1]: [ok(), ...Array.from({ length: 80 }, (_, i) => (i % 5 === 1 ? refused(9) : ok(6)))] },
        snapshots: [...rising(10, 7, 12), snap(40, 40, 30)],
    }),
    scenario('second-host', {
        ws: { [S1]: [refused()], [S2]: [ok()], [R1]: [hang()], [R2]: [ok()] },
        snapshots: [...rising(20, 20, 4), snap(55, 40, 30)],
    }),
    scenario('slow-connects', {
        ws: { [S1]: [ok(), ok(7001)], [R1]: [ok(), ok(7003)] },
        snapshots: [snap(10, 30, 20)],
    }),
    scenario('connect-timeouts', {
        ws: { [S1]: [ok(), hang()], [R1]: [ok(), hang()] },
        snapshots: [snap(10, 30, 20)],
    }),
    scenario('stats-fail', {
        ws: reachable(),
        snapshots: [...rising(40, 20, 3), snap(50, 40, 30)],
        stats: [{ success: false, error: 'connect ECONNREFUSED' }, 'throw', STATS, { success: true, data: null }, { success: true }],
    }),
    scenario('estimate-from-memory', {
        ws: { '*': [refused()] },
        snapshots: [snap(1.5, 79.5, 10, { mem: { total: 2 * GiB, percent: 79.5 } }), snap(2, 79, 10, { mem: { total: 2 * GiB, percent: 79 } })],
    }),
    scenario('estimate-without-memory-total', {
        ws: { '*': [refused()] },
        snapshots: [snap(10, 41, 10, { mem: { total: 0, percent: 41 }, cpuCount: 0 })],
    }),
    scenario('estimate-zero', {
        ws: { '*': [refused()] },
        snapshots: [snap(80, 40, 30), snap(99, 90, 30)],
    }),
    scenario('estimate-zero-after-load', {
        ws: { [S1]: [ok(), refused()], [R1]: [ok(), refused()] },
        snapshots: [snap(80, 85, 90)],
    }),
    scenario('disks', {
        ws: { '*': [refused()] },
        snapshots: [snap(10, 40, 0, { disks: [{ size: 0, used: 5 }, { size: 3e9, used: 2.5e9 }, { size: 1e12, used: 1e9 }] }),
            snap(10, 40, 0, { disks: [] }), snap(10, 40, 0, { disks: [{ size: 7, used: 1 }] })],
    }),
    scenario('environment', {
        env: { WS_USE_TLS: 'TRUE', WS_HBBS_PORT: '31116', WS_HBBR_PORT: 'abc', WS_HBBS_HOST: 'signal.test', WS_HBBR_HOST: 'relay.test' },
        config: { ...defaultConfig(), betterdeskApiUrl: 'http://[::1]:21114/api', wsProxy: { hbbsHost: 'signal.test', hbbsPort: 31116, hbbrHost: 'relay.test', hbbrPort: 21117 }, allowSelfSignedCerts: true },
        ws: { 'wss://relay.test:31118': [ok()], 'wss://[::1]:21119': [ok()] },
        snapshots: [...rising(30, 25, 3), snap(40, 40, 30)],
    }),
    scenario('environment-bad-api-url', {
        env: { WS_HBBR_HOST: 'localhost' },
        config: { ...defaultConfig(), betterdeskApiUrl: 'not a url', hbbsApiUrl: 'not a url' },
        ws: { '*': [refused()] },
    }),
    scenario('environment-no-api-url', {
        env: { WS_HBBS_HOST: '127.0.0.1', WS_USE_TLS: 'yes' },
        config: { ...defaultConfig(), betterdeskApiUrl: '', hbbsApiUrl: 'http://Relay.EXAMPLE:80/api', wsProxy: undefined },
        ws: { '*': [refused()] },
    }),
    scenario('abort-in-baseline', { ws: reachable(), runs: [{ aborts: [5037] }] }),
    scenario('abort-twice', { ws: reachable(), runs: [{ aborts: [3011, 3113, 9999] }] }),
    scenario('abort-in-probe', { ws: { '*': [hang()] }, runs: [{ aborts: [13037] }] }),
    scenario('abort-in-first-pause', {
        ws: reachable(), snapshots: [snap(10, 40, 30)], runs: [{ aborts: [10900] }],
    }),
    scenario('abort-in-ramp-pause', {
        ws: reachable(), snapshots: [snap(10, 40, 30)], runs: [{ aborts: [14437] }],
    }),
    scenario('abort-at-end-of-ramp-pause', {
        ws: reachable(), snapshots: [snap(10, 40, 30)], runs: [{ aborts: [16450] }],
    }),
    scenario('abort-while-connecting', {
        ws: { [S1]: [ok(), ok(41)], [R1]: [ok(), ok(43)] }, snapshots: [snap(10, 40, 30)], runs: [{ aborts: [19500] }],
    }),
    scenario('abort-while-connecting-first', {
        ws: { [S1]: [ok(), ok(41)], [R1]: [ok(), ok(43)] }, snapshots: [snap(10, 40, 30)], runs: [{ aborts: [10061] }],
    }),
    scenario('abort-in-stabilize', {
        ws: reachable(), snapshots: [...rising(10, 30, 3), snap(85, 40, 30)], runs: [{ aborts: [30437] }],
    }),
    scenario('abort-estimate-stabilize', {
        ws: { '*': [refused()] }, runs: [{ aborts: [12531] }],
    }),
    scenario('two-runs', {
        ws: reachable(),
        snapshots: [...rising(10, 30, 3), snap(40, 40, 30), snap(10, 40, 30)],
        runs: [{ aborts: [] }, { gap: 60000, aborts: [] }, { gap: 1000, aborts: [4011] }],
    }),
    scenario('save-fails', { ws: { '*': [refused()] }, failSave: true }),
    scenario('stored-result-before', {
        stored: JSON.stringify({ tier: 'iron', maxConnections: 30, testedAt: '2026-11-01T00:00:00.000Z' }),
        ws: { '*': [refused()] },
    }),
    scenario('stored-result-falsy', { stored: '0', ws: { '*': [refused()] }, runs: [{ aborts: [2011] }] }),
];

async function main() {
    let pool = null;
    if (PG_DSN) {
        const { Pool } = require(path.join(WEB, 'node_modules/pg'));
        const admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        await admin.end();
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
    }
    const db = require(path.join(WEB, 'services/database'));
    const quiet = { log: console.log, warn: console.warn, error: console.error };
    const logged = [];
    console.log = () => {};
    console.warn = () => {};
    console.error = (...args) => { logged.push(args.map(String).join(' ')); };
    await db.init();
    const config = require(path.join(WEB, 'config/config'));
    const { getAdapter } = require(path.join(WEB, 'services/dbAdapter'));
    const exec = async (sql, args) => {
        if (pool) {
            let n = 0;
            await pool.query(sql.replace(/\?/g, () => `$${++n}`), args);
        } else {
            getAdapter(config).getSqliteAuthDb().prepare(sql).run(...args);
        }
    };

    const records = [];
    for (const sc of scenarios) {
        for (const key of ENV_KEYS) delete process.env[key];
        for (const [key, value] of Object.entries(sc.env)) process.env[key] = value;
        await exec('DELETE FROM settings WHERE key = ?', [RESULT_KEY]);
        if (sc.stored !== undefined) await db.setSetting(RESULT_KEY, sc.stored);

        const world = makeWorld(sc, db);
        await settle();
        const svc = world.service;
        const runs = [];
        for (const plan of sc.runs) {
            if (plan.gap) {
                world.clock.tick(plan.gap);
                await settle();
            }
            const startedAt = world.at();
            const run = { start: startedAt, emits: [], aborts: [] };
            const timers = plan.aborts.map((t) => world.clock.setTimeout(() => {
                run.aborts.push({ t: world.at(), value: svc.requestAbort() });
            }, t));
            let outcome = null;
            const promise = svc.runLoadTest({
                onProgress: () => run.emits.push({ t: world.at(), status: JSON.stringify(svc.getStatus()) }),
            });
            run.statusAfterStart = JSON.stringify(svc.getStatus());
            promise.then(
                (result) => { outcome = { ok: true, result: JSON.stringify(result) }; },
                (err) => { outcome = { ok: false, error: String(err && err.message) }; },
            );
            await drive(world, () => outcome !== null);
            for (const id of timers) world.clock.clearTimeout(id);
            run.end = world.at();
            run.outcome = outcome;
            run.statusAfter = JSON.stringify(svc.getStatus());
            run.abortAfter = svc.requestAbort();
            run.row = await db.getSetting(RESULT_KEY);
            run.lastResult = JSON.stringify(await svc.getLastResult());
            runs.push(run);
        }
        records.push({
            name: sc.name, env: sc.env, config: sc.config, stored: sc.stored === undefined ? null : sc.stored,
            failSave: !!sc.failSave, ws: sc.ws, snapshots: sc.snapshots, stats: sc.stats, plans: sc.runs,
            runs, log: world.log,
        });
    }
    Object.assign(console, quiet);

    const file = path.join(OUT, `runs_${dialect}.json.gz`);
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify({ dialect, start: START, scenarios: records }), { level: 9 }));
    const nRuns = records.reduce((n, r) => n + r.runs.length, 0);
    const nLog = records.reduce((n, r) => n + r.log.length, 0);
    console.log(`${dialect}: ${records.length} scenarios, ${nRuns} runs, ${nLog} recorded events, ${logged.length} error lines, ${fs.statSync(file).size} bytes`);

    await db.close();
    if (pool) {
        await pool.query(`DROP SCHEMA ${schema} CASCADE`);
        await pool.end();
    }
    fs.rmSync(scratch, { recursive: true, force: true });
}

main().then(() => process.exit(0), (err) => { console.error(err); process.exit(1); });
