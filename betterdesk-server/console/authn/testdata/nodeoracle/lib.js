'use strict';

// Shared by the oracle scripts: where the Node console is, a repeatable
// random source, and writing vector files.

const fs = require('fs');
const path = require('path');

const os = require('os');

const WEB = process.env.BETTERDESK_NODE_ROOT || '/repo/web-nodejs';
const OUT = process.env.AUTHN_VECTORS_DIR || path.resolve(__dirname, '..', 'vectors');

// The console's configuration reads the environment when it is loaded and
// writes a session secret into its data folder. Give it a folder of its
// own, so nothing lands in the repository.
const SCRATCH = fs.mkdtempSync(path.join(os.tmpdir(), 'authn-oracle-'));
process.env.DATA_DIR = process.env.DATA_DIR || path.join(SCRATCH, 'data');
process.env.KEYS_PATH = process.env.KEYS_PATH || path.join(SCRATCH, 'keys');
process.env.SESSION_SECRET = process.env.SESSION_SECRET || 'oracle-secret-0123456789abcdef0123456789abcdef';
process.env.LOG_LEVEL = 'info';
fs.mkdirSync(process.env.DATA_DIR, { recursive: true });
fs.mkdirSync(process.env.KEYS_PATH, { recursive: true });

function web(rel) {
    return require(path.join(WEB, rel));
}

// mulberry32: the same numbers on every run.
function prng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    const rnd = {
        next,
        int: (n) => Math.floor(next() * n),
        pick: (list) => list[Math.floor(next() * list.length)],
        bytes: (n) => {
            const b = Buffer.alloc(n);
            for (let i = 0; i < n; i++) b[i] = Math.floor(next() * 256);
            return b;
        },
        string: (alphabet, min, max) => {
            const chars = Array.from(alphabet);
            const len = min + Math.floor(next() * (max - min + 1));
            let s = '';
            for (let i = 0; i < len; i++) s += chars[Math.floor(next() * chars.length)];
            return s;
        },
    };
    return rnd;
}

function write(name, data) {
    fs.mkdirSync(OUT, { recursive: true });
    const file = path.join(OUT, name);
    fs.writeFileSync(file, JSON.stringify(data));
    const count = Array.isArray(data)
        ? data.length
        : Object.values(data).reduce((n, v) => n + (Array.isArray(v) ? v.length : 1), 0);
    console.log(`${name}: ${count} vectors, ${fs.statSync(file).size} bytes`);
}

// outcome runs fn and returns {result} or {thrown: message}.
async function outcome(fn) {
    try {
        const result = await fn();
        return { result: result === undefined ? null : result };
    } catch (err) {
        return { thrown: String(err && err.message) };
    }
}

const ALPHA = {
    lower: 'abcdefghijklmnopqrstuvwxyz',
    upper: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ',
    digit: '0123456789',
    punct: '!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~ ',
    base32: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567',
    hex: '0123456789abcdef',
    // Letters with accents, other scripts, emoji, combining marks, odd spaces
    unicode: String.fromCodePoint(0xe4, 0xf6, 0xfc, 0xdf, 0xc4, 0xd6, 0xdc, 0xe9, 0xe8, 0xea, 0xf1, 0xe7, 0xf8, 0xe5, 0xc5, 0x130, 0x131, 0x17f, 0x212a, 0xb5, 0x3a9, 0x3c9, 0x416, 0x436, 0x5d0, 0x5d1, 0x5d2, 0x62f, 0x630, 0x4f60, 0x597d, 0x65e5, 0x672c, 0x8a9e, 0xd55c, 0xae00, 0x1f600, 0x1f389, 0x1f44d, 0x1f3fd, 0x301, 0x30a, 0x200b, 0xa0),
};
ALPHA.ascii = ALPHA.lower + ALPHA.upper + ALPHA.digit + ALPHA.punct;
ALPHA.mixed = ALPHA.ascii + ALPHA.unicode;

module.exports = { WEB, OUT, SCRATCH, web, prng, write, outcome, ALPHA };
