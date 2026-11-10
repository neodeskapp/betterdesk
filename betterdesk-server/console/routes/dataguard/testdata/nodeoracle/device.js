'use strict';

/**
 * Records what the device routes of routes/dataguard.routes.js (GET
 * dlp-policies and POST dlp-events, behind requireDeviceToken) answer at
 * both mounts with the real database adapter, on SQLite and (with
 * ORACLE_PG_DSN) on PostgreSQL. Writes ../node_device_<dialect>.json. See
 * README.md.
 */

const crypto = require('crypto');
const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');

const TOKEN = 'INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)';
const POLICY = 'INSERT INTO dlp_policies (id, name, enabled, rules) VALUES (?, ?, ?, ?)';
const FUTURE = '2999-01-01 00:00:00';
const token = (text, user, client, expires = FUTURE) => [TOKEN, [text, sha(text), user, client, `uuid-${text}`, expires, pg ? false : 0]];

// Policies with every value of enabled and rules each database can hold
const policies = pg
    ? [
        [1, 'usb block', true, '[{"type":"usb","action":"block"}]'],
        [2, 'off, rules no list', false, '"not json"'],
        [3, 'enabled null', null, '[]'],
        [4, 'rules null', true, null],
        [5, 'rules a JSON null', true, 'null'],
        [6, 'rules zero', true, '0'],
        [7, 'rules a text of a list', true, '"[1,2]"'],
        [8, 'rules an object', true, '{"zeta":1,"alpha":{"b":2,"a":1}}'],
        [9, 'off', false, '[{"type":"file"}]'],
        [10, 'Ünïcödé', true, '[{"path":"C:\\\\Users","ext":[".docx",".xlsx"]}]'],
    ]
    : [
        [1, 'usb block', 1, '[{"type":"usb","action":"block"}]'],
        [2, 'off, rules no JSON', 0, 'not json'],
        [3, 'enabled null', null, '[]'],
        [4, 'rules null', 1, null],
        [5, 'rules a JSON null', 1, 'null'],
        [6, 'rules zero', 1, '0'],
        [7, 'rules a text of a list', 1, '"[1,2]"'],
        [8, 'rules an object', 1, '{"zeta":1,"alpha":{"b":2,"a":1}}'],
        [9, 'enabled two', 2, '[{"type":"file"}]'],
        [10, 'enabled a fraction', 0.5, '[]'],
        [11, 'enabled text', 'yes', '[3]'],
        [12, 'enabled text zero', '0', '[4]'],
        [13, 'enabled minus one', -1, '[5]'],
        [14, 'Ünïcödé', 1, '[{"path":"C:\\\\Users","ext":[".docx",".xlsx"]}]'],
        [15, 'rules with spaces', 1, ' [ 1 , {"b" : 2} ] '],
    ];
const seed = [
    token('tok-device-a', 5, 'device-a'),
    token('tok-device-b', 6, 'device-b'),
    token('tok-unbound', 5, ''),
    token('tok-expired', 5, 'device-a', '2001-01-01 00:00:00'),
    ...policies.map((p) => [POLICY, p]),
];

const steps = [];
let n = 0;
const A = { Authorization: 'Bearer tok-device-a' };
const B = { Authorization: 'Bearer tok-device-b' };
function step(method, url, body, headers = A, role = '') {
    n += 1;
    const s = { name: `${n} ${method} ${url}`, role, method, path: url, headers };
    if (body !== undefined) s.body = typeof body === 'string' ? body : J(body);
    steps.push(s);
}
const sql = (name, text, params = []) => steps.push({ name, sql: text, params });
const events = () => sql('the events', `SELECT id, device_id, event_source, event_type, policy_id, policy_name, action, ${pg ? 'details::text AS details' : 'details'} FROM dlp_events ORDER BY id`);

for (const mount of ['/api/bd', '/api/dataguard']) {
    // Who may call at all
    for (const headers of [{}, { 'X-Device-Id': 'device-a' }, { Authorization: 'Bearer nope' }, { Authorization: 'Bearer tok-unbound' }, { Authorization: 'Bearer tok-expired' }]) {
        step('GET', `${mount}/dlp-policies`, undefined, headers);
        step('POST', `${mount}/dlp-events`, { event_source: 'usb' }, headers);
    }
    step('GET', `${mount}/dlp-policies`, undefined, {}, 'admin');
    step('POST', `${mount}/dlp-events`, { event_source: 'usb' }, {}, 'admin');

    // The policies of a device
    step('GET', `${mount}/dlp-policies`);
    step('GET', `${mount}/dlp-policies`, undefined, B);
    step('GET', `${mount}/dlp-policies?device_id=device-b`, undefined, { ...A, 'X-Device-Id': 'device-b' });
    step('GET', `${mount.toUpperCase()}/DLP-POLICIES/`);
    step('HEAD', `${mount}/dlp-policies`);
    step('GET', `${mount}/dlp-policies`, undefined, A, 'operator');
}

// Events with every kind of value, at /api/bd
const bodies = [
    {},
    { event_source: '' },
    { event_source: 5 },
    { event_source: true },
    { event_source: ['usb'] },
    { event_source: { a: 1 } },
    { event_source: null },
    { event_source: 'usb' },
    { event_source: 'file', event_type: 'violation', policy_id: 1, policy_name: 'usb block', action: 'block', details: { path: 'E:\\', size: 12 } },
    { event_source: 'usb', device_id: 'device-z' },
    { event_source: 'usb', event_type: '' },
    { event_source: 'usb', event_type: 0 },
    { event_source: 'usb', event_type: 7 },
    { event_source: 'usb', event_type: true },
    { event_source: 'usb', event_type: ['a'] },
    { event_source: 'usb', event_type: { a: 1 } },
    { event_source: 'usb', policy_id: 0 },
    { event_source: 'usb', policy_id: '1' },
    { event_source: 'usb', policy_id: 'abc' },
    { event_source: 'usb', policy_id: 999999 },
    { event_source: 'usb', policy_id: 1.5 },
    { event_source: 'usb', policy_id: [1] },
    { event_source: 'usb', policy_name: 12 },
    { event_source: 'usb', policy_name: false },
    { event_source: 'usb', action: '' },
    { event_source: 'usb', action: 'allow' },
    { event_source: 'usb', action: { a: 1 } },
    { event_source: 'usb', details: '' },
    { event_source: 'usb', details: null },
    { event_source: 'usb', details: 0 },
    { event_source: 'usb', details: false },
    { event_source: 'usb', details: 'plain text' },
    { event_source: 'usb', details: '{"as":"text"}' },
    { event_source: 'usb', details: 42 },
    { event_source: 'usb', details: true },
    { event_source: 'usb', details: [1, 'two', null] },
    { event_source: 'usb', details: { b: 1, a: { d: 2, c: 'é' } } },
    { event_source: 'usb', details: { __proto__x: 1, constructor: 2 } },
    { event_source: 'Ünïcödé 🎫', event_type: 'line\nbreak' },
];
for (const body of bodies) step('POST', '/api/bd/dlp-events', body);
step('POST', '/api/bd/dlp-events', '[{"event_source":"usb"}]');
step('POST', '/api/bd/dlp-events', '{"event_source":"usb","details":{"__proto__":{"x":1}}}');
step('POST', '/api/bd/dlp-events', 'event_source=usb');
steps[steps.length - 1].headers = { ...A, 'Content-Type': 'application/x-www-form-urlencoded' };
step('POST', '/api/bd/dlp-events', { event_source: 'usb' }, B);
step('POST', '/api/dataguard/dlp-events', { event_source: 'file', details: { at: 'second mount' } });
step('POST', '/api/bd/dlp-events', { event_source: 'usb' }, A, 'operator');
events();

// A policy that is on with rules that are no JSON makes the list fail
sql('turn the policy with broken rules on', 'UPDATE dlp_policies SET enabled = ? WHERE id = 2', [pg ? true : 1]);
step('GET', '/api/bd/dlp-policies');
step('GET', '/api/dataguard/dlp-policies');

record({ routes: 'routes/dataguard.routes', mounts: ['/api/dataguard', '/api/bd'], seed, steps, out: 'node_device' }).catch((err) => {
    console.error(err);
    process.exit(1);
});
