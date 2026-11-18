'use strict';

/**
 * Records what the device routes of routes/automation.routes.js (GET
 * /commands and POST /commands/:id(\d+)/result behind requireDeviceToken)
 * answer with the real database adapter, on SQLite and (with
 * ORACLE_PG_DSN) on PostgreSQL, and what they leave in the database.
 * Writes ../node_device_<dialect>.json. See README.md.
 */

const crypto = require('crypto');
const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');
const at = (minute) => `2001-01-01 00:${String(minute).padStart(2, '0')}:00${pg ? '+00' : ''}`;

const TOKEN = 'INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)';
const FUTURE = '2999-01-01 00:00:00';
function token(text, client, { expires = FUTURE, revoked = false, hashed = true } = {}) {
    return [TOKEN, [text, hashed ? sha(text) : null, 5, client, `uuid-${text}`, expires, pg ? revoked : (revoked ? 1 : 0)]];
}
const COMMAND = 'INSERT INTO remote_commands (device_id, command_type, payload, status, result, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)';
const seed = [
    token('tok-device-a', 'device-a'),
    token('tok-device-b', 'device-b'),
    token('tok-numeric', '123'),
    token('tok-unbound', ''),
    token('tok-null-client', null),
    token('tok-short', 'x'),
    token('tok-spaced', 'dev a'),
    token('tok-expired', 'device-a', { expires: '2001-01-01 00:00:00' }),
    token('tok-revoked', 'device-a', { revoked: true }),
    token('tok-plain', 'device-p', { hashed: false }),
    // 1 to 9; the two pending commands of device-a are listed oldest first
    [COMMAND, ['device-a', 'shell', 'hostname', 'pending', null, 'e2e_admin', at(2)]],
    [COMMAND, ['device-a', 'powershell', 'Get-Process', 'pending', null, 'e2e_operator', at(1)]],
    [COMMAND, ['device-a', 'shell', 'uptime', 'running', null, 'e2e_admin', at(3)]],
    [COMMAND, ['device-a', 'reboot', 'now', 'completed', 'done', 'e2e_admin', at(4)]],
    [COMMAND, ['device-b', 'shell', 'whoami', 'pending', null, 'e2e_admin', at(5)]],
    [COMMAND, ['123', 'shell', 'id', 'pending', null, 'e2e_admin', at(6)]],
    [COMMAND, ['device-a ', 'shell', 'spaced', 'pending', null, 'e2e_admin', at(7)]],
    [COMMAND, ['DEVICE-A', 'shell', 'upper', 'pending', null, 'e2e_admin', at(8)]],
    [COMMAND, ['device-a', 'script', 'echo 1', 'failed', 'exit 1', 'e2e_admin', at(9)]],
];

const steps = [];
let n = 0;
const A = { Authorization: 'Bearer tok-device-a' };
function step(method, url, body, headers = A, role = '') {
    n += 1;
    const s = { name: `${n} ${method} ${url}`, role, method, path: url, headers };
    if (body !== undefined) s.body = typeof body === 'string' ? body : J(body);
    steps.push(s);
}
const sql = (name, text, params) => steps.push({ name, sql: text, params: params || [] });
const nulls = pg ? 'CASE WHEN executed_at IS NULL THEN 1 ELSE 0 END AS executed, CASE WHEN completed_at IS NULL THEN 1 ELSE 0 END AS completed'
    : 'executed_at IS NULL AS executed, completed_at IS NULL AS completed';
const command = (id) => sql(`command ${id}`, `SELECT id, device_id, status, result, ${nulls} FROM remote_commands WHERE id = ?`, [id]);
const reset = (id) => sql(`command ${id} is pending again`, 'UPDATE remote_commands SET status = ?, result = NULL, executed_at = NULL, completed_at = NULL WHERE id = ?', ['pending', id]);

// Who may call at all, at both mounts
const tokens = [
    {}, { 'X-Device-Id': 'device-a' }, { Authorization: 'Bearer nope' }, { Authorization: 'Bearer tok-unbound' }, { Authorization: 'Bearer tok-null-client' },
    { Authorization: 'Bearer tok-short' }, { Authorization: 'Bearer tok-spaced' }, { Authorization: 'Bearer tok-expired' }, { Authorization: 'Bearer tok-revoked' },
    { Authorization: 'Basic tok-device-a' }, { Authorization: 'bearer tok-device-a' }, { Authorization: 'Bearer  tok-device-a' },
    { Authorization: 'Bearer tok-device-a more' }, { Authorization: 'Bearer tok-plain' },
];
for (const headers of tokens) {
    step('GET', '/api/bd/commands', undefined, headers);
    step('POST', '/api/bd/commands/5/result', { status: 'nope' }, headers);
    step('POST', '/api/automation/commands/5/result', { status: 'nope' }, headers);
}
step('GET', '/api/bd/commands', undefined, {}, 'admin');

// The pending commands of a device, oldest first; what the request names does not count
step('GET', '/api/bd/commands');
step('GET', '/api/bd/commands/');
step('GET', '/API/BD/COMMANDS');
step('GET', '/api/bd/commands?device_id=device-b&status=completed');
step('GET', '/api/bd/commands', undefined, { ...A, 'X-Device-Id': 'device-b' });
step('GET', '/api/bd/commands', undefined, { Authorization: 'Bearer tok-device-b' });
step('GET', '/api/bd/commands', undefined, { Authorization: 'Bearer tok-numeric' });
step('GET', '/api/bd/commands', undefined, { Authorization: 'Bearer tok-plain' });
step('HEAD', '/api/bd/commands');

// A result: the status first, then the command, then its device
const statuses = [undefined, null, '', 0, false, 'completed', 'failed', 'running', 'pending', 'COMPLETED', ' running', ['completed'], ['running', 'failed'], 1, true, {}];
for (const status of statuses) {
    step('POST', '/api/bd/commands/1/result', { status, result: 'r' });
    command(1);
    reset(1);
    step('POST', '/api/bd/commands/999/result', { status });
    step('POST', '/api/bd/commands/5/result', { status });
}
// The result with a value of every type
const results = [undefined, null, 0, -0, '', 'text', 5.5, 1e21, true, false, [], [1, [2, null]], [null], {}, { a: 1 }, { toString: 1 }, [{ toString: 1 }],
    `caf${String.fromCodePoint(0xe9)} ${String.fromCodePoint(0x1F600)}`, 'x'.repeat(500)];
for (const result of results) {
    step('POST', '/api/bd/commands/2/result', { status: 'running', result });
    command(2);
}
reset(2);
// Every status moves the command, also back
for (const status of ['completed', 'running', 'failed', 'running', 'completed']) {
    step('POST', '/api/bd/commands/3/result', { status });
    command(3);
}
step('GET', '/api/bd/commands');

// The command and its device
for (const id of ['4', '9', '5', '6', '7', '8', '0', '999', '01', '0002', '2147483647', '2147483648', '9007199254740993', '99999999999999999999', `1${'0'.repeat(400)}`]) {
    step('POST', `/api/bd/commands/${id}/result`, { status: 'running' });
}
command(4);
command(9);
step('POST', '/api/bd/commands/6/result', { status: 'completed', result: 'uid=0' }, { Authorization: 'Bearer tok-numeric' });
command(6);
step('POST', '/api/bd/commands/5/result', { status: 'completed' }, { Authorization: 'Bearer tok-device-b' });
command(5);
step('POST', '/api/bd/commands/7/result', { status: 'completed' }, { Authorization: 'Bearer tok-spaced' });
// Addresses the route does not have: no digits, other words
for (const url of ['/api/bd/commands/abc/result', '/api/bd/commands/-1/result', '/api/bd/commands/1.0/result', '/api/bd/commands/%31/result', '/api/bd/commands/1/results', '/api/bd/commands//result']) {
    step('POST', url, { status: 'running' });
}
// The other mount and spellings
step('POST', '/api/automation/commands/1/result', { status: 'failed', result: 'at the other mount' });
command(1);
step('POST', '/API/BD/Commands/1/Result/', { status: 'completed' });
command(1);
step('POST', '/api/bd/commands/1/result?status=failed', {});
step('POST', '/api/bd/commands/1/result', { status: 'running' }, { ...A, 'X-Device-Id': 'device-b' });
command(1);

// Bodies that are no object
step('POST', '/api/bd/commands/1/result', '[{"status":"completed"}]');
step('POST', '/api/bd/commands/1/result', 'status=completed');
steps[steps.length - 1].headers = { ...A, 'Content-Type': 'application/x-www-form-urlencoded' };
step('POST', '/api/bd/commands/1/result', '{"status":');
step('POST', '/api/bd/commands/1/result');
step('POST', '/api/bd/commands/1/result', '{"status":"running","status":"failed","result":1,"result":2}');
command(1);
step('GET', '/api/bd/commands');
sql('the commands', `SELECT id, device_id, status, result, ${nulls} FROM remote_commands ORDER BY id`);
sql('the tokens were noted', 'SELECT token, last_used IS NOT NULL AS used FROM access_tokens ORDER BY id');

record({
    routes: 'routes/automation.routes',
    mounts: ['/api/automation', '/api/bd'],
    seed,
    steps,
    out: 'node_device',
}).catch((err) => {
    console.error(err);
    process.exit(1);
});
