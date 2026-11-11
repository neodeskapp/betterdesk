'use strict';

// Runs services/advancedConfigService.js of the Node console through
// scenarios on a scratch folder and writes ../vectors.json.gz: for every
// step what the function returned or threw, and the files of the scratch
// folder afterwards (type, mode, content).
//
// The service takes its paths from its own folder and from config.js, so
// each scenario gets a copy of the real module in <root>/web-nodejs/services
// with a config.js that points the data and keys folders into <root>. The
// clock moves one second per new Date(), from 2026-01-01T00:00:00.000Z, so
// the names of the backups are known.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');

const WEB = process.env.ORACLE_WEB || '/repo/web-nodejs';
const SOURCE = fs.readFileSync(path.join(WEB, 'services/advancedConfigService.js'), 'utf8');
const SCRATCH = fs.mkdtempSync(path.join(os.tmpdir(), 'advancedconfig-oracle-'));

const RealDate = Date;
let tick = 0;
class StepDate extends RealDate {
    constructor(...args) {
        if (args.length) super(...args);
        else super(RealDate.UTC(2026, 0, 1) + 1000 * tick++);
    }
}

const MAX = 512 * 1024;
const ISO = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/;

// content builds file bytes from a spec: a string, or {repeat, n},
// {bytes: [...]}.
function content(spec) {
    if (typeof spec === 'string') return Buffer.from(spec, 'utf8');
    if (spec.repeat !== undefined) return Buffer.from(spec.repeat.repeat(spec.n), 'utf8');
    if (spec.bytes) return Buffer.from(spec.bytes);
    throw new Error('bad content spec');
}

// value turns the JSON of a scenario into the value a route would pass:
// {undefined: true} is undefined, {repeat, n} a long string.
function value(spec) {
    if (spec && typeof spec === 'object' && !Array.isArray(spec)) {
        if (spec.undefined) return undefined;
        if (spec.repeat !== undefined) return spec.repeat.repeat(spec.n);
        if (spec.root !== undefined) return currentRoot + spec.root;
    }
    return spec;
}

let currentRoot = '';

function setup(root, steps) {
    for (const [op, rel, arg, mode] of steps) {
        const p = path.join(root, rel);
        if (op === 'dir') {
            fs.mkdirSync(p, { recursive: true });
        } else if (op === 'file') {
            fs.mkdirSync(path.dirname(p), { recursive: true });
            fs.writeFileSync(p, content(arg));
            if (mode !== undefined) fs.chmodSync(p, mode);
        } else if (op === 'symlink') {
            fs.mkdirSync(path.dirname(p), { recursive: true });
            fs.symlinkSync(arg.startsWith('/') ? arg : path.join(root, arg), p);
        } else {
            throw new Error(`unknown setup ${op}`);
        }
    }
}

// tree lists the scratch folder below root, without the module's copy.
function tree(root) {
    const out = [];
    const walk = (dir) => {
        for (const name of fs.readdirSync(dir).sort()) {
            const p = path.join(dir, name);
            const rel = path.relative(root, p);
            if (rel === 'web-nodejs/services' || rel === 'web-nodejs/config') continue;
            const st = fs.lstatSync(p);
            const entry = { path: rel, mode: (st.mode & 0o777).toString(8) };
            if (st.isSymbolicLink()) {
                entry.type = 'symlink';
                entry.target = fs.readlinkSync(p).split(root).join('<root>');
                delete entry.mode;
            } else if (st.isDirectory()) {
                entry.type = 'dir';
                out.push(entry);
                walk(p);
                continue;
            } else {
                entry.type = 'file';
                const buf = fs.readFileSync(p);
                entry.size = buf.length;
                entry.sha256 = crypto.createHash('sha256').update(buf).digest('hex');
            }
            out.push(entry);
        }
    };
    walk(root);
    return out;
}

// clean replaces the root with <root> and checks and hides the times.
function clean(v, root) {
    if (typeof v === 'string') return v.split(root).join('<root>');
    if (Array.isArray(v)) return v.map((x) => clean(x, root));
    if (v && typeof v === 'object') {
        const out = {};
        for (const [k, x] of Object.entries(v)) {
            if (k === 'mtime' && typeof x === 'string') {
                if (!ISO.test(x)) throw new Error(`mtime ${x}`);
                out[k] = '<mtime>';
            } else {
                out[k] = clean(x, root);
            }
        }
        return out;
    }
    return v;
}

const CONFIG_STUB = `module.exports = global.__advancedOracleConfig;\n`;

async function runScenario(index, sc) {
    const root = path.join(SCRATCH, `s${index}`);
    currentRoot = root;
    fs.mkdirSync(path.join(root, 'web-nodejs', 'services'), { recursive: true });
    fs.mkdirSync(path.join(root, 'web-nodejs', 'config'), { recursive: true });
    fs.writeFileSync(path.join(root, 'web-nodejs', 'services', 'advancedConfigService.js'), SOURCE);
    fs.writeFileSync(path.join(root, 'web-nodejs', 'services', 'updateService.js'), 'module.exports = {};\n');
    fs.writeFileSync(path.join(root, 'web-nodejs', 'config', 'config.js'), CONFIG_STUB);
    global.__advancedOracleConfig = {
        dataDir: path.join(root, 'data'),
        keysPath: path.join(root, 'keys'),
        rustdeskDir: path.join(root, 'keys'),
    };
    delete process.env.AUDIT_LOG_FILE;
    delete process.env.BETTERDESK_BUILD_ENV_FILE;
    for (const [k, v] of Object.entries(sc.env || {})) process.env[k] = v.split('<root>').join(root);
    setup(root, sc.setup || []);
    const service = require(path.join(root, 'web-nodejs', 'services', 'advancedConfigService.js'));

    tick = 0;
    global.Date = StepDate;
    const steps = [];
    try {
        for (const step of sc.steps) {
            const [op, ...args] = step;
            const out = { step };
            try {
                let result;
                if (op === 'list') result = await service.listFiles();
                else if (op === 'read') result = await service.readFile(value(args[0]));
                else if (op === 'write') result = await service.writeFile(value(args[0]), value(args[1]));
                else if (op === 'restore') result = await service.restoreBackup(value(args[0]), value(args[1]), value(args[2]));
                else throw new Error(`unknown step ${op}`);
                out.result = clean(result, root);
            } catch (err) {
                out.error = clean(err.message, root);
            }
            out.tree = tree(root);
            steps.push(out);
        }
    } finally {
        global.Date = RealDate;
    }
    return { name: sc.name, env: sc.env || {}, setup: sc.setup || [], steps };
}

const ids = ['console-env', 'console-env-local', 'console-session-secret', 'go-blocklist', 'go-audit-log',
    'systemd-console', 'systemd-server', 'build-env', 'docker-supervisord', 'docker-compose'];
const odd = ['', 'unknown', 'CONSOLE-ENV', ' console-env', 'console-env ', '__proto__', 'constructor', 'toString',
    '../console-env', 'console-env/..', 5, null, { undefined: true }, ['console-env'], { id: 'console-env' }, true];

const scenarios = [];

scenarios.push({
    name: 'fresh install',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env' },
    steps: [['list'], ...ids.map((id) => ['read', id]), ...odd.map((id) => ['read', id])],
});

scenarios.push({
    name: 'files of every kind',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env', AUDIT_LOG_FILE: 'relative/audit.jsonl' },
    setup: [
        ['file', 'web-nodejs/.env', 'PORT=5000\nSECRET=abc\n', 0o600],
        ['dir', 'web-nodejs/.env.local'],
        ['file', 'data/.session_secret', { bytes: [0x61, 0x00, 0x62] }],
        ['file', 'keys/blocklist.txt', { repeat: 'a', n: MAX + 1 }],
        ['file', 'keys/audit.jsonl', { repeat: 'b', n: MAX }],
        ['file', 'etc/build.env', { bytes: [0x41, 0xe2, 0x82, 0x41, 0xc0, 0xaf, 0xed, 0xa0, 0x80, 0xf0, 0x9f, 0x98, 0x80, 0xff, 0x0a, 0x08, 0x0c, 0x1b, 0x22, 0x5c, 0xe2, 0x80, 0xa8] }],
        ['file', 'docker/supervisord.conf', '[supervisord]\nnodaemon=true\n', 0o644],
        ['symlink', 'docker-compose.yml', 'docker/supervisord.conf'],
    ],
    steps: [['list'], ...ids.map((id) => ['read', id])],
});

scenarios.push({
    name: 'absolute audit log and a broken link',
    env: { AUDIT_LOG_FILE: '<root>/logs/audit.jsonl', BETTERDESK_BUILD_ENV_FILE: '<root>/nowhere/../etc/./build.env' },
    setup: [
        ['file', 'logs/audit.jsonl', '{"a":1}\n'],
        ['symlink', 'docker-compose.yml', 'missing-target'],
        ['file', 'etc/build.env', 'X=1\r\nY="2"\n'],
    ],
    steps: [['list'], ['read', 'go-audit-log'], ['read', 'docker-compose'], ['read', 'build-env'],
        ['write', 'docker-compose', 'x'], ['write', 'build-env', 'Z=3\n'], ['list']],
});

scenarios.push({
    name: 'a path through a file',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env/inner' },
    setup: [['file', 'etc/build.env', 'not a folder\n'], ['file', 'keys', 'not a folder\n']],
    steps: [['list'], ['read', 'build-env'], ['read', 'go-blocklist'], ['write', 'build-env', 'x'], ['write', 'go-blocklist', 'x'],
        ['write', 'console-session-secret', 'created with its folder\n']],
});

const contents = ['', 'a', 'A=1\nB=2\n', 'ünïcödé ✓ 😀\n', 'line\u2028sep\u2029x', 'nul\u0000inside', '\ufeffbom\n',
    5, 0, null, true, false, { undefined: true }, [], ['a'], {}, { a: 1 },
    { repeat: 'a', n: MAX }, { repeat: 'a', n: MAX + 1 }, { repeat: 'é', n: MAX / 2 }, { repeat: 'é', n: MAX / 2 + 1 },
    { repeat: '😀', n: MAX / 4 }, { repeat: '😀', n: MAX / 4 + 1 }];

scenarios.push({
    name: 'write every kind of content',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env' },
    steps: contents.flatMap((c) => [['write', 'console-session-secret', c], ['read', 'console-session-secret']]),
});

scenarios.push({
    name: 'write to every id',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env', AUDIT_LOG_FILE: '<root>/logs/audit.jsonl' },
    setup: [['file', 'web-nodejs/.env', 'A=1\n', 0o640], ['file', 'docker-compose.yml', 'services: {}\n', 0o644]],
    steps: [...ids.map((id) => ['write', id, `content of ${id}\n`]), ...odd.map((id) => ['write', id, 'x']), ['list'],
        ...ids.map((id) => ['write', id, `second content of ${id}\n`]), ['list']],
});

scenarios.push({
    name: 'write over what is not a file',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env' },
    setup: [['dir', 'data/.session_secret'], ['file', 'keys/blocklist.txt', { repeat: 'z', n: MAX + 1 }],
        ['file', 'target.txt', 'linked\n', 0o604], ['symlink', 'web-nodejs/.env', 'target.txt']],
    steps: [['write', 'console-session-secret', 'x'], ['write', 'go-blocklist', 'x'], ['write', 'go-blocklist', { undefined: true }],
        ['write', 'console-env', 'through the link\n'], ['read', 'console-env'], ['list']],
});

scenarios.push({
    name: 'restore backups',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env' },
    setup: [['file', 'web-nodejs/.env', 'first\n', 0o640]],
    steps: [
        ['write', 'console-env', 'second\n'],
        ['write', 'console-env', 'third\n'],
        ['restore', 'console-env', { root: '/web-nodejs/.env.bak.2026-01-01T00-00-00-000Z' }],
        ['read', 'console-env'],
        ['restore', 'console-env', { root: '/web-nodejs/.env.bak.2026-01-01T00-00-01-000Z' }],
        ['read', 'console-env'],
        ['restore', 'console-env', { root: '/web-nodejs/.env.bak.missing' }],
        ['restore', 'console-env', { root: '/web-nodejs/.env.bak.' }],
        ['restore', 'console-env', { root: '/web-nodejs/.env.bak.x/../.env.bak.2026-01-01T00-00-00-000Z' }],
        ['restore', 'console-env', { root: '/web-nodejs//.env.bak.2026-01-01T00-00-00-000Z' }],
        ['restore', 'console-env', { root: '/web-nodejs/./.env.bak.2026-01-01T00-00-00-000Z' }],
        ['restore', 'console-env', { root: '/web-nodejs/.env.bak.2026-01-01T00-00-00-000Z/' }],
        ['restore', 'console-env', { root: '/web-nodejs/.envX.bak.2026-01-01T00-00-00-000Z' }],
        ['restore', 'console-env', 'web-nodejs/.env.bak.2026-01-01T00-00-00-000Z'],
        ['restore', 'console-env', { root: '/data/.session_secret.bak.2026' }],
        ['restore', 'console-env', null],
        ['restore', 'console-env', { undefined: true }],
        ['restore', 'console-env', 5],
        ['restore', 'console-env', ['x']],
        ['restore', 'console-env-local', { root: '/web-nodejs/.env.bak.2026-01-01T00-00-00-000Z' }],
        ['restore', 'unknown', { root: '/web-nodejs/.env.bak.2026-01-01T00-00-00-000Z' }],
        ['restore', null, null],
        ['restore', 'systemd-server', '/etc/systemd/system/betterdesk-server.service.bak.x'],
    ],
});

scenarios.push({
    name: 'restore what is odd',
    env: { BETTERDESK_BUILD_ENV_FILE: '<root>/etc/build.env' },
    setup: [['file', 'keys/blocklist.txt', 'now\n', 0o600], ['dir', 'keys/blocklist.txt.bak.dir'],
        ['file', 'keys/blocklist.txt.bak.ro', 'old\n', 0o444], ['file', 'data/.session_secret', 'kept\n']],
    steps: [
        ['restore', 'go-blocklist', { root: '/keys/blocklist.txt.bak.dir' }],
        ['restore', 'go-blocklist', { root: '/keys/blocklist.txt.bak.ro' }],
        ['read', 'go-blocklist'],
        ['restore', 'console-session-secret', null, true],
        ['restore', 'console-session-secret', null, true],
        ['restore', 'console-session-secret', 'anything', 'yes'],
        ['restore', 'console-env', null, true],
        ['restore', 'unknown', null, true],
        ['list'],
    ],
});

(async () => {
    const out = [];
    for (let i = 0; i < scenarios.length; i++) out.push(await runScenario(i, scenarios[i]));
    const file = path.join(__dirname, '..', 'vectors.json.gz');
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(out, null, 1)));
    const count = out.reduce((n, s) => n + s.steps.length, 0);
    console.log(`wrote ${out.length} scenarios with ${count} steps to ${file}`);
    fs.rmSync(SCRATCH, { recursive: true, force: true });
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
