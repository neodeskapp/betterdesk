'use strict';

/**
 * Small font files for the upload vectors, built here byte by byte so
 * that no binary files have to live in the repository: a TrueType font
 * with two glyphs (.notdef and a triangle for "A"), the same outline as a
 * CFF OpenType font, and both wrapped as WOFF (zlib) and WOFF2 (Brotli,
 * tables untransformed). Plus files that are not fonts or broken ones.
 */

const zlib = require('zlib');

const u8 = (...v) => Buffer.from(v);
const u16 = (...v) => { const b = Buffer.alloc(2 * v.length); v.forEach((x, i) => b.writeUInt16BE(x & 0xffff, 2 * i)); return b; };
const u32 = (...v) => { const b = Buffer.alloc(4 * v.length); v.forEach((x, i) => b.writeUInt32BE(x >>> 0, 4 * i)); return b; };
const tag = (t) => Buffer.from(t, 'latin1');
const pad4 = (b) => (b.length % 4 ? Buffer.concat([b, Buffer.alloc(4 - (b.length % 4))]) : b);

function checksum(buf) {
    const b = pad4(buf);
    let sum = 0;
    for (let i = 0; i < b.length; i += 4) sum = (sum + b.readUInt32BE(i)) >>> 0;
    return sum;
}

const FAMILY = 'Bdtest';

function nameTable() {
    const strings = [[1, FAMILY], [2, 'Regular'], [3, `${FAMILY} 1.0`], [4, FAMILY], [5, 'Version 1.0'], [6, FAMILY]];
    const data = [];
    const records = [];
    let offset = 0;
    for (const [id, text] of strings) {
        const s = Buffer.from(text, 'utf16le').swap16();
        records.push(u16(3, 1, 0x409, id, s.length, offset));
        data.push(s);
        offset += s.length;
    }
    return Buffer.concat([u16(0, strings.length, 6 + 12 * strings.length), ...records, ...data]);
}

function commonTables(cff) {
    const head = Buffer.concat([
        u32(0x00010000, 0x00010000, 0, 0x5f0f3cf5), u16(0x000b, 1000), Buffer.alloc(16),
        u16(100, 0, 500, 700), u16(0, 8, 2, 0, 0),
    ]);
    const hhea = Buffer.concat([u32(0x00010000), u16(800, -200, 0, 600, 0, 0, 600, 1, 0, 0, 0, 0, 0, 0, 0, 2)]);
    const maxp = cff ? Buffer.concat([u32(0x00005000), u16(2)]) : Buffer.concat([u32(0x00010000), u16(2, 3, 1, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0)]);
    const os2 = Buffer.concat([
        u16(3, 600, 400, 5, 0, 650, 700, 0, 140, 650, 700, 0, 480, 50, 250, 0), Buffer.alloc(10), u32(1, 0, 0, 0), tag('BDSK'),
        u16(0x40, 0x41, 0x41, 800, -200, 0, 800, 200), u32(1, 0), u16(500, 700, 0, 0x20, 1),
    ]);
    const hmtx = u16(600, 0, 600, 100);
    const cmap = Buffer.concat([
        u16(0, 1, 3, 1), u32(12),
        u16(4, 32, 0, 4, 4, 1, 0, 0x41, 0xffff, 0, 0x41, 0xffff, 1 - 0x41, 1, 0, 0),
    ]);
    const post = Buffer.concat([u32(0x00030000, 0), u16(-100, 50), u32(0, 0, 0, 0, 0)]);
    return { head, hhea, maxp, 'OS/2': os2, hmtx, cmap, name: nameTable(), post };
}

function glyfAndLoca() {
    const triangle = Buffer.concat([
        u16(1, 100, 0, 500, 700), u16(2), u16(0), u8(1, 1, 1),
        u16(100, 400, -200), u16(0, 0, 700),
    ]);
    const glyf = pad4(triangle);
    return { glyf, loca: u16(0, 0, glyf.length / 2) };
}

/** A number in a CFF dictionary: the five byte form, so offsets have a fixed size. */
const dictInt = (n) => Buffer.concat([u8(29), u32(n)]);
/** A number in a Type 2 charstring. */
const csInt = (n) => (n >= -107 && n <= 107 ? u8(n + 139) : Buffer.concat([u8(28), u16(n)]));

function cffIndex(items) {
    if (items.length === 0) return u16(0);
    const offsets = [1];
    for (const item of items) offsets.push(offsets[offsets.length - 1] + item.length);
    return Buffer.concat([u16(items.length), u8(1), Buffer.from(offsets), ...items]);
}

function cffTable() {
    const header = u8(1, 0, 4, 1);
    const names = cffIndex([Buffer.from(FAMILY)]);
    const strings = cffIndex([]);
    const globalSubrs = cffIndex([]);
    const charStrings = cffIndex([
        u8(14),
        Buffer.concat([csInt(600), csInt(100), csInt(0), u8(21), csInt(400), csInt(0), u8(5), csInt(-200), csInt(700), u8(5), u8(14)]),
    ]);
    const privateDict = Buffer.concat([csInt(600), u8(20)]); // defaultWidthX
    const topDictLength = 5 + 1 + 5 + 5 + 1;
    const topIndexLength = 2 + 1 + 2 + topDictLength;
    const charStringsOffset = header.length + names.length + topIndexLength + strings.length + globalSubrs.length;
    const privateOffset = charStringsOffset + charStrings.length;
    const topDict = Buffer.concat([dictInt(charStringsOffset), u8(17), dictInt(privateDict.length), dictInt(privateOffset), u8(18)]);
    return Buffer.concat([header, names, cffIndex([topDict]), strings, globalSubrs, charStrings, privateDict]);
}

/** Put tables together as an sfnt file with checksums. */
function sfnt(version, tables) {
    const tags = Object.keys(tables).sort();
    const n = tags.length;
    const pow = 2 ** Math.floor(Math.log2(n));
    const dir = [];
    const bodies = [];
    let offset = 12 + 16 * n;
    for (const t of tags) {
        const data = tables[t];
        dir.push(Buffer.concat([tag(t.padEnd(4)), u32(checksum(data), offset, data.length)]));
        bodies.push(pad4(data));
        offset += pad4(data).length;
    }
    const file = Buffer.concat([u32(version), u16(n, pow * 16, Math.log2(pow), n * 16 - pow * 16), ...dir, ...bodies]);
    const headAt = 12 + 16 * n + bodies.slice(0, tags.indexOf('head')).reduce((sum, b) => sum + b.length, 0);
    file.writeUInt32BE((0xb1b0afba - checksum(file)) >>> 0, headAt + 8);
    return file;
}

function tablesOf(file) {
    const n = file.readUInt16BE(4);
    const out = [];
    for (let i = 0; i < n; i++) {
        const at = 12 + 16 * i;
        const offset = file.readUInt32BE(at + 8);
        const length = file.readUInt32BE(at + 12);
        out.push({ tag: file.subarray(at, at + 4), checksum: file.readUInt32BE(at + 4), data: file.subarray(offset, offset + length) });
    }
    return out;
}

function woff(file) {
    const tables = tablesOf(file);
    const dir = [];
    const bodies = [];
    let offset = 44 + 20 * tables.length;
    for (const t of tables) {
        const packed = zlib.deflateSync(t.data, { level: 9 });
        const stored = packed.length < t.data.length ? packed : t.data;
        dir.push(Buffer.concat([t.tag, u32(offset, stored.length, t.data.length, t.checksum)]));
        bodies.push(pad4(stored));
        offset += pad4(stored).length;
    }
    return Buffer.concat([tag('wOFF'), u32(file.readUInt32BE(0), offset), u16(tables.length, 0), u32(file.length), u16(1, 0), u32(0, 0, 0, 0, 0), ...dir, ...bodies]);
}

const WOFF2_TAGS = ['cmap', 'head', 'hhea', 'hmtx', 'maxp', 'name', 'OS/2', 'post', 'cvt ', 'fpgm', 'glyf', 'loca', 'prep', 'CFF '];

function base128(n) {
    const out = [n & 0x7f];
    for (n >>>= 7; n > 0; n >>>= 7) out.unshift((n & 0x7f) | 0x80);
    return Buffer.from(out);
}

function woff2(file) {
    // loca has to follow glyf directly
    const tables = tablesOf(file).filter((t) => t.tag.toString('latin1') !== 'loca');
    const glyfAt = tables.findIndex((t) => t.tag.toString('latin1') === 'glyf');
    if (glyfAt >= 0) tables.splice(glyfAt + 1, 0, tablesOf(file).find((t) => t.tag.toString('latin1') === 'loca'));
    const dir = [];
    const bodies = [];
    for (const t of tables) {
        const name = t.tag.toString('latin1');
        const known = WOFF2_TAGS.indexOf(name);
        // glyf and loca go in as they are ("null transform")
        const flags = (known >= 0 ? known : 63) | (name === 'glyf' || name === 'loca' ? 0xc0 : 0);
        dir.push(Buffer.concat([u8(flags), known >= 0 ? Buffer.alloc(0) : t.tag, base128(t.data.length)]));
        bodies.push(t.data);
    }
    const packed = zlib.brotliCompressSync(Buffer.concat(bodies));
    const directory = Buffer.concat(dir);
    // the file, not the compressed block, ends on a multiple of four
    const padding = Buffer.alloc((4 - ((48 + directory.length + packed.length) % 4)) % 4);
    const length = 48 + directory.length + packed.length + padding.length;
    return Buffer.concat([
        tag('wOF2'), u32(file.readUInt32BE(0), length), u16(tables.length, 0), u32(file.length, packed.length), u16(1, 0), u32(0, 0, 0, 0, 0),
        directory, packed, padding,
    ]);
}

/** Every fixture: { name: Buffer }. The first four are fonts. */
function fixtures() {
    const ttf = sfnt(0x00010000, { ...commonTables(false), ...glyfAndLoca() });
    const otf = sfnt(0x4f54544f, { ...commonTables(true), 'CFF ': cffTable() });
    const flipped = Buffer.from(ttf);
    flipped[flipped.length - 9] ^= 0xff;
    const wrongMagic = Buffer.from(woff(ttf));
    wrongMagic.write('wOFX', 0, 'latin1');
    return {
        'valid.ttf': ttf,
        'valid.otf': otf,
        'valid.woff': woff(ttf),
        'valid.woff2': woff2(ttf),
        'otf-in.woff2': woff2(otf),
        'truncated.ttf': ttf.subarray(0, 40),
        'bad-checksum.ttf': flipped,
        'wrong-magic.woff': wrongMagic,
        'truncated.woff2': woff2(ttf).subarray(0, 30),
        'empty.woff2': Buffer.alloc(0),
        'text.ttf': Buffer.from('this is not a font\n'),
        'page.ttf': Buffer.from('<!doctype html><script>alert(1)</script>'),
        'picture.png': Buffer.concat([Buffer.from('89504e470d0a1a0a', 'hex'), Buffer.from('0000000d49484452', 'hex'), Buffer.alloc(13)]),
        'script.svg': Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script></svg>'),
    };
}

module.exports = { fixtures, FAMILY };
