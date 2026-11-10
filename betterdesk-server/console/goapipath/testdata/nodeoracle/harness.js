'use strict';

// Runs route files of the Node console against an API server that is a
// script: every request is sent to the real routes through Express, the
// routes call the real API client, and the stub answers what the vector
// says. The result is one vector per request: what the client got and
// what the API server was asked.
//
// The route groups' own oracle scripts use this file too
// (console/routes/<group>/testdata/nodeoracle).

const http = require('http');
const os = require('os');
const fs = require('fs');
const path = require('path');
const { load } = require('./lib');

function listen(server) {
    return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server.address().port)));
}

// start brings up the API stub and an Express app. mount(app, load) adds
// the routes; load is require() from the Node console's folder. The API
// client is only loaded after the stub listens, because it reads its
// address once.
async function start(mount) {
    const state = { answer: null, seen: [] };
    const stub = http.createServer((req, res) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => {
            state.seen.push({
                method: req.method,
                url: req.url,
                type: req.headers['content-type'] || '',
                body: Buffer.concat(chunks).toString('utf8'),
            });
            const a = state.answer || { status: 404, type: 'application/json', body: '{"error":"not found"}' };
            if (a.hang) {
                req.socket.destroy();
                return;
            }
            const headers = {};
            if (a.type) headers['Content-Type'] = a.type;
            if (a.disposition) headers['Content-Disposition'] = a.disposition;
            res.writeHead(a.status, headers);
            res.end(a.base64 ? Buffer.from(a.base64, 'base64') : (a.body || ''));
        });
    });
    const apiPort = await listen(stub);

    process.env.BETTERDESK_API_URL = `http://127.0.0.1:${apiPort}/api`;
    process.env.DATA_DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-oracle-'));
    process.env.NODE_ENV = 'development';

    const express = load('express');
    const app = express();
    app.use(express.json({ limit: '2mb' }));
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    // The session of the request, from a test header: {"userId":7,"user":{...}}
    app.use((req, _res, next) => {
        const header = req.headers['x-test-session'];
        req.session = header ? JSON.parse(header) : {};
        req.session.destroy = () => {};
        next();
    });
    mount(app, load);
    // A request no route takes. The Go replay answers the same.
    app.use((req, res) => {
        res.status(404).type('text/plain').send('no route');
    });
    // The console's last error handler, outside production
    app.use((err, req, res, _next) => {
        res.status(err.status || 500).json({ success: false, error: err.message });
    });
    const server = http.createServer(app);
    const port = await listen(server);

    // call sends one request and returns its vector. request: { method,
    // path, session, type, body, headers }. answer: what the API stub says
    // to every call during this request; null is 404 {"error":"not found"}.
    function call(request, answer) {
        state.answer = answer || null;
        state.seen = [];
        return new Promise((resolve, reject) => {
            const headers = { ...(request.headers || {}) };
            if (request.session) headers['x-test-session'] = JSON.stringify(request.session);
            if (request.type) headers['Content-Type'] = request.type;
            const body = request.body === undefined ? null : Buffer.from(request.body, 'utf8');
            if (body) headers['Content-Length'] = body.length;
            const req = http.request({ host: '127.0.0.1', port, method: request.method || 'GET', path: request.path, headers }, (res) => {
                const chunks = [];
                res.on('data', (c) => chunks.push(c));
                res.on('end', () => {
                    const got = Buffer.concat(chunks);
                    const vector = {
                        request: { method: request.method || 'GET', path: request.path },
                        api: answer || null,
                        status: res.statusCode,
                        type: res.headers['content-type'] || '',
                        calls: state.seen,
                    };
                    if (request.session) vector.request.session = request.session;
                    if (request.type) vector.request.type = request.type;
                    if (request.body !== undefined) vector.request.body = request.body;
                    if (request.headers) vector.request.headers = request.headers;
                    if (res.headers['content-disposition']) vector.disposition = res.headers['content-disposition'];
                    if (request.binary) vector.base64 = got.toString('base64');
                    else vector.body = got.toString('utf8');
                    resolve(vector);
                });
            });
            req.on('error', reject);
            if (body) req.write(body);
            req.end();
        });
    }

    function stop() {
        server.close();
        stub.close();
        server.closeAllConnections();
        stub.closeAllConnections();
    }

    return { call, stop };
}

// Accounts the vectors sign in as. Without the permission tables loaded,
// the Node guards decide by the built-in role map, as the Go guards do in
// a test without the pipeline.
const accounts = {
    admin: { userId: 1, user: { id: 1, username: 'e2e_admin', role: 'admin' } },
    super_admin: { userId: 2, user: { id: 2, username: 'e2e_super_admin', role: 'super_admin' } },
    server_admin: { userId: 3, user: { id: 3, username: 'e2e_server_admin', role: 'server_admin' } },
    global_admin: { userId: 4, user: { id: 4, username: 'e2e_global_admin', role: 'global_admin' } },
    operator: { userId: 5, user: { id: 5, username: 'e2e_operator', role: 'operator' } },
    viewer: { userId: 6, user: { id: 6, username: 'e2e_viewer', role: 'viewer' } },
    pro: { userId: 8, user: { id: 8, username: 'e2e_pro', role: 'pro' } },
};

// Answers of the API stub that every proxy route should be tried with.
const apiAnswers = [
    { status: 200, type: 'application/json', body: '{"b":1,"a":[1,2,{"z":null,"y":true}],"10":"x","2":"y"}' },
    { status: 200, type: 'application/json', body: '[]' },
    { status: 201, type: 'application/json', body: '{"id":"t-1","n":1.50,"big":12345678901234567890,"exp":1e21,"small":0.0000001,"html":"<a href=\\"x\\">&</a>","u":"\\u00e9\\u4e16"}' },
    { status: 200, type: 'text/plain', body: 'plain text, not JSON' },
    { status: 200, type: 'text/plain', body: '{"json":"in a text answer"}' },
    { status: 200, type: 'application/json', body: '' },
    { status: 200, type: 'application/json', body: 'null' },
    { status: 200, type: 'application/json', body: '"text"' },
    { status: 200, type: 'application/json', body: '17' },
    { status: 200, type: 'application/json', body: '{"zero":-0.0,"list":[-0,{"__proto__":1,"constructor":{"prototype":2}}]}' },
    { status: 409, type: 'application/json', body: '-0' },
    { status: 204 },
    { status: 400, type: 'application/json', body: '{"error":"bad request","details":["a","b"]}' },
    { status: 403, type: 'application/json', body: '{"error":"Not a member of this organization"}' },
    { status: 404, type: 'application/json', body: '{"error":"not found"}' },
    { status: 404, type: 'text/plain', body: '404 page not found\n' },
    { status: 404 },
    { status: 409, type: 'application/json', body: 'false' },
    { status: 409, type: 'application/json', body: '0' },
    { status: 409, type: 'application/json', body: 'null' },
    { status: 409, type: 'application/json', body: '""' },
    { status: 409, type: 'application/json', body: '[]' },
    { status: 500, type: 'application/json', body: '{"error":"failed to save policy"}' },
    { status: 502, type: 'text/html', body: '<html>Bad Gateway</html>' },
    { status: 503 },
    { hang: true },
];

module.exports = { start, accounts, apiAnswers };
