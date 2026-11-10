'use strict';

// Vectors for the fleet routes: routes/fleet.routes.js of the Node
// console, mounted at / and at /api/bd as routes/index.js mounts it, with
// the real guards and the real API client in front of a scripted API
// server. Writes ../vectors.json.

const path = require('path');
const fs = require('fs');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));
const { random } = require(path.join(oracleDir, 'lib'));

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true,"items":[]}' };

// Every admin-facing route: method, path with good parameters, and
// whether it sends a body.
const routes = [
    ['GET', '/api/panel/fleet/availability'],
    ['GET', '/api/panel/fleet/resources/acme'],
    ['POST', '/api/panel/fleet/resources/acme', true],
    ['PUT', '/api/panel/fleet/resources/acme/res-1', true],
    ['DELETE', '/api/panel/fleet/resources/acme/res-1'],
    ['GET', '/api/panel/fleet/tasks'],
    ['GET', '/api/panel/fleet/tasks/task-1'],
    ['POST', '/api/panel/fleet/tasks', true],
    ['PUT', '/api/panel/fleet/tasks/task-1', true],
    ['DELETE', '/api/panel/fleet/tasks/task-1'],
    ['GET', '/api/panel/fleet/tasks/task-1/output'],
    ['POST', '/api/panel/fleet/tasks/task-1/retry', true],
    ['GET', '/api/panel/fleet/workflows'],
    ['GET', '/api/panel/fleet/workflows/wf-1'],
    ['POST', '/api/panel/fleet/workflows', true],
    ['PUT', '/api/panel/fleet/workflows/wf-1', true],
    ['DELETE', '/api/panel/fleet/workflows/wf-1'],
    ['POST', '/api/panel/fleet/workflows/wf-1/execute', true],
    ['GET', '/api/panel/fleet/workflows/wf-1/history'],
    ['GET', '/api/panel/fleet/compliance'],
    ['GET', '/api/panel/fleet/compliance/900100001'],
    ['POST', '/api/panel/fleet/compliance/900100001/scan', true],
    ['POST', '/api/panel/fleet/compliance/900100001/remediate', true],
];

async function main() {
    const oracle = await start((app, load) => {
        const router = load('./routes/fleet.routes');
        app.use('/', router);
        app.use('/api/bd', router);
    });
    const vectors = [];
    const send = async (request, answer) => vectors.push(await oracle.call(request, answer));
    const body = '{"name":"nightly","device_id":"900100001","created_by":"someone else","steps":[{"run":"echo"}]}';

    // Who may call what, at both mounts
    for (const [method, p, sends] of routes) {
        for (const mount of ['', '/api/bd']) {
            for (const session of [undefined, accounts.pro, accounts.viewer, accounts.server_admin, accounts.operator, accounts.global_admin, accounts.admin]) {
                const request = { method, path: mount + p, session };
                if (sends) Object.assign(request, { type: json, body });
                await send(request, ok);
            }
        }
    }

    // Every kind of answer of the API server
    for (const [method, p, sends] of routes) {
        for (const answer of apiAnswers) {
            const request = { method, path: p, session: accounts.operator };
            if (sends) Object.assign(request, { type: json, body: '{"a":1}' });
            await send(request, answer);
        }
    }

    // Parameters that may and may not go into a path
    const params = ['a.b', 'a..b', 'a:b@c', 'a%25b', 'a+b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', 'a*b', 'A-Z_0.9',
        'a'.repeat(256), 'a'.repeat(257), '%C3%A9', 'a%00', '%09abc%0A'];
    for (const [method, p, sends] of routes) {
        for (const bad of params) {
            for (const target of new Set([p.replace('acme', bad), p.replace(/res-1|task-1|wf-1|900100001/, bad)])) {
                if (target === p) continue;
                const request = { method, path: target, session: accounts.admin };
                if (sends) Object.assign(request, { type: json, body: '{}' });
                await send(request, ok);
            }
        }
    }
    await send({ method: 'PUT', path: '/api/panel/fleet/resources/bad%20org/bad%20res', session: accounts.admin, type: json, body: '{}' }, ok);

    // The query of the two list routes
    const queries = [
        'orgId=acme', 'orgId=acme&status=running&deviceId=900100001', 'deviceId=d1&status=s&orgId=o', 'orgId=&status=&deviceId=', 'orgId', 'orgId=bad+id',
        'orgId=%20acme%20', 'deviceId=a/b', 'orgId=bad+id&deviceId=also+bad', 'orgId=acme&deviceId=bad+id', 'orgId=a&orgId=b', 'orgId=&orgId=', 'orgId[]=acme',
        'orgId[]=', 'orgId[0]=acme', 'orgId[][]=acme', 'orgId[x]=acme', 'orgId[toString]=acme', 'orgId%5B%5D=acme', '[orgId]=acme', 'status=running&status=failed',
        'status[]=a&status[]=b', 'status[x]=a', 'status[toString]=a', 'status=' + 's'.repeat(80), 'status=' + '%F0%9F%98%80'.repeat(40), 'status=a%20b%26c%3Dd%2Be',
        'status=%E4%B8%96%E7%95%8C', 'status=a*b~c!d(e)', 'status=%zz', 'deviceId[toString]=x', 'deviceId=a..b', 'orgId=a:b@c%25%2Bd', 'orgId=..',
        'orgId=.', 'orgId=' + 'a'.repeat(256), 'orgId=' + 'a'.repeat(257), 'other=1', 'ORGID=acme', 'org_id=acme', 'status=0', 'orgId=0', 'orgId=acme#frag',
        'status=x&orgId[toString]=1', 'orgId=acme&status[toString]=1&deviceId=bad+id',
    ];
    const next = random(11);
    const tokens = ['orgId', 'status', 'deviceId', 'orgId', '[]', '[0]', '[x]', '[toString]', '=', '=', '&', '&', 'acme', 'bad+id', 'x', '%5B', '%5D', '%20', '', ',', '.', '..', '1'];
    for (let i = 0; i < 700; i++) {
        let q = '';
        const n = 1 + next(9);
        for (let j = 0; j < n; j++) q += tokens[next(tokens.length)];
        queries.push(q);
    }
    for (const q of queries) {
        await send({ path: `/api/panel/fleet/tasks?${q}`, session: accounts.operator }, ok);
        await send({ path: `/api/panel/fleet/compliance?${q}`, session: accounts.operator }, ok);
    }

    // Bodies, and who is named as the creator
    const bodies = ['{}', '[]', '["a","b"]', '{"created_by":"x","z":1}', '{"z":1,"created_by":null,"a":2}', '{"2":"b","1":"a","name":"n"}',
        '{"__proto__":{"x":1},"constructor":2,"prototype":3,"keep":{"constructor":4}}', '{"n":-0.0,"big":12345678901234567890,"t":"\\u00e9 <b>"}', ' {"spaced" : true } '];
    const nameless = { userId: 9, user: { id: 9, username: '', role: 'admin' } };
    for (const b of bodies) {
        for (const p of ['/api/panel/fleet/tasks', '/api/panel/fleet/workflows', '/api/panel/fleet/resources/acme', '/api/bd/api/panel/fleet/workflows/wf-1/execute']) {
            await send({ method: 'POST', path: p, session: accounts.global_admin, type: json, body: b }, ok);
        }
    }
    for (const p of ['/api/panel/fleet/tasks', '/api/panel/fleet/workflows']) {
        await send({ method: 'POST', path: p, session: nameless, type: json, body: '{"name":"n"}' }, ok);
        await send({ method: 'POST', path: p, session: accounts.admin }, ok);
        await send({ method: 'POST', path: p, session: accounts.admin, type: 'application/x-www-form-urlencoded', body: 'name=n&created_by=form' }, ok);
    }

    // Other spellings Express takes for the same route
    await send({ path: '/API/Panel/Fleet/Tasks/', session: accounts.admin }, ok);
    await send({ method: 'HEAD', path: '/api/panel/fleet/workflows', session: accounts.admin }, ok);
    await send({ method: 'DELETE', path: '/api/panel/fleet/tasks/Task-1/', session: accounts.admin }, ok);

    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
