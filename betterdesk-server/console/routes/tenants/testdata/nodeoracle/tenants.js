'use strict';

/**
 * Records what routes/tenants.routes.js answers with the real database
 * adapter, on SQLite and (with ORACLE_PG_DSN) on PostgreSQL. See README.md.
 */

const { record, USERS, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const yes = pg ? true : 1;
const no = pg ? false : 0;

// Devices the console knows: online, offline, deleted, and with NULL in
// the deleted flag.
const seed = [
    ['INSERT INTO peer (id, note, status_online, ip, is_deleted) VALUES (?, ?, ?, ?, ?)', ['D-ON', 'pc-on', yes, '10.0.0.1', no]],
    ['INSERT INTO peer (id, note, status_online, ip, is_deleted) VALUES (?, ?, ?, ?, ?)', ['D-OFF', '', no, null, no]],
    ['INSERT INTO peer (id, note, status_online, ip, is_deleted) VALUES (?, ?, ?, ?, ?)', ['D-GONE', 'gone', yes, '10.0.0.3', yes]],
    ['INSERT INTO peer (id, note, status_online, ip, is_deleted) VALUES (?, ?, ?, ?, ?)', ['D-NULLDEL', null, yes, '10.0.0.4', null]],
];
if (!pg) {
    // What only SQLite can hold in the flag: other numbers and text
    seed.push(['INSERT INTO peer (id, note, status_online, ip) VALUES (?, ?, ?, ?)', ['D-TWO', 'two', 2, '10.0.0.5']]);
    seed.push(['INSERT INTO peer (id, note, status_online, ip) VALUES (?, ?, ?, ?)', ['D-TEXT', 'text', 'yes', '10.0.0.6']]);
    seed.push(['INSERT INTO peer (id, note, status_online, ip) VALUES (?, ?, ?, ?)', ['D-REAL', 'real', 1.5, '10.0.0.7']]);
}

const steps = [];
let n = 0;
function step(method, path, body, role = 'admin', raw = false) {
    n += 1;
    const s = { name: `${n} ${method} ${path}`, role, method, path };
    if (body !== undefined) s.body = raw ? body : J(body);
    steps.push(s);
}

// Who may: every role and nobody
for (const role of [...Object.keys(USERS), '']) {
    step('GET', '/api/tenants', undefined, role);
    step('GET', '/api/tenants/1', undefined, role);
    step('POST', '/api/tenants', { name: `By ${role || 'nobody'}` }, role);
    step('PATCH', '/api/tenants/999', { notes: 'x' }, role);
    step('DELETE', '/api/tenants/999', undefined, role);
    step('GET', '/api/tenants/999/devices', undefined, role);
    step('POST', '/api/tenants/999/devices', { device_id: 'x' }, role);
    step('DELETE', '/api/tenants/999/devices/x', undefined, role);
    step('GET', '/api/tenants/999/users', undefined, role);
    step('POST', '/api/tenants/999/users', { user_id: 1 }, role);
    step('DELETE', '/api/tenants/999/users/1', undefined, role);
    step('GET', '/api/tenants/999/stats', undefined, role);
}
step('GET', '/api/tenants');

// Names and the slugs made from them
for (const name of ['Acme Corp', '  Trim me \t', 'acme corp', 'ACME--CORP!!', 'İstanbul Büro', 'ÀÉÎ 123 --- x',
    '!!!', '???', '---a---', 'K kelvin', 'ǅ titlecase', 'a b c', '  ', '', null, 5, 0, true, ['x'], { a: 1 }]) {
    step('POST', '/api/tenants', { name });
}
step('POST', '/api/tenants', {});
step('POST', '/api/tenants', '[{"name":"In a list"}]', 'admin', true);

// Every other field with every type
let serial = 0;
const fresh = () => `Tenant ${++serial}`;
const values = [0, '', 77, 1.5, -1, 1e21, 1e-7, true, false, null, [], ['one'], ['a', 'b'], [5], [null], [[1]], [true], {}, { a: 1 },
    'UNIQUE', 'plain text', ' 12 ', '12', '0x10', '1e3', '1.5', 'abc', '99999999999', 99999999999, 2147483648, -2147483649,
    { b: [1, { c: null }], 2: 'x', a: 1.0 }, [1, [2, 'x"y\\z'], null, 'q']];
for (const field of ['slug', 'contact_name', 'contact_email', 'max_devices', 'notes', 'active']) {
    for (const value of values) step('POST', '/api/tenants', { name: fresh(), [field]: value });
}
step('POST', '/api/tenants', `{"name":"${fresh()}","max_devices":1e400}`, 'admin', true);
step('POST', '/api/tenants', `{"name":"${fresh()}","notes":-1e400}`, 'admin', true);
step('POST', '/api/tenants', `{"name":"${fresh()}","slug":12345678901234567890}`, 'admin', true);
// A list gives SQLite one value per element, an object none: the rest moves up
step('POST', '/api/tenants', { name: fresh(), slug: {}, max_devices: [5, 'x'] });
step('POST', '/api/tenants', { name: fresh(), slug: {}, notes: {} });
step('POST', '/api/tenants', { name: fresh(), contact_name: ['a', 'b'], contact_email: {} });
step('GET', '/api/tenants');

// Ids in every spelling, on every route that takes one
const ids = ['1', '01', '1.0', '1e0', '%201', '1%20', '0x1', '0X1', '0b1', '0o1', '+1', '-1', '1.5', 'abc', 'NaN', 'Infinity', '-Infinity',
    '999', '0', '-0', '3000000000', '99999999999999999999', '1e400', '%20', '1_0', '%EF%BC%91', '1;x', '1%2C2', '.5e1', '5.', '1e', '--1', 'null', 'true'];
for (const id of ids) {
    step('GET', `/api/tenants/${id}`);
    step('GET', `/api/tenants/${id}/devices`);
    step('GET', `/api/tenants/${id}/users`);
    step('GET', `/api/tenants/${id}/stats`);
    step('PATCH', `/api/tenants/${id}`, { contact_email: `set-by-${id}` });
    step('POST', `/api/tenants/${id}/devices`, { device_id: `spelling-${ids.indexOf(id)}` });
    step('DELETE', `/api/tenants/${id}/devices/spelling-${ids.indexOf(id)}`);
    step('POST', `/api/tenants/${id}/users`, { user_id: 7 });
    step('DELETE', `/api/tenants/${id}/users/7`);
}

// Updates of tenant 2. Its times are set back first, so that an answer
// shows which of them an update moves.
steps.push({
    name: 'set the times of tenant 2 back',
    sql: pg
        ? "UPDATE tenants SET created_at = TIMESTAMPTZ '2001-02-03 04:05:06+00', updated_at = TIMESTAMPTZ '2001-02-03 04:05:07+00' WHERE id = 2"
        : "UPDATE tenants SET created_at = '2001-02-03 04:05:06', updated_at = '2001-02-03 04:05:07' WHERE id = 2",
});
step('PATCH', '/api/tenants/2', {});
step('PATCH', '/api/tenants/2', { unknown: true });
step('GET', '/api/tenants/2');
for (const body of [{ name: 'Renamed' }, { name: null }, { name: 5 }, { name: true }, { name: '' }, { slug: 'acme-corp' }, { slug: 'renamed' },
    { contact_name: null, contact_email: 7, notes: ['x'] }, { max_devices: 'abc' }, { max_devices: null }, { max_devices: '0x20' }, { max_devices: 7.5 },
    { active: false }, { active: 0 }, { active: '0' }, { active: null }, { active: [] }, { active: '' }, { active: 'false' }, { active: 1 },
    {}, { unknown: 1 }, { name: ['a', 'b'] }, { name: {} }, { notes: {}, max_devices: [1, 2] }, { name: 'Last', id: 99, created_at: 'x' },
    { slug: 'UNIQUE' }, { max_devices: 'UNIQUE' }]) {
    step('PATCH', '/api/tenants/2', body);
}
step('PATCH', '/api/tenants/2', '[{"name":"In a list"}]', 'admin', true);
step('PATCH', '/api/tenants/2', '{"name":"Twice","name":"Twice, last wins"}', 'admin', true);
step('GET', '/api/tenants/2');

// Devices of tenant 1
for (const body of [{ device_id: 'D-ON' }, { device_ids: ['D-OFF', 'D-GONE', 'D-NULLDEL', 'ghost', 'D-TWO', 'D-TEXT', 'D-REAL'] }, { device_ids: 'ab' },
    { device_ids: '💻x' }, { device_ids: 5 }, { device_ids: true }, { device_ids: {} }, { device_ids: { length: 0 } },
    { device_ids: { length: '0' } }, { device_ids: { length: 2 } }, { device_ids: [] },
    { device_ids: [7, null, true, ['nested'], {}, [], 1.5, '', ['p', 'q'], { length: 0 }, 1e21] },
    { device_id: 0 }, { device_id: '' }, { device_id: 5 }, { device_id: ['q'] }, { device_id: {} }, { device_id: true }, {},
    { device_ids: null, device_id: 'via-single' }, { device_ids: 0, device_id: 'via-single-2' }, { device_ids: '', device_id: 'via-single-3' },
    { device_ids: ['D-ON', 'D-ON'], device_id: 'ignored' }]) {
    step('POST', '/api/tenants/1/devices', body);
}
step('POST', '/api/tenants/1/devices', '{"device_ids":{"length":0.0}}', 'admin', true);
step('POST', '/api/tenants/1/devices', '{"device_ids":{"length":-0}}', 'admin', true);
steps.push({
    name: 'spread the device assignments over time',
    sql: pg
        ? "UPDATE tenant_devices SET assigned_at = TIMESTAMPTZ '2001-01-01 00:00:00+00' + id * INTERVAL '1 minute'"
        : "UPDATE tenant_devices SET assigned_at = datetime('2001-01-01 00:00:00', '+' || id || ' minutes')",
});
step('GET', '/api/tenants/1/devices');
step('GET', '/api/tenants/1/stats');
for (const device of ['ghost', 'ghost', '7.0', '7', '1.5', '%F0%9F%92%BB', 'D-ON', 'a%2Fb', '%20']) {
    step('DELETE', `/api/tenants/1/devices/${device}`);
}
step('GET', '/api/tenants/1/devices');

// Users of tenant 1
for (const body of [{ user_id: 2 }, { user_id: '3' }, { user_id: ' 4 ' }, { user_id: '0x5' }, { user_id: 1.5 }, { user_id: '2.5' }, { user_id: true },
    { user_id: [6] }, { user_id: ['8'] }, { user_id: [9, 10] }, { user_id: [] }, { user_id: [[11]] }, { user_id: {} }, { user_id: 'abc' },
    { user_id: null }, { user_id: 0 }, { user_id: '' }, { user_id: '0' }, { user_id: -1 }, { user_id: 1e21 }, { user_id: false }, {},
    { user_id: 2 }, { user_id: 3000000000 }, { user_id: '12abc' }]) {
    step('POST', '/api/tenants/1/users', body);
}
step('POST', '/api/tenants/1/users', '{"user_id":1e400}', 'admin', true);
step('POST', '/api/tenants/999/users', { user_id: 2 });
steps.push({
    name: 'spread the user assignments over time',
    sql: pg
        ? "UPDATE tenant_users SET assigned_at = TIMESTAMPTZ '2001-01-01 00:00:00+00' + id * INTERVAL '1 minute'"
        : "UPDATE tenant_users SET assigned_at = datetime('2001-01-01 00:00:00', '+' || id || ' minutes')",
});
step('GET', '/api/tenants/1/users');
step('GET', '/api/tenants/1/stats');
for (const user of ['2', '2', '1.5', '2.5', 'abc', '0x5', '%203', 'Infinity', '-1']) {
    step('DELETE', `/api/tenants/1/users/${user}`);
}
step('GET', '/api/tenants/1/users');

// A tenant without a device limit: NULL is 0 in the stats
step('PATCH', '/api/tenants/3', { max_devices: null });
step('GET', '/api/tenants/3');
step('GET', '/api/tenants/3/stats');

// Stats of every tenant there may be: max_devices in every stored form
for (let id = 1; id <= 60; id += 1) step('GET', `/api/tenants/${id}/stats`);

// Deleting a tenant takes its assignments along
step('DELETE', '/api/tenants/1');
step('DELETE', '/api/tenants/1');
step('GET', '/api/tenants/1/devices');
step('GET', '/api/tenants/1/users');
for (const id of ['2.0', 'abc', '1e400', '3.5', '%204']) step('DELETE', `/api/tenants/${id}`);
step('GET', '/api/tenants');

record({ routes: 'routes/tenants.routes', mounts: ['/api/tenants'], seed, steps, out: 'node' }).catch((err) => {
    console.error(err);
    process.exit(1);
});
