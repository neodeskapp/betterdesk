'use strict';
// Runs nodemailer's address parser on many inputs and writes what it returns,
// so the Go tests can replay it. See README.md in this folder.
//
//   node addressparser.js                       writes ../addressparser.json
//   node addressparser.js --inputs in.json --out out.json
//                                               runs on a JSON array of strings
const fs = require('fs');
const path = require('path');

const MODULE = process.env.NODEMAILER_ADDRESSPARSER || '/repo/web-nodejs/node_modules/nodemailer/dist/cjs/addressparser/index.js';
const addressparser = require(MODULE);

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

const inputs = [];
const seen = new Set();
function add(...list) {
    for (const input of list) {
        if (!seen.has(input)) {
            seen.add(input);
            inputs.push(input);
        }
    }
}

// --- written by hand -------------------------------------------------------

add(
    '',
    ' ',
    'user@example.com',
    ' user@example.com ',
    'user@example.com, other@example.com',
    'user@example.com; other@example.com',
    'user@example.com,other@example.com;third@example.com',
    'user@example.com,',
    ',user@example.com',
    ',,,',
    ';;;',
    ', ; , ;',
    'Joe Foo <joe@example.com>',
    'Joe Foo<joe@example.com>',
    '<joe@example.com> Joe Foo',
    '<joe@example.com>',
    '<>',
    '< >',
    'Name <>',
    '<> Name',
    'joe@example.com (Joe Foo)',
    '(Joe Foo) joe@example.com',
    'joe@example.com (Joe (nested) Foo)',
    'joe@example.com (Joe \\) Foo)',
    'joe@example.com (Joe \\( Foo)',
    'joe@example.com (unclosed',
    'joe@example.com (unclosed (twice',
    '(only a comment)',
    '(a@evil.com)',
    '()',
    '(()',
    '())',
    'Joe Foo, PhD <joe@example.com>',
    'Foo, Bar, Baz <joe@example.com>',
    'Foo, Bar, Baz <joe@example.com>, Qux, Quux <q@example.com>',
    'Foo, <joe@example.com>',
    'Foo, joe@example.com',
    'Foo, Bar joe@example.com',
    'Foo, Bar, joe@example.com, Baz <b@example.com>',
    '"Joe Foo" <joe@example.com>',
    '"Foo, Joe" <joe@example.com>',
    '"Foo; Joe" <joe@example.com>',
    '"Foo: Joe" <joe@example.com>',
    '"Joe \\"Quoted\\" Foo" <joe@example.com>',
    '"Joe \\\\ Foo" <joe@example.com>',
    '"Joe Foo <joe@example.com>',
    '"Joe Foo\\" <joe@example.com>',
    '"" <joe@example.com>',
    '""',
    '"" ""',
    '"a" "b" <joe@example.com>',
    '"a""b" <joe@example.com>',
    '"Display Name" <(a comment)>',
    '"user@evil.com"@good.com',
    '"user@evil.com" @good.com',
    '"user@evil.com" @ good.com',
    '"user@evil.com"@ good.com',
    '<"user@evil.com"@good.com>',
    '"user name"@example.com',
    '<"user name"@example.com>',
    '<"user name"@example.com junk>',
    '<"user name"@example.com  >',
    '<junk "user@evil.com b"@good.com>',
    '"user"@example.com',
    '"us,er"@example.com',
    '"us;er"@example.com',
    '"us\\"er"@example.com',
    '"us\\\\er"@example.com',
    '""@example.com',
    '"" @example.com',
    'x""@example.com',
    '""@',
    '""@[1.2.3.4]',
    '"a"@[1.2.3.4]',
    '"a"@[x@y]',
    '"a"@b@c',
    '"a"@"b"',
    '"a"."b"@example.com',
    'a."b"@example.com',
    '"a".b@example.com',
    '"user"@example.com(x)evil.com',
    '"user"@example.com (x) evil.com',
    'user@example.com(x)evil.com',
    'Name <user@example.com(x)evil.com>',
    'user(x)@example.com',
    'user@(x)example.com',
    'user(x)@(y)example.com',
    'user (x) @ (y) example.com',
    '<user(x)@(y)example.com>',
    '<a(x)(y)@b>',
    '<a(x) (y)@b>',
    '<a(x)b@c>',
    '<a@b(x)>',
    '<(x)a@b>',
    '<(x)>',
    '<(x)(y)>',
    '<( x )> Name',
    '<a@b(unclosed>',
    '<a@b(un(closed)>',
    '<a@b (x \\) y)>',
    'user @ good.com',
    'user @good.com',
    'user@ good.com',
    'user  @\tgood.com',
    'Name user @ good.com',
    'x@evil.com @good.com',
    '<x@evil.com @good.com>',
    '<user @ good.com>',
    '<user@example.com user@example.com>',
    '<example.com user@example.com>',
    '<a@b@c.com junk>',
    'a@b@c.com junk',
    '<junk a@b@c.com>',
    '<junk more junk>',
    '<a b c>',
    '<a@b c@d>',
    '< a@b >',
    'user@good.com (x) evil@evil.com',
    'user@good.com evil@evil.com',
    'user@good.com <evil@evil.com>',
    '<user@good.com> <evil@evil.com>',
    '<user@good.com> evil@evil.com',
    '<user@good.com><evil@evil.com>',
    'Name <a@b> more <c@d> text',
    '<<a@b>>',
    '<a<b@c>',
    '<a@b<c@d>',
    '<a@b>>',
    '<a@b',
    'a@b>',
    'Name <a@b',
    'Name <a@b, c@d',
    'Name <a@b; c@d',
    '<"a<b"@victim.com>',
    '<"a@evil.com>"@good.com>',
    '<"a>"@b>',
    '<a@[1.2.3.4]>',
    '<a@[>]>',
    '<a@[x>y]>',
    '<a[>]@b>',
    '<a(>)@b>',
    '<a(\\)>)@b>',
    '<@relay1,@relay2:user@example.com>',
    '<@relay1:user@example.com>',
    '<@relay1:user>',
    '<@relay1:>',
    '<@[1:2]:user@example.com>',
    '<"@relay":user@example.com>',
    '<a:user@example.com>',
    '< @relay1 : user@example.com >',
    '<@a:@b:c@d>',
    'user@[127.0.0.1]',
    'user@[IPv6:2001:db8::1]',
    'Name <user@[IPv6:2001:db8::1]>',
    'user@[IPv6:2001:db8::1], other@example.com',
    'user@[IPv6:2001:db8::1; other@example.com',
    'user@[unclosed, other@example.com',
    'user@[unclosed: other@example.com;',
    'user@ [1.2.3.4]',
    'user@(c)[1:2]',
    'a@\t[1:2]',
    'a@ [1:2]',
    'a@\t\t[IPv6:2001:db8::1], c@d',
    'a@\n[1:2]',
    'a@\r[1:2]',
    'a@\r\n [1:2]',
    'a@' + String.fromCharCode(0xa0) + '[1:2]',
    'a@' + String.fromCharCode(0x2028) + '[1:2]',
    'a@' + String.fromCharCode(0) + '[1:2]',
    '<a@\t[x>y]>',
    '<a@' + String.fromCharCode(0xa0) + '[x>y]>',
    'a@b@c "x"',
    'a@b@c (Name)',
    'a@b (Name)',
    'x (c) a@b@c',
    '(c) a@b@c x',
    'x,x<x,()x',
    'x,y<x, y>',
    String.fromCharCode(0xfffd) + ',' + String.fromCharCode(0xfffd) + '<' + String.fromCharCode(0xfffd) + ',()' + String.fromCharCode(0xfffd),
    'a@[x@evil.com]',
    'a@[x@evil.com',
    'a@[x]@evil.com]',
    'a@[[x]]',
    'a@[x\\]]',
    'a@[]',
    '[a]@b',
    'Name [x:y] <a@b>',
    '[ ( ] <a@victim.com> ) <b@evil.com>',
    '[x]: a@b;',
    'Group: a@example.com, b@example.com;',
    'Group:a@example.com,b@example.com;',
    'Group: ;',
    'Group:;',
    'Group:',
    'Group: a@example.com',
    'Group: a@example.com, b@example.com',
    ':',
    '::',
    ':;',
    ';:',
    ': a@b;',
    ':a@b',
    'Group: a@b; c@d',
    'Group: a@b;, c@d',
    'Group: a@b; Other: c@d;',
    'One: a@b; Two: ; Three: c@d, e@f;',
    'Outer: Inner: a@b; c@d;',
    'Outer: a@b, Inner: c@d, e@f; g@h;',
    'A: B: C: D: a@b;;;;',
    'Group: Joe Foo <a@b>, "Foo, Bar" <c@d>, Baz, Qux <e@f>;',
    'Group: Baz, Qux;',
    'Baz, Group: Qux <a@b>;',
    'Baz, Group: a@b;, Qux <a@b>',
    '"Quoted: Group": a@b;',
    '"Group": a@b;',
    '(comment) : a@b;',
    '(comment) Group (other): a@b;',
    'Group <x@y>: a@b;',
    'Group: <a@b; c@d>',
    'Group: <a@b; c@d',
    'Group: "a@b; c@d',
    'Group: (a@b; c@d',
    'Group: Name <a@b;>, c@d',
    '<a@b; c@d>',
    'undisclosed-recipients:;',
    'Undisclosed recipients: ;',
    'a@b:c@d',
    'http://example.com',
    'mailto:user@example.com',
    'a:b:c',
    'a@b.c:25',
    "O'Neill <o@example.com>",
    "'single' <a@b>",
    "'a@b'",
    "'a@b' <c@d>",
    'a\\@b',
    'a\\,b@c',
    '\\',
    '\\\\',
    '"\\',
    '"\\"',
    '"\\\\"',
    '(\\',
    '(\\)',
    '(\\\\)',
    '"a\\',
    '"a\\"b"@c',
    '"\\a"@b',
    '"\\""@b',
    '"\\\\"@b',
    '"\\\\\\""@b',
    'a@b\\',
    '@',
    '@@',
    '@@@',
    '@b',
    'a@',
    'a@@b',
    'a@b@c',
    'a@b@c@d',
    '@a@b',
    'a @ b @ c',
    'a@ b@ c',
    'a @b @c',
    'Name @',
    'Name @b',
    'Name a@',
    '.@.',
    'a.@.b',
    '-@-',
    '_@_',
    'a_@_b',
    '!@!',
    '#$%@^&*',
    '=?UTF-8?B?SsO1Z2V2YQ==?= <a@b>',
    '=?UTF-8?Q?J=C3=B5geva?= <a@b>',
    'J\u00f5geva <jogeva@example.com>',
    'j\u00f5geva@example.com',
    'user@j\u00f5geva.ee',
    '\u00f5@\u00f5',
    '\u00f5a@b',
    'a\u00f5@b\u00f5',
    '\u65e5\u672c\u8a9e <nihongo@example.jp>',
    '\u65e5\u672c@\u65e5\u672c.jp',
    '\ud83d\ude00 <smile@example.com>',
    '\ud83d\ude00@example.com',
    'a@\ud83d\ude00',
    'a@b\ud83d\ude00 c',
    '\ud83d\ude00a@b',
    'x \ud83d\ude00a@b\ud83d\ude00 y',
    '"\ud83d\ude00"@b',
    '"\\\ud83d\ude00"@b',
    '(\\\ud83d\ude00) a@b',
    '<a@b(\\\ud83d\ude00)>',
    'text a@b.c',
    'text a@b.c text',
    'a@b.c text',
    'text  a@b.c  text',
    'text .a@b.c. text',
    'text -a@b.c- text',
    'text a@b.c-- text',
    'text --a@b text',
    'text a@- text',
    'text -@a text',
    'text -@- text',
    'text a@b@c text',
    'text @a@b text',
    'text a@@b text',
    'text a@b@ text',
    'text x@ a@b text',
    'text @ a@b text',
    'one two a@b three c@d four',
    'one.two@three four.five@six',
    '"quoted a@b" c@d',
    '"quoted" a@b',
    '"a@b"',
    '"a@b" x',
    'x "a@b"',
    '"a@b" "c@d"',
    '"a b"@c d',
    '"a@b" (comment)',
    '"a@b" <>',
    '"a@b" <(c)>',
    '"a b@c d"',
    '" a@b "',
    '"a @ b"',
    '"a"@',
    '"a" @',
    '"a@"',
    '"@a"',
    '"@"',
    '"a"b@c',
    'a"b"@c',
    'a"b@c"',
    'a"b"c@d',
    'a "b" c@d',
    'a@"b"',
    'a@b"c"',
    'a@"b".c',
    '"a"@b "c"@d',
    '"a"@b, "c"@d',
    '"a"@b (x) "c"@d',
    '"a" (x) @b',
    '"a"(x)@b',
    '"a"@(x)b',
    'a (x) b',
    'a(x)b',
    'a(x)@b',
    'a@(x)b',
    'a(x)@(y)b(z)c',
    '(x)a@b(y)',
    '(x)(y)(z)',
    '(x) (y) a@b',
    'a@b (x) (y)',
    '(a) b (c) <d@e> (f)',
    '(a, b) c@d',
    '(a; b) c@d',
    '(a: b) c@d',
    '(a "b) c@d',
    '(a <b) c@d',
    '"a (b" c@d',
    '"a <b" c@d',
    'a@b\r\nBcc: x@y',
    'a@b\nBcc: x@y',
    'a@b\rBcc: x@y',
    'Name\r\nBcc: x@y <a@b>',
    'Name <a@b\r\nBcc: x@y>',
    '"Name\r\nBcc: x@y" <a@b>',
    '(Name\r\nBcc: x@y) <a@b>',
    'a@b\r\n\r\nbody',
    'a@b\r\n c@d',
    'a@b,\r\n c@d',
    'Name\r\n\t<a@b>',
    'Na\nme <a@b>',
    'Na\rme <a@b>',
    'a\n@b',
    'a@\nb',
    'a\r@b',
    'a\u0000@b',
    'a@b\u0000',
    '\u0000a@b',
    '\u0000',
    '\u0000\u0000',
    'Na\u0000me <a@b>',
    '"Na\u0000me" <a@b>',
    '<a\u0000@b>',
    '\u0001\u0002\u0003',
    'a\u0007b@c',
    'a\u001fb@c',
    'a\u007fb@c',
    'a\u0080b@c',
    'a\u0085b@c',
    '\u001f \u001f',
    '"\u0001"',
    '"\u0001" <a@b>',
    '"\n" <a@b>',
    '"\r" <a@b>',
    '"\\\n" <a@b>',
    '"a\\\nb" <a@b>',
    '(\n) a@b',
    '\ta@b\t',
    '\u00a0a@b\u00a0',
    '\u2028a@b\u2028',
    '\ufeffa@b\ufeff',
    '\ufeffName <a@b>',
    'a\u00a0@\u00a0b',
    'a\u2028@\u2028b',
    'a\ufeff@\ufeffb',
    'a\u3000@\u3000b',
    'a\u200b@\u200bb',
    'Name\u00a0<a@b>',
    'Name\u00a0 a@b',
    'text\u00a0a@b\u00a0text',
    'text\u2028a@b\u2029text',
    'text\u1680a@b\u205ftext',
    'text\u2000a@b\u200atext',
    'text\u202fa@b\u3000text',
    'text\u000ba@b\u000ctext',
    '<a@b\u00a0c@d>',
    '<\u00a0a@b\u00a0>',
    '<"a\u00a0b"@c\u00a0d>',
    '"a"@b\u00a0',
    '<"a"@b \u00a0>',
    '<"a"@b\u00a0\u00a0>',
    'a@b>\u00a0c',
    '"a"\u00a0b',
    '(a)\u00a0b',
    '<a@b>\u00a0<c@d>',
    'Group:\u00a0a@b;',
    '\u00a0,\u00a0',
    '\u00a0'
);

// --- every special character alone, in pairs, and around an address --------

const SPECIALS = ['"', "'", '(', ')', '<', '>', ',', ';', ':', '@', '[', ']', '\\', ' ', '\t', '\r', '\n', '\u00a0', '\u2028', '\ufeff', '\u0000', '\u0001', '\u007f', '.', '-', '_', 'a', '\u00e9', '\ud83d\ude00'];
for (const x of SPECIALS) {
    add(x, 'a' + x, x + 'a', 'a' + x + 'b', 'a@b' + x, x + 'a@b', 'a' + x + '@b', 'a@' + x + 'b', 'N ' + x + ' <a@b>', '<a' + x + '@b>', '<a@b' + x + '>', '"' + x + '"@b', '(' + x + ') a@b', 'G: a@b' + x + ' c@d;');
    for (const y of SPECIALS) {
        add(x + y, 'a' + x + y + 'b', x + 'a@b' + y, 'a' + x + '@' + y + 'b', '<a' + x + 'b@c' + y + 'd>');
    }
}
const CORE = ['"', '(', ')', '<', '>', ',', ';', ':', '@', '[', ']', '\\', ' ', 'a'];
for (const x of CORE) {
    for (const y of CORE) {
        for (const z of CORE) {
            add(x + y + z, '<' + x + y + z);
        }
    }
}

// --- seeded random strings --------------------------------------------------

const WORDS = ['a', 'b', 'c', 'user', 'Joe', 'Foo Bar', 'example.com', 'evil.com', 'user@example.com', 'x@y', 'a.b', '_x_', '1', '-', 'IPv6:2001:db8::1', '[127.0.0.1]', '\u00e9', '\u00fc\u00f1\u00ef', '\u65e5\u672c', '\ud83d\ude00', '"q"', '"q r"', '(c)', '<a@b.c>', 'Group:', '@[', '\\"', '\\\\', '\\(', '\\)', '""', '()', '<>'];
const SOUP = ['"', '"', "'", '(', ')', '<', '>', ',', ';', ':', '@', '@', '[', ']', '\\', ' ', ' ', '\t', '\r', '\n', '\r\n', '\u00a0', '\u2028', '\ufeff', '\u0000', '\u0001', '\u007f', '.', '-', '_', '=', '!', '%', '+', '/'];

const random = prng(0x5eed1234);
const pick = list => list[Math.floor(random() * list.length)];
const between = (low, high) => low + Math.floor(random() * (high - low + 1));

function soup(length, wordShare) {
    let text = '';
    for (let i = 0; i < length; i++) {
        text += random() < wordShare ? pick(WORDS) : pick(SOUP);
    }
    return text;
}
for (let i = 0; i < 2500; i++) {
    add(soup(between(1, 8), 0.2));
}
for (let i = 0; i < 2500; i++) {
    add(soup(between(2, 12), 0.5));
}
for (let i = 0; i < 1500; i++) {
    add(soup(between(12, 40), 0.4));
}

// address lists built by a grammar, some of them damaged afterwards
const NAMES = ['Joe', 'Joe Foo', 'Foo, Joe', '"Joe Foo"', '"Foo, Joe"', '"a@b"', '"Joe \\"J\\" Foo"', 'J\u00f5geva', '\u65e5\u672c \u8a9e', '\ud83d\ude00 Smile', "O'Neill", 'Dr. A. B.', '=?UTF-8?Q?x?=', '', '', 'x@y', 'a:b', 'A (B) C'];
const LOCALS = ['user', 'first.last', 'u+tag', '"quoted"', '"quo ted"', '"a@b"', '"a,b"', '""', 'u(c)', '(c)u', '\u00fc', '\ud83d\ude00', 'a b', '', '@relay:user', 'a@b'];
const DOMAINS = ['example.com', 'b', 'sub.example.co.uk', '[127.0.0.1]', '[IPv6:2001:db8::1]', '[x@y]', '(c)example.com', 'example.com(c)', 'j\u00f5geva.ee', '\ud83d\ude00.example', 'a b', '', '[', '[]', 'x].y'];
const COMMENTS = ['', '', '', ' (comment)', ' (a (nested) one)', ' (un closed', ' (a@b)', '(x)', ' (\\))', ' ()'];
const GAPS = ['', ' ', ' ', '  ', '\t', '\r\n ', '\u00a0', '\u2028'];

function addrSpec() {
    return pick(LOCALS) + pick(['@', '@', '@', ' @ ', '@ ', ' @', '@@', '']) + pick(DOMAINS);
}
function entry() {
    const kind = random();
    const comment = pick(COMMENTS);
    if (kind < 0.3) {
        return addrSpec() + comment;
    }
    if (kind < 0.7) {
        return pick(NAMES) + pick(GAPS) + '<' + pick(GAPS) + addrSpec() + pick(GAPS) + '>' + comment;
    }
    if (kind < 0.8) {
        return '<' + addrSpec() + '>' + pick(GAPS) + pick(NAMES) + comment;
    }
    if (kind < 0.9) {
        return pick(NAMES) + pick(GAPS) + addrSpec() + comment;
    }
    return pick(NAMES) + comment;
}
function entries(count) {
    const list = [];
    for (let i = 0; i < count; i++) {
        list.push(entry());
    }
    return list.join(pick([',', ', ', ';', '; ', ' , ', ',\r\n ', ',,']));
}
function list() {
    const parts = [];
    for (let i = between(1, 3); i > 0; i--) {
        if (random() < 0.25) {
            parts.push(pick(['Group', 'My Team', '"Q: G"', '', '(c) G', 'undisclosed-recipients']) + ':' + pick(GAPS) + entries(between(0, 3)) + pick([';', ';', ';', '', ' ;']));
        } else {
            parts.push(entries(between(1, 3)));
        }
    }
    return parts.join(pick([', ', ',', '; ', ' ']));
}
function damage(text) {
    const chars = Array.from(text);
    for (let i = between(1, 3); i > 0 && chars.length; i--) {
        const at = Math.floor(random() * chars.length);
        const how = random();
        if (how < 0.35) {
            chars.splice(at, 1);
        } else if (how < 0.8) {
            chars.splice(at, 0, pick(SOUP));
        } else {
            chars.splice(at, 0, ...chars.slice(at, at + between(1, 6)));
        }
    }
    return chars.join('');
}
for (let i = 0; i < 3500; i++) {
    add(list());
}
for (let i = 0; i < 3500; i++) {
    add(damage(list()));
}

// --- long inputs ------------------------------------------------------------

add(
    'a,'.repeat(2000),
    'a@b,'.repeat(1000),
    'Joe, '.repeat(1500) + 'Foo <joe@example.com>',
    'word '.repeat(2000),
    'word '.repeat(1000) + 'a@b' + ' word'.repeat(1000),
    'a(x)'.repeat(1000) + '@b',
    '<' + 'a(x)'.repeat(1000) + '@b>',
    '@'.repeat(3000),
    'a@'.repeat(1500),
    ' @'.repeat(1500),
    'x' + ' '.repeat(3000) + '@' + ' '.repeat(3000) + 'y',
    '<x' + ' '.repeat(3000) + 'y>',
    '('.repeat(2000) + 'a@b' + ')'.repeat(2000),
    '('.repeat(2000) + 'a@b' + ')'.repeat(1999) + ' c@d',
    '<'.repeat(2000) + 'a@b' + '>'.repeat(2000),
    '"'.repeat(3001) + 'a@b',
    '\\'.repeat(3001),
    '"' + '\\'.repeat(3001) + '"@b',
    'G:'.repeat(1500) + 'a@b' + ';'.repeat(1500),
    'a@[' + ':'.repeat(3000) + ']',
    'a@[' + ':'.repeat(3000),
    '-'.repeat(3000) + '@' + '-'.repeat(3000),
    ('-'.repeat(50) + '@' + '-'.repeat(50) + ' ').repeat(40) + 'a@b',
    'x'.repeat(5000) + '@' + 'y'.repeat(5000),
    '"' + 'q '.repeat(2000) + '"@example.com',
    '\ud83d\ude00'.repeat(2000) + ' <a@b>',
    '\r\n'.repeat(2000) + 'a@b',
    '\u0000'.repeat(3000) + 'a@b'
);

// --- lone surrogates --------------------------------------------------------
// The file holds them as \uXXXX escapes. Go decodes each as U+FFFD, in the
// input and in the result alike, so a vector is only useful when the module
// treats the two the same. It does unless it drops a character that stood
// between two halves and so builds a pair; such inputs are counted and left out.

add(
    '\ud83d',
    '\ude00',
    '\ude00\ud83d',
    '\ud83d@\ude00',
    'a\ud83d@b\ude00',
    '\ud83d <a@b>',
    '"\ud83d" <a@b>',
    '<a\ude00@b>',
    'x \ud83da@b\ude00 y',
    '\ud83d a@b \ude00',
    '(\ud83d) a@b',
    'G\ude00: a@b;',
    '"\ud83d\\\ude00" <a@b>',
    '\ud83d\u0001\ude00 <a@b>',
    '\ud83d\r\ude00@b'
);

// --- run ----------------------------------------------------------------------

function run(input) {
    const out = addressparser(input);
    const flat = addressparser(input, { flatten: true });
    return { in: input, out, flat };
}

// JSON with everything outside printable ASCII escaped, so the file survives
// any editor and any line ending conversion
function encode(value) {
    return JSON.stringify(value).replace(/[\u007f-\uffff]/g, chr => '\\u' + chr.charCodeAt(0).toString(16).padStart(4, '0'));
}

// a copy of a result with every lone surrogate in its strings replaced by U+FFFD
function wellFormed(value) {
    return JSON.parse(JSON.stringify(value), (key, item) => (typeof item === 'string' ? item.toWellFormed() : item));
}

function write(file, list) {
    const lines = [];
    let pairsBuilt = 0;
    for (const input of list) {
        const vector = run(input);
        if (!input.isWellFormed()) {
            const same = run(input.toWellFormed());
            if (JSON.stringify(wellFormed(vector.out)) !== JSON.stringify(same.out) || JSON.stringify(wellFormed(vector.flat)) !== JSON.stringify(same.flat)) {
                pairsBuilt++;
                continue;
            }
        }
        // flat is left out where it equals out, which is every list without a group
        if (JSON.stringify(vector.out) === JSON.stringify(vector.flat)) {
            delete vector.flat;
        }
        lines.push(encode(vector));
    }
    fs.writeFileSync(file, '[\n' + lines.join(',\n') + '\n]\n');
    console.log(`${lines.length} vectors written to ${file}, ${pairsBuilt} left out (lone surrogates the module joins into a pair)`);
}

const args = process.argv.slice(2);
const inputsAt = args.indexOf('--inputs');
if (inputsAt >= 0) {
    const outAt = args.indexOf('--out');
    if (outAt < 0) {
        throw new Error('--inputs needs --out');
    }
    write(args[outAt + 1], JSON.parse(fs.readFileSync(args[inputsAt + 1], 'utf8')));
} else {
    write(path.join(__dirname, '..', 'addressparser.json'), inputs);
}
