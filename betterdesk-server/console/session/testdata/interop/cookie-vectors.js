'use strict';

// Writes ../../cookie/testdata/parse_vectors.json: Cookie headers and what
// the Node console's cookie parser (the "cookie" package) reads from them
// for the cookie "sid". The Go parser is tested against the file.
//
//   node cookie-vectors.js

const fs = require('fs');
const path = require('path');
const cookie = require('/repo/web-nodejs/node_modules/cookie');

// A small fixed generator, so the file is the same on every run
let seed = 0x2f6e2b1;
function rnd(n) {
    seed ^= seed << 13; seed >>>= 0;
    seed ^= seed >>> 17;
    seed ^= seed << 5; seed >>>= 0;
    return seed % n;
}

const pieces = [
    'sid', 'sid', 'sid', 'a', 'b', 'SID', 'xsid', '=', '=', '=', ';', ';', ';', ' ', '\t', '"', '"', '%', '%3A', '%2B',
    '%2F', '%7C', '%C3%A4', '%E2%82%AC', '%F0%9F%98%80', '%C0%AF', '%ED%A0%80', '%E2%82', '%A4', '%zz', '%4', '+',
    's:', 'abc', '.', 'ä', 'ÿ', '\u0080', 'x', '1', ',', '|', 'j:', '{}', '\\',
];

const headers = [
    '', 's', 'sid=abc', 'a=1; sid=abc; b=2', 'sid=first; sid=second', 'sid=; sid=second', ' \tsid \t= \tabc\t ; b=2',
    'sid="abc"', 'sid=""abc""', 'sid="', 'sid="abc', 'sid=s%3Aabc.x%2By%2Fz', 'sid=a+b', 'sid=%C3%A4%E2%82%AC%F0%9F%98%80',
    'sid=abc%zz%3A', 'sid=abc%3', 'sid=%A4', 'sid=%C0%AF', 'sid=%ED%A0%80', 'sid=%E2%82', 'junk; sid=abc', 'sid=abc; junk',
    'sid=a=b=c', 'SID=abc', 'xsid=abc; sid2=def', 'a=1; sid=', 'sid=ä', 'sid=Ã%A9', 'sid = "a b" ;', ';;sid=1',
    '=x; sid=2', 'sid', 'sid;sid=3', 'a=b;sid=" "', 'sid=%F4%90%80%80', 'sid=%f0%9f%98%80', 'sid=%25', 'sid="%22"',
];
for (let i = 0; i < 1500; i++) {
    let h = '';
    const n = 1 + rnd(9);
    for (let k = 0; k < n; k++) h += pieces[rnd(pieces.length)];
    headers.push(h);
}

const vectors = headers.map((header) => {
    const parsed = cookie.parse(header);
    const has = Object.prototype.hasOwnProperty.call(parsed, 'sid');
    return { header_hex: Buffer.from(header, 'latin1').toString('hex'), value: has ? parsed.sid : null };
});

const out = path.join(__dirname, '..', '..', 'cookie', 'testdata', 'parse_vectors.json');
fs.mkdirSync(path.dirname(out), { recursive: true });
fs.writeFileSync(out, JSON.stringify(vectors, null, 0).replace(/\},\{/g, '},\n{') + '\n');
console.log(`${vectors.length} vectors written to ${out}`);
