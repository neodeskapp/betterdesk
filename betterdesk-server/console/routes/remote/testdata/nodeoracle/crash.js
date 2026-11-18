'use strict';

// Checks which requests end the Node process: each case runs
// routes/remote.routes.js in a child process of its own, sends one
// request and reports whether the process was still there to answer.
// Writes ../crashes.json, which routes_test.go reads: the Go console
// closes those connections without an answer (DECISIONS A19).
//
//   node crash.js

const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const CASES = [
    '/api/remote/viewer/dev-1?guest[toString]=1',
    '/api/remote/viewer/dev-1?t[toString]=1',
    '/api/remote/viewer/dev-1?mesh_share[toString]=1',
    '/api/remote/viewer/dev-1?transport[toString]=1',
    '/api/remote/viewer/dev-1?guest[a][toString]=1',
    '/api/remote/guest-info?t[toString]=1',
    '/api/remote/guest-info?guest[toString]=1',
    '/api/remote/login-info?return[toString]=1',
];

if (process.argv[2] === 'child') {
    const { load } = require(path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle', 'lib'));
    const http = require('http');
    process.env.RDCLIENT_PAGE_RATE_LIMIT_MAX = '100000000';
    const express = load('express');
    const app = express();
    app.use(load('cookie-parser')());
    app.use((req, _res, next) => {
        req.session = process.argv[4] ? JSON.parse(process.argv[4]) : {};
        req.session.destroy = () => {};
        next();
    });
    app.use(load('./middleware/i18n').initI18n());
    app.use('/', load('./routes/remote.routes'));
    const server = http.createServer(app).listen(0, '127.0.0.1', () => {
        http.get({ host: '127.0.0.1', port: server.address().port, path: process.argv[3] }, (res) => {
            res.resume();
            res.on('end', () => {
                console.log(`ANSWER ${res.statusCode}`);
                process.exit(0);
            });
        }).on('error', () => {});
    });
} else {
    const out = [];
    const operator = JSON.stringify({ userId: 5, user: { id: 5, username: 'e2e_operator', role: 'operator' } });
    for (const target of CASES) {
        for (const session of ['', operator]) {
            const run = spawnSync(process.execPath, [__filename, 'child', target, session], { encoding: 'utf8', timeout: 20000 });
            const answered = /ANSWER (\d+)/.exec(run.stdout || '');
            out.push({ path: target, signedIn: session !== '', ended: !answered, status: answered ? Number(answered[1]) : 0, exit: run.status });
        }
    }
    fs.writeFileSync(path.join(__dirname, '..', 'crashes.json'), JSON.stringify(out, null, 1) + '\n');
    console.log(out.map((c) => `${c.ended ? 'ENDED ' : 'answer'} ${c.status} ${c.signedIn ? 'operator' : 'signed out'} ${c.path}`).join('\n'));
}
