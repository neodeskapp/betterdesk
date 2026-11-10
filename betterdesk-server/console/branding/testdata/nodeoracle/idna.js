'use strict';

/**
 * Host names with characters outside ASCII.
 *
 * validateBrandingUrl writes a URL out with `new URL(x).toString()`, which
 * turns such a host into Punycode after mapping every character (case
 * folding, compatibility forms, characters that are dropped or refused).
 * Go's standard library has no such table, so this script asks Node about
 * every code point and writes the answers as ../../idna_table.go. It also
 * records hosts made of interesting characters for the Go tests
 * (../idna.json.gz). See README.md in this folder.
 *
 *   node idna.js            table and vectors
 *   node idna.js vectors    vectors only
 */

const fs = require('fs');
const path = require('path');
const url = require('url');
const lib = require('./lib');

const ZWNJ = '\u200c';
const ZWJ = '\u200d';
const BEH = '\u0628'; // an Arabic letter that joins on both sides
const ALEF = '\u05d0'; // a Hebrew letter; nothing combines with it

/** The host Node makes of a name, or null when it refuses it. */
function hostOf(name) {
    try {
        return new URL(`http://${name}/`).hostname;
    } catch (_) {
        return null;
    }
}

/**
 * What one character becomes inside a host: { to: text } or null when it
 * is refused everywhere. The character is tried between two Latin
 * letters, between two Hebrew letters (right-to-left characters are only
 * allowed there) and alone.
 */
function mappingOf(ch) {
    for (const [before, after] of [['q', 'q'], [ALEF, ALEF], ['', '']]) {
        const host = hostOf(`${before}${ch}${after}.x`);
        if (host === null || !host.endsWith('.x')) continue;
        const shown = url.domainToUnicode(host.slice(0, -2));
        if (shown === '' && host.length > 2) continue;
        if (shown.startsWith(before) && shown.endsWith(after) && shown.length >= before.length + after.length) {
            return { to: shown.slice(before.length, shown.length - after.length) };
        }
    }
    return null;
}

function buildTable() {
    // kind: 0 valid (stays), 1 refused, 2 mapped by a fixed distance, 3 mapped to text
    const ranges = [];
    const texts = [];
    const textIndex = new Map();
    let current = null;
    const push = (cp, kind, value) => {
        if (current && current.kind === kind && current.value === value && kind !== 3) {
            current.end = cp;
            return;
        }
        if (current && kind === 3 && current.kind === 3 && current.value === value) {
            current.end = cp;
            return;
        }
        current = { start: cp, end: cp, kind, value };
        ranges.push(current);
    };
    for (let cp = 0x80; cp <= 0x10ffff; cp++) {
        if (cp >= 0xd800 && cp <= 0xdfff) { push(cp, 1, 0); continue; }
        const ch = String.fromCodePoint(cp);
        const m = mappingOf(ch);
        if (m === null) { push(cp, 1, 0); continue; }
        if (m.to === ch) { push(cp, 0, 0); continue; }
        const points = [...m.to];
        if (points.length === 1) { push(cp, 2, points[0].codePointAt(0) - cp); continue; }
        if (!textIndex.has(m.to)) { textIndex.set(m.to, texts.length); texts.push(m.to); }
        push(cp, 3, textIndex.get(m.to));
    }
    return { ranges, texts };
}

/** The code points for which a probe host is accepted, as ranges. */
function probeSet(candidates, makeLabel) {
    const out = [];
    for (const cp of candidates) {
        if (hostOf(`${makeLabel(String.fromCodePoint(cp))}.x`) === null) continue;
        const last = out[out.length - 1];
        if (last && last[1] === cp - 1) last[1] = cp;
        else out.push([cp, cp]);
    }
    return out;
}

function writeTable() {
    const { ranges, texts } = buildTable();
    const valid = [];
    for (const r of ranges) {
        if (r.kind !== 0) continue;
        for (let cp = r.start; cp <= r.end; cp++) valid.push(cp);
    }
    // A zero width joiner is only allowed after a virama.
    const viramas = probeSet(valid, (c) => `q${c}${ZWJ}q`);
    const isVirama = (cp) => viramas.some(([a, b]) => cp >= a && cp <= b);
    // A zero width non-joiner is also allowed between a character that
    // joins to the left and one that joins to the right.
    const joinsLeft = probeSet(valid.filter((cp) => !isVirama(cp)), (c) => `${ALEF}${c}${ZWNJ}${BEH}`);
    const joinsRight = probeSet(valid, (c) => `${BEH}${ZWNJ}${c}${ALEF}`);
    // A label must not start with a combining mark. Every other valid
    // character is accepted as a label of its own.
    const alone = probeSet(valid, (c) => c);
    const isAlone = new Set();
    for (const [a, b] of alone) for (let cp = a; cp <= b; cp++) isAlone.add(cp);
    const leadingMarks = [];
    for (const cp of valid) {
        if (isAlone.has(cp)) continue;
        const last = leadingMarks[leadingMarks.length - 1];
        if (last && last[1] === cp - 1) last[1] = cp;
        else leadingMarks.push([cp, cp]);
    }
    // The direction of every valid character, as far as the rule for
    // right-to-left labels tells them apart (see idnaLabelValid in Go):
    //   1 a right-to-left letter       refused between two Latin letters, fine before a digit
    //   2 an Arabic digit              refused between two Latin letters and before a European digit
    //   3 a left-to-right letter       refused between two Hebrew letters
    //   4 a European digit             refused between a Hebrew letter and an Arabic digit
    //   5 a mark                       fine at the end of a Hebrew label
    //   0 neutral                      fine inside a Hebrew label, not at its end
    //   6 other                        refused in every label that has a right-to-left character
    const ok = (label) => hostOf(`${label}.x`) !== null;
    const classes = [];
    for (const cp of valid) {
        const c = String.fromCodePoint(cp);
        let cls;
        if (!ok(`a${c}a`)) cls = ok(`${ALEF}${c}1`) ? 1 : 2;
        else if (!ok(`${ALEF}${c}${ALEF}`)) {
            // a left-to-right letter may start a label that ends in a Hebrew letter
            const first = ok(`${c}${ALEF}`);
            const inside = ok(`a${c}${ALEF}`);
            // (a spacing mark with that direction may not start any label, but counts as one inside)
            if (first !== inside && (first || isAlone.has(cp))) throw new Error(`unexpected direction of U+${cp.toString(16)}`);
            cls = inside ? 3 : 6;
        }
        else if (!ok(`${ALEF}${c}\u0661`)) cls = 4;
        else cls = ok(`${ALEF}${c}`) ? 5 : 0;
        // Runs go from one valid character to the next of the same kind;
        // what lies between is never looked up.
        const last = classes[classes.length - 1];
        if (last && last[2] === cls) last[1] = cp;
        else classes.push([cp, cp, cls]);
    }
    const directions = classes.filter((run) => run[2] !== 3); // left-to-right letters are the default

    const hex = (n) => `0x${n.toString(16)}`;
    const lines = [];
    lines.push('// Code generated by testdata/nodeoracle/idna.js. DO NOT EDIT.');
    lines.push('');
    lines.push('package branding');
    lines.push('');
    lines.push(`// What Node ${process.version} (URL parser ada ${process.versions.ada}, Unicode ${process.versions.unicode}) makes of every`);
    lines.push('// character above ASCII in a host name. idnaRanges holds, for each run of');
    lines.push('// characters that are treated alike, its first character and what');
    lines.push('// happens to the run: the low two bits are idnaValid, idnaRefused,');
    lines.push('// idnaShift (the rest is the distance to the character it becomes) or');
    lines.push('// idnaText (the rest is an index into idnaTexts).');
    lines.push('var idnaRanges = [...][2]int32{');
    let row = [];
    for (const r of ranges) {
        row.push(`{${hex(r.start)}, ${(r.value * 4) + r.kind}}`);
        if (row.length === 6) { lines.push(`\t${row.join(', ')},`); row = []; }
    }
    if (row.length) lines.push(`\t${row.join(', ')},`);
    lines.push('}');
    lines.push('');
    lines.push('// idnaTexts are the texts some characters become: nothing for a character');
    lines.push('// that is dropped, several characters for a ligature or a symbol.');
    lines.push('var idnaTexts = [...]string{');
    for (const t of texts) {
        lines.push(`\t"${[...t].map((c) => { const cp = c.codePointAt(0); return cp < 0x80 && c !== '"' && c !== '\\' ? c : (cp > 0xffff ? `\\U${cp.toString(16).padStart(8, '0')}` : `\\u${cp.toString(16).padStart(4, '0')}`); }).join('')}",`);
    }
    lines.push('}');
    const set = (name, comment, list) => {
        lines.push('');
        for (const c of comment) lines.push(`// ${c}`);
        lines.push(`var ${name} = [...][2]int32{`);
        let line = [];
        for (const [a, b] of list) {
            line.push(`{${hex(a)}, ${hex(b)}}`);
            if (line.length === 6) { lines.push(`\t${line.join(', ')},`); line = []; }
        }
        if (line.length) lines.push(`\t${line.join(', ')},`);
        lines.push('}');
    };
    set('idnaViramas', ['idnaViramas are the characters after which a zero width joiner or', 'non-joiner is allowed.'], viramas);
    set('idnaJoinsLeft', ['idnaJoinsLeft are the characters that may stand somewhere before a zero', 'width non-joiner.'], joinsLeft);
    set('idnaJoinsRight', ['idnaJoinsRight are the characters that may stand somewhere after a zero', 'width non-joiner.'], joinsRight);
    set('idnaLeadingMarks', ['idnaLeadingMarks are the combining marks a label must not start with.'], leadingMarks);
    lines.push('');
    lines.push('// idnaDirections gives the direction of the valid characters that are not');
    lines.push('// left-to-right letters, as runs of first character, last character and');
    lines.push('// one of the idnaDir values.');
    lines.push('var idnaDirections = [...][3]int32{');
    let dirLine = [];
    for (const [a, b, cls] of directions) {
        dirLine.push(`{${hex(a)}, ${hex(b)}, ${cls}}`);
        if (dirLine.length === 5) { lines.push(`\t${dirLine.join(', ')},`); dirLine = []; }
    }
    if (dirLine.length) lines.push(`\t${dirLine.join(', ')},`);
    lines.push('}');
    lines.push('');
    const file = path.join(__dirname, '..', '..', 'idna_table.go');
    fs.writeFileSync(file, lines.join('\n'));
    const perDirection = {};
    for (const [a, b, cls] of classes) perDirection[cls] = (perDirection[cls] || 0) + 1;
    return {
        file, ranges: ranges.length, texts: texts.length, viramas: viramas.length, joinsLeft: joinsLeft.length, joinsRight: joinsRight.length,
        leadingMarks: leadingMarks.length, directionRuns: perDirection,
        other: classes.filter((run) => run[2] >= 6).slice(0, 30).map(([a, b, cls]) => `${a.toString(16)}-${b.toString(16)}:${cls}`).join(' '),
    };
}

// ---- vectors ----

const ALPHABET = [
    // Latin and friends
    'a', 'b', 'z', 'A', 'Z', '0', '1', '9', '-', '_', 'q', 'xn--', 'xn--9ca', 'xn--bcher-kva', 'xn--', 'XN--MGBH0FB', 'xn--a', 'xn--0', 'xn--abc-', 'xn---', 'xn--ls8h', 'xn--zca', 'xn--ss',
    '\u00e4', '\u00c4', '\u00e9', '\u00df', '\u1e9e', '\u03c2', '\u03a3', '\u03c3', '\u0131', '\u0130', '\u017f', '\u212a', '\u00b5', '\u2126', '\ufb01', '\u00bd', '\u2460', '\u33a2',
    // marks, joiners, things that are dropped
    '\u0301', '\u0308', '\u0327', '\u20d7', '\u094d', '\u0d4d', '\u1039', ZWNJ, ZWJ, '\u00ad', '\u200b', '\u2060', '\ufeff', '\ufe0f', '\u034f', '\u180b',
    // other scripts
    '\u4f60', '\u597d', '\u3042', '\u30a2', '\uff71', '\uac00', '\u0915', '\u0937', '\u0e01', '\u0434', '\u0414', '\u03b1',
    // right to left
    ALEF, '\u05d1', '\u05b7', BEH, '\u0627', '\u0645', '\u0661', '\u0669', '\u06f1', '\u064b', '\u0640', '\u0710', '\u07ca', '\ua872', '\u0622', '\ufb50', '\ufe8e',
    '\u1820', '\u1807', '\u0e3a', '\u08d2', '\u1ac1', '\u0600', '\u06dd', '\u200f', '\u061c', '\u202e', '\ud803\udc00', '\ud83a\udd00', '\ud802\udc00',
    // dots, digits, symbols, width forms
    '.', '..', '\u3002', '\uff0e', '\uff61', '\uff11', '\uff21', '\uff41', '\uff0f', '\uff05', '\uff20', '\u2024', '\u2488', '\u00b9', '\u2075',
    '\ud83d\udc4d', '\ud83c\uddfa', '\u2764', '\u00a9', '\u2122', '\u00a0', '\u2028', '\u3000', '\ufffd', '\ud800\udc00', '\ud835\udc00', '\ud83c\udd50', '\u1f48', '\u0345', '\u1e9b', '\u2c00', '\ua7ae', '\u10a0', '\ua640',
];

function hostVectors() {
    const r = lib.rng(4242);
    const hosts = new Set();
    for (const a of ALPHABET) {
        hosts.add(a);
        hosts.add(`a${a}`);
        hosts.add(`${a}a`);
        hosts.add(`a${a}b.com`);
        hosts.add(`${a}.com`);
        hosts.add(`www.${a}${a}.example`);
        hosts.add(`${ALEF}${a}${ALEF}`);
        hosts.add(`${BEH}${a}${BEH}.${ALEF}${ALEF}`);
        hosts.add(`1${a}`);
        hosts.add(`${a}1`);
    }
    while (hosts.size < 14000) {
        const labels = [];
        const n = r.pick([1, 1, 2, 2, 3, 4]);
        for (let i = 0; i < n; i++) labels.push(r.some(ALPHABET, r.pick([1, 2, 3, 5, 8])).join(''));
        let host = labels.join('.');
        if (r.chance(0.05)) host = encodeURIComponent(host);
        if (r.chance(0.05)) host += '.';
        hosts.add(host);
    }
    // some code points at random, alone and in company
    while (hosts.size < 20000) {
        let cp = r.pick([r.int(0x3000), r.int(0x10000), 0x10000 + r.int(0x20000), 0xe0000 + r.int(0x200), r.int(0x110000)]);
        if (cp >= 0xd800 && cp <= 0xdfff) cp = 0xe9;
        if (cp < 0x80) cp += 0x100;
        const ch = String.fromCodePoint(cp);
        hosts.add(r.pick([ch, `a${ch}b`, `${ch}${ch}.com`, `${ALEF}${ch}`, `x.${ch}a`]));
    }
    return [...hosts];
}

function writeVectors() {
    const root = lib.scratchConsole('branding-idna');
    lib.stub(root, 'services/database.js', {});
    const branding = require(path.join(root, 'services/brandingService'));
    const out = [];
    for (const host of hostVectors()) {
        const input = lib.jsonClone(`https://${host}/x`);
        out.push({ a: [input], ...lib.record(() => branding.validateBrandingUrl(input), 100000) });
    }
    const info = lib.writeVectors('idna', { validateBrandingUrl: out });
    fs.rmSync(root, { recursive: true, force: true });
    return { ...info, vectors: out.length, accepted: out.filter((v) => v.r && v.r !== 'null').length };
}

const report = {};
if (process.argv[2] !== 'vectors') report.table = writeTable();
report.vectors = writeVectors();
console.log(JSON.stringify(report, null, 1));
