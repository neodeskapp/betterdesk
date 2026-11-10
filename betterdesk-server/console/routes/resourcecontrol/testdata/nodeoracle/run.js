'use strict';

// Vectors for the resource control routes:
// routes/resource-control.routes.js of the Node console, mounted at / and
// at /api/bd as routes/index.js mounts it, with the real guards and the
// real API client in front of a scripted API server. Writes
// ../vectors.json.

const path = require('path');
const fs = require('fs');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true,"usb":{"mode":"block"}}' };

const routes = [
    ['GET', '/api/panel/resource-control/devices'],
    ['GET', '/api/panel/resource-control/policies/900100001'],
    ['POST', '/api/panel/resource-control/policies/900100001/usb', true],
    ['POST', '/api/panel/resource-control/policies/900100001/optical', true],
    ['POST', '/api/panel/resource-control/policies/900100001/monitors', true],
    ['POST', '/api/panel/resource-control/policies/900100001/disks', true],
    ['POST', '/api/panel/resource-control/policies/900100001/quotas', true],
    ['GET', '/api/panel/resource-control/compliance'],
    ['GET', '/api/panel/resource-control/org-policy'],
    ['PUT', '/api/panel/resource-control/org-policy', true],
];

async function main() {
    const oracle = await start((app, load) => {
        const router = load('./routes/resource-control.routes');
        app.use('/', router);
        app.use('/api/bd', router);
    });
    const vectors = [];
    const send = async (request, answer) => vectors.push(await oracle.call(request, answer));
    const body = '{"mode":"allowlist","allowed":[{"vendor":"046d","product":"c52b"}],"notify":true}';

    // Who may call what, at both mounts
    for (const [method, p, sends] of routes) {
        for (const mount of ['', '/api/bd']) {
            for (const session of [undefined, accounts.pro, accounts.viewer, accounts.operator, accounts.global_admin, accounts.server_admin, accounts.super_admin, accounts.admin]) {
                const request = { method, path: mount + p, session };
                if (sends) Object.assign(request, { type: json, body });
                await send(request, ok);
            }
        }
    }

    // Every kind of answer of the API server
    for (const [method, p, sends] of routes) {
        for (const answer of apiAnswers) {
            const request = { method, path: p, session: accounts.server_admin };
            if (sends) Object.assign(request, { type: json, body: '{"a":1}' });
            await send(request, answer);
        }
    }

    // Device ids that may and may not go into a path
    const ids = ['a.b', 'a..b', 'a:b@c', 'a%25b', 'a+b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', 'a*b', 'A-Z_0.9', 'resources',
        'a'.repeat(256), 'a'.repeat(257), '%C3%A9', 'a%00', '%09abc%0A'];
    for (const [method, p, sends] of routes) {
        if (!p.includes('900100001')) continue;
        for (const id of ids) {
            const request = { method, path: p.replace('900100001', id), session: accounts.admin };
            if (sends) Object.assign(request, { type: json, body: '{}' });
            await send(request, ok);
        }
    }
    await send({ method: 'POST', path: '/api/panel/resource-control/policies/900100001/unknown', session: accounts.admin, type: json, body: '{}' }, ok);
    await send({ method: 'PUT', path: '/api/panel/resource-control/policies/900100001/usb', session: accounts.admin, type: json, body: '{}' }, ok);

    // Bodies of a write
    const bodies = ['{}', '[]', '["a","b"]', '{"2":"b","1":"a","name":"n"}', '{"__proto__":{"x":1},"constructor":2,"prototype":3,"keep":{"constructor":4}}',
        '{"n":-0.0,"big":12345678901234567890,"t":"\\u00e9 <b>"}', ' {"spaced" : true } '];
    for (const b of bodies) {
        await send({ method: 'POST', path: '/api/panel/resource-control/policies/900100001/usb', session: accounts.admin, type: json, body: b }, ok);
        await send({ method: 'PUT', path: '/api/bd/api/panel/resource-control/org-policy', session: accounts.admin, type: json, body: b }, ok);
    }
    await send({ method: 'POST', path: '/api/panel/resource-control/policies/900100001/quotas', session: accounts.admin }, ok);
    await send({ method: 'PUT', path: '/api/panel/resource-control/org-policy', session: accounts.admin, type: 'application/x-www-form-urlencoded', body: 'usb=block&usb=audit' }, ok);
    await send({ method: 'GET', path: '/api/panel/resource-control/org-policy', session: accounts.admin, type: json, body: '{"ignored":true}' }, ok);

    // What the API server may send as its device list
    const peer = '{"id":"900100001","hostname":"pc-1","platform":"Windows","status":"offline","live_status":"online","device_type":"desktop","tags":"a,b","extra":1}';
    const lists = [
        `[${peer}]`, `{"data":[${peer}]}`, `{"peers":[${peer}]}`, `{"data":[${peer}],"peers":[]}`, `{"data":[],"peers":[${peer}]}`, `{"data":null,"peers":[${peer}]}`,
        `{"data":{"not":"a list"},"peers":[${peer}]}`, `{"data":"text","peers":[${peer}]}`, `{"data":0,"peers":0}`, '{}', '[]', 'null', '""', '"text"', '17', 'true',
        '{"total":0}', '[{}]', '[{"id":null}]', '[{"id":0,"hostname":0,"platform":false,"status":"","live_status":"","device_type":null,"tags":[]}]',
        '[{"id":17,"hostname":17,"platform":{"os":"x"},"status":"offline","device_type":true,"tags":["a","b"]}]', '[{"status":"online"}]', '[{"live_status":"degraded","status":"online"}]',
        '[{"id":"a"},null,{"id":"b"}]', '[null]', '[1,"two",true,[3],{"id":"x"}]', '[{"id":-0.0,"hostname":-0.0,"tags":{"__proto__":1}}]',
        '[{"hostname":"h","id":"late","tags":"t","device_type":"d","status":"s","platform":"p"}]', '{"data":[{"id":"<b>&\\u00e9"}]}',
        `{"data":0,"peers":[${peer}]}`, `{"data":"","peers":[${peer}]}`, `{"data":false,"peers":[${peer}]}`, '[{"live_status":"","status":"online"}]',
        '[{"live_status":0,"status":"up"},{"live_status":null,"status":"down"},{"live_status":false}]', '[{"id":"a\\u2028b","hostname":"x\\u2029"}]',
        'not json', '',
    ];
    for (const list of lists) {
        await send({ path: '/api/panel/resource-control/devices', session: accounts.admin }, { status: 200, type: json, body: list });
    }
    await send({ path: '/api/panel/resource-control/devices?limit=5&x=1', session: accounts.admin }, { status: 200, type: json, body: `[${peer}]` });
    await send({ path: '/api/panel/resource-control/compliance', session: accounts.admin }, { status: 201, type: json, body: '{"total_devices":3,"compliance_rate":66.7}' });

    // Other spellings Express takes for the same route
    await send({ path: '/API/Panel/Resource-Control/Devices/', session: accounts.admin }, ok);
    await send({ method: 'HEAD', path: '/api/panel/resource-control/org-policy', session: accounts.admin }, ok);

    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
