'use strict';

// Runs the calls of calls.json through the Node console's own client code
// (services/betterdeskApi.js, serverBackend.js and authService.js) against
// a Go server it starts itself, and writes what every call returned.
//
//   node vectors.js <go server binary> <output file>
//
// Three phases: "down" with nothing listening, "up" against the server on a
// new database, and "hang" against a listener that never answers.

const fs = require('fs');
const net = require('net');
const os = require('os');
const path = require('path');
const { spawn, execFileSync } = require('child_process');

const [serverBinary, outFile] = process.argv.slice(2);
if (!serverBinary || !outFile) {
    console.error('usage: node vectors.js <go server binary> <output file>');
    process.exit(2);
}

const PORT = 21114;
const HANG_TIMEOUT_MS = 150;
const API_KEY = 'madeup-key-0123456789abcdef0123456789abcdef';
const work = fs.mkdtempSync(path.join(os.tmpdir(), 'goapi-vectors-'));
const dbFile = path.join(work, 'db.sqlite3');
fs.mkdirSync(path.join(work, 'console'));

Object.assign(process.env, {
    NODE_ENV: 'test',
    DATA_DIR: path.join(work, 'console'),
    KEYS_PATH: work,
    SESSION_SECRET: 'made-up-session-secret',
    BETTERDESK_API_URL: `http://127.0.0.1:${PORT}/api`,
    BETTERDESK_API_KEY: API_KEY,
});

const web = path.resolve(__dirname, '../../../../..', 'web-nodejs');
const config = require(path.join(web, 'config/config'));
const api = require(path.join(web, 'services/betterdeskApi'));
// loadWithInternals loads a module and also hands out functions it does
// not export: the sign-in calls to Go are internal to the auth service.
function loadWithInternals(file, names) {
    const Module = require('module');
    const source = fs.readFileSync(file, 'utf8') + `
module.exports.__internals = { ${names.join(', ')} };
`;
    const loaded = new Module(file, module);
    loaded.filename = file;
    loaded.paths = Module._nodeModulePaths(path.dirname(file));
    loaded._compile(source, file);
    return loaded.exports;
}

const modules = {
    serverBackend: require(path.join(web, 'services/serverBackend')),
    authService: loadWithInternals(path.join(web, 'services/authService.js'), ['getGoSSOStatus', 'tryGoServerAuth', 'tryLdapVerifyOnGo']),
};

const steps = JSON.parse(fs.readFileSync(path.join(__dirname, 'calls.json'), 'utf8'));

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function dig(value, dotted) {
    return dotted.split('.').reduce((v, key) => (v === null || v === undefined ? undefined : v[key]), value);
}

// substitute fills {{name}} from saved; a name not saved yet becomes 0.
function substitute(value, saved) {
    if (typeof value === 'string') {
        const whole = /^\{\{(\w+)\}\}$/.exec(value);
        if (whole) return whole[1] in saved ? saved[whole[1]] : 0;
        return value.replace(/\{\{(\w+)\}\}/g, (_, name) => (name in saved ? String(saved[name]) : '0'));
    }
    if (Array.isArray(value)) return value.map((v) => substitute(v, saved));
    if (value && typeof value === 'object') {
        return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, substitute(v, saved)]));
    }
    return value;
}

function thrownShape(e) {
    const shape = { message: e.message };
    if (e.response) {
        shape.status = e.response.status;
        shape.data = e.response.data;
    }
    return shape;
}

// proxyOIDCCallback writes to an Express response; this records what it wrote.
async function callProxyOIDCCallback(query) {
    const answer = { status: 200, headers: {} };
    const res = {
        headersSent: false,
        setHeader(name, value) { answer.headers[name.toLowerCase()] = value; },
        status(code) { answer.status = code; return res; },
        type(t) { answer.headers['content-type'] = t === 'text/plain' ? 'text/plain; charset=utf-8' : t; return res; },
        send(body) { answer.body = Buffer.isBuffer(body) ? body.toString('utf8') : String(body); res.headersSent = true; return res; },
    };
    await api.proxyOIDCCallback({ query }, res);
    return {
        status: answer.status,
        contentType: answer.headers['content-type'] || '',
        location: answer.headers.location || '',
        cacheControl: answer.headers['cache-control'] || '',
        body: answer.body,
    };
}

function resolveFunction(name) {
    if (name === 'proxyOIDCCallback') return callProxyOIDCCallback;
    const [owner, fn] = name.includes('.') ? name.split('.') : [null, name];
    const target = owner ? modules[owner] : api;
    if (typeof target[fn] !== 'function' && target.__internals && typeof target.__internals[fn] === 'function') return target.__internals[fn];
    if (typeof target[fn] !== 'function') throw new Error(`no function ${name}`);
    return target[fn].bind(target);
}

async function runPhase(phase, saved) {
    const records = [];
    for (let i = 0; i < steps.length; i++) {
        const step = steps[i];
        if (step.only && !step.only.includes(phase)) continue;
        if (phase === 'hang' && !step.hang) continue;
        const record = { step: i };
        if (step.sql) {
            if (phase !== 'up') continue;
            execFileSync('sqlite3', [dbFile, fs.readFileSync(path.join(__dirname, step.sql), 'utf8')]);
            record.sql = step.sql;
        } else if (step.http) {
            const [method, url, data] = substitute(step.http, saved);
            record.http = `${method} ${step.http[1]}`;
            try {
                const response = await api.apiClient.request({ method, url, data });
                record.result = { status: response.status, data: response.data };
            } catch (e) {
                record.thrown = thrownShape(e);
            }
        } else {
            record.fn = step.fn;
            record.args = step.args || [];
            try {
                record.result = await resolveFunction(step.fn)(...substitute(record.args, saved));
                if (record.result === undefined) record.result = null;
            } catch (e) {
                record.thrown = thrownShape(e);
            }
        }
        if (phase === 'up' && step.save) {
            for (const [name, dotted] of Object.entries(step.save)) {
                const value = dig(record.result, dotted);
                if (value !== undefined) saved[name] = value;
            }
        }
        records.push(record);
    }
    return records;
}

async function startServer() {
    const log = fs.openSync(path.join(work, 'server.log'), 'w');
    const child = spawn(serverBinary, [
        '-db', dbFile, '-key-file', path.join(work, 'id_ed25519'), '-api-port', String(PORT),
        '-init-admin-user', 'tadmin', '-init-admin-pass', 'Made-Up-Pass-1',
    ], { cwd: work, env: { ...process.env, API_KEY }, stdio: ['ignore', log, log] });
    for (let i = 0; i < 100; i++) {
        try {
            await api.apiClient.get('/health');
            return child;
        } catch (_) {
            await sleep(100);
        }
    }
    child.kill();
    throw new Error(`the Go server did not come up; see ${path.join(work, 'server.log')}`);
}

function stopServer(child) {
    return new Promise((resolve) => {
        child.once('exit', resolve);
        child.kill('SIGTERM');
    });
}

// collation compares every pair of a list of texts the way the device list
// is ordered: with localeCompare.
function collation() {
    const items = [];
    for (let c = 0x20; c < 0x7f; c++) items.push(String.fromCharCode(c));
    items.push('', '\t', '\n', '\u0001', '\u007f', 'a\u0001b');
    // The Latin letters with accents (Latin-1 and Latin Extended-A) and the
    // Romanian letters with a comma below.
    for (let c = 0xc0; c <= 0x17f; c++) items.push(String.fromCharCode(c));
    for (const c of [0x218, 0x219, 0x21a, 0x21b]) items.push(String.fromCharCode(c));
    items.push('ab', 'aB', 'Ab', 'AB', 'a b', 'a_b', 'a-b', 'a.b', 'a1', '1a', '10', '9', '09', 'host-10', 'Host-9', 'host_9',
        'resume', 'résumé', 'Resume', 'RESUME', 'strasse', 'straße', 'Strasse', 'zebra', 'Zulu', 'ärger', 'arger', 'Ärger', 'azure',
        'Öl', 'ol', 'oz', 'øl', 'année', 'annee', 'anned', 'annef', 'coop', 'co-op', 'co op', 'я', 'Я', '中', 'z',
        'łódź', 'lodz', 'lódz', 'lody', 'lodza', 'Łódź', 'żółw', 'zolw', 'zolx', 'ćma', 'cma', 'cmb', 'æon', 'aeon', 'aeoo', 'aen', 'Æon',
        'œuvre', 'oeuvre', 'oev', 'ij', 'ik', 'dz', 'tz', 'nz', 'kz', 'iz', 'qz', 'zz', 'čaj', 'caj', 'cz', 'şu', 'su', 'sz', 'İzmir', 'izmir', 'Izmir',
        '2026-11-10T01:05:50Z', '2026-11-10T01:05:50.5Z', '2026-11-10T01:05:51Z', '2020-01-01T00:00:00Z');
    // Other scripts, with mixed case, digits and Latin letters next to them.
    items.push(
        // Cyrillic
        'а', 'б', 'в', 'г', 'е', 'ж', 'и', 'й', 'А', 'Б', 'ё', 'Ё', 'ґ', 'є', 'іва', 'їжак',
        'сервер', 'Сервер', 'СЕРВЕР', 'сервер-10', 'сервер-9', 'сервер2', 'Сервер 1', 'ёлка', 'елка', 'еж', 'ёж', 'Ёж',
        'компьютер', 'Компьютер 1', 'пк', 'ПК1', 'пк2', 'ПК10',
        // Greek
        'α', 'β', 'ω', 'Α', 'Ω', 'ά', 'έ', 'σ', 'ς', 'Σ', 'άλφα', 'αλφα', 'Άλφα', 'ΑΛΦΑ', 'βήτα', 'ωμέγα',
        'υπολογιστής', 'Υπολογιστής', 'σας', 'σασ', 'γραφείο 2', 'Γραφείο 10',
        // Chinese, Japanese, Korean
        '文', '日', '本', '語', '一', '二', '三', '服务器', '服務器', '电脑', '電腦', '主机1', '主机2', '主机10',
        'サーバー', 'サーバ', 'さーばー', 'あ', 'ア', 'ぁ', 'か', 'が', 'カ', 'ガ', '서버', '컴퓨터', '가', '나', '한글', '한글2',
        // Arabic and Hebrew
        'ا', 'ب', 'ي', 'أ', 'إ', 'آ', 'ة', 'ه', 'خادم', 'حاسوب', 'خادم1', 'خادم2', 'خادم١', '١', '٢', 'مكتب', 'مَكتب',
        'א', 'ב', 'שרת', 'מחשב', 'שרת2',
        // Scripts next to each other
        'PC-сервер', 'pc-Сервер', 'Büro-ПК', 'host-中', 'host-日', '中-host', '1中', '中1', 'Ωmega', 'omega', 'αlpha', 'alpha',
        'server', 'Server2', 'я1', 'Я2', 'z1', 'ω1', '語2',
        // Digits of other scripts, and punctuation outside ASCII
        '۱', '१', '๑', '１', '𝟏', '٩', '١٠', 'a١', 'a２', 'a١b', 'a1b', 'a2',
        '·', '–', '—', '«', '»', '€', '£', '©', '°', '№', ' ', 'a–b', 'a·b', 'col·lega', 'coŀlega', 'collega');
    const rows = items.map((a) => items.map((b) => {
        const c = a.localeCompare(b);
        return c < 0 ? '<' : c > 0 ? '>' : '=';
    }).join(''));
    return { items, rows, locale: new Intl.Collator().resolvedOptions().locale };
}

// probes are observations that go into the report, not into the comparison.
async function probes() {
    const out = {};
    try {
        const r = await api.apiClient.get('/org/%2e%2e/users');
        out.encodedDotSegments = { status: r.status, path: r.request && r.request.path, kind: Array.isArray(r.data) ? 'list' : typeof r.data };
    } catch (e) {
        out.encodedDotSegments = thrownShape(e);
    }
    try {
        const r = await api.apiClient.get('/peers//status');
        out.emptySegment = { status: r.status, path: r.request && r.request.path, data: r.data };
    } catch (e) {
        out.emptySegment = { ...thrownShape(e), path: e.request && e.request.path };
    }
    out.exports = Object.keys(api).filter((k) => typeof api[k] === 'function' && k !== 'apiClient').sort();
    return out;
}

(async () => {
    const saved = {};
    const out = { node: process.version, apiTimeoutMs: config.betterdeskApiTimeout, hangTimeoutMs: HANG_TIMEOUT_MS };

    out.down = await runPhase('down', saved);

    const server = await startServer();
    try {
        out.up = await runPhase('up', saved);
        out.probes = await probes();
    } finally {
        await stopServer(server);
    }

    const sockets = new Set();
    const silent = net.createServer((socket) => {
        sockets.add(socket);
        socket.on('error', () => {});
        socket.on('close', () => sockets.delete(socket));
    });
    await new Promise((resolve) => silent.listen(PORT, '127.0.0.1', resolve));
    api.apiClient.defaults.timeout = HANG_TIMEOUT_MS;
    config.betterdeskApiTimeout = HANG_TIMEOUT_MS;
    out.hang = await runPhase('hang', saved);
    for (const socket of sockets) socket.destroy();
    await new Promise((resolve) => silent.close(resolve));

    out.collation = collation();
    fs.writeFileSync(outFile, JSON.stringify(out, null, 1));
    console.log(`vectors: ${out.down.length} down, ${out.up.length} up, ${out.hang.length} hang, ` +
        `${out.collation.items.length} texts compared; written to ${outFile}`);
    process.exit(0);
})().catch((e) => {
    console.error(e);
    process.exit(1);
});
