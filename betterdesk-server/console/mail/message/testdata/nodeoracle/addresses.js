'use strict';
// Runs nodemailer's address normalization (MimeNode._normalizeAddress, with
// Node's url.domainToASCII and url.domainToUnicode behind it) on many
// addresses and writes the results, so the Go tests can replay them.
// See README.md in this folder.
//
//   node addresses.js                    writes ../addresses.json.gz
//   node addresses.js --block 3 --out f  writes every result of one block of
//                                        the code point sweep to the file f
//   node addresses.js --mix 50000 --seed 7 --out f
//                                        writes that many more random addresses
//   node addresses.js --inputs in.json --out f
//                                        runs on a JSON array of strings
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');

const ROOT = process.env.NODEMAILER_DIR || '/repo/web-nodejs/node_modules/nodemailer';
const MimeNode = require(path.join(ROOT, 'dist/cjs/mime-node/index.js'));
const punycode = require(path.join(ROOT, 'dist/cjs/punycode/index.js'));
const node = new MimeNode();

// what the SMTP layer would put on the wire: half a surrogate pair is U+FFFD
const wire = str => Buffer.from(str, 'utf8').toString('utf8');
const normalize = address => wire(node._normalizeAddress(address));

// mulberry32: a small seeded generator, so every run builds the same inputs
function prng(seed) {
    let a = seed >>> 0;
    return () => {
        a = (a + 0x6d2b79f5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}

// --- the sweep: every code point in a few places of an address ------------

const BLOCK = 0x1000;
const BLOCKS = 0x110000 / BLOCK;
// keep in step with sweepTemplates in domain_test.go
const TEMPLATES = [
    c => 'a@' + c + '.de',
    c => 'a@x' + c + 'y.de',
    c => '\u{fc}@x' + c + 'y.de',
    c => 'a@' + c + ' x',
    c => '\u{fc}@' + c + ' x',
    c => '\u{fc}@a\u{3a3}' + c + ' x',
    c => '\u{fc}@' + c + '\u{3a3} x',
    c => c + '@example.com',
    c => 'a@xn--' + c + '.de',
    c => 'A' + c + '\u{e9}@B' + c + '.De'
];

function sweepBlock(block, each) {
    for (let cp = block * BLOCK; cp < (block + 1) * BLOCK; cp++) {
        if (cp >= 0xd800 && cp <= 0xdfff) {
            continue;
        }
        const chr = String.fromCodePoint(cp);
        for (const template of TEMPLATES) {
            const input = template(chr);
            each(input, normalize(input));
        }
    }
}

// --- addresses with their full results -------------------------------------

const inputs = [];
const seen = new Set();
function add(...list) {
    for (const input of list) {
        // Go gets its input as UTF-8, which has no half surrogate pairs
        if (!seen.has(input) && input.isWellFormed()) {
            seen.add(input);
            inputs.push(input);
        }
    }
}

const DOMAINS = [
    'example.com', 'EXAMPLE.COM', 'Example.Co.UK', 'localhost', 'a', 'a.', '.a', 'a..b', '.', '..', '', ' ',
    'sub.domain.example.org', 'a-b.example', '-a.example', 'a-.example', 'ab--cd.example', 'a_b.example',
    '1.2.3.4', '1.2.3', '1.2', '1', '0', '4294967295', '4294967296', '1.2.3.4.5', '1.2.3.4.', '1.2.3.4..', '1..2',
    '0x7f.1', '0x7f.0.0.1', '0X7F.0.0.1', '0x', '0x.0x.0x.0x', '0x.1', '1.0x', '0xg', '0xffffffff', '0x100000000',
    '0177.0.0.1', '08.1', '018', '00.00.00.00', '010.010.010.010', '1.2.3.256', '1.2.256.3', '256', '1.65536',
    '1.2.65536', '1.16777216', '999.1', 'foo.1', 'foo.0x1', 'foo.1.', '1.foo', '1e3', '1.e', '0x1.0x2.0x3.0x4',
    '127.1', '127.0.1', '1.1.1.1.', 'a.1.2.3.4', '01.02.03.04', '0.0.0.0', '255.255.255.255', '09', '0', '00', '0x0',
    '[::1]', '[::]', '[1::]', '[::1:2]', '[0:0:0:0:0:0:0:1]', '[1:2:3:4:5:6:7:8]', '[1:2:3:4:5:6:7:8:9]', '[1:2:3:4:5:6:7]',
    '[2001:DB8::1]', '[2001:db8:0:0:1:0:0:1]', '[2001:0db8:0000:0000:0000:0000:0000:0001]', '[1:0:0:2:0:0:0:3]',
    '[1:0:0:0:2:0:0:3]', '[0:0:1:0:0:1:0:0]', '[::ffff:1.2.3.4]', '[::1.2.3.4]', '[1:2:3:4:5:6:1.2.3.4]',
    '[1:2:3:4:5:6:7:1.2.3.4]', '[::1.2.3]', '[::1.2.3.4.5]', '[::01.2.3.4]', '[::1.2.3.256]', '[::1.2.3.4x]', '[::.1.2.3]',
    '[1.2.3.4]', '[127.0.0.1]', '[]', '[', ']', '[:]', '[:::]', '[::1', '::1]', '::1', '[::1]x', 'x[::1]', '[::g]',
    '[12345::]', '[1::2::3]', '[::1]:25', 'a:b', 'a:25', '[a@b]', '[a@b@c]', '[x@evil.com]', '[a]b]', '[a\\]', '[a[b]',
    '[IPv6:::1]', '[ipv6:2001:db8::1]', '[1:2:3:4:5:6:7::]', '[::2:3:4:5:6:7:8]', '[1::8]', '[1:2::7:8]', '[0::0]', '[::0:0]',
    'm\u{fc}ller.de', 'M\u{dc}LLER.DE', 'stra\u{df}e.de', 'STRASSE.de', 'fa\u{df}.de', 'j\u{f5}geva.ee', 'b\u{fc}cher.example',
    'xn--mller-kva.de', 'XN--MLLER-KVA.DE', 'xn--MLLER-kva.de', 'xn--strae-oqa.de', 'xn--zca.de', 'xn--', 'xn--a', 'xn---',
    'xn--a-ecp.ru', 'xn--80ak6aa92e.com', 'xn--n28h.com', 'xn--e28h.com', 'xn--ab--c.d', 'xn--a.xn--b', 'xn--mller-kva.1',
    'xn--1ch.com', 'xn--pple-43d.com', 'xn--bcher-kva.example', 'xn--bcher-kva.xn--', 'xn-a.com', 'axn--b.com', 'a.xn-',
    'xn--0.com', 'xn--00.com', 'xn--a0.com', 'xn--99999999999.com', 'xn--zzzzzzzzzzzzzzzzzzzz.com', 'xn--kva.de', 'xn--mller-KVA.de',
    'xn--m\u{fc}ller.de', 'xn--\u{fc}.de', 'x\u{fc}n--a.de', 'xn--caf-dma.com', 'xn--cafe-yvc.com', 'xn--9hb.com', 'xn--1-zhc.com',
    '\u{3c3}\u{3c2}.gr', '\u{3a3}\u{3a3}.gr', '\u{3a3}.gr', 'a\u{3a3}.gr', '\u{39f}\u{394}\u{39f}\u{3a3}.gr', '\u{39f}\u{394}\u{39f}\u{3a3}', '\u{130}stanbul.tr', 'I\u{307}.tr',
    'compa\u{ad}ny.com', 'a\u{200d}b.com', 'a\u{200c}b.com', '\u{200d}.com', '\u{915}\u{94d}\u{200d}\u{937}.com', '\u{915}\u{94d}\u{200c}\u{937}.com',
    '\u{646}\u{200c}\u{645}.com', '\u{646}\u{200c}.com', 'a\u{200c}.com', '\u{661}.com', '\u{5d0}1.com', '1\u{5d0}.com', '\u{5d0}a.com', 'a\u{5d0}.com',
    '\u{5d0}-\u{5d1}.com', '\u{5d0}\u{661}1.com', '\u{5d0}\u{661}.com', '\u{627}\u{644}\u{639}\u{631}\u{628}\u{64a}\u{629}.\u{645}\u{635}\u{631}', '\u{5e2}\u{5d1}\u{5e8}\u{5d9}\u{5ea}.\u{5d9}\u{5e9}\u{5e8}\u{5d0}\u{5dc}',
    '\u{4f8b}\u{3048}.\u{30c6}\u{30b9}\u{30c8}', '\u{4f8b}\u{5b50}\u{3002}\u{6d4b}\u{8bd5}', 'a\u{ff0e}b', 'a\u{ff61}b', '\u{ff41}\u{ff42}\u{ff43}.com', '\u{2121}.com', '\u{3392}.com', '\u{1c5}.com',
    '\u{212a}.com', '\u{1f600}.com', '\u{1f600}\u{1f600}.\u{1f600}', 'a\u{301}.com', '\u{301}a.com', 'a\u{308}b.com', 'e\u{301}.fr', '\u{e9}.fr', 'a\u{323}\u{301}.com',
    'a\u{301}\u{323}.com', '\u{1100}\u{1161}.kr', '\u{1100}\u{1161}\u{11a8}.kr', '\u{ac00}\u{11a8}.kr', '\u{ac01}\u{11a8}.kr', '\u{ac00}.kr', '\u{1100}\u{1161}\u{11a7}.kr',
    '\u{ff0f}.com', 'a\u{ff05}b.com', '\u{ff20}.com', 'a\u{2024}b.com', '\u{2488}.com', 'a\u{3002}', '\u{3002}a', 'a\u{2260}b.com', 'a\u{226e}b.com', '\u{a0}.com',
    'a b.com', 'a\tb.com', 'a/b.com', 'a\\b.com', 'a?b.com', 'a#b.com', 'a%41.com', 'a%2eb.com', 'a\u007fb.com', 'a\u0000b.com',
    'evil.example/mail.corp.example', 'a,b.com', 'a;b.com', 'a"b.com', 'a(b).com', 'a^b.com', 'a|b.com', 'a~b.com', 'a`b.com', 'a{b}.com',
    'a!b$c&d\'e*f+g=h.com', 'a@b', '@', 'a@', '@b', '\u{fffd}.com', '\u{e000}.com', '\u{fdd0}.com', '\u{ffff}.com', '\u{378}.com', '\u{2028}.com', '\u0085.com',
    'x'.repeat(63) + '.com', 'x'.repeat(64) + '.com', '\u{fc}'.repeat(63) + '.com', '\u{fc}'.repeat(64) + '.com',
    ('x'.repeat(60) + '.').repeat(4) + 'com', ('x'.repeat(62) + '.').repeat(4) + 'c', ('x'.repeat(62) + '.').repeat(4) + 'com',
    'x'.repeat(300), '\u{fc}'.repeat(300), ('\u{fc}'.repeat(50) + '.').repeat(6), 'xn--' + 'a'.repeat(70), '1.'.repeat(200) + '1',
    // too long for the fallback codec, which then leaves the name alone, and other names only it sees
    '\u{dc}'.repeat(64) + ' x', '\u{dc}'.repeat(61) + ' x', ('\u{fc}'.repeat(50) + '.').repeat(6) + ' x', 'XN---A.de', 'xn--a.b-', 'XN--A.de x', 'a\u{3a3}b x', "a'\u{3a3} x",
    'xn--a-.xn--a-rc4g x', 'xn--a-j023p x', 'xn--a-h023p x', 'xn--bcher-kva.xn--MLLER-kva.x y',
    '256.0x1.1.1', '255.0x1.1.1', '1.0x100.1.1', '[\u{fc}.x@b\u{fc}.c]', '\u{646}\u{200d}\u{645}.com', '\u{646}\u{200d}\u{645}\u{fefb}.com', '\u{646}\u{200c}\u{645}\u{fefb}.com',
];
const LOCALS = [
    'user', 'User.Name', 'first.last+tag', '"quoted"', '"quo ted"', '"a\\"b"', '"a"b"', '"unclosed', 'a b', 'a..b', '.a', 'a.', '',
    'a@b', '"a@b"', 'j\u{fc}rgen', '\u{7528}\u{6237}', '\u{1f600}', 'a\u{301}', 'a,b', 'a;b', 'a(b)', 'a[b]', 'a\\b', 'a:b', '"', '\\', '"\\"',
    'A', 'a!#$%&\'*+-/=?^_`{|}~z', 'a\u{a0}b', 'a\u{3000}b', '\u{a0}', 'a"b', '""', '"\u{fc}"', 'x'.repeat(70)
];
for (const domain of DOMAINS) {
    add('user@' + domain, 'j\u{fc}rgen@' + domain);
}
for (const local of LOCALS) {
    add(local, local + '@example.com', local + '@m\u{fc}ller.de', local + '@[::1]', local + '@1.2.3');
}
add('', ' ', '\t', '<>', '<a@b.c>', 'a@b.c\r\nBcc: x@y.z', 'a\r\n@b.c', 'a@b.c\u0000', '\u0000', 'a<b>c@d.e', '\u{a0}a@b.c\u{a0}', '\u{feff}a@b.c',
    ' a@b.c ', 'a @b.c', 'a@ b.c', 'a@b .c', 'a\u007f@b.c', '<<a>>@b.c', 'a@b.c>', '>a@b.c<', 'a\u0001\u0002\u0003b@c.d');

const written = inputs.length;
// message.js loads this file for its domains; the options are for a run of its own
const standalone = require.main === module;
const random = prng((standalone && Number(args('--seed'))) || 20261110);
const pick = list => list[Math.floor(random() * list.length)];
const ASCII_LABEL = () => {
    const chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_';
    let out = '';
    for (let n = 1 + Math.floor(random() * 8); n > 0; n--) {
        out += chars[Math.floor(random() * chars.length)];
    }
    return out;
};
const NUMBER = () => pick([
    () => String(Math.floor(random() * 300)),
    () => String(Math.floor(random() * 5e9)),
    () => '0x' + Math.floor(random() * 0x1ffff).toString(16),
    () => '0X' + Math.floor(random() * 0x1ff).toString(16).toUpperCase(),
    () => '0' + Math.floor(random() * 0x1ff).toString(8),
    () => '0' + Math.floor(random() * 99),
    () => '0x', () => '0', () => ''
])();
const UNICODE = [
    '\u{fc}', '\u{dc}', '\u{df}', '\u{1e9e}', '\u{e9}', '\u{c9}', 'e\u{301}', '\u{3c3}', '\u{3c2}', '\u{3a3}', '\u{130}', '\u{131}', 'I', 'i\u{307}', '\u{1c5}', '\u{212a}', '\u{2126}',
    '\u{430}', '\u{410}', '\u{451}', '\u{5d0}', '\u{5d1}', '\u{627}', '\u{628}', '\u{646}', '\u{645}', '\u{661}', '\u{6f1}', '\u{915}', '\u{94d}', '\u{937}', '\u{e01}', '\u{4f8b}', '\u{3048}',
    '\u{30c6}', '\u{ac00}', '\u{1100}', '\u{1161}', '\u{11a8}', '\u{1f600}', '\u{1f1fa}', '\u{200c}', '\u{200d}', '\u{ad}', '\u{301}', '\u{323}', '\u{308}', '\u{3002}', '\u{ff0e}',
    '\u{ff61}', '\u{ff41}', '\u{ff21}', '\u{2460}', '\u{b2}', '\u{2075}', '\u{bd}', '\u{fb01}', '\u{2160}', '\u{b5}', '\u{17f}', '\u{1fbe}', '\u{345}', '\u{aa}', '\u{2024}', '\u{fe52}',
    '\u{b7}', '\u{27}', '\u{2019}', '\u{a0}', '\u{3000}', '\u{2028}', '\u{fffd}', '\u{e000}', '\u{378}', '\u{1f1e6}', '\u{10400}', '\u{10428}', '\u{1d400}', '\u{e0061}', '\u{2f800}'
];
const UNICODE_LABEL = () => {
    let out = '';
    for (let n = 1 + Math.floor(random() * 5); n > 0; n--) {
        out += random() < 0.35 ? ASCII_LABEL().slice(0, 2) : pick(UNICODE);
    }
    return out;
};
const PUNY_LABEL = () => {
    const kind = random();
    if (kind < 0.5) {
        try {
            return 'xn--' + punycode.encode(UNICODE_LABEL());
        } catch (err) {
            return 'xn--a';
        }
    }
    if (kind < 0.7) {
        return 'XN--' + ASCII_LABEL();
    }
    return 'xn--' + ASCII_LABEL().toLowerCase() + pick(['', '-', '-a', '-' + ASCII_LABEL()]);
};
const IPV6 = () => {
    const pieces = [];
    for (let n = Math.floor(random() * 10); n > 0; n--) {
        pieces.push(pick([() => Math.floor(random() * 0x10000).toString(16), () => '0', () => '', () => '0000', () => 'FFFF', () => '1.2.3.4', () => String(Math.floor(random() * 300))])());
    }
    return '[' + pick(['', '', ':', '::']) + pieces.join(':') + pick(['', '', ':', '::', ':1.2.3.4']) + ']';
};
const LABEL = () => pick([ASCII_LABEL, ASCII_LABEL, ASCII_LABEL, NUMBER, NUMBER, UNICODE_LABEL, UNICODE_LABEL, PUNY_LABEL, () => ''])();
const DOMAIN = () => {
    if (random() < 0.07) {
        return IPV6();
    }
    const labels = [];
    for (let n = 1 + Math.floor(random() * 4); n > 0; n--) {
        labels.push(LABEL());
    }
    let domain = labels.join(pick(['.', '.', '.', '.', '.', '\u{3002}', '\u{ff0e}', '\u{ff61}']));
    if (random() < 0.08) {
        const junk = pick([' ', '/', '\\', '?', '#', '%', ':', '@', '[', ']', '<', '>', '\r\n', '\u0000', '\u007f', ',', ';', '"', '(', ')']);
        // between characters, never inside a surrogate pair
        const chars = Array.from(domain);
        const at = Math.floor(random() * (chars.length + 1));
        domain = chars.slice(0, at).join('') + junk + chars.slice(at).join('');
    }
    return domain;
};
for (let i = 0; i < ((standalone && Number(args('--mix'))) || 14000); i++) {
    const local = random() < 0.75 ? pick(['a', 'user', 'User', 'first.last']) : pick(LOCALS);
    add(local + '@' + DOMAIN());
}

// --- output ----------------------------------------------------------------

// JSON with everything outside printable ASCII escaped, one UTF-16 code unit at a time
const NOT_PRINTABLE_ASCII = /[^ -~]/g;
const BACKSLASH = String.fromCharCode(92);
function ascii(json) {
    return json.replace(NOT_PRINTABLE_ASCII, c => BACKSLASH + 'u' + c.charCodeAt(0).toString(16).padStart(4, '0'));
}
function args(name) {
    const at = process.argv.indexOf(name);
    return at < 0 ? undefined : process.argv[at + 1];
}

if (!standalone) {
    module.exports = { DOMAINS, LOCALS, DOMAIN };
} else if (args('--block') !== undefined) {
    const lines = [];
    sweepBlock(Number(args('--block')), (input, out) => lines.push(ascii(JSON.stringify({ in: input, out }))));
    fs.writeFileSync(args('--out'), lines.join('\n') + '\n');
    console.log(lines.length + ' results of block ' + args('--block') + ' written to ' + args('--out'));
} else if (args('--inputs') !== undefined) {
    const list = JSON.parse(fs.readFileSync(args('--inputs'), 'utf8'));
    fs.writeFileSync(args('--out'), list.map(input => ascii(JSON.stringify({ in: input, out: normalize(input) }))).join('\n') + '\n');
    console.log(list.length + ' results written to ' + args('--out'));
} else if (args('--mix') !== undefined) {
    const list = inputs.slice(written);
    fs.writeFileSync(args('--out'), list.map(input => ascii(JSON.stringify({ in: input, out: normalize(input) }))).join('\n') + '\n');
    console.log(list.length + ' results written to ' + args('--out'));
} else {
    const lines = [];
    for (let block = 0; block < BLOCKS; block++) {
        const hash = crypto.createHash('sha256');
        let count = 0;
        sweepBlock(block, (input, out) => {
            hash.update(out + '\n', 'utf8');
            count++;
        });
        lines.push(JSON.stringify({ block, count, sha256: hash.digest('hex') }));
    }
    for (const input of inputs) {
        lines.push(ascii(JSON.stringify({ in: input, out: normalize(input) })));
    }
    const out = path.join(__dirname, '..', 'addresses.json.gz');
    fs.writeFileSync(out, zlib.gzipSync(lines.join('\n') + '\n', { level: 9 }));
    console.log(BLOCKS + ' sweep blocks and ' + inputs.length + ' addresses written to ' + out);
}
