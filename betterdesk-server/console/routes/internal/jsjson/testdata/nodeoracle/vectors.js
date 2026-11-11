'use strict';

/**
 * Writes ../vectors.json: what JavaScript makes of JSON texts, for the Go
 * package to be held against. See README.md.
 *
 * This file has no backslash in it: every special character is made from
 * its number, so that nothing on the way to the disk can change one.
 */

const fs = require('fs');
const path = require('path');

const ch = String.fromCodePoint;
const BS = ch(92); // a backslash
const Q = '"';
const NL = ch(10);
const TAB = ch(9);
const SMILE = ch(0x1F600);
const esc = (hex) => `${BS}u${hex}`;
const quoted = (s) => Q + s + Q;

// A small generator with a fixed seed, so that a run gives the same file
let state = 20261110;
function rnd(n) {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state % n;
}
const pick = (list) => list[rnd(list.length)];

const scalars = ['null', 'true', 'false', '0', '-0', '1', '-1', '5', '1.5', '0.1', '1e3', '1E3', '1e+3', '1e-7', '1e21', '1e400', '-1e400', '1e-400',
    '123456789012345678901234567890', '9007199254740993', '0.000001', '0.0000001', '100000000000000000000', '1000000000000000000000', '2.5', '-2.5', '0.5', '-0.5',
    '1048576', '1572864', '524288', '524287', '4.35', '0.30000000000000004', '5e-324', '1.7976931348623157e308',
    quoted(''), quoted('a'), quoted('abc'), quoted('0'), quoted(' '), quoted('5'), quoted(' 12 '), quoted('0x10'), quoted('0b11'), quoted('0o17'), quoted('1e3'),
    quoted('Infinity'), quoted('-Infinity'), quoted('+Infinity'), quoted('NaN'), quoted('.5'), quoted('5.'), quoted('+5'), quoted('--5'), quoted('1_000'), quoted('12px'),
    quoted('true'), quoted('null'), quoted('undefined'), quoted('[object Object]'), quoted('1,2'), quoted('0x'), quoted('0xg'), quoted('-0x10'), quoted('1e'),
    quoted(`${BS}n`), quoted(`a${BS}tb`), quoted(`${BS}${Q}`), quoted(`${BS}${BS}`), quoted(`${BS}/`), quoted(`${BS}b${BS}f${BS}r`), quoted(esc('0000')), quoted(esc('001f')),
    quoted(esc('007f')), quoted(esc('00e9')), quoted(esc('00E9')), quoted(esc('2028') + esc('2029')), quoted(esc('feff') + '5'), quoted(esc('00a0') + '7' + esc('3000')),
    quoted(esc('d83d') + esc('de00')), quoted(esc('D83D') + esc('DE00')), quoted(esc('d83d')), quoted(esc('de00')), quoted(esc('de00') + esc('d83d')),
    quoted(esc('d83d') + 'x'), quoted('x' + esc('d83d') + esc('d83d') + esc('de00')), quoted(esc('d83d') + BS + 'n'), quoted(esc('dbff') + esc('dfff')),
    quoted(esc('d800') + esc('dc00')), quoted(esc('ffff')), quoted(esc('fffd')), quoted(esc('d83d') + SMILE), quoted(SMILE + esc('de00')),
    quoted(ch(0xe9) + ch(0x20ac) + SMILE), quoted('<b>&amp;</b>'), quoted(ch(0x2028)), quoted(ch(0x7f)), quoted(SMILE.repeat(3)), quoted('a'.repeat(130)),
    quoted('ab' + SMILE.repeat(70)), quoted('a' + SMILE.repeat(70)), quoted(ch(0xa0) + ' trim me ' + ch(0x2003)), quoted(ch(0xfeff) + '12' + ch(0x2029)),
    quoted(ch(0x85) + '5'), quoted(ch(0x180e) + '5'), quoted(ch(0x200b) + '5')];
const keys = ['a', 'b', 'hostname', 'cpu', 'disks', 'length', 'toString', 'valueOf', 'constructor', '', '0', '1', '2', '10', '01', '-1', '1.5', '4294967294', '4294967295',
    'z', esc('00e9'), 'A', ' '];

function value(depth) {
    const kind = depth > 3 ? 0 : rnd(10);
    if (kind < 6) return pick(scalars);
    if (kind < 8) {
        const n = rnd(4);
        return `[${Array.from({ length: n }, () => value(depth + 1)).join(',')}]`;
    }
    const n = rnd(5);
    return `{${Array.from({ length: n }, () => `${quoted(pick(keys))}:${value(depth + 1)}`).join(',')}}`;
}

const texts = new Set(scalars);
for (const text of ['[]', '{}', '[[]]', '[{}]', '{"a":{}}', '[null]', '[null,null]', '[1,[2,[3,[4]]]]', '[[],[[]],{}]', '["a",null,"b"]', '[5]', '["5"]', '[" 5 "]', '[1,2]',
    '[[5]]', '[true]', '[[null]]', '[""]', '["",""]', '{"toString":1}', '{"toString":null}', '{"valueOf":1}', '{"toString":1,"valueOf":2}',
    '[{"toString":1}]', '[[{"toString":"x"}]]', '[1,{"toString":1}]', '{"a":{"toString":1}}', '{"length":3}', '{"length":"x"}', '{"length":null}', '{"length":[1,2]}',
    '{"b":1,"a":2}', '{"2":1,"1":2,"b":3,"a":4,"10":5,"01":6}', '{"a":1,"a":2}', '{"a":1,"b":2,"a":3}', '{"1":1,"a":2,"1":3}', '{"__proto__":5}', '{"__proto__":{"a":1}}',
    '{"constructor":1}', '{"hostname":"pc","cpu":{"brand":"x"}}', '{"":1}', ' {"a" : [1 , 2] } ', `${TAB}[1]${NL}`, `${ch(13)}${NL}5${ch(13)}${NL}`, `${ch(0xa0)}5`, `${ch(0xfeff)}5`,
    '5 5', '5,', ',5', '[1,]', '[,1]', '{"a":1,}', '{,}', '{"a"}', '{"a":}', '{a:1}', "{'a':1}", '[1 2]', '01', '-', '+1', '.5', '5.', '1e', '1e+', '0x10', 'NaN', 'Infinity',
    '-Infinity', 'undefined', 'nul', 'nulll', 'True', 'tru', '"', '"a', 'a"', `"${BS}"`, `"${BS}x"`, `"${BS}u12"`, `"${BS}u12g4"`, `"${BS}u+123"`, `"${BS}U0041"`,
    `"a${NL}b"`, `"a${TAB}b"`, `"${ch(0)}"`, `"${ch(0x1f)}"`, `"${ch(0x7f)}"`, '', ' ', '[', ']', '{', '}',
    '[}', '{]', '[[]', '[]]', '{}{}', '[][]', 'null null', '1 2', '--1', '-01', '1.e3', '1.5.5', '0.0', '-0.0', '0e0', '00', '-00', '1E-2', '1e03',
    '['.repeat(50) + ']'.repeat(50), '{"a":'.repeat(30) + '1' + '}'.repeat(30), '['.repeat(50) + ']'.repeat(49), '/* c */ 1', '1 // c', `${ch(0x2028)}[]`, `[${ch(0xa0)}]`,
    `[${ch(11)}]`, `[${ch(12)}]`]) {
    texts.add(text);
}
while (texts.size < 6000) texts.add(value(0));
// Valid texts with one character changed, dropped or doubled
const valid = [...texts].filter((t) => { try { JSON.parse(t); return t.length > 1; } catch (_) { return false; } });
while (texts.size < 9000) {
    const text = pick(valid);
    const at = rnd(text.length);
    const how = rnd(3);
    if (how === 0) texts.add(text.slice(0, at) + text.slice(at + 1));
    else if (how === 1) texts.add(text.slice(0, at) + text[at] + text.slice(at));
    else texts.add(text.slice(0, at) + pick([Q, ',', ':', '[', '}', '0', BS, ' ', 'e', '-', '.', 'u']) + text.slice(at + 1));
}

const units = (s) => Array.from({ length: s.length }, (_, i) => s.charCodeAt(i));
const number = (n) => (Object.is(n, -0) ? '-0' : String(n));
const NAMES = ['length', 'hostname', 'cpu', 'a', 'b', 'disks', 'toString'];

const vectors = [];
for (const text of texts) {
    // A text has to be UTF-8 to sit in a database
    if (Buffer.from(text, 'utf8').toString('utf8') !== text) continue;
    let v;
    try {
        v = JSON.parse(text);
    } catch (_) {
        vectors.push({ text, error: true });
        continue;
    }
    const out = { text, json: JSON.stringify(v), truthy: !!v, members: {} };
    try { out.string = units(String(v)); } catch (_) { out.stringError = true; }
    try { out.number = number(Number(v)); } catch (_) { out.numberError = true; }
    if (v !== null && v !== undefined) {
        for (const name of NAMES) {
            const member = v[name];
            // What a value has by its type alone (a method) is no member
            out.members[name] = typeof member === 'function' || member === undefined ? null : JSON.stringify(member);
        }
    }
    if (typeof v === 'string') {
        out.length = v.length;
        out.slices = [0, 1, 2, 3, 5, 120].map((n) => [n, units(v.slice(0, n)), JSON.stringify(v.slice(0, n))]);
    }
    vectors.push(out);
}
const file = path.join(__dirname, '..', 'vectors.json');
fs.writeFileSync(file, `${JSON.stringify(vectors)}${NL}`);
console.log(`${file}: ${vectors.length} vectors, ${vectors.filter((x) => x.error).length} of them no JSON`);
