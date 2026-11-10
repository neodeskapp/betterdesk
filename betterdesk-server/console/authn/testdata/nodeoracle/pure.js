'use strict';

// Vectors for the parts of sign-in that need no database: password hashes
// and their strength, time-based codes, token hashes, the device request
// checks and the Redirect URL rule. Everything is computed by the Node
// console's own code and written to ../vectors.
//
//   node pure.js

const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const net = require('net');
const { WEB, web, prng, write, outcome, ALPHA } = require('./lib');

// authService needs neither database nor logger for these calls.
require.cache[require.resolve(path.join(WEB, 'services/database'))] = { exports: {}, loaded: true, id: 'db' };
const authService = web('services/authService');
const bcrypt = web('node_modules/bcrypt');
const { authenticator } = web('node_modules/otplib');
const { hashAccessToken } = web('lib/tokenHash');
const deviceAuth = web('middleware/deviceAuth');
const oidcRedirect = web('lib/oidcRedirectUrl');

const cp = (...points) => String.fromCodePoint(...points);
const unit = (...units) => String.fromCharCode(...units);
const rnd = prng(20261110);

// ---------------------------------------------------------------- hashes

function passwords() {
    const list = [
        '', 'a', 'password', 'correct horse battery staple', 'Passw0rd!', ' ', '  pw  ', '\tpw\n', 'pw\r\n',
        'a'.repeat(71), 'a'.repeat(72), 'a'.repeat(73), 'a'.repeat(80), 'a'.repeat(128), 'a'.repeat(129),
        'x'.repeat(254), 'x'.repeat(255), 'x'.repeat(256), 'x'.repeat(257), 'ab'.repeat(130), 'abcdefgh'.repeat(40).slice(0, 258),
        'q' + 'r'.repeat(254), 'k'.repeat(300), 'k'.repeat(326), 'k'.repeat(327), 'k'.repeat(511), 'k'.repeat(512),
        'abc\0def', '\0', '\0abc', 'abc\0', '\0\0\0', 'a'.repeat(71) + '\0', 'a'.repeat(72) + '\0',
        cp(0xe9), 'e' + cp(0x301), cp(0xc5), 'A' + cp(0x30a), cp(0x212b), cp(0xfb01), 'fi', cp(0xdf), 'ss', cp(0x130), 'i' + cp(0x307),
        cp(0x1f600), cp(0x1f600).repeat(18), cp(0x1f600).repeat(24), cp(0x20ac).repeat(24), cp(0x20ac).repeat(25),
        'a'.repeat(71) + cp(0xe9), 'a'.repeat(70) + cp(0x20ac), 'a'.repeat(69) + cp(0x1f600), 'a'.repeat(71) + cp(0xe9) + 'tail',
        unit(0xd800), unit(0xdc00) + 'x', 'x' + unit(0xd83d), cp(0xfffd), unit(0xd800, 0xd800),
        cp(0x4f60, 0x597d), cp(0x5d0, 0x5d1), cp(0x200b), cp(0xa0), cp(0xfeff) + 'pw', '"quoted"', "it's", 'back\\slash', '$2b$04$not', 'pbkdf2-sha256$1$aa$bb',
    ];
    for (let i = 0; i < 150; i++) list.push(rnd.string(ALPHA.ascii, 0, 40));
    for (let i = 0; i < 110; i++) list.push(rnd.string(ALPHA.mixed, 1, 60));
    for (let i = 0; i < 40; i++) list.push(rnd.string(ALPHA.mixed, 60, 140));
    return list;
}

function wrongVariants(pw) {
    const out = [pw + 'x', pw.slice(0, -1), pw.toUpperCase() === pw ? pw.toLowerCase() : pw.toUpperCase(), pw.normalize('NFD'), pw.normalize('NFC')];
    if (pw.length > 80) out.push(pw.slice(0, 72), pw.slice(0, 73));
    return [...new Set(out)].filter((v) => v !== pw);
}

async function verifyCase(group, password, hash) {
    const o = await outcome(() => authService.verifyPassword(password, hash));
    return { group, password, hash, ...o };
}

function pbkdf2Modern(pw, iterations, saltBytes, keyLength) {
    const salt = rnd.bytes(saltBytes);
    const key = crypto.pbkdf2Sync(pw, salt, iterations, keyLength, 'sha256');
    return `pbkdf2-sha256$${iterations}$${salt.toString('hex')}$${key.toString('hex')}`;
}

function pbkdf2Legacy(pw) {
    const salt = rnd.bytes(16);
    const key = crypto.pbkdf2Sync(pw, salt, 100000, 32, 'sha256');
    return `${salt.toString('hex')}:${key.toString('hex')}`;
}

async function hashVectors() {
    const verify = [];
    const pws = passwords();
    const saltOf = (minor) => {
        const s = bcrypt.genSaltSync(4, 'b');
        return minor === 'b' ? s : `$2${minor}$04$${s.slice(7)}`;
    };
    for (let i = 0; i < pws.length; i++) {
        const pw = pws[i];
        const wrong = wrongVariants(pw);
        const h2b = bcrypt.hashSync(pw, 4);
        verify.push(await verifyCase('bcrypt-2b', pw, h2b));
        for (const w of wrong.slice(0, 2)) verify.push(await verifyCase('bcrypt-2b', w, h2b));
        if (i % 3 === 0 || i < 70) {
            for (const minor of ['a', '']) {
                const h = bcrypt.hashSync(pw, saltOf(minor));
                verify.push(await verifyCase(`bcrypt-2${minor}`, pw, h));
                for (const w of wrong) verify.push(await verifyCase(`bcrypt-2${minor}`, w, h));
            }
            // The same salt and hash letters under another version
            verify.push(await verifyCase('bcrypt-relabel', pw, h2b.replace('$2b$', '$2a$')));
            verify.push(await verifyCase('bcrypt-relabel', pw, h2b.replace('$2b$', '$2$')));
            verify.push(await verifyCase('bcrypt-relabel', pw, h2b.replace('$2b$', '$2y$')));
        }
        const modern = pbkdf2Modern(pw, rnd.pick([1, 2, 1000, 4096]), 1 + rnd.int(32), rnd.pick([16, 20, 32, 64]));
        verify.push(await verifyCase('pbkdf2-modern', pw, modern));
        verify.push(await verifyCase('pbkdf2-modern', wrong[0], modern));
        if (i % 5 === 0) {
            const legacy = pbkdf2Legacy(pw);
            verify.push(await verifyCase('pbkdf2-legacy', pw, legacy));
            verify.push(await verifyCase('pbkdf2-legacy', wrong[0], legacy));
            verify.push(await verifyCase('pbkdf2-legacy', pw, legacy.toUpperCase()));
        }
    }

    // Hashes at the cost the console really uses
    for (const pw of ['admin', 'correct horse battery staple', 'a'.repeat(100), cp(0x1f600) + 'pw' + cp(0xe9), 'abc\0def', '']) {
        const h = await authService.hashPassword(pw);
        verify.push({ ...(await verifyCase('node-hashed-cost12', pw, h)), produced: true });
        verify.push(await verifyCase('node-hashed-cost12', pw + '!', h));
    }
    // The spec's fixed vectors
    verify.push(await verifyCase('fixed', 'correct horse battery staple',
        '000102030405060708090a0b0c0d0e0f:49d49c25f597846209f0d92e7770ab64e1c75e94b4ce6c509265ee67175d2a1e'));
    verify.push(await verifyCase('fixed', 'correct horse battery staple',
        'pbkdf2-sha256$600000$000102030405060708090a0b0c0d0e0f$ef177144eec9420cbc1093d2a8b344a92bc506d0d4ec9c028dd19f8324d8c1e6'));

    // Stored texts that are not, or not quite, a hash
    const pw = 'pw-for-malformed';
    const good = bcrypt.hashSync(pw, 4);
    const body = good.slice(7);
    const malformed = new Set(['', ' ', '$', '$2', '$2b', '$2b$', '$2b$04', '$2b$04$', 'plaintext', 'null', 'undefined', pw, ':', '::', 'a:b', 'a:b:c']);
    for (let n = 0; n <= good.length; n++) malformed.add(good.slice(0, n));
    for (let n = 7; n < good.length; n++) malformed.add(good.slice(0, n) + '!' + good.slice(n + 1));
    const alphabet = './ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    for (const delta of [1, 2, 3, 15, 16, 17, 31, 63]) {
        const at = alphabet.indexOf(body[21]);
        malformed.add(good.slice(0, 7) + body.slice(0, 21) + alphabet[(at + delta) % 64] + body.slice(22));
        const last = alphabet.indexOf(body[52]);
        malformed.add(good.slice(0, 7) + body.slice(0, 52) + alphabet[(last + delta) % 64]);
    }
    for (const prefix of ['$2x$', '$2y$', '$2c$', '$2B$', '$2A$', '$3b$', '$1b$', '$0b$', '$2b ', ' $2b$', '2b$', '$$2b$', '$2bb$', '$2ab$']) {
        malformed.add(prefix + good.slice(4));
    }
    for (const cost of ['00', '03', '05', '10', '31', '32', '99', '-1', '+4', ' 4', '4 ', '0x', '4$', '4', '004', 'aa', '0a', '4a']) {
        malformed.add(`$2b$${cost}$${body}`);
    }
    for (const tail of ['x', ' ', '\n', '\0', '\0garbage', '$', body, cp(0xe9)]) malformed.add(good + tail);
    malformed.add(good.slice(0, 30) + '\0' + good.slice(31));
    malformed.add('\0' + good);
    malformed.add(good.toUpperCase());
    malformed.add(good.replace(/\//g, '+'));

    const modern = pbkdf2Modern(pw, 1000, 16, 32);
    const [, iter, saltHex, hashHex] = modern.split('$');
    for (const it of ['0', '-1', '1e3', ' 1000', '+1000', '1000abc', '1000.9', '10000000', '10000001', '', 'NaN', '0x10', 'abc', '01000', cp(0xa0) + '1000', '1000 ']) {
        malformed.add(`pbkdf2-sha256$${it}$${saltHex}$${hashHex}`);
    }
    for (const variant of [
        'pbkdf2-sha256$', 'pbkdf2-sha256$$$', `pbkdf2-sha256$${iter}`, `pbkdf2-sha256$${iter}$${saltHex}`, `${modern}$`, `${modern}$extra`,
        `pbkdf2-sha256$${iter}$$${hashHex}`, `pbkdf2-sha256$${iter}$${saltHex}$`, `pbkdf2-sha256$${iter}$zz$${hashHex}`,
        `pbkdf2-sha256$${iter}$${saltHex}$zz`, `pbkdf2-sha256$${iter}$${saltHex}z$${hashHex}`, `pbkdf2-sha256$${iter}$${saltHex}0$${hashHex}`,
        `pbkdf2-sha256$${iter}$${saltHex}$${hashHex.slice(0, 2)}`, `pbkdf2-sha256$${iter}$${saltHex}$${hashHex.slice(0, 3)}`,
        `pbkdf2-sha256$${iter}$${saltHex}$${hashHex.slice(0, 16)}zz${hashHex.slice(18)}`, `pbkdf2-sha256$${iter}$${saltHex}$${hashHex}0`,
        `pbkdf2-sha256$${iter}$${saltHex}$${hashHex}00`, `pbkdf2-sha256$${iter}$${saltHex.toUpperCase()}$${hashHex.toUpperCase()}`,
        `PBKDF2-SHA256$${iter}$${saltHex}$${hashHex}`, ` ${modern}`, `${modern} `, `${modern}\n`, `pbkdf2-sha256$${iter}$${saltHex} $${hashHex}`,
        `pbkdf2-sha512$${iter}$${saltHex}$${hashHex}`,
    ]) malformed.add(variant);

    const legacy = pbkdf2Legacy(pw);
    const [lSalt, lHash] = legacy.split(':');
    for (const variant of [
        legacy.toUpperCase(), `${lSalt.slice(1)}:${lHash}`, `${lSalt}:${lHash.slice(1)}`, `${lSalt}0:${lHash}`, `${lSalt}:${lHash}0`,
        `${lSalt}:${lHash}:`, `:${lSalt}:${lHash}`, ` ${legacy}`, `${legacy} `, `${legacy}\n`, `${lSalt.slice(0, 31)}g:${lHash}`,
        `${lSalt}:${lHash.slice(0, 63)}g`, `${lSalt};${lHash}`, `${lSalt}:${lSalt}`, `${lHash}:${lHash}`, `${lSalt}:`,
    ]) malformed.add(variant);

    for (const stored of malformed) {
        for (const password of [pw, '', 'other']) verify.push(await verifyCase('malformed', password, stored));
    }
    write('hashes.json', verify);
}

// -------------------------------------------------------------- strength

function strengthVectors() {
    const inputs = ['', 'a', 'A', '1', '!', 'aA1!', 'aaaaaaaa', 'aaaaaaaaaaaa', 'Aa1!Aa1!', 'Aa1!Aa1!Aa1!', 'password', 'PASSWORD', '12345678',
        cp(0xe9).repeat(8), cp(0x1f600).repeat(4), cp(0x1f600).repeat(6), unit(0xd800).repeat(8), 'abcdefg' + cp(0x1f600), ' '.repeat(12),
        'a'.repeat(7), 'a'.repeat(11), 'a'.repeat(1000), 'aB3$' + cp(0x4f60)];
    const classes = [ALPHA.lower, ALPHA.upper, ALPHA.digit, ALPHA.punct, ALPHA.unicode];
    for (let i = 0; i < 3000; i++) {
        let alphabet = '';
        for (const c of classes) if (rnd.next() < 0.5) alphabet += c;
        if (!alphabet) alphabet = rnd.pick(classes);
        inputs.push(rnd.string(alphabet, 0, rnd.pick([3, 9, 13, 20])));
    }
    write('strength.json', inputs.map((password) => ({ password, result: authService.validatePasswordStrength(password) })));
}

// ------------------------------------------------------------------ totp

function withFixedNow(ms, fn) {
    const real = Date.now;
    Date.now = () => ms;
    try {
        return fn();
    } finally {
        Date.now = real;
    }
}

function tryValue(fn) {
    try {
        return { result: fn() };
    } catch (err) {
        return { thrown: String(err && err.message) };
    }
}

function totpVectors() {
    const secrets = ['GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', 'gezdgnbvgy3tqojqgezdgnbvgy3tqojq', 'GEZDGNBV', 'GEZD GNBV', 'GEZD-GNBV', 'GEZDGNB1',
        'A', 'AA', 'AAAAAAAAAAAAAAAA', 'AAAAAAAAAAAAAAA', 'AAAAAAAAAAAAAAAAA', 'GEZDGNBVGY3TQOJQ=', 'GEZDGNBVGY3TQOJQ====', '=GEZDGNBV', 'GE=ZDGNBV',
        '{}', '!!!!', '0189', '[]^_`', 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', ' GEZDGNBVGY3TQOJQ ', 'GEZD' + cp(0xe9), cp(0x1f600), 'GEZDGNBV\n',
        '~~~~~~~~', 'zzzzzzzz', '77777777', '22222222'];
    for (let i = 0; i < 60; i++) secrets.push(rnd.string(ALPHA.base32, 32, 32));
    for (let i = 0; i < 40; i++) secrets.push(rnd.string(ALPHA.base32, 1, 64));
    for (let i = 0; i < 20; i++) secrets.push(rnd.string(ALPHA.base32 + ALPHA.lower + ' -=0189', 1, 40));
    for (let i = 0; i < 10; i++) secrets.push(rnd.string(ALPHA.ascii, 1, 24));

    const fixedTimes = [0, 1, 29999, 30000, 59000, 1111111109000, 1234567890000, 2000000000000, 1794287700123, 4102444800000, 253402300799000];
    const codes = [];
    const checks = [];
    for (const secret of secrets) {
        const times = fixedTimes.slice(0, 5);
        for (let i = 0; i < 3; i++) {
            const step = 50000000 + rnd.int(20000000);
            times.push(step * 30000 - 1, step * 30000, step * 30000 + 1, step * 30000 + 29999, step * 30000 + rnd.int(30000));
        }
        for (const at of times) {
            codes.push({ secret, at, ...withFixedNow(at, () => tryValue(() => authenticator.generate(secret))) });
        }
    }
    for (const at of fixedTimes) {
        codes.push({ secret: secrets[0], at, ...withFixedNow(at, () => tryValue(() => authenticator.generate(secrets[0]))) });
    }

    for (const secret of secrets.slice(0, 70)) {
        const step = 50000000 + rnd.int(20000000);
        const at = step * 30000 + rnd.int(30000);
        const gen = (t) => withFixedNow(t, () => tryValue(() => authenticator.generate(secret))).result;
        const now = gen(at);
        const tokens = ['', '000000', '123456', 'abcdef', '-12345', '+12345', '12345', '1234567', ' ', '12 456'];
        if (typeof now === 'string') {
            const digits = cp(...Array.from(now).map((d) => 0xff10 + Number(d)));
            tokens.push(now, gen(at - 30000), gen(at + 30000), gen(at - 60000), ` ${now}`, `${now} `, `${now}\n`, `\t${now}`, now.slice(0, 5),
                `0${now}`, `${now}0`, now.slice(1), digits, `${now.slice(0, 3)} ${now.slice(3)}`, `${now.slice(0, 3)}-${now.slice(3)}`, `${now}\0`,
                (Number(now) + 1).toString().padStart(6, '0').slice(-6));
        }
        for (const token of tokens) {
            checks.push({ secret, at, token, ...withFixedNow(at, () => tryValue(() => authenticator.verify({ token, secret }))) });
        }
        // The edges of the step a code belongs to
        const start = step * 30000;
        const code = gen(start);
        if (typeof code === 'string') {
            for (const t of [start - 1, start, start + 1, start + 14999, start + 29999, start + 30000, start + 30001]) {
                checks.push({ secret, at: t, token: code, ...withFixedNow(t, () => tryValue(() => authenticator.verify({ token: code, secret }))) });
            }
        }
    }

    const accounts = ['admin', 'admin user', 'a@b.c', 'user+tag', 'a&b=c', 'a/b', 'a?b#c', 'a%20b', "o'neil", '(x)', '*~!', cp(0xe9, 0x74, 0xe9), cp(0x1f600),
        cp(0x4f60, 0x597d), '', ' ', 'A B', 'tab\there', 'line\nbreak', '"q"', '<x>', 'a\\b', 'a:b', 'a;b', 'a,b', 'a$b', 'a^b', 'a`b', 'a{b}', 'a|b', 'a[b]'];
    for (let i = 0; i < 280; i++) accounts.push(rnd.string(rnd.next() < 0.5 ? ALPHA.ascii : ALPHA.mixed, 0, 24));
    const urls = accounts.map((account) => {
        const secret = rnd.string(ALPHA.base32, 32, 32);
        return { account, secret, url: authService.buildTotpOtpauthUrl(account, secret) };
    });
    for (const [issuer, secret] of [['Better Desk', 'ABCDEFGH'], ['B' + cp(0xe9) + 't&r=1', 'a b+c*d~e'], ['', ''], ['x/y', "!'()"]]) {
        urls.push({ account: 'acc ount', secret, issuer, url: authService.buildTotpOtpauthUrl('acc ount', secret, issuer) });
    }

    // New secrets for fixed random bytes
    const realRandom = crypto.randomBytes;
    const generated = [];
    const patterns = [Buffer.alloc(20, 0), Buffer.alloc(20, 0xff), Buffer.alloc(20, 0x7f), Buffer.alloc(20, 0x80), Buffer.from('12345678901234567890')];
    for (let i = 0; i < 60; i++) patterns.push(rnd.bytes(20));
    for (const bytes of patterns) {
        crypto.randomBytes = (n) => bytes.subarray(0, n);
        generated.push({ random: bytes.toString('hex'), secret: authenticator.generateSecret(20) });
    }
    crypto.randomBytes = realRandom;

    write('totp.json', { codes, checks, urls, generated });
}

// ------------------------------------------------- tokens, device checks

function tokenVectors() {
    const tokens = ['', 'a', '0123456789abcdef'.repeat(4), 'a'.repeat(64), 'A'.repeat(64), cp(0xe9), cp(0x1f600), unit(0xd800), '\0', ' token ', 'token\n'];
    for (let i = 0; i < 150; i++) tokens.push(rnd.string(ALPHA.hex, 64, 64));
    for (let i = 0; i < 60; i++) tokens.push(rnd.string(ALPHA.mixed, 0, 80));
    const hashes = tokens.map((token) => ({ token, hash: hashAccessToken(token) }));

    const headers = ['Bearer abc', 'Bearer  abc', 'Bearer\tabc', 'Bearer' + cp(0xa0) + 'abc', 'Bearer abc ', 'Bearer abc def', ' Bearer abc', 'bearer abc', 'BEARER abc',
        'Bearer', 'Bearer ', 'Bearerabc', 'Basic abc', '', 'Bearer abc\n', 'Bearer \nabc', 'Bearer a,b', 'Bearer ' + cp(0xe9), 'Bearer a' + cp(0x2028) + 'b',
        'Bearer a' + cp(0x85) + 'b', 'Bearer' + cp(0x3000) + 'abc', 'Bearer' + cp(0xfeff) + 'abc', 'Bearer ' + 'x'.repeat(300), 'Bearer Bearer abc', 'Token abc'];
    for (let i = 0; i < 180; i++) headers.push(rnd.pick(['Bearer ', 'Bearer', 'Bearer  ', 'bearer ', '']) + rnd.string(ALPHA.ascii + cp(0xa0, 0xe9), 0, 30));
    const bearer = headers.map((header) => ({ header, token: deviceAuth.extractBearerToken({ headers: { authorization: header } }) }));

    const ids = ['abc', 'ab', 'a', '', 'a'.repeat(64), 'a'.repeat(65), 'device-a', 'device_a', 'device a', 'device.a', 'DEV123', '123', 'abc\n', '\nabc', cp(0xe9) + 'abc',
        'abc' + cp(0xe9), 'a-b_c', '---', '___'];
    for (let i = 0; i < 180; i++) ids.push(rnd.string(ALPHA.lower + ALPHA.upper + ALPHA.digit + '_-. ' + cp(0xe9), 0, 70));
    const deviceIds = ids.map((id) => ({ id, ok: deviceAuth.DEVICE_ID_PATTERN.test(id) }));

    // requireTokenDeviceMatch with every mix of body, query and header
    const values = [undefined, null, '', 'device-a', 'device-b', 0, 1, 123, 1.5, 1e21, false, true, [], ['device-a'], ['device-a', 'x'], [null], [['device-a']], {}, { a: 1 }, '0', 'false', ' device-a'];
    const headerValues = [undefined, '', 'device-a', 'device-b', '123', 'DEVICE-A'];
    const queryValues = [undefined, '', 'device-a', 'device-b', ['device-a'], ['device-b', 'device-a'], { x: 'device-a' }, '1'];
    const match = [];
    const run = (deviceId, body, query, header) => {
        const req = { deviceId, body: {}, query: {}, headers: {} };
        if (body !== undefined) req.body.device_id = body;
        if (query !== undefined) req.query.device_id = query;
        if (header !== undefined) req.headers['x-device-id'] = header;
        let answer = null;
        const res = { status(code) { answer = { status: code }; return this; }, json(payload) { answer.body = payload; return this; } };
        deviceAuth.requireTokenDeviceMatch(req, res, () => { answer = { next: true }; });
        const entry = { deviceId, answer };
        if (body !== undefined) entry.body = body;
        if (query !== undefined) entry.query = query;
        if (header !== undefined) entry.header = header;
        match.push(entry);
    };
    for (const body of values) for (const header of headerValues) run('device-a', body, undefined, header);
    for (const query of queryValues) for (const header of headerValues) run('device-a', undefined, query, header);
    for (let i = 0; i < 300; i++) run(rnd.pick(['device-a', '123', '1', 'true', '']), rnd.pick(values), rnd.pick(queryValues), rnd.pick(headerValues));

    write('tokens.json', { hashes, bearer, deviceIds, match });
}

// ------------------------------------------------- redirect URL, client IP

function privateIpFunction() {
    // The function is private to the route file. Its text is taken from
    // there and run as it is.
    const source = fs.readFileSync(path.join(WEB, 'routes/auth.routes.js'), 'utf8');
    const start = source.indexOf('function isPrivateClientIp(rawIp) {');
    const end = source.indexOf('\n}\n', start);
    if (start < 0 || end < 0) throw new Error('isPrivateClientIp not found in routes/auth.routes.js');
    return new Function('net', `${source.slice(start, end + 2)}\nreturn isPrivateClientIp;`)(net);
}

function oidcVectors() {
    const urls = new Set([
        'https://host:5443/api/auth/oidc/callback', 'http://host:21114/api/auth/oidc/callback', 'https://host:21121/api/oidc/callback',
        'https://host/api/auth/oidc/callback/', '', 'https://host:5443/login', 'https://host:5443/api/auth/oidc/session',
        'ftp://host/api/auth/oidc/callback', 'not a url', '   ', '\t\n',
    ]);
    const schemes = ['http', 'https', 'HTTP', 'HttPs', 'ftp', 'ws', 'wss', 'file', 'javascript', 'data', 'mailto', 'custom+x.y-z', '1http', 'ht tp', ''];
    const seps = ['://', ':', ':/', ':///', ':\\\\', ':/\\', '//', ''];
    const hosts = ['host', 'HOST.Example.COM', 'example.com.', 'localhost', '127.0.0.1', '127.1', '0x7f.1', '0177.0.0.1', '2130706433', '1.2.3.4.5', '256.1.1.1', '1.2.3.256',
        '999999999999', '0x', '1.2.3', 'a.1', '1.a', '[::1]', '[::1', '::1', '[2001:db8::1]', '[2001:DB8:0:0:0:0:0:1]', '[::ffff:1.2.3.4]', '[1:2:3:4:5:6:7:8]', '[1::2::3]',
        '[fe80::1%25eth0]', '[::]', '[]', '[1.2.3.4]', 'user@host', 'user:pass@host', 'user:p@ss@host', '@host', 'user@', ':pass@host', 'a b', 'a%20b', 'a%2eb',
        'ho%73t', 'ex%41mple.com', 'a<b', 'a>b', 'a|b', 'a^b', 'a"b', 'a{b}', 'a`b', 'a~b', 'a_b', 'a!b', "a'b", 'a(b)', 'a*b', 'a+b', 'a,b', 'a;b', 'a=b', 'a$b', 'a&b',
        'xn--bcher-kva.example', 'xn--', 'a..b', '.a', 'a.', '-a-', '', '%', '%zz', 'a%00b', 'a\tb', 'a\nb', '203.0.113.10', 'betterdesk.example.org'];
    const idn = ['b' + cp(0xfc) + 'cher.example', 'B' + cp(0xdc) + 'CHER.example', cp(0x4f8b, 0x3048) + '.' + cp(0x30c6, 0x30b9, 0x30c8), 'stra' + cp(0xdf) + 'e.de', cp(0x1f600) + '.example',
        'a' + cp(0x3002) + 'b', 'fa' + cp(0xdf) + '.de', cp(0x430, 0x431, 0x432) + '.' + cp(0x440, 0x444)];
    const ports = ['', ':5443', ':80', ':443', ':21', ':0', ':65535', ':65536', ':', ':abc', ':80a', ':-1', ':00080', ':8 0', ':99999999999999999999'];
    const paths = ['', '/', '/api/auth/oidc/callback', '/api/oidc/callback', '/api/auth/oidc/callback/', '/api/auth/oidc/callback//', '/api/auth/oidc/callback/x',
        '/API/auth/oidc/callback', '/api/auth/oidc/callback?x=1', '/api/auth/oidc/callback#f', '/api/auth/oidc/callback?x=/login#/y', '/x/../api/auth/oidc/callback',
        '/./api/auth/oidc/callback', '/api/auth/oidc/callback/..', '/api/auth/oidc/callback/.', '/api/auth/oidc/callback/%2e%2e', '/api/auth/oidc/callback/%2E',
        '/x/%2e%2E/api/oidc/callback', '\\api\\auth\\oidc\\callback', '/api/auth/oidc\\callback', '//api/auth/oidc/callback', '/api//auth/oidc/callback',
        '/api/auth/oidc/call%62ack', '/api/auth/oidc/callback%2f', '/api/auth/oidc/callback%20', '/api/auth/oidc/callback ', '/api/auth/oidc/call\tback',
        '/api/auth/oidc/call\nback', '/api/auth/oidc/callback;x', '/login', '/api/auth/oidc/session', '?/api/auth/oidc/callback', '#/api/auth/oidc/callback',
        '/' + cp(0xe9), '/api/oidc/callback/../callback', '/..', '/../..', '/api/oidc/callback/../../oidc/callback', '/a b', '/"<>`{}^|'];
    for (const host of hosts.concat(idn)) {
        for (const port of ['', ':5443']) {
            urls.add(`https://${host}${port}/api/auth/oidc/callback`);
            urls.add(`http://${host}${port}`);
            urls.add(`foo://${host}${port}/x`);
            urls.add(`file://${host}${port}/x`);
        }
    }
    for (const port of ports) for (const scheme of ['http', 'https', 'ftp', 'ws', 'foo']) urls.add(`${scheme}://host${port}/api/oidc/callback`);
    for (const p of paths) for (const scheme of ['https', 'http', 'ftp', 'foo']) urls.add(`${scheme}://host${p}`);
    for (const scheme of schemes) for (const sep of seps) urls.add(`${scheme}${sep}host/api/auth/oidc/callback`);
    for (let i = 0; i < 1500; i++) {
        const pad = rnd.pick(['', '', '', ' ', '\t', '\n', cp(0xa0), cp(0x3000), '\0', '\x1f']);
        urls.add(pad + rnd.pick(schemes) + rnd.pick(seps) + rnd.pick(hosts) + rnd.pick(ports) + rnd.pick(paths) + rnd.pick(['', '', '', ' ', '\n', cp(0xfeff)]));
    }
    for (let i = 0; i < 300; i++) urls.add(rnd.string(ALPHA.ascii, 0, 40));
    urls.add('file:');
    urls.add('file:///C:/x');
    urls.add('file://C:/x');
    urls.add('file://C|/x');
    urls.add('file:/x');
    urls.add('file:x');
    urls.add('file://localhost/x');
    urls.add('file://a b/x');

    const parsed = [];
    const suggested = [];
    for (const raw of urls) {
        const r = oidcRedirect.parseOidcRedirectUrl(raw);
        const entry = { raw, ok: r.ok, valid: oidcRedirect.isValidOidcRedirectUrl(raw) };
        if (r.ok) Object.assign(entry, { pathname: r.pathname, origin: r.url.origin });
        else entry.reason = r.reason;
        parsed.push(entry);
        suggested.push({ origin: raw, result: oidcRedirect.suggestOidcRedirectUrl(raw) });
    }
    for (const origin of ['https://203.0.113.10:5443', 'https://example.com/', 'https://example.com///', ' https://example.com ', 'https://example.com/path', 'https://EXAMPLE.com:443',
        'http://example.com:80', 'http://example.com:8080/', 'https://user:pw@example.com', 'https://[::1]:5443', 'https://[2001:DB8::0:1]', 'http://0x7f.1:5000', 'example.com', '//example.com']) {
        suggested.push({ origin, result: oidcRedirect.suggestOidcRedirectUrl(origin) });
    }

    const isPrivate = privateIpFunction();
    const ips = ['', '10.0.0.1', '10.255.255.255', '11.0.0.1', '172.15.0.1', '172.16.0.1', '172.31.255.255', '172.32.0.1', '192.168.1.1', '192.169.1.1', '127.0.0.1', '127.255.255.255',
        '128.0.0.1', '::1', '::ffff:10.0.0.1', '::FFFF:192.168.0.1', '::ffff:8.8.8.8', 'fc00::1', 'fd12:3456::1', 'FD00::1', 'fe80::1', '2001:db8::1', 'fc', 'fd', 'fc00', 'fc00::',
        'fc00::1%eth0', '::1%lo', ' 10.0.0.1 ', '10.0.0.01', '010.0.0.1', '10.0.0', '10.0.0.1.1', '10.0.0.256', '1.2.3.4', '0.0.0.0', '255.255.255.255', 'localhost', 'fc00:::1',
        '::ffff:fc00::1', '0:0:0:0:0:0:0:1', '::0001', 'fd::', 'fcab::1.2.3.4', '10.0.0.1:80', '[::1]', 'fc00::1/64', '::ffff:127.0.0.1', '::ffff:0a00:0001', '::', '1::', 'fd00::g'];
    for (let i = 0; i < 250; i++) {
        ips.push(rnd.next() < 0.6
            ? `${rnd.pick([10, 127, 172, 192, 8, 0, 255, 256, '01'])}.${rnd.pick([0, 15, 16, 31, 32, 168, 169, 255, 256])}.${rnd.int(300)}.${rnd.int(300)}`
            : rnd.pick(['', '::ffff:', '::FFFF:']) + rnd.string('fcd0123:', 1, 12));
    }
    write('oidc.json', { parsed, suggested, privateIps: ips.map((ip) => ({ ip, private: isPrivate(ip) })) });
}

// ------------------------------------------------------------- providers

function providerVectors() {
    const names = ['', 'local', 'ldap', 'oidc', 'LDAP', 'Oidc', ' ldap ', '\tOIDC\n', 'saml', 'ldaps', 'l dap', 'LOCAL', 'O' + cp(0x130) + 'DC', 'o' + cp(0x131) + 'dc', cp(0xa0) + 'ldap' + cp(0x3000),
        cp(0xfeff) + 'oidc', 'ldap' + cp(0x85), 'null', 'undefined', '0'];
    const normalize = names.map((name) => ({ name, result: authService.normalizeAuthProvider(name), external: authService.isExternalAuthProvider(name) }));
    const infer = [];
    const results = [null, {}, { role: 'viewer' }, { auth_provider: '' }, ...names.map((n) => ({ auth_provider: n, role: 'operator' }))];
    for (const go of results) {
        for (const ldap of [false, true]) {
            for (const oidc of [false, true]) {
                const sso = { ldap, oidc, any: ldap || oidc };
                infer.push({ go, sso, result: authService.inferAuthProviderFromSSO(go, sso), external: authService.isExternalAuthResult(go) });
            }
        }
    }
    write('providers.json', { normalize, infer });
}

(async () => {
    await hashVectors();
    strengthVectors();
    totpVectors();
    tokenVectors();
    oidcVectors();
    providerVectors();
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
