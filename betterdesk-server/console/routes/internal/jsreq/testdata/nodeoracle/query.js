// Writes ../query.json: what Express's query parser and JavaScript make of
// query strings, for the Go tests of package jsreq to replay.
//
//   cd /repo/web-nodejs && node ../betterdesk-server/console/routes/internal/jsreq/testdata/nodeoracle/query.js
'use strict';

const fs = require('fs');
const path = require('path');
const nodeModules = path.join('/repo', 'web-nodejs', 'node_modules');
const { compileQueryParser } = require(path.join(nodeModules, 'express', 'lib', 'utils'));
const { assertSafeApiId } = require(path.join('/repo', 'web-nodejs', 'lib', 'goApiPath'));

const parse = compileQueryParser('extended');

const parts = [
    'a=1', 'a=2', 'a', 'a=', 'a=true', 'a[]=1', 'a[]=', 'a[0]=x', 'a[2]=y', 'a[1]=o', 'a[x]=1', 'a[x]=',
    'a[x][y]=2', 'a[][]=3', 'a[1001]=z', 'a[999]=k', 'a%5B%5D=q', 'a%5bx%5d=r', 'b=1', 'a=%E0%A4%A',
    'a=+1e3', '[a]=5', 'a[=1', 'a]=1', 'a[b=1', 'a=0x1F', 'a=1e21', 'a=-5', 'A=1', 'a.b=1',
    'a[__proto__]=1', 'a[length]=2', 'a[-1]=n', 'a[01]=m', 'a=%20%207', '=x', '', 'a=%C3%A9', 'a=12abc',
    'a=99999999999999999999999', 'a[a][b][c][d][e][f][g]=deep', 'a=%2C', 'a=x,y', 'a[][x]=1', 'a[0][x]=2',
];

const queries = new Set();
for (const p of parts) queries.add(p);
for (const p of parts) for (const q of parts) queries.add(p + '&' + q);
let seed = 12345;
function rand(n) {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed % n;
}
for (let i = 0; i < 4000; i++) {
    const n = 3 + rand(3);
    const picked = [];
    for (let j = 0; j < n; j++) picked.push(parts[rand(parts.length)]);
    queries.add(picked.join('&'));
}
queries.add(Array(1001).fill('a=1').join('&'));
queries.add(Array(1000).fill('b=1').join('&') + '&a=late');
queries.add(Array(999).fill('b=1').join('&') + '&a=last');
queries.add(Array(30).fill('a[]=1').join('&') + '&a=2&a[x]=3');
queries.add(Array(1001).fill('a[]=7').join('&'));

function parseIntOr(v, radix) {
    const n = radix === 0 ? parseInt(v) : parseInt(v, radix);
    return Number.isNaN(n) ? null : String(n);
}

const out = [];
for (const query of queries) {
    const value = parse(query).a;
    out.push({
        query,
        // undefined has no JSON form: the key is left out
        value,
        string: value === undefined ? null : String(value),
        truthy: !!value,
        int10: parseIntOr(value, 10),
        int: parseIntOr(value, 0),
    });
}

const ids = [];
const idInputs = [
    'abc', ' abc ', '', ' ', '.', '..', '...', 'a.b', 'a/b', 'a b', 'a%2Fb', 'user@example.com', 'a:b', 'a+b', 'a-b_c',
    'ä', 'a\\b', 'a?b', 'a#b', 'a&b', 'x'.repeat(256), 'x'.repeat(257), ' abc﻿', '\tabc\n', 'a\nb', '900100001',
    'super_admin', 'device.view', '*', 'a,b', '[object Object]', 'null', 'undefined', '0', '%', '%zz',
];
for (const input of idInputs) {
    let safe = null;
    let error = null;
    try { safe = encodeURIComponent(assertSafeApiId(input, 'deviceId')); } catch (e) { error = e.message; }
    ids.push({ input, safe, error });
}

const values = [];
const jsonInputs = [
    '1', '1.5', '1e21', '1e-7', '-0', '0', 'true', 'false', 'null', '"x"', '""', '[]', '[1,2]', '[null,1]',
    '[[1,2],[3]]', '{}', '{"a":1}', '[{"a":1}]', '" 42 "', '"0x10"', '"-0x10"', '123456789012345680000', '0.000001',
    '1e300', '"12px"', '"+7"', '"-7.9"', '".5"', '"1e3"', '[" 9"]', '["900100001"]', '"\\u00a015"', '-1.5e-9',
    '4294967296', '"Infinity"', '"0"', '"00012"', '"0b11"', '[true]', '[""]', '1e999',
];
for (const json of jsonInputs) {
    const value = JSON.parse(json);
    values.push({
        json,
        string: String(value),
        stringOrEmpty: String(value || ''),
        truthy: !!value,
        int10: parseIntOr(value, 10),
        int: parseIntOr(value, 0),
    });
}

const file = path.join(__dirname, '..', 'query.json');
fs.writeFileSync(file, JSON.stringify({ queries: out, ids, values }));
console.log(`wrote ${out.length} queries, ${ids.length} ids and ${values.length} values to ${file}`);
