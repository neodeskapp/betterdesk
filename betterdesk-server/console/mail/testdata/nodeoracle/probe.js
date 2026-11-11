'use strict';

/**
 * Asks the real nodemailer what it does with the options emailService hands
 * to createTransport, without a mail server: name lookups answer 127.0.0.1,
 * connections go to this machine's loopback address (where nothing
 * listens), and what nodemailer asked for is written down.
 *
 *   const { probe } = require('./probe')(realNodemailer, WEB);
 *   const seen = await probe(options);
 *
 * The answer is one of
 *   { connect: { tls, host, port, servername, rejectUnauthorized }, error }
 *       nodemailer opened a connection with these values (error is what the
 *       loopback address answered)
 *   { connect: {..., threw }, error }   Node's net or tls refused the values
 *   { error }                           nodemailer failed before connecting
 *   { crash }                           an exception nobody caught: the
 *                                       console's process ends
 * each with `auth` (the credentials nodemailer would log in with), `derived`
 * (host, port and secure as its connection object reads them) and `lookups`
 * (the names it asked DNS for).
 */

const net = require('net');
const tls = require('tls');
const dns = require('dns');
const path = require('path');
const { dump } = require('./lib');

module.exports = function makeProbe(nodemailer, WEB) {
    const SMTPConnection = require(path.join(WEB, 'node_modules/nodemailer/dist/cjs/smtp-connection/index.js')).default;
    const shared = require(path.join(WEB, 'node_modules/nodemailer/dist/cjs/shared/index.js'));
    let seen = null;

    const loopback = (host) => ['127.0.0.1', '::1'].includes(String(host));
    function wrap(real, isTLS) {
        return function connect(opts, cb) {
            // Only a running probe is kept at home; the database client
            // connects as usual.
            if (!seen) return real.apply(this, arguments);
            const rec = {
                tls: isTLS,
                host: dump(opts.host),
                port: dump(opts.port),
                servername: dump(opts.servername),
                rejectUnauthorized: dump(opts.rejectUnauthorized),
            };
            seen.connect = rec;
            const safe = Object.assign({}, opts);
            if (typeof safe.host === 'string') {
                safe.host = '127.0.0.1';
            } else if (!loopback(safe.host)) {
                rec.threw = 'ORACLE: host is no text';
                throw new Error(rec.threw);
            }
            try {
                return real.call(this, safe, cb);
            } catch (err) {
                rec.threw = err.message;
                throw err;
            }
        };
    }
    net.connect = wrap(net.connect, false);
    net.createConnection = net.connect;
    tls.connect = wrap(tls.connect, true);

    const proto = dns.Resolver.prototype;
    for (const [name, answer] of [['resolve4', ['127.0.0.1']], ['resolve6', []]]) {
        const real = proto[name];
        proto[name] = function resolve(hostname, cb) {
            if (!seen) return real.call(this, hostname, cb);
            if (name === 'resolve4') seen.lookups.push(dump(hostname));
            if (typeof hostname !== 'string') return real.call(this, hostname, cb);
            setImmediate(() => cb(null, answer));
            return undefined;
        };
    }
    const realLookup = dns.lookup;
    dns.lookup = function lookup(hostname, options, cb) {
        if (!seen || typeof hostname !== 'string') return realLookup.call(this, hostname, options, cb);
        const done = typeof options === 'function' ? options : cb;
        const all = options && options.all;
        setImmediate(() => (all ? done(null, [{ address: '127.0.0.1', family: 4 }]) : done(null, '127.0.0.1', 4)));
        return undefined;
    };

    const cache = new Map();

    function run(options) {
        return new Promise((resolve) => {
            seen = { lookups: [] };
            shared.dnsCache.clear();
            let done = false;
            const finish = (result) => {
                if (done) return;
                done = true;
                process.removeListener('uncaughtException', onCrash);
                const out = Object.assign(result, seen);
                seen = null;
                resolve(out);
            };
            const onCrash = (err) => finish({ crash: String(err && err.message), crashName: err && err.name });
            process.on('uncaughtException', onCrash);

            let transporter;
            try {
                transporter = nodemailer.createTransport(options);
            } catch (err) {
                finish({ error: err.message, thrownBy: 'createTransport' });
                return;
            }
            try {
                const conn = new SMTPConnection(options);
                seen.derived = { host: dump(conn.host), port: dump(conn.port), secure: conn.secureConnection, servername: dump(conn.servername) };
            } catch (err) {
                seen.derived = { threw: err.message };
            }
            const auth = transporter.transporter.auth;
            seen.auth = auth ? { user: dump(auth.credentials.user), pass: dump(auth.credentials.pass), type: auth.type } : null;
            transporter.verify().then(
                () => finish({ ok: true }),
                (err) => finish({ error: String(err && err.message), code: err && err.code }),
            );
        });
    }

    /** One answer per distinct set of options. */
    async function probe(options) {
        const key = dump(options);
        if (!cache.has(key)) cache.set(key, await run(options));
        return cache.get(key);
    }

    return { probe };
};
