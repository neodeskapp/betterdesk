'use strict';

// The Node console's auth service as a child process of the Go tests
// (interop_test.go, build tag nodeinterop). It works on the database the
// test names in the environment (DB_TYPE with DB_PATH or DATABASE_URL) and
// answers one JSON request per line on stdin with one JSON line on stdout:
//
//   {"fn": "verifyPassword", "args": ["pw", "$2b$..."], "now": 1794287700123}
//   {"result": true}   or   {"thrown": "message"}
//
// "now" fixes Date.now() for the call. There is no Go server: its address
// points at a closed port, so every call to it fails at once.

const path = require('path');
const readline = require('readline');

const WEB = process.env.BETTERDESK_NODE_ROOT || '/repo/web-nodejs';
process.env.BETTERDESK_API_URL = 'http://127.0.0.1:1/api';
process.env.BETTERDESK_API_TIMEOUT = '200';
process.env.LOG_LEVEL = 'error';

const realLog = console.log;
console.log = () => {}; // stdout carries the answers only
console.warn = () => {};

const RealDate = Date;
let fakeNow = null;
class FakeDate extends RealDate {
    constructor(...args) {
        if (args.length === 0 && fakeNow !== null) super(fakeNow);
        else super(...args);
    }
    static now() {
        return fakeNow !== null ? fakeNow : RealDate.now();
    }
}
global.Date = FakeDate;

const db = require(path.join(WEB, 'services/database'));
const authService = require(path.join(WEB, 'services/authService'));
const deviceAuth = require(path.join(WEB, 'middleware/deviceAuth'));
const deviceTokenAuth = require(path.join(WEB, 'lib/deviceTokenAuth'));
const { authenticator } = require(path.join(WEB, 'node_modules/otplib'));

async function run(fn, args) {
    switch (fn) {
    case 'ping':
        return 'pong';
    case 'codeAt':
        return authenticator.generate(args[0]);
    case 'checkCode':
        return authenticator.verify({ token: args[1], secret: args[0] });
    case 'verifyDeviceWsAuth':
        return deviceTokenAuth.verifyDeviceWsAuth(args[0], args[1], db);
    case 'requireDeviceToken': {
        const req = { headers: { authorization: args[0] } };
        let answer = null;
        const res = { status(code) { answer = { status: code }; return this; }, json(body) { answer.body = body; return this; } };
        await deviceAuth.requireDeviceToken(req, res, () => { answer = { next: true, deviceId: req.deviceId }; });
        return answer;
    }
    default:
        if (typeof authService[fn] !== 'function') throw new Error(`no such function: ${fn}`);
        return authService[fn](...args);
    }
}

(async () => {
    await db.init();
    realLog(JSON.stringify({ ready: true }));
    const lines = readline.createInterface({ input: process.stdin });
    for await (const line of lines) {
        if (!line.trim()) continue;
        let answer;
        try {
            const request = JSON.parse(line);
            fakeNow = typeof request.now === 'number' ? request.now : null;
            const result = await run(request.fn, request.args || []);
            answer = { result: result === undefined ? null : result };
        } catch (err) {
            answer = { thrown: String(err && err.message) };
        } finally {
            fakeNow = null;
        }
        realLog(JSON.stringify(answer));
    }
    process.exit(0);
})().catch((err) => {
    process.stderr.write(`${err && err.stack}\n`);
    process.exit(1);
});
