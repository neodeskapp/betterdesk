'use strict';

/**
 * Records what the device routes of routes/tickets.routes.js (POST and GET
 * /api/tickets/bd, behind requireDeviceToken) answer with the real database
 * adapter, on SQLite and (with ORACLE_PG_DSN) on PostgreSQL. Writes
 * ../node_device_<dialect>.json. See README.md.
 */

const crypto = require('crypto');
const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const at = (minute) => `2001-01-01 00:${String(minute).padStart(2, '0')}:00${pg ? '+00' : ''}`;
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');

const TOKEN = 'INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)';
const TICKET = 'INSERT INTO tickets (title, description, status, priority, category, device_id, created_by, assigned_to, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)';
const FUTURE = '2999-01-01 00:00:00';
const token = (text, user, client, expires = FUTURE, revoked = false) => [TOKEN, [text, sha(text), user, client, `uuid-${text}`, expires, pg ? revoked : (revoked ? 1 : 0)]];
const seed = [
    token('tok-device-a', 5, 'device-a'),
    token('tok-device-b', 6, 'device-b'),
    token('tok-numeric', 1, '123'),
    token('tok-unbound', 5, ''),
    token('tok-expired', 5, 'device-a', '2001-01-01 00:00:00'),
    token('tok-revoked', 5, 'device-a', FUTURE, true),
    // A ticket a person opened about device-a, and one about another device
    [TICKET, ['Screen flickers', 'seen by the help desk', 'in_progress', 'high', 'hardware', 'device-a', 'e2e_admin', 'e2e_operator', at(1), at(1)]],
    [TICKET, ['Other device', null, 'open', 'low', 'other', 'device-z', 'e2e_admin', null, at(2), at(2)]],
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
const sql = (name, text, params) => steps.push({ name, sql: text, params });
// New tickets get the time of the run; lists are ordered by it
const spread = () => sql('spread the new tickets out in time', pg
    ? "UPDATE tickets SET created_at = TIMESTAMPTZ '2001-01-01 01:00:00+00' + id * INTERVAL '1 minute', updated_at = TIMESTAMPTZ '2001-01-01 01:00:00+00' + id * INTERVAL '1 minute' WHERE created_by LIKE 'agent:%'"
    : "UPDATE tickets SET created_at = datetime('2001-01-01 01:00:00', '+' || id || ' minutes'), updated_at = datetime('2001-01-01 01:00:00', '+' || id || ' minutes') WHERE created_by LIKE 'agent:%'", []);
const rows = () => sql('the tickets', 'SELECT id, title, description, status, priority, category, device_id, created_by, assigned_to FROM tickets ORDER BY id', []);
const sla = () => sql('the time each new ticket is due, in hours after its creation', pg
    ? "SELECT id, CAST(ROUND(EXTRACT(EPOCH FROM (sla_due_at - NOW())) / 3600) AS INTEGER) AS hours FROM tickets WHERE created_by LIKE 'agent:%' ORDER BY id"
    : "SELECT id, CAST(ROUND((julianday(sla_due_at) - julianday('now')) * 24) AS INTEGER) AS hours FROM tickets WHERE created_by LIKE 'agent:%' ORDER BY id", []);

// Who may call at all
step('GET', '/api/tickets/bd', undefined, {});
step('GET', '/api/tickets/bd', undefined, { 'X-Device-Id': 'device-a' });
step('GET', '/api/tickets/bd', undefined, { Authorization: 'Bearer nope' });
step('GET', '/api/tickets/bd', undefined, { Authorization: 'Bearer tok-unbound' });
step('GET', '/api/tickets/bd', undefined, { Authorization: 'Bearer tok-expired' });
step('GET', '/api/tickets/bd', undefined, { Authorization: 'Bearer tok-revoked' });
step('GET', '/api/tickets/bd', undefined, { Authorization: 'Basic tok-device-a' });
step('GET', '/api/tickets/bd', undefined, {}, 'admin');
step('POST', '/api/tickets/bd', { title: 'From a session' }, {}, 'admin');
step('POST', '/api/tickets/bd', { title: 'From nobody' }, {});

// The list before the device opened any: the ticket a person opened about it
step('GET', '/api/tickets/bd');
step('GET', '/api/tickets/bd/');
step('GET', '/API/Tickets/BD');
step('GET', '/api/tickets/bd?device_id=device-z&status=open');
step('GET', '/api/tickets/bd', undefined, { ...A, 'X-Device-Id': 'device-z' });
step('GET', '/api/tickets/bd', undefined, B);
step('GET', '/api/tickets/bd', undefined, A, 'viewer');

// Creating, with every kind of value
const bodies = [
    {},
    { title: '' },
    { title: '   ' },
    { title: null },
    { title: 0 },
    { title: false },
    { title: 5 },
    { title: true },
    { title: [] },
    { title: ['a'] },
    { title: {} },
    { title: 'Printer offline' },
    { title: '  Spaces around  ', description: 'with a description' },
    { title: 'Priority high', priority: 'high', category: 'network' },
    { title: 'Priority critical', priority: 'critical', category: 'access' },
    { title: 'Priority low', priority: 'low', category: 'software' },
    { title: 'Priority odd', priority: 'urgent', category: 'printers' },
    { title: 'Priority upper', priority: 'HIGH', category: 'Hardware' },
    { title: 'Priority number', priority: 1, category: 2 },
    { title: 'Priority list', priority: ['high'], category: ['other'] },
    { title: 'Description number', description: 7 },
    { title: 'Description zero', description: 0 },
    { title: 'Description false', description: false },
    { title: 'Description null', description: null },
    { title: 'Description list', description: ['a', 'b'] },
    { title: 'Description object', description: { a: 1 } },
    { title: 'Description true', description: true },
    { title: 'Fields that are not read', status: 'closed', device_id: 'device-z', assigned_to: 'e2e_admin', created_by: 'someone', sla_due_at: '2000-01-01' },
    { title: 'Ünïcödé 🎫 title', description: 'line\nbreak' },
    { title: 'x'.repeat(300) },
];
for (const body of bodies) step('POST', '/api/tickets/bd', body);
step('POST', '/api/tickets/bd', '[{"title":"a list"}]');
step('POST', '/api/tickets/bd', 'title=form', A);
steps[steps.length - 1].headers = { ...A, 'Content-Type': 'application/x-www-form-urlencoded' };
step('POST', '/api/tickets/bd', { title: 'From device-b', priority: 'medium' }, B);
step('POST', '/api/tickets/bd', { title: 'From a device with a number for an id' }, { Authorization: 'Bearer tok-numeric' });
step('POST', '/api/tickets/bd', { title: 'With a session too' }, A, 'operator');
step('POST', '/api/tickets/bd', { title: 'Device named in the header' }, { ...A, 'X-Device-Id': 'device-b' });
rows();
sla();
spread();

// The lists after
step('GET', '/api/tickets/bd');
step('GET', '/api/tickets/bd', undefined, B);
step('GET', '/api/tickets/bd', undefined, { Authorization: 'Bearer tok-numeric' });
step('HEAD', '/api/tickets/bd');

record({ routes: 'routes/tickets.routes', mounts: ['/api/tickets'], seed, steps, out: 'node_device' }).catch((err) => {
    console.error(err);
    process.exit(1);
});
