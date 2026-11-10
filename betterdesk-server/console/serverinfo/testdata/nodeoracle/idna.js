'use strict';

// Records what "new URL" makes of a host name with one character outside
// ASCII in it, for every such character of the Basic Multilingual Plane
// and a sample beyond it, and of a list of whole names. Writes
// idna.json.gz.
//
// The Go port has no table of Unicode's rules for domain names. Its test
// counts how many of these it answers differently.

process.noDeprecation = true;
const punycode = require('punycode');
const lib = require('./lib');

function hostOf(name) {
    try {
        return new URL(`http://${name}/`).hostname;
    } catch (_) {
        return null;
    }
}

const out = { inside: [], first: [], names: [] };

// One character between two letters, and one at the start of a label.
// Each entry is [from, to, kind, answer]. Kind "x": every character of the
// run is refused. Kind "v": every character of the run is kept as it is,
// so the answer is "xn--" and the Punycode of the label. Kind "m": the
// character is mapped to something else or left out; the answer is given.
function sweep(target, label) {
    let run = null;
    const flush = () => { if (run) target.push(run); run = null; };
    const visit = (cp) => {
        const text = label(String.fromCodePoint(cp));
        const answer = hostOf(`${text}.example`);
        let kind = 'm';
        if (answer === null) kind = 'x';
        else if (answer === `xn--${punycode.encode(text)}.example`) kind = 'v';
        if (run && kind !== 'm' && run[2] === kind && run[1] === cp - 1) {
            run[1] = cp;
            return;
        }
        flush();
        run = [cp, cp, kind, kind === 'm' ? answer : null];
    };
    for (let cp = 0x80; cp <= 0xFFFF; cp++) {
        if (cp >= 0xD800 && cp <= 0xDFFF) continue;
        visit(cp);
    }
    // Beyond the Basic Multilingual Plane: all of plane 1, then every 257th.
    for (let cp = 0x10000; cp <= 0x10FFFF; cp += (cp < 0x20000 ? 1 : 257)) visit(cp);
    flush();
}
sweep(out.inside, (c) => `a${c}b`);
sweep(out.first, (c) => `${c}a`);

const NAMES = [
    'b\u00fccher.example', 'B\u00dcCHER.example', 'm\u00fcnchen.de', 'fa\u00df.de', 'FA\u1e9e.de', '\u03c3\u03c5\u03bd\u03c2.gr', '\u03a3\u03a5\u039d\u03a3.gr',
    '\u65e5\u672c\u8a9e.jp', '\u043f\u0440\u0438\u043c\u0435\u0440.\u0440\u0444', '\u041f\u0420\u0418\u041c\u0415\u0420.\u0420\u0424', '\u2603.net', '\u{1f600}.example',
    '\uff45\uff58\uff41\uff4d\uff50\uff4c\uff45\uff0e\uff43\uff4f\uff4d', 'a\u3002b', 'a\uff61b', 'a\u00adb.example', '\u00ad.example', '\u00ad',
    'e\u0301.example', '\u00e9.example', '\u0301e.example', 'a.\u0301e', 'caf\u00e9.example', 'CAF\u00c9.EXAMPLE',
    '\u05d0\u05d1.example', '\u05d0a.example', 'a\u05d0.example', '1\u05d0.example', '\u05d01.example', '\u05d0\u0661\u06f1.example', '\u0627\u0644\u0639\u0631\u0628\u064a\u0629.example',
    '\u0661\u0662\u0663.example', 'a.\u05d0.b', '\u05d0-\u05d1.example', '\u05d0_.example', '\u05d0\u0301.example',
    'a1\u05d0', 'a\u05d0\u05d1', 'a\u05d01', '\u05d01a', '\u05d0-', '-\u05d0', 'a\u0628', 'a\u0661', '\u0661a', '\u0661', '\u05d0\u0661', '\u06f1\u05d0', 'a\u0300\u05d0',
    'a\u05d0\u0300', '\u05d0\u0300', '_\u05d0', '\u05d0_', 'a\u05d0b', 'a-\u05d0', 'ab\u05d0', 'abc\u05d0\u0300\u0300', '\u05d0a\u05d0', '\u05d0ab', '\u05d0\u06f1\u0661\u05d0',
    '\u05d0\u0661a\u05d0', '1\u0661', '\u06611', '\u0661\u05d0', '-\u0661', '\u05d0\u200e', '\u00e9\u05d0', '\u05d0\u00e9', '\u00e9\u0661', '\u05d0\u05d1\u0300\u0301',
    '\u0300', 'a\u0300', '\u05d0\u0661\u0300', '\u05d0(\u05d1)', '\u05d0+\u05d1', '\u05d0,\u05d1', '\u05d0%25\u05d1', '\u05d0$\u05d1', '\u05d0\u00a9\u05d1',
    'a\u200cb.example', 'a\u200db.example', '\u0915\u094d\u200d\u0937.example', '\u0915\u094d\u200c\u0937.example', '\u0628\u200c\u0628.example',
    '\u0646\u0627\u0645\u0647\u200c\u0627\u06cc.example', '\u200c.example', 'a\u200c.example', '\u200ca.example', '\u0915\u094d\u200d', '\u0915\u200d\u0937',
    'xn--bcher-kva.example', 'XN--BCHER-KVA.example', 'xn--bcher-kva.\u00fc', 'xn--a.\u00fc', 'xn--.\u00fc', 'xn--a-.\u00fc', 'xn--b\u00fccher.example',
    'xn--b\u00fc', 'xn--\u00fc', 'XN--\u00fc', 'xn-\u00fc', 'ab--\u00fc', '\u00fc--b', '-\u00fc', '\u00fc-', '--\u00fc',
    'xn--fa-hia.de', 'xn--zca.de', 'xn--nxasmq6b', 'xn--mnchen-3ya', 'xn--mnchen-3yA', 'xn--MNCHEN-3ya', 'xn--e-ufa.example', 'xn--ecb.example',
    'xn--9ca.example', 'xn--tda.example', 'xn--1ug.example', 'xn--0ca.example', 'xn--a', 'xn--a-', 'xn--xn--', 'xn---', 'xn----', 'xn--a-b', 'xn--ab-',
    'xn--80akhbyknj4f', 'xn--p1ai', 'xn--j1amh', 'xn--ls8h', 'xn--7ca', 'xn--99999999', 'xn--zzzzzzzz', 'xn--0', 'xn--00', 'xn--ab', 'xn--abc',
    'xn--4db', 'xn--a-0hc', 'xn--4db.a', 'xn--a-0hc.xn--4db', 'xn--1-zhc', 'xn--ksa35l', 'xn--b-0hc0a', 'xn--9hb', 'xn--mgbh0fb', 'xn--ss-', 'xn--bb-eka',
    'xn--bcher-KVA', 'xn--BCHER-kva', 'xn--b\u00fccher-kva', 'xn--d1acufc', 'xn--D1ACUFC', 'xn--80a', 'xn--dkb', 'xn--uxa', 'xn--a-gmb',
    '\u00fc.xn--a-', '\u00fc..\u00e4', '.\u00fc', '\u00fc.', '\u00fc..', '\u00fc.1', '1.\u00fc', '\u00fc.0x1', '\uff11.\uff12.\uff13.\uff14', '\uff10\uff58\uff17\uff46.1',
    '\u00fc%2e\u00e4', '%c3%bc.example', '%C3%BC.example', '%fc.example', '%c3.example', '\u00fc%41.example', 'a%c2%adb.example',
    '\u2460.example', '\u00bd.example', '\u2122.example', '\u2100.example', '\u00a0.example', 'a\u00a0b.example', '\u2024.example', '\u3000.example',
    '\ufb00.example', '\u01c4.example', '\u1e9e.example', '\u017f.example', '\u212a.example', '\u0130.example', '\u0131.example', '\u2126.example',
    '\u{1d400}.example', '\u{1f1e9}\u{1f1ea}.example', '\u0338.example', 'a\u0338.example', '\u226e.example', '<\u0338.example',
    '\u13f8.example', '\uab8e.example', '\u13a0.example', '\u04c0.example', '\u10a0.example', '\u10c7.example', '\u2d00.example', '\u1c90.example',
    'a'.repeat(63) + '\u00fc.example', '\u00fc'.repeat(64) + '.example', '\u00fc'.repeat(300) + '.example', ('\u00fc'.repeat(60) + '.').repeat(6) + 'example',
    '\u4e2d'.repeat(200), '\u{1f600}'.repeat(100),
];
out.names = NAMES.map((name) => ({ name, host: hostOf(name) }));

// Labels given in their "xn--" form that would not have come out that way:
// a capital letter, a letter with a combining mark that has a composed
// form, a full-width letter, a soft hyphen, a capital sharp s, an angstrom
// sign, an ideographic full stop. And three that are fine as they are.
for (const text of [[0xC9], [0x65, 0x301], [0xFF41], [0x61, 0xAD, 0x62], [0x1E9E], [0x212B], [0x4E2D, 0x3002], [0x3A3, 0x3B1],
    [0xDF], [0xE9], [0x3C3, 0x3B1], [0x4E2D], [0x61, 0x308], [0x1100, 0x1161], [0xAC00]]) {
    const name = `xn--${punycode.encode(String.fromCodePoint(...text))}.example`;
    out.names.push({ name, host: hostOf(name) });
}

lib.write('idna', out);
lib.writeCounts('idna.js');
