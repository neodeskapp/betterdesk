'use strict';

/**
 * Records conversations with the old console's services/serverTerminalProxy.js
 * on a real HTTP server: the refusals of the upgrade, and sockets that send
 * init, input, resize and close and get ready, output, end, error and the
 * close. Run once with node-pty hidden (pipe mode) and once with it (pty
 * mode). The session middleware, the permission lookup and the audit log
 * are stubbed; Date.now is fixed so the session ids repeat. After each step
 * the script waits until the shell printed a marker or was quiet, so the
 * output of a step does not depend on timing. The Go test plays the same
 * steps. See README.md.
 *
 *   node terminal.js pipe      writes ../node_terminal_pipe.json
 *   node terminal.js pty       writes ../node_terminal_pty.json
 */

const fs = require('fs');
const os = require('os');
const net = require('net');
const path = require('path');
const http = require('http');
const Module = require('module');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const MODE = process.argv[2];
if (MODE !== 'pipe' && MODE !== 'pty') {
    console.error('usage: node terminal.js pipe|pty');
    process.exit(2);
}
const OUT = path.join(__dirname, '..', `node_terminal_${MODE}.json`);

if (MODE === 'pipe') {
    const resolve = Module._resolveFilename;
    Module._resolveFilename = function (request, ...rest) {
        if (request === 'node-pty') throw new Error("Cannot find module 'node-pty'");
        return resolve.call(this, request, ...rest);
    };
}

Date.now = () => 1700000000000;

const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
let lookups = [];
stub('middleware/auth.js', {
    roleHasEffectivePermission: async (role, permission) => {
        lookups.push([role, permission]);
        if (role === 'throws') throw new Error('lookup failed');
        return ['admin', 'server_admin', 'super_admin', 'operator-with-override'].includes(role) && permission === 'server.config';
    },
});
const WebSocket = require(path.join(WEB, 'node_modules/ws'));
const { initServerTerminalProxy } = require(path.join(WEB, 'services/serverTerminalProxy.js'));

function sessionMiddleware(req, _res, next) {
    const raw = req.headers['x-oracle-session'];
    try {
        if (raw !== undefined) req.session = JSON.parse(raw);
    } catch (_) { /* none */ }
    next();
}

let audits = [];
const server = http.createServer();
initServerTerminalProxy(server, sessionMiddleware, { logAction: (...args) => audits.push(args) });

const HOST = os.hostname();
const norm = (s) => s.split(HOST).join('{HOST}');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const ADMIN = { userId: 7, user: { id: 7, username: 'oracle-admin', role: 'admin' } };

// ---- refusals, as raw bytes ----

function raw(port, text) {
    return new Promise((resolve) => {
        const sock = net.connect(port, '127.0.0.1');
        let got = '';
        sock.on('data', (c) => {
            got += c.toString('latin1');
            if (got.startsWith('HTTP/1.1 101') && got.includes('\r\n\r\n')) sock.destroy();
        });
        sock.on('close', () => resolve(got));
        sock.on('error', () => resolve(got));
        sock.write(text);
        setTimeout(() => sock.destroy(), 3000);
    });
}

function upgradeRequest(port, headers) {
    const lines = ['GET /ws/server-management/terminal HTTP/1.1', `Host: 127.0.0.1:${port}`, 'Upgrade: websocket', 'Connection: Upgrade',
        'Sec-WebSocket-Version: 13', ...headers];
    if (!headers.some((h) => h.startsWith('Sec-WebSocket-Key'))) lines.push('Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==');
    return lines.join('\r\n') + '\r\n\r\n';
}

// ---- conversations ----

class Client {
    constructor(port, session, extra) {
        this.events = [];
        this.output = '';
        this.closed = null;
        const headers = { 'X-Oracle-Session': JSON.stringify(session), Origin: 'http://127.0.0.1:' + port, ...(extra || {}) };
        this.ws = new WebSocket(`ws://127.0.0.1:${port}/ws/server-management/terminal`, { headers });
        this.ws.on('message', (data, isBinary) => {
            let msg;
            try { msg = JSON.parse(data.toString('utf8')); } catch (_) { this.events.push({ notJSON: data.toString('utf8') }); return; }
            if (msg.type === 'output') {
                this.output += msg.data;
                if (this.events.length === 0 || this.events[this.events.length - 1].type !== 'output') this.events.push({ type: 'output' });
            } else {
                this.events.push(msg);
            }
            if (isBinary) this.events.push({ binary: true });
        });
        this.ws.on('close', (code, reason) => { this.closed = { code, reason: reason.toString() }; });
        this.opened = new Promise((resolve) => this.ws.on('open', resolve));
    }
    take() {
        const out = { events: this.events, output: norm(this.output) };
        if (this.closed) out.closed = this.closed;
        this.events = [];
        this.output = '';
        return out;
    }
}

// Waits until the output holds the marker (count times) or the socket
// closed, then until the output has been quiet for quiet ms
async function settle(c, { marker, count = 1, quiet = 300, limit = 8000, untilClosed = false }) {
    const start = process.hrtime.bigint();
    const elapsed = () => Number((process.hrtime.bigint() - start) / 1000000n);
    for (;;) {
        const seen = marker ? c.output.split(marker).length - 1 : count;
        if (c.closed || (!untilClosed && seen >= count) || elapsed() > limit) break;
        await sleep(20);
    }
    let last = c.output.length;
    let still = 0;
    while (still < quiet && elapsed() < limit + quiet) {
        await sleep(20);
        if (c.output.length !== last || (c.events.length && c.events[c.events.length - 1].type !== 'output' && still === 0)) {
            last = c.output.length;
            still = 0;
        } else {
            still += 20;
        }
    }
}

function pidAlive(pid) {
    try {
        process.kill(pid, 0);
        const stat = fs.readFileSync(`/proc/${pid}/stat`, 'utf8');
        return !/^\d+ \(.*\) Z/.test(stat);
    } catch (_) {
        return false;
    }
}

async function conversation(port, name, env, steps, session = ADMIN) {
    const saved = {};
    for (const [k, v] of Object.entries(env)) { saved[k] = process.env[k]; if (v === null) delete process.env[k]; else process.env[k] = v; }
    audits = [];
    lookups = [];
    const c = new Client(port, session);
    await c.opened;
    const record = { name, env, steps: [] };
    const pids = {};
    for (const step of steps) {
        const r = { step };
        if (step.send !== undefined) {
            for (const m of [].concat(step.send)) c.ws.send(typeof m === 'string' ? m : JSON.stringify(m));
        }
        if (step.binary !== undefined) c.ws.send(Buffer.from(JSON.stringify(step.binary)), { binary: true });
        if (step.closeClient) c.ws.close();
        await settle(c, step);
        Object.assign(r, c.take());
        const m = /__PID(\w*)__(\d+)/.exec(r.output);
        if (m) pids[m[1]] = Number(m[2]);
        if (step.alive) {
            await sleep(step.alive);
            r.alive = Object.fromEntries(Object.entries(pids).map(([k, pid]) => [k, pidAlive(pid)]));
        }
        r.output = r.output.replace(/__PID(\w*)__\d+/g, '__PID$1__<pid>');
        record.steps.push(r);
    }
    if (!c.closed) {
        c.ws.close();
        await settle(c, { untilClosed: true, quiet: 100, limit: 3000 });
    }
    await sleep(100);
    for (const pid of Object.values(pids)) if (pidAlive(pid)) try { process.kill(pid, 'SIGKILL'); } catch (_) { /* gone */ }
    record.audits = audits.map(([userId, action, details, ip]) => [userId, action, details, ip]);
    record.lookups = lookups;
    for (const [k, v] of Object.entries(saved)) { if (v === undefined) delete process.env[k]; else process.env[k] = v; }
    return record;
}

const PROMPT = { quiet: 700 };
const run = (cmd, marker, extra) => ({ send: { type: 'input', data: `${cmd}; echo ${marker.replace(/_$/, '')}_\n` }, marker, count: MODE === 'pty' ? 2 : 1, ...extra });

(async () => {
    await new Promise((r) => server.listen(0, '127.0.0.1', r));
    const port = server.address().port;
    const out = { mode: MODE, host: '{HOST}', refusals: [], conversations: [] };

    const S = (o) => `X-Oracle-Session: ${JSON.stringify(o)}`;
    const ORIGIN = `Origin: http://127.0.0.1:${port}`;
    const refusals = [
        ['no session', [ORIGIN]],
        ['session without user id', [ORIGIN, S({ user: { role: 'admin' } })]],
        ['user id 0', [ORIGIN, S({ userId: 0, user: { role: 'admin' } })]],
        ['viewer', [ORIGIN, S({ userId: 3, user: { role: 'viewer', username: 'v' } })]],
        ['role only on the session', [ORIGIN, S({ userId: 3, role: 'admin' })]],
        ['lookup fails', [ORIGIN, S({ userId: 3, user: { role: 'throws' } })]],
        ['override grants', [ORIGIN, S({ userId: 3, user: { role: 'operator-with-override' } })]],
        ['foreign origin', ['Origin: https://evil.example', S(ADMIN)]],
        ['foreign origin, no session', ['Origin: https://evil.example']],
        ['no origin, session cookie', ['Cookie: bd.sid=s%3Aabc', S(ADMIN)]],
        ['no origin, no cookie', [S(ADMIN)]],
        ['bad key', [ORIGIN, S(ADMIN), 'Sec-WebSocket-Key: short']],
        ['bad key, no session', [ORIGIN, 'Sec-WebSocket-Key: short']],
        ['admin', [ORIGIN, S(ADMIN)]],
    ];
    for (const [name, headers] of refusals) {
        audits = [];
        lookups = [];
        const answer = await raw(port, upgradeRequest(port, headers));
        await sleep(100);
        out.refusals.push({ name, headers, answer: answer.replace(/Sec-WebSocket-Accept: .*\r\n/, 'Sec-WebSocket-Accept: <accept>\r\n'), lookups, audits });
    }

    const C = [];
    C.push(await conversation(port, 'basic', {}, [
        { send: { type: 'input', data: 'echo early\n' }, quiet: 300 },
        { send: ['hello', '[1]', 'null', '5', '"text"', '{', '{"type":"resize","cols":40}', '{"type":"close"}'], quiet: 300 },
        { send: { type: 'init', cols: 1000, rows: 'x' }, ...PROMPT },
        run('printf "%s|" "$TERM" "$COLUMNS" "$LINES" "$LANG" "$HOME" "$USER" "$LOGNAME" "$SHELL" "$(pwd)" "$(id -un)" "$(id -G)"', '__A__'),
        run('stty size 2>&1', '__B__'),
        { send: [{ type: 'resize', cols: 100, rows: 30 }, { type: 'resize', cols: 'abc', rows: 2 }], quiet: 300 },
        run('stty size 2>&1', '__C__'),
        { send: [{ type: 'input', data: 5 }, { type: 'ping' }, {}, { type: 'input' }], quiet: 300 },
        run('echo out; echo err 1>&2', '__D__'),
        { send: { type: 'input', data: 'exit\n' }, untilClosed: true, quiet: 300 },
    ]));
    C.push(await conversation(port, 'exit code', {}, [
        { send: { type: 'init' }, ...PROMPT },
        { send: { type: 'input', data: 'exit 3\n' }, untilClosed: true, quiet: 300 },
    ]));
    C.push(await conversation(port, 'killed', {}, [
        { send: { type: 'init' }, ...PROMPT },
        { send: { type: 'input', data: 'kill -9 $$\n' }, untilClosed: true, quiet: 300 },
    ]));
    C.push(await conversation(port, 'close message', {}, [
        { send: { type: 'init' }, ...PROMPT },
        run('echo __PID__$$', '__P__'),
        { send: { type: 'close' }, untilClosed: true, quiet: 300, alive: 1500 },
    ]));
    C.push(await conversation(port, 'client closes', {}, [
        { send: { type: 'init' }, ...PROMPT },
        run('echo __PID__$$', '__P__'),
        { closeClient: true, untilClosed: true, quiet: 300, alive: 1500 },
    ]));
    C.push(await conversation(port, 'second init', {}, [
        { send: { type: 'init', cols: 80, rows: 24 }, ...PROMPT },
        run('echo __PIDA__$$', '__P__'),
        { send: { type: 'init', cols: 30, rows: 10 }, ...PROMPT },
        run('echo __PIDB__$$; echo $COLUMNS', '__Q__'),
        { send: { type: 'input', data: 'exit\n' }, untilClosed: true, quiet: 300, alive: 1500 },
    ]));
    C.push(await conversation(port, 'binary init', {}, [
        { binary: { type: 'init', cols: 90 }, ...PROMPT },
        { binary: { type: 'input', data: 'exit\n' }, untilClosed: true, quiet: 300 },
    ]));
    C.push(await conversation(port, 'split character', {}, [
        { send: { type: 'init' }, ...PROMPT },
        run("printf '\\303'; sleep 0.3; printf '\\251\\n'", '__M__'),
        { send: { type: 'input', data: 'exit\n' }, untilClosed: true, quiet: 300 },
    ]));
    C.push(await conversation(port, 'much output', {}, [
        { send: { type: 'init' }, ...PROMPT },
        run("head -c 100000 /dev/zero | tr '\\0' a; echo", '__L__'),
        { send: { type: 'input', data: 'exit\n' }, untilClosed: true, quiet: 300 },
    ]));
    const SIZES = [[5, 1000], ['abc', undefined], [20.7, '7xyz'], ['1e3', -1], [[300], {}], [true, false], ['  42', ' 9 '], ['0x10', '010'], [499.9, 199.9], [1e21, 1e-7], [null, '-0'], ['', '0'], [501, 201], [19, 4]];
    for (const [cols, rows] of SIZES) {
        C.push(await conversation(port, `size ${JSON.stringify(cols)} x ${JSON.stringify(rows)}`, {}, [
            { send: { type: 'init', cols, rows }, ...PROMPT },
            run('echo "$COLUMNS $LINES"; stty size 2>/dev/null', '__S__'),
            { send: { type: 'close' }, untilClosed: true, quiet: 200 },
        ]));
    }
    const USERS = [
        { BETTERDESK_TERMINAL_USER: 'root' }, { BETTERDESK_TERMINAL_USER: '0' }, { BETTERDESK_TERMINAL_USER: '1000' }, { BETTERDESK_TERMINAL_USER: 'nobody' },
        { BETTERDESK_TERMINAL_USER: 'sync' }, { BETTERDESK_TERMINAL_USER: 'no-such-user' }, { SERVER_MANAGEMENT_USER: 'root' },
        { BETTERDESK_TERMINAL_USER: 'node', SERVER_MANAGEMENT_USER: 'root' }, { LANG: 'C.UTF-8' }, { LANG: '' },
    ];
    for (const env of USERS) {
        C.push(await conversation(port, `user ${JSON.stringify(env)}`, env, [
            { send: { type: 'init' }, ...PROMPT },
            run('printf "%s|" "$(id -un)" "$(id -G)" "$HOME" "$(pwd)" "$SHELL" "$LANG"', '__U__'),
            { send: { type: 'close' }, untilClosed: true, quiet: 200 },
        ]));
    }
    out.conversations = C;
    server.close();
    fs.writeFileSync(OUT, JSON.stringify(out, null, 1) + '\n');
    console.log(`wrote ${OUT}: ${out.refusals.length} upgrades, ${C.length} conversations, ${C.reduce((n, c) => n + c.steps.length, 0)} steps`);
    process.exit(0);
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
