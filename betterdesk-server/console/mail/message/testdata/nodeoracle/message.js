'use strict';
// Sends thousands of mails through the real nodemailer the way the console
// does and writes what reaches the transport, so the Go tests can replay it.
// See README.md in this folder.
//
//   node message.js                        writes ../message.json.gz
//   node message.js --mix 50000 --seed 7 --out out.json
//                                          writes that many more random mails
//   node message.js --inputs in.json --out out.json
//                                          runs on a JSON array of mails
//                                          ({from, to, subject, text, html})
//
// The mails go through transporter.sendMail({from, to, subject, text, html})
// with a transport that records mail.message.getEnvelope(),
// mail.message.messageId() and the bytes of mail.message.createReadStream(),
// which is what nodemailer's SMTP transport takes from a mail. The clock and
// crypto.randomBytes are replaced, and every vector names the time and the
// random bytes it was built with.
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');
const Module = require('module');

const WEB = process.env.WEB_NODEJS_DIR || '/repo/web-nodejs';
const nodemailer = require(path.join(WEB, 'node_modules/nodemailer'));
const domains = require('./addresses.js');

// --- a clock and a random source the script controls -----------------------

const RealDate = Date;
let fixedNow = 0;
globalThis.Date = class extends RealDate {
    constructor(...args) {
        if (args.length) {
            super(...args);
        } else {
            super(fixedNow);
        }
    }
    static now() {
        return fixedNow;
    }
};

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

const randomByte = prng(0xb0a710ad);
let drawn = [];
crypto.randomBytes = size => {
    const bytes = Buffer.alloc(size);
    for (let i = 0; i < size; i++) {
        bytes[i] = Math.floor(randomByte() * 256);
    }
    drawn.push(bytes);
    return bytes;
};

// --- the transport that records ---------------------------------------------

const transporter = nodemailer.createTransport({
    name: 'oracle',
    version: '1',
    send(mail, callback) {
        const envelope = mail.message.getEnvelope();
        const messageId = mail.message.messageId();
        const chunks = [];
        const stream = mail.message.createReadStream();
        stream.on('data', chunk => chunks.push(chunk));
        stream.once('error', callback);
        stream.once('end', () => callback(null, { envelope, messageId, raw: Buffer.concat(chunks) }));
    }
});

// Output over this size is recorded as a hash
const MAX_RAW = 24 * 1024;
// what the SMTP layer would put on the wire: half a surrogate pair is U+FFFD
const wire = str => Buffer.from(str, 'utf8').toString('utf8');
const wellFormed = value => (Array.isArray(value) ? value.every(wellFormed) : typeof value !== 'string' || value.isWellFormed());

const vectors = [];
let skipped = 0;
let clock = RealDate.UTC(2026, 10, 10, 19, 25, 17);

// record sends one mail and keeps what came of it. Go gets its input as
// UTF-8, so a mail with half a surrogate pair in it is left out.
async function record(data, when) {
    if (![data.from, data.to, data.subject, data.text, data.html].every(wellFormed)) {
        skipped++;
        return;
    }
    if (when === undefined) {
        // a new second and a new day now and then
        clock += 86400000 * 3 + 3723000;
        when = clock;
    }
    fixedNow = when;
    drawn = [];
    const vector = { now: when };
    for (const key of ['from', 'to', 'subject', 'text', 'html']) {
        if (data[key] !== undefined) {
            vector[key] = data[key];
        }
    }
    try {
        const info = await realSendMail({ from: data.from, to: data.to, subject: data.subject, text: data.text, html: data.html });
        vector.envFrom = info.envelope.from === false ? false : wire(info.envelope.from);
        vector.envTo = info.envelope.to.map(wire);
        vector.id = wire(info.messageId);
        if (info.raw.length > MAX_RAW || vector.envTo.length > 2000) {
            vector.rawLength = info.raw.length;
            vector.rawSha256 = crypto.createHash('sha256').update(info.raw).digest('hex');
            if (vector.envTo.length > 2000) {
                vector.envToSha256 = crypto.createHash('sha256').update(vector.envTo.join('\n')).digest('hex');
                vector.envToCount = vector.envTo.length;
                delete vector.envTo;
            }
        } else {
            // one character per byte
            vector.raw = info.raw.toString('latin1');
        }
    } catch (err) {
        vector.err = err.message;
    }
    vector.rand = Buffer.concat(drawn).toString('hex');
    vectors.push(vector);
}

const realSendMail = transporter.sendMail.bind(transporter);

// --- the console's own mails ------------------------------------------------

// services/emailService.js is loaded as it is. Its database adapter is
// replaced by one that hands out the SMTP settings below, and the transport
// it creates is the recording one, so every mail it sends ends up in record.
let smtpFrom = 'betterdesk@localhost';
const adapterFile = require.resolve(path.join(WEB, 'services/dbAdapter.js'));
const adapterModule = new Module(adapterFile);
adapterModule.filename = adapterFile;
adapterModule.loaded = true;
adapterModule.exports = {
    getAdapter: () => ({
        getSetting: async () => JSON.stringify({ host: 'smtp.example.com', port: 587, from: smtpFrom })
    })
};
require.cache[adapterFile] = adapterModule;

let pending = [];
nodemailer.createTransport = () => ({
    sendMail: data => {
        const done = record(data);
        pending.push(done);
        return done;
    }
});
const quiet = fn => async (...args) => {
    const { log, warn, error } = console;
    console.log = console.warn = console.error = () => {};
    try {
        return await fn(...args);
    } finally {
        Object.assign(console, { log, warn, error });
    }
};
const emailService = require(path.join(WEB, 'services/emailService.js'));
const sendAlertEmail = quiet(emailService.sendAlertEmail);
const sendHelpRequestEmail = quiet(emailService.sendHelpRequestEmail);

// --- material ---------------------------------------------------------------

const rep = (str, count) => str.repeat(count);
const NAMES = [
    'CPU high', 'Disk usage > 90%', 'Offline for 5 minutes', 'B\u{fc}ro-PC "Empfang"', 'Server (K\u{f6}ln) #2', 'M\u{fc}ller & S\u{f6}hne GmbH',
    '\u{65e5}\u{672c}\u{8a9e}\u{306e}\u{30eb}\u{30fc}\u{30eb}', '\u{41f}\u{440}\u{430}\u{432}\u{438}\u{43b}\u{43e} \u{2116}1', '\u{642}\u{627}\u{639}\u{62f}\u{629} \u{627}\u{644}\u{62a}\u{646}\u{628}\u{64a}\u{647}',
    '\u{5e9}\u{5dc}\u{5d5}\u{5dd} \u{5e2}\u{5d5}\u{5dc}\u{5dd}', '\u{1f600} alert \u{1f525}', 'caf\u{e9}', 'na\u{ef}ve r\u{e9}sum\u{e9}', '\u{39f}\u{394}\u{39f}\u{3a3}', 'x', 'A', ' ', '  two  spaces  ',
    'trailing space ', ' leading space', 'tab\there', 'line\nbreak', 'cr\rreturn', 'crlf\r\nSubject: injected', 'crlf\r\n\r\nbody text',
    'nul\x00byte', 'bell\x07', 'esc\x1b[31mred', 'del\x7f', 'vt\x0bff\x0c', '=?UTF-8?Q?fake?=', '=?UTF-8?B?ZmFrZQ==?= tail', 'a=b?c_d', '?=', '=', '_', '"quoted"',
    'back\\slash', '<script>alert(1)</script>', "O'Brien & Sons", 'semi;colon, comma', '100% (approx.)', 'a@b.c', 'user@example.com <x@y.z>',
    rep('a', 60), rep('a', 76), rep('a', 200), rep('word ', 40), rep('\u{e4}', 30), rep('\u{e4}\u{f6}\u{fc} ', 20), rep('\u{1f600}', 20), rep('\u{65e5}\u{672c}', 40),
    'mixed ' + rep('\u{e4}', 5) + ' and ' + rep('b', 70), 'DESKTOP-4F7Q2K1', 'srv-01.corp.example', '192.168.1.20', 'fe80::1', 'Zw\u{f6}lf Boxk\u{e4}mpfer jagen Viktor quer \u{fc}ber den gro\u{df}en Sylter Deich',
    '\u{a0}nbsp\u{a0}', 'zero\u{200b}width', 'rtl\u{202e}override', 'line\u{2028}separator', 'e\u{301}combining', 'i\u{307}', '\u{feff}bom'
];
const MESSAGES = [
    'CPU usage is 97% (threshold 90%)', '', 'x', 'Device went offline', 'Free space on C: is 1.2 GB', 'Line one\nLine two\nLine three',
    'Line one\r\nLine two\r\n', 'ends with newline\n', '\nstarts with newline', 'a\rb', 'bare\rcr\rand\nlf\r\nmix', '.', '.\n', '.leading dot\n..two dots\n.\nend',
    'From here on\nFrom: someone\n>From quoted', 'tab\tseparated\tvalues', 'trailing spaces   \nnext line \t\nlast  ', '   ', '\t', ' \n \n', 'a ', 'a\t',
    'nul\x00in\x00body', 'ctrl\x01\x02\x1f', 'del\x7fete', 'Gr\u{fc}\u{df}e aus K\u{f6}ln', '\u{65e5}\u{672c}\u{8a9e}\u{306e}\u{30e1}\u{30c3}\u{30bb}\u{30fc}\u{30b8}\u{3067}\u{3059}\u{3002}',
    '\u{1f600}\u{1f601}\u{1f602}', 'a = b; c == d; e=f', '=', '==', '=\n=', 'soft=\nbreak', rep('=', 100), rep('a', 76), rep('a', 77), rep('a', 78), rep('a', 998), rep('a', 999),
    rep('a', 1200), rep('a ', 600), rep('\u{e4}', 200), rep('\u{e4} ', 100), rep('\u{1f600}', 100), rep('x', 75) + '\u{e4}', rep('x', 74) + '\u{e4}\u{e4}', rep('x', 73) + '=',
    rep('0123456789', 7) + '.,!?' + rep('b', 30), rep('word, ', 30) + rep('\u{e4}', 40), rep('line of text\n', 50), rep(rep('y', 80) + '\n', 5),
    rep('\u{e4}\u{f6}\u{fc}\u{df} text with some umlauts and enough plain latin letters to choose quoted-printable\n', 4),
    rep('\u{65e5}\u{672c}\u{8a9e} ', 60), 'mostly ascii with one \u{20ac} sign and a long line ' + rep('z', 90), '<b>bold</b> & "quoted" \'single\'', 'https://example.com/a?b=c&d=e#f',
    'x'.padEnd(75, 'x') + ' ' + 'y', rep('x', 76) + ' ', rep('x', 75) + ' \n', rep(' ', 80), rep('\t', 80), rep('. ', 50), rep('!?', 50),
    'line\u{2028}sep and para\u{2029}sep', 'nbsp\u{a0}here', '\u{feff}bom first', 'ends with cr\r', 'ends with crlf\r\n', 'ends with lfcr\n\r', '\r', '\n', '\r\n', '\n\n\n'
];
const FROMS = [
    'betterdesk@localhost', 'BetterDesk <noreply@example.com>', '"BetterDesk Console" <console@example.com>', 'noreply@example.com',
    'B\u{fc}ro Technik <technik@m\u{fc}ller.de>', '"M\u{fc}ller, J\u{fc}rgen" <j.mueller@example.de>', '\u{7ba1}\u{7406}\u{8005} <admin@example.jp>', 'j\u{fc}rgen@m\u{fc}ller.de',
    'Alerts <ALERTS@EXAMPLE.COM>', 'alerts@xn--mller-kva.de', 'alerts@[192.168.1.10]', 'alerts@[IPv6:::1]', 'alerts@192.168.1', 'root', '<root>', 'Just A Name',
    '', ' ', 'a@b.c, d@e.f', 'First <a@b.c>, Second <d@e.f>', 'Team: a@b.c, d@e.f;', 'undisclosed-recipients:;', 'a@b.c\r\nBcc: victim@example.com',
    'Name\r\nX-Injected: 1 <a@b.c>', '"Name\r\n" <a@b.c>', 'a@b.c\x00', 'a\x00@b.c', '<a@b.c> <d@e.f>', 'a@b.c (comment)', '(comment) a@b.c', 'a@b@c', '"a@b"@c.d',
    'O\'Neil <oneil@example.com>', 'Name With "Quotes" <q@example.com>', 'back\\slash <b@example.com>', '=?UTF-8?Q?fake?= <f@example.com>', '\u{1f600} <smile@example.com>',
    'Very ' + rep('Long ', 30) + 'Name <long@example.com>', rep('\u{e4}', 60) + ' <ae@example.com>', rep('l', 70) + '@' + rep('d', 60) + '.example.com', 'user@' + rep('sub.', 40) + 'example.com',
    'a b@example.com', '"a b"@example.com', 'user@exa mple.com', 'user@example.com.', 'user@.example.com', '@example.com', 'user@', '@', '<>', '<<>>', ',', ';', ':', ':;',
    'user@EXAMPLE.com', 'user@\u{39f}\u{394}\u{39f}\u{3a3}.gr', 'user@stra\u{df}e.de', 'user@compa\u{ad}ny.com', 'user@0x7f.1', 'user@[::ffff:1.2.3.4]', '\u{442}\u{435}\u{441}\u{442}@\u{43f}\u{440}\u{438}\u{43c}\u{435}\u{440}.\u{440}\u{444}',
    'user@xn--a', 'user@foo.1', 'user@evil.example/mail.corp.example', 'user@a,b.com', 'user@a;b.com', '"user@evil.com"@good.com', 'user@evil.com@good.com'
];
const TOS = [
    'admin@example.com', 'a@example.com, b@example.com', 'a@example.com,b@example.com,c@example.com', 'a@example.com; b@example.com',
    'Admin <admin@example.com>', '"Admin, Chief" <chief@example.com>, ops@example.com', 'Chief, Admin <chief@example.com>', 'J\u{fc}rgen M\u{fc}ller <jm@m\u{fc}ller.de>',
    'j\u{fc}rgen@m\u{fc}ller.de', 'admin@EXAMPLE.COM, ADMIN@example.com, admin@example.com', 'a@b.c, a@b.c, A@b.c, a@B.C', 'Ops: a@b.c, d@e.f;, g@h.i', 'Empty:;',
    'undisclosed-recipients:;', '', ' ', ',', ', ,', 'not an address', 'no at sign, another one', 'admin', '<admin>', 'admin@', '@example.com',
    'a@b.c\r\nBcc: victim@example.com', 'a@b.c\nTo: other@example.com', 'a@b.c>\r\nRCPT TO:<victim@example.com', 'a@b.c\r\n\r\ninjected body', 'a\x00b@c.d',
    'a@b.c\x00', '<a@b.c>\x7f', 'a@b.c (Bcc: x@y.z)', '"a@b.c, d@e.f" <real@example.com>', 'a@b.c <d@e.f>', '<a@b.c> d@e.f', 'a@b.c d@e.f',
    'admin@[10.0.0.1]', 'admin@[::1]', 'admin@127.1', 'admin@xn--mller-kva.de', 'admin@M\u{dc}LLER.de', '\u{7528}\u{6237}@\u{4f8b}\u{5b50}.\u{6d4b}\u{8bd5}', 'user@\u{4f8b}\u{5b50}.\u{6d4b}\u{8bd5}',
    '"quoted local"@example.com', 'a..b@example.com', '.a@example.com', '"a\\"b"@example.com', 'a"b@example.com', 'first.last+tag@example.com',
    '\u{1f600}@example.com', 'x@\u{1f600}.example', 'a@b.c, \u{fc}@b.c, a@\u{fc}.c', rep('r', 300) + '@example.com', 'a@' + rep('x', 64) + '.com', 'a@' + rep('\u{fc}', 64) + '.com',
    '=?UTF-8?Q?fake?= <f@example.com>', 'Name (comment) <n@example.com>', 'Group: Name <a@b.c>, "Other, Name" <d@e.f>;', 'A: a@b.c; B: d@e.f;'
];
const many = (count, fn) => Array.from({ length: count }, (unused, i) => fn(i));
const TO_ARRAYS = [
    [], [''], ['admin@example.com'], ['a@example.com', 'b@example.com'], ['a@example.com, b@example.com', 'c@example.com'], ['a@example.com', '', 'a@example.com'],
    ['Chief', 'Admin <chief@example.com>'], ['Chief, Admin <chief@example.com>'], ['a@b.c', 'A@B.C', 'a@b.c '], ['not an address'], ['', ' ', ','],
    ['a@b.c\r\nBcc: x@y.z', 'd@e.f'], ['Team: a@b.c', 'd@e.f;'], ['J\u{fc}rgen <j@m\u{fc}ller.de>', '\u{7528}\u{6237}@\u{4f8b}\u{5b50}.\u{6d4b}\u{8bd5}'],
    many(150, i => 'user' + i + '@example.com'), many(400, i => 'User ' + i + ' <user' + (i % 250) + '@example.com>'),
    many(300, i => 'u' + i + '@d' + (i % 7) + '.example'), [many(120, i => 'list' + i + '@example.org').join(', ')], many(60, i => 'N\u{e4}me ' + i + ' <n' + i + '@ex\u{e4}mple.de>')
];
const SUBJECTS = [
    'Test', '', ' ', '   ', '\t', 'Hello World', '[BetterDesk Alert] CPU high \u{2014} CRITICAL', 'Help request \u{2014} DESKTOP-1', 'Re: Fwd: [x] y',
    ...NAMES, ...many(30, i => rep('s', 60 + i)), ...many(20, i => rep('s', 60 + i) + ' tail'), ...many(12, i => rep('word ', 13) + rep('w', i)),
    ...many(45, i => rep('\u{e4}', i + 1)), ...many(25, i => rep('a', i) + '\u{e4}' + rep('b', 30)), ...many(20, i => rep('\u{65e5}', i + 1)), ...many(16, i => rep('\u{1f600}', i + 1)),
    ...many(16, i => rep('a', i) + rep('\u{1f600}', 9)), ...many(12, i => rep('x', 20 + i) + ' "q" ' + rep('y', 20)), ...many(10, i => rep('?', i + 1) + rep('=', i) + rep('_', i)),
    'ends with space ' + '\u{e4} ', '\u{e4} \u{e4} \u{e4}  \u{e4}', 'a\u{e4}b c d e f g h i j k l m n o p q r s t u v w x y z', rep('\u{e4}b ', 30), rep('ab\u{e4} ', 30), rep('Abc ', 5) + '\u{e4}',
    'more latin than others \u{e4}\u{f6}\u{fc}', '\u{e4}\u{f6}\u{fc}\u{df} abc', 'abcd \u{e4}\u{f6}\u{fc}\u{df}', 'ABC\x01', 'AB\x01\x02', '\x01', 'a\tb', 'a\x7fb', 'DEL only \x7f',
    'tab\tonly', 'x'.padEnd(74, 'x') + ' y', 'x'.padEnd(75, 'x') + ' y', 'x'.padEnd(66, 'x') + ' yy', 'x'.padEnd(67, 'x') + ' yy', rep('x', 70) + '  ' + rep('y', 10), rep(' ', 100), 'a' + rep(' ', 100) + 'b',
    rep('ab ', 26), rep('abc', 26), rep('a ', 200), rep('ab', 600), rep('a', 998), rep('a', 1500)
];

// --- the mails ---------------------------------------------------------------

async function build() {
    const random = prng(20261110);
    const pick = list => list[Math.floor(random() * list.length)];
    const body = '<p>Hello</p>';

    // the mails the console's alert rules send
    const severities = ['info', 'warning', 'critical', 'Critical', 'sch\u{f6}n'];
    for (let i = 0; i < NAMES.length; i++) {
        for (let j = 0; j < 5; j++) {
            smtpFrom = j === 0 ? 'betterdesk@localhost' : pick(FROMS);
            const alert = { severity: severities[(i + j) % severities.length], device_id: j % 3 ? pick(NAMES) : '', message: pick(MESSAGES), triggered_at: '2026-11-10 19:25:' + String(i % 60).padStart(2, '0') };
            const rule = {
                name: NAMES[i], description: j % 2 ? pick(MESSAGES) : '', condition_type: 'cpu_usage', condition_op: '>', condition_value: 90 + j,
                notify_emails: j === 4 ? pick(TOS) || 'admin@example.com' : 'admin@example.com, ' + pick(TOS)
            };
            await sendAlertEmail(alert, rule);
        }
    }
    for (let i = 0; i < MESSAGES.length; i++) {
        smtpFrom = FROMS[i % FROMS.length];
        await sendAlertEmail({ severity: 'warning', device_id: 'DESKTOP-' + i, message: MESSAGES[i], triggered_at: new RealDate(clock).toISOString() },
            { name: pick(NAMES), description: MESSAGES[(i * 7) % MESSAGES.length], condition_type: 'offline', condition_op: '>=', condition_value: 5, notify_emails: TOS[i % TOS.length] || 'ops@example.com' });
    }
    // the mails for help requests
    for (let i = 0; i < NAMES.length; i++) {
        for (let j = 0; j < 4; j++) {
            smtpFrom = j === 0 ? 'betterdesk@localhost' : pick(FROMS);
            const helpRequest = { hostname: j === 3 ? '' : NAMES[i], device_id: j % 2 ? '' : pick(NAMES), message: j === 2 ? '' : pick(MESSAGES) };
            const to = j % 2 ? pick(TO_ARRAYS) : pick(TOS);
            await sendHelpRequestEmail({ to, helpRequest, folderName: j === 1 ? pick(NAMES) : '' });
        }
    }
    for (let i = 0; i < MESSAGES.length; i++) {
        smtpFrom = 'Support <support@example.com>';
        await sendHelpRequestEmail({ to: ['admin@example.com', 'second@example.com'], helpRequest: { hostname: 'PC-' + i, device_id: '1234567' + i, message: MESSAGES[i] }, folderName: i % 3 ? '' : pick(NAMES) });
    }
    await Promise.all(pending);
    pending = [];

    // every subject, sender and recipient on its own
    for (const subject of SUBJECTS) {
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject, text: 'Hello' });
    }
    for (const from of FROMS) {
        await record({ from, to: 'admin@example.com', subject: 'Test', text: 'Hello' });
        await record({ from, to: 'admin@example.com', subject: 'Test', text: 'Hello', html: body });
    }
    for (const to of [...TOS, ...TO_ARRAYS, undefined]) {
        await record({ from: 'betterdesk@localhost', to, subject: 'Test', text: 'Hello' });
        await record({ from: 'Sender <sender@example.org>', to, subject: 'Test', html: body });
    }
    for (const from of FROMS) {
        for (let i = 0; i < 4; i++) {
            await record({ from, to: i % 2 ? pick(TO_ARRAYS) : pick(TOS), subject: pick(SUBJECTS), text: i < 2 ? pick(MESSAGES) : undefined, html: i > 0 ? pick(MESSAGES) : undefined });
        }
    }
    // an address as display name and as address, in every header
    for (const address of [...FROMS, ...TOS]) {
        for (const name of ['', 'Plain Name', 'N\u{e4}me', '"quoted, name"', 'a.b', '\u{65e5}\u{672c}']) {
            const value = name ? name + ' <' + address + '>' : '<' + address + '>';
            await record({ from: value, to: [value, address], subject: 'Test', text: 'Hello' });
        }
    }
    // the domains and local parts of the address vectors, in whole mails
    for (const domain of domains.DOMAINS) {
        await record({ from: 'Sender <sender@' + domain + '>', to: ['admin@' + domain, 'J\u{fc}rgen <j\u{fc}rgen@' + domain + '>'], subject: 'Domain', text: 'Hello' });
    }
    for (const local of domains.LOCALS) {
        await record({ from: local + '@example.com', to: local + '@m\u{fc}ller.de, <' + local + '@example.com>, ' + local, subject: 'Local part', text: 'Hello' });
    }
    for (let i = 0; i < 1200; i++) {
        const address = () => pick(['a', 'user', 'Us\u{e9}r', pick(domains.LOCALS)]) + '@' + domains.DOMAIN();
        const named = () => pick(['', '', 'Name ', '"N, N" ', 'N\u{e4}me ']) + '<' + address() + '>';
        await record({ from: i % 3 ? named() : address(), to: i % 2 ? [address(), named(), address()] : address() + ', ' + named(), subject: 'Domains', html: body });
    }

    // every body alone, as text, as html and as both, and the four ways of having bodies
    for (const message of MESSAGES) {
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Body', text: message });
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Body', html: message });
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Body', text: message, html: pick(MESSAGES) });
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Body', text: pick(MESSAGES), html: message });
    }
    for (const text of [undefined, '', 'plain', 'pl\u{e4}in']) {
        for (const html of [undefined, '', '<p>html</p>', '<p>h\u{e4}tml</p>']) {
            for (const subject of ['', 'S', 'S\u{e4}']) {
                await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject, text, html });
                await record({ from: '', to: [], subject, text, html });
            }
        }
    }
    // line lengths around the limits of the encoders
    const fillers = ['a', 'ab ', '=', '\u{e4}', 'a\u{e4}', 'aaa\u{e4}', '\u{20ac}', 'ab\u{20ac}', '\u{1f600}', 'abc\u{1f600}', '.', ' ', 'a.', 'a ', 'a\t', '\u{e4} ', 'ab=', 'a,b!c?d'];
    for (const filler of fillers) {
        for (let length = 60; length <= 82; length++) {
            const line = rep(filler, Math.ceil(length / filler.length)).slice(0, length);
            if (!line.isWellFormed()) {
                continue;
            }
            await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Lines', text: line + '\n' + line + ' tail\n\u{e4}' + line });
        }
        for (const length of [150, 151, 152, 153, 228, 300, 997, 998, 999, 1000, 2000]) {
            const line = rep(filler, length);
            await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Lines', html: line, text: length % 2 ? line + '\r\n' + line : undefined });
        }
    }
    for (let length = 0; length <= 130; length++) {
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Base64', text: rep('\u{65e5}', length) });
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Base64', html: rep('\u{e4}', length) + rep('\x01', length % 3) });
    }

    // dates
    const dates = [0, 1, 999, 1000, -1, -1000, 951782400000, 1709164800000, 1735689599999, 1735689600000, 253402300799999, 4102444800000,
        RealDate.UTC(2026, 0, 1), RealDate.UTC(2026, 1, 3, 4, 5, 6), RealDate.UTC(2026, 11, 31, 23, 59, 59), RealDate.UTC(1999, 8, 9, 9, 9, 9), RealDate.UTC(1969, 6, 20),
        RealDate.UTC(1000, 0, 1), RealDate.UTC(999, 11, 31), RealDate.UTC(100, 5, 5), -62135596800000, -62167219200000, 253402300800000, RealDate.UTC(12345, 5, 6)];
    for (let i = 0; i < 12; i++) {
        dates.push(RealDate.UTC(2026, i, 1 + i * 2, i * 2, i * 5, i * 4, i * 80));
    }
    for (let i = 0; i < 7; i++) {
        dates.push(RealDate.UTC(2027, 2, 1 + i));
    }
    for (const when of dates) {
        await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Date', text: 'Hello' }, when);
    }

    await mix(random, 5200);

    // the large ones
    const big = many(3000, i => 'line ' + i + ' of a long text with a few words in it').join('\n');
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', text: big });
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', text: big.replace(/ a /g, ' \u{e4} '), html: rep('\u{65e5}\u{672c}\u{8a9e}', 30000) });
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', text: rep('q', 65500) + rep(' \u{e4}=.', 40) + rep('z', 70000) });
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', text: 'S\u{fc}\u{df}' + rep('abcdefghij', 6553) + rep(' ', 70000) });
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', text: 'S\u{fc}\u{df} and enough latin letters' + rep(' \t', 30000) });
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', text: rep('abcdefg ', 16384) + 'x' });
    await record({ from: 'betterdesk@localhost', to: 'admin@example.com', subject: 'Big', html: rep('abcdefg ', 16383) + 'x\x01' + rep('abcdefg hijklmnop.', 5000) });
    await record({ from: 'betterdesk@localhost', to: many(100000, i => 'r' + i + '@example.com'), subject: 'Many', text: 'Hello' });
    await record({ from: 'betterdesk@localhost', to: many(100001, i => 'r' + i + '@example.com'), subject: 'Too many', text: 'Hello' });
    await record({ from: 'betterdesk@localhost', to: many(100001, i => 'r' + (i % 100000) + '@example.com').join(','), subject: 'Many, one twice', text: 'Hello' });
}

// mix sends mails put together at random from the material above and from
// random text of many scripts.
async function mix(random, count) {
    const pick = list => list[Math.floor(random() * list.length)];
    const alphabets = [
        'abcdefghijklmnopqrstuvwxyz', 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', '0123456789', '      ', ' \t', '\r\n', '\n\n', '.,!?;:', '=?_"\\', '<>()[]@', '\'`~^|{}%&#*+-/',
        '\x00\x01\x07\x08\x0b\x0c\x0e\x1b\x1f\x7f', '\u{e4}\u{f6}\u{fc}\u{df}\u{c4}\u{d6}\u{dc}\u{e9}\u{e8}\u{f1}', '\u{3b1}\u{3b2}\u{3b3}\u{3a3}\u{3c3}\u{3c2}', '\u{430}\u{431}\u{432}\u{433}\u{434}', '\u{5d0}\u{5d1}\u{5d2}', '\u{627}\u{628}\u{62a}\u{661}\u{662}',
        '\u{65e5}\u{672c}\u{8a9e}\u{4e2d}\u{6587}', '\u{1f600}\u{1f525}\u{1f44d}\u{1f3fb}', '\u{301}\u{308}\u{323}', '\u{a0}\u{2028}\u{2029}\u{200b}\u{200d}\u{feff}\u{3000}', '\u{80}\u{85}\u{9f}\u{ad}', '\u{fffd}\u{ffff}\u{e000}\u{10ffff}'
    ];
    const randomText = maxLength => {
        const length = Math.floor(Math.pow(random(), 2.2) * maxLength);
        const used = many(1 + Math.floor(random() * 4), () => pick(alphabets));
        let out = '';
        for (let i = 0; i < length; i++) {
            const chars = Array.from(random() < 0.6 ? used[0] : pick(used));
            out += chars[Math.floor(random() * chars.length)];
        }
        return out;
    };
    const randomAddress = () => {
        const kind = random();
        if (kind < 0.3) {
            return pick(TOS);
        }
        if (kind < 0.4) {
            return pick(FROMS);
        }
        const local = random() < 0.8 ? randomText(12).replace(/[\s,;:<>()@"\\[\]]/g, '') || 'u' : randomText(14);
        const domain = random() < 0.8 ? (randomText(10).replace(/[\s,;:<>()@"\\[\]]/g, '') || 'd') + '.example' : randomText(16);
        const address = local + '@' + domain;
        const named = random();
        if (named < 0.4) {
            return address;
        }
        if (named < 0.7) {
            return randomText(20) + ' <' + address + '>';
        }
        return '"' + randomText(20) + '" <' + address + '>';
    };
    const randomRecipients = () => {
        const kind = random();
        if (kind < 0.25) {
            return randomAddress();
        }
        if (kind < 0.55) {
            return many(1 + Math.floor(random() * 5), randomAddress).join(pick([', ', ',', '; ', ' , ']));
        }
        if (kind < 0.9) {
            return many(Math.floor(random() * 6), randomAddress);
        }
        return randomText(60);
    };
    for (let i = 0; i < count; i++) {
        const shape = random();
        const bodyLength = random() < 0.15 ? 1500 : 300;
        await record({
            from: random() < 0.6 ? pick(FROMS) : randomAddress(),
            to: randomRecipients(),
            subject: random() < 0.3 ? pick(SUBJECTS) : randomText(160),
            text: shape < 0.75 ? (random() < 0.2 ? pick(MESSAGES) : randomText(bodyLength)) : undefined,
            html: shape > 0.4 ? (random() < 0.2 ? pick(MESSAGES) : randomText(bodyLength)) : undefined
        });
    }
}

// --- output ------------------------------------------------------------------

// JSON with everything outside printable ASCII escaped, one UTF-16 code unit at a time
const NOT_PRINTABLE_ASCII = /[^ -~]/g;
const BACKSLASH = String.fromCharCode(92);
const ascii = json => json.replace(NOT_PRINTABLE_ASCII, c => BACKSLASH + 'u' + c.charCodeAt(0).toString(16).padStart(4, '0'));
const arg = name => (process.argv.includes(name) ? process.argv[process.argv.indexOf(name) + 1] : undefined);

(async () => {
    let out = path.join(__dirname, '..', 'message.json.gz');
    if (arg('--inputs')) {
        out = arg('--out');
        for (const mail of JSON.parse(fs.readFileSync(arg('--inputs'), 'utf8'))) {
            await record(mail);
        }
    } else if (arg('--mix')) {
        out = arg('--out');
        await mix(prng(Number(arg('--seed')) || 1), Number(arg('--mix')));
    } else {
        await build();
    }
    const lines = vectors.map(vector => ascii(JSON.stringify(vector))).join('\n') + '\n';
    fs.writeFileSync(out, out.endsWith('.gz') ? zlib.gzipSync(lines, { level: 9 }) : lines);
    console.log(vectors.length + ' vectors written to ' + out + (skipped ? ', ' + skipped + ' mails with half a surrogate pair left out' : ''));
})().catch(err => {
    console.error(err);
    process.exit(1);
});
