'use strict';

/**
 * Records what the policy and event routes of routes/dataguard.routes.js
 * answer with the real database adapter, at both mounts, on SQLite and
 * (with ORACLE_PG_DSN) on PostgreSQL. See README.md.
 */

const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const at = (minute) => `2001-01-01 00:${String(minute).padStart(2, '0')}:00${pg ? '+00' : ''}`;

const steps = [];
let n = 0;
function step(method, url, body, role = 'admin', raw = false) {
    n += 1;
    const s = { name: `${n} ${method} ${url}`, role, method, path: url };
    if (body !== undefined) s.body = raw ? body : J(body);
    steps.push(s);
}
const sql = (name, text, params) => steps.push({ name, sql: text, params });
const MOUNTS = ['/api/dataguard', '/api/bd'];

// Nothing there yet: empty lists, and counts of nothing
for (const mount of MOUNTS) {
    step('GET', `${mount}/policies`);
    step('GET', `${mount}/events`);
    step('GET', `${mount}/stats`);
}

// Who may: every role that can sign in, and nobody, at both mounts
for (const role of ['admin', 'super_admin', 'server_admin', 'global_admin', 'operator', 'viewer', 'guest_issuer', '']) {
    for (const mount of MOUNTS) {
        step('POST', `${mount}/policies`, { name: `By ${role || 'nobody'} at ${mount}` }, role);
        step('GET', `${mount}/policies`, undefined, role);
        step('GET', `${mount}/policies/1`, undefined, role);
        step('PATCH', `${mount}/policies/999`, { name: 'x' }, role);
        step('DELETE', `${mount}/policies/999`, undefined, role);
        step('GET', `${mount}/events`, undefined, role);
        step('GET', `${mount}/stats`, undefined, role);
    }
}

// Creating: the name, then every field with values of every type
for (const name of ['P', '  spaced out  ', '', '   ', null, 0, 5, true, ['x'], { a: 1 }]) step('POST', '/api/dataguard/policies', { name });
step('POST', '/api/bd/policies', {});
step('POST', '/api/bd/policies', '[{"name":"In a list"}]', 'admin', true);
let serial = 0;
const fresh = () => `Policy ${++serial}`;
const values = ['text', '', 0, 5, 1.5, true, false, null, ['one'], ['a', 'b'], [], {}, { a: 1 }, 'UNIQUE'];
for (const field of ['description', 'policy_type', 'action', 'scope']) {
    for (const value of values) step('POST', '/api/dataguard/policies', { name: fresh(), [field]: value });
}
for (const enabled of [true, false, 1, 0, 2, -1, 'yes', 'no', 'true', 'false', 't', 'on', 'off', '', ' ', 'abc', null, [], [1], {}, 1.5]) {
    step('POST', '/api/bd/policies', { name: fresh(), enabled });
}
for (const rules of [[], [1, 'a', null, true, [2, [3]], { b: 1, 2: 'x', a: 1.5, 10: 'y' }], [{ rule_type: 'usb', action: 'block', filter: '*' }],
    ['\u2028<&>"\\', 1e21, 1e-7, -0, 123456789012345680000], {}, { length: 0 }, 'text', '[]', null, 5, 0, true, false]) {
    step('POST', '/api/dataguard/policies', { name: fresh(), rules });
}
step('POST', '/api/dataguard/policies', `{"name":"${fresh()}","rules":[1.0, 1e2, 12345678901234567890, {"b":1,"a":2,"b":3}]}`, 'admin', true);
step('POST', '/api/dataguard/policies', { name: fresh(), description: {}, scope: ['a', 'b'] });
step('GET', '/api/dataguard/policies');
step('GET', '/api/bd/policies', undefined, 'viewer');

// Ids in every spelling
for (const id of ['1', '01', '1.0', '1e0', '%201', '0x1', '1.5', 'abc', 'Infinity', '999', '0', '-1', '3000000000', '99999999999999999999', '%20']) {
    step('GET', `/api/dataguard/policies/${id}`);
    step('GET', `/api/bd/policies/${id}`);
    step('PATCH', `/api/bd/policies/${id}`, { scope: `set-by-${id}` });
}

// Updating policy 2. Its times are set back first, so that an answer
// shows which of them an update moves.
sql('set the times of policy 2 back', pg
    ? "UPDATE dlp_policies SET created_at = TIMESTAMPTZ '2001-02-03 04:05:06+00', updated_at = TIMESTAMPTZ '2001-02-03 04:05:07+00' WHERE id = 2"
    : "UPDATE dlp_policies SET created_at = '2001-02-03 04:05:06', updated_at = '2001-02-03 04:05:07' WHERE id = 2");
step('PATCH', '/api/dataguard/policies/2', {});
step('PATCH', '/api/bd/policies/2', { unknown: true });
step('GET', '/api/dataguard/policies/2');
for (const body of [{ name: 'Renamed' }, { name: null }, { name: 5 }, { name: '' }, { name: ['a', 'b'] }, { description: null }, { description: 7 }, { description: true },
    { policy_type: 'file', action: 'block', scope: 'group:1' }, { action: null }, { scope: {} }, { enabled: false }, { enabled: 0 }, { enabled: '0' }, { enabled: null },
    { enabled: [] }, { enabled: '' }, { enabled: 'false' }, { enabled: 1 }, { rules: [] }, { rules: [{ rule_type: 'file', action: 'log', filter: '*.docx' }, 2] },
    { rules: {} }, { rules: null }, { rules: 'text' }, { rules: 0 }, { rules: [[]] }, { name: 'All', description: 'd', policy_type: 'usb', action: 'log', scope: '', enabled: true, rules: [1] },
    { rules: {}, name: null }, { description: {}, scope: ['x', 'y'] }, { id: 99, created_at: 'x', name: 'Last' }]) {
    step('PATCH', '/api/dataguard/policies/2', body);
}
step('PATCH', '/api/bd/policies/2', '[{"name":"In a list"}]', 'admin', true);
step('GET', '/api/bd/policies/2');

// Events, as devices reported them
const EVENT = 'INSERT INTO dlp_events (device_id, event_source, event_type, policy_id, policy_name, action, details, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)';
const events = [
    ['D1', 'usb', 'connected', 1, 'By admin', 'block', '{"vendor":"0781","product":"5567","label":"SanDisk"}', at(1)],
    ['D1', 'usb', 'removed', 1, 'By admin', 'log', '{}', at(2)],
    ['D1', 'file', 'copy', null, '', 'log', '{"path":"C:\\\\Users\\\\a\\\\x.docx","size":1.50,"tags":[1,[2]],"10":"a","2":"b"}', at(3)],
    ['D2', 'file', 'delete', 2, 'By super_admin', 'block', '[1,2,3]', at(4)],
    ['D2', 'usb', 'info', null, '', 'allow', '"a JSON string holding {\\"x\\":1}"', at(5)],
    ['D2', 'unknown', 'info', null, null, null, null, at(6)],
    ['D3', 'usb', 'info', null, '', 'log', '5', at(7)],
    ['D3', 'usb', 'info', null, '', 'log', 'null', at(8)],
    ['D3', 'USB', 'INFO', null, '', 'BLOCK', '0', at(9)],
    ['D4', 'file', 'copy', null, '', 'log', '"{\\"inner\\":true}"', at(10)],
];
events.forEach((event, i) => sql(`event ${i + 1}`, EVENT, event));
for (const mount of MOUNTS) step('GET', `${mount}/stats`);
const queries = ['device_id=D1', 'device_id=D3', 'device_id=D4', 'device_id=nobody', 'device_id=', 'event_source=usb', 'event_source=file&device_id=D1', 'event_type=info&device_id=D3',
    'event_type=copy&event_source=file&device_id=D1', 'device_id=D1&limit=1', 'device_id=D1&limit=2', 'device_id=D1&limit=0', 'device_id=D1&limit=abc', 'device_id=D1&limit=1.5',
    'device_id=D1&limit=-1', 'device_id=D1&limit=1e400', 'device_id=D1&limit=+2+', 'device_id=D1&limit=0x1', 'device_id=D1&limit=2&limit=3', 'device_id=D1&limit[]=2',
    'device_id=D1&limit[x]=2', 'device_id=D1&from=2001-01-01+00:02:00', 'device_id=D1&to=2001-01-01+00:02:00', 'device_id=D1&from=2001-01-01+00:02:00&to=2001-01-01+00:02:30',
    'device_id=D1&from=2001-01-01', 'device_id=D1&from=2002', 'device_id=D1&from=garbage', 'device_id=D1&to=garbage', 'device_id=D1&from=2001-01-01T00:02:00.000Z',
    'device_id=D1&device_id=D2', 'device_id[]=D1', 'device_id[0]=D1', 'device_id[x]=D1', 'device_id[]=D1&event_source[]=usb', 'device_id=D1&event_source=usb&event_source=file',
    'device_id=D1&unknown=1', 'DEVICE_ID=D3', 'device_id=d1'];
for (const query of queries) step('GET', `/api/dataguard/events?${query}`);
step('GET', '/api/bd/events?device_id=D1', undefined, 'viewer');
step('GET', '/api/bd/events?device_id=D1&limit=1', undefined, 'operator');
// Without a filter the list holds details that are no JSON object
step('GET', '/api/dataguard/events');
step('GET', '/api/bd/events?device_id=D2');
if (!pg) {
    // What only a text column can hold: details that are no JSON at all
    sql('an event with broken details', EVENT, ['D5', 'usb', 'info', null, '', 'log', 'not json', at(11)]);
    sql('an event with empty details', EVENT, ['D6', 'usb', 'info', null, '', 'log', '', at(12)]);
    step('GET', '/api/dataguard/events?device_id=D5');
    step('GET', '/api/dataguard/events?device_id=D6');
    step('GET', '/api/dataguard/events?device_id=D1');
    step('GET', '/api/dataguard/events');
}
for (const mount of MOUNTS) step('GET', `${mount}/stats`);

// Policies with rules nobody could save through the routes
const odd = pg
    ? [['rules null', 'null'], ['rules a string', '"[1,2]"'], ['rules a broken string', '"not json"'], ['rules a number', '7'], ['rules zero', '0'], ['rules false', 'false'], ['rules an object', '{"b":1,"a":2}']]
    : [['rules a string', '"[1,2]"'], ['rules broken', 'not json'], ['rules a number', '7'], ['rules empty', ''], ['rules an object', '{"b":1,"a":2}']];
sql('a policy without rules', 'INSERT INTO dlp_policies (id, name, rules) VALUES (?, ?, NULL)', [900, 'no rules']);
odd.forEach(([name, rules], i) => sql(`a policy with ${name}`, 'INSERT INTO dlp_policies (id, name, rules) VALUES (?, ?, ?)', [901 + i, name, rules]));
for (let id = 900; id <= 901 + odd.length; id += 1) step('GET', `/api/dataguard/policies/${id}`);
step('GET', '/api/dataguard/policies');
step('PATCH', '/api/dataguard/policies/902', { scope: 'x' });
step('PATCH', '/api/dataguard/policies/903', { rules: [] });
for (let id = 900; id <= 901 + odd.length; id += 1) step('DELETE', `/api/bd/policies/${id}`);
step('GET', '/api/bd/policies/1');

// Deleting a policy leaves its events, without the link
step('DELETE', '/api/dataguard/policies/1', undefined, 'operator');
step('DELETE', '/api/dataguard/policies/1', undefined, 'server_admin');
step('DELETE', '/api/dataguard/policies/1');
step('GET', '/api/dataguard/events?device_id=D1');
for (const id of ['2.0', 'abc', '1e400', '3.5', '%204']) step('DELETE', `/api/bd/policies/${id}`);
sql('policies left', 'SELECT id, name FROM dlp_policies ORDER BY id');

record({ routes: 'routes/dataguard.routes', mounts: MOUNTS, steps, out: 'node' }).catch((err) => {
    console.error(err);
    process.exit(1);
});
