'use strict';

// Vectors for the policy and attestation routes: routes/policies.routes.js
// of the Node console, mounted at / and at /api/bd as routes/index.js
// mounts it, with the real guards and the real API client in front of a
// scripted API server. Writes ../vectors.json.
//
// A query value that JavaScript cannot turn into text
// (limit[toString]=1) is not sent to GET /api/panel/attestation: the
// route throws outside any try in an async function, and the Node process
// ends.

const path = require('path');
const fs = require('fs');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));
const { random } = require(path.join(oracleDir, 'lib'));

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true,"connection":{},"entries":null}' };

const routes = [
    ['GET', '/api/panel/policies/org-1'],
    ['PUT', '/api/panel/policies/org-1/connection', true],
    ['PUT', '/api/panel/policies/org-1/features', true],
    ['PUT', '/api/panel/policies/org-1/security', true],
    ['PUT', '/api/panel/policies/org-1/network', true],
    ['PUT', '/api/panel/policies/org-1/update', true],
    ['GET', '/api/panel/policies/org-1/effective/900100001'],
    ['GET', '/api/panel/policies/org-1/audit'],
    ['GET', '/api/panel/attestation'],
    ['GET', '/api/panel/attestation/900100001'],
];

async function main() {
    const oracle = await start((app, load) => {
        const router = load('./routes/policies.routes');
        app.use('/', router);
        app.use('/api/bd', router);
    });
    const vectors = [];
    const send = async (request, answer) => vectors.push(await oracle.call(request, answer));
    const body = '{"allow_direct":true,"max_sessions":3,"hours":{"from":"08:00","to":"18:00"},"blocked":["a","b"]}';

    // Who may call what, at both mounts
    for (const [method, p, sends] of routes) {
        for (const mount of ['', '/api/bd']) {
            for (const session of [undefined, accounts.pro, accounts.viewer, accounts.operator, accounts.server_admin, accounts.global_admin, accounts.super_admin, accounts.admin]) {
                const request = { method, path: mount + p, session };
                if (sends) Object.assign(request, { type: json, body });
                await send(request, ok);
            }
        }
    }

    // Every kind of answer of the API server
    for (const [method, p, sends] of routes) {
        for (const answer of apiAnswers) {
            const request = { method, path: p, session: accounts.admin };
            if (sends) Object.assign(request, { type: json, body: '{"a":1}' });
            await send(request, answer);
        }
    }

    // Ids that may and may not go into a path. "policy" as an id makes the
    // path of other routes end like the one that has a fallback.
    const ids = ['a.b', 'a..b', 'a:b@c', 'a%25b', 'a+b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', 'a*b', 'A-Z_0.9', 'policy', 'audit', 'effective',
        'a'.repeat(256), 'a'.repeat(257), '%C3%A9', 'a%00', '%09abc%0A'];
    const missing = { status: 404, type: json, body: '{"error":"not found"}' };
    for (const [method, p, sends] of routes) {
        for (const id of ids) {
            for (const target of new Set([p.replace('org-1', id), p.replace('900100001', id)])) {
                if (target === p) continue;
                for (const answer of [ok, missing]) {
                    const request = { method, path: target, session: accounts.admin };
                    if (sends) Object.assign(request, { type: json, body: '{}' });
                    await send(request, answer);
                }
            }
        }
    }
    await send({ path: '/api/panel/policies/bad%20org/effective/bad%20device', session: accounts.admin }, ok);
    await send({ path: '/api/panel/policies/org-1/unknown', session: accounts.admin }, ok);
    await send({ method: 'PUT', path: '/api/panel/policies/org-1/unknown', session: accounts.admin, type: json, body: '{}' }, ok);
    await send({ method: 'PUT', path: '/api/panel/policies/org-1/audit', session: accounts.admin, type: json, body: '{}' }, ok);

    // Bodies of a write
    const bodies = ['{}', '[]', '["a","b"]', '{"2":"b","1":"a","name":"n"}', '{"__proto__":{"x":1},"constructor":2,"prototype":3,"keep":{"constructor":4}}',
        '{"n":-0.0,"big":12345678901234567890,"t":"\\u00e9 <b>"}', ' {"spaced" : true } '];
    for (const b of bodies) {
        await send({ method: 'PUT', path: '/api/panel/policies/org-1/security', session: accounts.global_admin, type: json, body: b }, ok);
    }
    await send({ method: 'PUT', path: '/api/panel/policies/org-1/security', session: accounts.admin }, ok);
    await send({ method: 'PUT', path: '/api/bd/api/panel/policies/org-1/update', session: accounts.admin, type: 'application/x-www-form-urlencoded', body: 'channel=stable&auto=1' }, ok);

    // limit and offset of the attestation list
    const queries = [
        '', 'limit=10', 'limit=10&offset=20', 'offset=5', 'limit=0', 'limit=-0', 'limit=-5&offset=-7', 'limit=200', 'limit=201', 'limit=1e3', 'limit=12abc', 'limit=abc',
        'limit=%2010', 'limit=+10', 'limit=%2B10', 'limit=0x1f', 'limit=0X10&offset=0x', 'limit=1.9&offset=2.9', 'limit=10&limit=20', 'limit[]=7', 'limit[]=7&limit[]=8',
        'limit[x]=7', 'offset=1&offset=2', 'offset[]=9', 'offset[0]=9&offset[1]=1', 'offset[x]=9', 'offset=99999999999999999999', 'offset=999999999999999999999',
        'offset=99999999999999999999999', 'offset=' + '9'.repeat(400), 'limit=' + '9'.repeat(400), 'offset=-' + '9'.repeat(400), 'offset=9007199254740993',
        'offset=0x' + 'f'.repeat(30), 'limit=Infinity', 'offset=Infinity', 'limit=%EF%BB%BF12', 'limit=%C2%A012', 'LIMIT=5', 'limit', 'limit=', 'limit=5#offset=6',
        'limit=5&other=1', 'limit=%35', 'limit=5,6', 'offset=5,6',
    ];
    const next = random(21);
    const tokens = ['limit', 'offset', 'limit', '[]', '[0]', '[x]', '=', '=', '&', '&', '1', '9', '0', '-', '+', 'x', 'e', '.', ',', '%20', '0x', '99999999999', 'a'];
    for (let i = 0; i < 500; i++) {
        let q = '';
        const n = 1 + next(9);
        for (let j = 0; j < n; j++) q += tokens[next(tokens.length)];
        queries.push(q);
    }
    for (const q of queries) {
        await send({ path: `/api/panel/attestation${q ? '?' + q : ''}`, session: accounts.server_admin }, { status: 200, type: json, body: '{"records":[{"device_id":"d1"}],"total":1}' });
    }
    await send({ path: '/api/panel/attestation?limit=5', session: accounts.admin }, { status: 201, type: json, body: '{"records":[],"total":0}' });
    await send({ path: '/api/panel/attestation/900100001', session: accounts.admin }, { status: 201, type: json, body: '{"status":"trusted"}' });

    // Other spellings Express takes for the same route
    await send({ path: '/API/Panel/Policies/org-1/', session: accounts.admin }, ok);
    await send({ path: '/api/panel/policies/ORG-1/Audit', session: accounts.admin }, ok);
    await send({ method: 'HEAD', path: '/api/panel/attestation', session: accounts.admin }, ok);

    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
