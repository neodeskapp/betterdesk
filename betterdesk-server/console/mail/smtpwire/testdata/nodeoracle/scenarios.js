'use strict';

// The scenario tables of the session recordings. Every scenario names a call
// (send or verify), the options the console would store, an envelope and a
// message for send, and the script the sink plays (see sink.js).

const b64 = s => Buffer.from(s, 'utf8').toString('base64');
const latin1 = buf => Buffer.from(buf).toString('latin1');

const GREETING = '220 sink.example.test ESMTP\r\n';

function reply(code, lines) {
    return lines.map((line, i) => code + (i === lines.length - 1 ? ' ' : '-') + line + '\r\n').join('');
}

const ehloReply = features => reply('250', ['sink.example.test'].concat(features || []));

// A sink script that accepts everything. o.features is the EHLO answer,
// o.features2 the answer to the second EHLO (after STARTTLS).
function server(o = {}) {
    const rules = {
        EHLO: [[{ w: o.ehloRaw !== undefined ? o.ehloRaw : ehloReply(o.features) }]],
        HELO: [[{ w: '250 sink.example.test\r\n' }]],
        STARTTLS: [[{ w: '220 2.0.0 Ready to start TLS\r\n' }, { tls: 1 }]],
        'AUTH PLAIN': [[{ w: '235 2.7.0 Authentication successful\r\n' }]],
        'AUTH LOGIN': [[{ w: '334 VXNlcm5hbWU6\r\n' }, { cont: 'LOGIN-USER' }]],
        'LOGIN-USER': [[{ w: '334 UGFzc3dvcmQ6\r\n' }, { cont: 'LOGIN-PASS' }]],
        'LOGIN-PASS': [[{ w: '235 2.7.0 Authentication successful\r\n' }]],
        'AUTH CRAM-MD5': [[{ w: '334 ' + b64('<1896.697170952@sink.example.test>') + '\r\n' }, { cont: 'CRAM' }]],
        CRAM: [[{ w: '235 2.7.0 Authentication successful\r\n' }]],
        MAIL: [[{ w: '250 2.1.0 Ok\r\n' }]],
        RCPT: [[{ w: '250 2.1.5 Ok\r\n' }]],
        DATA: [[{ w: '354 End data with <CR><LF>.<CR><LF>\r\n' }, { data: 1 }]],
        ENDDATA: [[{ w: '250 2.0.0 Ok: queued as 4F1A2B\r\n' }]],
        QUIT: [[{ w: '221 2.0.0 Bye\r\n' }, { close: 1 }]],
    };
    if (o.features2 !== undefined || o.ehloRaw2 !== undefined) {
        rules.EHLO.push([{ w: o.ehloRaw2 !== undefined ? o.ehloRaw2 : ehloReply(o.features2) }]);
    }
    const script = { tls: !!o.tls, cert: o.cert || 'localhost', greeting: [{ w: GREETING }], rules };
    for (const [verb, lists] of Object.entries(o.rules || {})) {
        rules[verb] = lists;
    }
    if (o.greeting) {
        script.greeting = o.greeting;
    }
    return script;
}

// Replaces what the sink does at the nth line with a verb ("GREETING" is the greeting)
function at(script, verb, n, ops) {
    const copy = JSON.parse(JSON.stringify(script));
    if (verb === 'GREETING') {
        copy.greeting = ops;
        return copy;
    }
    const lists = copy.rules[verb] || [[{ w: '250 OK\r\n' }]];
    // the steps before and after the replaced one stay as they were
    while (lists.length < n + 2) {
        lists.push(JSON.parse(JSON.stringify(lists[lists.length - 1])));
    }
    lists[n] = ops;
    copy.rules[verb] = lists;
    return copy;
}

const HOST = '127.0.0.1';
const CREDS = { user: 'console@example.test', pass: 'correct horse battery' };
const ENVELOPE = { from: 'sender@example.test', to: ['first@example.test', 'second@example.test'] };
const MESSAGE = latin1(Buffer.from('From: sender@example.test\r\nTo: first@example.test\r\nSubject: wire test\r\n\r\nHello.\r\n'));
const SHORT = { greetingTimeout: 4000, socketTimeout: 800 };

const scenarios = [];
const names = new Set();

function add(name, sc) {
    if (names.has(name)) {
        throw new Error('duplicate scenario name ' + name);
    }
    names.add(name);
    const full = Object.assign({ name, call: 'send', hostname: { value: 'client.example.test' } }, sc);
    full.options = Object.assign({ host: HOST, secure: false, tlsVerify: false }, sc.options || {});
    if (full.call === 'send') {
        if (full.envelope === undefined && full.forceEnvelope === undefined) {
            full.envelope = ENVELOPE;
        }
        if (full.message === undefined) {
            full.message = { s: MESSAGE };
        }
    }
    scenarios.push(full);
}

const slug = s => String(s).replace(/[^A-Za-z0-9]+/g, '-').replace(/^-|-$/g, '').toLowerCase() || 'empty';

// ---------------------------------------------------------------- greetings

const greetings = [
    ['plain', [{ w: GREETING }]],
    ['code-only', [{ w: '220\r\n' }]],
    ['code-space', [{ w: '220 \r\n' }]],
    ['multi-line', [{ w: '220-first line\r\n220-second line\r\n220 last line\r\n' }]],
    ['four-digits', [{ w: '2200 odd code\r\n' }]],
    ['no-space', [{ w: '220sink\r\n' }]],
    ['leading-space', [{ w: ' 220 leading space\r\n' }]],
    ['after-empty-line', [{ w: '\r\n220 after an empty line\r\n' }]],
    ['after-blank-lines', [{ w: '\r\n\r\n \t\r\n220 after blank lines\r\n' }]],
    ['bare-lf', [{ w: '220 bare lf\n' }]],
    ['bare-lf-multi', [{ w: '220-a\n220 b\n' }]],
    ['utf8', [{ w: latin1(Buffer.from('220 caf\u00e9 \u2713 ready\r\n', 'utf8')) }]],
    ['latin1-byte', [{ w: '220 latin \xe9 byte\r\n' }]],
    ['nbsp-end', [{ w: '220 nbsp\xa0\r\n' }]],
    ['nbsp-line-first', [{ w: '\xa0\r\n220 after a nbsp line\r\n' }]],
    ['554', [{ w: '554 5.3.2 no service here\r\n' }]],
    ['554-multi', [{ w: '554-no\r\n554 service\r\n' }]],
    ['421', [{ w: '421 4.3.2 too busy\r\n' }]],
    ['421-multi', [{ w: '421-too busy\r\n421 come back later\r\n' }]],
    ['500', [{ w: '500 what\r\n' }]],
    ['250', [{ w: '250 hi\r\n' }]],
    ['text', [{ w: 'hello\r\n' }]],
    ['two-digits', [{ w: '22\r\n' }]],
    ['utf8-554', [{ w: latin1(Buffer.from('554 abgelehnt \u00fc\u00f6\u00e4\r\n', 'utf8')) }]],
    ['latin1-554', [{ w: '554 abgelehnt \xfc\xf6\xe4\r\n' }]],
    ['extra-reply', [{ w: '220 hi\r\n250 extra reply\r\n' }]],
    ['split', [{ w: '22' }, { sleep: 30 }, { w: '0 split greeting\r' }, { sleep: 30 }, { w: '\n' }]],
    ['split-multi', [{ w: '220-first\r\n' }, { sleep: 30 }, { w: '220 second\r\n' }]],
    ['split-bytes', Array.from('220 byte by byte\r\n').flatMap(ch => [{ w: ch }, { sleep: 5 }])],
    ['close', [{ close: 1 }]],
    ['close-after-partial', [{ w: '220 no line end' }, { close: 1 }]],
    ['close-after-554-tail', [{ w: '554 5.3.2 go away' }, { close: 1 }]],
    ['close-after-554-only', [{ w: '554' }, { close: 1 }]],
    ['close-after-450-tail', [{ w: '  450 4.3.2 later  ' }, { close: 1 }]],
    ['close-after-250-tail', [{ w: '250 fine' }, { close: 1 }]],
    ['close-after-554-line', [{ w: '554 5.3.2 go away\r\n' }, { close: 1 }]],
    ['close-after-220-line', [{ w: GREETING }, { close: 1 }]],
];
for (const [name, ops] of greetings) {
    for (const call of ['send', 'verify']) {
        add(`greeting/${name}/${call}`, { call, server: at(server(), 'GREETING', 0, ops) });
    }
}
const greetingStalls = [
    ['stall', [{ stall: 1 }]],
    ['stall-after-partial', [{ w: '220 no line end' }, { stall: 1 }]],
    ['stall-after-cr', [{ w: '220 cr only\r' }, { stall: 1 }]],
    ['stall-after-continuation', [{ w: '220-more to come\r\n' }, { stall: 1 }]],
    ['stall-after-greeting-and-partial', [{ w: '220 hi\r\n250-partial\r\n' }, { stall: 1 }]],
];
for (const [name, ops] of greetingStalls) {
    add(`greeting/${name}/greeting-timeout`, { call: 'verify', options: { greetingTimeout: 300, socketTimeout: 3000 }, server: at(server(), 'GREETING', 0, ops) });
    add(`greeting/${name}/socket-timeout`, { call: 'send', options: { greetingTimeout: 3000, socketTimeout: 300 }, server: at(server(), 'GREETING', 0, ops) });
}
add('greeting/implicit-tls/554', { call: 'verify', options: { secure: true }, server: at(server({ tls: true }), 'GREETING', 0, [{ w: '554 no\r\n' }]) });
add('greeting/implicit-tls/close', { call: 'verify', options: { secure: true }, server: at(server({ tls: true }), 'GREETING', 0, [{ close: 1 }]) });
add('greeting/implicit-tls/stall', { call: 'verify', options: { secure: true, greetingTimeout: 300, socketTimeout: 3000 }, server: at(server({ tls: true }), 'GREETING', 0, [{ stall: 1 }]) });

// --------------------------------------------------------------- EHLO replies

const RICH_ENVELOPE = { from: 'sender@example.test', to: ['rcpt@example.test', 'jos\u00e9@example.test'], use8BitMime: true, size: 1234 };
const featureLists = [
    [],
    ['PIPELINING'],
    ['8BITMIME'],
    ['SMTPUTF8'],
    ['SIZE'],
    ['SIZE 1000000'],
    ['SIZE 0'],
    ['SIZE 10'],
    ['SIZE 1234'],
    ['SIZE 1233'],
    ['SIZE\t500000'],
    ['SIZE  77777 extra'],
    ['SIZE=1000'],
    ['size 2000000'],
    ['XSIZE 5'],
    ['SIZE 99999999999999999999999999'],
    ['SIZE 12', 'SIZE 999999'],
    ['DSN'],
    ['AUTH PLAIN'],
    ['AUTH LOGIN'],
    ['AUTH CRAM-MD5'],
    ['AUTH PLAIN LOGIN CRAM-MD5'],
    ['AUTH CRAM-MD5 LOGIN PLAIN'],
    ['AUTH LOGIN CRAM-MD5'],
    ['AUTH CRAM-MD5 LOGIN'],
    ['AUTH XOAUTH2'],
    ['AUTH XOAUTH2 PLAIN'],
    ['AUTH XOAUTH2 CRAM-MD5 OAUTHBEARER'],
    ['AUTH GSSAPI DIGEST-MD5'],
    ['AUTH'],
    ['AUTH '],
    ['AUTH=PLAIN LOGIN'],
    ['AUTH=LOGIN', 'AUTH PLAIN'],
    ['AUTH LOGIN', 'AUTH=CRAM-MD5'],
    ['auth plain login'],
    ['Auth Cram-Md5'],
    ['AUTH  PLAIN   LOGIN  '],
    ['AUTH\tLOGIN'],
    ['AUTH LOGIN=PLAIN'],
    ['AUTHX PLAIN'],
    ['AUTH-PLAIN'],
    ['X-AUTH LOGIN'],
    ['XAUTH LOGIN'],
    ['AUTH PLAINX LOGINS'],
    ['AUTH PLA\xc4\xb1N'],
    ['AUTH LOG\xc4\xb1N \xc5\xbfPLAIN'],
    ['AUTH\xa0LOGIN'],
    ['AUTH\xc2\xa0LOGIN'],
    ['STARTTLS'],
    ['starttls'],
    ['StartTls', 'AUTH PLAIN'],
    ['STARTTLSX'],
    ['XSTARTTLS'],
    ['X-STARTTLS'],
    ['STARTTLS-NOT'],
    ['\xc5\xbfTARTTLS'],
    ['pipelining'],
    ['PIPELININGX'],
    ['X PIPELINING'],
    ['8bitmime', 'smtputf8'],
    ['SMTPUTF8X', '8BITMIMEX'],
    ['PIPELINING', 'SIZE 35882577', 'VRFY', 'ETRN', 'AUTH PLAIN LOGIN', 'ENHANCEDSTATUSCODES', '8BITMIME', 'DSN', 'SMTPUTF8', 'CHUNKING'],
    ['SIZE 35882577', '8BITMIME', 'AUTH LOGIN PLAIN XOAUTH2 PLAIN-CLIENTTOKEN OAUTHBEARER XOAUTH', 'ENHANCEDSTATUSCODES', 'PIPELINING', 'CHUNKING', 'SMTPUTF8'],
];
featureLists.forEach((features, i) => {
    add(`ehlo/features/${i}-${slug(features.join('+')).slice(0, 40)}`, {
        options: Object.assign({}, CREDS),
        envelope: RICH_ENVELOPE,
        server: server({ features, features2: features.filter(f => !/STARTTLS/i.test(f)) }),
    });
});
const ehloRaw = [
    ['single-line-auth', '250 sink.example.test AUTH LOGIN\r\n'],
    ['single-line-starttls', '250 STARTTLS\r\n'],
    ['single-line-pipelining', '250 sink PIPELINING SIZE 5\r\n'],
    ['first-line-size', '250-sink SIZE 20\r\n250 PIPELINING\r\n'],
    ['empty-last-line', '250-sink.example.test\r\n250-PIPELINING\r\n250 \r\n'],
    ['code-only-last-line', '250-sink.example.test\r\n250-PIPELINING\r\n250\r\n'],
    ['bare-lf', '250-sink.example.test\n250 PIPELINING\n'],
    ['empty-line-inside', '250-sink.example.test\r\n\r\n250 PIPELINING\r\n'],
    ['cr-inside-auth', '250-sink.example.test\r\n250-AUTH LOGIN\rPLAIN\r\n250 OK\r\n'],
    ['indented-line', '250-sink.example.test\r\n 250-PIPELINING\r\n250 OK\r\n'],
    ['code-only', '250\r\n'],
    ['dash-only', '250-\r\n250 \r\n'],
    ['251', '251 fine\r\n'],
    ['200', '200 fine\r\n'],
    ['one-digit', '2 fine\r\n'],
    ['299-multi', '299-sink\r\n299 PIPELINING\r\n'],
    ['mixed-codes', '250-sink\r\n550-PIPELINING\r\n250 AUTH LOGIN\r\n'],
    ['text-after-code-dash', '250-sink\r\n250-AUTH PLAIN LOGIN\r\n250 8BITMIME\r\n'],
    ['utf8', latin1(Buffer.from('250-gr\u00fc\u00df dich\r\n250 AUTH LOGIN\r\n', 'utf8'))],
    ['latin1', '250-gr\xfc\xdf dich\r\n250 AUTH LOGIN\r\n'],
    ['split', null],
];
for (const [name, raw] of ehloRaw) {
    let script = server({ ehloRaw: raw || '', ehloRaw2: '250-sink\r\n250 AUTH PLAIN\r\n' });
    if (name === 'split') {
        script = at(server({ features: ['PIPELINING'] }), 'EHLO', 0, [{ w: '250-sink.exam' }, { sleep: 30 }, { w: 'ple.test\r\n250-PIPE' }, { sleep: 30 }, { w: 'LINING\r\n25' }, { sleep: 30 }, { w: '0 AUTH LOGIN\r\n' }]);
    }
    add(`ehlo/raw/${name}`, { options: Object.assign({}, CREDS), envelope: RICH_ENVELOPE, server: script });
}
const ehloRefused = [
    ['500', '500 5.5.1 Command unrecognized\r\n'],
    ['502', '502 5.5.1 Not implemented\r\n'],
    ['550', '550 5.7.1 No\r\n'],
    ['450', '450 4.7.1 Later\r\n'],
    ['354', '354 odd\r\n'],
    ['text', 'what?\r\n'],
    ['421', '421 4.3.2 Shutting down\r\n'],
    ['421-multi', '421-4.3.2 Shutting down\r\n421 4.3.2 now\r\n'],
    ['4210', '4210 not really 421\r\n'],
];
const heloReplies = [
    ['ok', '250 sink.example.test\r\n'],
    ['ok-multi', '250-sink.example.test\r\n250 AUTH LOGIN\r\n'],
    ['500', '500 5.5.1 Command unrecognized\r\n'],
    ['421', '421 4.3.2 Shutting down\r\n'],
    ['text', 'no\r\n'],
];
for (const [ename, ereply] of ehloRefused) {
    for (const [hname, hreply] of heloReplies) {
        for (const call of ['send', 'verify']) {
            for (const creds of [false, true]) {
                if (creds && call === 'send' && hname !== 'ok') {
                    continue;
                }
                add(`ehlo/refused-${ename}/helo-${hname}/${call}${creds ? '-creds' : ''}`, {
                    call,
                    options: creds ? Object.assign({}, CREDS) : {},
                    server: server({ rules: { EHLO: [[{ w: ereply }]], HELO: [[{ w: hreply }]] } }),
                });
            }
        }
    }
}

// ------------------------------------------------------------------ STARTTLS

const starttls = [
    ['normal', server({ features: ['STARTTLS'], features2: ['AUTH PLAIN LOGIN'] })],
    ['auth-only-before-tls', server({ features: ['STARTTLS', 'AUTH PLAIN'], features2: [] })],
    ['offered-again', server({ features: ['STARTTLS', 'PIPELINING'], features2: ['STARTTLS', 'AUTH LOGIN'] })],
    ['pipelining-only-before-tls', server({ features: ['STARTTLS', 'PIPELINING', 'SMTPUTF8', '8BITMIME', 'SIZE 100'], features2: ['AUTH PLAIN'] })],
    ['extensions-after-tls', server({ features: ['STARTTLS'], features2: ['PIPELINING', 'SMTPUTF8', '8BITMIME', 'SIZE 100000', 'AUTH CRAM-MD5'] })],
    ['refused-454', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '454 4.7.0 TLS not available\r\n' }])],
    ['refused-500', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '500 5.5.1 no\r\n' }])],
    ['refused-502-multi', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '502-5.5.1 no\r\n502 5.5.1 never\r\n' }])],
    ['refused-421', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '421 4.3.2 bye\r\n' }])],
    ['refused-354', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '354 odd\r\n' }])],
    ['refused-text', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: 'nope\r\n' }])],
    ['refused-close', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ close: 1 }])],
    ['refused-tail', at(server({ features: ['STARTTLS', 'AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '454 4.7.0 no tls' }, { close: 1 }])],
    ['accepted-250', at(server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '250 go ahead\r\n' }, { tls: 1 }])],
    ['accepted-multi', at(server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }), 'STARTTLS', 0, [{ w: '220-go\r\n220 ahead\r\n' }, { tls: 1 }])],
    ['injected-reply', at(server({ features: ['STARTTLS'], features2: ['AUTH LOGIN'] }), 'STARTTLS', 0, [{ w: '220 go\r\n250-INJECTED\r\n250 AUTH PLAIN\r\n' }, { tls: 1 }])],
    ['injected-fragment', at(server({ features: ['STARTTLS'], features2: ['AUTH LOGIN'] }), 'STARTTLS', 0, [{ w: '220 go\r\n250 AUTH PLAIN' }, { tls: 1 }])],
    ['second-ehlo-500', at(server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }), 'EHLO', 1, [{ w: '500 5.5.1 no\r\n' }])],
    ['second-ehlo-421', at(server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }), 'EHLO', 1, [{ w: '421 4.3.2 bye\r\n' }])],
    ['second-ehlo-close', at(server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }), 'EHLO', 1, [{ close: 1 }])],
    ['none-offered', server({ features: ['AUTH PLAIN LOGIN'] })],
    ['none-offered-pipelining', server({ features: ['AUTH LOGIN', 'PIPELINING'] })],
];
for (const [name, script] of starttls) {
    for (const call of ['send', 'verify']) {
        add(`starttls/${name}/${call}`, { call, options: Object.assign({}, CREDS), server: script });
    }
}
// A reply that goes on after the 220 keeps nodemailer waiting for its end,
// while the server waits for the handshake
add('starttls/injected-partial-reply/verify', {
    call: 'verify',
    options: Object.assign({}, SHORT, CREDS),
    server: at(server({ features: ['STARTTLS'], features2: ['AUTH LOGIN'] }), 'STARTTLS', 0, [{ w: '220 go\r\n250-sink\r\n250-AUTH PLAIN\r\n250 X' }, { tls: 1 }]),
});
add('starttls/no-credentials/send', { server: server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }) });
add('starttls/implicit-tls-ignores-offer/send', { options: Object.assign({ secure: true }, CREDS), server: server({ tls: true, features: ['STARTTLS', 'AUTH PLAIN'] }) });
add('starttls/handshake-stall/phase-timeout', {
    call: 'verify',
    options: { greetingTimeout: 300, socketTimeout: 3000 },
    cmp: { clientUntil: 'STARTTLS' },
    server: at(server({ features: ['STARTTLS'] }), 'STARTTLS', 0, [{ w: '220 go\r\n' }, { stall: 1 }]),
});
add('starttls/handshake-stall/socket-timeout', {
    call: 'send',
    options: { greetingTimeout: 3000, socketTimeout: 300 },
    cmp: { clientUntil: 'STARTTLS' },
    server: at(server({ features: ['STARTTLS'] }), 'STARTTLS', 0, [{ w: '220 go\r\n' }, { stall: 1 }]),
});
add('starttls/handshake-close', {
    call: 'verify',
    cmp: { clientUntil: 'STARTTLS' },
    server: at(server({ features: ['STARTTLS'] }), 'STARTTLS', 0, [{ w: '220 go\r\n' }, { close: 1 }]),
});
add('starttls/handshake-garbage', {
    call: 'verify',
    cmp: { clientUntil: 'STARTTLS', message: false },
    server: at(server({ features: ['STARTTLS'] }), 'STARTTLS', 0, [{ w: '220 go\r\n' }, { sleep: 50 }, { w: '250 this is not TLS\r\n' }, { stall: 1 }]),
});

// --------------------------------------------------------- TLS certificates

const certs = [
    'localhost', 'other', 'untrusted', 'ca-leaf', 'ca-expired', 'ca-future', 'rogue-leaf', 'rogue-leaf-root',
    'rogue-inter-leaf', 'rogue-inter-leaf-root', 'client-only', 'issued-by-leaf', 'cn-only', 'cn-other', 'ip-only', 'ip-other', 'no-names',
    'mixed-san', 'wildcard', 'multi-cn', 'expired-untrusted', 'expired-trusted', 'odd-san',
];
for (const cert of certs) {
    for (const mode of ['starttls', 'implicit']) {
        for (const host of ['localhost', '127.0.0.1']) {
            for (const tlsVerify of [true, false]) {
                if (!tlsVerify && !['localhost', 'other', 'untrusted', 'expired-untrusted', 'no-names'].includes(cert)) {
                    continue;
                }
                const script = mode === 'implicit'
                    ? server({ tls: true, cert, features: ['AUTH PLAIN'] })
                    : server({ cert, features: ['STARTTLS'], features2: ['AUTH PLAIN'] });
                add(`tls/${cert}/${mode}/${host}/verify-${tlsVerify ? 'on' : 'off'}`, {
                    call: tlsVerify ? 'verify' : 'send',
                    options: Object.assign({ host, secure: mode === 'implicit', tlsVerify }, CREDS),
                    server: script,
                });
            }
        }
    }
}
for (const [hostName, host] of [['upper', 'LOCALHOST'], ['trailing-dot', 'localhost.'], ['ipv6', '::1']]) {
    for (const mode of ['starttls', 'implicit']) {
        add(`tls/host-${hostName}/${mode}`, {
            call: 'verify',
            options: { host, secure: mode === 'implicit', tlsVerify: true },
            cmp: { sni: host !== 'localhost.' },
            server: mode === 'implicit' ? server({ tls: true }) : server({ features: ['STARTTLS'] }),
        });
    }
}
add('tls/secure-on-plain-server', {
    call: 'verify',
    options: { secure: true },
    cmp: { client: false, message: false },
    server: server(),
});
add('tls/plain-on-tls-server', {
    call: 'verify',
    options: { secure: false, greetingTimeout: 300, socketTimeout: 3000 },
    cmp: { tlsEvents: false },
    server: server({ tls: true }),
});

// -------------------------------------------------------------------- AUTH

const ok235 = [{ w: '235 2.7.0 Authentication successful\r\n' }];
const authCases = [
    ['plain/ok', ['AUTH PLAIN'], {}],
    ['plain/535', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '535 5.7.8 Authentication credentials invalid\r\n' }]] }],
    ['plain/535-multi', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '535-5.7.8 Username and Password not accepted.\r\n535 5.7.8 Learn more\r\n' }]] }],
    ['plain/435', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '435 4.7.8 Try later\r\n' }]] }],
    ['plain/250', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '250 fine\r\n' }]] }],
    ['plain/235-no-text', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '235\r\n' }]] }],
    ['plain/text', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: 'welcome\r\n' }]] }],
    ['plain/334-then-ok', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '334 \r\n' }, { cont: 'PLAIN-MORE' }]], 'PLAIN-MORE': [ok235] }],
    ['plain/334-three-times', ['AUTH PLAIN'], {
        'AUTH PLAIN': [[{ w: '334 \r\n' }, { cont: 'PLAIN-MORE' }]],
        'PLAIN-MORE': [[{ w: '334 again\r\n' }, { cont: 'PLAIN-MORE' }], [{ w: '334-and\r\n334 again\r\n' }, { cont: 'PLAIN-MORE' }], ok235],
    }],
    ['plain/334-then-535', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '334 \r\n' }, { cont: 'PLAIN-MORE' }]], 'PLAIN-MORE': [[{ w: '535 5.7.8 no\r\n' }]] }],
    ['plain/3340', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '3340 odd\r\n' }, { cont: 'PLAIN-MORE' }]], 'PLAIN-MORE': [ok235] }],
    ['plain/421', ['AUTH PLAIN'], { 'AUTH PLAIN': [[{ w: '421 4.3.2 bye\r\n' }]] }],
    ['login/ok', ['AUTH LOGIN'], {}],
    ['login/first-535', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '535 5.7.8 no\r\n' }]] }],
    ['login/first-250', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '250 fine\r\n' }]] }],
    ['login/first-334-no-space', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '334\r\n' }, { cont: 'LOGIN-USER' }]] }],
    ['login/first-334-multi', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '334-VXNlcm5hbWU6\r\n334 VXNlcm5hbWU6\r\n' }, { cont: 'LOGIN-USER' }]] }],
    ['login/first-334-other-text', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '334 Give me your name\r\n' }, { cont: 'LOGIN-USER' }]] }],
    ['login/first-3345', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '3345 x\r\n' }, { cont: 'LOGIN-USER' }]] }],
    ['login/first-421', ['AUTH LOGIN'], { 'AUTH LOGIN': [[{ w: '421 4.3.2 bye\r\n' }]] }],
    ['login/user-535', ['AUTH LOGIN'], { 'LOGIN-USER': [[{ w: '535 5.7.8 unknown user\r\n' }]] }],
    ['login/user-235', ['AUTH LOGIN'], { 'LOGIN-USER': [ok235] }],
    ['login/user-334-no-space', ['AUTH LOGIN'], { 'LOGIN-USER': [[{ w: '334\r\n' }, { cont: 'LOGIN-PASS' }]] }],
    ['login/user-421', ['AUTH LOGIN'], { 'LOGIN-USER': [[{ w: '421 4.3.2 bye\r\n' }]] }],
    ['login/pass-535', ['AUTH LOGIN'], { 'LOGIN-PASS': [[{ w: '535 5.7.8 wrong password\r\n' }]] }],
    ['login/pass-250', ['AUTH LOGIN'], { 'LOGIN-PASS': [[{ w: '250 fine\r\n' }]] }],
    ['login/pass-334-then-ok', ['AUTH LOGIN'], { 'LOGIN-PASS': [[{ w: '334 more\r\n' }, { cont: 'LOGIN-MORE' }]], 'LOGIN-MORE': [ok235] }],
    ['login/pass-334-then-535', ['AUTH LOGIN'], { 'LOGIN-PASS': [[{ w: '334 more\r\n' }, { cont: 'LOGIN-MORE' }]], 'LOGIN-MORE': [[{ w: '535 no\r\n' }]] }],
    ['login/pass-421', ['AUTH LOGIN'], { 'LOGIN-PASS': [[{ w: '421 4.3.2 bye\r\n' }]] }],
    ['cram/ok', ['AUTH CRAM-MD5'], {}],
    ['cram/first-535', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '535 5.7.8 no\r\n' }]] }],
    ['cram/first-334-only', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-space', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334 \r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-two-spaces', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334  \r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-tab', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334\t' + b64('<tab@sink>') + '\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-spaces', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334   ' + b64('<spaces@sink>') + '  \r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-not-base64', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334 !!! not base64 at all ???\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-base64url', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334 PDE-Mjw_Pz4-QHNpbms_\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-unpadded', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334 PGFiY0BzaW5rPg\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-8bit', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334 ' + Buffer.from('<\xe9\xff\x80@sink>', 'latin1').toString('base64') + '\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-multi', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334-' + b64('<a@sink>') + '\r\n334 ' + b64('<b@sink>') + '\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-334-cr-inside', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '334 PGFi\rY0BzaW5rPg==\r\n' }, { cont: 'CRAM' }]] }],
    ['cram/first-421', ['AUTH CRAM-MD5'], { 'AUTH CRAM-MD5': [[{ w: '421 4.3.2 bye\r\n' }]] }],
    ['cram/final-535', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '535 5.7.8 no\r\n' }]] }],
    ['cram/final-250', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '250 fine\r\n' }]] }],
    ['cram/final-235-no-text', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '235\r\n' }]] }],
    ['cram/final-235-tab', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '235\tfine\r\n' }]] }],
    ['cram/final-235-multi', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '235-fine\r\n235 really\r\n' }]] }],
    ['cram/final-2350', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '2350 x\r\n' }]] }],
    ['cram/final-421', ['AUTH CRAM-MD5'], { CRAM: [[{ w: '421 4.3.2 bye\r\n' }]] }],
];
for (const [name, features, rules] of authCases) {
    for (const call of ['send', 'verify']) {
        add(`auth/${name}/${call}`, { call, options: Object.assign({}, CREDS), server: server({ features, rules }) });
    }
}
const credentials = [
    ['simple', 'user', 'pass'],
    ['address', 'user@example.test', 'p@ss:w0rd/with+signs='],
    ['spaces', 'user name', ' pass with spaces '],
    ['unicode', 'j\u00f6rg@example.test', 'p\u00e4ssw\u00f6rd \u2713 \ud83d\udd11'],
    ['long', 'u'.repeat(200) + '@example.test', 'p'.repeat(600)],
    ['nul', 'user\u0000name', 'pass\u0000word'],
    ['controls', 'user\tname', 'pass\r\nword'],
];
for (const [name, user, pass] of credentials) {
    for (const mech of ['PLAIN', 'LOGIN', 'CRAM-MD5']) {
        add(`auth/credentials/${name}/${slug(mech)}`, { call: 'verify', options: { user, pass }, server: server({ features: ['AUTH ' + mech] }) });
    }
}
const noCredentials = [
    ['none', {}],
    ['user-only', { user: 'console@example.test', pass: '' }],
    ['pass-only', { user: '', pass: 'secret' }],
];
for (const [name, creds] of noCredentials) {
    for (const call of ['send', 'verify']) {
        add(`auth/no-credentials/${name}/${call}`, { call, options: Object.assign({}, creds), server: server({ features: ['AUTH PLAIN LOGIN'] }) });
    }
}
add('auth/not-offered/send', { options: Object.assign({}, CREDS), server: server({ features: ['PIPELINING'] }) });
add('auth/not-offered/verify', { call: 'verify', options: Object.assign({}, CREDS), server: server({ features: ['PIPELINING'] }) });

// ------------------------------------------------------------- recipients

const rcptReplies = {
    ok: '250 2.1.5 Ok\r\n',
    r550: '550 5.1.1 No such user\r\n',
    r450: '450 4.2.1 Mailbox busy\r\n',
};
const THREE = { from: 'sender@example.test', to: ['one@example.test', 'two@example.test', 'three@example.test'] };
for (const pipelining of [false, true]) {
    for (const a of Object.keys(rcptReplies)) {
        for (const b of Object.keys(rcptReplies)) {
            for (const c of Object.keys(rcptReplies)) {
                add(`rcpt/${pipelining ? 'pipelined' : 'plain'}/${a}-${b}-${c}`, {
                    envelope: THREE,
                    server: server({
                        features: pipelining ? ['PIPELINING'] : [],
                        rules: {
                            RCPT: [[{ w: rcptReplies[a] }], [{ w: rcptReplies[b] }], [{ w: rcptReplies[c] }]],
                            DATA: [a === 'ok' || b === 'ok' || c === 'ok' ? [{ w: '354 go ahead\r\n' }, { data: 1 }] : [{ w: '554 5.5.1 No valid recipients\r\n' }]],
                        },
                    }),
                });
            }
        }
    }
}
const rcptOdd = [
    ['multi-line-550', ['550-5.1.1 No such user\r\n550 5.1.1 here\r\n', '250 Ok\r\n']],
    ['one-digit-4', ['4 odd\r\n', '550 no\r\n']],
    ['text', ['nope\r\n', 'still nope\r\n']],
    ['251-forward', ['251 2.1.5 will forward\r\n', '252 cannot verify\r\n']],
    ['354', ['354 odd\r\n', '250 Ok\r\n']],
    ['421-first', ['421 4.3.2 bye\r\n', '250 Ok\r\n']],
    ['421-last', ['250 Ok\r\n', '421 4.3.2 bye\r\n']],
    ['552-then-452', ['552 5.5.3 too many\r\n', '452 4.5.3 too many\r\n']],
    ['utf8-550', [latin1(Buffer.from('550 unbekannt \u00fc\r\n', 'utf8')), latin1(Buffer.from('550 unbekannt \u00f6\r\n', 'utf8'))]],
];
for (const pipelining of [false, true]) {
    for (const [name, replies] of rcptOdd) {
        add(`rcpt/${pipelining ? 'pipelined' : 'plain'}/odd-${name}`, {
            server: server({ features: pipelining ? ['PIPELINING'] : [], rules: { RCPT: replies.map(w => [{ w }]) } }),
        });
    }
}
// every recipient refused although the server takes DATA (only seen with PIPELINING)
const dotReplies = [
    ['250', [{ w: '250 2.0.0 Ok\r\n' }]],
    ['554', [{ w: '554 5.5.1 no recipients\r\n' }]],
    ['421', [{ w: '421 4.3.2 bye\r\n' }]],
    ['close', [{ close: 1 }]],
];
for (const [name, ops] of dotReplies) {
    add(`rcpt/pipelined/all-refused-data-accepted/dot-${name}`, {
        server: server({ features: ['PIPELINING'], rules: { RCPT: [[{ w: rcptReplies.r550 }]], DATA: [[{ w: '354 go ahead\r\n' }]], DOT: [ops] } }),
    });
}
add('rcpt/pipelined/all-refused-data-accepted/dot-stall', {
    options: SHORT,
    server: server({ features: ['PIPELINING'], rules: { RCPT: [[{ w: rcptReplies.r450 }]], DATA: [[{ w: '354 go ahead\r\n' }]], DOT: [[{ stall: 1 }]] } }),
});
for (const dataReply of ['354 go ahead\r\n', '250 odd\r\n', '554 5.5.1 no\r\n', '451 4.3.0 later\r\n', '421 4.3.2 bye\r\n']) {
    add(`rcpt/pipelined/mail-refused/data-${slug(dataReply.slice(0, 3))}`, {
        server: server({ features: ['PIPELINING'], rules: { MAIL: [[{ w: '550 5.7.1 sender refused\r\n' }]], RCPT: [[{ w: '503 5.5.1 need MAIL\r\n' }]], DATA: [[{ w: dataReply }]] } }),
    });
    add(`rcpt/pipelined/all-refused/data-${slug(dataReply.slice(0, 3))}`, {
        server: server({ features: ['PIPELINING'], rules: { RCPT: [[{ w: rcptReplies.r550 }], [{ w: rcptReplies.r450 }]], DATA: [[{ w: dataReply }]], DOT: [[{ w: '250 Ok\r\n' }]] } }),
    });
}
const many = n => ({ from: 'sender@example.test', to: Array.from({ length: n }, (_, i) => `rcpt${i}@example.test`) });
for (const pipelining of [false, true]) {
    for (const n of [1, 5, 150]) {
        add(`rcpt/${pipelining ? 'pipelined' : 'plain'}/count-${n}`, {
            envelope: many(n),
            server: server({ features: pipelining ? ['PIPELINING'] : [], rules: { RCPT: [[{ w: '250 Ok\r\n' }], [{ w: '550 no\r\n' }], [{ w: '250 Ok\r\n' }]] } }),
        });
    }
}

// ---------------------------------------------------- MAIL, DATA, final reply

const mailReplies = ['250 2.1.0 Ok\r\n', '250-2.1.0 Ok\r\n250 really\r\n', '550 5.7.1 sender refused\r\n', '550-5.7.1 sender\r\n550 refused\r\n', '450 4.7.1 later\r\n', '553 5.1.8 bad sender\r\n', '421 4.3.2 bye\r\n', '354 odd\r\n', 'nope\r\n', '2 short\r\n', '200 ok\r\n'];
const dataReplies = ['354 go ahead\r\n', '354-go\r\n354 ahead\r\n', '250 odd but taken\r\n', '300 odd\r\n', '554 5.5.1 no\r\n', '451 4.3.0 later\r\n', '421 4.3.2 bye\r\n', 'nope\r\n', '5 short\r\n'];
const finalReplies = [
    '250 2.0.0 Ok: queued as 4F1A2B\r\n', '250-2.0.0 Ok\r\n250 queued\r\n', '250\r\n', '200 fine\r\n', '2 short\r\n', '354 odd\r\n',
    '550 5.7.1 content refused\r\n', '552-5.3.4 too\r\n552 big\r\n', '451 4.3.0 later\r\n', '421 4.3.2 bye\r\n', 'nope\r\n',
    '250 2.0.0 OK <DB7PR04MB4f1a2b@DB7PR04MB1234.eurprd04.prod.outlook.com> [Hostname=DB7PR04MB1234.eurprd04.prod.outlook.com]\r\n',
    '250 2.0.0 ok <abc@mail.PROD.OUTLOOK.COM>\r\n',
    '250 2.0.0 OK <abc@example.com>\r\n',
    '250 Ok 0100018b5c3c0a4f-12345678-9abc-def0-1234-56789abcdef0-000000\r\n',
    latin1(Buffer.from('250 2.0.0 angenommen \u00fc\u00df \u2713\r\n', 'utf8')),
    '250 2.0.0 latin \xfc\xdf\r\n',
    latin1(Buffer.from('250 replacement \ufffd char\r\n', 'utf8')),
];
for (const pipelining of [false, true]) {
    const features = pipelining ? ['PIPELINING'] : [];
    const p = pipelining ? 'pipelined' : 'plain';
    mailReplies.forEach((w, i) => add(`mail/${p}/${i}-${slug(w.slice(0, 12))}`, { server: server({ features, rules: { MAIL: [[{ w }]] } }) }));
    dataReplies.forEach((w, i) => add(`data/${p}/${i}-${slug(w.slice(0, 12))}`, {
        server: server({ features, rules: { DATA: [/^[23]/.test(w) ? [{ w }, { data: 1 }] : [{ w }]] } }),
    }));
    finalReplies.forEach((w, i) => add(`final/${p}/${i}-${slug(w.slice(0, 12))}`, { server: server({ features, rules: { ENDDATA: [[{ w }]] } }) }));
}
add('data/early-reply/552', { server: server({ rules: { DATA: [[{ w: '354 go ahead\r\n552 5.3.4 too big\r\n' }, { data: 1 }]], ENDDATA: [[]] } }) });
add('data/early-reply/250', { server: server({ rules: { DATA: [[{ w: '354 go ahead\r\n250 2.0.0 already fine\r\n' }, { data: 1 }]], ENDDATA: [[]] } }) });
add('data/split-354', { server: server({ rules: { DATA: [[{ w: '35' }, { sleep: 30 }, { w: '4 go' }, { sleep: 30 }, { w: ' ahead\r\n' }, { data: 1 }]] } }) });
add('final/split', { server: server({ rules: { ENDDATA: [[{ w: '250-2.0.0 Ok\r' }, { sleep: 30 }, { w: '\n250 queu' }, { sleep: 30 }, { w: 'ed\r\n' }]] } }) });

// internationalized addresses and MAIL parameters
const utf8Cases = [
    ['from-utf8', { from: 'j\u00f6rg@example.test', to: ['rcpt@example.test'] }],
    ['to-utf8', { from: 'sender@example.test', to: ['rcpt@example.test', 'm\u00fcller@example.test'] }],
    ['both-utf8', { from: 'se\u00f1or@example.test', to: ['\u65e5\u672c@example.test'] }],
    ['astral', { from: 'sender@example.test', to: ['\ud83d\ude00@example.test'] }],
    ['ascii', { from: 'sender@example.test', to: ['rcpt@example.test'] }],
];
for (const [name, envelope] of utf8Cases) {
    for (const features of [[], ['SMTPUTF8'], ['SMTPUTF8', 'PIPELINING', '8BITMIME', 'SIZE 5000']]) {
        for (const refuse of ['none', 'mail-550', 'mail-553', 'rcpt-553', 'rcpt-550']) {
            const rules = {};
            if (refuse.startsWith('mail')) {
                rules.MAIL = [[{ w: refuse.slice(5) + ' 5.6.7 no utf8 here\r\n' }]];
                if (features.includes('PIPELINING')) {
                    rules.DATA = [[{ w: '554 no\r\n' }]];
                }
            }
            if (refuse.startsWith('rcpt')) {
                rules.RCPT = [[{ w: '250 Ok\r\n' }], [{ w: refuse.slice(5) + ' 5.6.7 no utf8 here\r\n' }]];
                if (envelope.to.length === 1) {
                    rules.RCPT.shift();
                    rules.DATA = [[{ w: '554 no\r\n' }]];
                }
            }
            add(`utf8/${name}/${features.length ? slug(features.join('+')) : 'no-extensions'}/${refuse}`, {
                envelope: Object.assign({ use8BitMime: name !== 'ascii', size: 2000 }, envelope),
                forced: true,
                server: server({ features, rules }),
            });
        }
    }
}
const paramCases = [
    ['8bit-offered', { use8BitMime: true }, ['8BITMIME']],
    ['8bit-not-offered', { use8BitMime: true }, []],
    ['8bit-not-wanted', {}, ['8BITMIME']],
    ['size-offered', { size: 4321 }, ['SIZE 10000']],
    ['size-offered-no-limit', { size: 4321 }, ['SIZE']],
    ['size-not-offered', { size: 4321 }, []],
    ['size-zero', { size: 0 }, ['SIZE 10000']],
    ['size-equal-limit', { size: 10000 }, ['SIZE 10000']],
    ['size-over-limit', { size: 10001 }, ['SIZE 10000']],
    ['size-over-limit-pipelined', { size: 10001 }, ['SIZE 10000', 'PIPELINING']],
    ['all', { use8BitMime: true, size: 77 }, ['SIZE 10000', '8BITMIME', 'SMTPUTF8', 'PIPELINING']],
];
for (const [name, extra, features] of paramCases) {
    add(`params/${name}`, { envelope: Object.assign({}, ENVELOPE, extra), forced: true, server: server({ features }) });
}

// ------------------------------------------------------------------ envelopes

// handed to sendMail as they are, mime-node turns them into the envelope
const mailerEnvelopes = [
    ['one', { from: 'sender@example.test', to: 'rcpt@example.test' }],
    ['list-string', { from: 'sender@example.test', to: 'a@example.test, b@example.test,c@example.test' }],
    ['names', { from: 'Sender Name <sender@example.test>', to: ['"Last, First" <a@example.test>', 'B <b@example.test>'] }],
    ['cc-bcc', { from: 'sender@example.test', to: 'a@example.test', cc: ['b@example.test'], bcc: 'c@example.test' }],
    ['duplicates', { from: 'sender@example.test', to: ['a@example.test', 'A@example.test', 'a@example.test'] }],
    ['no-sender', { to: ['a@example.test'] }],
    ['empty-sender', { from: '', to: ['a@example.test'] }],
    ['no-recipients', { from: 'sender@example.test', to: [] }],
    ['nothing', {}],
    ['idn-domain', { from: 'sender@b\u00fccher.example', to: ['rcpt@\u65e5\u672c.example'] }],
    ['utf8-local', { from: 'j\u00f6rg@example.test', to: ['m\u00fcller@example.test'] }],
    ['spaces', { from: 'sen der@example.test', to: ['rc pt@example.test'] }],
    ['angle-brackets', { from: '<sender@example.test>', to: ['<a@example.test>', 'x<y>z@example.test'] }],
    ['line-breaks', { from: 'sender@example.test\r\nRCPT TO:<evil@example.test>', to: ['a@example.test\nDATA', 'b@example.test\r\n'] }],
    ['group', { from: 'sender@example.test', to: 'Team: a@example.test, b@example.test;' }],
];
for (const [name, envelope] of mailerEnvelopes) {
    for (const features of [[], ['PIPELINING', 'SMTPUTF8']]) {
        add(`envelope/mailer/${name}/${features.length ? 'pipelined' : 'plain'}`, { envelope, server: server({ features }) });
    }
}
// put in place of the envelope mime-node made, so that smtp-connection sees them unchanged
const forcedEnvelopes = [
    ['plain', { from: 'sender@example.test', to: ['a@example.test'] }],
    ['empty-from', { from: '', to: ['a@example.test'] }],
    ['false-from', { from: false, to: ['a@example.test'] }],
    ['padded', { from: '  sender@example.test \t\r\n', to: [' a@example.test ', '\tb@example.test\n'] }],
    ['unicode-padding', { from: '\u00a0\ufeffsender@example.test\u2028\u3000', to: ['\u2003a@example.test\u2029'] }],
    ['from-gt', { from: 'a@b>c', to: ['a@example.test'] }],
    ['from-lt', { from: '<sender@example.test', to: ['a@example.test'] }],
    ['from-cr', { from: 'sender@example.test\rRCPT TO:<evil@example.test>', to: ['a@example.test'] }],
    ['from-lf', { from: 'sender\n@example.test', to: ['a@example.test'] }],
    ['from-quotes', { from: '"quoted\\name"\u0001\u007f<@example.test', to: ['a@example.test'] }],
    ['from-unicode-invalid', { from: 'j\u00f6rg\u2028>\ud83d\ude00@example.test', to: ['a@example.test'] }],
    ['from-spaces', { from: 'sen der@exam ple.test', to: ['a b@example.test'] }],
    ['from-odd-valid', { from: '"quoted\\name"\u0001\u007f@example.test', to: ['we"ird\\@example.test', 'tab\there@example.test'] }],
    ['to-empty', { from: 'sender@example.test', to: ['a@example.test', ''] }],
    ['to-blank', { from: 'sender@example.test', to: ['   ', 'a@example.test'] }],
    ['to-lt', { from: 'sender@example.test', to: ['a@example.test', 'x<y@example.test'] }],
    ['to-gt', { from: 'sender@example.test', to: ['a>@example.test'] }],
    ['to-lf', { from: 'sender@example.test', to: ['a@example.test\nRCPT TO:<evil@example.test>'] }],
    ['to-cr', { from: 'sender@example.test', to: ['a@example.test', 'b\r@example.test'] }],
    ['to-controls', { from: 'sender@example.test', to: ['a\u0000\u0008\u001f<@example.test'] }],
    ['no-recipients', { from: 'sender@example.test', to: [] }],
    ['both-invalid', { from: 'bad<sender', to: ['bad>rcpt'] }],
    ['no-recipients-invalid-from', { from: 'bad<sender', to: [] }],
    ['long-address', { from: 's'.repeat(2000) + '@example.test', to: ['r'.repeat(3000) + '@example.test'] }],
];
for (const [name, envelope] of forcedEnvelopes) {
    for (const features of [[], ['PIPELINING', 'SMTPUTF8', 'AUTH PLAIN']]) {
        add(`envelope/forced/${name}/${features.length ? 'pipelined' : 'plain'}`, {
            options: Object.assign({}, CREDS),
            envelope,
            forced: true,
            server: server({ features }),
        });
    }
}

// ------------------------------------------------------------------- messages

function generated(seed, size, alphabet) {
    return { gen: { seed, size, alphabet } };
}
const messages = [
    ['crlf', { s: 'Subject: x\r\n\r\nline one\r\nline two\r\n' }],
    ['bare-lf', { s: 'Subject: x\n\nline one\nline two\n' }],
    ['bare-cr', { s: 'Subject: x\r\rline one\rline two\r' }],
    ['mixed', { s: 'a\r\nb\nc\rd\n\re\r\r\nf\n\n\r\n\r' }],
    ['no-final-newline', { s: 'Subject: x\r\n\r\nno newline at the end' }],
    ['ends-with-cr', { s: 'body\r' }],
    ['ends-with-lf', { s: 'body\n' }],
    ['empty', { s: '' }],
    ['one-byte', { s: 'x' }],
    ['only-crlf', { s: '\r\n' }],
    ['only-lf', { s: '\n' }],
    ['only-cr', { s: '\r' }],
    ['only-dot', { s: '.' }],
    ['dot-line', { s: '.\r\n' }],
    ['dot-lines', { s: 'a\r\n.\r\nb\r\n..\r\n...\r\n.c\r\n' }],
    ['dot-after-lf', { s: 'a\n.\nb\n.c\n' }],
    ['dot-after-cr', { s: 'a\r.\rb\r.c\r' }],
    ['dot-end-crlf', { s: 'a\r\n.' }],
    ['dot-end-lf', { s: 'a\n.' }],
    ['dot-end-cr', { s: 'a\r.' }],
    ['smuggling-crlf', { s: 'a\r\n.\r\nMAIL FROM:<evil@example.test>\r\nRCPT TO:<victim@example.test>\r\nDATA\r\nx\r\n.\r\n' }],
    ['smuggling-lf', { s: 'a\n.\nMAIL FROM:<evil@example.test>\n' }],
    ['smuggling-cr', { s: 'a\r.\rMAIL FROM:<evil@example.test>\r' }],
    ['smuggling-mixed', { s: 'a\n.\r\nb\r.\nc\r\n.\rd\n\r.\n\re' }],
    ['dots-inside', { s: 'a.b\r\nc..d\r\n e.\r\n' }],
    ['8bit', { s: latin1(Buffer.from('Subject: \u00fc\r\n\r\ngr\u00fc\u00dfe \u2713 \ud83d\ude00\r\n', 'utf8')) }],
    ['binary', { s: latin1(Buffer.from(Array.from({ length: 256 }, (_, i) => i))) + '\r\n' }],
    ['nul', { s: 'a\u0000b\r\n\u0000\r\n.\u0000\r\n' }],
    ['long-line', { s: 'Subject: long\r\n\r\n' + 'x'.repeat(5000) + '\r\n' }],
    ['long-line-dots', { s: '.'.repeat(3000) + '\r\n' + '.'.repeat(3000) }],
    ['generated-64k', generated(1, 65536, 'abcdefghijklmnopqrstuvwxyz .\r\n')],
    ['generated-64k-plus-1', generated(2, 65537, 'ab.\r\n')],
    ['generated-1m', generated(3, 1048576, 'abcdefghijklmnopqrstuvwxyz0123456789 .,\r\n')],
    ['generated-1m-line-ends', generated(4, 1048576, '.\r\n')],
    ['generated-1m-no-newline', generated(5, 1048576, 'abcdefghijklmnopqrstuvwxyz.')],
    ['generated-1m-binary', generated(6, 1048576, latin1(Buffer.from(Array.from({ length: 256 }, (_, i) => i))))],
    ['generated-3m', generated(7, 3 * 1048576, 'abcdefghijklmnopqrstuvwxyz \r\n.')],
];
for (const [name, message] of messages) {
    for (const features of [[], ['PIPELINING']]) {
        add(`message/${name}/${features.length ? 'pipelined' : 'plain'}`, { message, server: server({ features }) });
    }
}
add('message/crlf/implicit-tls', { options: { secure: true }, message: generated(8, 300000, 'abcdefghijklmnopqrstuvwxyz .\r\n'), server: server({ tls: true }) });
add('message/crlf/starttls', { message: generated(9, 300000, 'abcdefghijklmnopqrstuvwxyz .\r\n'), server: server({ features: ['STARTTLS'], features2: ['PIPELINING'] }) });

// -------------------------------------------------- failures at every step

const failures = [
    ['r4', [{ w: '451 4.3.0 Try again later\r\n' }]],
    ['r5', [{ w: '550 5.7.1 Rejected\r\n' }]],
    ['r5-multi', [{ w: '554-5.7.1 Rejected\r\n554 5.7.1 for policy reasons\r\n' }]],
    ['r421', [{ w: '421 4.3.2 Service shutting down\r\n' }]],
    ['r421-multi', [{ w: '421-4.3.2 Service\r\n421 4.3.2 shutting down\r\n' }]],
    ['r2', [{ w: '250 OK\r\n' }]],
    ['r3', [{ w: '354 Go on\r\n' }]],
    ['garbage', [{ w: 'hello there\r\n' }]],
    ['close', [{ close: 1 }]],
    ['reply-then-close', [{ w: '250 OK\r\n' }, { close: 1 }]],
    ['tail', [{ w: '554 5.3.2 go away' }, { close: 1 }]],
    ['stall', [{ stall: 1 }]],
    ['stall-partial', [{ w: '250-partial\r\n250 no line end' }, { stall: 1 }]],
];
const TWO = { from: 'sender@example.test', to: ['first@example.test', 'second@example.test'] };
const bases = [
    ['send-plain', { call: 'send', envelope: TWO, server: server() },
        [['GREETING', 0], ['EHLO', 0], ['MAIL', 0], ['RCPT', 0], ['RCPT', 1], ['DATA', 0], ['ENDDATA', 0]]],
    ['send-pipelined', { call: 'send', envelope: TWO, server: server({ features: ['PIPELINING'] }) },
        [['GREETING', 0], ['EHLO', 0], ['MAIL', 0], ['RCPT', 0], ['RCPT', 1], ['DATA', 0], ['ENDDATA', 0]]],
    ['send-starttls-plain-auth', { call: 'send', envelope: TWO, options: CREDS, server: server({ features: ['STARTTLS'], features2: ['AUTH PLAIN', 'PIPELINING'] }) },
        [['GREETING', 0], ['EHLO', 0], ['STARTTLS', 0], ['EHLO', 1], ['AUTH PLAIN', 0], ['MAIL', 0], ['RCPT', 1], ['DATA', 0], ['ENDDATA', 0]]],
    ['send-implicit-tls-login', { call: 'send', envelope: TWO, options: Object.assign({ secure: true }, CREDS), server: server({ tls: true, features: ['AUTH LOGIN'] }) },
        [['GREETING', 0], ['EHLO', 0], ['AUTH LOGIN', 0], ['LOGIN-USER', 0], ['LOGIN-PASS', 0], ['MAIL', 0], ['RCPT', 0], ['DATA', 0], ['ENDDATA', 0]]],
    ['send-cram', { call: 'send', envelope: TWO, options: CREDS, server: server({ features: ['AUTH CRAM-MD5'] }) },
        [['AUTH CRAM-MD5', 0], ['CRAM', 0]]],
    ['verify-plain', { call: 'verify', server: server() },
        [['GREETING', 0], ['EHLO', 0], ['QUIT', 0]]],
    ['verify-starttls-plain-auth', { call: 'verify', options: CREDS, server: server({ features: ['STARTTLS'], features2: ['AUTH PLAIN'] }) },
        [['GREETING', 0], ['EHLO', 0], ['STARTTLS', 0], ['EHLO', 1], ['AUTH PLAIN', 0], ['QUIT', 0]]],
    ['verify-implicit-tls-login', { call: 'verify', options: Object.assign({ secure: true }, CREDS), server: server({ tls: true, features: ['AUTH LOGIN'] }) },
        [['GREETING', 0], ['EHLO', 0], ['AUTH LOGIN', 0], ['LOGIN-USER', 0], ['LOGIN-PASS', 0], ['QUIT', 0]]],
    ['verify-cram', { call: 'verify', options: CREDS, server: server({ features: ['AUTH CRAM-MD5'] }) },
        [['AUTH CRAM-MD5', 0], ['CRAM', 0], ['QUIT', 0]]],
];
for (const [bname, base, steps] of bases) {
    for (const [verb, n] of steps) {
        for (const [fname, failOps] of failures) {
            const ops = JSON.parse(JSON.stringify(failOps));
            const cmp = {};
            if (verb === 'STARTTLS' && fname === 'r2') {
                // the client would start a handshake against a sink that reads it as commands
                continue;
            }
            if (verb === 'STARTTLS' && fname === 'reply-then-close') {
                // the client starts a TLS handshake nobody answers
                cmp.clientUntil = 'STARTTLS';
            }
            if (verb === 'DATA' && fname === 'reply-then-close') {
                // whether the message is written before the close is seen depends on timing
                continue;
            }
            if (verb === 'DATA' && ['r2', 'r3'].includes(fname)) {
                // the server takes the DATA command, so it has to read the message
                ops.push({ data: 1 });
            }
            const options = Object.assign({}, base.options || {});
            if (fname.startsWith('stall')) {
                Object.assign(options, verb === 'GREETING' ? { greetingTimeout: 300, socketTimeout: 3000 } : SHORT);
            }
            add(`step/${bname}/${slug(verb)}-${n}/${fname}`, Object.assign({}, base, { options, cmp, server: at(base.server, verb, n, ops) }));
        }
    }
}

// ------------------------------------------------------------- client names

const hostnames = [
    ['fqdn', { value: 'console.example.test' }],
    ['no-dot', { value: 'betterdesk' }],
    ['empty', { value: '' }],
    ['error', { error: true }],
    ['ipv4', { value: '10.1.2.3' }],
    ['ipv4-like-big', { value: '999.999.999.999' }],
    ['five-numbers', { value: '1.2.3.4.5' }],
    ['ipv4-trailing-lf', { value: '10.1.2.3\n' }],
    ['ipv6', { value: 'fe80::1' }],
    ['upper', { value: 'CONSOLE.Example.TEST' }],
    ['unicode', { value: 'h\u00f6st.example.test' }],
    ['space', { value: 'my host.example' }],
    ['line-breaks', { value: 'evil.example\r\nMAIL FROM:<x@example.test>' }],
    ['trailing-dot', { value: 'console.example.test.' }],
    ['dot-only', { value: '.' }],
    ['docker-id', { value: '8c46a72a9d27' }],
];
for (const [name, hostname] of hostnames) {
    add(`hostname/${name}/plain`, { call: 'verify', hostname, server: server({ rules: { EHLO: [[{ w: '500 no\r\n' }]] } }) });
    add(`hostname/${name}/starttls`, { call: 'verify', hostname, server: server({ features: ['STARTTLS'], features2: [] }) });
}

// ------------------------------------------------------------------ connecting

for (const call of ['send', 'verify']) {
    add(`connect/refused/${call}`, { call, server: { listen: false } });
    add(`connect/refused-localhost/${call}`, { call, options: { host: 'localhost' }, server: { listen: false } });
    add(`connect/refused-ipv6/${call}`, { call, options: { host: '::1' }, server: { listen: false } });
    add(`connect/refused-implicit-tls/${call}`, { call, options: { secure: true }, server: { listen: false } });
    add(`connect/unresolvable/${call}`, { call, options: { host: 'smtpwire-no-such-host.invalid' }, server: { listen: false } });
    add(`connect/unresolvable-implicit-tls/${call}`, { call, options: { host: 'smtpwire-no-such-host.invalid', secure: true }, server: { listen: false } });
    add(`connect/default-port/${call}`, { call, options: { port: 'default' }, server: { listen: false } });
    add(`connect/default-port-secure/${call}`, { call, options: { port: 'default', secure: true }, server: { listen: false } });
    add(`connect/port-too-big/${call}`, { call, options: { port: 70000 }, server: { listen: false } });
    add(`connect/port-negative/${call}`, { call, options: { port: -1 }, server: { listen: false } });
    add(`connect/localhost/${call}`, { call, options: { host: 'localhost' }, server: server() });
    add(`connect/default-host/${call}`, { call, options: { host: 'default' }, server: server() });
}

// Names with records of their own: the recorder answers nodemailer's A and
// AAAA lookups for these hosts from `dns`. Every scenario has its own name,
// because nodemailer remembers the addresses of a name for five minutes.
add('connect/dns/one-record/verify', { call: 'verify', options: { host: 'one.smtpwire.test' }, dns: { a: ['127.0.0.1'] }, server: server() });
add('connect/dns/one-record-refused/verify', { call: 'verify', options: { host: 'one-refused.smtpwire.test' }, dns: { a: ['127.0.0.5'] }, server: { listen: false } });
add('connect/dns/two-records-all-refused/verify', { call: 'verify', options: { host: 'two-refused.smtpwire.test' }, dns: { a: ['127.0.0.2', '127.0.0.3'] }, server: { listen: false } });
add('connect/dns/two-records-all-refused/send', { options: { host: 'two-refused-send.smtpwire.test' }, dns: { a: ['127.0.0.2', '127.0.0.3'] }, server: { listen: false } });
add('connect/dns/two-records-one-answers/send', { options: { host: 'two.smtpwire.test' }, dns: { a: ['127.0.0.9', '127.0.0.1'] }, server: server() });
add('connect/dns/three-records-one-answers/verify', { call: 'verify', options: { host: 'three.smtpwire.test' }, dns: { a: ['127.0.0.8', '127.0.0.1', '127.0.0.9'] }, server: server() });
add('connect/dns/two-records-implicit-tls/verify', {
    call: 'verify',
    options: { host: 'smtp.example.test', secure: true, tlsVerify: true },
    dns: { a: ['127.0.0.9', '127.0.0.1'] },
    server: server({ tls: true, cert: 'wildcard' }),
});
add('connect/dns/two-records-starttls/send', {
    options: Object.assign({ host: 'mail.example.test', tlsVerify: true }, CREDS),
    dns: { a: ['127.0.0.1', '127.0.0.9'] },
    server: server({ cert: 'wildcard', features: ['STARTTLS'], features2: ['AUTH PLAIN'] }),
});
add('connect/dns/wildcard-two-labels-deep/verify', {
    call: 'verify',
    options: { host: 'a.b.example.test', secure: true, tlsVerify: true },
    dns: { a: ['127.0.0.1'] },
    server: server({ tls: true, cert: 'wildcard' }),
});
add('connect/dns/upper-case-name/verify', {
    call: 'verify',
    options: { host: 'SMTP2.Example.TEST', secure: true, tlsVerify: true },
    dns: { a: ['127.0.0.1'] },
    server: server({ tls: true, cert: 'wildcard' }),
});

// ----------------------------------------------------------- reply size cap

const big = n => ({ wrep: { s: 'x', n } });
const capCases = [
    ['greeting-line-over', 'GREETING', [{ w: '220 ' }, big(1100000), { stall: 1 }]],
    ['greeting-line-far-over', 'GREETING', [big(3000000), { w: '\r\n' }]],
    ['greeting-line-under', 'GREETING', [{ w: '220 ' }, big(900000), { w: '\r\n' }]],
    ['greeting-multi-over', 'GREETING', [{ wrep: { s: '220-' + 'y'.repeat(94) + '\r\n', n: 11000 } }, { w: '220 done\r\n' }]],
    ['greeting-multi-under', 'GREETING', [{ wrep: { s: '220-' + 'y'.repeat(94) + '\r\n', n: 9000 } }, { w: '220 done\r\n' }]],
    ['ehlo-line-over', 'EHLO', [{ w: '250-sink\r\n250 ' }, big(1100000), { stall: 1 }]],
    ['ehlo-multi-over', 'EHLO', [{ wrep: { s: '250-PIPELINING\r\n', n: 70000 } }, { w: '250 done\r\n' }]],
    ['ehlo-multi-under', 'EHLO', [{ wrep: { s: '250-PIPELINING\r\n', n: 50000 } }, { w: '250 AUTH PLAIN\r\n' }]],
    ['mail-line-over', 'MAIL', [big(1100000), { stall: 1 }]],
    ['final-line-over', 'ENDDATA', [{ w: '250 ' }, big(1100000), { stall: 1 }]],
];
for (const [name, verb, ops] of capCases) {
    add(`cap/${name}`, { options: { greetingTimeout: 5000, socketTimeout: 5000 }, server: at(server(), verb, 0, ops) });
}

// ------------------------------------------------------------ QUIT (verify)

const quitCases = [
    ['221', [{ w: '221 2.0.0 Bye\r\n' }, { close: 1 }]],
    ['221-no-close', [{ w: '221 2.0.0 Bye\r\n' }]],
    ['250', [{ w: '250 OK\r\n' }]],
    ['500', [{ w: '500 no\r\n' }]],
    ['421', [{ w: '421 4.3.2 bye\r\n' }]],
    ['close', [{ close: 1 }]],
    ['tail', [{ w: '554 5.3.2 go away' }, { close: 1 }]],
    ['stall', [{ stall: 1 }]],
    ['partial-stall', [{ w: '221-bye\r\n' }, { stall: 1 }]],
];
for (const [name, ops] of quitCases) {
    add(`quit/${name}`, { call: 'verify', options: Object.assign({}, SHORT, CREDS), server: at(server({ features: ['AUTH PLAIN'] }), 'QUIT', 0, ops) });
}

module.exports = { scenarios };
