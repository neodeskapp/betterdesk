'use strict';

// A scripted SMTP server for the recordings. It listens on loopback, plays the
// server side a scenario describes and records everything the client sends.
// The Go tests have the same sink (sink_test.go) and replay the same scripts,
// so both files have to read a script the same way.
//
// A script:
//   tls:      true for implicit TLS (the handshake comes before the greeting)
//   cert:     name of the key and chain under testdata/certs
//   greeting: ops run when a client has connected
//   rules:    { VERB: [ops, ops, ...] } the nth line with that verb runs the
//             nth list, the last list repeats. No rule: "250 OK".
// Verbs: the first word of a command line in upper case ("MAIL", "RCPT"),
// "AUTH <MECHANISM>" for AUTH (falls back to the rule "AUTH"), the name given
// by an op {cont: name} for the line after it, "ENDDATA" when the message
// data is complete, "DOT" for a line with a single dot, "EMPTY" for an empty
// line.
// Ops: {w: text} write (text holds bytes as Latin-1 characters),
// {wrep: {s, n}} write s n times, {sleep: ms}, {tls: 1} start TLS,
// {data: 1} read message data next, {cont: name} read the next line as verb
// name, {close: 1} close the sending side, {stall: 1} never answer again.
//
// Recorded events: {e:'tls', sni}, {e:'tlsfail'}, {e:'line', s},
// {e:'data', b64} or {e:'data', len, sha256} for large data,
// {e:'partial', s} for bytes without a line end before the client closed,
// {e:'eof'} when the client closed.

const net = require('net');
const tls = require('tls');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const CERT_DIR = path.join(__dirname, '..', 'certs');
const contexts = new Map();

function secureContext(name) {
    if (!contexts.has(name)) {
        contexts.set(name, tls.createSecureContext({
            key: fs.readFileSync(path.join(CERT_DIR, name + '.key')),
            cert: fs.readFileSync(path.join(CERT_DIR, name + '.chain.pem')),
        }));
    }
    return contexts.get(name);
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

class Session {
    constructor(sock, script, events) {
        this.plain = sock;
        this.sock = sock;
        this.script = script;
        this.events = events;
        this.buf = Buffer.alloc(0);
        this.ended = false;
        this.waiter = null;
        this.mode = 'line';
        this.stalled = false;
        this.counts = {};
        this.attach(sock);
        sock.setNoDelay(true);
    }

    attach(sock) {
        this.onData = chunk => {
            this.buf = Buffer.concat([this.buf, chunk]);
            this.wake();
        };
        this.onEnd = () => {
            this.ended = true;
            this.wake();
        };
        sock.on('data', this.onData);
        sock.on('end', this.onEnd);
        sock.on('close', this.onEnd);
        sock.on('error', this.onEnd);
    }

    detach(sock) {
        sock.removeListener('data', this.onData);
        sock.removeListener('end', this.onEnd);
        sock.removeListener('close', this.onEnd);
        sock.removeListener('error', this.onEnd);
        sock.on('error', () => {});
    }

    wake() {
        if (this.waiter) {
            const waiter = this.waiter;
            this.waiter = null;
            waiter();
        }
    }

    wait() {
        return new Promise(resolve => {
            this.waiter = resolve;
        });
    }

    async readLine() {
        for (;;) {
            const pos = this.buf.indexOf(0x0a);
            if (pos >= 0) {
                const line = this.buf.subarray(0, pos + 1);
                this.buf = this.buf.subarray(pos + 1);
                return line;
            }
            if (this.ended) {
                return null;
            }
            await this.wait();
        }
    }

    // The data ends with CRLF "." CRLF, where the first CRLF may be the one
    // that ended the DATA command
    async readData() {
        const end = Buffer.from('\r\n.\r\n');
        for (;;) {
            let pos = -1;
            if (this.buf.length >= 3 && this.buf.subarray(0, 3).toString('latin1') === '.\r\n') {
                pos = 3;
            } else {
                const found = this.buf.indexOf(end);
                if (found >= 0) {
                    pos = found + end.length;
                }
            }
            if (pos >= 0) {
                const data = this.buf.subarray(0, pos);
                this.buf = this.buf.subarray(pos);
                return data;
            }
            if (this.ended) {
                return null;
            }
            await this.wait();
        }
    }

    async startTLS() {
        this.detach(this.plain);
        const secure = new tls.TLSSocket(this.plain, {
            isServer: true,
            secureContext: secureContext(this.script.cert || 'localhost'),
        });
        const ok = await new Promise(resolve => {
            secure.once('secure', () => resolve(true));
            secure.once('error', () => resolve(false));
            secure.once('close', () => resolve(false));
            this.plain.once('close', () => resolve(false));
        });
        if (!ok) {
            this.events.push({ e: 'tlsfail' });
            this.ended = true;
            secure.on('error', () => {});
            secure.destroy();
            return false;
        }
        this.events.push({ e: 'tls', sni: secure.servername || null });
        this.sock = secure;
        this.buf = Buffer.alloc(0);
        this.attach(secure);
        return true;
    }

    async exec(ops) {
        for (const op of ops) {
            if (op.w !== undefined) {
                this.write(Buffer.from(op.w, 'latin1'));
            } else if (op.wrep) {
                this.write(Buffer.alloc(op.wrep.s.length * op.wrep.n, op.wrep.s, 'latin1'));
            } else if (op.sleep) {
                await sleep(op.sleep);
            } else if (op.tls) {
                if (!(await this.startTLS())) {
                    return;
                }
            } else if (op.data) {
                this.mode = 'data';
            } else if (op.cont) {
                this.mode = 'cont';
                this.contVerb = op.cont;
            } else if (op.close) {
                if (!this.sock.destroyed) {
                    this.sock.end();
                }
            } else if (op.stall) {
                this.stalled = true;
            }
        }
    }

    write(buf) {
        if (!this.sock.destroyed && this.sock.writable) {
            this.sock.write(buf, () => {});
        }
    }

    ops(verb) {
        const rules = this.script.rules || {};
        let list = rules[verb];
        let key = verb;
        if (!list && verb.startsWith('AUTH ')) {
            list = rules.AUTH;
            key = 'AUTH';
        }
        if (!list) {
            return [{ w: '250 OK\r\n' }];
        }
        const n = this.counts[key] || 0;
        this.counts[key] = n + 1;
        return list[Math.min(n, list.length - 1)];
    }

    async run() {
        if (this.script.tls) {
            if (!(await this.startTLS())) {
                this.events.push({ e: 'eof' });
                this.plain.destroy();
                return;
            }
        }
        await this.exec(this.script.greeting || [{ w: '220 sink.example.test ESMTP\r\n' }]);
        for (;;) {
            if (this.mode === 'data') {
                const data = await this.readData();
                if (data === null) {
                    break;
                }
                this.mode = 'line';
                if (data.length > 4096 && !this.script.fullData) {
                    this.events.push({ e: 'data', len: data.length, sha256: crypto.createHash('sha256').update(data).digest('hex') });
                } else {
                    this.events.push({ e: 'data', b64: data.toString('base64') });
                }
                if (!this.stalled) {
                    await this.exec(this.ops('ENDDATA'));
                }
                continue;
            }
            const line = await this.readLine();
            if (line === null) {
                break;
            }
            const text = line.toString('latin1');
            this.events.push({ e: 'line', s: text });
            let verb;
            if (this.mode === 'cont') {
                verb = this.contVerb;
                this.mode = 'line';
            } else {
                verb = verbOf(text);
            }
            if (!this.stalled) {
                await this.exec(this.ops(verb));
            }
        }
        if (this.buf.length) {
            this.events.push({ e: 'partial', s: this.buf.toString('latin1') });
        }
        this.events.push({ e: 'eof' });
        if (!this.sock.destroyed) {
            this.sock.end();
        }
        const plain = this.plain;
        setTimeout(() => plain.destroy(), 200).unref();
    }
}

function verbOf(text) {
    const line = text.replace(/\r?\n$/, '');
    if (line === '') {
        return 'EMPTY';
    }
    if (line === '.') {
        return 'DOT';
    }
    const words = line.split(/[ :]/);
    const first = words[0].toUpperCase();
    if (first === 'AUTH') {
        return 'AUTH ' + (words[1] || '').toUpperCase();
    }
    return first;
}

// Starts a sink for one scenario. Resolves with the port, the event list and
// a promise that settles when the (single) client connection is over.
function startSink(script) {
    return new Promise((resolve, reject) => {
        const events = [];
        let finished;
        const done = new Promise(r => {
            finished = r;
        });
        let connections = 0;
        const server = net.createServer(sock => {
            connections++;
            if (connections > 1) {
                events.push({ e: 'extra-connection' });
                sock.destroy();
                return;
            }
            const session = new Session(sock, script, events);
            session.run().then(finished, err => {
                events.push({ e: 'sink-error', s: String(err && err.message) });
                finished();
            });
        });
        server.on('error', reject);
        server.listen(0, '127.0.0.1', () => {
            resolve({
                port: server.address().port,
                events,
                done,
                connections: () => connections,
                close: () => new Promise(r => server.close(() => r())),
            });
        });
    });
}

// A port on loopback nothing listens on
function closedPort() {
    return new Promise((resolve, reject) => {
        const server = net.createServer();
        server.on('error', reject);
        server.listen(0, '127.0.0.1', () => {
            const port = server.address().port;
            server.close(() => resolve(port));
        });
    });
}

module.exports = { startSink, closedPort, CERT_DIR };
