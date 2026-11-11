'use strict';

// Runs every scenario of scenarios.js against the real nodemailer and writes
// what the client sent and how the call ended to ../sessions.json.

const dns = require('dns');
const fs = require('fs');
const path = require('path');
const { startSink, closedPort } = require('./sink');
const { scenarios } = require('./scenarios');
const { nodemailerPath, setHostname, messageBytes, errorFields, infoFields, sleep, pool } = require('./common');

const nodemailer = require(nodemailerPath);

if (!process.env.NODE_EXTRA_CA_CERTS) {
    console.error('run through run.sh: NODE_EXTRA_CA_CERTS has to name ../certs/trusted.pem');
    process.exit(2);
}

// The transport as services/emailService.js creates it. The timeouts are the
// only additions, and "default" leaves host or port to nodemailer.
function createTransport(o, port) {
    const smtpConfig = { host: o.host, port, secure: o.secure, user: o.user, pass: o.pass, tlsVerify: o.tlsVerify };
    const options = {
        host: smtpConfig.host,
        port: smtpConfig.port || 587,
        secure: smtpConfig.secure || false,
        auth: (smtpConfig.user && smtpConfig.pass) ? { user: smtpConfig.user, pass: smtpConfig.pass } : undefined,
        tls: { rejectUnauthorized: !!smtpConfig.tlsVerify },
    };
    if (o.host === 'default') {
        delete options.host;
    }
    if (o.port === 'default') {
        delete options.port;
    }
    for (const key of ['connectionTimeout', 'greetingTimeout', 'socketTimeout', 'dnsTimeout']) {
        if (o[key]) {
            options[key] = o[key];
        }
    }
    return nodemailer.createTransport(options);
}

const clone = value => JSON.parse(JSON.stringify(value));

// The hosts a scenario gives DNS records for (its `dns`). nodemailer asks a
// dns.Resolver for the A and AAAA records of the SMTP host; for these hosts
// the answer comes from the scenario, every other lookup is the real one.
const records = new Map();
for (const [family, key, query] of [[4, 'a', 'queryA'], [6, 'aaaa', 'queryAaaa']]) {
    const original = dns.Resolver.prototype['resolve' + family];
    dns.Resolver.prototype['resolve' + family] = function (hostname, ...rest) {
        const callback = rest[rest.length - 1];
        const fake = records.get(hostname);
        if (!fake) {
            return original.call(this, hostname, ...rest);
        }
        setImmediate(() => {
            if (fake[key] && fake[key].length) {
                return callback(null, fake[key].slice());
            }
            callback(Object.assign(new Error(`${query} ENODATA ${hostname}`), { code: 'ENODATA', syscall: query, hostname }));
        });
        return undefined;
    };
}

async function run(sc) {
    let sink = null;
    let port;
    if (sc.server.listen === false) {
        port = typeof sc.options.port === 'number' ? sc.options.port : await closedPort();
    } else {
        sink = await startSink(sc.server);
        port = sink.port;
    }
    if (sc.dns) {
        records.set(sc.options.host, sc.dns);
    }
    const transporter = createTransport(sc.options, port);
    let envelope = null;
    transporter.use('stream', (mail, callback) => {
        if (sc.forced) {
            mail.message._envelope = clone(sc.envelope);
        }
        envelope = clone(mail.message.getEnvelope());
        callback();
    });
    const outcome = {};
    try {
        if (sc.call === 'verify') {
            await transporter.verify();
            outcome.ok = true;
        } else {
            const info = await transporter.sendMail({
                envelope: sc.forced ? { from: 'forced@example.test', to: ['forced@example.test'] } : sc.envelope,
                raw: messageBytes(sc.message),
            });
            outcome.info = infoFields(info);
        }
    } catch (err) {
        outcome.error = errorFields(err);
    }
    const record = {
        name: sc.name,
        call: sc.call,
        hostname: sc.hostname,
        options: Object.assign({}, sc.options, { port: sc.options.port === 'default' ? 0 : port }),
        cmp: Object.assign({}, sc.cmp || {}),
        server: sc.server,
    };
    if (sc.dns) {
        record.dns = sc.dns;
    }
    if (sc.call === 'send') {
        record.envelope = envelope;
        record.message = sc.message;
    }
    if (sink) {
        // a client that never got as far as connecting leaves nothing to wait for
        if (sink.connections() > 0) {
            const closed = await Promise.race([sink.done.then(() => true), sleep(6000).then(() => false)]);
            if (!closed) {
                record.clientStillOpen = true;
            }
        }
        record.client = sink.events.slice();
        await sink.close();
    } else {
        record.client = [];
    }
    if (sc.options.tlsVerify && outcome.error && outcome.error.code === 'ESOCKET') {
        // nodemailer finishes the handshake and then drops the connection, the
        // port stops inside the handshake
        record.cmp.tlsEvents = false;
    }
    record.outcome = outcome;
    return record;
}

async function main() {
    const only = process.argv[2] ? new RegExp(process.argv[2]) : null;
    const todo = scenarios.filter(sc => !only || only.test(sc.name));
    const groups = new Map();
    for (const sc of todo) {
        const key = JSON.stringify(sc.hostname);
        if (!groups.has(key)) {
            groups.set(key, []);
        }
        groups.get(key).push(sc);
    }
    const results = new Map();
    for (const group of groups.values()) {
        setHostname(group[0].hostname);
        await pool(group, 12, async sc => {
            results.set(sc.name, await run(sc));
        });
    }
    setHostname(null);
    const records = todo.map(sc => results.get(sc.name));
    if (only) {
        console.log(JSON.stringify(records, null, 1));
        return;
    }
    const out = path.join(__dirname, '..', 'sessions.json');
    fs.writeFileSync(out, '[\n' + records.map(r => JSON.stringify(r)).join(',\n') + '\n]\n');
    const open = records.filter(r => r.clientStillOpen).map(r => r.name);
    console.log(`${records.length} sessions written to ${out}`);
    if (open.length) {
        console.log('client still connected after 6 s: ' + open.join(', '));
    }
}

process.on('uncaughtException', err => {
    console.error('uncaught', err);
    process.exit(1);
});

main().then(() => process.exit(0), err => {
    console.error(err);
    process.exit(1);
});
