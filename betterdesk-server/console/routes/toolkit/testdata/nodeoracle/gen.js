'use strict';

// Runs the handlers of web-nodejs/routes/toolkit.routes.js that answer
// from the request alone (hash, password, base64, urlencode, jwt-decode)
// and writes ../text-vectors.jsonl.gz. See README.md.

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');

const repo = path.resolve(__dirname, '../../../../../..');
const web = path.join(repo, 'web-nodejs');

// The guards are not what is recorded here: let every request through.
const pass = (req, res, next) => next();
const authPath = require.resolve(path.join(web, 'middleware/auth'));
require.cache[authPath] = { id: authPath, filename: authPath, loaded: true, exports: { requireAuth: pass, requirePermission: () => pass } };

// mulberry32: the same vectors on every run.
function prng(seed) {
    let a = seed >>> 0;
    return () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}
const rand = prng(20261110);
const pick = (list) => list[Math.floor(rand() * list.length)];

// The password route's random bytes come from the generator, and each
// vector keeps the bytes its request drew.
let drawn = [];
crypto.randomBytes = (n) => {
    const b = Buffer.alloc(n);
    for (let i = 0; i < n; i++) b[i] = Math.floor(rand() * 256);
    drawn.push(b);
    return b;
};
const NOW = Date.UTC(2026, 10, 10, 12, 0, 0);
Date.now = () => NOW;

const router = require(path.join(web, 'routes/toolkit.routes'));

function call(route, bodyText) {
    return new Promise((resolve, reject) => {
        drawn = [];
        const req = { method: 'POST', url: '/api/toolkit/' + route, headers: {}, body: JSON.parse(bodyText) };
        const res = {
            statusCode: 200,
            status(code) { this.statusCode = code; return this; },
            json(value) { resolve({ route, body: bodyText, random: Buffer.concat(drawn).toString('hex'), status: this.statusCode, answer: JSON.stringify(value) }); },
        };
        router.handle(req, res, (err) => reject(err || new Error('no route for ' + route)));
    });
}

function text(pieces, max) {
    let out = '';
    const n = Math.floor(rand() * (max + 1));
    for (let i = 0; i < n; i++) out += pick(pieces);
    return out;
}
const b64url = (s) => Buffer.from(s, 'utf8').toString('base64url');

(async () => {
    const out = [];
    const send = async (route, body) => out.push(await call(route, typeof body === 'string' ? body : JSON.stringify(body)));

    // hash
    const texts = ['', 'hello', 'Grüße', '\u{1F600}', 'a\ud800b', '\udc00', ' \u0000\u007f', 'x'.repeat(1048576), 'x'.repeat(1048577), '€'.repeat(349526), '\u{1F600}'.repeat(262145)];
    for (const t of texts) {
        for (const algorithm of [undefined, 'md5', 'sha1', 'sha256', 'sha512', 'SHA256', 'sha3', 5, null, ['md5']]) {
            if (t.length > 1000 && algorithm !== undefined && algorithm !== 'md5') continue;
            await send('hash', { text: t, algorithm });
        }
    }
    for (const body of ['{}', '[]', '["x"]', '{"text":null}', '{"text":5}', '{"text":true}', '{"text":["a"]}', '{"text":{"a":1}}', '{"text":"a","text":"b"}', '{"text":"md\\u0035","algorithm":"md\\u0035"}']) await send('hash', body);

    // password: every choice of sets at every length, then odd input
    for (let sets = 0; sets < 16; sets++) {
        for (let length = 4; length <= 128; length++) {
            await send('password', { length, uppercase: !!(sets & 1), lowercase: !!(sets & 2), digits: !!(sets & 4), symbols: !!(sets & 8) });
        }
    }
    for (const length of [undefined, null, 0, 1, 3, 4, 129, 1e9, -5, 12.9, '12', ' 12px', '0x10', '', ' ', 'abc', true, [40], { n: 1 }, 1e21, '1e3', '-7', 4.999]) {
        await send('password', { length });
    }
    for (const flag of [true, false, 'true', 'false', '1', '0', 1, 0, 2, 'yes', '', null, [true], {}, 'TRUE', 1.0]) {
        await send('password', { length: 20, uppercase: flag, lowercase: flag, digits: flag, symbols: flag });
        await send('password', { length: 6, symbols: flag });
    }
    for (const body of ['[]', '[1,2,3]', '[1,2,3,4,5,6,7,8,9,10,11,12]', `[${'0,'.repeat(200)}0]`, '{"length":8,"length":9}']) await send('password', body);

    // base64
    const b64 = ['A', 'Q', 'a', 'z', '0', '9', '+', '/', '-', '_', '=', '=', ' ', '\n', '\t', '*', '.', 'Ł', '€', 'Ā', 'Ľ', '\u{1F600}', 'aGVsbG8', '4oKs', '8J-YgA', '/w', 'wyg', '4oI', 'AA'];
    for (let i = 0; i < 3000; i++) await send('base64', { text: text(b64, 14), mode: 'decode' });
    const any = ['a', 'B', ' ', 'é', '€', '\u{1F600}', '\ud800', '\udfff', '\u0000', '\n', '"', '\\', ' ', '﻿', '<', '&'];
    for (let i = 0; i < 400; i++) await send('base64', { text: text(any, 10) });
    for (const mode of [undefined, 'encode', 'decode', 'DECODE', 'x', '', 0, 1, 1.5, 1e21, -0, true, false, null, [], ['decode'], {}, { a: 1 }, 'a\ud800', ' ']) {
        await send('base64', { text: 'aGk=', mode });
        await send('urlencode', { text: 'a%20b', mode });
    }
    for (const body of ['{"text":"hi","mode":{"b":1e3,"2":[null,"x\\u2028"],"1":true,"b":-0,"c":1.50,"d":"\\ud800"}}', '{"text":"hi","mode":[1.0,2e2,-0,1e400,"\\u0041"]}', '{}', '[]', '{"text":7}']) {
        await send('base64', body);
        await send('urlencode', body);
    }
    await send('base64', { text: 'x'.repeat(1048577), mode: 'decode' });

    // urlencode
    const enc = ['%', '%4', '%41', '%C3%A9', '%c3%a9', '%E2%82%AC', '%F0%9F%98%80', '%C0%AF', '%ED%A0%80', '%ED%9F%BF', '%EF%BF%BD', '%F4%8F%BF%BF', '%F4%90%80%80', '%80', '%FF', '%C3', '%E2%82',
        'a', ' ', '+', '%2', '%zz', '%e2', '%82', '%ac', '\ud800', 'é', '%00', '%0A', '%25', '%2541', '%C3%28', '%F8%88%80%80%80'];
    for (let i = 0; i < 2500; i++) await send('urlencode', { text: text(enc, 6), mode: 'decode' });
    const plain = ['a', 'Z', '0', '-', '_', '.', '!', '~', '*', "'", '(', ')', ' ', '/', '?', '#', '&', '=', '+', '%', 'é', '€', '\u{1F600}', '\ud800', '\udc00', '\ud83d', '\ude00', '\u0000', '\u007f', ' '];
    for (let i = 0; i < 1500; i++) await send('urlencode', { text: text(plain, 8) });
    await send('urlencode', { text: 'x'.repeat(1048577) });

    // jwt-decode
    const header = '{"alg":"HS256","typ":"JWT"}';
    const exps = ['1000000000', '9999999999', '0', '-1', '1.5', '1e3', '8640000000000', '8640000000001', '-8640000000000', '-8640000000001', '1e400', '-62167219200', '-62167219201', '253402300799', '253402300800',
        '"100"', '" 100 "', '"0x10"', '"0b11"', '"0o17"', '"1e2"', '"Infinity"', '"-Infinity"', '"abc"', '""', '" "', '"12px"', '".5"', '"5."', '"+7"', '"1_000"', '"0x"', '"-0x10"', 'true', 'false', 'null',
        '[]', '[5]', '[[5]]', '[1,2]', '[null]', '["7"]', '[true]', '[{}]', '{}', '{"a":1}', '{"toString":1}', '{"valueOf":1}', '[{"toString":1}]', '1700000000.9999', '0.0004', '-0.0004', '1e-7', '123456789012'];
    for (const exp of exps) await send('jwt-decode', { token: b64url(header) + '.' + b64url(`{"sub":"x","exp":${exp}}`) + '.sig' });
    const json = ['{', '}', '[', ']', ':', ',', '"', '"a"', '"exp"', '1', '0', '-', '.', 'e', '1.5', '01', 'true', 'false', 'null', 'tru', ' ', '\n', '\t', '\\', '\\u00', '\\u0041', '\\n', '\\x', "'", 'NaN', 'undefined', 'é', '\u0000',
        '{"exp":5}', '{"a":[1,2,{"b":null}]}', '"😀"', '1e5', '-0', '1E+2', '2e-7', '1e21', '123456789012345678901234567890', '0.1', '{"10":1,"b":2,"2":3,"b":4}', '{"__proto__":{"exp":1}}', '﻿', '/'];
    for (let i = 0; i < 3000; i++) {
        const head = rand() < 0.5 ? header : text(json, 5);
        const payload = text(json, 6);
        const parts = [b64url(head), b64url(payload)];
        if (rand() < 0.5) parts.push('sig');
        if (rand() < 0.05) parts.push('more');
        await send('jwt-decode', { token: parts.join('.') });
    }
    // Parts that are no clean base64, and tokens of other shapes
    for (let i = 0; i < 600; i++) await send('jwt-decode', { token: text(['e30', 'eyJhIjoxfQ', 'bnVsbA', 'NDI', 'IngI', '.', '.', '=', '-', '_', '+', '/', ' ', 'Ł', 'W10', 'eyJleHAiOjF9', 'e', 'x'], 6) });
    for (const body of ['{}', '[]', '{"token":""}', '{"token":5}', '{"token":null}', '{"token":["a.b"]}', '{"token":"abc"}', '{"token":"."}', '{"token":".."}', '{"token":"..."}']) await send('jwt-decode', body);

    const counts = {};
    for (const v of out) counts[v.route] = (counts[v.route] || 0) + 1;
    fs.writeFileSync(path.join(__dirname, '..', 'text-vectors.jsonl.gz'), zlib.gzipSync(out.map((v) => JSON.stringify(v)).join('\n') + '\n', { level: 9 }));
    console.log(JSON.stringify(counts), out.length, 'vectors, clock at', new Date(NOW).toISOString());
})().catch((err) => { console.error(err); process.exit(1); });
