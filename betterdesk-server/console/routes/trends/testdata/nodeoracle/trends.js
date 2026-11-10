// Writes ../trends.json: what GET /api/reports/trends of the Node console
// answers for made-up answers of the API client, for the Go tests of
// package trends to replay.
//
//   cd /repo/web-nodejs && node ../betterdesk-server/console/routes/trends/testdata/nodeoracle/trends.js
'use strict';

const fs = require('fs');
const path = require('path');

const root = path.join('/repo', 'web-nodejs');
const { compileQueryParser } = require(path.join(root, 'node_modules', 'express', 'lib', 'utils'));
const parseQuery = compileQueryParser('extended');

function stub(rel, exports) {
    const id = require.resolve(path.join(root, rel));
    require.cache[id] = { id, filename: id, loaded: true, exports };
}

let scenario = null;
let calls = [];
stub('services/betterdeskApi.js', {
    getAllPeers: async (options) => {
        calls.push(['getAllPeers', options]);
        return scenario.peers;
    },
    getPeerConnectionSessions: async (id, options) => {
        calls.push(['getPeerConnectionSessions', id, options]);
        const answers = scenario.sessions || {};
        return Object.prototype.hasOwnProperty.call(answers, id) ? answers[id] : { success: true, data: { peer_id: id, count: 0, sessions: [] } };
    },
    listHelpRequests: async (filter) => {
        calls.push(['listHelpRequests', filter]);
        return scenario.help === undefined ? { success: true, data: [] } : scenario.help;
    },
});
stub('middleware/auth.js', {
    requireAuth: (req, res, next) => next(),
    requirePermission: () => (req, res, next) => next(),
});

const realNow = Date.now;
const routeFile = require.resolve(path.join(root, 'routes', 'trends.routes.js'));

async function run(s) {
    scenario = s;
    calls = [];
    // A fresh module has nothing kept from the scenario before
    delete require.cache[routeFile];
    const router = require(routeFile);
    const layer = router.stack.find((l) => l.route && l.route.path === '/api/reports/trends');
    const handler = layer.route.stack[layer.route.stack.length - 1].handle;
    const answer = { status: 200, body: undefined };
    const res = {
        status(code) { answer.status = code; return this; },
        json(body) { answer.body = JSON.parse(JSON.stringify(body)); return this; },
    };
    const req = { query: parseQuery(s.query || ''), t: (key) => 'T(' + key + ')' };
    Date.now = () => s.now;
    const quiet = console.error;
    console.error = () => {};
    try {
        await handler(req, res);
    } finally {
        Date.now = realNow;
        console.error = quiet;
    }
    return { ...s, answer, calls: calls.map((c) => JSON.parse(JSON.stringify(c))) };
}

const NOW = Date.UTC(2026, 10, 10, 8, 0, 0);
const peer = (id) => ({ id, hostname: 'pc-' + id });
const ok = (sessions) => ({ success: true, data: { peer_id: 'x', count: sessions.length, sessions } });

const scenarios = [];
function add(name, s) {
    scenarios.push({ name, now: NOW, ...s });
}

add('nothing at all', { peers: [] });
add('sessions of three devices', {
    peers: [peer('900100001'), peer('900100002'), peer('900100003')],
    sessions: {
        900100001: ok([
            { id: 1, host_id: '900100001', peer_id: '900100002', transport: 'relay', started_at: '2026-11-09T07:00:00Z', ended_at: '2026-11-09T07:30:00.5Z', source: 'signal', active: false },
            { id: 2, host_id: '900100001', peer_id: '900100003', started_at: '2026-11-10T07:59:59.123456789Z', source: 'signal', active: true },
            { id: 3, host_id: '900100001', peer_id: '900100003', started_at: '2026-10-01T00:00:00Z', ended_at: '2026-10-01T01:00:00Z' },
            { id: 4, started_at: '2026-11-08T10:00:00+01:00', ended_at: '2026-11-08T10:00:00-0130' },
            { id: 5, started_at: '2026-11-08 10:00:00', ended_at: '2026-11-08 11:00:00.250' },
            { id: 6, started_at: '0001-01-01T00:00:00Z' },
            { id: 7, started_at: '2026-11-08T10:00:00Z', ended_at: 'soon' },
            { id: 8, started_at: '2026-11-08T10:00:00Z', ended_at: null },
            { id: 9, started_at: '2026-11-08T10:00:00Z', ended_at: '0001-01-01T00:00:00Z' },
        ]),
        900100002: ok([
            { id: 1, host_id: '900100001', peer_id: '900100002', started_at: '2026-11-09T07:00:00Z', ended_at: '2026-11-09T08:00:00Z' },
            { id: 10, started_at: '2026-11-09T09:00:00Z' },
            { id: '10', started_at: '2026-11-09T09:30:00Z' },
            { id: 0, started_at: '2026-11-09T09:40:00Z' },
            { id: 0, started_at: '2026-11-09T09:50:00Z' },
        ]),
        900100003: ok([
            { host_id: 'a', peer_id: 'b', started_at: '2026-11-09T12:00:00Z' },
            { host_id: 'a', peer_id: 'b', started_at: '2026-11-09T12:00:00Z', ended_at: '2026-11-09T12:10:00Z' },
            { host_id: 'a', peer_id: 'c', started_at: '2026-11-09T12:00:00Z' },
            { id: null, host_id: 'a', started_at: '2026-11-09T12:00:00Z' },
            { id: null, host_id: 'a', peer_id: null, started_at: '2026-11-09T12:00:00Z' },
            { id: [1, 2], started_at: '2026-11-09T13:00:00Z' },
            { id: '1,2', started_at: '2026-11-09T13:30:00Z' },
            { id: { a: 1 }, started_at: '2026-11-09T14:00:00Z' },
            { id: { b: 2 }, started_at: '2026-11-09T14:30:00Z' },
            { id: 1.5, started_at: '2026-11-09T15:00:00Z' },
            { id: true, started_at: '2026-11-09T15:30:00Z' },
            { id: 1e21, started_at: '2026-11-09T16:00:00Z' },
        ]),
    },
});
add('the edge of the period', {
    query: 'days=1',
    peers: [peer('1')],
    sessions: {
        1: ok([
            { id: 1, started_at: '2026-11-09T08:00:00Z' },
            { id: 2, started_at: '2026-11-09T07:59:59.999Z' },
            { id: 3, started_at: '2026-11-09T08:00:00.000999Z' },
            { id: 4, started_at: '2026-11-09T09:00:00+01:00' },
            { id: 5, started_at: '2026-11-09T08:59:59+01:00' },
        ]),
    },
    help: {
        success: true,
        data: [
            { id: 1, status: 'open', created_at: '2026-11-09T08:00:00Z', updated_at: '2026-11-09T08:00:00Z' },
            { id: 2, status: 'resolved', created_at: '2026-11-01T08:00:00Z', updated_at: '2026-11-09T08:00:00Z' },
            { id: 3, status: 'acknowledged', created_at: '2026-11-01T08:00:00Z', updated_at: '2026-11-09T07:59:59Z' },
            { id: 4, status: 'open', created_at: '2026-11-10T07:00:00Z' },
            { id: 5, status: 'open', created_at: 'unknown', updated_at: '2026-11-10T07:00:00Z' },
            { id: 6, status: 'open', created_at: 'unknown', updated_at: 'unknown' },
            { id: 7, status: null, created_at: '2026-11-10T07:00:00Z', updated_at: 'unknown' },
            { id: 8, status: 0, created_at: '2026-11-10T07:00:00Z', updated_at: '2026-11-10T07:00:00.7Z' },
            { id: 9, status: 5, created_at: '2026-11-10T07:00:00Z', updated_at: '2026-11-10 07:10:00' },
            { id: 10, status: ['a', 'b'], created_at: '2026-11-10T07:00:00Z' },
            { id: 11, status: { a: 1 }, created_at: '2026-11-10T07:00:00Z' },
            { id: 12, status: true, created_at: '2026-11-10T07:00:00Z' },
            { id: 13, created_at: '2026-11-10T07:00:00Z' },
            'text', 5, true, [1], {},
        ],
    },
});

for (const query of ['', 'days=30', 'days=0', 'days=-3', 'days=365', 'days=abc', 'days=7d', 'days=7&days=30', 'days[x]=5',
    'days[]=21', 'days=+12', 'days=%2012', 'days=1e3', 'days=2.9', 'days=90', 'days=91', 'days=1', 'days=', 'days', 'DAYS=5',
    'days=0x10', 'days=999999999999999999999999', 'days=-0', 'days=00008']) {
    add('days from ?' + query, {
        query,
        peers: [peer('1')],
        sessions: { 1: ok([{ id: 1, started_at: '2026-11-10T07:00:00Z' }, { id: 2, started_at: '2026-08-13T08:00:00Z' }, { id: 3, started_at: '2026-08-12T07:59:59Z' }]) },
    });
}

add('device ids of every kind', {
    peers: [{ id: 'a' }, { peer_id: 'b' }, { id: '', peer_id: 'c' }, { id: 0, peer_id: 0 }, { hostname: 'no id' }, { id: 77 }, { id: null, peer_id: 'd e/f' }, { id: ['x', 'y'] }, { id: true }],
    sessions: {
        a: ok([{ id: 1, started_at: '2026-11-10T01:00:00Z' }]),
        b: ok([{ id: 2, started_at: '2026-11-10T02:00:00Z' }]),
        c: ok([{ id: 3, started_at: '2026-11-10T03:00:00Z' }]),
        77: ok([{ id: 4, started_at: '2026-11-10T04:00:00Z' }]),
        'd e/f': ok([{ id: 5, started_at: '2026-11-10T05:00:00Z' }]),
    },
});
add('more devices than are asked', {
    peers: Array.from({ length: 503 }, (_, i) => peer(String(1000 + i))),
    sessions: { 1000: ok([{ id: 1, started_at: '2026-11-10T01:00:00Z' }]), 1499: ok([{ id: 2, started_at: '2026-11-10T02:00:00Z' }]), 1500: ok([{ id: 3, started_at: '2026-11-10T03:00:00Z' }]) },
});
add('no device list', { peers: null });
add('a device that is null', { peers: [peer('1'), null] });

const row = { id: 1, started_at: '2026-11-10T01:00:00Z' };
const shapes = {
    'a refusal': { success: false, error: 'Peer not found' },
    'no answer': null,
    'text as data': { success: true, data: 'Internal server error\n' },
    'a list as data': { success: true, data: [row] },
    'an empty list as data and sessions beside it': { success: true, data: [], sessions: [row] },
    'sessions without data': { sessions: [row] },
    'sessions beside empty data': { success: true, data: '', sessions: [row] },
    'sessions null': { success: true, data: { sessions: null } },
    'sessions as text': { success: true, data: { sessions: 'abc' } },
    'sessions as a number': { success: true, data: { sessions: 5 } },
    'sessions as an object': { success: true, data: { sessions: { 0: row } } },
    'sessions true': { success: true, data: { sessions: true } },
    'data as a number': { success: true, data: 5 },
    'data true': { success: true, data: true },
    'a session that is null': ok([row, null]),
    'sessions that are no objects': ok(['text', 5, true, [row], row]),
    'a list of lists': { success: true, data: [[row], row] },
};
for (const [name, answer] of Object.entries(shapes)) {
    add('sessions: ' + name, { peers: [peer('1'), peer('2')], sessions: { 1: answer, 2: ok([{ id: 2, started_at: '2026-11-10T02:00:00Z' }]) } });
}

const request = { id: 1, status: 'open', created_at: '2026-11-10T01:00:00Z', updated_at: '2026-11-10T01:00:00Z' };
const helpShapes = {
    'a refusal': { success: false, error: 'internal' },
    'no answer': null,
    'no data': { success: true },
    'data null': { success: true, data: null },
    'text as data': { success: true, data: 'internal' },
    'an object as data': { success: true, data: { requests: [request] } },
    'a number as data': { success: true, data: 5 },
    'zero as data': { success: true, data: 0 },
    'true as data': { success: true, data: true },
    'a request that is null': { success: true, data: [request, null] },
    'a list in the list': { success: true, data: [[request], request] },
};
for (const [name, answer] of Object.entries(helpShapes)) {
    add('help: ' + name, { peers: [peer('1')], sessions: { 1: ok([row]) }, help: answer });
}

// Every text below as created_at of a help request, with a clock so far
// back that each time that can be read is in the period. status says which.
const dates = [
    '2026-11-10T07:32:11Z', '2026-11-10T07:32:11.1Z', '2026-11-10T07:32:11.12Z', '2026-11-10T07:32:11.123Z',
    '2026-11-10T07:32:11.1239Z', '2026-11-10T07:32:11.123456789Z', '2026-11-10T07:32:11.999999999Z',
    '2026-11-10T07:32:11.0000000001Z', '2026-11-10T07:32:11.12345678901234Z', '2026-11-10T07:32Z', '2026-11-10T07:32:11+01:00',
    '2026-11-10T07:32:11-08:00', '2026-11-10T07:32:11+0530', '2026-11-10T07:32:11.5-0030', '2026-11-10T07:32:11+14:00',
    '2026-11-10T07:32:11+23:59', '2026-11-10T07:32:11+24:00', '2026-11-10T07:32:11+01:60', '2026-11-10T07:32:11+1',
    '2026-11-10T07:32:11+01', '2026-11-10T07:32:11+01:0', '2026-11-10 07:32:11', '2026-11-10 07:32:11.5', '2026-11-10 07:32',
    '2026-11-10', '2026-11-10T07:32:11', '2026-11-10T07:32:11.Z', '2026-11-10T07:32:60Z', '2026-11-10T07:60:00Z',
    '2026-11-10T24:00:00Z', '2026-11-10T24:00:00.000Z', '2026-11-10T24:00:01Z', '2026-11-10T24:01:00Z', '2026-11-10T25:00:00Z',
    '2026-02-29T00:00:00Z', '2026-02-30T00:00:00Z', '2026-02-31T00:00:00Z', '2026-04-31T12:00:00Z', '2026-11-32T00:00:00Z',
    '2026-11-00T00:00:00Z', '2026-00-10T00:00:00Z', '2026-13-10T00:00:00Z', '2024-02-29T23:59:59.999Z', '0001-01-01T00:00:00Z',
    '0000-01-01T00:00:00Z', '9999-12-31T23:59:59.999Z', '1969-12-31T23:59:59.999Z', '1970-01-01T00:00:00Z',
    '2026-11-10T7:32:11Z', '2026-11-10T07:3:11Z', '2026-11-10T07:32:1Z', '2026-1-10T07:32:11Z', '2026-11-1T07:32:11Z',
    '26-11-10T07:32:11Z', '2026-11-10T07:32:11', 'unknown', '', ' ', 'Z', '2026-11-10T07:32:11 Z', ' 2026-11-10T07:32:11Z',
    '2026-11-10T07:32:11Z ', '2026-11-10T07:32:11.123+01:00', '2026-11-10T07:32:11,123Z',
];
const dateRows = dates.map((text, i) => ({ id: i, status: String(i), created_at: text }));
const far = -8e15;
add('times as text', { now: far, query: 'days=1', peers: [], help: { success: true, data: dateRows }, dates });

(async () => {
    const out = [];
    for (const s of scenarios) out.push(await run(s));
    const file = path.join(__dirname, '..', 'trends.json');
    fs.writeFileSync(file, JSON.stringify(out));
    console.log(`wrote ${out.length} scenarios to ${file}`);
})();
