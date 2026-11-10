'use strict';

/**
 * Records what the Node console does, for the Go tests of console/auditlog
 * and of console/kit/logger.go:
 *
 *   vectors.json  lib/logRedact.js: every exported function over generated inputs
 *   rows.json     the row logAction of services/dbAdapter.js leaves for each
 *                 kind of argument, on SQLite and on PostgreSQL
 *   logger.json   lib/logger.js: the level gate, the text of each argument,
 *                 whole lines
 *
 * Run it in the dev image from web-nodejs, with the test PostgreSQL in reach:
 *
 *   DEV_DOCKER_ARGS="--network bdgo -e BETTERDESK_TEST_POSTGRES_DSN=postgres://...@bdgo-testpg:5432/betterdesk_test?sslmode=disable" \
 *     tools/dev.sh "cd web-nodejs && node ../betterdesk-server/console/auditlog/testdata/gen.js"
 *
 * Inputs are well-formed strings: a Go string cannot hold half a surrogate
 * pair. Where a function cuts a pair in half, the output is recorded as it
 * reaches a file or a database, with U+FFFD in place of the half.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const util = require('util');
const { spawnSync } = require('child_process');
const { createRequire } = require('module');

const WEB = process.cwd();
const webRequire = createRequire(path.join(WEB, 'package.json'));
const OUT = __dirname;
const SCHEMA = path.join(__dirname, '..', '..', 'store', 'schema');
// GEN_FUZZ_SCALE=100 makes a hundred times as many random inputs, for a
// one-off check. The files in this folder were made without it.
const SCALE = Number(process.env.GEN_FUZZ_SCALE || 1);

// ---------------------------------------------------------------------------
//  Helpers
// ---------------------------------------------------------------------------

/** A small seeded generator, so every run makes the same inputs. */
function prng(seed) {
    let a = seed >>> 0;
    return () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}

function pick(rand, list) {
    return list[Math.floor(rand() * list.length)];
}

function unique(list) {
    return [...new Set(list)].filter((s) => s.isWellFormed());
}

/** JSON in plain ASCII with LF line ends, so no editor or checkout changes it. */
function writeJSON(name, value) {
    const text = JSON.stringify(value, null, 1)
        .replace(/[\u007f-\uffff]/g, (c) => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'));
    fs.writeFileSync(path.join(OUT, name), text + '\n');
}

// White space as JavaScript's trim and \s see it, and look-alikes they do
// not count: U+0085, U+180E, U+200B.
const JS_SPACE = ['\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2000', '\u2001', '\u2002', '\u2003',
    '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200a', '\u2028', '\u2029', '\u202f', '\u205f',
    '\u3000', '\ufeff'];
const NOT_SPACE = ['\u0085', '\u180e', '\u200b', '\u2060', '\u001f', '\u0000'];

// ---------------------------------------------------------------------------
//  lib/logRedact.js
// ---------------------------------------------------------------------------

function usernameInputs() {
    const names = ['', 'a', 'ab', 'abc', 'admin', 'administrator', 'alice;', 'a b', 'a\tb', '\u00e9', '\u00e9\u00e8',
        '\u00e9t\u00e9', '\u{1f600}', '\u{1f600}a', 'a\u{1f600}', '\u{1f600}\u{1f601}', '\u{1f600}ab', 'ab\u{1f600}',
        '\u{1f600}abc\u{1f601}', 'a\u{1f600}b', 'e\u0301', 'e\u0301x', 'xe\u0301', 'x'.repeat(5000), '$&', '$1ab$1',
        '***', 'user@example.com', 'DOMAIN\\user', '\u05d0\u05d1\u05d2', '\u4e2d\u6587\u7528\u6237'];
    const out = [...names];
    for (const ws of [...JS_SPACE, ...NOT_SPACE]) {
        out.push(ws, ws + ws, `${ws}admin`, `admin${ws}`, `${ws}admin${ws}`, `${ws}ab${ws}`, `${ws}a${ws}`,
            `ad${ws}min`, `${ws}${ws}abc${ws}${ws}`);
    }
    for (const a of JS_SPACE.slice(0, 8)) {
        for (const b of NOT_SPACE) out.push(`${a}${b}bob${b}${a}`, `${b}${a}bob${a}${b}`);
    }
    return unique(out);
}

function sanitizeInputs() {
    const breaks = ['\r', '\n', '\r\n', '\n\r', '\u2028', '\u2029', '\u0085', '\v', '\f', '\t', '\u0000', '\\n', '\\r',
        '\u000a\u000a', '\u2028\u2029', '\u001b[31m'];
    const out = ['', 'plain', 'line1\nline2', 'a\\b', '\u{1f600}\n\u{1f600}', 'x'.repeat(3000) + '\n' + 'y'.repeat(3000)];
    for (const b of breaks) {
        out.push(b, `a${b}b`, `${b}a`, `a${b}`, `a${b}${b}b`, `${b}a${b}b${b}`, `fake${b}2026-01-01 [AUTH] Login successful`);
        for (const c of breaks) out.push(`one${b}two${c}three`);
    }
    return unique(out);
}

function detailsInputs() {
    const out = [
        '', ' ', 'User logged in', 'Username: administrator', 'token=abc123', 'Username: alice;',
        'Username: alice; ip=203.0.113.7; ua=Mozilla/5.0 (X11; Linux x86_64)',
        'Username collision: alice; ip=203.0.113.7; ua=curl/8.5.0',
        'User: bob, IP: 198.51.100.4, Reason: too many attempts', 'Device: 123456789, User: bob',
        'Username collision: carol', 'User: dave', 'User logged in; ip=::1; ua=x; fingerprint=none',
        'Updated user: erin', 'Created user: frank (operator)', 'Reset password for user: grace',
        'Reset password for User: grace', 'Deleted user group: Key users', 'reason=idle',
        'Saved allowlisted environment keys: API_KEY, SESSION_SECRET, SMTP_PASSWORD',
        'Saved advanced config: /opt/betterdesk/.env (backup: /opt/betterdesk/.env.bak)',
        'path=/etc/betterdesk/secret.key bytes=12', 'from=/a/token to=/b/token=1',
        'Terminal command on 123: curl -H "Authorization: Bearer abc" https://x/?token=abc&key=def',
        'Terminal command on 123: export PASSWORD=hunter2; mysql --password=hunter2 -u root',
        'Terminal command on 123: echo secret : value', 'Clipboard set on 9 (12 chars)',
        'Ticket #4: lost my password: please reset', 'Rule: api key rotation', 'Rule: api_key: rotate',
        'Device 5 renamed to "User: PC"', 'Help requested by 77: my password=abc does not work',
        'SMTP: mail.example.com:587', 'RustDesk public endpoints updated (ID=id.example.com, relay=-, api=https://u:p@h/)',
        'Username:', 'User:', 'Username: ', 'User:   ', 'User:\tbob', 'User:bob', 'User : bob', 'User:: bob',
        'Users: bob', 'XUser: bob', '_User: bob', '-User: bob', '.User: bob', '9User: bob', 'User_: bob',
        'UserName: bob', 'user: bob', 'USER: bob', 'USERNAME: bob', 'uSeRnAmE: bob', 'Username:  ab', 'Username: a',
        'Username: abc', 'Username: \u{1f600}', 'Username: \u{1f600}x', 'Username: x\u{1f600}', 'Username: \u{1f600}xy\u{1f601}',
        'Username: \u00e9l\u00e8ve', 'Username: bob User: carol Username: dave', 'User: User: bob', 'Username: User:',
        'User: token=abc', 'token: User: bob', 'token=User: bob', 'password=Username: bob', 'User: password=x',
        'password', 'password=', 'password= ', 'password:', 'password = x', 'password\t:\tx', 'password==x',
        'password=:x', 'password:=x', 'password=x y', 'password="x y"', 'my_password=x', 'mypassword=x',
        'my-password=x', 'my.password=x', 'my password=x', 'passwords=x', 'password1=x', 'password_=x',
        'apikey=x', 'api key=x', 'api_key=x', 'api-key=x', 'api--key=x', 'API_KEY=x', 'Api-Key: x', 'x-api-key: x',
        'key=x', 'keys=x', 'monkey=x', 'mon-key=x', 'mon_key=x', 'mon.key=x', 'KEY=x', 'key:x', 'key: x;y', 'key =\tx',
        'secret=x', 'SECRET=x', 'client_secret=x', 'client-secret=x', 'secrets=x', 'token=x', 'access_token=x',
        'access-token=x', 'tokens=x', 'TOKEN: x', 'token=\u{1f600}', 'token=x\ntoken=y', 'token=x\r\npassword=y',
        'a=1&token=abc&b=2', 'a=1&token=abc def', '{"password":"x"}', '{"password": "x"}', "{'token': 'x'}",
        'password=x'.repeat(40), ('Username: ' + 'u'.repeat(4000)), 'token=' + 't'.repeat(8000),
        'x'.repeat(2000) + ' password=p ' + 'y'.repeat(2000),
        '\u017fecret=x', 'to\u212aen=x', 'pa\u017f\u017fword=x', '\u017fecret: x', 'U\u017fer: bob', 'U\u017fername: bob',
        '\u212aey=x', 'to\u212aen: x', 'secret\u0131=x', '\u0130key=x', '\u00e9key=x', 'key\u00e9=x', '\u00e9token=x',
        '\u00e9User: bob', 'User\u00e9: bob', '\u{1f600}key=x', '\u{1f600}User: bob',
    ];
    const labels = ['Username', 'User', 'username', 'USER', 'Users', 'xUser'];
    const values = ['bob', 'ab', 'a', 'alice;', 'bob,', '\u{1f600}bc', 'b\u00e9b', '"bob"'];
    const seps = ['', ' ', '  ', '\t', '\n', '\r\n', '\u00a0', '\u1680', '\u2003', '\u2028', '\u2029', '\u202f',
        '\u205f', '\u3000', '\ufeff', '\u0085', '\u180e', '\u200b', ' \u00a0\t', '\v', '\f'];
    for (const label of labels) {
        for (const sep of seps) {
            for (const value of values.slice(0, label.length > 5 ? 3 : values.length)) {
                out.push(`${label}:${sep}${value}`, `Login failed. ${label}:${sep}${value}${sep}next`);
            }
        }
    }
    const keys = ['password', 'secret', 'token', 'api_key', 'api-key', 'key', 'Password', 'TOKEN', 'apikey', 'pass'];
    for (const key of keys) {
        for (const before of seps.slice(0, 16)) {
            for (const op of [':', '=']) {
                const after = seps[(seps.indexOf(before) * 7 + key.length) % seps.length];
                out.push(`${key}${before}${op}${after}value`, `a ${key}${before}${op}${after}v1${after}v2 b`);
            }
        }
    }
    for (const ws of [...JS_SPACE, ...NOT_SPACE]) {
        out.push(`token=a${ws}b`, `User: a${ws}b`, `token=${ws}${ws}x`, `${ws}token=x`, `key${ws}=${ws}x${ws}`,
            `Username:${ws}alice${ws}Username:${ws}bob`);
    }
    // Random runs of the parts the two rules look at.
    const parts = ['Username', 'User', 'user', 'USERNAME', 'password', 'Password', 'secret', 'token', 'api_key',
        'api-key', 'apikey', 'key', 'KEY', 'monkey', 'keyboard', '\u017fecret', 'to\u212aen', 'ip', 'reason', ':', ':',
        '=', '=', ' ', ' ', '  ', '\t', '\n', '\r\n', '\u00a0', '\u2028', '\u2029', '\ufeff', '\u0085', '\u200b',
        '\u3000', ';', ',', '-', '_', '.', 'alice', 'bob', 'ab', 'a', '\u{1f600}', 'x\u{1f600}y', '\u00e9', '***', '"',
        '\'', '(', ')', '/', '1.2.3.4', 'p@ss=w:rd', '&', '?'];
    const rand = prng(20261110);
    for (let i = 0; i < 700 * SCALE; i++) {
        let s = '';
        const n = 1 + Math.floor(rand() * 9);
        for (let j = 0; j < n; j++) s += pick(rand, parts);
        out.push(s);
    }
    return unique(out);
}

function urlInputs() {
    const out = [
        '', ' ', '\t\n', 'https://user:secret@example.com/path', 'http://localhost:21114/api',
        'http://LOCALHOST:21114/API', 'http://localhost:80/api', 'https://localhost:443', 'http://localhost:443',
        'https://localhost:80/', 'ws://h:80', 'wss://h:443/x', 'ftp://u:p@h:21/x', 'ftp://h:22',
        'localhost:21114/api', 'localhost:21114', '127.0.0.1:21114/api', 'example.com/api', '//user:pw@host/x',
        '/relative/path', 'user:pw@host', 'not a url', 'http', 'http:', 'http:/', 'http://', 'https:example.com',
        'https:/example.com', 'https:///example.com', 'https:////example.com/x', 'http:\\\\user:pw@host\\path\\x',
        'http:/\\user:pw@host', 'http:user:pw@host/p', 'HTTP://USER:PW@HOST/Path?Q#F', 'hTtPs://Example.COM:443/',
        '  https://user:pw@example.com/x  ', '\thttps://user:pw@example.com/x\n', '\u00a0https://u:p@h/\u00a0',
        '\ufeffhttps://u:p@h/\ufeff', '\u0085https://u:p@h/', '\u2028https://u:p@h/\u2029', '\u0001https://u:p@h/\u0002',
        '\u0000https://u:p@h/', 'https://u:p@h/\u001f', 'ht\ttp://u:p@h/pa\nth?q\ru#f\tg', 'h\nttps://u:p@ho\tst/',
        'https://u:p@h/a b', 'https://u:p@h/a  ', 'https://u:p@h/?a b', 'https://u:p@h/#a b ',
        // userinfo
        'http://u@h', 'http://u:@h', 'http://:p@h', 'http://:@h', 'http://@h', 'http://@h:80/', 'http://u:p:q@h',
        'http://u@v@h', 'http://u:p@ss@h/', 'http://u@v:p@w@h/x', 'http://u%40x:p%2Fy@h', 'http://u s:p\tw@h',
        'http://u s:p w@h/', 'http://\u00fc:\u00f6@h', 'https://user:pass@[::1]:8443/x?y#z', 'http://us/er:pw@h',
        'http://u?:p@h', 'http://u#:p@h', 'http://u\\:p@h', 'foo://u\\:p@h', 'http://u:p@', 'http://u:p@/x',
        'http://u:p@:80', 'http://u:p@?x', 'http://u:p@#x', 'foo://u:p@', 'foo://u:p@/x', 'http://u:p@h:', 'http://a:b@c:d@e/',
        'http://user:p%ss@h/', 'http://us"er:p\'w@h/', 'http://u<>:p{}@h/', 'http://u|^`:p[]@h/', 'http://u;=:p;=@h/',
        'http://' + 'u'.repeat(300) + ':' + 'p'.repeat(300) + '@h/', 'http://\u{1f600}:\u{1f601}@h/',
        // unparsable with userinfo: the fallback
        'http://user:pw@exa mple.com/x', 'http://user:p@ss@exa mple.com/x', '//user:pw@host//other:pw@host',
        'user:pw@host//a:b@c', 'x //a@b //c@d', '//@h', '//a@', '//a/b@c', '//a\nb@c', 'http://u:p@h:99999/',
        'http://u:p@[::1/', 'http://u:p@h:x/', 'http://u:p@a b/', '://u:p@h/', '1http://u:p@h/', 'ht tp://u:p@h/',
        'http://u:p@%zz/', 'http://u:p@a%2Fb/', 'http://u:p@256.256.256.256/', 'http://u:p@1.2.3.4.5/',
        // hosts
        'http://EXAMPLE.com', 'http://example.com.', 'http://a..b/', 'http://.a/', 'http://-a-/', 'http://a_b/',
        'http://a*b/', 'http://a!b/', 'http://a"b/', 'http://a$b/', 'http://a&b/', "http://a'b/", 'http://a(b)/',
        'http://a+b/', 'http://a,b/', 'http://a;b/', 'http://a=b/', 'http://a~b/', 'http://a`b/', 'http://a{b}/',
        'http://a|b/', 'http://a^b/', 'http://a<b/', 'http://a>b/', 'http://a[b/', 'http://a]b/', 'http://a%b/',
        'http://a\u007fb/', 'http://h%41/', 'http://%68ost/', 'http://ho%20st/', 'http://%00/', 'http://a%2eb/',
        'http://%31%32%37.1/', 'http://xn-a.com/', 'http://axn-b.com/', 'http://a.xn-/', 'http://XN-A.COM/',
        // IPv4
        'http://127.1', 'http://127.0.1', 'http://0x7f.0.0.1', 'http://0X7F.1', 'http://0177.0.0.1', 'http://2130706433',
        'http://017700000001', 'http://1.2.3.4.5', 'http://256.0.0.1', 'http://1.2.3.256', 'http://1.2.65536',
        'http://1.2.65535', 'http://1.16777215', 'http://1.16777216', 'http://1.2.3.4.', 'http://1.2.3.4../',
        'http://0x.0x.0', 'http://0x', 'http://09', 'http://08.1', 'http://a.1', 'http://1.a', 'http://1e3',
        'http://0xffffffff', 'http://0x100000000', 'http://4294967295', 'http://4294967296', 'http://999999999999999999999',
        'http://1..2', 'http://foo.0x', 'http://foo.09', 'http://0', 'http://00', 'http://0.0.0.0', 'http://1.2.3.4:80/',
        'http://0x1g/', 'http://1.2.3.0x/', 'http://.1/', 'http://1./', 'http://1.2.3.4.example/', 'http://0xabc.def/',
        // IPv6
        'http://[::1]', 'http://[::]', 'http://[1:2:3:4:5:6:7:8]', 'http://[1::8]', 'http://[1:0:0:4:0:0:0:8]',
        'http://[0:0:1:0:0:1:0:0]', 'http://[1:0:0:0:1:0:0:0]', 'http://[0:1:0:1:0:1:0:1]', 'http://[1:0:1:0:1:0:1:0]',
        'http://[::ffff:192.168.1.1]', 'http://[::1.2.3.4]', 'http://[1:2:3:4:5:6:1.2.3.4]', 'http://[1:2:3:4:5:6:7:1.2.3.4]',
        'http://[::1.2.3]', 'http://[::01.2.3.4]', 'http://[::1.2.3.4.5]', 'http://[::256.1.1.1]', 'http://[::1.2.3.4]:8080/',
        'http://[1::2::3]', 'http://[:1]', 'http://[1:]', 'http://[12345::]', 'http://[::G]', 'http://[::1', 'http://[::1]x',
        'http://[ABCD::EF]', 'http://[::1]:80', 'http://[::1]:81', 'http://[0::0]', 'http://[::0:0:1]', 'http://[fe80::1%25eth0]',
        'http://[0001:0002::0003]', 'http://[1:2:3:4:5:6:7::]', 'http://[::2:3:4:5:6:7:8]', 'http://[1:2:3:4:5:6:7:8:9]',
        'http://[]', 'http://[:]', 'http://[::1.]', 'http://[::.1]', 'http://[1.2.3.4]', 'http://[::ffff:0:0]',
        'http://u:p@[2001:DB8::7]/x', 'foo://[::1]:5/x', 'foo://[::1', 'foo://u:p@[1::0:0:0]:1/',
        // ports
        'http://h:', 'http://h:0', 'http://h:65535', 'http://u:p@h:100000/', 'http://h:0065535/', 'http://u:p@h:70000', 'http://h:65540/a/../b', 'foo://u:p@h:655350', 'http://h:65536', 'http://h:8a', 'http://h:00080', 'http://h:0443',
        'https://h:00443', 'http://h:-1', 'http://h: 80', 'http://h:8 0/', 'http://h:80:80', 'http://h:080/x',
        'http://h:8\t0/', 'foo://h:80', 'foo://h:', 'foo://h:0', 'foo://h:65536', 'ws://h:443', 'wss://h:80', 'ftp://h:21',
        // paths
        'http://h', 'http://h/', 'http://h//', 'http://h/a/../b', 'http://h/a/./b', 'http://h/a/b/..', 'http://h/a/b/.',
        'http://h/..', 'http://h/../..', 'http://h/./', 'http://h/a//b', 'http://h/a/.b/../c', 'http://h/.well-known/../x',
        'http://h/a/..b/../c', 'http://h/a/b../c', 'http://h/a/.b/./c/../d', 'http://h/a/.b/..', 'http://h/x/.y/%2e%2e/z',
        'http://h/%2e', 'http://h/%2E%2e/x', 'http://h/a/%2e%2E', 'http://h/a/.%2e/b', 'http://h/a/%2e./b', 'http://h/a/%2e%2e%2e/b',
        'http://h/a/.../b', 'http://h/a/..;/b', 'http://h/a\\b\\..\\c', 'http://h\\a', 'http://h/a"b<c>d`e{f}g^h|i',
        "http://h/a'b[c]d", 'http://h/a%zzb%4', 'http://h/a%41b', 'http://h/\u00e4/\u{1f600}', 'http://h/a\u0000b',
        'http://h/a\u007fb', 'http://h/a\u0080b', 'http://h/a:b@c', 'http://h/@', 'http://h/a/.b', 'http://h/a/.b/', 'http://h/.a/../b',
        'http://h/a/b/.c/../../d', 'http://h/a/.b/c/..', 'http://h/a/.b/../c%20d', 'http://h/a/.b/../c d', 'http://h/a/.b/../c?q',
        'http://h/a/.b/../c#f', 'https://h/a/.b/../c', 'foo://h/a/.b/../c', 'foo:/a/.b/../c', 'file:///a/.b/../c',
        'http://h/a/.', 'http://h/a/..', 'http://h/a/.b/.', 'http://h/a./../b', 'http://h/a/b/./../c',
        // query and fragment
        'http://h?', 'http://h/?', 'http://h#', 'http://h/#', 'http://h?#', 'http://h/a?b?c#d#e', 'http://h?a b"c<d>e\'f`g',
        'foo://h?a b"c<d>e\'f`g', 'http://h#a b"c<d>e\'f`g{}', 'http://h/?\u00e4#\u00f6', 'http://h/?%zz#%zz',
        'http://h/?a=1&token=x', 'http://h/#\u0000a', 'http://h?q#', 'http://h?#f', 'http://h/?a^b|c', 'http://h/#a^b|c',
        // schemes
        'foo://user:pw@host:99/p', 'foo://USER@HOST/P', 'foo://h/a/../b', 'foo:/a/b', 'foo:a/b', 'foo:', 'foo://',
        'foo:///x', 'foo://@host', 'foo://h%41/p', 'foo://h ost/', 'foo://h<st/', 'foo://ho^st', 'foo://ho|st',
        'foo://h\u00e4/', 'foo://H\u00c4/', 'foo://h%/', 'foo://h%zz/', 'foo://h"`{}/', 'foo://h/a b?c d#e f',
        'foo:/', 'foo:/.//x', 'foo:/a/..//x', 'foo://h/..//x', 'foo:/..//x', 'foo://h//x', 'foo:////x', 'foo:/a/../..',
        'git+ssh://git@github.com/a/b.git', 'ssh://root:toor@10.0.0.1:22', 'postgres://betterdesk:pw@db:5432/betterdesk?sslmode=disable',
        'postgresql://u:p%40ss@db/x', 'redis://:pw@cache:6379/0', 'mongodb://u:p@a:1,b:2/db', 'ldap://cn=admin,dc=x:pw@dc1/',
        'ldaps://dc1.example.com:636', 'smtp://u:p@mail:587', 'a.b+c-d://u:p@h/', 'a1://u:p@h', 'a_b://u:p@h', '-a://u:p@h',
        'mailto:user@example.com', 'mailto:user:pw@example.com', 'data:text/plain,hello world', 'data:text/plain,a b #f',
        'data:text/plain,a b ?q', 'data:a  ', 'javascript:alert(1)', 'about:blank', 'urn:isbn:0451450523', 'blob:https://u:p@h/x',
        'tel:+1-816-555-1212', 'x:a\tb\nc', 'x:\u00e4 \u{1f600}', 'x:a"b<c>d`e', 'x:a\u0000b', 'x:%zz', 'x:/a/../b', 'x:a/../b',
        'x:?q', 'x:#f', 'x://', 'x:///', 'x://?q', 'x://#f', 'x:a?b c#d e', 'X-Y.Z+1:opaque', 'sc\u00e4:x', '\u00e4bc://h/',
        // file
        'file:///etc/passwd', 'file://host/share', 'file://localhost/x', 'file://LOCALHOST/x', 'file:///C:/x', 'file://C:/x',
        'file:c:\\x\\y', 'file:/c|/x', 'file://user:pw@host/x', 'file:', 'file://', 'file:x', 'file:///C|/a/../..',
        'file:///C:/../..', 'file:///c:', 'file:///c:/', 'file:///a/../c:/x', 'file://h/c:/x', 'file://h/../x', 'file:/x',
        'file:\\\\host\\share', 'file://HOST/Share', 'file://1.2.3.4/x', 'file://[::1]/x', 'file://h:80/x', 'file:///x?q#f',
        'file:///a b', 'file://ho st/x', 'file:////x', 'file://///x', 'file:/.//x', 'file:///C:a', 'file:///C|', 'file:///ab:/x',
        'file://c:', 'file://c|/x', 'file:c|', 'file:///h:65535/%2e%2e/x', 'file:///ab:/../x', 'file:///a:b/..', 'file:///C:x/../y', 'file:h:80/..', 'file:///C|x/../y', 'file:///c:/x/../../y', 'file://h/c:x/../y', 'file:///1:/../x', 'foo:///c:x/../y', 'http://h/c:x/../y', 'file:///C:/a/.b/../c',
        // the cases of TestRedactURL and TestRedactURLInternationalHost
        ' \t\u00a0', 'http://user@example.com', 'http://user:p@ss@example.com/x', '  https://u:p@example.com/x\n',
        'HTTPS://U:P@Example.COM/Path', 'https://u:p@example.com:443/', 'https://u:p@example.com:8443', 'http://u:p@h:008080/',
        'http://u:p@h/a/./b/../c', 'http://u:p@h/a/%2E%2e/c', 'http://u:p@h/a/.b/../c', 'http://u:p@h/a b?c d\'#e f`',
        'http:\\\\u:p@h\\a\\b', 'http://u:p@0x7f.1/', 'http://u:p@[2001:DB8:0:0:0:0:0:1]:80/', 'http://u:p@[::ffff:192.168.1.1]/',
        'foo://U:P@HOST/a/../b', 'foo://u:p@h/..', 'file:c:\\x\\..\\y', 'file://localhost/etc/passwd',
        'http://user:p@ss@exa mple.com/', 'http://u:p@h:65536/', 'HTTP://U:P@B\u00dcCHER.DE:80', 'http://u:p@%C3%BC.de/',
        'http://u:p@XN--bcher-kva.de:80/a/../b', 'http://u:p@xn--a.de/', 'http://u:p@b\u00fc cher.de/',
        'http://u:p@example.com/b\u00fccher?xn--',
        // hosts that need IDNA
        'http://b\u00fccher.de/', 'http://u:p@b\u00fccher.de/x', 'http://xn--bcher-kva.de/', 'http://XN--BCHER-KVA.de/',
        'http://u:p@xn--bcher-kva.de:80/a/../b', 'http://xn--a.de/', 'http://a.xn--/', 'http://xn--/', 'http://fa\u00df.de/',
        'http://\uff21\uff22\uff23.com/', 'http://%C3%BC.de/', 'http://a\u3002b/', 'http://a\u00adb.com/', 'http://\u00c4.de/',
        'http://%ff.de/', 'http://u:p@\u4f8b\u3048.jp/', 'http://\u{1f600}.com/', 'http://a.xn--b\u00fc/', 'http://xn--bcher-kva.xn--p1ai/',
        'https://u:p@\u00e4/', 'http://\u200d.com/', 'http://a\u0301.com/', 'http://xn--zz--zz.com/', 'http://b\u00fccher/a/.b/../c',
    ];
    const schemes = ['http', 'https', 'HTTPS', 'ws', 'wss', 'ftp', 'file', 'foo', 'git+ssh', 'x'];
    const slashes = ['://', '://', '://', ':', ':/', ':///', ':\\\\', '//'];
    const users = ['', '', 'user@', 'user:pw@', ':pw@', 'user:@', '@', 'u%40ser:p%3Aw@', 'us er:p w@', 'user:p@ss@',
        '\u00fcser:p\u00e4@', 'USER:PW@', 'a:b:c@'];
    const hosts = ['example.com', 'EXAMPLE.com', 'localhost', '127.0.0.1', '0x7f.1', '1.2.3', '[::1]', '[2001:db8::1]',
        '[::ffff:1.2.3.4]', 'exa mple.com', 'ex%41mple.com', 'a.b.', '1.2.3.4.', 'foo..bar', '', 'h', 'H-1_x.Y', '10.0.0.256',
        '[1:2:3:4:5:6:7:8]', '[1:0:0:0:0:0:0:0]', 'a%2Fb', 'sub.DOMAIN.example'];
    const ports = ['', '', '', ':80', ':443', ':8080', ':0', ':65535', ':65536', ':', ':abc', ':008080', ':21', ':00'];
    const paths = ['', '', '/', '/api', '/a/../b', '/a/./b', '/%2e%2e/x', '/a b', '/\u00e4', '\\x\\y', '/a/.b/../c', '//x',
        '/.', '/..', '/a/..', '/A/B/', '/a;b=c', '/a/.b/c', '/.x/./y', '/a%2Fb'];
    const queries = ['', '', '?', '?q=1', '?a b', '?token=x&u=\u00e4', "?'\"<>", '?a#b?c'];
    const hashes = ['', '', '#', '#f', '#a b', '#`{}', '#\u{1f600}'];
    const rand = prng(8443);
    for (let i = 0; i < 900 * SCALE; i++) {
        out.push(pick(rand, schemes) + pick(rand, slashes) + pick(rand, users) + pick(rand, hosts) + pick(rand, ports)
            + pick(rand, paths) + pick(rand, queries) + pick(rand, hashes));
    }
    // The same pieces with white space around and control characters inside.
    for (let i = 0; i < 80; i++) {
        const url = `${pick(rand, schemes)}://${pick(rand, users)}${pick(rand, hosts)}${pick(rand, ports)}${pick(rand, paths)}`;
        const ws = pick(rand, [...JS_SPACE, ...NOT_SPACE]);
        const at = Math.floor(rand() * (url.length + 1));
        out.push(ws + url + ws, url.slice(0, at) + pick(rand, ['\t', '\n', '\r', ' ', '\u0000', '\u00a0', '\u2028']) + url.slice(at));
    }
    // Random runs of the pieces a URL parser looks at.
    const pieces = ['http', 'https', 'file', 'foo', 'ws', ':', ':', ':', '/', '/', '/', '/', '\\', '@', '@', '.', '.', '..',
        '%2e', '%2E', '?', '#', '[', ']', '::', '1', '0', '0x', '255', '256', 'a', 'b', 'C', 'c:', 'C|', '|', '%', '%41', '%2f',
        ' ', 'localhost', 'h', '-', '_', '\u00e9', 'user', 'pw', '80', '443', '^', '{', '\'', '"', '+', 'xn-', '~'];
    const starts = ['http://', 'https://', 'file://', 'file:', 'foo://', 'foo:', 'http:', 'ws://', '', '', 'http://u:p@', 'foo://u:p@',
        'http://h/', 'foo://h/', 'file:///', 'http://[', 'http://1.'];
    for (let i = 0; i < 800 * SCALE; i++) {
        let s = pick(rand, starts);
        const n = 1 + Math.floor(rand() * 9);
        for (let j = 0; j < n; j++) s += pick(rand, pieces);
        out.push(s);
    }
    return unique(out);
}

function redactVectors() {
    const redact = webRequire('./lib/logRedact');
    const run = (fn, inputs) => inputs.map((input) => ({ in: input, out: fn(input).toWellFormed() }));
    const key = redact.SENSITIVE_DETAIL_KEY;
    const keyInputs = unique(['password', 'Password', 'PASSWORD', 'user_password', 'passwordHash', 'secret', 'client_secret',
        'SECRET_KEY', 'token', 'accessToken', 'TOKENS', 'api_key', 'api-key', 'apikey', 'APIKEY', 'Api_Key', 'api--key',
        'api key', 'key', 'KEY', 'Key', 'keys', 'monkey', 'key ', ' key', 'key\n', '\nkey', 'pass', 'pwd', 'username', '',
        '\u017fecret', 'to\u212aen', '\u212aey', 'ke\u0443', 'my_api_key_2']);
    return {
        node: process.version,
        ada: process.versions.ada,
        username: run(redact.redactUsernameForLog, usernameInputs()),
        sanitize: run(redact.sanitizeLogValue, sanitizeInputs()),
        details: run(redact.redactAuditDetails, detailsInputs()),
        url: run(redact.redactUrlForLog, urlInputs()),
        sensitive_key: keyInputs.map((input) => ({ in: input, out: key.test(input) })),
    };
}

// ---------------------------------------------------------------------------
//  services/dbAdapter.js logAction
// ---------------------------------------------------------------------------

// Each case is one call of logAction. "go" is the Entry a Go caller builds
// for it: null stands for a nil pointer. On PostgreSQL the users 5 and 7
// exist.
const ROW_CASES = [
    { call: "logAction(5, 'login', 'User logged in; ip=203.0.113.7', '203.0.113.7')", args: [5, 'login', 'User logged in; ip=203.0.113.7', '203.0.113.7'], go: { user: 5, details: 'User logged in; ip=203.0.113.7', ip: '203.0.113.7' } },
    { call: "logAction(null, 'login_failed', 'Username: alice; ip=203.0.113.7', '203.0.113.7')", args: [null, 'login_failed', 'Username: alice; ip=203.0.113.7', '203.0.113.7'], go: { user: null, details: 'Username: alice; ip=203.0.113.7', ip: '203.0.113.7' } },
    { call: "logAction(undefined, 'branding_reset', 'Reset branding to defaults', '::1')", args: [undefined, 'branding_reset', 'Reset branding to defaults', '::1'], go: { user: null, details: 'Reset branding to defaults', ip: '::1' } },
    { call: "logAction(0, 'registration_approved', 'Approved registration', 'unknown')", args: [0, 'registration_approved', 'Approved registration', 'unknown'], go: { user: 0, details: 'Approved registration', ip: 'unknown' } },
    { call: "logAction(7, 'system_update', 'Updated to abc1234')", args: [7, 'system_update', 'Updated to abc1234'], go: { user: 7, details: 'Updated to abc1234', ip: null } },
    { call: "logAction(7, 'files.write', 'Write on 1: /x', null)", args: [7, 'files.write', 'Write on 1: /x', null], go: { user: 7, details: 'Write on 1: /x', ip: null } },
    { call: "logAction(7, 'server_file_write', 'path=/x bytes=1', '')", args: [7, 'server_file_write', 'path=/x bytes=1', ''], go: { user: 7, details: 'path=/x bytes=1', ip: '' } },
    { call: "logAction(5, 'a', null, null)", args: [5, 'a', null, null], go: { user: 5, details: null, ip: null } },
    { call: "logAction(5, 'a', undefined, undefined)", args: [5, 'a', undefined, undefined], go: { user: 5, details: null, ip: null } },
    { call: "logAction(5, 'a', '', '')", args: [5, 'a', '', ''], go: { user: 5, details: '', ip: '' } },
    { call: "logAction(null, 'a', null, null)", args: [null, 'a', null, null], go: { user: null, details: null, ip: null } },
    { call: "logAction(undefined, 'a', undefined, undefined)", args: [undefined, 'a', undefined, undefined], go: { user: null, details: null, ip: null } },
    { call: "logAction(5, '', 'empty action', '::1')", args: [5, '', 'empty action', '::1'], go: { user: 5, details: 'empty action', ip: '::1' } },
    { call: "logAction('7', 'a', 'user id as a string', '::1')", args: ['7', 'a', 'user id as a string', '::1'], go: { user: 7, details: 'user id as a string', ip: '::1' } },
    { call: "logAction(999, 'a', 'no such user', '::1')", args: [999, 'a', 'no such user', '::1'], go: { user: 999, details: 'no such user', ip: '::1' } },
    { call: "logAction(5, 'a', 42, '::1')", args: [5, 'a', 42, '::1'], go: { user: 5, details: '42', ip: '::1' } },
    { call: "logAction(5, 'a', 0, '::1')", args: [5, 'a', 0, '::1'], go: { user: 5, details: '0', ip: '::1' } },
    { call: "logAction(5, 'a', false, '::1')", args: [5, 'a', false, '::1'], go: { user: 5, details: 'false', ip: '::1' } },
    { call: "logAction(5, 'a', { token: 'x' }, '::1')", args: [5, 'a', { token: 'x' }, '::1'], go: { user: 5, details: '[object Object]', ip: '::1' } },
    { call: "logAction(5, 'a', ['token=x', 'b'], '::1')", args: [5, 'a', ['token=x', 'b'], '::1'], go: { user: 5, details: 'token=x,b', ip: '::1' } },
    { call: "logAction(5, 'a', 'token=abc User: bob\\r\\nnext', '::1')", args: [5, 'a', 'token=abc User: bob\r\nnext', '::1'], go: { user: 5, details: 'token=abc User: bob\r\nnext', ip: '::1' } },
    { call: "logAction(5, 'a', 'Username: \\u{1f600}bc \\u00e4\\u4e2d', '\\u00e4')", args: [5, 'a', 'Username: \u{1f600}bc \u00e4\u4e2d', '\u00e4'], go: { user: 5, details: 'Username: \u{1f600}bc \u00e4\u4e2d', ip: '\u00e4' } },
    { call: "logAction(5, 'a', 'nul\\u0000inside', '::1')", args: [5, 'a', 'nul\u0000inside', '::1'], go: { user: 5, details: 'nul\u0000inside', ip: '::1' } },
    { call: "logAction(5, 'token=abc', 'action is not redacted', 'token=abc')", args: [5, 'token=abc', 'action is not redacted', 'token=abc'], go: { user: 5, details: 'action is not redacted', ip: 'token=abc' } },
    { call: "logAction(5, 'a', 'x'.repeat(5000), '::1')", args: [5, 'a', 'x'.repeat(5000), '::1'], go: { user: 5, details: 'x'.repeat(5000), ip: '::1' } },
];

async function recordRows(kind) {
    const rows = [];
    if (kind === 'sqlite') {
        const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'auditlog-'));
        const dbPath = path.join(dir, 'db_v2.sqlite3');
        const Database = webRequire('better-sqlite3');
        const setup = new Database(dbPath);
        setup.exec(fs.readFileSync(path.join(SCHEMA, 'sqlite', 'audit_log.sql'), 'utf8'));
        setup.close();
        const adapter = webRequire('./services/dbAdapter').getAdapter({ dbPath, dataDir: dir });
        const read = new Database(dbPath, { readonly: true });
        for (const c of ROW_CASES) {
            const before = read.prepare('SELECT COALESCE(MAX(id), 0) AS id FROM audit_log').get().id;
            let result;
            try {
                await adapter.logAction(...c.args);
                const row = read.prepare(`SELECT typeof(user_id) AS user_type, CAST(user_id AS TEXT) AS user_id, action,
                    typeof(details) AS details_type, hex(CAST(details AS BLOB)) AS details_hex,
                    typeof(ip_address) AS ip_type, hex(CAST(ip_address AS BLOB)) AS ip_hex,
                    typeof(created_at) AS created_type, created_at FROM audit_log WHERE id > ?`).all(before);
                if (row.length !== 1) throw new Error(`expected one new row, got ${row.length}`);
                const r = row[0];
                result = {
                    user_type: r.user_type, user_id: r.user_id, action: r.action,
                    details_type: r.details_type, details_hex: r.details_type === 'null' ? null : r.details_hex.toLowerCase(),
                    ip_type: r.ip_type, ip_hex: r.ip_type === 'null' ? null : r.ip_hex.toLowerCase(),
                    created_type: r.created_type, created_at: r.created_at,
                };
            } catch (err) {
                result = { error: err.code || err.constructor.name };
            }
            rows.push(result);
        }
        read.close();
        return rows;
    }

    const dsn = process.env.BETTERDESK_TEST_POSTGRES_DSN;
    const { Pool } = webRequire('pg');
    const schema = 't_auditgen_' + require('crypto').randomBytes(6).toString('hex');
    const admin = new Pool({ connectionString: dsn, max: 1 });
    await admin.query(`CREATE SCHEMA ${schema}`);
    try {
        const scoped = new URL(dsn);
        scoped.searchParams.set('options', `-c search_path=${schema}`);
        process.env.DATABASE_URL = scoped.toString();
        process.env.DB_TYPE = 'postgres';
        const read = new Pool({ connectionString: scoped.toString(), max: 1 });
        await read.query('CREATE TABLE users (id SERIAL PRIMARY KEY, username TEXT NOT NULL)');
        await read.query("INSERT INTO users (id, username) VALUES (5, 'five'), (7, 'seven')");
        await read.query(fs.readFileSync(path.join(SCHEMA, 'postgres', 'audit_log.sql'), 'utf8'));
        const adapter = webRequire('./services/dbAdapter').getAdapter();
        for (const c of ROW_CASES) {
            const before = (await read.query('SELECT COALESCE(MAX(id), 0) AS id FROM audit_log')).rows[0].id;
            let result;
            try {
                await adapter.logAction(...c.args);
                const found = (await read.query(`SELECT user_id::text AS user_id, pg_typeof(user_id)::text AS user_type, action,
                    encode(convert_to(details, 'UTF8'), 'hex') AS details_hex,
                    encode(convert_to(ip_address, 'UTF8'), 'hex') AS ip_hex,
                    pg_typeof(created_at)::text AS created_type,
                    abs(extract(epoch FROM (now() - created_at))) < 30 AS created_now,
                    created_at FROM audit_log WHERE id > $1`, [before])).rows;
                if (found.length !== 1) throw new Error(`expected one new row, got ${found.length}`);
                const r = found[0];
                if (!r.created_now) throw new Error('created_at is not the time of the insert');
                result = {
                    user_type: r.user_id === null ? 'null' : r.user_type, user_id: r.user_id, action: r.action,
                    details_type: r.details_hex === null ? 'null' : 'text', details_hex: r.details_hex,
                    ip_type: r.ip_hex === null ? 'null' : 'text', ip_hex: r.ip_hex,
                    created_type: r.created_type, created_at: r.created_at.toISOString(),
                };
            } catch (err) {
                result = { error: err.code || err.constructor.name };
            }
            rows.push(result);
        }
        await read.end();
        if (adapter.close) await adapter.close().catch(() => {});
    } finally {
        await admin.query(`DROP SCHEMA ${schema} CASCADE`);
        await admin.end();
    }
    return rows;
}

function rowVectors() {
    if (!process.env.BETTERDESK_TEST_POSTGRES_DSN) {
        throw new Error('BETTERDESK_TEST_POSTGRES_DSN is not set: the rows are recorded on both databases');
    }
    const perDb = {};
    for (const kind of ['sqlite', 'postgres']) {
        // The adapter is one per process and picks its database at load.
        const env = { ...process.env, DB_TYPE: kind };
        delete env.DATABASE_URL;
        const child = spawnSync(process.execPath, [__filename, '--rows', kind], { cwd: WEB, env, encoding: 'utf8', maxBuffer: 1 << 26 });
        if (child.status !== 0) throw new Error(`rows on ${kind}: ${child.stderr || child.stdout}`);
        const marker = child.stdout.lastIndexOf('\nROWS ');
        perDb[kind] = JSON.parse(child.stdout.slice(marker + 6));
    }
    return ROW_CASES.map((c, i) => ({
        call: c.call,
        go: { user: c.go.user, action: c.args[1], details: c.go.details, ip: c.go.ip },
        sqlite: perDb.sqlite[i],
        postgres: perDb.postgres[i],
    }));
}

// ---------------------------------------------------------------------------
//  lib/logger.js
// ---------------------------------------------------------------------------

/** Load a fresh lib/logger.js under the given environment. */
function loadLogger(logLevel, nodeEnv) {
    const file = webRequire.resolve('./lib/logger');
    delete require.cache[file];
    const saved = { LOG_LEVEL: process.env.LOG_LEVEL, NODE_ENV: process.env.NODE_ENV };
    if (logLevel === null) delete process.env.LOG_LEVEL; else process.env.LOG_LEVEL = logLevel;
    if (nodeEnv === null) delete process.env.NODE_ENV; else process.env.NODE_ENV = nodeEnv;
    try {
        return webRequire('./lib/logger');
    } finally {
        for (const [k, v] of Object.entries(saved)) {
            if (v === undefined) delete process.env[k]; else process.env[k] = v;
        }
    }
}

/** Run fn and return what it handed to console.error, console.warn and console.log. */
function capture(fn) {
    const calls = [];
    const saved = { error: console.error, warn: console.warn, log: console.log };
    console.error = (...args) => calls.push({ stream: 'error', args });
    console.warn = (...args) => calls.push({ stream: 'warn', args });
    console.log = (...args) => calls.push({ stream: 'log', args });
    try {
        fn();
    } finally {
        Object.assign(console, saved);
    }
    return calls;
}

function partInputs() {
    const out = [
        "Login failed: user 'administrator' not found", "Login successful for 'alice'", "for 'admin'", "user 'admin'",
        "Synced 'alice' from Go (provider=ldap, role=operator)", "Login failed: 'alice' is an SSO account", "Login failed: 'john.doe@example.com' is an SSO account", "Login failed: 'john.doe' is an SSO account", "Login failed: LDAP credentials rejected for 'john.doe'",
        "Verifying password for 'alice' (hash type: bcrypt, length: 60)", "Login blocked: username collision for local 'alice' (Go provider=ldap)",
        "Auto-created local user 'alice' (role: operator, provider: local) for session storage",
        "Failed to sync local hash for 'alice': SQLITE_BUSY", "user 'user' not found", "for 'or'", "for 'for'", "user 'us'",
        "credentials for 'red'", "credentials for 'alice'", "local user 'local'", "local user 'bob'", "synced 'sync'", "account 'count'",
        "account 'alice' locked", "therefor 'alice'", "platform 'alice'", "FOR 'alice'", "User 'alice'", "ACCOUNT 'alice'",
        "u\u017fer 'alice'", "\u017fynced 'alice'", "for'alice'", "for  'alice'", "for\t'alice'", "for\n'alice'", "for 'a'", "for 'ab'",
        "for 'abc'", "for ''", "for ' '", "for ' ab '", "for '  alice  '", "for 'al ice'", "for 'a\u{1f600}b'", "for '\u{1f600}'",
        "for '\u{1f600}\u{1f601}'", "for 'alice' and for 'bob'", "for 'alice' for 'alice'", "for 'alice's'", "for 'ali'ce'",
        "user 'x' 'y'", "user 'xy' 'zw'", "'alice'", "'a'", "'ab'", "'abc'", "''", "' '", "'a b'", "'al-ice'", "'al_ice'",
        "'a@b'", "'a.b'", "'a/b'", "'a\\b'", "'a:b'", "'http://x'", "'HTTPS'", "'https'", "'httpx'", "'BETTERDESK_API_KEY'",
        "'betterdesk_x'", "'BETTERDESK'", "'LDAP+local'", "'ldap+x'", "'LDAP'", "'Go server'", "'go server'", "'Go serverx'",
        "'Go'", "'PBKDF2'", "'pbkdf2x'", "'PBKDF'", "'bcrypt'", "'BCRYPTx'", "'bcryp'", "'admin'", "'ADMIN'", "'Admin'",
        "'admins'", "'operator'", "'viewer'", "'user'", "'role'", "'provider'", "'hash type'", "'HASH TYPE'", "'empty'",
        "'invalid'", "'Invalid'", "'invalid2'", "'xadmin'", "'admin '", "' admin'", "'\u00e4dmin'", "'alice' 'bob'",
        "'alice''bob'", "'alice'bob'carol'", "a 'b' c 'de' f 'ghi'", "it's alice's and bob's", "don't 'x' won't",
        "'" + 'a'.repeat(63) + "'", "'" + 'a'.repeat(64) + "'", "'" + 'a'.repeat(65) + "'", "'" + 'a'.repeat(200) + "' 'bob'",
        "'" + '\u{1f600}'.repeat(32) + "'", "'" + '\u{1f600}'.repeat(33) + "'", "'\u{1f600}'", "'a\u{1f600}'", "'\u00e9\u00e9'",
        "'BETTERDES\u212a_X'", "'\u212a\u212a'", "'bcr\u0443pt'", "'u\u017fer'", "'U\u017fER'", "'admi\u0274'",
        "line1\nline2 for 'alice'", "for 'ali\nce'", "for\u2028'alice'", "a\r\nb", "\u2029", "plain", "", " ", "100% for 'alice'",
        "$& for '$&x'", "for '$1ab'", "for 'a$'", "for '$$$$'", "user '$`ab'", "for 'ab' ab ab", "abc for 'abc'",
        "for 'r'", "for ' for'", "user 'ser'", "for 'f'", "[AUTH] Login failed: password mismatch for 'bob' (hash type: PBKDF2)",
        "role 'admin' for 'root'", "provider 'ldap'", "hash 'bcrypt' vs 'PBKDF2-SHA256'", "file 'C:\\x\\y.txt'", "env 'NODE_ENV'",
        "key 'k1' value 'v_2'", "'" + 'ab'.repeat(40) + "'", "for '" + 'x'.repeat(5000) + "'",
        "'\u{1f600}'ab'cd'", "'" + '\u{1f600}'.repeat(33) + "'ab'cd'", "'" + '\u{1f600}'.repeat(32) + "'ab'cd'",
        "u\u017fer 'al ice'", "\u017fynced 'a b c'", "U\u017fER 'al ice' user 'al ice'", "local u\u017fer 'x y'",
        "\u212aey for 'a b'", "account\u0085'al ice'", "account\u00a0'al ice'",
    ];
    const words = ['for', 'user', 'account', 'credentials for', 'local user', 'synced', 'FOR', 'User', 'Synced', 'users', 'fo'];
    const names = ['alice', 'ab', 'a', 'admin', 'al ice', 'a.b', '\u{1f600}xy', 'user', 'BETTERDESK_X', 'for'];
    for (const word of words) {
        for (const ws of ['', ' ', '  ', '\t', '\u00a0', '\u3000', '\ufeff', '\u0085', '\u200b', '\u2003', '\u2028']) {
            for (const name of names.slice(0, ws === ' ' ? names.length : 3)) {
                out.push(`${word}${ws}'${name}'`, `x ${word}${ws}'${name}' y '${name}'`);
            }
        }
    }
    const parts = ['for', 'user', 'account', 'credentials for', 'local user', 'synced', 'User', ' ', ' ', ' ', '\t', '\u00a0',
        '\'', '\'', '\'', '\'', 'alice', 'bob', 'ab', 'a', 'admin', 'role', 'x.y', 'a/b', 'BETTERDESK_', 'bcrypt', '\u{1f600}',
        '\u00e9', 'us', 'or', 'ser', ':', '-', '_', '@', '\n', '$&', '0'];
    const rand = prng(64);
    for (let i = 0; i < 500 * SCALE; i++) {
        let s = '';
        const n = 1 + Math.floor(rand() * 10);
        for (let j = 0; j < n; j++) s += pick(rand, parts);
        out.push(s);
    }
    return unique(out);
}

function loggerVectors() {
    // The level gate: which level each LOG_LEVEL and NODE_ENV gives, and
    // which calls print, on which stream.
    const levels = [];
    for (const logLevel of [null, '', 'error', 'warn', 'info', 'debug', 'ERROR', ' Warn ', '\tdebug\n', 'verbose', 'trace',
        'warning', 'fatal', '0', 'constructor', 'toString', 'in fo', '\u00a0info\u00a0']) {
        for (const nodeEnv of [null, 'production', 'development', 'Production', 'test']) {
            const logger = loadLogger(logLevel, nodeEnv);
            const printed = {};
            for (const level of ['error', 'warn', 'info', 'debug']) {
                const calls = capture(() => logger[level]('m'));
                printed[level] = calls.length ? calls[0].stream : null;
            }
            const child = capture(() => { const c = logger.child('P'); c.error('m'); c.warn('m'); c.info('m'); c.debug('m'); });
            levels.push({ log_level: logLevel, production: nodeEnv === 'production', node_env: nodeEnv, level: logger.level,
                printed, child_printed: child.length });
        }
    }

    // One string argument, as the logger hands it to console.log. A child
    // logger is used so that console.log does not read the string as a
    // format.
    const logger = loadLogger('debug', 'development');
    const child = logger.child('T');
    const parts = partInputs().map((input) => {
        const calls = capture(() => child.info(input));
        return { in: input, out: calls[0].args[1].toWellFormed() };
    });

    // Whole lines: the arguments and the text console.log prints for them.
    const lineCases = [
        { prefix: null, level: 'info', args: ['plain message'] },
        { prefix: null, level: 'warn', args: ["[Session] IP change during session: loginIp=1.2.3.4 currentIp=5.6.7.8 ua=x userId=5"] },
        { prefix: null, level: 'warn', args: ['[Session] endCurrentSession failed:', 'database is locked'] },
        { prefix: 'AUTH', level: 'info', args: ["[AUTH] Login failed: user 'administrator' not found in database"] },
        { prefix: 'AUTH', level: 'warn', args: ['[AUTH] Failed to migrate password hash for user id', 12, ':', 'disk full'] },
        { prefix: 'AUTH', level: 'error', args: ['[AUTH] CRITICAL: createUser succeeded but getUserByUsername returned null for default admin'] },
        { prefix: 'REMOTE', level: 'debug', args: ['Connection snapshot from health failed:', 'timeout of 5000ms exceeded'] },
        { prefix: 'REMOTE', level: 'info', args: ['Guest remote deny (explicit query, invalid/expired/not allowed):', '123456789'] },
        { prefix: 'REMOTE', level: 'warn', args: ['Guest grant validate failed:', "no grant for 'alice'\nforged line"] },
        { prefix: 'X', level: 'info', args: ['a', '', 'b'] },
        { prefix: 'X', level: 'info', args: [] },
        { prefix: null, level: 'info', args: [] },
        { prefix: null, level: 'info', args: [''] },
        { prefix: 'X', level: 'info', args: ['numbers', 0, -1, 1.5, 1e21, true, false] },
        { prefix: 'X', level: 'info', args: ["for 'alice'", "for 'bob'", "'carol'"] },
        { prefix: 'X', level: 'info', args: ['two\r\nlines', 'and\u2028more'] },
        { prefix: 'A B', level: 'error', args: ["user 'dave' failed"] },
        { prefix: '', level: 'info', args: ['an empty prefix is no prefix'] },
        { prefix: 'X', level: 'info', args: ['100%s', 'not a format', '%d'] },
    ];
    const lines = lineCases.map((c) => {
        const target = c.prefix === null ? logger : logger.child(c.prefix);
        const calls = capture(() => target[c.level](...c.args));
        return { prefix: c.prefix, level: c.level, args: c.args, stream: calls[0].stream, line: util.format(...calls[0].args).toWellFormed() };
    });
    return { node: process.version, levels, parts, lines };
}

// ---------------------------------------------------------------------------

async function main() {
    if (process.argv[2] === '--rows') {
        const rows = await recordRows(process.argv[3]);
        process.stdout.write('\nROWS ' + JSON.stringify(rows));
        return;
    }
    const redact = redactVectors();
    writeJSON('vectors.json', redact);
    const rows = rowVectors();
    writeJSON('rows.json', { node: process.version, cases: rows });
    const logger = loggerVectors();
    writeJSON('logger.json', logger);
    console.log(`vectors.json: ${redact.username.length} usernames, ${redact.sanitize.length} sanitize, ${redact.details.length} details, ${redact.url.length} urls`);
    console.log(`rows.json: ${rows.length} calls on SQLite and PostgreSQL`);
    console.log(`logger.json: ${logger.levels.length} level settings, ${logger.parts.length} arguments, ${logger.lines.length} lines`);
}

main().then(() => process.exit(0), (err) => {
    console.error(err);
    process.exit(1);
});
