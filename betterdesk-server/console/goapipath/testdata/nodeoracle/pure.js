'use strict';

// Vectors for the functions that need no server: the path checks of
// lib/goApiPath.js and lib/goApiProxy.js, req.query as Express parses it,
// and the JavaScript functions the routes use on query values.

const { load, random, write } = require('./lib');

const { assertSafeGoApiRelativePath, assertSafeApiId } = load('./lib/goApiPath');
const { safeSegment } = load('./lib/goApiProxy');
const parseQuery = load('express/lib/utils').compileQueryParser('extended');

function attempt(fn) {
    try {
        return { ok: fn() };
    } catch (err) {
        return { error: err.message };
    }
}

// Strings whose characters are all below U+0100 stand for the bytes of a
// request; the Go test turns each character back into its byte.
function pick(next, tokens, min, max) {
    let out = '';
    const n = min + next(max - min + 1);
    for (let i = 0; i < n; i++) out += tokens[next(tokens.length)];
    return out;
}

// --- paths ---------------------------------------------------------------

function paths() {
    const next = random(1);
    const ids = [
        '', ' ', '.', '..', '...', 'a', 'device-123', '1192137448', 'peer-abc_1', '../x', 'foo/bar', 'a b', ' a ', '\ta\n',
        ' a ', '\ufeffa', ' a　', 'a\u0000', 'a%2Fb', 'a:b@c%+d', 'a..b', 'ä', 'a,b', '[object Object]',
        'a'.repeat(256), 'a'.repeat(257), ' ' + 'a'.repeat(256) + ' ', 'A.Z_0-9', '%', '+', '@', ':', '*', '~', '!', "'", '(', 'a\\b', 'a?b', 'a#b', 'a&b', 'a=b',
    ];
    const idTokens = ['a', 'Z', '0', '.', '_', ':', '@', '%', '+', '-', ' ', '/', '\\', '..', 'é', '\t', ',', '*', '~', '?'];
    for (let i = 0; i < 1500; i++) ids.push(pick(next, idTokens, 0, 6));
    const labels = ['id', 'orgId', 'deviceId', 'device_id', ''];
    const vectors = [];
    ids.forEach((value, i) => {
        const label = labels[i % labels.length];
        vectors.push({ fn: 'id', value, label, ...attempt(() => assertSafeApiId(value, label)) });
        vectors.push({ fn: 'segment', value, label, ...attempt(() => safeSegment(value, label)) });
    });

    const urls = [
        '', '/', '/peers', '/org/acme/policy', '/peers?id=1', 'http://evil.com/x', 'HTTPS://evil.com/x', '//evil.com/x', '/org/../secrets',
        'peers', '/a/./b', '/a/.', '/.', '/a..b', '/a/..', '/a?b=..', '/a?b=\n', '/a?b=\r', '/a?b=\u0000', '/a\\b', '/a\u0000', '?x', '/a?b?c=/../',
        '/a?', '/%2e%2e/x', '/a/.b', '/a/b.', 'ftp://x', '/http://x',
    ];
    const urlTokens = ['/', 'a', '.', '..', '?', '=', '\\', '\n', '\r', '\u0000', 'http://', '//', 'b', '%2e', '#', '&'];
    for (let i = 0; i < 1500; i++) urls.push(pick(next, urlTokens, 0, 7));
    urls.forEach((value) => {
        vectors.push({ fn: 'path', value, ...attempt(() => assertSafeGoApiRelativePath(value)) });
    });
    write('paths.json', vectors);
}

// --- req.query -----------------------------------------------------------

// describe gives, for the names a and b: [exists, truthy, String(value)],
// or [exists, truthy, null, message] when String(value) throws.
function describe(query) {
    return ['a', 'b'].map((name) => {
        const value = query[name];
        try {
            return [value !== undefined, !!value, String(value)];
        } catch (err) {
            return [value !== undefined, !!value, null, err.message];
        }
    });
}

function queries() {
    const next = random(2);
    const list = [
        '', 'a', 'a=', 'a=1', 'a=1&a=2', 'a=&a=', 'a[]=1', 'a[]=', 'a[]=1&a[]=2', 'a[0]=1', 'a[1]=1', 'a[3]=x&a[1]=y', 'a[x]=1',
        'a[toString]=1', 'a[x]=1&a[toString]=2', 'a[]=1&a[x][toString]=2', 'a=1&a[x]=2', 'a[x]=2&a=1', 'a[x]=2&a=1&a=3', 'a[]=1&a=2', 'a=2&a[]=1',
        'a[][]=1', 'a[0][0]=1', 'a[0][x]=1', 'a[a][b][c][d][e][f][g]=1', '[a]=1', '[a][]=1', '[]=1', '[0]=1', 'a[=1', 'a]=1', 'a[b=1', 'a[b]c=1', 'a[b]c[d]=1',
        'a%5B%5D=1', 'a%5b0%5d=1', 'a=%5B', 'a=1+2', 'a=%20x', 'a=%', 'a=%zz', 'a=%C3%A9', 'a=%E2%82%AC', 'a=%ff', 'a=%C3', 'a=%ED%A0%80', 'a=%F0%9F%98%80',
        'a=é', 'a=Ã©', 'é=1', 'a=1#b=2', 'a=1&b=2', 'b=2&a=1', 'a=1&&b=2&', '&a=1', '=1', 'a==1', 'a=1=2', 'a[]=1=2', 'a]=1=2', 'x]=a=1',
        'a[999]=1', 'a[1000]=1', 'a[1001]=1', 'a[1000]=1&a=2', 'a=2&a[1000]=1', 'a[1000]=1&a[x]=2', 'a[1000]=1&a[1001]=2', 'a[-1]=1', 'a[01]=1', 'a[1e3]=1',
        'a[12345678901234567890123]=1', 'a[9007199254740993]=1', 'a[__proto__]=1', 'a[__proto__][x]=1', '__proto__=1', 'a[constructor]=1', 'a[hasOwnProperty]=1',
        'a[valueOf]=1', 'a[toString][]=1', 'a[0]=1&a[0]=2', 'a[0][x]=1&a[0][y]=2', 'a[0]=1&a[0][y]=2', 'a[x]=1&a[x]=2', 'a[x][]=1&a[x]=2', 'a[x]=1&a[x][y]=2',
        'a[x][y]=1&a[x]=2&a[x]=3', 'a[0][0][0][0][toString]=1', 'a[0][0][0][toString]=1', 'a[x]=1&a=2&a[y]=3&a=4', 'a[x]=1&a=2&a[y]=3&[a]=4', 'a[x]=1&a=2&a[y]=3&[a]=4&a[]=5', 'a[x]=1&[a]=2', 'a[x]=1&a=2&a[y]=3&a=4&a=5',
        Array(1000).fill('a[]=1').join('&') + '&a=2', Array(999).fill('a[]=1').join('&') + '&a=2', '0=1&a=2', 'a=2&0=1', 'a=x&a[0]=y&a[5]=z', 'a.b=1', 'a[b.c]=1', 'a=1,2', 'a[]=1,2',
        Array(1000).fill('a=1').join('&'), Array(1001).fill('a=1').join('&'), Array(1002).fill('a=1').join('&'),
        Array(999).fill('x=1').join('&') + '&a=1', Array(1000).fill('x=1').join('&') + '&a=1',
        Array(1001).fill('a[]=1').join('&'), Array(600).fill('a[]=1').join('&') + '&' + Array(390).fill('a=2').join('&') + '&a[x]=1',
        Array(1000).fill(0).map((_, i) => `a[${i}]=1`).join('&'), 'b=1&' + Array(998).fill(0).map((_, i) => `a[${i}]=1`).join('&') + '&a=9&a=8',
    ];
    const tokens = [
        'a', 'a', 'a', 'b', '[', ']', '[]', '[0]', '[1]', '[2]', '[a]', '[b]', '[toString]', '=', '=', '=', '&', '&', '&', 'x', 'y', '', '%5B', '%5D', '%5b',
        '+', '%20', '%', '%zz', '%C3%A9', '%ff', '1', '0', '[1000]', '[999]', '__proto__', '.', ',', 'é', '#', 'toString', 'a[', 'a=', '&a', 'a[]=', 'a[x]=',
    ];
    for (let i = 0; i < 6000; i++) list.push(pick(next, tokens, 1, 12));
    const vectors = list.map((raw) => [raw, describe(parseQuery(raw.split('#')[0]))]);
    write('query.json', vectors);
}

// --- JavaScript functions ------------------------------------------------

function js() {
    const next = random(3);
    const texts = [
        '', '0', '-0', '+5', '5', '50abc', 'abc', ' 12', '\t\n12', ' 12', '\ufeff12', '1e3', '1.9', '-1.9', '0x1f', '0X1F', '0x', '0xg', '-0x10', '08', '0o7',
        '200', '201', '-5', '99999999999999999999', '999999999999999999999', '99999999999999999999999', '9007199254740993', '1'.repeat(400), '-' + '1'.repeat(400),
        '0x' + 'f'.repeat(300), '0x1fffffffffffff', '0x20000000000001', '0x20000000000003', 'Infinity', 'NaN', 'undefined', '5,6', '[object Object]', '12px', '1_000',
        '１２', '+', '-', '+-5', '--5', '5e400', '123456789012345678901', '1234567890123456789012', '100000000000000000000', '1000000000000000000000',
    ];
    const tokens = ['0', '1', '9', ' ', '-', '+', 'x', 'X', 'a', 'f', 'e', '.', ',', '\t', '99999999', '0x'];
    for (let i = 0; i < 2000; i++) texts.push(pick(next, tokens, 0, 8));
    const vectors = [];
    for (const text of texts) {
        const n = parseInt(text);
        vectors.push({ fn: 'parseInt', text, number: String(n), limit: String(Math.min(n || 50, 200)), offset: String(n || 0) });
    }
    const slices = ['', 'abc', 'a'.repeat(64), 'a'.repeat(65), 'a'.repeat(63) + '😀', 'a'.repeat(62) + '😀', '😀'.repeat(40), 'é'.repeat(70), 'a,b', '€'.repeat(64) + 'x'];
    const encodeTokens = ['a', 'Z', '9', ' ', '+', '*', '-', '.', '_', '~', '!', "'", '(', ')', '%', '&', '=', '/', '?', '#', 'é', '€', '😀', ',', ':', '@', '[', ']', '\n', '\u0000'];
    for (let i = 0; i < 600; i++) slices.push(pick(next, encodeTokens, 0, 80));
    for (const text of slices) {
        const cut = text.slice(0, 64);
        const qs = new URLSearchParams();
        qs.set('status', cut);
        qs.set('org id', text.slice(0, 5));
        // The cut text as it travels on: well-formed, as URLSearchParams and JSON files hold it
        vectors.push({ fn: 'slice', text, cut: cut.toWellFormed(), form: qs.toString() });
    }
    write('js.json', vectors);
}

paths();
queries();
js();
