'use strict';
// Writes ../../tables.bin.gz, the Unicode data the Go package embeds.
// See README.md in this folder.
//
//   node tables.js /path/to/ada.cpp
//
// ada.cpp is the single-file release of the URL library inside Node (the
// version `process.versions.ada` names). Its tables decide what
// url.domainToASCII makes of a domain, so the Go code uses the same ones.
// The lower-case data is read from this Node itself.
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const adaFile = process.argv[2];
if (!adaFile) {
    console.error('usage: node tables.js /path/to/ada.cpp');
    process.exit(2);
}
const ada = fs.readFileSync(adaFile, 'utf8');
const wantVersion = process.versions.ada;
if (!ada.includes('IDNA  15.0.0')) {
    console.error('ada.cpp does not carry the IDNA 15.0.0 tables this script was written for');
    process.exit(2);
}

const DIRECTIONS = ['NONE', 'BN', 'CS', 'ES', 'ON', 'EN', 'L', 'R', 'NSM', 'AL', 'AN', 'ET', 'WS', 'RLO', 'LRO', 'PDF', 'RLE', 'RLI', 'FSI', 'PDI', 'LRI', 'B', 'S', 'LRE'];

// numbers returns every number of the array that `declaration` starts. With
// a row width, the array has rows in braces, and a row that names fewer
// values than that is filled up with zeros, as the compiler does.
function numbers(declaration, expected, rowWidth) {
    const start = ada.indexOf(declaration);
    if (start < 0) {
        throw new Error('not found in ada.cpp: ' + declaration);
    }
    const open = ada.indexOf('{', ada.indexOf('=', start));
    const close = ada.indexOf('};', open);
    const body = ada.slice(open, close + 1).replace(/direction::(\w+)/g, (all, name) => {
        const value = DIRECTIONS.indexOf(name);
        if (value < 0) {
            throw new Error('unknown direction ' + name);
        }
        return String(value);
    });
    const read = text => (text.match(/0x[0-9a-fA-F]+|\d+/g) || []).map(Number);
    let list;
    if (rowWidth) {
        list = [];
        for (const row of body.slice(1).match(/\{[^{}]*\}/g) || []) {
            const values = read(row);
            if (values.length > rowWidth) {
                throw new Error(declaration + ': a row of ' + values.length + ' values');
            }
            while (values.length < rowWidth) {
                values.push(0);
            }
            list.push(...values);
        }
        // rows the source leaves out are zero as well
        while (expected !== undefined && list.length < expected) {
            list.push(0);
        }
    } else {
        list = read(body);
    }
    if (expected !== undefined && list.length !== expected) {
        throw new Error(declaration + ': ' + list.length + ' numbers, expected ' + expected);
    }
    return list;
}

const sections = [];
function section(name, size, list) {
    const data = Buffer.alloc(list.length * size);
    list.forEach((value, i) => {
        if (size === 1) {
            data.writeUInt8(value, i);
        } else if (size === 2) {
            data.writeUInt16LE(value, i * 2);
        } else {
            data.writeUInt32LE(value, i * 4);
        }
    });
    const head = Buffer.alloc(1 + name.length + 1 + 4);
    head.writeUInt8(name.length, 0);
    head.write(name, 1, 'latin1');
    head.writeUInt8(size, 1 + name.length);
    head.writeUInt32LE(list.length, 2 + name.length);
    sections.push(head, data);
}

section('map_ranges', 4, numbers('const uint32_t table[8000][2]', 16000, 2));
section('map_data', 4, numbers('const uint32_t mappings[5164]', 5164));
section('decomp_index', 1, numbers('const uint8_t decomposition_index[4352]', 4352));
section('decomp_block', 2, numbers('const uint16_t decomposition_block[67][257]', 67 * 257, 257));
section('decomp_data', 4, numbers('const char32_t decomposition_data[9102]', 9102));
section('ccc_index', 1, numbers('const uint8_t canonical_combining_class_index[4352]', 4352));
section('ccc_block', 1, numbers('const uint8_t canonical_combining_class_block[67][256]', 67 * 256, 256));
section('comp_index', 1, numbers('const uint8_t composition_index[4352]', 4352));
section('comp_block', 2, numbers('const uint16_t composition_block[67][257]', 67 * 257, 257));
section('comp_data', 4, numbers('const char32_t composition_data[1883]', 1883));
section('bidi', 4, numbers('static directions dir_table[]', undefined, 3));
section('combining', 4, numbers('constexpr static uint32_t combining[]'));
section('virama', 4, numbers('constexpr static uint32_t virama[]'));
section('join_r', 4, numbers('constexpr static uint32_t R[]'));
section('join_l', 4, numbers('constexpr static uint32_t L[]'));
section('join_d', 4, numbers('constexpr static uint32_t D[]'));

// What String.prototype.toLowerCase does to every code point on its own, and
// the two classes that decide whether a capital sigma is the last letter of
// a word: a cased letter (1) and a character that is skipped on the way to
// one (2). A character that is both is skipped.
const lower = [];
const classes = [];
let run = null;
for (let cp = 0; cp <= 0x10ffff; cp++) {
    let cls = 0;
    if (cp < 0xd800 || cp > 0xdfff) {
        const chr = String.fromCodePoint(cp);
        const low = chr.toLowerCase();
        if (low !== chr) {
            const mapped = Array.from(low, c => c.codePointAt(0));
            lower.push(cp, mapped.length, ...mapped);
        }
        if ((chr + '\u{3a3}').toLowerCase().endsWith('\u{3c2}')) {
            cls = 1;
        } else if (('a' + chr + '\u{3a3}').toLowerCase().endsWith('\u{3c2}')) {
            cls = 2;
        }
    }
    if (run && run[2] === cls && run[1] === cp - 1) {
        run[1] = cp;
    } else {
        run = cls ? [cp, cp, cls] : null;
        if (run) {
            classes.push(run);
        }
    }
}
section('lower', 4, lower);
section('case_class', 4, classes.flat());

const out = path.join(__dirname, '..', '..', 'tables.bin.gz');
const raw = Buffer.concat([Buffer.from('BDMSGTAB1\n', 'latin1'), ...sections]);
fs.writeFileSync(out, zlib.gzipSync(raw, { level: 9 }));
console.log('ada ' + wantVersion + ', unicode ' + process.versions.unicode + ': ' + raw.length + ' bytes, ' + fs.statSync(out).size + ' compressed, written to ' + out);
