'use strict';

// Records what single parts of nodemailer and of Node do with many inputs,
// without a connection: the reply parser, the EHLO parser, the DATA stream,
// CRAM-MD5, the envelope commands, the EHLO name, the server message id and
// Node's check of a certificate's names. Writes ../vectors.json.

const fs = require('fs');
const path = require('path');
const tls = require('tls');
const crypto = require('crypto');
const { nodemailerPath, setHostname, errorFields } = require('./common');

const SMTPConnection = require(nodemailerPath + '/dist/cjs/smtp-connection/index.js');
const DataStream = require(nodemailerPath + '/dist/cjs/smtp-connection/data-stream.js');
const { parseServerMessageId } = require(nodemailerPath + '/dist/cjs/smtp-connection/server-message-id.js');

// a small seeded generator, so that a new recording gives the same inputs
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
const rand = prng(20261110);
const int = n => Math.floor(rand() * n);
const pick = list => list[int(list.length)];
const tick = () => new Promise(resolve => setImmediate(resolve));

function connection(options) {
    const conn = new SMTPConnection(options || {});
    conn.on('error', () => {});
    return conn;
}

// ---------------------------------------------------------------- EHLO

function ehloVector(reply, secure) {
    const conn = connection();
    conn.secure = secure;
    const sent = [];
    let connected = false;
    let error = null;
    conn._sendCommand = cmd => sent.push(cmd);
    conn.on('connect', () => {
        connected = true;
    });
    conn._onError = (err, type, data, command) => {
        error = errorFields(conn._formatError(err, type, data, command));
    };
    conn._actionEHLO(reply);
    const out = { reply, secure, sent, connected };
    if (error) {
        out.error = error;
    }
    if (connected) {
        out.allowsAuth = conn.allowsAuth;
        out.supportedAuth = conn._supportedAuth;
        out.extensions = conn._supportedExtensions;
        out.maxAllowedSize = String(conn._maxAllowedSize);
        out.ehloLines = conn._ehloLines || [];
    }
    return out;
}

const ehloWords = [
    'STARTTLS', 'starttls', 'StartTLS', 'AUTH', 'auth', 'PLAIN', 'LOGIN', 'CRAM-MD5', 'XOAUTH2', 'plain', 'login', 'cram-md5',
    'PIPELINING', 'pipelining', '8BITMIME', 'SMTPUTF8', 'SIZE', 'size', 'DSN', 'REQUIRETLS', 'CHUNKING', 'ENHANCEDSTATUSCODES',
    'X', '_', '0', '12345', '99999999999999999999', '9'.repeat(400), 'sink.example.test', 'PLA\u0131N', 'LOG\u0131N', '\u017fTARTTLS',
    '\u017fIZE', '\u212aEY', 'AUTH=PLAIN', 'AUTH=', '=LOGIN', 'SIZE=5', 'STARTTLS_', 'STARTTLS9', 'XSTARTTLS', '\u00e9', '\ud83d\ude00',
];
const ehloSeps = [' ', ' ', ' ', '-', '=', '\t', '  ', '\u00a0', '\u2003', '\u2028', '\r', '', '.', ',', '\u3000', '\ufeff', '\v', '\f'];
function ehloLine() {
    let line = '';
    const n = 1 + int(4);
    for (let i = 0; i < n; i++) {
        line += (i ? pick(ehloSeps) : '') + pick(ehloWords);
    }
    if (rand() < 0.15) {
        line += pick(ehloSeps);
    }
    if (rand() < 0.1) {
        line = pick(ehloSeps) + line;
    }
    return line;
}
function ehloReplyText() {
    const code = rand() < 0.85 ? '250' : pick(['220', '2', '25', '2500', '421', '500', '550', '451', '354', '', 'x', '200', '299']);
    const n = int(6);
    const lines = [];
    for (let i = 0; i <= n; i++) {
        const sep = i === n ? pick([' ', ' ', ' ', '', '-']) : pick(['-', '-', '-', '-', ' ', '']);
        const lineCode = rand() < 0.9 ? code : pick(['250', '550', '', '25', ' 250']);
        lines.push(lineCode + sep + (i === 0 && rand() < 0.8 ? 'sink.example.test' : ehloLine()));
    }
    return lines.join(pick(['\n', '\n', '\n', '\n', '\r\n', '\n\n', '\r\r\n']));
}
function ehloVectors() {
    const out = [];
    const fixed = [
        '250 sink', '250-sink\n250 STARTTLS', '250-sink\n250-AUTH PLAIN LOGIN\n250 SIZE 1000', '250 STARTTLS', '250-STARTTLS', '250STARTTLS',
        '250 sink AUTH', '250-sink\n250 AUTH', '250-sink\n250 AUTH ', '250-sink\n250 AUTH\tLOGIN', '250-sink\n250-AUTH LOGIN\rPLAIN\n250 x',
        '250-sink\n250 AUTH LOGIN\u2028PLAIN', '250-sink\n250 AUTH\u2028PLAIN', '250-sink\n250 SIZE', '250-sink\n250 SIZE ', '250-sink\n250 SIZE\t\t77',
        '250-sink\n250 SIZE 0', '250-sink\n250 SIZE 00012', '250-sink\n250 SIZE 12 13', '250-sink\n250 SIZE -1', '250-sink\n250 SIZE 1e3',
        '250-sink\n250 SIZE 1.5', '250-sink SIZE 7\n250 SIZE 9', '250-sink\n250 SIZEABLE 5', '250-sink\n250 XSIZE 5', '250-sink\n250 X-SIZE 5',
        '250-sink\n250 SIZE ' + '9'.repeat(30), '250-sink\n250 SIZE ' + '9'.repeat(400), '250-sink\n\n250 PIPELINING', '250-sink\n250-\n250 PIPELINING',
        '250-sink\n250-   \n250    AUTH   LOGIN   ', '250-sink\n250-250-AUTH LOGIN\n250 250 PIPELINING', '250-sink\n250-12 AUTH LOGIN\n250 OK',
        '421 bye', '421-bye\n421 now', '4210 x', '500 no', '', ' ', '2', '25', '250', '250-', '250 ', '2xx', '250 full', '\uff12\uff15\uff10 wide',
        '250-sink\n250 AUTH PLAIN=LOGIN==CRAM-MD5', '250-sink\n250 AUTH=', '250-sink\n250 AUTH==PLAIN', '250-sink\n250 auth=plain',
        '250-sink\n250 AUTH PLA\u0131N LOG\u0131N', '250-sink\n250 AUTH \u017fPLAIN', '250-sink\n250 \u017fTARTTLS', '250-sink\n250 STARTTL\u017f',
        '250-sink\n250 \u212aSTARTTLS', '250-sink\n250 STARTTLS\u00e9', '250-sink\n250 STARTTLS\u0131', '250-sink\n250 8BITMIME8', '250-sink\n250 8BITMIME_',
        '250-sink\n250 8BITMIME-X', '250-sink\n250 SMTPUTF8.', '250-sink\n250 PIPELINING\u00a0', '250-sink\n250 DSN', '250-sink\n250 REQUIRETLS',
        '250-AUTH LOGIN\n250 AUTH PLAIN', '250-sink\n250-auth login\n250-AUTH CRAM-MD5\n250 AUTH PLAIN',
    ];
    for (const reply of fixed) {
        out.push(ehloVector(reply, false));
        out.push(ehloVector(reply, true));
    }
    for (let i = 0; i < 2500; i++) {
        out.push(ehloVector(ehloReplyText(), rand() < 0.3));
    }
    return out;
}

// ------------------------------------------------------------ reply parser

async function replyVector(chunks) {
    const conn = connection();
    const events = [];
    const refill = () => {
        while (conn._responseActions.length < 4) {
            conn._responseActions.push(str => {
                events.push({ reply: str });
                refill();
            });
        }
    };
    refill();
    let failed = false;
    conn._onServerClosing = () => {};
    conn._onError = (err, type, data, command) => {
        failed = true;
        events.push({ error: errorFields(conn._formatError(err, type, data, command)) });
    };
    for (const chunk of chunks) {
        if (failed) {
            break;
        }
        conn._onData(chunk);
        // let the replies queued by this chunk be handled before the next one
        for (let i = 0; i < 3; i++) {
            let before;
            do {
                before = events.length;
                await tick();
                await tick();
            } while (events.length !== before);
        }
    }
    return { chunks: chunks.map(c => c.toString('hex')), events, remainder: Buffer.from(conn._remainder, 'latin1').toString('hex') };
}

const replyPieces = [
    '250', '220', '354', '550', '5', '25', '2500', '', 'x', '250-', '250 ', '-', ' ', 'OK', 'hello there', 'sink.example.test',
    '\r\n', '\r\n', '\r\n', '\n', '\n', '\r', '\r\r\n', '\n\r', '\t', '\x0b', '\x0c', '\xa0', '\xc2\xa0', '\xe2\x80\xa8', '\xef\xbb\xbf',
    'caf\xc3\xa9', 'caf\xe9', '\xff\xfe', '\xef\xbf\xbd', '\xf0\x9f\x98\x80', '\xed\xa0\x80', '\xc0\xaf', '\x00', '\x80', '\xc3', '\xa9',
    '250-first\r\n250 second\r\n', '250-a\r\n250-b\r\n', '250 done\r\n', '451 later\r\n', '451-later\r\n451 now\r\n', '\r\n\r\n', ' \r\n',
    '\xa0\r\n', '\xc2\xa0\r\n', '0-', '00 ', '1-x\n', '-\n', '2 \n', '250--\n', '250- \n', '250-250-\n', '12345678901234567890-\n',
];
function replyBytes() {
    let s = '';
    const n = 1 + int(12);
    for (let i = 0; i < n; i++) {
        s += pick(replyPieces);
    }
    if (rand() < 0.5) {
        s += '\r\n';
    }
    return Buffer.from(s, 'latin1');
}
function split(buf) {
    const chunks = [];
    let pos = 0;
    const mode = int(4);
    while (pos < buf.length) {
        let n = buf.length - pos;
        if (mode === 1) {
            n = 1;
        } else if (mode >= 2) {
            n = 1 + int(Math.min(n, mode === 2 ? 4 : 40));
        }
        chunks.push(buf.subarray(pos, pos + n));
        pos += n;
    }
    if (!chunks.length) {
        chunks.push(Buffer.alloc(0));
    }
    return chunks;
}
async function replyVectors() {
    const out = [];
    for (let i = 0; i < 2000; i++) {
        out.push(await replyVector(split(replyBytes())));
    }
    // around the size limit, in chunks of the size a socket read delivers
    const chunked = (buf, size) => {
        const chunks = [];
        for (let pos = 0; pos < buf.length; pos += size) {
            chunks.push(buf.subarray(pos, pos + size));
        }
        return chunks;
    };
    const big = [
        ['line-over', Buffer.concat([Buffer.from('250 '), Buffer.alloc(1100000, 'x')]), 65536],
        ['line-under-then-end', Buffer.concat([Buffer.from('250 '), Buffer.alloc(1000000, 'x'), Buffer.from('\r\n')]), 65536],
        ['line-exactly-limit', Buffer.concat([Buffer.alloc(1048576, 'x'), Buffer.from('\r\n250 next\r\n')]), 1048576],
        ['line-limit-plus-one', Buffer.alloc(1048577, 'x'), 1048577],
        ['line-over-in-one-chunk-with-end', Buffer.concat([Buffer.alloc(1500000, 'x'), Buffer.from('\r\n')]), 2000000],
        ['multi-over', Buffer.from(('250-' + 'y'.repeat(94) + '\r\n').repeat(11000) + '250 done\r\n'), 65536],
        ['multi-under', Buffer.from(('250-' + 'y'.repeat(94) + '\r\n').repeat(9000) + '250 done\r\n'), 65536],
        ['multi-over-in-one-chunk', Buffer.from(('250-' + 'y'.repeat(94) + '\r\n').repeat(11000) + '250 done\r\n'), 2000000],
        ['multi-last-line-over', Buffer.from(('250-' + 'y'.repeat(94) + '\r\n').repeat(10400) + '250 ' + 'z'.repeat(60000) + '\r\n'), 2000000],
        ['remainder-over-after-lines', Buffer.concat([Buffer.from('250 ok\r\n'), Buffer.alloc(1048577, 'x')]), 2000000],
    ];
    const bigOut = [];
    for (const [name, buf, size] of big) {
        const vector = await replyVector(chunked(buf, size));
        // the inputs are rebuilt by the Go test, only the outcome is stored
        bigOut.push({
            name,
            events: vector.events.map(e => e.reply !== undefined
                ? { replyLength: e.reply.length, replySha256: crypto.createHash('sha256').update(e.reply, 'utf8').digest('hex') }
                : e),
            remainderLength: vector.remainder.length / 2,
        });
    }
    return { random: out, big: bigOut };
}

// ------------------------------------------------------------- DATA stream

function dataVector(chunks) {
    return new Promise((resolve, reject) => {
        const stream = new DataStream();
        const out = [];
        stream.on('data', chunk => out.push(chunk));
        stream.on('error', reject);
        stream.on('end', () => resolve({
            chunks: chunks.map(c => c.toString('hex')),
            out: Buffer.concat(out).toString('hex'),
            inBytes: stream.inByteCount,
            outBytes: stream.outByteCount,
        }));
        for (const chunk of chunks) {
            stream.write(chunk);
        }
        stream.end();
    });
}
async function dataVectors() {
    const out = [];
    const alphabets = ['.\r\n', '.\r\nab', '\r\n', '.', 'abc.\r\n \x00\xff', '\r', '\n'];
    for (let i = 0; i < 2500; i++) {
        const alphabet = pick(alphabets);
        const len = int(i % 10 === 0 ? 200 : 24);
        const buf = Buffer.alloc(len);
        for (let j = 0; j < len; j++) {
            buf[j] = alphabet.charCodeAt(int(alphabet.length));
        }
        out.push(await dataVector(split(buf)));
    }
    for (const fixed of ['', '.', '\r', '\n', '\r\n', '.\r\n', '\r\n.\r\n', '\n.\n', '\r.\r', 'a\r\n.\r\nb', 'a\r', 'a\n', 'a\r\n', '..', '.\r', '\r.']) {
        out.push(await dataVector([Buffer.from(fixed, 'latin1')]));
        out.push(await dataVector(Array.from(Buffer.from(fixed, 'latin1')).map(b => Buffer.from([b]))));
        out.push(await dataVector([Buffer.alloc(0), Buffer.from(fixed, 'latin1'), Buffer.alloc(0)]));
    }
    return out;
}

// ----------------------------------------------------------------- CRAM-MD5

function cramVector(reply, user, pass) {
    const conn = connection();
    conn._auth = { user, credentials: { user, pass } };
    conn._authMethod = 'CRAM-MD5';
    const sent = [];
    let error = null;
    conn._sendCommand = cmd => sent.push(cmd);
    conn._actionAUTH_CRAM_MD5(reply, err => {
        error = errorFields(err);
    });
    const out = { reply, user, pass };
    if (error) {
        out.error = error;
    } else {
        out.sent = sent[0];
    }
    return out;
}
function cramVectors() {
    const out = [];
    const b64chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/-_';
    const junk = ['=', '==', ' ', '\t', '!', '.', '\u00e9', '\u0141', '\u0100', '\ud83d\ude00', '\u00a0', '*', '\\', '"', '\u2003'];
    const creds = [['user', 'pass'], ['console@example.test', 'correct horse battery'], ['j\u00f6rg', 'p\u00e4ss \u2713 \ud83d\udd11'], ['u', 'p'.repeat(100)]];
    const challenges = [
        '', ' ', '  ', '\t', ' \t', '\u00a0', ' \u00a0', '\u2028', ' \u2028', '\r', ' \r', ' x', '  x', ' x ', ' x\r', ' x\ry', ' x\u2028y', ' x\u2029',
        '-x', ' PDE4OTYuNjk3MTcwOTUyQHNpbmsuZXhhbXBsZS50ZXN0Pg==', ' PDE4OTYuNjk3MTcwOTUyQHNpbmsuZXhhbXBsZS50ZXN0Pg', ' PDE4OTYu NjQw', ' =', ' ==', ' A', ' AA', ' AAA',
        ' AAAA', ' AAAAA', ' A=', ' AA=', ' AA==', ' AAA=', ' AA=A', ' =AAAA', ' AAAA=AAAA', ' AAAA==', ' AA==AA==', ' !!!!', ' A!A!A!A', ' \u0141\u0141\u0141\u0141',
        ' QUJD\u00e9', ' \ud83d\ude00QUJD', ' -_-_', ' +/+/', ' ////', ' ____', ' QQ', ' QUI', ' QUJD', ' QUJDRA', ' QUJDREU', ' QUJDREVG',
        ' /w==', ' gICA', ' //////////8=', '\n334 x', ' x\n334 y', '5 x', ' 334',
    ];
    for (const challenge of challenges) {
        for (const [user, pass] of creds) {
            out.push(cramVector('334' + challenge, user, pass));
        }
    }
    for (const reply of ['335 x', '33 x', '3340 x', '235 ok', '535 no', 'x', '', '334', ' 334 x']) {
        out.push(cramVector(reply, 'user', 'pass'));
    }
    for (let i = 0; i < 1500; i++) {
        let challenge = '';
        const n = int(40);
        for (let j = 0; j < n; j++) {
            challenge += rand() < 0.9 ? b64chars[int(b64chars.length)] : pick(junk);
        }
        const [user, pass] = pick(creds);
        out.push(cramVector('334' + pick([' ', ' ', ' ', '  ', '\t', '\u00a0']) + challenge, user, pass));
    }
    return out;
}

// ----------------------------------------------------------------- envelope

function envelopeVector(envelope, extensions, maxAllowedSize) {
    const conn = connection();
    const sent = [];
    let error = null;
    conn._sendCommand = cmd => sent.push(cmd);
    conn._socket = { cork() {}, uncork() {}, destroyed: false, writable: true };
    conn._supportedExtensions = extensions.slice();
    conn._maxAllowedSize = maxAllowedSize;
    const input = JSON.parse(JSON.stringify(envelope));
    conn._setEnvelope(envelope, err => {
        error = errorFields(err);
    });
    const out = { envelope: input, extensions, sent };
    if (error) {
        out.error = error;
    } else {
        out.from = conn._envelope.from;
        out.to = conn._envelope.to;
    }
    return out;
}
function envelopeVectors() {
    const out = [];
    const locals = ['sender', 'a', 'j\u00f6rg', '\u65e5\u672c', '\ud83d\ude00', 'a b', 'a<b', 'a>b', 'a\rb', 'a\nb', '"q"', 'a\\b', '\u0001', '\u007f', 'a\tb', '', '\u2028', '\u00a0x'];
    const pads = ['', '', '', ' ', '  ', '\t', '\n', '\r\n', '\u00a0', '\ufeff', '\u2028', '\u2029', '\u3000', '\u1680', '\u200a', '\u200b', '\u0085', '\v', '\f'];
    const address = () => {
        if (rand() < 0.05) {
            return pick(pads);
        }
        return pick(pads) + pick(locals) + '@' + pick(['example.test', 'b\u00fccher.example', 'x', '']) + pick(pads);
    };
    const extensionSets = [[], ['PIPELINING'], ['SMTPUTF8'], ['8BITMIME'], ['SIZE'], ['PIPELINING', 'SMTPUTF8', '8BITMIME', 'SIZE'], ['SMTPUTF8', 'SIZE'], ['DSN', 'REQUIRETLS']];
    for (let i = 0; i < 2000; i++) {
        const envelope = { from: rand() < 0.1 ? '' : address(), to: [] };
        const n = rand() < 0.05 ? 0 : 1 + int(4);
        for (let j = 0; j < n; j++) {
            envelope.to.push(address());
        }
        if (rand() < 0.4) {
            envelope.use8BitMime = rand() < 0.8;
        }
        if (rand() < 0.5) {
            envelope.size = pick([0, 1, 5, 1234, 1048576, 9007199254740991, -1]);
        }
        out.push(envelopeVector(envelope, pick(extensionSets), 0));
    }
    return out;
}

// ----------------------------------------------------------------- EHLO name

function hostnameVectors() {
    const names = [
        'console.example.test', 'betterdesk', '', '10.1.2.3', '999.999.999.999', '1.2.3.4.5', '1.2.3', '10.1.2.3\n', '\n10.1.2.3', '10.1.2.3 ',
        'fe80::1', 'CONSOLE.Example.TEST', 'h\u00f6st.example.test', 'my host.example', 'evil.example\r\nMAIL FROM:<x@example.test>',
        'console.example.test.', '.', '..', 'a.b', '1.2.3.4a', '0001.2.3.4', '1234.1.1.1', '\uff11.2.3.4', '1.2.3.\u0664', 'a\r.\nb', '\r\n', '\r.\n',
        'localhost', 'localhost.localdomain', '8c46a72a9d27', '[10.1.2.3]', '10.1.2.3.', '-.-',
    ];
    const out = [];
    for (const value of names) {
        setHostname({ value });
        out.push({ hostname: value, name: connection().name });
    }
    setHostname({ error: true });
    out.push({ error: true, name: connection().name });
    setHostname(null);
    return out;
}

// ---------------------------------------------------------- server message id

function messageIdVectors() {
    const hosts = [
        'email-smtp.eu-west-1.amazonaws.com', 'email-smtp.us-east-1.amazonaws.com', 'email-smtp-fips.us-gov-west-1.amazonaws.com',
        'vpce-0123-abcd.email-smtp.ap-southeast-2.vpce.amazonaws.com', 'EMAIL-SMTP.EU-WEST-1.AMAZONAWS.COM.', 'smtp.mail.us-east-1.awsapps.com',
        'amazonaws.com', 'eu-west-1.amazonaws.com.evil.example', 'email-smtp.eu-west-1.amazonaws.co', 'email-smtp.euwest1.amazonaws.com',
        'email-smtp.eu-west.amazonaws.com', 'email-smtp.e1-west-1.amazonaws.com', 'email-smtp.eu-west-1a.amazonaws.com', 'smtp.example.test',
        'u\u212a-west-1.amazonaws.com', 'e\u0130-west-1.amazonaws.com', '127.0.0.1', 'eu-west-1..amazonaws.com', 'x.eu-west-1.us-east-1.amazonaws.com', undefined,
    ];
    const replies = [
        '250 2.0.0 OK <DB7PR04MB4f1a2b@DB7PR04MB1234.eurprd04.prod.outlook.com> [Hostname=DB7PR04MB1234.eurprd04.prod.outlook.com]',
        '250 2.0.0 ok <abc@mail.PROD.OUTLOOK.COM>', '250 2.0.0 OK <abc@example.com>', '250 2.0.0 OK <.prod.outlook.com>', '250 2.0.0 OK <x.prod.outlook.com>',
        '250 2.0.0 OK <a b@x.prod.outlook.com>', '250 2.0.0 OK <a<b@x.prod.outlook.com>', '250 2.0.0 OK <a@x.prod.outlook.com', '250 2.0.0 OK  <a@x.prod.outlook.com>',
        ' 250 2.0.0 OK <a@x.prod.outlook.com> \r\n', '250-2.0.0 OK <a@x.prod.outlook.com>', '250 2.0.0 OK <a@x.prod.outlook.com.>', '250 2.0.0 OK <a@x.prod.outlook.comx>',
        '250 2.0.0 OK <a@x.prod.outlook.com><b@y.prod.outlook.com>', '250 2.0.0 OK <a@x.prod.outlook.com>\n250 more', '250 2.0.0 O\u212a <a@x.prod.outlook.com>',
        '250 2.0.0 OK <a@x.prod.outloo\u212a.com>', '250 2.0.0 OK <a\u00a0@x.prod.outlook.com>', '250 2.0.0 OK <a\u00e9@x.prod.outlook.com>',
        '250 Ok 0100018b5c3c0a4f-12345678-9abc-def0-1234-56789abcdef0-000000', '250 ok 0100018B5C3C0A4F-ABCDEF-000000', '250 OK 0100018b5c3c0a4f', '250 Ok 01-02',
        '250 Ok 01-', '250 Ok -01', '250 Ok 01--02', '250 Ok 0g-01', '250 Ok 01-02 ', '250 Ok 01-02\n', '250 Ok  01-02', '250 Ok 01-02 queued', '250 O\u212a 01-02',
        '250 2.0.0 Ok: queued as 4F1A2B', '', ' ', '250', '550 Ok 01-02',
    ];
    const out = [];
    for (const host of hosts) {
        for (const response of replies) {
            const id = parseServerMessageId(response, host);
            const vector = { response };
            if (host !== undefined) {
                vector.host = host;
            }
            if (id !== undefined) {
                vector.id = id;
            }
            out.push(vector);
        }
    }
    return out;
}

// ------------------------------------------- names of a certificate (Node)

function identityVectors() {
    const hosts = [
        'localhost', 'LOCALHOST', 'localhost.', 'smtp.example.test', 'SMTP.Example.Test', 'a.b.example.test', 'example.test', 'test', 'x.test',
        'xn--bcher-kva.example', 'smtp_1.example.test', 'smtp-1.example.test', '-.example.test', 'a..b', '.', '', 'foo.bar.example.test.',
        '127.0.0.1', '127.0.0.1.', '10.1.2.3', '192.0.2.7', '::1', '0:0:0:0:0:0:0:1', '2001:db8::a:1', '2001:DB8::A:1', '::ffff:10.1.2.3', 'fe80::1',
        '127.1', '127.0.1', '0x7f.0.0.1', '0x7f000001', '2130706433', '017700000001', '127.0.0.01', '1.2.3.4.5', '256.1.1.1', '1.2.3.256', '1.2.3.0x', '1.2.3.08',
        '4294967296', '4294967295', '0', '00', '0x', '1.', '1..', 'a.1', 'a.0x1', 'a.1.', '1.a', '999', '1.2.65536', '1.16777216', '0xffffffffffffffffff',
        'foo.example.test', 'bar.foo.example.test', 'foobar.example.test', 'fo.example.test', 'f.example.test', 'x.localhost', 'www.xn--bcher-kva.example',
    ];
    const altLists = [
        undefined, '', 'DNS:localhost', 'DNS:LOCALHOST', 'DNS:localhost.', 'DNS:smtp.example.test', 'DNS:*.example.test', 'DNS:*.test', 'DNS:*', 'DNS:*.*.example.test',
        'DNS:f*.example.test', 'DNS:*o.example.test', 'DNS:f*o.example.test', 'DNS:f*o*.example.test', 'DNS:foo*.example.test', 'DNS:*foo.example.test', 'DNS:fo*o.example.test',
        'DNS:*.b.example.test', 'DNS:a.*.example.test', 'DNS:xn--*.example', 'DNS:*.xn--bcher-kva.example', 'DNS:xn--bcher-kva.example', 'DNS:', 'DNS:.', 'DNS:..example.test',
        'DNS:other.example.test, DNS:smtp.example.test', 'DNS:other.example.test, IP Address:127.0.0.1', 'IP Address:127.0.0.1', 'IP Address:10.1.2.3, IP Address:0:0:0:0:0:0:0:1',
        'IP Address:2001:DB8:0:0:0:0:A:1', 'IP Address:0:0:0:0:0:FFFF:A01:203', 'IP Address:FE80:0:0:0:0:0:0:1', 'email:postmaster@example.test', 'URI:https://localhost/',
        'email:x@localhost, URI:https://smtp.example.test/', 'DNS:127.0.0.1', 'DNS:2130706433', 'DNS:smtp_1.example.test', 'DNS:sp ace.example.test', 'DNS:*.localhost',
        'DNS:b\u00fccher.example', 'DNS:example.test, DNS:*.example.test', 'IP Address:192.0.2.7, DNS:smtp.example.test, IP Address:127.0.0.1',
    ];
    const cns = [undefined, '', 'localhost', 'smtp.example.test', '*.example.test', 'elsewhere.example.test', ['first.example.test', 'localhost'], ['a', '*.example.test'], '127.0.0.1', 'LOCALHOST.'];
    const out = [];
    for (const host of hosts) {
        for (const altNames of altLists) {
            for (const cn of cns) {
                if (rand() > 0.35 && !(altNames === undefined || cn === undefined)) {
                    continue;
                }
                const subject = {};
                if (cn !== undefined) {
                    subject.CN = cn;
                }
                const err = tls.checkServerIdentity(host, { subject, subjectaltname: altNames });
                const vector = { host, cn: cn === undefined ? [] : [].concat(cn), hasCN: cn !== undefined };
                if (altNames !== undefined) {
                    vector.altNames = altNames;
                    const names = altNames === '' ? [] : altNames.split(', ');
                    vector.dns = names.filter(n => n.startsWith('DNS:')).map(n => n.slice(4));
                    vector.ipText = names.filter(n => n.startsWith('IP Address:')).map(n => n.slice(11));
                }
                if (err) {
                    vector.error = err.message;
                }
                out.push(vector);
            }
        }
    }
    return out;
}

// how Node prints the names of the test certificates
function certificateVectors() {
    const dir = path.join(__dirname, '..', 'certs');
    const out = [];
    for (const file of fs.readdirSync(dir).filter(f => f.endsWith('.chain.pem')).sort()) {
        const cert = new crypto.X509Certificate(fs.readFileSync(path.join(dir, file)));
        const subject = cert.toLegacyObject().subject;
        const vector = { name: file.replace(/\.chain\.pem$/, ''), cn: subject.CN === undefined ? [] : [].concat(subject.CN) };
        if (cert.subjectAltName !== undefined) {
            vector.altNames = cert.subjectAltName;
        }
        out.push(vector);
    }
    return out;
}

async function main() {
    // the name a refused EHLO is followed up with (HELO) comes from the machine
    setHostname({ value: 'client.example.test' });
    const vectors = {
        ehlo: ehloVectors(),
        replies: await replyVectors(),
        data: await dataVectors(),
        cram: cramVectors(),
        envelope: envelopeVectors(),
        hostname: hostnameVectors(),
        messageId: messageIdVectors(),
        identity: identityVectors(),
        certificates: certificateVectors(),
    };
    const out = path.join(__dirname, '..', 'vectors.json');
    const parts = Object.entries(vectors).map(([key, value]) => {
        if (Array.isArray(value)) {
            return JSON.stringify(key) + ': [\n' + value.map(v => JSON.stringify(v)).join(',\n') + '\n]';
        }
        return JSON.stringify(key) + ': ' + JSON.stringify(value);
    });
    fs.writeFileSync(out, '{\n' + parts.join(',\n') + '\n}\n');
    const count = Object.values(vectors).reduce((n, v) => n + (Array.isArray(v) ? v.length : v.random.length + v.big.length), 0);
    console.log(`${count} vectors written to ${out}`);
}

main().then(() => process.exit(0), err => {
    console.error(err);
    process.exit(1);
});
