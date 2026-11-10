'use strict';

// Shared helpers of the oracle scripts: a seeded random source, the fakes
// that stand in for the Go API client, the updater and the privileged
// helper, and the writer of the vector files.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const Module = require('module');

const WEB = process.env.ORACLE_WEB || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');

// The console's config module reads the environment when it loads. Keep it
// away from the worktree and quiet.
const SCRATCH = fs.mkdtempSync(path.join(os.tmpdir(), 'serverinfo-oracle-'));
process.env.NODE_ENV = 'test';
process.env.SESSION_SECRET = 'oracle';
process.env.DATA_DIR = path.join(SCRATCH, 'data');
for (const key of ['PUBLIC_SERVER_ID', 'PUBLIC_RELAY_SERVER', 'PUBLIC_API_URL', 'PANEL_PUBLIC_HOST',
    'BETTERDESK_API_URL', 'HBBS_API_URL', 'GO_API_PORT', 'API_PORT', 'DOCKER', 'BETTERDESK_DOCKER_LAYOUT',
    'KEYS_PATH', 'RUSTDESK_DIR', 'RUSTDESK_PATH', 'PUB_KEY_PATH', 'API_KEY_PATH']) {
    delete process.env[key];
}

function web(rel) {
    return path.join(WEB, rel);
}

// fakeModule puts an object in place of a module of the console, before
// anything requires it.
function fakeModule(rel, exports) {
    const filename = require.resolve(web(rel));
    const m = new Module(filename, null);
    m.filename = filename;
    m.loaded = true;
    m.exports = exports;
    require.cache[filename] = m;
    return exports;
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
    const r = {
        next,
        int: (n) => Math.floor(next() * n),
        pick: (list) => list[Math.floor(next() * list.length)],
        chance: (p) => next() < p,
        // text builds a string of n pieces drawn from an alphabet (an array
        // of strings).
        text: (alphabet, min, max) => {
            const n = min + Math.floor(next() * (max - min + 1));
            let s = '';
            for (let i = 0; i < n; i++) s += alphabet[Math.floor(next() * alphabet.length)];
            return s;
        },
    };
    return r;
}

// wellFormed reports whether a string has no lone surrogate. Go strings
// cannot hold one, so vectors keep to well-formed text.
function wellFormed(s) {
    return typeof s !== 'string' || s.isWellFormed();
}

function b64(text) {
    return Buffer.isBuffer(text) ? text.toString('base64') : Buffer.from(String(text), 'utf8').toString('base64');
}

// caught runs fn and returns {out} or {err: message}.
function caught(fn) {
    try {
        return { out: fn() };
    } catch (err) {
        return { err: String(err && err.message) };
    }
}

async function caughtAsync(fn) {
    try {
        return { out: await fn() };
    } catch (err) {
        return { err: String(err && err.message) };
    }
}

const counts = {};

function write(name, data) {
    const json = JSON.stringify(data);
    fs.writeFileSync(path.join(OUT, `${name}.json.gz`), zlib.gzipSync(json, { level: 9 }));
    const n = {};
    let total = 0;
    for (const [key, value] of Object.entries(data)) {
        if (Array.isArray(value)) {
            n[key] = value.length;
            total += value.length;
        }
    }
    counts[name] = { total, ...n };
    console.log(`${name}: ${total} vectors, ${json.length} bytes of JSON`);
}

function writeCounts(script) {
    const file = path.join(OUT, 'counts.json');
    let all = {};
    try {
        all = JSON.parse(fs.readFileSync(file, 'utf8'));
    } catch (_) { /* first run */ }
    Object.assign(all, counts);
    const sorted = {};
    for (const key of Object.keys(all).sort()) sorted[key] = all[key];
    fs.writeFileSync(file, JSON.stringify(sorted, null, 2) + '\n');
    console.log(`${script}: done on ${process.version}`);
}

module.exports = { WEB, OUT, SCRATCH, web, fakeModule, rng, wellFormed, b64, caught, caughtAsync, write, writeCounts };
