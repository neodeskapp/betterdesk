'use strict';

// Records whole sessions of the console's mail service for the integration
// test of the Go mail service: the real services/emailService.js is called
// with a stubbed database adapter that returns one smtp_config setting per
// session, against the sink. Writes ../e2e_sessions.json (format: README.md).

const fs = require('fs');
const os = require('os');
const path = require('path');
const Module = require('module');
const { startSink, closedPort } = require('./sink');
const { WEB, setHostname, sleep } = require('./common');

if (!process.env.NODE_EXTRA_CA_CERTS) {
    console.error('run through run.sh: NODE_EXTRA_CA_CERTS has to name ../certs/trusted.pem');
    process.exit(2);
}

// the console's configuration is loaded for real, with its files kept out of the repository
const ENV = {
    DATA_DIR: fs.mkdtempSync(path.join(os.tmpdir(), 'smtpwire-e2e-')),
    SESSION_SECRET: 'smtpwire-e2e-session-secret-not-used-anywhere',
    HOST: '0.0.0.0',
    PORT: '5000',
};
Object.assign(process.env, ENV);
for (const key of ['SMTP_HOST', 'SMTP_PORT', 'SMTP_SECURE', 'SMTP_USER', 'SMTP_PASS', 'SMTP_FROM', 'SMTP_ALERT_EMAIL', 'SMTP_TLS_VERIFY', 'PANEL_PUBLIC_URL', 'PUBLIC_URL', 'HTTP_REDIRECT_HTTPS']) {
    delete process.env[key];
}

// what getSetting('smtp_config') returns for the session being recorded
let storedConfig = null;
const adapterPath = require.resolve(WEB + '/services/dbAdapter');
const stub = new Module(adapterPath);
stub.filename = adapterPath;
stub.loaded = true;
stub.exports = {
    getAdapter: () => ({
        getSetting: async key => (key === 'smtp_config' ? storedConfig : null),
    }),
};
require.cache[adapterPath] = stub;

const emailService = require(WEB + '/services/emailService');

const b64 = s => Buffer.from(s, 'utf8').toString('base64');
const reply = (code, lines) => lines.map((line, i) => code + (i === lines.length - 1 ? ' ' : '-') + line + '\r\n').join('');

function server(o = {}) {
    const ehlo = features => reply('250', ['sink.example.test'].concat(features || []));
    const rules = {
        EHLO: [[{ w: ehlo(o.features) }]],
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
    if (o.features2) {
        rules.EHLO.push([{ w: ehlo(o.features2) }]);
    }
    Object.assign(rules, o.rules || {});
    return { tls: !!o.tls, cert: o.cert || 'localhost', fullData: true, greeting: [{ w: '220 sink.example.test ESMTP\r\n' }], rules };
}

const servers = {
    plain: () => server(),
    plainAuth: () => server({ features: ['AUTH PLAIN LOGIN', 'SIZE 10485760', '8BITMIME'] }),
    plainPipelining: () => server({ features: ['PIPELINING', 'SMTPUTF8', '8BITMIME', 'SIZE 10485760', 'AUTH CRAM-MD5'] }),
    starttls: () => server({ features: ['STARTTLS'], features2: ['AUTH LOGIN PLAIN', 'PIPELINING', '8BITMIME', 'SMTPUTF8'] }),
    starttlsUntrusted: () => server({ cert: 'untrusted', features: ['STARTTLS'], features2: ['AUTH PLAIN'] }),
    starttlsOtherName: () => server({ cert: 'other', features: ['STARTTLS'], features2: ['AUTH PLAIN'] }),
    implicit: () => server({ tls: true, features: ['AUTH PLAIN LOGIN', 'PIPELINING'] }),
    implicitCram: () => server({ tls: true, features: ['AUTH CRAM-MD5'] }),
    implicitUntrusted: () => server({ tls: true, cert: 'untrusted', features: ['AUTH PLAIN'] }),
    wrongPassword: () => server({ features: ['AUTH PLAIN'], rules: { 'AUTH PLAIN': [[{ w: '535 5.7.8 Authentication credentials invalid\r\n' }]] } }),
    oneRecipientRefused: () => server({ rules: { RCPT: [[{ w: '250 2.1.5 Ok\r\n' }], [{ w: '550 5.1.1 No such user\r\n' }], [{ w: '250 2.1.5 Ok\r\n' }]] } }),
    allRecipientsRefused: () => server({ rules: { RCPT: [[{ w: '550 5.1.1 No such user\r\n' }]] } }),
    senderRefused: () => server({ features: ['PIPELINING'], rules: { MAIL: [[{ w: '550 5.7.1 Sender refused\r\n' }]], DATA: [[{ w: '554 5.5.1 No valid recipients\r\n' }]] } }),
    messageRefused: () => server({ rules: { ENDDATA: [[{ w: '554 5.7.1 Message looks like spam\r\n' }]] } }),
    busy: () => Object.assign(server(), { greeting: [{ w: '421 4.3.2 Service not available\r\n' }] }),
};

const CREDS = { user: 'console@example.test', pass: 'correct horse battery' };
const alert = { severity: 'critical', device_id: '123456789', message: 'CPU load is 97 % <for 5 min> & rising', triggered_at: '2026-11-10 12:34:56' };
const rule = { name: 'High CPU "prod"', description: 'CPU above 90 %', condition_type: 'cpu', condition_op: '>', condition_value: '90', notify_emails: 'ops@example.test, oncall@example.test' };
const helpRequest = { id: '42', device_id: '987654321', hostname: 'PC-RECEPTION', message: 'Printer does not work <again> & "nobody" helps' };

const calls = {
    send: { fn: 'sendEmail', args: [{ to: 'admin@example.test', subject: 'BetterDesk test', text: 'Plain text body\nsecond line', html: '<p>HTML body</p>' }] },
    sendList: { fn: 'sendEmail', args: [{ to: 'a@example.test, B <b@example.test>,c@example.test', subject: 'Three recipients', text: 'text only' }] },
    sendArray: { fn: 'sendEmail', args: [{ to: ['a@example.test', 'b@example.test'], subject: 'Array of recipients', html: '<b>html only</b>' }] },
    sendUnicode: { fn: 'sendEmail', args: [{ to: 'm\u00fcller@b\u00fccher.example', subject: 'Gr\u00fc\u00dfe \u2713 \ud83d\ude00 ' + 'lang '.repeat(30), text: 'Stra\u00dfe\n.\n.leading dot\nlong ' + 'x'.repeat(1200), html: '<p>Stra\u00dfe \u2713</p>' }] },
    sendNoRecipient: { fn: 'sendEmail', args: [{ to: '', subject: 'Nobody', text: 'x' }] },
    sendLarge: { fn: 'sendEmail', args: [{ to: 'admin@example.test', subject: 'Large', text: ('line of text with a dot at the end.\n.dot first\n').repeat(900) }] },
    alert: { fn: 'sendAlertEmail', args: [alert, rule] },
    alertNoRecipients: { fn: 'sendAlertEmail', args: [alert, Object.assign({}, rule, { notify_emails: '' })] },
    alertNoDevice: { fn: 'sendAlertEmail', args: [Object.assign({}, alert, { device_id: null, severity: 'warning' }), Object.assign({}, rule, { description: '', notify_emails: 'ops@example.test' })] },
    help: { fn: 'sendHelpRequestEmail', args: [{ to: 'operator@example.test', helpRequest, folderName: 'Support & Sales' }] },
    helpNoFolder: { fn: 'sendHelpRequestEmail', args: [{ to: ['op1@example.test', 'op2@example.test'], helpRequest: { id: '43', device_id: '555', message: '' }, folderName: '' }] },
    test: { fn: 'testConnection', args: [] },
};

const FROM = { from: 'BetterDesk <noreply@example.test>', alert_email: 'ops@example.test' };
// [name, server, call, config, how the setting is stored, where the config points]
const table = [
    ['plain/send', 'plain', 'send', FROM],
    ['plain/send-no-from', 'plain', 'send', {}],
    ['plain/send-list', 'plain', 'sendList', FROM],
    ['plain/send-array', 'plain', 'sendArray', FROM],
    ['plain/send-unicode', 'plain', 'sendUnicode', FROM],
    ['plain/send-no-recipient', 'plain', 'sendNoRecipient', FROM],
    ['plain/send-large', 'plain', 'sendLarge', FROM],
    ['plain/alert', 'plain', 'alert', FROM],
    ['plain/alert-no-recipients', 'plain', 'alertNoRecipients', FROM],
    ['plain/alert-no-device', 'plain', 'alertNoDevice', FROM],
    ['plain/help', 'plain', 'help', FROM],
    ['plain/help-no-folder', 'plain', 'helpNoFolder', FROM],
    ['plain/test', 'plain', 'test', FROM],
    ['plain/test-credentials-not-used', 'plain', 'test', Object.assign({}, FROM, CREDS)],
    ['plain/send-credentials-not-used', 'plain', 'send', Object.assign({}, FROM, CREDS)],
    ['plain/send-stored-as-object', 'plain', 'send', FROM, 'object'],
    ['plain/send-port-as-string', 'plain', 'send', FROM, 'string', { portAsString: true }],
    ['plain/send-localhost', 'plain', 'send', Object.assign({ host: 'localhost' }, FROM)],
    ['plain-auth/send', 'plainAuth', 'send', Object.assign({}, FROM, CREDS)],
    ['plain-auth/alert', 'plainAuth', 'alert', Object.assign({}, FROM, CREDS)],
    ['plain-auth/help', 'plainAuth', 'help', Object.assign({}, FROM, CREDS)],
    ['plain-auth/test', 'plainAuth', 'test', Object.assign({}, FROM, CREDS)],
    ['plain-auth/test-no-credentials', 'plainAuth', 'test', FROM],
    ['plain-auth/test-user-only', 'plainAuth', 'test', Object.assign({}, FROM, { user: 'console@example.test', pass: '' })],
    ['plain-auth/send-unicode-credentials', 'plainAuth', 'send', Object.assign({}, FROM, { user: 'j\u00f6rg@example.test', pass: 'p\u00e4ssw\u00f6rd \u2713' })],
    ['plain-pipelining/send', 'plainPipelining', 'send', Object.assign({}, FROM, CREDS)],
    ['plain-pipelining/send-list', 'plainPipelining', 'sendList', Object.assign({}, FROM, CREDS)],
    ['plain-pipelining/send-unicode', 'plainPipelining', 'sendUnicode', Object.assign({}, FROM, CREDS)],
    ['plain-pipelining/alert', 'plainPipelining', 'alert', Object.assign({}, FROM, CREDS)],
    ['plain-pipelining/test', 'plainPipelining', 'test', Object.assign({}, FROM, CREDS)],
    ['starttls/send', 'starttls', 'send', Object.assign({}, FROM, CREDS)],
    ['starttls/send-no-credentials', 'starttls', 'send', FROM],
    ['starttls/send-verify-on', 'starttls', 'send', Object.assign({ host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['starttls/send-verify-on-ip', 'starttls', 'send', Object.assign({ tlsVerify: true }, FROM, CREDS)],
    ['starttls/send-verify-off', 'starttls', 'send', Object.assign({ tlsVerify: false }, FROM, CREDS)],
    ['starttls/alert', 'starttls', 'alert', Object.assign({}, FROM, CREDS)],
    ['starttls/help', 'starttls', 'help', Object.assign({}, FROM, CREDS)],
    ['starttls/test', 'starttls', 'test', Object.assign({}, FROM, CREDS)],
    ['starttls/test-verify-on', 'starttls', 'test', Object.assign({ host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['starttls-untrusted/test-verify-on', 'starttlsUntrusted', 'test', Object.assign({ host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['starttls-untrusted/send-verify-on', 'starttlsUntrusted', 'send', Object.assign({ host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['starttls-untrusted/test-verify-default', 'starttlsUntrusted', 'test', Object.assign({ host: 'localhost' }, FROM, CREDS)],
    ['starttls-untrusted/send-verify-off', 'starttlsUntrusted', 'send', Object.assign({ host: 'localhost', tlsVerify: false }, FROM, CREDS)],
    ['starttls-other-name/test-verify-on', 'starttlsOtherName', 'test', Object.assign({ host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['starttls-other-name/test-verify-off', 'starttlsOtherName', 'test', Object.assign({ host: 'localhost', tlsVerify: false }, FROM, CREDS)],
    ['implicit/send', 'implicit', 'send', Object.assign({ secure: true }, FROM, CREDS)],
    ['implicit/send-no-credentials', 'implicit', 'send', Object.assign({ secure: true }, FROM)],
    ['implicit/send-verify-on', 'implicit', 'send', Object.assign({ secure: true, host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['implicit/alert', 'implicit', 'alert', Object.assign({ secure: true }, FROM, CREDS)],
    ['implicit/help', 'implicit', 'help', Object.assign({ secure: true }, FROM, CREDS)],
    ['implicit/test', 'implicit', 'test', Object.assign({ secure: true }, FROM, CREDS)],
    ['implicit/test-verify-on', 'implicit', 'test', Object.assign({ secure: true, host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['implicit/test-verify-on-ip', 'implicit', 'test', Object.assign({ secure: true, tlsVerify: true }, FROM, CREDS)],
    ['implicit/test-secure-as-string', 'implicit', 'test', Object.assign({ secure: 'true' }, FROM, CREDS)],
    ['implicit-cram/send', 'implicitCram', 'send', Object.assign({ secure: true }, FROM, CREDS)],
    ['implicit-cram/test', 'implicitCram', 'test', Object.assign({ secure: true }, FROM, CREDS)],
    ['implicit-untrusted/test-verify-on', 'implicitUntrusted', 'test', Object.assign({ secure: true, host: 'localhost', tlsVerify: true }, FROM, CREDS)],
    ['implicit-untrusted/send-verify-off', 'implicitUntrusted', 'send', Object.assign({ secure: true, host: 'localhost', tlsVerify: false }, FROM, CREDS)],
    ['wrong-password/send', 'wrongPassword', 'send', Object.assign({}, FROM, CREDS)],
    ['wrong-password/test', 'wrongPassword', 'test', Object.assign({}, FROM, CREDS)],
    ['one-recipient-refused/send-list', 'oneRecipientRefused', 'sendList', FROM],
    ['all-recipients-refused/alert', 'allRecipientsRefused', 'alert', FROM],
    ['sender-refused/send', 'senderRefused', 'send', FROM],
    ['message-refused/help', 'messageRefused', 'help', FROM],
    ['busy/send', 'busy', 'send', FROM],
    ['busy/test', 'busy', 'test', FROM],
    ['refused/send', null, 'send', FROM],
    ['refused/test', null, 'test', FROM],
    ['unresolvable/test', null, 'test', Object.assign({ host: 'smtpwire-no-such-host.invalid' }, FROM)],
    ['no-config/send', null, 'send', null],
    ['no-config/test', null, 'test', null],
    ['config-without-host/test', null, 'test', { host: '', port: 2525, from: 'x@example.test' }, 'string', { noPort: true }],
];

function capture() {
    const logs = [];
    const original = { log: console.log, warn: console.warn, error: console.error };
    for (const level of Object.keys(original)) {
        console[level] = (...args) => logs.push({ level, text: args.map(String).join(' ') });
    }
    return {
        logs,
        restore: () => Object.assign(console, original),
    };
}

async function record([name, serverName, callName, config, storedAs = 'string', extra = {}]) {
    const script = serverName ? servers[serverName]() : null;
    const sink = script ? await startSink(script) : null;
    const port = sink ? sink.port : await closedPort();
    let full = null;
    if (config) {
        full = Object.assign({ host: '127.0.0.1', port, secure: false, user: '', pass: '' }, config);
        if (extra.portAsString) {
            full.port = String(port);
        }
        if (extra.noPort) {
            full.port = config.port;
        }
    }
    storedConfig = full && (storedAs === 'object' ? full : JSON.stringify(full));
    emailService.resetTransporter();
    const call = calls[callName];
    const output = capture();
    let result;
    try {
        result = await emailService[call.fn](...JSON.parse(JSON.stringify(call.args)));
    } finally {
        output.restore();
    }
    const session = {
        name,
        hostname: 'console.example.test',
        config: storedConfig,
        port,
        call: { fn: call.fn, args: call.args },
        result,
        logs: output.logs.map(entry => ({ level: entry.level, text: entry.text.split(String(port)).join('{port}') })),
        server: script,
        client: [],
    };
    if (sink) {
        if (sink.connections() > 0) {
            await Promise.race([sink.done, sleep(6000)]);
        }
        session.client = sink.events.slice();
        await sink.close();
    }
    return session;
}

async function main() {
    setHostname({ value: 'console.example.test' });
    const sessions = [];
    for (const row of table) {
        sessions.push(await record(row));
    }
    setHostname(null);
    const file = {
        about: 'Sessions of web-nodejs/services/emailService.js against the scripted sink, see nodeoracle/README.md',
        nodemailer: require(WEB + '/node_modules/nodemailer/package.json').version,
        env: Object.assign({}, ENV, { DATA_DIR: '(a temporary directory)' }),
        panelBaseUrl: emailService.getPanelBaseUrl(),
        sessions,
    };
    const out = path.join(__dirname, '..', 'e2e_sessions.json');
    fs.writeFileSync(out, JSON.stringify(file, null, 1) + '\n');
    console.log(`${sessions.length} sessions written to ${out}`);
}

main().then(() => process.exit(0), err => {
    console.error(err);
    process.exit(1);
});
