'use strict';

/**
 * Records what the ten panel routes of routes/automation.routes.js (alert
 * rules, alert history, remote commands) answer with the real database
 * adapter, on SQLite and (with ORACLE_PG_DSN) on PostgreSQL. See README.md.
 */

const { record, USERS, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const yes = pg ? true : 1;
const no = pg ? false : 0;
// A time of the fixed week the recording plays in, as the alert engine
// wrote it (ISO) and as the database wrote it itself
const iso = (day, clock) => `2019-03-0${day}T${clock}.000Z`;
const own = (day, clock) => (pg ? `2019-03-0${day} ${clock}+00` : `2019-03-0${day} ${clock}`);
const fired = (day, clock) => (pg ? own(day, clock) : iso(day, clock));

const seed = [];
const rule = (row) => {
    const columns = Object.keys(row);
    seed.push([`INSERT INTO alert_rules (${columns.join(', ')}) VALUES (${columns.map(() => '?').join(', ')})`, columns.map((c) => row[c])]);
};
const alert = (row) => {
    const columns = Object.keys(row);
    seed.push([`INSERT INTO alert_history (${columns.join(', ')}) VALUES (${columns.map(() => '?').join(', ')})`, columns.map((c) => row[c])]);
};
const command = (row) => {
    const columns = Object.keys(row);
    seed.push([`INSERT INTO remote_commands (${columns.join(', ')}) VALUES (${columns.map(() => '?').join(', ')})`, columns.map((c) => row[c])]);
};

// Rules 1 to 9, the same on both databases
rule({ name: 'CPU high', description: 'CPU over 90 percent', enabled: yes, condition_type: 'cpu_usage', condition_op: 'gt', condition_value: 90,
    severity: 'critical', scope_device_id: 'D1', cooldown_secs: 600, notify_emails: 'ops@example.com', created_by: 'e2e_admin',
    created_at: own(1, '08:00:01'), updated_at: own(1, '09:00:01') });
rule({ name: 'Patch me', description: 'before', enabled: no, condition_type: 'memory_usage', condition_op: 'lt', condition_value: 10,
    severity: 'warning', scope_device_id: 'D2', cooldown_secs: 300, notify_emails: '', created_by: 'e2e_super_admin',
    created_at: own(1, '08:00:02'), updated_at: own(1, '09:00:02') });
rule({ name: 'Nulls', description: null, enabled: null, condition_type: 'custom', condition_op: 'eq', condition_value: 0,
    severity: null, scope_device_id: null, cooldown_secs: null, notify_emails: null, created_by: null,
    created_at: own(1, '08:00:03'), updated_at: null });
rule({ name: 'A tenth', condition_type: 'disk_usage', condition_value: 0.1, cooldown_secs: 2147483647, created_at: own(1, '08:00:04'), updated_at: own(1, '09:00:04') });
rule({ name: 'Cents', condition_type: 'disk_usage', condition_value: 99.99, cooldown_secs: 0, created_at: own(1, '08:00:05'), updated_at: own(1, '09:00:05') });
rule({ name: 'More digits than four bytes hold', condition_type: 'custom', condition_value: 16777217, cooldown_secs: -5, created_at: own(1, '08:00:06'), updated_at: own(1, '09:00:06') });
rule({ name: 'Large', condition_type: 'custom', condition_value: 1e30, created_at: own(1, '08:00:07'), updated_at: own(1, '09:00:07') });
rule({ name: 'Small', condition_type: 'custom', condition_value: 1.5e-10, created_at: own(1, '08:00:08'), updated_at: own(1, '09:00:08') });
rule({ name: 'Signs <b>&amp; "q" \\ é 😀 \u2028 \u0007', description: 'line\nbreak\ttab', condition_type: 'idle_duration', condition_op: 'neq', condition_value: -2.5,
    notify_emails: 'a@example.com, b@example.com', created_at: own(1, '08:00:09'), updated_at: own(1, '09:00:09') });
const RULES = 9;

// Alerts 1 to 20, the same on both databases
alert({ rule_id: 1, device_id: 'D1', severity: 'critical', message: 'CPU at 97%', triggered_at: fired(4, '08:00:00'), created_at: own(4, '08:00:01') });
alert({ rule_id: 1, device_id: 'D1', severity: 'critical', message: 'CPU at 99%', triggered_at: fired(5, '08:00:00'), acknowledged: yes,
    acknowledged_by: 'someone', acknowledged_at: own(5, '08:30:00'), created_at: own(5, '08:00:01') });
alert({ rule_id: 1, device_id: 'D2', severity: 'warning', message: 'CPU at 91%', triggered_at: fired(5, '09:00:00'), created_at: own(5, '09:00:01') });
alert({ rule_id: 2, device_id: 'D2', severity: 'info', message: 'Memory at 5%', triggered_at: fired(6, '08:00:00'), acknowledged: yes,
    acknowledged_by: 'e2e_admin', acknowledged_at: own(6, '08:30:00'), created_at: own(6, '08:00:01') });
alert({ rule_id: 2, device_id: null, severity: null, message: '', triggered_at: fired(6, '09:00:00'), acknowledged: null, created_at: null });
alert({ rule_id: 3, device_id: 'D3', severity: 'warning', message: 'Signs <b>&amp; "q" \\ é 😀', triggered_at: fired(6, '10:00:00'), created_at: own(6, '10:00:01') });
alert({ rule_id: 9, device_id: '7', severity: 'critical', message: 'device with a number for a name', triggered_at: fired(6, '11:00:00'), created_at: own(6, '11:00:01') });
alert({ rule_id: 9, device_id: '7.0', severity: 'critical', message: 'device with a fraction for a name', triggered_at: fired(6, '12:00:00'), created_at: own(6, '12:00:01') });
for (let i = 0; i < 12; i += 1) {
    alert({ rule_id: 4, device_id: 'MANY', severity: i % 2 ? 'warning' : 'info', message: `alert ${i}`,
        triggered_at: fired(7, `0${Math.floor(i / 10)}:0${i % 10}:00`), acknowledged: i % 3 === 0 ? yes : no, created_at: own(7, `0${Math.floor(i / 10)}:0${i % 10}:01`) });
}
const ALERTS = 20;

// Commands 1 to 6, the same on both databases
command({ device_id: 'D1', command_type: 'shell', payload: 'whoami', status: 'pending', created_by: 'e2e_admin', created_at: own(2, '08:00:01') });
command({ device_id: 'D1', command_type: 'powershell', payload: 'Get-Process', status: 'running', created_by: 'e2e_admin',
    created_at: own(2, '08:00:02'), executed_at: own(2, '08:01:02') });
command({ device_id: 'D1', command_type: 'script', payload: 'echo "a"\necho b', status: 'completed', result: 'a\nb\n', created_by: 'e2e_super_admin',
    created_at: own(2, '08:00:03'), executed_at: own(2, '08:01:03'), completed_at: own(2, '08:02:03') });
command({ device_id: 'D2', command_type: 'reboot', payload: 'now', status: 'failed', result: 'Signs <b>&amp; "q" \\ é 😀', created_by: 'e2e_server_admin',
    created_at: own(2, '08:00:04'), completed_at: own(2, '08:02:04') });
command({ device_id: 'D2', command_type: 'restart_service', payload: 'spooler', status: null, created_by: 'someone', created_at: null });
command({ device_id: '7.0', command_type: 'shell', payload: 'hostname', status: 'pending', created_by: 'e2e_admin', created_at: own(2, '08:00:06') });
const COMMANDS = 6;

if (!pg) {
    // What only SQLite's columns can hold: text and fractions in number
    // columns, numbers in text columns, flags that are neither 0 nor 1
    rule({ name: 'Value is text', condition_type: 'custom', condition_value: 'high', cooldown_secs: 'soon', enabled: 'yes', created_at: own(1, '08:00:10'), updated_at: 'no time' });
    rule({ name: 'Half a second', condition_type: 'custom', condition_value: 5, cooldown_secs: 1.5, enabled: 2, created_at: own(1, '08:00:11'), updated_at: 5.25 });
    rule({ name: 'Too large for JavaScript', condition_type: 'custom', condition_value: 9007199254740993, cooldown_secs: 9007199254740993, enabled: 1.5,
        created_at: '2019-03-01T08:00:12.000Z', updated_at: own(1, '09:00:12') });
    alert({ rule_id: 10, device_id: 'ODD', severity: 'warning', message: 'flag is text', triggered_at: iso(3, '08:00:00'), acknowledged: 'yes', created_at: own(3, '08:00:01') });
    alert({ rule_id: 10, device_id: 'ODD', severity: 'warning', message: 'flag is two', triggered_at: '2019-03-03 09:00:00', acknowledged: 2, created_at: own(3, '09:00:01') });
    alert({ rule_id: 11, device_id: 'ODD', severity: 5.5, message: 'time is a number', triggered_at: 5.5, acknowledged: 1.5, acknowledged_at: 'yesterday', created_at: 7.5 });
    command({ device_id: 'ODD', command_type: 'shell', payload: 'x', status: 'pending', result: 5.5, created_by: 'e2e_admin', created_at: '2019-03-02T08:00:07.000Z', executed_at: 'later', completed_at: 6.5 });
}

const steps = [];
let n = 0;
function step(method, path, body, role = 'admin', raw = false) {
    n += 1;
    const s = { name: `${n} ${method} ${path}`, role, method, path };
    if (body !== undefined) s.body = raw ? body : J(body);
    steps.push(s);
}
const get = (path, role) => step('GET', path, undefined, role);
const sql = (name, text, params) => steps.push({ name, sql: text, params });
const A = '/api/automation';
// Rows written during the run get times by their id, so that lists ordered
// by time have one order. The seeded rows keep theirs.
const SEEDED = { alert_rules: pg ? RULES : RULES + 3, remote_commands: pg ? COMMANDS : COMMANDS + 1 };
const spread = (table) => sql(`spread the times of ${table}`, pg
    ? `UPDATE ${table} SET created_at = TIMESTAMPTZ '2001-01-01 00:00:00+00' + id * INTERVAL '1 minute' WHERE id > ${SEEDED[table]}`
    : `UPDATE ${table} SET created_at = datetime('2001-01-01 00:00:00', '+' || id || ' minutes') WHERE id > ${SEEDED[table]}`);

// What is there at the start
get(`${A}/rules`);
for (let id = 1; id <= RULES + 4; id += 1) get(`${A}/rules/${id}`);
get(`${A}/alerts`);
get(`${A}/commands`);
for (let id = 1; id <= COMMANDS + 2; id += 1) get(`${A}/commands/${id}`);

// Who may: every role that can sign in, and nobody. The writes of this
// round change nothing also when they are let through, because the panel
// access guard of the console, which this recording does not have, refuses
// some roles the routes let in.
for (const role of [...Object.keys(USERS).filter((r) => r !== 'pro'), '']) {
    get(`${A}/rules`, role);
    get(`${A}/rules/1`, role);
    get(`${A}/rules/999`, role);
    get(`${A}/alerts`, role);
    get(`${A}/commands`, role);
    get(`${A}/commands/1`, role);
    get(`${A}/commands/999`, role);
    step('POST', `${A}/rules`, { name: '', condition_type: 'custom' }, role);
    step('PATCH', `${A}/rules/999`, { description: 'x' }, role);
    step('DELETE', `${A}/rules/999`, undefined, role);
    step('POST', `${A}/alerts/999/ack`, {}, role);
    step('POST', `${A}/commands`, { device_id: '', payload: 'x' }, role);
}
// Writes that go through, as the two roles the guard lets write
for (const role of ['admin', 'super_admin']) {
    step('POST', `${A}/rules`, { name: `By ${role}`, condition_type: 'custom' }, role);
    step('PATCH', `${A}/rules/3`, { description: `by ${role}` }, role);
    step('POST', `${A}/commands`, { device_id: 'D3', payload: `echo ${role}` }, role);
}
get(`${A}/rules/3`);
spread('alert_rules');
spread('remote_commands');

// Under the device mount none of the ten answers
get('/api/bd/rules');
get('/api/bd/rules/1');
step('POST', '/api/bd/rules', { name: 'At the device mount', condition_type: 'custom' });
step('PATCH', '/api/bd/rules/1', { description: 'x' });
step('DELETE', '/api/bd/rules/1');
get('/api/bd/alerts');
step('POST', '/api/bd/alerts/1/ack', {});
step('POST', '/api/bd/commands', { device_id: 'D3', payload: 'x' });
get('/api/bd/commands/1');

// Spellings of the address
for (const path of [`${A}/rules/`, '/API/Automation/Rules', `${A}/rules/1/`, `${A}/RULES/1`, `${A}/alerts/`, `${A}/Alerts`, `${A}/commands/`, `${A}/COMMANDS/1/`,
    `${A}`, `${A}/`, `${A}/rules/1/more`, `${A}/rule`, `${A}/alerts/1`, `${A}/alerts/1/ack`, `${A}/commands/1/more`, `${A}//rules`, `${A}/rules//1`,
    `${A}/rules;x=1`, `${A}/rules%2F1`, `${A}/rules/%C3%28`, `${A}/rules/%00`]) {
    get(path);
}
step('POST', `${A}/alerts/1/ACK/`, {});
step('PUT', `${A}/rules/1`, { description: 'x' });
step('POST', `${A}/rules/1`, { description: 'x' });
step('DELETE', `${A}/rules`);
step('DELETE', `${A}/alerts/1`);
step('DELETE', `${A}/commands/1`);
step('PATCH', `${A}/commands/1`, { status: 'completed' });

// The alert history with every filter, in every form a query can give it
const texts = ['D1', 'D2', 'MANY', 'ODD', '7', '7.0', 'nobody', '', '%20', 'd1', 'D1%20', '%20D1', 'D%31', 'critical', 'warning', 'info', 'null', 'undefined', '0', 'true',
    '%E2%82%AC', '%FF', '%00', "x'%20OR%201=1--", 'D1%00', 'D1+', 'D1%2CD2', 'D1#x', 'D1;severity=info'];
const forms = (name, a, b) => [`${name}=${a}&${name}=${b}`, `${name}=${a}&${name}=${a}`, `${name}[]=${a}`, `${name}[]=${a}&${name}[]=${b}`, `${name}[0]=${a}`, `${name}[1]=${a}`,
    `${name}[1]=${b}&${name}[0]=${a}`, `${name}[]=`, `${name}[]`, `${name}`, `${name}=&${name}=`, `${name}[id]=${a}`, `${name}[][]=${a}`, `${name}[0][id]=${a}`,
    `${name}[toString]=${a}`, `${name}[length]=1`, `${name}[__proto__]=${a}`, `${name}%5B%5D=${a}`, `${name}=${a}&${name}[]=${b}`, `${name}[25]=${a}`, name.toUpperCase() + `=${a}`,
    `${name}=${Array.from({ length: 21 }, () => a).join(`&${name}=`)}`, `${name}[]=${Array.from({ length: 22 }, () => a).join(`&${name}[]=`)}`];
const alertQueries = [];
for (const text of texts) alertQueries.push(`device_id=${text}`, `severity=${text}`);
alertQueries.push(...forms('device_id', 'D1', 'D2'), ...forms('severity', 'critical', 'warning'));
for (const flag of ['true', 'false', 'TRUE', 'True', '1', '0', '', 'yes', 'no', '%20true', 'true%20', 'null', 'undefined']) alertQueries.push(`acknowledged=${flag}`);
alertQueries.push(...forms('acknowledged', 'true', 'false'));
alertQueries.push('device_id=D1&severity=critical', 'device_id=D1&severity=critical&acknowledged=true', 'device_id=D1&acknowledged=false', 'device_id=D2&severity=info&acknowledged=true&limit=1',
    'severity=warning&acknowledged=false&limit=2', 'device_id=MANY&acknowledged=true', 'device_id=MANY&acknowledged=false&limit=3', 'device_id=&severity=&acknowledged=&limit=',
    // An object binds nothing and a list one value per element: together they can fill the statement
    'device_id[id]=x&severity[]=D1&severity[]=critical', 'severity[id]=x&device_id[]=D1&device_id[]=critical', 'device_id[id]=x&severity[]=D1&severity[]=critical&severity[]=more',
    'device_id[]=D1&device_id[]=critical&severity[id]=x&acknowledged=true', 'device_id[]=MANY&device_id[]=info&severity[id]=x&limit=2',
    'other=1', 'device_id=D1&other=1', 'device_id=D1#severity=info');
const limits = ['', '0', '1', '2', '3', '5', '19', '20', '21', '100', '1.9', '0.9', '-1', '-0', '-2', '+2', '%2B2', '0x10', '0X2', '010', '2abc', 'abc', '%202', '2%20', '%092',
    '%C2%A02', '%E3%80%802', '%EF%BB%BF2', '1e3', '2e0', 'Infinity', '-Infinity', 'NaN', 'null', 'true', '2,3', '2;3', '2147483647', '2147483648', '4294967296',
    '9007199254740991', '9007199254740993', '9223372036854775807', '9223372036854775808', '99999999999999999999', '1e400', `${'9'.repeat(400)}`, '%00', '2%00', '%E2%82%AC'];
const limitQueries = limits.map((limit) => `limit=${limit}`);
limitQueries.push('limit=1&limit=2', 'limit=0&limit=2', 'limit=&limit=2', 'limit=abc&limit=2', 'limit[]=2', 'limit[]=', 'limit[]', 'limit', 'limit[0]=2', 'limit[1]=2',
    'limit[rows]=2', 'limit[toString]=2', 'limit[valueOf]=2', 'limit[][]=2', 'limit[0][rows]=2', 'limit[0][toString]=2', 'limit[]=2&limit[rows]=3', 'limit=2&limit[rows]=3',
    'limit[length]=2', 'LIMIT=2', 'limit[]=1&limit[]=2&limit[]=3', `limit=${Array.from({ length: 21 }, () => '2').join('&limit=')}`,
    `limit[]=${Array.from({ length: 1001 }, () => '2').join('&limit[]=')}`);
for (const query of [...alertQueries, ...limitQueries]) get(`${A}/alerts?${query}`);

// Acknowledging: any id, any number of times, whoever may
for (const [id, role] of [[1, 'admin'], [1, 'super_admin'], [2, 'super_admin'], [3, 'viewer'], [3, 'global_admin'], [5, 'admin'], [999, 'admin'], [0, 'admin']]) {
    step('POST', `${A}/alerts/${id}/ack`, {}, role);
}
step('POST', `${A}/alerts/6/ack`, { acknowledged_by: 'somebody else', id: 7 });
step('POST', `${A}/alerts/7/ack`, '[1]', 'admin', true);
step('POST', `${A}/alerts/8/ack`);
steps.push({ name: `${++n} POST ${A}/alerts/8/ack as a form`, role: 'admin', method: 'POST', path: `${A}/alerts/8/ack`, body: 'a=1', headers: { 'Content-Type': 'application/x-www-form-urlencoded' } });
steps.push({ name: `${++n} POST ${A}/alerts/8/ack as text`, role: 'admin', method: 'POST', path: `${A}/alerts/8/ack`, body: '{}', headers: { 'Content-Type': 'text/plain' } });
get(`${A}/alerts`);
get(`${A}/alerts?acknowledged=true`);
get(`${A}/alerts?acknowledged=false`);
sql('who acknowledged what', 'SELECT id, acknowledged_by FROM alert_history WHERE acknowledged_by IS NOT NULL ORDER BY id');

// Creating rules: the name, then every field with values of every type
for (const name of ['R', '  spaced out \t', '', '   ', '\u00a0\u2003\ufeff', null, 0, 5, true, false, [], ['x'], {}, { a: 1 }, { trim: 'x' }, 'Signs <b>&amp; "q" \\ é 😀 \u2028 \u0007',
    'x'.repeat(5000), 'password=secret token: abc']) {
    step('POST', `${A}/rules`, { name, condition_type: 'custom' });
}
step('POST', `${A}/rules`, {});
step('POST', `${A}/rules`, '[{"name":"In a list","condition_type":"custom"}]', 'admin', true);
step('POST', `${A}/rules`, '{"name":"Twice","name":"Twice, last wins","condition_type":"nonsense","condition_type":"custom"}', 'admin', true);
step('POST', `${A}/rules`, '{"name":"half \\ud83d pair","description":"half \\ude00 pair","condition_type":"custom"}', 'admin', true);
step('POST', `${A}/rules`, '{"name":"nul \\u0000 inside","condition_type":"custom"}', 'admin', true);
step('POST', `${A}/rules`, '{"name":"nul in the description","description":"a\\u0000b","condition_type":"custom"}', 'admin', true);
steps.push({ name: `${++n} POST ${A}/rules as a form`, role: 'admin', method: 'POST', path: `${A}/rules`, body: 'name=Form&condition_type=custom', headers: { 'Content-Type': 'application/x-www-form-urlencoded' } });
steps.push({ name: `${++n} POST ${A}/rules as text`, role: 'admin', method: 'POST', path: `${A}/rules`, body: '{"name":"Text","condition_type":"custom"}', headers: { 'Content-Type': 'text/plain' } });
steps.push({ name: `${++n} POST ${A}/rules with broken JSON`, role: 'admin', method: 'POST', path: `${A}/rules`, body: '{"name":' });
for (const type of ['cpu_usage', 'memory_usage', 'disk_usage', 'offline_duration', 'idle_duration', 'custom', 'CUSTOM', ' custom', 'custom ', 'nonsense', '', null, 0, 5, true, false,
    [], ['custom'], [['custom']], {}, { custom: 1 }]) {
    step('POST', `${A}/rules`, { name: 'Type', condition_type: type });
}
step('POST', `${A}/rules`, { name: 'No type' });
for (const op of ['gt', 'gte', 'lt', 'lte', 'eq', 'neq', 'GT', ' gt', '>', 'nonsense', '', null, 0, 5, true, false, [], ['gt'], {}, { gt: 1 }]) {
    step('POST', `${A}/rules`, { name: 'Op', condition_type: 'custom', condition_op: op });
}
// The checks in their order: the name, the type, the comparison
step('POST', `${A}/rules`, { name: '', condition_type: 'nonsense', condition_op: 'nonsense' });
step('POST', `${A}/rules`, { name: 5, condition_type: 'nonsense', condition_op: 'nonsense' });
step('POST', `${A}/rules`, { name: 'x', condition_type: 'nonsense', condition_op: 'nonsense' });
step('POST', `${A}/rules`, { name: 'x', condition_type: 'custom', condition_op: 'nonsense', description: true });

let serial = 0;
const fresh = () => `Rule ${++serial}`;
const values = [0, '', 77, 1.5, -1, 300, 1e21, 1e-7, true, false, null, [], ['one'], ['a', 'b'], [5], [null], [[1]], [true], [{}], {}, { a: 1 },
    'plain text', ' 12 ', '12', '0x10', '1e3', '1.5', 'abc', '99999999999', 99999999999, 2147483647, 2147483648, -2147483649, 3.4e38, 3.5e38, 1e39, 16777217, 0.1,
    { b: [1, { c: null }], 2: 'x', a: 1.0 }, [1, [2, 'x"y\\z'], null, 'q'], 'true', 'false', '0', '1', 'info', 'warning', 'critical', 'D1', 'NaN', 'Infinity', '-Infinity',
    't', 'f', 'yes', 'no', 'on', 'off', ' true ', 'TRUE', '{}', '{"a":1}', '[1]', "it's", 'a,b', 'a"b', 'a\\b', '{a,b}'];
const FIELDS = ['description', 'condition_value', 'severity', 'scope_device_id', 'cooldown_secs', 'notify_emails', 'enabled'];
for (const field of FIELDS) {
    for (const value of values) step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', [field]: value });
}
for (const raw of ['"condition_value":1e400', '"condition_value":-1e400', '"condition_value":-0', '"condition_value":0.0', '"condition_value":1.0', '"condition_value":12345678901234567890',
    '"condition_value":1E2', '"condition_value":0.30000000000000004', '"cooldown_secs":1e400', '"cooldown_secs":-0', '"cooldown_secs":3e2', '"cooldown_secs":300.0',
    '"cooldown_secs":12345678901234567890', '"description":1e400', '"description":-0', '"description":1.0', '"description":1e21', '"description":0.000001', '"description":1e-7',
    '"scope_device_id":123456789012345678901234567890', '"scope_device_id":1.10', '"enabled":-0', '"enabled":0.0', '"enabled":0e5', '"enabled":"0.0"',
    '"description":{"b":1,"a":2,"10":"x","2":"y","a":3}', '"description":[1.0,1e2,"\\u00e9",{"z":null}]', '"notify_emails":"\\ud83d"', '"description":"\\u2028\\u2029"']) {
    step('POST', `${A}/rules`, `{"name":"${fresh()}","condition_type":"custom",${raw}}`, 'admin', true);
}
// A list gives SQLite one value per element, an object none: the rest moves up
step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', description: {}, notify_emails: ['a', 'b'] });
step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', description: ['a', 'b'], notify_emails: {} });
step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', description: ['d', 0, 'cpu_usage', 'lt', 5, 'info', null, 60, 'n', 'someone else'], condition_value: {}, severity: 'critical', scope_device_id: {},
    cooldown_secs: {}, notify_emails: {} });
step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', description: {}, scope_device_id: {} });
step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', description: ['a', ['b']], notify_emails: {} });
step('POST', `${A}/rules`, { name: fresh(), condition_type: 'custom', description: ['a', true], notify_emails: {} });
// Everything at once
step('POST', `${A}/rules`, { name: '  Everything ', description: 'all fields', condition_type: 'offline_duration', condition_op: 'lte', condition_value: 12.5, severity: 'info',
    scope_device_id: 'D2', cooldown_secs: 60, notify_emails: 'a@example.com', enabled: false, id: 5, created_by: 'somebody else', created_at: 'x', unknown: 1 });
spread('alert_rules');
get(`${A}/rules`);
sql('the rules as they are stored', `SELECT id, name, ${pg ? 'enabled::int AS enabled' : 'enabled'}, condition_type, condition_op, severity, created_by FROM alert_rules ORDER BY id`);
sql('the text columns of the rules', 'SELECT id, description, scope_device_id, notify_emails FROM alert_rules ORDER BY id');

// Updating rule 2: every field with every value, then the row
step('PATCH', `${A}/rules/2`, {});
step('PATCH', `${A}/rules/2`, { unknown: true });
step('PATCH', `${A}/rules/2`, '[{"description":"In a list"}]', 'admin', true);
steps.push({ name: `${++n} PATCH ${A}/rules/2 without a body`, role: 'admin', method: 'PATCH', path: `${A}/rules/2` });
get(`${A}/rules/2`);
for (const field of ['name', 'condition_type', 'condition_op', ...FIELDS]) {
    for (const value of values) {
        step('PATCH', `${A}/rules/2`, { [field]: value });
        get(`${A}/rules/2`);
    }
    sql(`rule 2 after the updates of ${field}`, `UPDATE alert_rules SET name = 'Patch me', description = 'before', enabled = ${pg ? 'FALSE' : '0'}, condition_type = 'memory_usage', condition_op = 'lt',
        condition_value = 10, severity = 'warning', scope_device_id = 'D2', cooldown_secs = 300, notify_emails = '', updated_at = ${pg ? "TIMESTAMPTZ '2019-03-01 09:00:02+00'" : "'2019-03-01 09:00:02'"} WHERE id = 2`);
}
for (const type of ['cpu_usage', 'memory_usage', 'disk_usage', 'offline_duration', 'idle_duration', 'custom', 'CUSTOM', 'nonsense']) step('PATCH', `${A}/rules/2`, { condition_type: type });
for (const op of ['gt', 'gte', 'lt', 'lte', 'eq', 'neq', 'GT', '>']) step('PATCH', `${A}/rules/2`, { condition_op: op });
get(`${A}/rules/2`);
for (const body of [
    // The checks in their order: the rule, the name, the type, the comparison
    { name: 5, condition_type: 'nonsense', condition_op: 'nonsense' }, { name: 'x', condition_type: 'nonsense', condition_op: 'nonsense' }, { name: 'x', condition_op: 'nonsense', description: 'not written' },
    { name: '  Renamed ', description: 'after', enabled: true, condition_type: 'cpu_usage', condition_op: 'gte', condition_value: 95.5, severity: 'critical', scope_device_id: null,
        cooldown_secs: 900, notify_emails: 'ops@example.com' },
    { name: '' }, { name: '   ' }, { id: 99, created_by: 'x', created_at: 'x', updated_at: 'x' },
    { description: {}, notify_emails: ['a', 'b'] }, { description: ['a', 'b'], notify_emails: {} }, { description: {}, severity: {} }, { description: ['a', 'b', 'c'], severity: {}, notify_emails: {} },
    { description: ['only one'] }, { description: [] }, { description: [], severity: ['s', 't'] }, { enabled: {}, description: {} }, { enabled: [], description: ['x'] },
    { severity: null, scope_device_id: null, notify_emails: null, description: null, cooldown_secs: null }, { condition_value: null }, { enabled: null }]) {
    step('PATCH', `${A}/rules/2`, body);
    get(`${A}/rules/2`);
}
step('PATCH', `${A}/rules/2`, '{"description":"Twice","description":"Twice, last wins"}', 'admin', true);
step('PATCH', `${A}/rules/2`, '{"condition_value":1e400}', 'admin', true);
step('PATCH', `${A}/rules/2`, '{"cooldown_secs":1e400}', 'admin', true);
step('PATCH', `${A}/rules/2`, '{"name":"half \\ud83d pair"}', 'admin', true);
step('PATCH', `${A}/rules/2`, '{"description":"a\\u0000b"}', 'admin', true);
get(`${A}/rules/2`);
step('PATCH', `${A}/rules/999`, { name: 5, condition_type: 'nonsense' });
step('PATCH', `${A}/rules/999`, {});

// Sending commands: the device, the payload, the type
const things = [0, '', 77, 1.5, -1, 1e21, 1e-7, true, false, null, [], ['one'], ['a', 'b'], [5], [null], [[1]], [[1, 2], [3]], [true], [{}], [{ toString: 1 }], {}, { a: 1 }, { toString: 1 }, { valueOf: 1 },
    { toString: null }, { toString: 'x', valueOf: 'y' }, { b: [1, { c: null }], 2: 'x', a: 1.0 }, [1, [2, 'x"y\\z'], null, 'q'], 'D1', ' D1 ', '12', 'abc', 'Signs <b>&amp; "q" \\ é 😀 \u2028 \u0007',
    'password=secret', 'x'.repeat(3000)];
for (const device of things) step('POST', `${A}/commands`, { device_id: device, payload: 'whoami' });
for (const payload of [...things, '   ', ' \n\t ', '  padded  ', '\u00a0\u2003x\ufeff', { trim: 'x' }, { trim: null }]) step('POST', `${A}/commands`, { device_id: 'D4', payload });
for (const type of ['shell', 'powershell', 'script', 'restart_service', 'reboot', 'SHELL', ' shell', 'nonsense', '', null, 0, 5, true, false, [], ['shell'], {}, { shell: 1 }]) {
    step('POST', `${A}/commands`, { device_id: 'D5', payload: 'whoami', command_type: type });
}
step('POST', `${A}/commands`, {});
step('POST', `${A}/commands`, '[{"device_id":"D1","payload":"whoami"}]', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":"first","device_id":"last wins","payload":"whoami"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":"half \\ud83d pair","payload":"half \\ude00 pair"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":"nul","payload":"a\\u0000b"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":1e400,"payload":"whoami"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":12345678901234567890,"payload":"whoami"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":1.0,"payload":"whoami"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":-0,"payload":"whoami"}', 'admin', true);
step('POST', `${A}/commands`, '{"device_id":0.0,"payload":"whoami"}', 'admin', true);
// The checks in their order: the device, the payload, the type
step('POST', `${A}/commands`, { device_id: '', payload: 5, command_type: 'nonsense' });
step('POST', `${A}/commands`, { device_id: 'D1', payload: 5, command_type: 'nonsense' });
step('POST', `${A}/commands`, { device_id: 'D1', payload: '', command_type: 'nonsense' });
step('POST', `${A}/commands`, { device_id: 'D1', payload: 'x', command_type: 'nonsense' });
step('POST', `${A}/commands`, { device_id: true, payload: 'x', command_type: 'script' });
// A list gives SQLite one value per element
step('POST', `${A}/commands`, { device_id: ['D6', 'script', 'from the list', 'somebody else'], command_type: 'reboot', payload: 'x' });
step('POST', `${A}/commands`, { device_id: 'D6', payload: 'everything', command_type: 'script', id: 5, status: 'completed', result: 'x', created_by: 'somebody else', created_at: 'x' });
spread('remote_commands');
get(`${A}/commands`);
sql('the commands as they are stored', 'SELECT id, device_id, command_type, payload, status, result, created_by FROM remote_commands ORDER BY id');

// The command list with every filter
const commandQueries = [];
for (const text of ['D1', 'D2', 'D3', 'D4', 'ODD', '7', '7.0', '77.0', 'nobody', '', '%20', 'd1', 'pending', 'running', 'completed', 'failed', 'PENDING', 'null', '%00', "x'%20OR%201=1--"]) {
    commandQueries.push(`device_id=${text}`, `status=${text}`);
}
commandQueries.push(...forms('device_id', 'D1', 'D2'), ...forms('status', 'pending', 'running'));
commandQueries.push('device_id=D1&status=pending', 'device_id=D1&status=completed&limit=1', 'device_id=D3&limit=2', 'status=pending&limit=3', 'device_id=&status=&limit=',
    'device_id[id]=x&status[]=D1&status[]=pending', 'status[id]=x&device_id[]=D1&device_id[]=running', 'device_id[]=D3&device_id[]=pending&status[id]=x&limit=2', 'other=1');
for (const query of [...commandQueries, ...limitQueries]) get(`${A}/commands?${query}`);
for (let id = 1; id <= COMMANDS + 60; id += 7) get(`${A}/commands/${id}`);

// Ids in every spelling, on every route that takes one. The deletes come
// last: the first id that is the number 1 removes rule 1 and its alerts.
const ids = ['1', '01', '001', '0000000000000000000000000000001', '%31', '1%20', '%201', '%EF%BC%91', '%D9%A1', '1.0', '1e0', '+1', '-1', '0x1', '0', '00', '-0', 'abc', '999',
    '2147483647', '2147483648', '3000000000', '4294967297', '9007199254740992', '9007199254740993', '9223372036854775807', '9223372036854775808', '99999999999999999999',
    `1${'0'.repeat(30)}`, `1${'0'.repeat(400)}`, '9'.repeat(400), '1e3', '%20', '1;x', '1%2C2', '1_0', '1%2F', '1%00', 'null', 'true', 'NaN', 'Infinity'];
if (!pg) {
    // Rows with ids no JavaScript number holds exactly: the nearest number finds them
    sql('a rule with a large id', 'INSERT INTO alert_rules (id, name, condition_type, created_at) VALUES (9007199254740992, ?, ?, ?)', ['Large id', 'custom', own(1, '08:00:13')]);
    sql('a command with a large id', 'INSERT INTO remote_commands (id, device_id, payload, created_by, created_at) VALUES (9007199254740992, ?, ?, ?, ?)', ['LARGE', 'x', 'someone', own(2, '08:00:08')]);
    sql('an alert with a large id', 'INSERT INTO alert_history (id, rule_id, message, triggered_at) VALUES (9007199254740992, 9, ?, ?)', ['large id', iso(3, '10:00:00')]);
}
for (const id of ids) {
    get(`${A}/rules/${id}`);
    step('PATCH', `${A}/rules/${id}`, { description: `set by ${ids.indexOf(id)}` });
    step('PATCH', `${A}/rules/${id}`, { name: 5 });
    step('POST', `${A}/alerts/${id}/ack`, {}, 'super_admin');
    get(`${A}/commands/${id}`);
}
get(`${A}/rules/1`);
sql('who acknowledged what by then', 'SELECT id, acknowledged_by FROM alert_history WHERE acknowledged_by IS NOT NULL ORDER BY id');
for (const id of ids) step('DELETE', `${A}/rules/${id}`);
step('DELETE', `${A}/rules/1`);
get(`${A}/rules/1`);
get(`${A}/alerts?device_id=D1`);
get(`${A}/alerts`);
// Deleting as every role; a rule without alerts; a rule that was never there
for (const role of ['guest_issuer', 'viewer', 'operator', 'global_admin', 'super_admin', 'server_admin']) step('DELETE', `${A}/rules/5`, undefined, role);
step('DELETE', `${A}/rules/9`, { name: 'a body nobody reads' });
step('DELETE', `${A}/rules/9`);
sql('the rules that are left', 'SELECT id FROM alert_rules WHERE id < 30 ORDER BY id');
sql('the alerts that are left', 'SELECT id, rule_id FROM alert_history ORDER BY id');
sql('the audit log', 'SELECT user_id, action, details FROM audit_log ORDER BY id');

record({ routes: process.env.ORACLE_ROUTES || 'routes/automation.routes', mounts: ['/api/automation', '/api/bd'], seed, steps, out: 'node' }).catch((err) => {
    console.error(err);
    process.exit(1);
});
