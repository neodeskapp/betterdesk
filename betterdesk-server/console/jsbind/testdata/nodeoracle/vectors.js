'use strict';

/**
 * Records what JavaScript and the Node console's two database drivers make
 * of values, for the Go package console/jsbind to replay. See README.md.
 *
 *   node vectors.js        writes ../vectors.json
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const Database = require(path.join(WEB, 'node_modules/better-sqlite3'));
const { prepareValue } = require(path.join(WEB, 'node_modules/pg/lib/utils'));

// A small generator with a fixed seed: the same vectors on every run.
let state = 0x2545f491;
function next() {
    state ^= state << 13; state >>>= 0;
    state ^= state >>> 17;
    state ^= state << 5; state >>>= 0;
    return state / 0x100000000;
}
const pick = (list) => list[Math.floor(next() * list.length)];

const PIECES = ['0', '1', '2', '5', '7', '9', '00', '10', '.', '.', 'e', 'E', '+', '-', 'x', 'X', 'o', 'O', 'b', 'B', 'a', 'f', 'F', '_', ' ',
    '\t', '\n', 'Infinity', 'infinity', 'NaN', 'nan', 'inf', ' ', '﻿', '　', ' ', '᠎', 'İ', '１', 'I', 'K',
    'K', 'Σ', 'ß', 'ǅ', 'p', 'n', ',', '0x', '1e', 'e5', 'E-3', '9999999999', '.5', '5.'];

const strings = ['0x-1', '0x+1', '0b-1', '0o+7', '0x1_0', '0b_1', '', ' ', '0', '-0', '+0', '1', '01', '1.0', '1.', '.1', '1e0', '1E3', '1e+3', '1e-3', '1e', 'e1', '.e1', '1.e1', '.', '+', '-',
    '+.', '0x', '0x1', '0X1F', '0xg', '-0x1', '+0x1', '0b1', '0b2', '0B101', '0o17', '0o8', '0O7', '0x1p3', '1_000', '1,000', 'Infinity',
    '+Infinity', '-Infinity', 'infinity', 'INFINITY', 'Inf', 'NaN', 'nan', '  12  ', '\t12\n', ' 12 ', '﻿12', '12　', '1 2',
    '12abc', 'abc', '1e400', '-1e400', '1e-400', '-1e-400', '9007199254740993', '0.1', '0.30000000000000004', '123456789012345678901234567890',
    '0.000001', '0.0000001', '1e21', '1e20', '123e-20', '5e-324', '1.7976931348623157e308', '1.7976931348623159e308', '0xffffffffffffffffffff',
    '0b' + '1'.repeat(70), '0o' + '7'.repeat(30), '１', '١', 'true', 'null', 'undefined', '[object Object]', '1;x', '++1', '--1', '+-1',
    '1e5.5', '1ee5', '00.5', '-.5e-1', 'İstanbul', 'İ', 'STRASSE ẞ', 'ΣΣ', 'AΣ ', 'ǅ', 'K', 'Ω', 'ǅ',
    '  İ  ', 'MiXeD CaSe 123'];
for (let i = 0; i < 4000; i += 1) {
    let s = '';
    const parts = 1 + Math.floor(next() * 6);
    for (let p = 0; p < parts; p += 1) s += pick(PIECES);
    strings.push(s);
}

const values = [null, true, false, 0, -0, 1, -1, 1.5, 5, 77, 1e21, 1e-7, 123456789012345680000, 0.1, 2147483647, 2147483648, -2147483649,
    9007199254740991, 9007199254740993, '', ' ', '0', 'text', '12', ' 12 ', '0x10', 'a"b\\c', "it's", '{}', 'NULL', 'null', ' ', '<&>',
    [], [1], ['a'], ['a', 'b'], [null], [true], [[1]], [[]], [1, [2, [3, null]], 'x"y\\z', '', 'NULL', true, 1.5, {}], [' 3 '], ['0x10'], [''],
    {}, { a: 1 }, { length: 0 }, { b: [1, { c: null }], 2: 'x', a: 1.0, 10: 'y', 1: 'z' }, { 'k"ey': 'v ', n: 1e21, m: -0, deep: { deeper: [] } },
    [{ a: 1 }], [[{ a: [1, 2] }]]];

const db = new Database(':memory:');
const one = db.prepare('SELECT typeof(?) AS type, CAST(? AS TEXT) AS text');
const two = db.prepare('SELECT ? AS a, ? AS b');

function sqliteBind(v) {
    try {
        const row = one.get(v, v);
        return { type: row.type, text: row.text };
    } catch (err) {
        return { error: `${err.name}: ${err.message}` };
    }
}

// Lists of values bound to a statement with two placeholders
const lists = [[1, 2], [1], [1, 2, 3], [[1, 2]], [[1], 2], [1, [2]], [[], 1, 2], [[1, 2, 3]], [{}, 1, 2], [{}, 1], [{}, {}], [1, {}, 2], [{ a: 1 }, [1, 2]],
    [[1, [2]]], [true, 1], [1, undefined], [null, null], ['a', 'b'], [[null, 'x']], [[[1]], 2], [NaN, Infinity], [-Infinity, -0], [1e21, 5],
    [[], [], 1, 2], [{}, [1], [2]], [[{}], 1]];

function sqliteList(list) {
    try {
        const row = two.get(...list);
        return { a: JSON.stringify(row.a), b: JSON.stringify(row.b) };
    } catch (err) {
        return { error: `${err.name}: ${err.message}` };
    }
}

const numberString = (s) => String(Number(s));
const out = {
    strings: strings.map((s) => ({ s, number: numberString(s), negZero: Object.is(Number(s), -0), trim: s.trim(), lower: s.toLowerCase() })),
    values: values.map((v) => ({
        json: Object.is(v, -0) ? '-0' : JSON.stringify(v),
        number: String(Number(v)),
        string: String(v),
        truthy: !!v,
        stringify: JSON.stringify(v),
        postgres: prepareValue(v),
        sqlite: sqliteBind(v),
    })),
    lists: lists.map((list) => ({
        // undefined, NaN and the infinities have no JSON: they are named
        json: JSON.stringify(list, (_k, v) => (v === undefined ? '@undefined' : Number.isNaN(v) ? '@NaN' : v === Infinity ? '@Infinity' : v === -Infinity ? '@-Infinity' : Object.is(v, -0) ? '@-0' : v)),
        sqlite: sqliteList(list),
        postgres: list.map((v) => prepareValue(v)),
    })),
};

const file = path.join(__dirname, '..', 'vectors.json');
fs.writeFileSync(file, `${JSON.stringify(out)}\n`);
console.log(`${file}: ${out.strings.length} strings, ${out.values.length} values, ${out.lists.length} lists`);
