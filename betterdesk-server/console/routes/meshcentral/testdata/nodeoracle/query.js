'use strict';

// Writes ../query.json.gz: what the Node console's mesh routes made of a
// query string. See README.md.

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const consoleDir = process.argv[2] || '/repo/web-nodejs';
const { compileQueryParser } = require(path.join(consoleDir, 'node_modules/express/lib/utils'));
const parse = compileQueryParser('extended');

function attempt(fn) {
    try {
        return fn();
    } catch (err) {
        return { throws: err.message };
    }
}

// One vector: the expressions of routes/meshcentral.routes.js on req.query.
function run(raw) {
    const query = parse(raw);
    const out = { q: raw };
    out.search = attempt(() => new URLSearchParams(query).toString());
    out.share = attempt(() => String(query.mesh_share || '').trim());
    out.guest = attempt(() => String(query.guest || query.t || '').trim());
    out.viewOnly = !!query.view_only;
    const watching = parse(raw);
    if (!watching.view_only) watching.view_only = '1';
    out.watch = attempt(() => new URLSearchParams(watching).toString());
    return out;
}

// A small generator with a fixed seed, so the file is the same on every run.
let seed = 0x2f6e2b1;
function random(n) {
    seed ^= seed << 13; seed >>>= 0;
    seed ^= seed >>> 17;
    seed ^= seed << 5; seed >>>= 0;
    return seed % n;
}
const pick = (list) => list[random(list.length)];

const names = ['a', 'b', 'mesh_share', 'guest', 't', 'view_only', 'name', 'mesh_id', 'toString', 'valueOf',
    '__proto__', 'constructor', 'hasOwnProperty', '0', '1', '2', '10', '999', '1000', '1001', '01', '-1', '1e3',
    '4294967294', '4294967295', '', ' a', 'a b', 'a.b', 'A', 'é', '%C3%A9', 'a%5Bb%5D', '%61'];
const brackets = ['', '', '', '', '[]', '[0]', '[1]', '[2]', '[x]', '[y]', '[toString]', '[valueOf]', '[a][b]', '[][]',
    '[0][0]', '[x][]', '[][x]', '[999]', '[1000]', '[1001]', '[20]', '[21]', '[-1]', '[01]', '[1e3]', '[', ']', '[a',
    'a]', '[a]b', '[[a]]', '[a][b][c][d][e][f][g]', '[a][b][c][d][e][f', '%5B%5D', '%5Bx%5D', '%5b0%5d',
    '[__proto__]', '[__proto__][x]', '[constructor]', '[length]', '[]x', '[ ]', '[x]=[y]', '[%5D]'];
const values = ['', '', '1', '1', 'x', 'abc', 'a b', 'a+b', '%20', '%', '%zz', '%C3%A9', '%E9', '%ED%A0%80', 'é', '€',
    '\u{1F600}', 'a,b', "~*'()!", '/?&=', ' x ', '%09x%0A', '%EF%BB%BFx', '%00', '=', '==', 'a=b', '[x]', '[]', '%5B%5D',
    '0', 'false', 'null', 'undefined', '[object Object]', '%2B', '%26', '%3D', '%25', '-._*', ';', ':@$', '"<>`{}|\\^'];

function pair() {
    const key = pick(names) + pick(brackets) + (random(12) === 0 ? pick(brackets) : '');
    switch (random(10)) {
        case 0: return key;
        default: return key + '=' + pick(values);
    }
}

const fixed = [
    '', '&', '&&', '=', '=x', '?', '?a=1', 'a', 'a=', 'a=1', 'a=1&a=2', 'a=1&a=2&a=3', 'a[]=1', 'a[]=1&a[]=2', 'a[]=',
    'a[]', 'a[0]=1', 'a[1]=1', 'a[1]=x&a[0]=y', 'a[x]=1', 'a[x]=1&a[y]=2', 'a[x][y]=1', 'a=1&a[x]=2', 'a[x]=2&a=1',
    'a[]=1&a[x]=2', 'a[x]=2&a[]=1', 'a[toString]=x', 'a[]=1&a[toString]=x', 'a[][toString]=x', 'a[x][toString]=1',
    'a[valueOf]=x', 'toString=x', 'toString[x]=1', '__proto__=x', '__proto__[x]=1', 'a[__proto__]=x',
    'constructor=x', 'constructor[prototype][x]=1', 'hasOwnProperty=x', 'length=3',
    'mesh_share=abc', 'mesh_share=%20abc%20', 'mesh_share=&guest=abc', 'guest=&t=abc', 'guest=abc&t=def', 't=abc',
    'mesh_share[]=abc', 'mesh_share[]=', 'mesh_share[]=&mesh_share[]=', 'mesh_share=a&mesh_share=b',
    'mesh_share[x]=abc', 'mesh_share[toString]=x', 'guest[toString]=x', 't[toString]=x', 'guest=a&t[toString]=x',
    'guest[]=&t=abc', 'guest[]=%20&t=abc', 'mesh_share=%C2%A0%E2%80%83abc%EF%BB%BF', 'mesh_share=%09%0A%0B%0C%0Dx',
    'view_only=1', 'view_only=', 'view_only=0', 'view_only=false', 'view_only[]=', 'view_only[x]=1',
    'guest=abc&view_only=&relay_base=/x', 'view_only=&guest=abc', '1=a&guest=abc&0=b', 'b=1&a=2&10=x&9=y&01=z',
    'name=Contract+Mesh&mesh_server=wss%3A%2F%2Fexample.test%2Fagent.ashx', 'name=a%20b', 'name=a+b', 'name=a%2Bb',
    "name=~*'()!-._", 'name=%E9', 'name=%C3%A9', 'name=é', 'name=%', 'name=%zz', 'name=%ED%A0%80', 'name=€',
    'a=1;b=2', 'a%5B%5D=1', 'a%5bx%5d=1', 'a%255B%255D=1', '[a]=1', '[]=1', '[]=1&[]=2', '[0]=1', '[2]=x', '[x]=1&x=2',
    '0=a', '0=a&[0]=b', '[0]=a&0=b', 'a[b]c=1', 'a[b]c[d]=1', 'a]=1', 'a[=1', 'a[[b]]=1', 'a[b[c]]=1', 'a[b]]=1',
    'a[a][b][c][d][e][f][g][h]=1', 'a.b=1', 'a[1000]=x', 'a[1001]=x', 'a[999]=x&a[]=y', 'a[1001]=x&a[]=y',
    'a[1001]=x&a=y', 'a=y&a[1001]=x', 'a[2]=x&a[5]=y', 'a[2]=x&a[2]=y', 'a[0]=x&a[0][b]=y', 'a[0][b]=y&a[0]=x',
    'a[b]=1&a[b][c]=2', 'a[b][c]=2&a[b]=1', 'a=1&a=2&a[x]=3&a=4', 'a[x]=3&a=1&a=2', 'a[]=1&a[]=2&a[5]=3&a=4',
    'a[-1]=x', 'a[01]=x', 'a[1e3]=x', 'a[ 1]=x', 'a[4294967295]=x', 'a[10]=x&a[9]=y&a[b]=z',
    'a[0][0][0][0][toString]=1', 'a[0][0][0][0][0][toString]=1', 'a[0][0][0][toString]=1', 'a[][][][][toString]=1',
    'a[][][][][][toString]=1', 'a%5D=b]=c', 'a%5Bx%5D=1]=2', 'a%5d=b]=c&a]=d', 'x%5B=1&x[=2',
    'x=' + 'y'.repeat(3000),
    Array.from({ length: 1005 }, (_, i) => `k${i}=${i}`).join('&'),
    Array.from({ length: 1005 }, (_, i) => `a=${i}`).join('&'),
    Array.from({ length: 1005 }, (_, i) => `a[]=${i}`).join('&'),
    Array.from({ length: 1002 }, (_, i) => `a[]=${i}`).join('&') + '&a=z&a[]=q&a[x]=1',
    Array.from({ length: 30 }, (_, i) => `a[${i * 40}]=${i}`).join('&'),
    Array.from({ length: 999 }, (_, i) => `a=${i}`).join('&') + '&mesh_share=abc',
    Array.from({ length: 1000 }, (_, i) => `a=${i}`).join('&') + '&mesh_share=abc',
];

const vectors = fixed.map(run);
for (let i = 0; i < 6000; i++) {
    const count = 1 + random(4) + (random(6) === 0 ? random(5) : 0);
    const parts = [];
    for (let j = 0; j < count; j++) parts.push(pair());
    vectors.push(run(parts.join('&')));
}

const file = path.join(__dirname, '..', 'query.json.gz');
fs.writeFileSync(file, zlib.gzipSync(vectors.map((v) => JSON.stringify(v)).join('\n') + '\n', { level: 9 }));
const thrown = vectors.filter((v) => typeof v.search === 'object' || typeof v.share === 'object' || typeof v.guest === 'object').length;
console.log(`${vectors.length} vectors, ${thrown} with a value that has no text, written to ${file}`);
