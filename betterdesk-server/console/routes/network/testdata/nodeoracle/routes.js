'use strict';

// Runs the steps below through the Node console's network routes on a new
// database and writes what they answered to ../routes.<database>.json.
// See README.md.
//
//   node routes.js sqlite
//   node routes.js postgres      (needs BETTERDESK_TEST_POSTGRES_DSN)

const fs = require('fs');
const os = require('os');
const path = require('path');
const http = require('http');

const database = process.argv[2] || 'sqlite';
const consoleDir = process.argv[3] || '/repo/web-nodejs';
const { table, install } = require('../../../../netmonitor/testdata/nodeoracle/lib');

const users = {
    root: { id: 1, username: 'root', role: 'super_admin' },
    adm: { id: 2, username: 'adm', role: 'admin' },
    sara: { id: 3, username: 'sara', role: 'server_admin' },
    gabi: { id: 4, username: 'gabi', role: 'global_admin' },
    olga: { id: 5, username: 'olga', role: 'operator' },
    vera: { id: 6, username: 'vera', role: 'viewer' },
    gus: { id: 7, username: 'gus', role: 'guest_issuer' },
    paul: { id: 8, username: 'paul', role: 'pro' },
};

const T = '/api/network/targets';
const steps = [];
const step = (name, as, method, p, body, extra = {}) => steps.push({ name, as, method, path: p, ...(body === undefined ? {} : { body }), ...extra });
const raw = (name, as, method, p, text, type = 'application/json') => steps.push({ name, as, method, path: p, raw: text, type });

// ---- who may do what --------------------------------------------------------
const everyone = [null, 'paul', 'gus', 'vera', 'olga', 'gabi', 'sara', 'adm', 'root'];
for (const who of everyone) {
    const n = who || 'signed out';
    step(`${n} lists`, who, 'GET', T);
    step(`${n} reads one`, who, 'GET', `${T}/999`);
    step(`${n} reads history`, who, 'GET', `${T}/999/history`);
    step(`${n} reads stats`, who, 'GET', '/api/network/stats');
    step(`${n} reads the monitor`, who, 'GET', '/api/network/monitor/status');
    step(`${n} creates`, who, 'POST', T, {});
    step(`${n} changes`, who, 'PATCH', `${T}/999`, { name: 'x' });
    step(`${n} deletes`, who, 'DELETE', `${T}/999`);
    step(`${n} checks`, who, 'POST', `${T}/999/check`, {});
    step(`${n} pings`, who, 'POST', '/api/network/ping', {});
    step(`${n} checks a port`, who, 'POST', '/api/network/tcp', {});
    step(`${n} checks a URL`, who, 'POST', '/api/network/http', {});
    step(`${n} resolves`, who, 'POST', '/api/network/resolve', {});
    step(`${n} starts the monitor`, who, 'POST', '/api/network/monitor/start', {});
    step(`${n} stops the monitor`, who, 'POST', '/api/network/monitor/stop', {});
}

// ---- the monitor, with nothing to check ----------------------------------------
step('monitor status at first', 'vera', 'GET', '/api/network/monitor/status');
step('monitor start', 'adm', 'POST', '/api/network/monitor/start', {});
step('monitor status running', 'vera', 'GET', '/api/network/monitor/status');
step('monitor start again with an interval', 'adm', 'POST', '/api/network/monitor/start', { interval_ms: 5000 });
step('monitor stop', 'sara', 'POST', '/api/network/monitor/stop', {});
step('monitor stop again', 'sara', 'POST', '/api/network/monitor/stop', {});
for (const interval of [120000, '90000', '45000ms', ' 30000 ', 1500.9, '1e5', 'soon', 0, '0', -5, null, true, [70000], ['80000', 1], {}, 2147483648, 1e21, 1e-7]) {
    step(`monitor start with interval ${JSON.stringify(interval)}`, 'adm', 'POST', '/api/network/monitor/start', { interval_ms: interval });
    step(`monitor status with interval ${JSON.stringify(interval)}`, 'vera', 'GET', '/api/network/monitor/status');
    step(`monitor stop after interval ${JSON.stringify(interval)}`, 'adm', 'POST', '/api/network/monitor/stop', {});
}
raw('monitor start with a list body', 'adm', 'POST', '/api/network/monitor/start', '[1,2]');
step('monitor stop after a list body', 'adm', 'POST', '/api/network/monitor/stop', {});
step('monitor start without a body', 'adm', 'POST', '/api/network/monitor/start');
step('monitor stop at last', 'adm', 'POST', '/api/network/monitor/stop', {});

// ---- creating targets ------------------------------------------------------------
step('create tcp', 'adm', 'POST', T, { name: 'B tcp', host: '0.0.0.0', port: 9, check_type: 'tcp', timeout_ms: 3000, interval_ms: 30000 });
step('create http', 'sara', 'POST', T, { name: 'A http', url: 'http://0.0.0.0:9/health', check_type: 'http', enabled: true });
step('create ping', 'root', 'POST', T, { name: 'C ping', host: '0.0.0.0', check_type: 'ping', enabled: false });
step('create https with host and url', 'adm', 'POST', T, { name: 'D https', host: 'zero.test', url: 'https://0.0.0.0:9/', port: '9', check_type: 'https' });
step('create with keys in another order', 'adm', 'POST', T, { enabled: 0, interval_ms: '7000', timeout_ms: '700', check_type: 'ping', url: '', port: 0, host: 'loop.test', name: 'E order' });
for (const [what, body] of Object.entries({
    'no body members': {},
    'no name': { check_type: 'ping', host: 'h' },
    'empty name': { name: '', check_type: 'ping', host: 'h' },
    'name zero': { name: 0, check_type: 'ping', host: 'h' },
    'name null': { name: null, check_type: 'ping', host: 'h' },
    'name false': { name: false, check_type: 'ping', host: 'h' },
    'no type': { name: 'x', host: 'h' },
    'empty type': { name: 'x', check_type: '', host: 'h' },
    'type in capitals': { name: 'x', check_type: 'PING', host: 'h' },
    'unknown type': { name: 'x', check_type: 'icmp', host: 'h' },
    'type as a list': { name: 'x', check_type: ['ping'], host: 'h' },
    'type as a number': { name: 'x', check_type: 1, host: 'h' },
    'type true': { name: 'x', check_type: true, host: 'h' },
    'type as an object': { name: 'x', check_type: { toString: 'ping' }, host: 'h' },
    'no host and no url': { name: 'x', check_type: 'ping' },
    'empty host and url': { name: 'x', check_type: 'ping', host: '', url: '' },
    'host zero and url null': { name: 'x', check_type: 'ping', host: 0, url: null },
    'unknown type and no host': { name: 'x', check_type: 'icmp' },
    'no name and unknown type': { check_type: 'icmp' },
})) step(`create refused: ${what}`, 'adm', 'POST', T, body);
raw('create refused: a list body', 'adm', 'POST', T, '[{"name":"x","check_type":"ping","host":"h"}]');
step('create refused: no body', 'adm', 'POST', T);
raw('create as a form', 'adm', 'POST', T, 'name=F+form&check_type=ping&host=0.0.0.0&port=22&enabled=false', 'application/x-www-form-urlencoded');
raw('create as a form with two hosts', 'adm', 'POST', T, 'name=G+form&check_type=ping&host=a&host=b', 'application/x-www-form-urlencoded');
raw('create as text', 'adm', 'POST', T, '{"name":"x","check_type":"ping","host":"h"}', 'text/plain');

const odd = {
    'name as a number': { name: 42, host: 'h', check_type: 'ping' },
    'name as a fraction': { name: 1.5, host: 'h', check_type: 'ping' },
    'name as a large number': { name: 5000000000, host: 'h', check_type: 'ping' },
    'name as 1e21': { name: 1e21, host: 'h', check_type: 'ping' },
    'name as a small number': { name: 1e-7, host: 'h', check_type: 'ping' },
    'name as minus zero': { name: -1, host: 'h', check_type: 'ping' },
    'name true': { name: true, host: 'h', check_type: 'ping' },
    'name as a list of one': { name: ['listed'], host: 'h', check_type: 'ping' },
    'name as a list of two': { name: ['a', 'b'], host: 'h', check_type: 'ping' },
    'name as an empty list': { name: [], host: 'h', check_type: 'ping' },
    'name as a list with a number': { name: [7], host: 'h', check_type: 'ping' },
    'name as a list with null': { name: [null], host: 'h', check_type: 'ping' },
    'name as a list with true': { name: [true], host: 'h', check_type: 'ping' },
    'name as a list in a list': { name: [['deep']], host: 'h', check_type: 'ping' },
    'name as a list with an object': { name: [{ a: 1 }], host: 'h', check_type: 'ping' },
    'name as a list with quotes': { name: ['say "hi"', 'back\\slash', null, 3, ['in', 'ner'], { k: 'v' }, true], host: 'h', check_type: 'ping' },
    'name as an object': { name: { b: 1, a: 'x', 10: null, 2: [1.50] }, host: 'h', check_type: 'ping' },
    'name as an empty object': { name: {}, host: 'h', check_type: 'ping' },
    'name and host as objects': { name: { a: 1 }, host: { b: 2 }, check_type: 'ping' },
    'host as a number': { name: 'x', host: 7, check_type: 'ping' },
    'host true': { name: 'x', host: true, check_type: 'ping' },
    'host as a list of one': { name: 'x', host: ['0.0.0.0'], check_type: 'ping' },
    'host as an empty list': { name: 'x', host: [], check_type: 'ping' },
    'host as a list of two': { name: 'x', host: ['a', 'b'], check_type: 'ping' },
    'host as an object': { name: 'x', host: { a: 1 }, check_type: 'ping' },
    'url as a number': { name: 'x', url: 5, check_type: 'http' },
    'url true': { name: 'x', url: true, check_type: 'http' },
    'url as a list': { name: 'x', url: ['http://0.0.0.0:9/'], check_type: 'http' },
    'url as an object': { name: 'x', url: { href: 'x' }, check_type: 'http' },
    'port as text': { name: 'x', host: 'h', port: '8080', check_type: 'tcp' },
    'port with letters': { name: 'x', host: 'h', port: '80abc', check_type: 'tcp' },
    'port not a number': { name: 'x', host: 'h', port: 'http', check_type: 'tcp' },
    'port with a fraction': { name: 'x', host: 'h', port: 80.9, check_type: 'tcp' },
    'port below zero': { name: 'x', host: 'h', port: -80, check_type: 'tcp' },
    'port true': { name: 'x', host: 'h', port: true, check_type: 'tcp' },
    'port as a list': { name: 'x', host: 'h', port: [443, 80], check_type: 'tcp' },
    'port as an object': { name: 'x', host: 'h', port: { a: 1 }, check_type: 'tcp' },
    'port large': { name: 'x', host: 'h', port: 5000000000, check_type: 'tcp' },
    'port 1e21': { name: 'x', host: 'h', port: 1e21, check_type: 'tcp' },
    'port of many digits': { name: 'x', host: 'h', port: '99999999999999999999999', check_type: 'tcp' },
    'port with spaces': { name: 'x', host: 'h', port: '  22  ', check_type: 'tcp' },
    'port in hex': { name: 'x', host: 'h', port: '0x50', check_type: 'tcp' },
    'timeouts as text': { name: 'x', host: 'h', check_type: 'ping', timeout_ms: '2500ms', interval_ms: ' 9000' },
    'timeouts zero': { name: 'x', host: 'h', check_type: 'ping', timeout_ms: 0, interval_ms: 0 },
    'timeouts below zero': { name: 'x', host: 'h', check_type: 'ping', timeout_ms: -1, interval_ms: -60000 },
    'timeouts null': { name: 'x', host: 'h', check_type: 'ping', timeout_ms: null, interval_ms: null },
    'timeouts large': { name: 'x', host: 'h', check_type: 'ping', timeout_ms: 5000000000, interval_ms: 1e21 },
    'timeouts with fractions': { name: 'x', host: 'h', check_type: 'ping', timeout_ms: 0.9, interval_ms: 1999.99 },
    'enabled false': { name: 'x', host: 'h', check_type: 'ping', enabled: false },
    'enabled zero': { name: 'x', host: 'h', check_type: 'ping', enabled: 0 },
    'enabled null': { name: 'x', host: 'h', check_type: 'ping', enabled: null },
    'enabled as text': { name: 'x', host: 'h', check_type: 'ping', enabled: 'false' },
    'unknown members': { name: 'x', host: 'h', check_type: 'ping', id: 77, last_status: 'up', created_at: 'then', note: 'n' },
    'text that needs escapes': { name: 'q"\\\b\f\n\r\t\u0001\u001f\u007f  /<>&é€\u{1F600}', host: '  ', check_type: 'ping' },
    'a long name': { name: 'n'.repeat(5000), host: 'h', check_type: 'ping' },
};
for (const [what, body] of Object.entries(odd)) {
    // Each target gets a name of its own: the list is ordered by name only
    step(`create odd: ${what}`, 'adm', 'POST', T, body.name === 'x' ? { ...body, name: `x ${what}` } : body);
}
raw('create odd: numbers as written', 'adm', 'POST', T, '{"name":"x numbers as written","host":"h","check_type":"ping","port":8.0e1,"timeout_ms":1E3,"interval_ms":20000.000}', 'application/json');
raw('create odd: a key twice', 'adm', 'POST', T, '{"name":"first","name":"second","host":"h","check_type":"icmp","check_type":"ping"}', 'application/json');

// ---- reading -----------------------------------------------------------------------
step('list all', 'vera', 'GET', T);
for (const q of ['enabled=true', 'enabled=false', 'enabled=1', 'enabled=', 'enabled', 'enabled=TRUE', 'enabled=true&enabled=true', 'enabled[]=true', 'enabled[a]=true',
    'check_type=tcp', 'check_type=http&enabled=true', 'check_type=ping&enabled=false', 'check_type=icmp', 'check_type=', 'check_type', 'check_type=TCP',
    'check_type=tcp&check_type=http', 'check_type[]=tcp', 'check_type[]=tcp&check_type[]=ping', 'check_type[0]=https', 'check_type[is]=tcp', 'check_type[a][b]=tcp',
    'check_type=%74cp', 'check_type=t+cp', 'check_type=%zz', 'other=1', 'ENABLED=true', 'enabled=true&check_type[]=tcp&check_type[]=x']) {
    step(`list ?${q}`, 'vera', 'GET', `${T}?${q}`);
}
for (const id of ['1', '2', '999', 'first', '1.0', '1e0', '%201%20', '01', '+1', '-1', '0', '0x1', '1abc', '1;drop', '99999999999999999999', '1.5', '%31', '']) {
    step(`get ${JSON.stringify(id)}`, 'vera', 'GET', `${T}/${id}`);
}
step('get in capitals', 'vera', 'GET', '/API/Network/Targets/1');
step('get with a slash at the end', 'vera', 'GET', `${T}/1/`);
step('head', 'vera', 'HEAD', `${T}/1`);
step('stats', 'vera', 'GET', '/api/network/stats');

// ---- changing ----------------------------------------------------------------------
const P = `${T}/1`;
for (const [what, body] of Object.entries({
    'all members': { name: 'B tcp renamed', host: '0.0.0.0', port: '9', url: 'http://0.0.0.0:9/x', check_type: 'tcp', timeout_ms: 2500, interval_ms: '45000', enabled: false },
    'nothing': {},
    'unknown members only': { last_status: 'up', last_check_at: 'now', last_rtt_ms: 5, id: 3, note: 'x' },
    'name only': { name: 'B tcp again' },
    'enabled true': { enabled: true },
    'enabled as text': { enabled: 'no' },
    'enabled empty text': { enabled: '' },
    'enabled zero': { enabled: 0 },
    'enabled as an object': { enabled: {} },
    'enabled null': { enabled: null },
    'enabled as a list': { enabled: [] },
    'unknown type': { check_type: 'icmp', name: 'not stored' },
    'type in capitals': { check_type: 'HTTP' },
    'type as a list': { check_type: ['tcp'] },
    'empty type': { check_type: '' },
    'type zero': { check_type: 0 },
    'type null': { check_type: null },
    'type false': { check_type: false },
    'type back': { check_type: 'tcp' },
    'name null': { name: null },
    'name empty': { name: '' },
    'name as a number': { name: 7 },
    'name true': { name: true },
    'name as a list of one': { name: ['one'] },
    'name as a list of two': { name: ['a', 'b'] },
    'name as an empty list': { name: [] },
    'name as an object': { name: { a: 1 } },
    'name as an object and host as an object': { name: { a: 1 }, host: { b: 2 } },
    'host null': { host: null },
    'host as a number': { host: 7.5 },
    'url null': { url: null },
    'url as a number': { url: 0 },
    'port null': { port: null },
    'port zero': { port: 0 },
    'port not a number': { port: 'none' },
    'port as text': { port: ' 8443 tcp' },
    'port large': { port: 5000000000 },
    'port 1e21': { port: 1e21 },
    'port with a fraction': { port: 80.9 },
    'timeouts not numbers': { timeout_ms: 'none', interval_ms: null },
    'timeouts zero': { timeout_ms: 0, interval_ms: 0 },
    'timeouts below zero': { timeout_ms: -1, interval_ms: '-5' },
    'timeouts large': { timeout_ms: '99999999999999999999999', interval_ms: 5000000000 },
    'timeouts true': { timeout_ms: true, interval_ms: [3000] },
    'back to normal': { name: 'B tcp', host: '0.0.0.0', port: 9, url: null, check_type: 'tcp', timeout_ms: 3000, interval_ms: 30000, enabled: 1 },
})) {
    step(`update: ${what}`, 'adm', 'PATCH', P, body);
    step(`get after update: ${what}`, 'vera', 'GET', P);
}
for (const id of ['999', 'first', '1.0', '%201', '1abc']) step(`update id ${JSON.stringify(id)}`, 'adm', 'PATCH', `${T}/${id}`, { timeout_ms: 3000 });
raw('update with a list body', 'adm', 'PATCH', P, '[{"name":"x"}]');
step('update without a body', 'adm', 'PATCH', P);
raw('update as a form', 'adm', 'PATCH', P, 'name=B+tcp&enabled=0', 'application/x-www-form-urlencoded');
step('get after the form', 'vera', 'GET', P);
step('update after the form', 'adm', 'PATCH', P, { enabled: true });
step('put is no route', 'adm', 'PUT', P, { name: 'x' });

// ---- checks ------------------------------------------------------------------------
for (const id of [1, 2, 3, 4, 5]) {
    step(`check target ${id}`, 'olga', 'POST', `${T}/${id}/check`, {});
    step(`get target ${id} after its check`, 'vera', 'GET', `${T}/${id}`);
    step(`history of target ${id}`, 'vera', 'GET', `${T}/${id}/history`);
}
step('check with a body', 'olga', 'POST', `${T}/1/check`, { host: '127.0.0.1', check_type: 'ping' });
step('check without a body', 'olga', 'POST', `${T}/1/check`);
for (const id of ['999', 'first', '1.0', '%201']) step(`check id ${JSON.stringify(id)}`, 'olga', 'POST', `${T}/${id}/check`, {});
for (const [what, body] of Object.entries({
    'ping without host': { check_type: 'ping', host: null, url: 'x' },
    'ping with a blocked host': { check_type: 'ping', host: 'localhost' },
    'tcp without port': { check_type: 'tcp', host: '0.0.0.0', port: null },
    'tcp with a large port': { check_type: 'tcp', host: '0.0.0.0', port: 70000 },
    'tcp with a timeout below zero': { check_type: 'tcp', host: '0.0.0.0', port: 9, timeout_ms: -5 },
    'tcp with a name that has no address': { check_type: 'tcp', host: 'empty.test', port: 9, timeout_ms: 3000 },
    'tcp with a name that is not found': { check_type: 'tcp', host: 'nowhere.test' },
    'tcp with a name of loopback': { check_type: 'tcp', host: 'loop.test' },
    'http by host and port': { check_type: 'http', host: '0.0.0.0', port: 9, url: null },
    'http by host': { check_type: 'http', host: '0.0.0.0', port: null },
    'https by host': { check_type: 'https', host: '0.0.0.0', port: null },
    'http without host': { check_type: 'http', host: null },
    'http with an empty host': { check_type: 'http', host: '' },
    'http with a url that is none': { check_type: 'http', url: 'not a url' },
    'http with a blocked url': { check_type: 'http', url: 'http://127.0.0.1/' },
    'http with a timeout below zero': { check_type: 'http', url: 'http://0.0.0.0:9/', timeout_ms: -1 },
    'no type': { check_type: '' },
})) {
    step(`prepare check: ${what}`, 'adm', 'PATCH', `${T}/5`, body);
    step(`check: ${what}`, 'olga', 'POST', `${T}/5/check`, {});
}
step('prepare check: back to ping', 'adm', 'PATCH', `${T}/5`, { check_type: 'ping', host: 'loop.test', port: 0, url: '', timeout_ms: 700 });
// A check's time has seconds only on SQLite, and checks of one second come
// in no order of their own. So the checks get times a minute apart here,
// in the script and in the test that replays it.
step('spread the times of the checks', null, 'SPREAD', '');
for (const q of ['from=2026-01-01 00:15:00', 'to=2026-01-01 00:15:00', 'from=2026-01-01 00:15:00&to=2026-01-01 00:17:00', 'from=2026-01-01T00:15:00.000Z', 'to=2026-01-01T00:15:00.000Z',
    'from=2026-01-01', 'to=2026-01-02', 'from=2026-01-01 00:15:00&limit=2', 'to=2026-01-01 00:15:00&limit=2']) {
    step(`history ?${q}`, 'vera', 'GET', `${T}/5/history?${encodeURI(q)}`);
}
for (const q of ['', 'limit=3', 'limit=1', 'limit=0', 'limit=-1', 'limit=all', 'limit=2.9', 'limit=%202', 'limit=2&limit=5', 'limit[]=4', 'limit[a]=4', 'limit=99999999999999999999', 'limit=1e1',
    'from=2000-01-01', 'from=2999-01-01', 'to=2000-01-01', 'to=2999-01-01', 'from=2000-01-01&to=2999-01-01', 'from=&to=', 'from', 'from=never', 'from=1', 'from=a&from=b', 'from[]=2000-01-01',
    'from[]=a&to[]=b', 'from[x]=1', 'to[x]=1&from[y]=2', 'from=2000-01-01&limit=2', 'from=a&from=b&limit=1', 'limit=2&from=2000-01-01T00:00:00.000Z&to=2999-12-31%2023:59:59']) {
    step(`history ?${q}`, 'vera', 'GET', `${T}/5/history${q ? '?' + q : ''}`);
}
for (const id of ['999', 'first', '5.0', '%205']) step(`history id ${JSON.stringify(id)}`, 'vera', 'GET', `${T}/${id}/history`);
step('stats after checks', 'vera', 'GET', '/api/network/stats');
step('list after checks', 'vera', 'GET', T);

// ---- tools -------------------------------------------------------------------------
const tool = (name, kind, body) => step(`${kind}: ${name}`, 'olga', 'POST', `/api/network/${kind}`, body);
const hosts = {
    'no host': {}, 'empty host': { host: '' }, 'host null': { host: null }, 'host zero': { host: 0 }, 'host false': { host: false },
    'host as a number': { host: 2130706433 }, 'host as a fraction': { host: 1.50 }, 'host true': { host: true }, 'host as a list': { host: ['0.0.0.0'] },
    'host as an empty list': { host: [] }, 'host as an object': { host: { b: 1, a: [1.0, 'x'], 3: null } },
    'loopback': { host: '127.0.0.1' }, 'loopback v6': { host: '::1' }, 'localhost': { host: 'LocalHost' }, 'link-local': { host: '169.254.169.254' },
    'a name of loopback': { host: 'loop.test' }, 'a name with loopback among others': { host: 'mixed.test' }, 'an unknown name': { host: 'nowhere.test' },
    'a name the lookup fails for': { host: 'again.test' }, 'a name without addresses': { host: 'empty.test' }, 'loopback as a number': { host: '2130706433' },
    'shell characters': { host: '0.0.0.0; id' }, 'an option': { host: '-c5' }, 'a space in front': { host: ' 0.0.0.0' }, 'brackets': { host: '[::1]' },
    'not ASCII': { host: 'münchen.de' }, 'too long': { host: 'a'.repeat(254) },
};
for (const [what, body] of Object.entries(hosts)) {
    tool(what, 'ping', body);
    tool(what, 'tcp', { ...body, port: 9 });
    tool(what, 'resolve', body);
}
tool('any address', 'ping', { host: '0.0.0.0' });
tool('any address, timeout as text', 'ping', { host: '0.0.0.0', timeout: '2500ms' });
tool('timeout below zero', 'ping', { host: '0.0.0.0', timeout: -5000 });
tool('timeout far below zero', 'ping', { host: '0.0.0.0', timeout: -5000000000 });
tool('timeout zero', 'ping', { host: '0.0.0.0', timeout: 0 });
tool('timeout as a list', 'ping', { host: '0.0.0.0', timeout: [1500, 2] });
tool('a private name', 'ping', { host: 'internal.corp', timeout: 1000 });
for (const [what, port] of Object.entries({
    'no port': undefined, 'port zero': 0, 'port null': null, 'port empty': '', 'port false': false, 'port text zero': '0', 'port 9': 9, 'port as text': '9/tcp',
    'port with spaces': ' 9 ', 'port not a number': 'http', 'port too large': 70000, 'port below zero': -1, 'port with a fraction': 9.9, 'port true': true,
    'port as a list': [9], 'port as a list of two': [9, 10], 'port as an empty list': [], 'port as an object': { a: 1 }, 'port 1e21': 1e21, 'port of many digits': '99999999999999999999999',
})) tool(what, 'tcp', port === undefined ? { host: '0.0.0.0' } : { host: '0.0.0.0', port });
tool('timeout below zero', 'tcp', { host: '0.0.0.0', port: 9, timeout: -1 });
tool('timeout below zero and a bad port', 'tcp', { host: '0.0.0.0', port: -1, timeout: -1 });
tool('timeout as text', 'tcp', { host: '0.0.0.0', port: 9, timeout: '1000' });
tool('by name', 'tcp', { host: 'zero.test', port: 9 });
tool('members in another order', 'tcp', { timeout: 1000, port: 9, host: '0.0.0.0' });
for (const [what, body] of Object.entries({
    'no url': {}, 'empty url': { url: '' }, 'url null': { url: null }, 'url zero': { url: 0 }, 'url as a number': { url: 5 }, 'url true': { url: true },
    'url as a list': { url: ['http://0.0.0.0:9/'] }, 'url as an object': { url: { href: 'http://0.0.0.0:9/', b: 1.50, a: 'x', 10: true, 2: null } },
    'closed port': { url: 'http://0.0.0.0:9/health' }, 'closed port by name': { url: 'http://zero.test:9/' }, 'default port': { url: 'http://0.0.0.0' },
    'https closed port': { url: 'https://0.0.0.0:9/' }, 'with spaces and capitals': { url: '  HTTP://0.0.0.0:9/health?probe=1#top  ', timeout: 2000 },
    'loopback': { url: 'http://127.0.0.1:9/' }, 'localhost': { url: 'http://localhost:9/' }, 'loopback v6': { url: 'http://[::1]:9/' },
    'loopback as a number': { url: 'http://2130706433:9/' }, 'loopback in hex': { url: 'http://0x7f.0.0.1:9/' }, 'loopback short': { url: 'http://127.1:9/' },
    'loopback with backslashes': { url: 'http:\\\\127.0.0.1:9\\x' }, 'link-local': { url: 'http://169.254.169.254/latest/meta-data/' },
    'a name of loopback': { url: 'http://loop.test/' }, 'an unknown name': { url: 'http://nowhere.test/' }, 'a v6 address': { url: 'http://[2001:db8::1]:9/' },
    'credentials': { url: 'http://user:secret@0.0.0.0:9/' }, 'user only': { url: 'http://user@0.0.0.0:9/' }, 'other scheme': { url: 'ftp://0.0.0.0/file' },
    'file': { url: 'file:///etc/passwd' }, 'not a url': { url: '0.0.0.0:9/health' }, 'no scheme': { url: '//0.0.0.0:9/health' }, 'bad port': { url: 'http://0.0.0.0:99999/' },
    'timeout below zero': { url: 'http://0.0.0.0:9/', timeout: -1 }, 'timeout as text': { url: 'http://0.0.0.0:9/', timeout: '1000ms' },
    'timeout checked after the url': { url: 'http://127.0.0.1:9/', timeout: -1 }, 'members in another order': { timeout: 1000, url: 'http://0.0.0.0:9/' },
    'text that needs escapes': { url: 'http://0.0.0.0:9/"<> é' },
})) tool(what, 'http', body);
raw('resolve with a list body', 'olga', 'POST', '/api/network/resolve', '["0.0.0.0"]');
step('resolve without a body', 'olga', 'POST', '/api/network/resolve');
raw('resolve as a form', 'olga', 'POST', '/api/network/resolve', 'host=0.0.0.0', 'application/x-www-form-urlencoded');
raw('resolve as a form with two hosts', 'olga', 'POST', '/api/network/resolve', 'host=0.0.0.0&host=10.0.0.1', 'application/x-www-form-urlencoded');
for (const host of ['0.0.0.0', '10.255.255.1', 'FD00::1', 'zero.test', 'dual.test', 'EXAMPLE.COM', '010.0.0.1', '256', 'internal.corp']) tool(host, 'resolve', { host });

// ---- the monitor polls what is due -------------------------------------------------
step('poll: every target due later', 'adm', 'PATCH', `${T}/2`, { interval_ms: 3600000 });
step('poll: start', 'adm', 'POST', '/api/network/monitor/start', { interval_ms: 3600000 });
step('poll: wait', null, 'WAIT', '2500');
step('poll: stop', 'adm', 'POST', '/api/network/monitor/stop', {});
step('poll: list', 'vera', 'GET', T);
step('poll: history of a target that was not due', 'vera', 'GET', `${T}/2/history`);
step('poll: history of a disabled target', 'vera', 'GET', `${T}/3/history`);

// ---- deleting ----------------------------------------------------------------------
for (const id of ['999', 'first', '%203', '4.0', '3abc']) step(`delete id ${JSON.stringify(id)}`, 'adm', 'DELETE', `${T}/${id}`);
step('delete', 'sara', 'DELETE', `${T}/1`);
step('delete again', 'sara', 'DELETE', `${T}/1`);
step('get deleted', 'vera', 'GET', `${T}/1`);
step('history of deleted', 'vera', 'GET', `${T}/1/history`);
step('check deleted', 'olga', 'POST', `${T}/1/check`, {});
step('update deleted', 'adm', 'PATCH', `${T}/1`, { name: 'x' });
step('delete with a body', 'adm', 'DELETE', `${T}/2`, { id: 5 });
step('list at the end', 'vera', 'GET', T);
step('stats at the end', 'vera', 'GET', '/api/network/stats');

function send(port, s) {
    const headers = {};
    let payload = null;
    if (s.body !== undefined) {
        payload = Buffer.from(JSON.stringify(s.body));
        headers['Content-Type'] = 'application/json';
    } else if (s.raw !== undefined) {
        payload = Buffer.from(s.raw);
        headers['Content-Type'] = s.type;
    }
    if (payload) headers['Content-Length'] = payload.length;
    if (s.as) headers['x-user'] = s.as;
    return new Promise((resolve, reject) => {
        const req = http.request({ host: '127.0.0.1', port, method: s.method, path: s.path, headers }, (res) => {
            const chunks = [];
            res.on('data', (chunk) => chunks.push(chunk));
            res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, text: Buffer.concat(chunks).toString('utf8') }));
        });
        req.on('error', reject);
        if (payload) req.write(payload);
        req.end();
    });
}

async function main() {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-network-oracle-'));
    process.env.DATA_DIR = tmpDir;
    let dropSchema = async () => {};
    let spread = async () => {};
    if (database === 'postgres') {
        let dsn = process.env.BETTERDESK_TEST_POSTGRES_DSN;
        if (!dsn) throw new Error('BETTERDESK_TEST_POSTGRES_DSN is not set');
        // The lookups below know no real names, so the server goes in by address
        const server = new URL(dsn);
        server.hostname = (await require('dns').promises.lookup(server.hostname)).address;
        dsn = server.toString();
        const { Client } = require(path.join(consoleDir, 'node_modules/pg'));
        const schema = 'oracle_network';
        const admin = new Client({ connectionString: dsn });
        await admin.connect();
        await admin.query(`DROP SCHEMA IF EXISTS ${schema} CASCADE`);
        await admin.query(`CREATE SCHEMA ${schema}`);
        dropSchema = async () => { await admin.query(`DROP SCHEMA ${schema} CASCADE`); await admin.end(); };
        spread = () => admin.query(`UPDATE ${schema}.network_checks SET checked_at = TIMESTAMPTZ '2026-01-01 00:00:00+00' + id * INTERVAL '1 minute'`);
        process.env.DB_TYPE = 'postgres';
        process.env.DATABASE_URL = dsn + (dsn.includes('?') ? '&' : '?') + 'options=' + encodeURIComponent(`-c search_path=${schema}`);
    } else {
        process.env.SQLITE_AUTH_DB_MODE = 'consolidated';
        process.env.DB_TYPE = 'sqlite';
        process.env.DB_PATH = path.join(tmpDir, 'db_v2.sqlite3');
        delete process.env.DATABASE_URL;
        spread = async () => {
            const second = require(path.join(consoleDir, 'node_modules/better-sqlite3'))(process.env.DB_PATH);
            second.prepare("UPDATE network_checks SET checked_at = datetime('2026-01-01 00:00:00', '+' || id || ' minutes')").run();
            second.close();
        };
    }
    // Lookups answer from the table of the monitor's oracle; the image has
    // no ping command; nothing listens on port 9 of 0.0.0.0.
    install();

    const express = require(path.join(consoleDir, 'node_modules/express'));
    const adapter = require(path.join(consoleDir, 'services/dbAdapter')).getAdapter();
    await adapter.init();
    const routes = require(path.join(consoleDir, 'routes/network.routes'));

    // The body parsers of server.js, a session per request, and the rule
    // of routes/index.js that a write to /api has to be JSON
    const { requireJsonContentType } = require(path.join(consoleDir, 'lib/requireJsonContentType'));
    const app = express();
    app.use(express.json({ limit: '2mb' }));
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use((req, _res, next) => {
        const user = users[req.headers['x-user']];
        req.session = user ? { userId: user.id, user } : {};
        req.t = (key) => key;
        next();
    });
    if (process.env.ORACLE_JSON_ONLY !== '0') app.use(requireJsonContentType);
    app.use('/api/network', routes);
    const server = app.listen(0, '127.0.0.1');
    await new Promise((resolve) => server.once('listening', resolve));
    const { port } = server.address();

    const out = [JSON.stringify({ dns: table })];
    const quietLog = console.log;
    const quietError = console.error;
    console.log = () => {};
    console.error = () => {};
    for (const s of steps) {
        if (s.method === 'SPREAD') {
            await spread();
            out.push(JSON.stringify({ name: s.name, method: 'SPREAD', path: '' }));
            continue;
        }
        if (s.method === 'WAIT') {
            await new Promise((resolve) => setTimeout(resolve, Number(s.path)));
            out.push(JSON.stringify({ name: s.name, method: 'WAIT', path: s.path }));
            continue;
        }
        const answer = await send(port, s);
        out.push(JSON.stringify({
            name: s.name,
            as: s.as,
            method: s.method,
            path: s.path,
            body: s.body !== undefined ? JSON.stringify(s.body) : (s.raw !== undefined ? s.raw : null),
            bodyType: s.raw !== undefined ? s.type : '',
            status: answer.status,
            type: answer.headers['content-type'] || '',
            text: answer.text,
        }));
    }
    console.log = quietLog;
    console.error = quietError;
    fs.writeFileSync(path.join(__dirname, '..', `routes.${database}.json`), out.join('\n') + '\n');
    console.log(`wrote routes.${database}.json: ${steps.length} steps`);
    server.close();
    await dropSchema();
    fs.rmSync(tmpDir, { recursive: true, force: true });
    process.exit(0);
}

main().catch((err) => { console.error(err); process.exit(1); });
