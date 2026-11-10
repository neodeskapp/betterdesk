// Writes ../crossplatform.json: what the cross-platform routes of the Node
// console answer for made-up answers of the API server, for the Go tests
// of package crossplatform to replay.
//
//   cd /repo/web-nodejs && node ../betterdesk-server/console/routes/crossplatform/testdata/nodeoracle/crossplatform.js
'use strict';

const fs = require('fs');
const path = require('path');

const root = path.join('/repo', 'web-nodejs');

function stub(rel, exports) {
    const id = require.resolve(path.join(root, rel));
    require.cache[id] = { id, filename: id, loaded: true, exports };
}

let scenario = null;
let calls = [];

// The API client as axios behaves: a body is read as JSON when it is
// JSON, a status outside 200 to 299 is an error that carries the answer.
const { assertSafeGoApiRelativePath } = require(path.join(root, 'lib', 'goApiPath'));

async function apiClient(options) {
    // The check the client runs on every path before it sends anything
    assertSafeGoApiRelativePath(String(options.url));
    calls.push(options.method + ' ' + options.url);
    const api = scenario.api;
    if (api.fail) throw new Error(api.fail);
    let data = api.body;
    if (typeof data === 'string' && data !== '') {
        try { data = JSON.parse(data); } catch (_) { /* text stays text */ }
    }
    if (api.status >= 200 && api.status < 300) return { status: api.status, data };
    const err = new Error('Request failed with status code ' + api.status);
    err.response = { status: api.status, data };
    throw err;
}

stub('services/betterdeskApi.js', { apiClient });
stub('middleware/auth.js', {
    requireAuth: (req, res, next) => next(),
    requirePermission: () => (req, res, next) => next(),
});

const router = require(path.join(root, 'routes', 'cross-platform.routes.js'));
const routes = {
    distribution: '/api/panel/cross-platform/distribution',
    capabilities: '/api/panel/cross-platform/capabilities/:deviceId',
    protocols: '/api/panel/cross-platform/protocols',
};

async function run(s) {
    scenario = s;
    calls = [];
    const layer = router.stack.find((l) => l.route && l.route.path === routes[s.route]);
    const handler = layer.route.stack[layer.route.stack.length - 1].handle;
    const answer = { status: 200, text: null };
    const res = {
        status(code) { answer.status = code; return this; },
        json(body) { answer.text = JSON.stringify(body); return this; },
    };
    await handler({ params: { deviceId: s.deviceId } }, res);
    return { ...s, answer, calls };
}

const scenarios = [];
function add(name, route, api, extra) {
    scenarios.push({ name, route, api, ...extra });
}
const page = (list) => ({ status: 200, body: JSON.stringify({ total: list.length, data: list }) });

const fleet = [
    { id: '1', platform: 'Windows 11 Pro', version: '1.4.2', live_online: true, status: 1 },
    { id: '2', platform: 'Ubuntu 22.04', version: '1.4.2', live_online: false, status: 1 },
    { id: '3', platform: 'Linux Mint 21', arch: 'X86_64', version: '1.4.1', live_online: true, supported_codecs: ['vp9', 'h264'], supported_features: 'file_transfer' },
    { id: '4', platform: '', os: 'macOS 14', arch: 'ARM64', version: '1.3.9', live_online: true, supported_codecs: ['h264'], supported_features: ['file_transfer', 'clipboard'] },
    { id: '5', os: 'Darwin 23', arch: 'arm64' },
    { id: '6', platform: 'Android 14', version: '10' },
    { id: '7', platform: 'iOS 17', version: '9' },
    { id: '8', platform: 'iPhone', version: 2 },
    { id: '9', platform: 'iPadOS', version: '2' },
    { id: '10', platform: 'ChromeOS', version: 1.5 },
    { id: '11', platform: 'Unknown' },
    { id: '12', platform: 'UNKNOWN', version: 0 },
    { id: '13' },
    { id: '14', platform: 'FreeBSD 14', version: true },
    { id: '15', platform: 'Kiosk display', version: [1, 2] },
    { id: '16', platform: 'debian 12', version: { major: 1 } },
    { id: '17', platform: 'Debian 12', version: '' },
    { id: '18', platform: 'Windows Server 2022 (linux subsystem)', arch: 'x86_64', version: '1.4.2' },
    { id: '19', platform: 'Macintosh', arch: 'AMD64', version: '1.4.2' },
    { id: '20', platform: 'ÄÖÜ Éire', arch: 'ΑΡΜ', version: 'Ünïcode' },
];
add('a mixed fleet', 'distribution', page(fleet));
add('a mixed fleet', 'protocols', page(fleet));
add('the list under peers', 'distribution', { status: 200, body: JSON.stringify({ peers: fleet.slice(0, 3) }) });
add('the list under peers', 'protocols', { status: 200, body: JSON.stringify({ peers: fleet.slice(0, 4) }) });
add('peers before data', 'distribution', { status: 200, body: JSON.stringify({ data: fleet.slice(0, 2), peers: fleet.slice(2, 5) }) });
add('empty peers beside data', 'distribution', { status: 200, body: JSON.stringify({ peers: [], data: fleet.slice(0, 2) }) });
add('a bare list', 'distribution', { status: 200, body: JSON.stringify(fleet) });
add('a bare list', 'protocols', { status: 200, body: JSON.stringify(fleet) });
add('no devices', 'distribution', page([]));
add('no devices', 'protocols', page([]));

const names = ['constructor', 'toString', 'valueOf', 'hasOwnProperty', 'isPrototypeOf', 'propertyIsEnumerable', 'toLocaleString',
    '__proto__', '__defineGetter__', '__defineSetter__', '__lookupGetter__', '__lookupSetter__', 'length', 'prototype', 'name'];
const named = [];
for (const name of names) {
    named.push({ platform: name, arch: name, version: name, live_online: true, supported_codecs: name, supported_features: [name, name] });
    named.push({ platform: name.toUpperCase(), arch: name, version: name, live_online: true, supported_codecs: [name], supported_features: name });
}
add('names every object has', 'distribution', page(named));
add('names every object has', 'protocols', page(named));

const numbered = ['10', '9', '1.4.2', '0', '-1', '007', '4294967294', '4294967295', 'b', 'a', '3'].map((v) => ({ platform: v, arch: v, version: v, live_online: 1, supported_codecs: [v], supported_features: v }));
add('names that are numbers', 'distribution', page(numbered));
add('names that are numbers', 'protocols', page(numbered));

for (const [name, entry] of Object.entries({
    'a platform that is a number': { platform: 5 },
    'a platform that is true': { platform: true },
    'a platform that is a list': { platform: ['Windows'] },
    'a platform that is an object': { platform: { name: 'Windows' } },
    'an os that is a number': { os: 7 },
    'an os that is a number behind a platform': { platform: 'Windows', os: 7 },
    'an arch that is a number': { platform: 'Windows', arch: 64 },
    'an arch that is a list': { arch: ['x86'] },
    'an arch that is zero': { arch: 0 },
    'a platform that is zero': { platform: 0, os: 0 },
})) {
    add('distribution: ' + name, 'distribution', page([{ platform: 'Linux' }, entry]));
}
for (const [name, list] of Object.entries({
    'an entry that is null': [{ platform: 'Linux', live_online: true }, null],
    'entries that are no objects': ['text', 5, true, [{ platform: 'Linux', live_online: true }], { platform: 'Linux', live_online: true }],
})) {
    add(name, 'distribution', page(list));
    add(name, 'protocols', page(list));
}
for (const [name, body] of Object.entries({
    'text as the list': { total: 3, data: 'abc' },
    'text with wide letters as the list': { data: 'a😀é' },
    'a number as the list': { data: 5 },
    'zero as the list': { data: 0 },
    'true as the list': { data: true },
    'an object as the list': { data: { 0: { platform: 'Linux' } } },
    'null as the list': { data: null },
    'peers as text': { peers: 'ab', data: fleet.slice(0, 1) },
    'an object without a list': { total: 0 },
})) {
    add(name, 'distribution', { status: 200, body: JSON.stringify(body) });
    add(name, 'protocols', { status: 200, body: JSON.stringify(body) });
}
for (const [name, api] of Object.entries({
    'the API server refuses with 500': { status: 500, body: '{"error":"internal"}' },
    'the API server refuses with 401': { status: 401, body: '{"error":"Unauthorized"}' },
    'the API server refuses with 404 and text': { status: 404, body: '404 page not found\n' },
    'the API server refuses without a body': { status: 503, body: '' },
    'the API server gives no answer': { fail: 'socket hang up' },
    'the API server answers text': { status: 200, body: 'not json' },
    'the API server answers nothing': { status: 200, body: '' },
    'the API server answers null': { status: 200, body: 'null' },
    'the API server answers a number': { status: 200, body: '5' },
    'the API server answers 204': { status: 204, body: '' },
})) {
    add(name, 'distribution', api);
    add(name, 'protocols', api);
}

const online = [
    { live_online: true }, { live_online: false, status: 'ONLINE' }, { live_online: false, status: 'online' }, { status: 'ONLINE' },
    { live_online: 1 }, { live_online: 'yes' }, { live_online: 0, status: 1 }, { live_online: null, status: 'OFFLINE' }, { live_online: [] }, { live_online: {} },
    { live_online: '' },
];
add('who counts as online', 'protocols', page(online));
const named2 = [
    { live_online: true, supported_codecs: 'vp9', supported_features: ['a', 'b', 'a'] },
    { live_online: true, supported_codecs: 5, supported_features: [1, 2.5, true, false, null, 'null', ['x', 'y'], { z: 1 }, ''] },
    { live_online: true, supported_codecs: { h264: true }, supported_features: [] },
    { live_online: true, supported_codecs: true, supported_features: 0 },
    { live_online: true, supported_codecs: '', supported_features: null },
    { live_online: true, supported_codecs: [[['deep']]], supported_features: [[]] },
    { live_online: false, supported_codecs: ['never counted'] },
];
add('what devices name', 'protocols', page(named2));

const peerBody = JSON.stringify({ id: '900100001', hostname: 'pc-1', os: 'Windows 11', status: 1, live_online: true, z: [1, { b: 2, a: 1 }] });
for (const [name, deviceId, api] of [
    ['a device', '900100001', { status: 200, body: peerBody }],
    ['an id with spaces around', ' 900100001 ', { status: 200, body: peerBody }],
    ['an id that needs escaping', 'a%b+c@d:e', { status: 200, body: peerBody }],
    ['an unknown device', '999999999', { status: 404, body: '{"error":"Peer not found"}' }],
    ['a refusal as text', '900100001', { status: 403, body: 'Forbidden\n' }],
    ['a refusal without a body', '900100001', { status: 502, body: '' }],
    ['a refusal with zero', '900100001', { status: 500, body: '0' }],
    ['a refusal with false', '900100001', { status: 500, body: 'false' }],
    ['a refusal with null', '900100001', { status: 500, body: 'null' }],
    ['a refusal with an empty list', '900100001', { status: 500, body: '[]' }],
    ['a refusal with an empty object', '900100001', { status: 500, body: '{}' }],
    ['no answer', '900100001', { fail: 'socket hang up' }],
    ['no answer in time', '900100001', { fail: 'timeout of 5000ms exceeded' }],
    ['an answer that is text', '900100001', { status: 200, body: 'plain' }],
    ['an answer that is empty', '900100001', { status: 200, body: '' }],
    ['an answer that is a list', '900100001', { status: 200, body: '[1,2]' }],
    ['an answer that is null', '900100001', { status: 200, body: 'null' }],
    ['an answer that is zero', '900100001', { status: 200, body: '0' }],
    ['an answer with another good status', '900100001', { status: 201, body: '{"made":true}' }],
    ['an id with a slash', 'a/b', { status: 200, body: peerBody }],
    ['an id with a space inside', 'a b', { status: 200, body: peerBody }],
    ['an id of dots', '..', { status: 200, body: peerBody }],
    ['an id of one dot', '.', { status: 200, body: peerBody }],
    ['an id of spaces', '   ', { status: 200, body: peerBody }],
    ['an id with a letter outside ASCII', 'gerät', { status: 200, body: peerBody }],
    ['an id with a question mark', 'a?b=c', { status: 200, body: peerBody }],
    ['an id with a backslash', 'a\\b', { status: 200, body: peerBody }],
    ['an id that is too long', 'x'.repeat(257), { status: 200, body: peerBody }],
    ['the longest id', 'x'.repeat(256), { status: 200, body: peerBody }],
    ['an id with two dots inside', 'a..b', { status: 200, body: peerBody }],
]) {
    add('capabilities: ' + name, 'capabilities', api, { deviceId });
}

(async () => {
    const out = [];
    for (const s of scenarios) out.push(await run(s));
    const file = path.join(__dirname, '..', 'crossplatform.json');
    fs.writeFileSync(file, JSON.stringify(out));
    console.log(`wrote ${out.length} scenarios to ${file}`);
})();
