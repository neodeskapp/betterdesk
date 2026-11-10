'use strict';

/**
 * Records what the Node console's query parser (qs, with the options Express
 * gives it) makes of the parameter "name" in query strings, for the Go
 * function jsbind.Query to replay. See README.md.
 *
 *   node query.js        writes ../query.json
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const qs = require(path.join(WEB, 'node_modules/qs'));

let state = 0x1f123bb5;
function next() {
    state ^= state << 13; state >>>= 0;
    state ^= state >>> 17;
    state ^= state << 5; state >>>= 0;
    return state / 0x100000000;
}
const pick = (list) => list[Math.floor(next() * list.length)];

const KEYS = ['name[][]', 'name[][][]', 'name[][2]', 'name[][][0]', 'name[][][][][]', 'name[][][][][][]', 'name[][x]y[]', 'name[]x[]', 'name[[]]', 'name[a[b]]',
    'name', 'name', 'name', 'name[]', 'name[]', 'name%5B%5D', 'name[0]', 'name[1]', 'name[2]', 'name[19]', 'name[20]', 'name[01]', 'name[-1]',
    'name[x]', 'name[x][y]', 'name[][x]', 'name[0][]', 'name[', 'name]', 'name[]x', 'other', 'names', 'nam', 'Name', 'na%6De', 'name%5b1%5d', 'name+', 'name[ ]',
    'name[1.5]', 'name[1e1]', 'name.x', '', 'name[toString]', 'name[length]'];
const VALUES = ['a', 'b', '', 'open', 'two+words', 'a%20b', '%E2%9C%93', '%zz', '%E0%A4%A', '%ff', 'x=y', 'a%26b', '1', '0', 'caf%C3%A9', '%ED%A0%80', ' ', '[]', 'a,b'];

const queries = ['name=a%5D=b', 'name=a]=b', 'x=1&name=a]=b&name=c', 'name[a=b]=c', 'name%5Ba=b%5D=c', 'name=%5Bx%5D', 'name[]=a]=b', '', 'name', 'name=', 'name=a', 'name=a&name=b', 'name[]=a', 'name[]=a&name[]=b', 'name=a&name[]=b&name=c', 'name[]=b&name=a&name=c',
    'name[1]=b&name[0]=a', 'name[1]=b', 'name[0]=a&name[0]=b', 'name[0]=a&name=b', 'name[x]=1', 'name[x][y]=1', 'other=1', '&&name=a&&', 'name=a=b',
    'name]=a', 'name[]=', 'a=1&name=%E2%9C%93', Array.from({ length: 20 }, (_, i) => `name=v${i}`).join('&'), Array.from({ length: 21 }, (_, i) => `name=v${i}`).join('&'),
    Array.from({ length: 21 }, (_, i) => `name[]=v${i}`).join('&'), Array.from({ length: 20 }, (_, i) => `name[${i}]=v${i}`).join('&'),
    `${Array.from({ length: 999 }, () => 'x=1').join('&')}&name=last-of-1000`, `${Array.from({ length: 1000 }, () => 'x=1').join('&')}&name=one-too-many`];
for (let i = 0; i < 6000; i += 1) {
    const parts = [];
    const n = 1 + Math.floor(next() * 4);
    for (let p = 0; p < n; p += 1) {
        const key = pick(KEYS);
        parts.push(next() < 0.1 ? key : `${key}=${pick(VALUES)}`);
    }
    queries.push(parts.join('&'));
}

function kindOf(v) {
    if (v === undefined) return 'undefined';
    if (typeof v === 'string') return 'string';
    if (Array.isArray(v) && v.every((item) => typeof item === 'string')) return 'list';
    return 'other';
}

const vectors = queries.map((query) => {
    const value = qs.parse(query, { allowPrototypes: true }).name;
    const kind = kindOf(value);
    return kind === 'string' || kind === 'list' ? { query, kind, value } : { query, kind };
});

const file = path.join(__dirname, '..', 'query.json');
fs.writeFileSync(file, `${JSON.stringify(vectors)}\n`);
const count = (kind) => vectors.filter((v) => v.kind === kind).length;
console.log(`${file}: ${vectors.length} queries (${count('string')} texts, ${count('list')} lists, ${count('other')} other, ${count('undefined')} without the parameter)`);
