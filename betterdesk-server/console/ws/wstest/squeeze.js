'use strict';

/**
 * Makes recordings small: every run of a short piece of text repeated
 * more than 256 times becomes "\u0001RUN:<n>:<piece>\u0001", which
 * wstest.Expand turns back. The recordings of the size limits hold
 * megabytes of one letter.
 *
 *   node squeeze.js <recording.json>...
 */

const fs = require('fs');

const RUN = /([\s\S]{1,4}?)\1{256,}/g;

function squeeze(s) {
    return s.replace(RUN, (whole, piece) => `\u0001RUN:${whole.length / piece.length}:${piece}\u0001`);
}

function walk(v) {
    if (typeof v === 'string') return squeeze(v);
    if (Array.isArray(v)) return v.map(walk);
    if (v && typeof v === 'object') {
        const out = {};
        for (const [k, x] of Object.entries(v)) out[k] = walk(x);
        return out;
    }
    return v;
}

for (const file of process.argv.slice(2)) {
    const before = fs.statSync(file).size;
    const data = walk(JSON.parse(fs.readFileSync(file, 'utf8')));
    fs.writeFileSync(file, `${JSON.stringify(data, null, 1)}\n`);
    console.log(`${file}: ${before} -> ${fs.statSync(file).size} bytes`);
}
