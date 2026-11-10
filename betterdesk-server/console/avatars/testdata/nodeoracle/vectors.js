'use strict';

/**
 * Runs lib/avatarImage.js of the Node console (web-nodejs) on generated
 * input and records every answer, for the Go package console/avatars to
 * replay. See README.md in this folder.
 *
 *   node vectors.js      writes ../image.json.gz, ../fetch.json.gz and ../counts.json
 *
 * No request leaves the process: http.request and https.request are
 * replaced by a fake that records what would be sent and plays a scripted
 * answer.
 */

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const http = require('http');
const https = require('https');
const { EventEmitter } = require('events');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');

const sent = [];
let script = [];
function fakeRequest(scheme) {
    return (u, options, cb) => {
        const req = new EventEmitter();
        req.destroy = () => { req.destroyed = true; };
        req.end = () => {
            sent.push({
                scheme, hostname: u.hostname, port: u.port, path: u.pathname + u.search,
                method: options.method, headers: options.headers, agent: options.agent,
                guarded: typeof options.lookup === 'function',
            });
            const step = script.shift();
            setImmediate(() => {
                if (req.destroyed) return;
                if (!step) return req.emit('error', new Error('no scripted answer'));
                if (step.error) return req.emit('error', new Error(step.error));
                const res = new EventEmitter();
                res.statusCode = step.status;
                res.headers = step.headers || {};
                res.resume = () => {};
                res.destroy = () => { res.destroyed = true; };
                cb(res);
                setImmediate(() => {
                    for (const chunk of step.chunks || []) {
                        if (res.destroyed || req.destroyed) return;
                        res.emit('data', Buffer.from(chunk, 'base64'));
                    }
                    if (res.destroyed || req.destroyed) return;
                    if (step.bodyError) res.emit('error', new Error(step.bodyError));
                    else res.emit('end');
                });
            });
        };
        return req;
    };
}
http.request = fakeRequest('http');
https.request = fakeRequest('https');

const avatar = require(path.join(WEB, 'lib/avatarImage'));

// mulberry32: the same numbers on every run.
function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return {
        next,
        int: (n) => Math.floor(next() * n),
        pick: (list) => list[Math.floor(next() * list.length)],
        chance: (p) => next() < p,
        bytes: (n) => { const b = Buffer.alloc(n); for (let i = 0; i < n; i++) b[i] = Math.floor(next() * 256); return b; },
    };
}

const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC', 'base64');
const JPEG = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 0x4a, 0x46, 0x49, 0x46, 0, 1]);
const GIF = Buffer.from('GIF89a\x01\x00\x01\x00\x00\x00\x00;', 'latin1');
const GIF87 = Buffer.from('GIF87a\x01\x00\x01\x00\x00\x00\x00;', 'latin1');
const WEBP = Buffer.concat([Buffer.from('RIFF'), Buffer.from([0x1a, 0, 0, 0]), Buffer.from('WEBPVP8 ')]);
const SVG = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>');
const HEADS = [PNG, JPEG, GIF, GIF87, WEBP, SVG, Buffer.from('<html>'), Buffer.from('RIFF\x00\x00\x00\x00WAVE', 'latin1'),
    Buffer.from('BM'), Buffer.from('%PDF-1.4'), Buffer.from([0xff, 0xd8]), Buffer.from('GIF88a'), Buffer.from([0x89, 0x50, 0x4e, 0x47])];

function caught(fn) {
    try {
        return { out: fn() };
    } catch (err) {
        return { code: err && err.code ? err.code : `not an AvatarError: ${err && err.message}`, message: String(err && err.message) };
    }
}

// ---- sniffImageType ----

function sniffVectors() {
    const r = rng(1);
    const list = [];
    const add = (buf) => list.push({ data: buf.toString('base64'), type: avatar.sniffImageType(buf) });
    for (const head of HEADS) {
        for (let cut = 0; cut <= Math.min(head.length, 14); cut++) add(head.subarray(0, cut));
        add(head);
        add(Buffer.concat([head, r.bytes(20)]));
        // One byte of the signature changed
        for (let i = 0; i < Math.min(head.length, 12); i++) {
            const changed = Buffer.from(head);
            changed[i] ^= 1 << r.int(8);
            add(changed);
        }
    }
    for (let i = 0; i < 600; i++) add(r.bytes(r.int(16)));
    for (let i = 0; i < 300; i++) {
        const head = Buffer.from(r.pick(HEADS));
        const tail = r.bytes(r.int(8));
        add(Buffer.concat([head.subarray(0, r.int(head.length + 1)), tail]));
    }
    return list;
}

// ---- parseAvatarDataUrl ----

function dataUrlVectors() {
    const r = rng(2);
    const list = [];
    const add = (value, maxBytes) => {
        const res = caught(() => avatar.parseAvatarDataUrl(value, maxBytes === undefined ? undefined : { maxBytes }));
        const rec = { value, code: res.code || null };
        if (maxBytes !== undefined) rec.maxBytes = maxBytes;
        if (res.out) {
            rec.mimeType = res.out.mimeType;
            rec.data = res.out.data.toString('base64');
        }
        list.push(rec);
    };
    const mimes = ['image/png', 'image/jpeg', 'image/jpg', 'image/pjpeg', 'image/gif', 'image/webp', 'image/svg+xml',
        'IMAGE/PNG', 'Image/Jpeg', 'image/x-icon', 'image/bmp', 'image/', 'image/png ', 'text/html', 'image/p\u017fng',
        'image/vnd.microsoft.icon', 'image/a.b+c-d', 'image/png;charset=utf-8'];
    const bodies = [PNG, JPEG, GIF, GIF87, WEBP, SVG, Buffer.from('<html><script>alert(1)</script></html>'), Buffer.alloc(0),
        Buffer.from([0x89]), Buffer.from([0xff, 0xd8, 0xff]), Buffer.from([0xff, 0xd8, 0xff, 0x00])];
    const b64url = (s) => s.replace(/\+/g, '-').replace(/\//g, '_');
    for (const mime of mimes) {
        for (const body of bodies) {
            const text = body.toString('base64');
            add(`data:${mime};base64,${text}`);
            add(`data:${mime};base64,${text.replace(/=+$/, '')}`);
            add(`data:${mime};base64,${b64url(text)}`);
        }
    }
    const png = PNG.toString('base64');
    for (const value of [
        '', ' ', 'data:', 'data:image/png', 'data:image/png,abc', 'data:image/png;base64,', 'data:image/png;base64,=',
        'data:image/png;base64,==', `data:image/png;base64,${png}===`, `data:image/png;base64,${png}=`, `data:image/png;base64,${png}==`,
        `DATA:IMAGE/PNG;BASE64,${png}`, `Data:Image/Png;Base64,${png}`, `  data:image/png;base64,${png}\n`,
        `\ufeffdata:image/png;base64,${png}\u2028`, `data:image/png;base64,${png}\n\n`, `data:image/png;base64,${png} x`,
        `data:image/png;base64, ${png}`, `data:image/png; base64,${png}`, `data:image/png;charset=utf-8;base64,${png}`,
        `data:image/png;base64,${png.slice(0, 20)}\n${png.slice(20)}`, `data:image/png;base64,${png.slice(0, 20)}=${png.slice(20)}`,
        `data:image/png;base64,${png}!`, `data:image/png;ba\u017fe64,${png}`, `data:image/png;base64,${png}\u00e9`,
        `xdata:image/png;base64,${png}`, `data:text/html;base64,PGI+`, `data:image/png;base64,iVBORw0KGgo`, `data:image/png;base64,iVBORw0KGg`,
        `data:image/png;base64,iVBORw0KG`, `data:image/png;base64,i`, `data:image/png;base64,iV`, `data:image/png;base64,iVB`,
        `data:image/gif;base64,R0lGODlh`, `data:image/gif;base64,R0lGODl`, `data:image/gif;base64,R0lGODk`, `data:image/gif;base64,R0lGOD`,
        `data:image/jpeg;base64,_9j_`, `data:image/jpeg;base64,/9j/`, `data:image/jpeg;base64,/9j_4A`, `data:image/jpeg;base64,_9j/4A==`,
    ]) add(value);
    // Sizes around the limit, with a small limit so the vectors stay small
    for (const max of [1, 8, 9, 10, 11, 12, 16, 100]) {
        for (let n = 0; n <= 24; n++) {
            const body = Buffer.concat([PNG.subarray(0, 8), Buffer.alloc(100)]).subarray(0, n);
            const text = body.toString('base64');
            add(`data:image/png;base64,${text}`, max);
            add(`data:image/png;base64,${text.replace(/=+$/, '')}`, max);
        }
    }
    add(`data:image/png;base64,${png}`, 0);
    add(`data:image/png;base64,${png}`, -5);
    // The real limit: one byte under, at and over
    for (const n of [avatar.MAX_AVATAR_BYTES - 1, avatar.MAX_AVATAR_BYTES, avatar.MAX_AVATAR_BYTES + 1, avatar.MAX_AVATAR_BYTES + 2,
        avatar.MAX_AVATAR_BYTES + 3, avatar.MAX_AVATAR_BYTES + 4, avatar.MAX_AVATAR_BYTES + 5, avatar.MAX_AVATAR_BYTES + 1024]) {
        const body = Buffer.concat([PNG, Buffer.alloc(n - PNG.length)]);
        const value = `data:image/png;base64,${body.toString('base64')}`;
        const res = caught(() => avatar.parseAvatarDataUrl(value));
        list.push({ pngPaddedTo: n, code: res.code || null, length: res.out ? res.out.data.length : null });
    }
    // Random text in the alphabet, with and without padding
    const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/-_';
    for (let i = 0; i < 1500; i++) {
        const head = r.pick([PNG, JPEG, GIF, WEBP]).subarray(0, 12).toString('base64').replace(/=+$/, '');
        let text = r.chance(0.7) ? head : '';
        const n = r.int(12);
        for (let j = 0; j < n; j++) text += alphabet[r.int(alphabet.length)];
        if (r.chance(0.1)) text = text.slice(0, r.int(text.length + 1));
        text += '='.repeat(r.pick([0, 0, 0, 1, 2, 3]));
        if (r.chance(0.05)) text += r.pick(['*', ' ', '\n', '%', 'é', '=A']);
        add(`data:${r.pick(['image/png', 'image/jpeg', 'image/gif', 'image/webp'])};base64,${text}`);
    }
    return list;
}

// ---- isRefusedAvatarAddress ----

function addressVectors() {
    const r = rng(3);
    const list = [];
    const seen = new Set();
    const add = (ip) => {
        if (seen.has(ip)) return;
        seen.add(ip);
        list.push({ ip, refused: avatar.isRefusedAvatarAddress(ip) });
    };
    const fixed = [
        '127.0.0.1', '127.8.8.8', '0.0.0.0', '0.1.2.3', '169.254.169.254', '169.254.1.1', '169.253.255.255', '169.255.0.0',
        '100.100.100.200', '100.100.100.201', '100.100.100.199', '192.0.0.1', '192.0.1.1', '192.0.2.1', '198.17.255.255',
        '198.18.0.1', '198.19.255.255', '198.20.0.0', '223.255.255.255', '224.0.0.1', '239.255.255.255', '240.0.0.1',
        '255.255.255.255', '10.0.0.5', '172.16.4.2', '192.168.1.20', '100.64.0.7', '8.8.8.8', '1.1.1.1', '126.255.255.255',
        '128.0.0.0', '01.2.3.4', '1.2.3', '1.2.3.4.5', '1.2.3.256', '1.2.3.4 ', ' 1.2.3.4', '1.2.3.4%eth0', '0x7f.0.0.1',
        '::1', '::', '::2', '0:0:0:0:0:0:0:1', '0:0:0:0:0:0:0:0', 'fe80::1', 'fe80::1%eth0', 'fe80::1%', 'fe80::1%eth 0',
        'fe80::1%a/b', 'febf::1', 'fec0::1', 'ff02::1', 'ff00::', 'feff::1', 'fd00:ec2::254', 'fd00:ec2::253', 'fd00:ec2:1::254',
        'fd12:3456::1', 'fc00::1', '::ffff:127.0.0.1', '::ffff:169.254.169.254', '::ffff:7f00:1', '::ffff:192.168.1.20',
        '::ffff:8.8.8.8', '::ffff:0808:0808', '::127.0.0.1', '::8.8.8.8', '::7f00:1', '::0808:0808', '::1:0:0', '64:ff9b::a9fe:a9fe',
        '64:ff9b::808:808', '64:ff9b:1::a9fe:a9fe', '64:ff9b::127.0.0.1', '2002:7f00:1::1', '2002:808:808::1', '2002:a9fe:a9fe::',
        '2002::', '2a01:4f8::1', '2001:db8::1', 'not-an-ip', '', 'localhost', '1::2::3', '12345::1', '::g', '[::1]', '::1%1',
        '::ffff:1.2.3.4%lo', '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7::', '::2:3:4:5:6:7:8', '1:2:3:4:5:6:1.2.3.4', '1:2:3:4:5:6:7:1.2.3.4',
        '2a01:4f8::1%eth0', '2a01:4f8::1%a/b', '2a01:4f8::1%', '2a01:4f8::1%eth 0', '2a01:4f8::1%e_0', '8.8.8.8%eth0', 'fd12:3456::1%1', 'FE80::1', '::FFFF:127.0.0.1', 'Fd00:Ec2::254', '0000:0000:0000:0000:0000:ffff:7f00:0001', '::ffff:0:1', '0::0', '0::1',
        '::ffff:01.2.3.4', '::1.2.3', '1.2.3.4:80', ':::', '::%eth0', '1.2.3.4.', '.1.2.3.4', '1..3.4', '١.٢.٣.٤',
    ];
    fixed.forEach(add);
    const v4 = () => `${r.int(256)}.${r.int(256)}.${r.int(256)}.${r.int(256)}`;
    const hex = () => r.int(65536).toString(16);
    for (let i = 0; i < 2500; i++) add(v4());
    // Around every refused range
    for (const base of ['0.0.0.0', '127.0.0.0', '169.254.0.0', '100.100.100.200', '192.0.0.0', '198.18.0.0', '224.0.0.0', '240.0.0.0']) {
        const p = base.split('.').map(Number);
        const n = ((p[0] << 24) >>> 0) + (p[1] << 16) + (p[2] << 8) + p[3];
        for (const d of [-2, -1, 0, 1, 2, 255, 256, 65535, 65536, 131071, 131072, 16777215, 16777216, 268435455, 268435456]) {
            const m = (n + d + 4294967296) % 4294967296;
            add([m >>> 24, (m >>> 16) & 255, (m >>> 8) & 255, m & 255].join('.'));
        }
    }
    for (let i = 0; i < 1500; i++) {
        const kind = r.int(8);
        if (kind === 0) add(`::ffff:${v4()}`);
        else if (kind === 1) add(`::${v4()}`);
        else if (kind === 2) add(`64:ff9b::${v4()}`);
        else if (kind === 3) add(`2002:${hex()}:${hex()}::${hex()}`);
        else if (kind === 4) add(`${r.pick(['fe80', 'fe90', 'fea0', 'febf', 'fec0', 'ff00', 'ff0e', 'fd00', 'fc00', '2001', '2a01'])}:${hex()}::${hex()}`);
        else if (kind === 5) add(Array.from({ length: 8 }, () => (r.chance(0.5) ? '0' : hex())).join(':'));
        else if (kind === 6) add(`::${hex()}:${hex()}`);
        else add(`::ffff:${hex()}:${hex()}`);
    }
    return list;
}

// ---- fetchAvatarUrl and loadAvatarPicture ----

const pngAnswer = () => ({ status: 200, headers: { 'content-type': 'image/png' }, chunks: [PNG.toString('base64')] });

async function runFetch(rec, fn) {
    sent.length = 0;
    script = (rec.answers || []).map((a) => ({ ...a }));
    try {
        const out = await fn();
        rec.code = null;
        rec.mimeType = out.mimeType;
        rec.data = out.data.toString('base64');
        if (out.version !== undefined) rec.version = out.version;
        if (out.sourceUrl !== undefined) rec.sourceUrl = out.sourceUrl;
    } catch (err) {
        rec.code = err && err.code ? err.code : `not an AvatarError: ${err && err.message}`;
    }
    rec.sent = sent.map((s) => ({ ...s }));
    return rec;
}

async function fetchVectors() {
    const r = rng(4);
    const urls = [];
    const fetches = [];

    // Addresses: what is refused before anything is sent, and what the
    // request looks like otherwise
    const hosts = ['example.com', 'EXAMPLE.com', 'example.com.', 'a.b.example.org', 'idp.lan', 'localhost', 'LOCALHOST', 'localhost.',
        'localhost.localdomain', 'x.localhost', 'x.localhost.', 'notlocalhost', 'metadata.google.internal', 'metadata.goog',
        'Metadata.Google.Internal', '127.0.0.1', '127.1', '0x7f.1', '0x7f000001', '2130706433', '017700000001', '0177.0.0.1',
        '127.0.0.1.', '10.0.0.5', '192.168.1.20', '169.254.169.254', '0', '0.0.0.0', '8.8.8.8', '1.2.3', '1.2.3.4.5', '256.1.1.1',
        '1.2.3.0x100', '0x', '08.8.8.8', '4294967295', '4294967296', '99999999999999999999999', '1.2.3.4.example.com', 'example.1',
        'example.0x1', '[::1]', '[::]', '[fe80::1]', '[fd00:ec2::254]', '[::ffff:127.0.0.1]', '[::ffff:7f00:1]', '[2a01:4f8::1]',
        '[fd12:3456::1]', '[::ffff:192.168.1.20]', '[64:ff9b::a9fe:a9fe]', '[2002:7f00:1::1]', '[::1', '::1', '[::1]x', '[fe80::1%25eth0]',
        '[1.2.3.4]', '[::g]', 'exa mple.com', 'exa%20mple.com', 'ex%41mple.com', 'example%2ecom', '%6c%6f%63%61%6c%68%6f%73%74',
        'a_b.example.com', 'a..b', '.', '..', '-', 'a b', 'a<b', 'a>b', 'a^b', 'a|b', 'a[b', 'a]b', 'a%b', 'a%zzb', 'a\tb.com',
        'exam\nple.com', 'a@b', 'u:p@example.com', 'u@example.com', ':p@example.com', '@example.com', ':@example.com', '::@example.com',
        'u:p@a@example.com', 'u%40x@example.com', 'xn--bcher-kva.example', '%00', 'a\u0000b', 'a\u007fb', '127.0.0.1%2e', 'a.b.c.d.e.f.g'];
    const schemes = ['http://', 'https://', 'HTTP://', 'hTTps://', 'http:', 'http:/', 'http:///', 'http:\\\\', 'https:\\/', 'ftp://', 'file:///',
        'javascript:', 'data:', 'ws://', '//', '', 'htt p://', 'http ://', ' http://', '\thttps://', 'h\nttp://'];
    const ports = ['', ':80', ':443', ':8080', ':0', ':65535', ':65536', ':00080', ':', ':8a', ':-1', ':99999999999'];
    const tails = ['', '/', '/a.png', '/a/b/../c.png', '/a/./b/', '/a/..', '/..', '/../..', '/a/%2e%2e/b', '/a/%2E/b', '/a/.%2e/b', '/%2e',
        '/a b.png', '/a"b', '/a<b>c', '/a`b', '/a{b}c', '/a|b', '/a^b', '/a%zz', '/a%20b', '/é.png', '/a\\b\\c.png', '\\a.png', '?x=1',
        '/a?x=1&y=2', '/a?x y', "/a?x='y'", '/a?x="y"', '/a?x<y>', '/a?é', '/a?x#frag', '#frag', '/a#b?c', '/a?', '//a//b', '/a/b/', '/a;b=c',
        '/a\tb', '/a\nb', '/~user/a.png', '/a%2Fb', '/a/%2e%2e', '/a/b/..?x', '/a/../../b', '/./a', '/a/.', '/a/..b', '/a/.b', '/a/...'];
    for (const scheme of schemes) for (const host of ['example.com', '127.0.0.1', 'localhost', 'u:p@example.com']) urls.push(scheme + host + '/a.png');
    for (const host of hosts) for (const port of ['', ':8080']) urls.push(`http://${host}${port}/a.png`);
    for (const port of ports) for (const scheme of ['http://', 'https://']) urls.push(`${scheme}example.com${port}/a.png`);
    for (const port of ports) urls.push(`http://[2a01:4f8::1]${port}/a.png`);
    for (const tail of tails) urls.push(`http://example.com${tail}`);
    for (const tail of tails) urls.push(`https://idp.lan:8443${tail}`);
    urls.push('', ' ', 'example.com/a.png', '/a.png', 'http', 'http:', 'http://', 'https:///', 'http://?x', 'http://#x', 'http:example.com',
        'http:/example.com', 'http:////example.com/a.png', 'http:\\\\example.com\\a.png', '  http://example.com/a.png  ',
        'http://example.com/a.png\n', '\u0000http://example.com/', 'http://example.com:80:80/', 'http://exam ple.com/',
        'HTTP://EXAMPLE.COM/A.PNG', 'http://example.com/' + 'a'.repeat(3000));
    for (let i = 0; i < 400; i++) {
        urls.push(`${r.pick(['http://', 'https://', 'http:', 'http:/', 'HTTP://'])}${r.pick(hosts)}${r.pick(ports)}${r.pick(tails)}`);
    }
    const seen = new Set();
    for (const url of urls) {
        if (seen.has(url) || !url.isWellFormed() || /[^\x00-\x7f]/.test(url.split(/[/?#]/).slice(0, 3).join('/'))) continue;
        seen.add(url);
        fetches.push(await runFetch({ url, answers: [pngAnswer()] }, () => avatar.fetchAvatarUrl(url)));
    }

    // Answers: status, type, size, content, errors
    const b64 = (buf) => buf.toString('base64');
    const answerCases = [];
    for (const status of [200, 201, 204, 206, 301, 302, 304, 400, 401, 403, 404, 500, 503]) {
        answerCases.push([{ status, headers: { 'content-type': 'image/png' }, chunks: [b64(PNG)] }]);
    }
    const types = ['image/png', 'image/jpeg', 'image/jpg', 'image/pjpeg', 'image/gif', 'image/webp', 'IMAGE/PNG', ' image/png ',
        'image/png; charset=binary', 'image/png;x', 'image/svg+xml', 'text/html', 'application/octet-stream', '', 'image/', 'image/x-png',
        'image/\u0130', 'image/p\u017fng', '\u0130mage/gif', 'image/gif\u00a0', ';image/png'];
    for (const type of types) {
        for (const body of [PNG, JPEG, GIF, WEBP, SVG]) {
            answerCases.push([{ status: 200, headers: { 'content-type': type }, chunks: [b64(body)] }]);
        }
    }
    answerCases.push([{ status: 200, headers: {}, chunks: [b64(PNG)] }]);
    answerCases.push([{ status: 200, headers: { 'content-type': 'image/png' }, chunks: [] }]);
    answerCases.push([{ status: 200, headers: { 'content-type': 'image/png' }, chunks: [b64(PNG.subarray(0, 4)), b64(PNG.subarray(4))] }]);
    answerCases.push([{ status: 200, headers: { 'content-type': 'image/png' }, chunks: [b64(PNG.subarray(0, 3))] }]);
    answerCases.push([{ status: 200, headers: { 'content-type': 'image/png' }, chunks: [b64(PNG)], bodyError: 'aborted' }]);
    answerCases.push([{ status: 200, headers: { 'content-type': 'image/png' }, chunks: [b64(Buffer.from('<script>x</script>'))] }]);
    answerCases.push([{ error: 'connect ECONNREFUSED 10.0.0.5:80' }]);
    answerCases.push([{ error: 'getaddrinfo ENOTFOUND example.com' }]);
    for (const answers of answerCases) {
        fetches.push(await runFetch({ url: 'http://example.com/a.png', answers }, () => avatar.fetchAvatarUrl('http://example.com/a.png')));
    }
    // Size limits, with a small limit
    for (const max of [8, 67, 68, 69, 100]) {
        for (const declared of [undefined, '0', '8', '67', '68', '69', '70', '100', '101', '1000000']) {
            for (const split of [false, true]) {
                const headers = { 'content-type': 'image/png' };
                if (declared !== undefined) headers['content-length'] = declared;
                const chunks = split ? [b64(PNG.subarray(0, 30)), b64(PNG.subarray(30))] : [b64(PNG)];
                fetches.push(await runFetch({ url: 'http://example.com/a.png', maxBytes: max, answers: [{ status: 200, headers, chunks }] },
                    () => avatar.fetchAvatarUrl('http://example.com/a.png', { maxBytes: max })));
            }
        }
    }

    // Redirects
    const locations = ['/b.png', 'b.png', '../b.png', './b.png', '?v=2', '#top', '', '//other.example/b.png', 'http://other.example/b.png',
        'https://other.example:8443/b.png', 'HTTP://OTHER.example/b.png', 'http://127.0.0.1/b.png', 'http://localhost/b.png',
        'http://169.254.169.254/latest/meta-data/', 'http://[::1]/b.png', 'ftp://other.example/b.png', 'file:///etc/passwd',
        'http://u:p@other.example/b.png', '\\\\other.example\\b.png', '/\\other.example/b.png', 'http:b.png', 'http:/b.png', 'https:b.png',
        'http://', 'http://exa mple.com/', '  /b.png  ', '/a/../b.png', 'c/d.png?x=1#y', '/b.png?x=1', 'javascript:alert(1)', 'data:image/png;base64,AAAA',
        '//127.1/b.png', '//0x7f.1/b.png', '/%2e%2e/b.png', '..', '.', '/', 'x y.png', 'http://other.example', 'http://other.example?x'];
    const bases = ['http://example.com/dir/a.png', 'https://example.com:8443/dir/sub/a.png?old=1', 'http://example.com', 'http://example.com/dir/'];
    for (const base of bases) {
        for (const location of locations) {
            for (const status of [302, 301, 307, 308, 303, 300, 304]) {
                if (status !== 302 && r.chance(0.8)) continue;
                const answers = [{ status, headers: { location, 'content-type': 'text/html' }, chunks: [] }, pngAnswer()];
                fetches.push(await runFetch({ url: base, answers }, () => avatar.fetchAvatarUrl(base)));
            }
        }
    }
    // A redirect status without Location is an answer like any other
    fetches.push(await runFetch({ url: bases[0], answers: [{ status: 302, headers: {}, chunks: [] }] }, () => avatar.fetchAvatarUrl(bases[0])));
    fetches.push(await runFetch({ url: bases[0], answers: [{ status: 302, headers: { location: '' }, chunks: [] }] }, () => avatar.fetchAvatarUrl(bases[0])));
    // How many are followed
    const hop = (n) => ({ status: 302, headers: { location: `/hop${n}` }, chunks: [] });
    for (const maxRedirects of [undefined, 0, 1, 2, 3, 5]) {
        for (let hops = 0; hops <= 6; hops++) {
            const answers = [];
            for (let i = 0; i < hops; i++) answers.push(hop(i));
            answers.push(pngAnswer());
            const rec = { url: 'http://example.com/start', answers };
            if (maxRedirects !== undefined) rec.maxRedirects = maxRedirects;
            fetches.push(await runFetch(rec, () => avatar.fetchAvatarUrl('http://example.com/start', maxRedirects === undefined ? {} : { maxRedirects })));
        }
    }

    // loadAvatarPicture: which kind of value, the version, the source address
    const loads = [];
    const values = ['', '   ', 'data:image/png;base64,' + b64(PNG), ' DATA:image/png;base64,' + b64(PNG) + ' ', 'data:nonsense',
        'http://example.com/a.png', '  https://example.com/a.png\n', 'HTTP://example.com/a.png', 'HttpS://example.com/a.png', 'ftp://example.com/a.png',
        'example.com/a.png', '/a.png', 'http:/example.com/a.png', 'http:example.com', 'https//example.com', 'javascript:alert(1)',
        'http://example.com/' + 'a'.repeat(2040), 'http://example.com/' + 'a'.repeat(2027) + '\u{1F600}', 'http://example.com/' + 'a'.repeat(2028) + '\u{1F600}x',
        'http://example.com/' + 'a'.repeat(2029) + '\u{1F600}', 'http://example.com/?q=' + 'é'.repeat(2100), 'http://127.0.0.1/a.png', 'http\u017f://example.com/a.png'];
    for (const value of values) {
        loads.push(await runFetch({ value, answers: [pngAnswer()] }, () => avatar.loadAvatarPicture(value)));
    }
    for (const body of [PNG, JPEG, GIF, WEBP]) {
        loads.push({ versionOf: b64(body), version: avatar.imageVersion(body) });
    }
    for (let i = 0; i < 200; i++) {
        const body = r.bytes(r.int(64));
        loads.push({ versionOf: b64(body), version: avatar.imageVersion(body) });
    }
    return { fetches, loads };
}

function write(name, value) {
    fs.writeFileSync(path.join(OUT, `${name}.json.gz`), zlib.gzipSync(Buffer.from(JSON.stringify(value)), { level: 9 }));
}

async function main() {
    const image = { sniff: sniffVectors(), data_url: dataUrlVectors(), refused: addressVectors() };
    const fetch = await fetchVectors();
    write('image', image);
    write('fetch', fetch);
    const counts = {
        sniff: image.sniff.length, data_url: image.data_url.length, refused: image.refused.length,
        fetch: fetch.fetches.length, load: fetch.loads.length,
        constants: { MAX_AVATAR_BYTES: avatar.MAX_AVATAR_BYTES, FETCH_TIMEOUT_MS: avatar.FETCH_TIMEOUT_MS, MAX_REDIRECTS: avatar.MAX_REDIRECTS },
    };
    fs.writeFileSync(path.join(OUT, 'counts.json'), JSON.stringify(counts, null, 1) + '\n');
    console.log(JSON.stringify(counts));
}

main().catch((err) => { console.error(err); process.exit(1); });
