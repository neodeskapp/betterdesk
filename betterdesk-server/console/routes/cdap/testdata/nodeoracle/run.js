'use strict';

// Vectors for the device gateway routes: routes/cdap.routes.js of the Node
// console with the real guards, the real API client in front of a scripted
// API server, and the real restart coordinator on a scratch data folder.
// Writes ../vectors.json.
//
// A vector whose method is STATE is no request: it puts its body in place
// of the restart transaction file (no body removes the file). The Go test
// does the same to its own file.

const path = require('path');
const fs = require('fs');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));
const { random } = require(path.join(oracleDir, 'lib'));

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true,"items":[]}' };
const device = '/api/cdap/devices/dev-1';

// Every route: method, path with a good id, and a body that passes its
// checks.
const routes = [
    ['GET', '/api/cdap/status'],
    ['GET', '/api/cdap/devices'],
    ['GET', device],
    ['GET', `${device}/manifest`],
    ['GET', `${device}/state`],
    ['POST', `${device}/command`, '{"widget_id":"power","action":"set","value":true,"reason":"test"}'],
    ['POST', '/api/cdap/toggle', '{"enabled":true}'],
    ['GET', '/api/cdap/alerts'],
    ['GET', `${device}/linked`],
    ['POST', `${device}/link`, '{"linked_peer_id":"900100002"}'],
];

async function main() {
    let statePath;
    const oracle = await start((app, load) => {
        // req.t without the language files gives the key, as kit.T does in
        // a Go test without them
        app.use((req, _res, next) => {
            req.t = (key) => key;
            next();
        });
        app.use('/', load('./routes/cdap.routes'));
        statePath = load('./services/restartCoordinator').STATE_PATH;
    });
    const vectors = [];
    // A vector of the gateway switch also holds the transaction file as the
    // request left it.
    const send = async (request, answer) => {
        const vector = await oracle.call(request, answer);
        if (request.path.toLowerCase().startsWith('/api/cdap/toggle')) {
            vector.state = fs.existsSync(statePath) ? fs.readFileSync(statePath, 'utf8') : null;
        }
        vectors.push(vector);
    };
    const state = (content) => {
        if (content === undefined) fs.rmSync(statePath, { force: true });
        else fs.writeFileSync(statePath, content);
        const vector = { request: { method: 'STATE', path: '/' }, status: 0, type: '', calls: [] };
        if (content !== undefined) vector.request.body = content;
        vectors.push(vector);
    };
    const everyone = [undefined, accounts.pro, accounts.viewer, accounts.operator, accounts.global_admin, accounts.server_admin, accounts.super_admin, accounts.admin];

    // Who may call what
    for (const [method, p, body] of routes) {
        for (const session of everyone) {
            state();
            const request = { method, path: p, session };
            if (body) Object.assign(request, { type: json, body });
            await send(request, ok);
        }
    }

    // Every kind of answer of the API server
    const answers = apiAnswers.concat([
        { status: 200, type: json, body: '{"error":"gateway is off"}' },
        { status: 200, type: json, body: '{"error":""}' },
        { status: 200, type: json, body: '{"error":null,"devices":[]}' },
        { status: 200, type: json, body: '{"success":false,"data":{"x":1}}' },
        { status: 200, type: json, body: 'false' },
        { status: 200, type: json, body: '0' },
        { status: 200, type: json, body: '""' },
        { status: 200, type: json, body: '{"devices":[{"id":"a\\u2028b","n":-0}],"total":1}' },
        { status: 404, type: json, body: '{"error":"device not found"}' },
    ]);
    for (const [method, p, body] of routes) {
        for (const answer of answers) {
            state();
            const request = { method, path: p, session: accounts.admin };
            if (body) Object.assign(request, { type: json, body });
            await send(request, answer);
        }
    }

    // Device ids that may and may not go into a path
    const ids = ['a.b', 'a..b', 'a:b@c', 'a%25b', 'a+b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', 'a*b', 'A-Z_0.9', '900100001',
        'a'.repeat(256), 'a'.repeat(300), '%C3%A9', 'a%00', '%09abc%0A', 'a%5Cb', 'a%3Fb%23c', "a'b(c)!~", '%E4%B8%96%E7%95%8C'];
    for (const [method, p, body] of routes) {
        if (!p.includes('dev-1')) continue;
        for (const id of ids) {
            const request = { method, path: p.replace('dev-1', id), session: accounts.admin };
            if (body) Object.assign(request, { type: json, body });
            await send(request, ok);
        }
    }

    // The query of the alerts route
    const queries = ['device_id=dev-1', 'device_id=', 'device_id', 'device_id=a&device_id=b', 'device_id[]=a', 'device_id[]=a&device_id[]=b', 'device_id[]=', 'device_id[x]=1',
        'device_id[x][y]=1', 'device_id[toString]=1', 'device_id[0]=a&device_id[2]=c', 'device_id=a%20b%26c', 'device_id=%E4%B8%96', 'device_id=%zz', 'device_id=0', 'device_id=false',
        'other=1', 'DEVICE_ID=a', 'device_id=a#frag', 'device_id=a&device_id[x]=b', 'device_id[x]=b&device_id=a', 'device_id[][]=a', 'device_id[]=a&device_id[x]=1',
        'device_id=' + 'd'.repeat(300), 'device_id[a]=&device_id[b]', 'device_id=+a+', 'device_id[1]=b&device_id[0]=a', 'device_id[__proto__]=x', 'device_id[length]=3'];
    const next = random(23);
    const tokens = ['device_id', 'device_id', 'device_id', '[]', '[0]', '[x]', '[toString]', '=', '=', '&', '&', 'a', 'b%2Cc', 'x', '%5B', '%5D', '%20', '', ',', '.', '1', '+'];
    for (let i = 0; i < 600; i++) {
        let q = '';
        const n = 1 + next(9);
        for (let j = 0; j < n; j++) q += tokens[next(tokens.length)];
        queries.push(q);
    }
    for (const q of queries) await send({ path: `/api/cdap/alerts?${q}`, session: accounts.viewer }, ok);

    // Bodies of a command
    const commands = ['{}', '[]', '["a","b"]', '{"widget_id":"w"}', '{"action":"a"}', '{"widget_id":"","action":"a"}', '{"widget_id":"w","action":""}', '{"widget_id":0,"action":"a"}',
        '{"widget_id":"w","action":0}', '{"widget_id":null,"action":"a"}', '{"widget_id":false,"action":"a"}', '{"widget_id":"w","action":"a"}', '{"widget_id":"w","action":"a","value":null}',
        '{"widget_id":"w","action":"a","value":0,"reason":""}', '{"widget_id":"w","action":"a","value":false,"reason":null}', '{"widget_id":"w","action":"a","value":"","reason":0}',
        '{"widget_id":"w","action":"a","value":{"b":[1,2,{"c":null}],"a":1.50},"reason":["r"]}', '{"widget_id":5,"action":["a"],"value":-0.0,"reason":{"x":1}}',
        '{"widget_id":"w","action":"a","value":12345678901234567890,"reason":true}', '{"reason":"first","value":1,"action":"a","widget_id":"w","extra":"dropped"}',
        '{"widget_id":"w","action":"a","value":"\\u00e9 <b>&","reason":"\\u2028"}', '{"widget_id":[],"action":{}}', '{"widget_id":"w","widget_id":"second","action":"a"}',
        '{"widget_id":"w","action":"a","__proto__":{"value":1}}', ' {"widget_id" : "w", "action" : "a" } ', '{"widget_id":"0","action":"false"}', '{"widget_id":"w","action":"a","value":1e21,"reason":" "}',
        '{"widget_id":"w","action":"a","value":{"constructor":{"x":1},"prototype":2,"__proto__":3,"keep":{"constructor":4,"list":[{"prototype":5}]}},"reason":{"constructor":1}}',
        '{"widget_id":{"__proto__":1,"a":2},"action":{"constructor":1},"value":[{"constructor":1}]}'];
    for (const body of commands) {
        await send({ method: 'POST', path: `${device}/command`, session: accounts.operator, type: json, body }, ok);
    }
    await send({ method: 'POST', path: `${device}/command`, session: accounts.operator }, ok);
    await send({ method: 'POST', path: `${device}/command`, session: accounts.operator, type: 'application/x-www-form-urlencoded', body: 'widget_id=w&action=a&value=1&reason=form' }, ok);
    await send({ method: 'POST', path: `${device}/command`, session: accounts.operator, type: 'application/x-www-form-urlencoded', body: 'widget_id=w&widget_id=v&action=a' }, ok);
    await send({ method: 'POST', path: `${device}/command`, session: accounts.operator, type: 'text/plain', body: '{"widget_id":"w","action":"a"}' }, ok);

    // Bodies of a link
    const links = ['{}', '[]', '{"linked_peer_id":"900100002"}', '{"linked_peer_id":""}', '{"linked_peer_id":null}', '{"linked_peer_id":0}', '{"linked_peer_id":false}', '{"linked_peer_id":5}',
        '{"linked_peer_id":["a",1]}', '{"linked_peer_id":{"id":"x"}}', '{"peer":"900100002"}', '{"linked_peer_id":"a","linked_peer_id":"b"}', '{"LINKED_PEER_ID":"x"}', '{"linked_peer_id":"\\u00e9 <b>"}'];
    for (const body of links) {
        await send({ method: 'POST', path: `${device}/link`, session: accounts.operator, type: json, body }, ok);
        await send({ method: 'POST', path: `${device}/link`, session: accounts.operator, type: json, body }, { status: 400, type: json, body: '{"error":"no valid fields to update"}' });
    }
    await send({ method: 'POST', path: `${device}/link`, session: accounts.operator }, ok);
    await send({ method: 'POST', path: `${device}/link`, session: accounts.operator, type: 'application/x-www-form-urlencoded', body: 'linked_peer_id=900100002' }, ok);
    await send({ method: 'POST', path: `${device}/link`, session: accounts.operator, type: 'application/x-www-form-urlencoded', body: 'linked_peer_id=' }, ok);

    // The gateway switch: what counts as a boolean
    const toggles = ['{}', '[]', '{"enabled":true}', '{"enabled":false}', '{"enabled":"true"}', '{"enabled":1}', '{"enabled":0}', '{"enabled":null}', '{"enabled":[true]}', '{"enabled":{}}',
        '{"enabled":"Y"}', '{"ENABLED":true}', '{"enabled":true,"enabled":"x"}', '{"enabled":"x","enabled":false}', ' { "enabled" : false , "more" : 1 } '];
    for (const body of toggles) {
        state();
        await send({ method: 'POST', path: '/api/cdap/toggle', session: accounts.admin, type: json, body }, { status: 200, type: json, body: '{"key":"cdap_enabled","value":"Y"}' });
    }
    state();
    await send({ method: 'POST', path: '/api/cdap/toggle', session: accounts.admin }, ok);
    await send({ method: 'POST', path: '/api/cdap/toggle', session: accounts.admin, type: 'application/x-www-form-urlencoded', body: 'enabled=true' }, ok);

    // The gateway switch: the value to return to, from what the API server
    // says about the setting
    const settings = ['{"key":"cdap_enabled","value":"Y"}', '{"key":"cdap_enabled","value":"N"}', '{"key":"cdap_enabled","value":""}', '{"key":"cdap_enabled","value":null}', '{"key":"cdap_enabled"}', '{}',
        '{"value":0}', '{"value":false}', '{"value":{"nested":[1]}}', '{"value":["a"]}', '"text"', '17', '0', 'null', 'false', '""', '[]', '["Y"]', '{"error":"no such key"}', '{"error":""}', '{"error":null}',
        '{"error":{"code":5}}', '{"error":0,"value":"Y"}', '{"value":"\\u2028 <b>"}', '{"value":-0.0}'];
    for (const body of settings) {
        for (const enabled of [true, false]) {
            state();
            await send({ method: 'POST', path: '/api/cdap/toggle', session: accounts.server_admin, type: json, body: JSON.stringify({ enabled }) }, { status: 200, type: json, body });
        }
    }
    for (const answer of [{ status: 200, type: 'text/plain', body: 'Y' }, { status: 204 }, { status: 404, type: json, body: '{"error":"not found"}' }, { status: 500, type: json, body: '{"error":""}' },
        { status: 500, type: json, body: '{"message":"no error key"}' }, { status: 503 }, { hang: true }]) {
        state();
        await send({ method: 'POST', path: '/api/cdap/toggle', session: accounts.admin, type: json, body: '{"enabled":true}' }, answer);
    }

    // The gateway switch and the restart transaction: twice by one
    // operator, then by another while the first one's is open, then over
    // transactions that are past pending or were edited by hand
    const setting = { status: 200, type: json, body: '{"key":"cdap_enabled","value":"N"}' };
    const toggle = (session, enabled) => send({ method: 'POST', path: '/api/cdap/toggle', session, type: json, body: JSON.stringify({ enabled }) }, setting);
    state();
    await toggle(accounts.admin, true);
    await toggle(accounts.admin, false);
    await toggle(accounts.server_admin, true);
    await toggle(accounts.super_admin, false);
    await toggle(accounts.admin, true);
    const transaction = (fields) => JSON.stringify({
        id: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', userId: '1', phase: 'pending', createdAt: '2025-12-31T23:00:00.000Z', updatedAt: '2025-12-31T23:00:01.000Z',
        changes: [{ key: 'connection-mode', label: 'Connection mode', rollback: { type: 'connection-mode', settings: {} } }], ...fields,
    }, null, 2);
    const many = Array.from({ length: 32 }, (_, i) => ({ key: `k${i}`, label: `L${i}`, rollback: { type: 'go-config', key: `k${i}`, value: 'v' } }));
    const files = [transaction({}), transaction({ userId: '3' }), transaction({ userId: 1 }), transaction({ phase: 'failed', userId: '3', error: 'rollback_failed' }), transaction({ phase: 'restarting', token: 't' }),
        transaction({ phase: 'ready' }), transaction({ changes: many }), transaction({ changes: many.slice(0, 31) }),
        transaction({ changes: [{ key: 'cdap-enabled', label: 'Old label', rollback: { type: 'go-config', key: 'cdap_enabled', value: 'first' } }] }),
        transaction({ changes: null }), transaction({ changes: 'text' }), transaction({ changes: [null] }), transaction({ changes: [5, { key: 'cdap-enabled' }] }), transaction({ id: 5 }),
        '', 'null', '[]', '{}', 'not json', '{"phase":"pending"}', '{"phase":"pending","userId":"1"}'];
    for (const content of files) {
        for (const session of [accounts.admin, accounts.server_admin]) {
            state(content);
            await toggle(session, true);
            await toggle(session, false);
        }
    }
    state();

    // Other spellings Express takes for the same route
    await send({ path: '/API/Cdap/Status/', session: accounts.admin }, ok);
    await send({ method: 'HEAD', path: '/api/cdap/devices', session: accounts.admin }, ok);
    await send({ method: 'POST', path: '/api/cdap/Devices/Dev-1/Command/', session: accounts.admin, type: json, body: '{"widget_id":"w","action":"a"}' }, ok);

    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
