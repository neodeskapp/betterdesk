'use strict';

/**
 * Records what the device routes of routes/inventory.routes.js (POST
 * /inventory, POST /telemetry, GET /inventory/:id, behind the file's own
 * token check) answer with the real database adapter and the real alert
 * rules engine, on SQLite and (with ORACLE_PG_DSN) on PostgreSQL, and what
 * they leave in the database. Writes ../node_device_<dialect>.json. See
 * README.md.
 *
 * This file has no backslash in it: every special character is made from
 * its number.
 */

const crypto = require('crypto');
const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const ch = String.fromCodePoint;
const BS = ch(92);
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');
const yes = pg ? true : 1;

const TOKEN = 'INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)';
const FUTURE = '2999-01-01 00:00:00';
function token(text, client, { expires = FUTURE, revoked = false, hashed = true } = {}) {
    return [TOKEN, [text, hashed ? sha(text) : null, 5, client, `uuid-${text}`, expires, pg ? revoked : (revoked ? 1 : 0)]];
}
const RULE = 'INSERT INTO alert_rules (name, enabled, condition_type, condition_op, condition_value, severity, cooldown_secs, notify_emails) VALUES (?, ?, ?, ?, ?, ?, ?, ?)';
const PEER = 'INSERT INTO peer (id, note, info, is_deleted) VALUES (?, ?, ?, ?)';

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
    token('tok-gone', 'gone'),
    token('tok-signal', 'sig-1'),
    token('tok-deleted', 'del-1'),
    // Every rule fires for every number but -0.5, so each evaluation leaves
    // one alert that names the value it was given
    [RULE, ['any disk', yes, 'disk_usage', 'neq', -0.5, 'critical', 300, '']],
    [RULE, ['any cpu', yes, 'cpu_usage', 'neq', -0.5, 'warning', 300, '']],
    [RULE, ['any memory', yes, 'memory_usage', 'neq', -0.5, 'info', 300, '']],
    [PEER, ['device-a', '', pg ? '{"hostname":"old-a","os":"Old OS","platform":"Old OS","version":"1.0"}' : '{"hostname":"old-a","os":"Old OS","platform":"Old OS","version":"1.0"}', 0]],
    [PEER, ['123', 'numbered', '{}', 0]],
    [PEER, ['x', '', '{"hostname":"short"}', 0]],
    [PEER, ['del-1', 'deleted here', '{"hostname":"deleted-here"}', yes]],
];
if (pg) {
    for (const row of seed) {
        if (row[0] === PEER) row[1][3] = row[1][3] === 0 ? false : row[1][3];
    }
}

// The info a device's row holds, and what an upload makes of it
const infos = pg
    ? [null, '{}', '{"hostname":"kept","b":1,"2":"two","a":2,"1":"one","os_version":"old"}', 'null', J('text'), J('{"from":"a text"}'), J('null'), J(''),
        '[1,2]', '5', '0', 'true', 'false', '{"__proto__":{"x":1},"z":1}', '{"memory_mb":1,"cpu":"old","os":"old"}']
    : [null, '', '{}', '{"hostname":"kept","b":1,"2":"two","a":2,"1":"one","os_version":"old"}', 'null', 'not json', J('text'), J('{"from":"a text"}'), J('ab' + ch(0x1F600)),
        '[1,2]', '5', '0', 'true', '{"__proto__":{"x":1},"z":1}', '{"memory_mb":1,"cpu":"old","os":"old"}', '{"a":1,"a":2}', ' {"spaced":true} '];
infos.forEach((info, i) => {
    seed.push(token(`tok-info-${i}`, `info-${i}`));
    seed.push([PEER, [`info-${i}`, i % 3 === 0 ? 'a note' : '', info, pg ? false : 0]]);
});

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
const alerts = () => {
    sql('the alerts raised', 'SELECT rule_id, device_id, severity, message FROM alert_history ORDER BY id');
    sql('the alerts go', 'DELETE FROM alert_history');
};
// A time the route filled in itself is the time of the run, which no
// replay has again: it is named, not shown
const time = (column) => (pg
    ? `CASE WHEN ${column} >= TIMESTAMPTZ '2020-01-01 00:00:00+00' THEN 'the time of the run' ELSE to_char(${column} AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS') END AS ${column}`
    : `CASE WHEN ${column} LIKE '20__-__-__T__:__:__.___Z' AND ${column} >= '2020' THEN 'the time of the run' ELSE ${column} END AS ${column}`);
const doc = (column) => (pg ? `${column}::text AS ${column}` : column);
const report = (id) => sql(`the report of ${id}`, `SELECT device_id, ${doc('hardware')}, ${doc('software')}, ${time('collected_at')} FROM device_inventory WHERE device_id = ?`, [id]);
const info = (id) => sql(`the info of ${id}`, `SELECT id, note, ${doc('info')}, ${pg ? 'CASE WHEN is_deleted THEN 1 ELSE 0 END' : 'is_deleted'} AS is_deleted FROM peer WHERE id = ?`, [id]);
const telemetry = (id) => sql(`the telemetry of ${id}`, pg
    ? `SELECT device_id, cpu_usage_percent::text AS cpu, memory_used_bytes::text AS used, memory_total_bytes::text AS total, uptime_secs::text AS uptime, ${time('timestamp')} FROM device_telemetry WHERE device_id = ?`
    : 'SELECT device_id, cpu_usage_percent AS cpu, typeof(cpu_usage_percent) AS cpu_type, memory_used_bytes AS used, typeof(memory_used_bytes) AS used_type, memory_total_bytes AS total, uptime_secs AS uptime, typeof(uptime_secs) AS uptime_type, timestamp FROM device_telemetry WHERE device_id = ?', [id]);

const hardware = {
    hostname: 'pc-a', os_name: 'Windows 11 Pro', os_version: '23H2', cpu: { brand: 'Intel i5', logical_cores: 8, usage_percent: 12.5 },
    memory: { total_bytes: 16 * 1048576 * 1024, used_bytes: 1048576 }, disks: [{ mount_point: 'C:', total_bytes: 200, used_bytes: 150 }],
};
const at = '2011-03-04T10:00:00.000Z';
const upload = (over = {}) => ({ device_id: 'device-a', hardware, software: { apps: [{ name: 'Editor' }] }, collected_at: at, ...over });

// Who may call at all, on each route and at both mounts
const tokens = [
    {}, { 'X-Device-Id': 'device-a' }, { Authorization: 'Bearer nope' }, { Authorization: 'Bearer tok-unbound' }, { Authorization: 'Bearer tok-null-client' },
    { Authorization: 'Bearer tok-expired' }, { Authorization: 'Bearer tok-revoked' }, { Authorization: 'Basic tok-device-a' }, { Authorization: 'bearer tok-device-a' },
    { Authorization: 'Bearer' }, { Authorization: 'Bearer ' }, { Authorization: 'Bearer  tok-device-a' }, { Authorization: `Bearer${ch(9)}tok-device-a` },
    { Authorization: 'Bearer tok-device-a ' }, { Authorization: 'Bearer tok-device-a more' }, { Authorization: 'BearerXtok-device-a' },
    { Authorization: 'Bearer tok-plain' }, { Authorization: 'Bearer tok-short' }, { Authorization: 'Bearer tok-spaced' },
    { Authorization: `Bearer ${sha('tok-device-a')}` },
];
for (const headers of tokens) {
    step('GET', '/api/bd/inventory/device-a', undefined, headers);
    step('POST', '/api/bd/telemetry', { device_id: 'device-a', timestamp: at }, headers);
    step('POST', '/api/inventory/inventory', { device_id: 'device-a' }, headers);
}
// A token is checked before the body, and a session does not count
step('POST', '/api/bd/inventory', {}, {});
step('POST', '/api/bd/inventory', upload(), {}, 'admin');
step('GET', '/api/bd/inventory/device-a', undefined, {}, 'admin');

// Reading a report: none yet, the device's own, another device's
step('GET', '/api/bd/inventory/device-a');
step('GET', '/api/bd/inventory/device-b');
step('GET', '/api/bd/inventory/device-b', undefined, { Authorization: 'Bearer tok-device-b' });
step('GET', '/api/inventory/inventory/x', undefined, { Authorization: 'Bearer tok-short' });
step('GET', '/api/bd/inventory/dev%20a', undefined, { Authorization: 'Bearer tok-spaced' });
step('GET', '/api/bd/inventory/123', undefined, { Authorization: 'Bearer tok-numeric' });
for (const id of ['DEVICE-A', 'device-a%20', '%64evice-a', 'device-%41', '%20device-a', 'device-b', 'device-a.json']) step('GET', `/api/bd/inventory/${id}`);

// The body, then the device it names
const falsy = [undefined, null, '', 0, false];
for (const v of falsy) {
    step('POST', '/api/bd/inventory', upload({ device_id: v }));
    step('POST', '/api/bd/inventory', upload({ hardware: v }));
    step('POST', '/api/bd/telemetry', { device_id: v, cpu_usage_percent: 5 });
}
for (const id of ['device-b', 'DEVICE-A', ' device-a', 'device-a ', ['device-a'], { id: 'device-a' }, true, 1, { toString: 1 }]) {
    step('POST', '/api/bd/inventory', upload({ device_id: id }));
    step('POST', '/api/bd/telemetry', { device_id: id, cpu_usage_percent: 5 });
}
step('POST', '/api/bd/inventory', upload({ device_id: 123 }), { Authorization: 'Bearer tok-numeric' });
step('POST', '/api/bd/inventory', upload({ device_id: '123' }), { Authorization: 'Bearer tok-numeric' });
step('POST', '/api/bd/telemetry', { device_id: 123 }, { Authorization: 'Bearer tok-numeric' });
step('POST', '/api/bd/inventory', upload({ hardware: undefined }), { Authorization: 'Bearer tok-device-b' });
step('POST', '/api/bd/inventory', upload(), { Authorization: 'Bearer tok-device-b' });
step('POST', '/api/bd/telemetry', {}, { Authorization: 'Bearer tok-device-b' });
step('POST', '/api/bd/inventory', upload(), { ...A, 'X-Device-Id': 'device-b' });
step('POST', '/api/bd/inventory?device_id=device-b', upload());
step('POST', '/api/bd/inventory', '[{"device_id":"device-a","hardware":{}}]');
step('POST', '/api/bd/inventory', 'device_id=device-a&hardware=x');
steps[steps.length - 1].headers = { ...A, 'Content-Type': 'application/x-www-form-urlencoded' };
step('POST', '/api/bd/inventory', '{"device_id":"device-a","hardware":');
step('POST', '/api/bd/inventory');
report('device-a');

// Uploads, with what they store and what they raise
step('POST', '/api/bd/inventory', upload());
report('device-a');
info('device-a');
alerts();
step('GET', '/api/bd/inventory/device-a');
step('GET', '/API/BD/Inventory/device-a/');
step('GET', '/api/inventory/inventory/device-a');
step('POST', '/API/Inventory/INVENTORY', upload({ collected_at: '2011-03-04T10:01:00.000Z' }));
report('device-a');
alerts();
// The two documents and the time of collection with values of every type
const values = [null, true, false, 0, 1, -1, 1.5, 1e21, '', '0', 'abc', '2011-03-04T10:02:00.000Z', '2011-03-04 10:03:00', '2011-03-04', ' 2011-03-04T10:04:00Z ',
    [], [5], ['a', 'b'], [[1]], {}, { a: 1 }, { length: 2 }, { toString: 1 }, `caf${ch(0xe9)} ${ch(0x1F600)}`, `half ${ch(0xD83D)} pair`, `nul ${ch(0)} here`,
    { b: 1, 2: 'x', a: 2, 1: 'y' }, 'x'.repeat(300)];
values.forEach((v) => {
    if (v !== null && v !== false && v !== 0 && v !== '') {
        step('POST', '/api/bd/inventory', upload({ hardware: v }));
        report('device-a');
    }
    step('POST', '/api/bd/inventory', upload({ software: v }));
    report('device-a');
    step('POST', '/api/bd/inventory', upload({ collected_at: v }));
    report('device-a');
});
alerts();
// Keys sent twice, numbers in every notation, escapes: the body as text
for (const text of ['{"device_id":"device-a","hardware":{"a":1,"a":2,"hostname":"x"},"software":{"apps":[1.0,1e2,-0,1E+2,0.1e1]}}',
    `{"device_id":"device-a","hardware":{"hostname":"${BS}u0041${BS}u00e9${BS}ud83d${BS}ude00${BS}/${BS}"${BS}${BS}"},"software":"${BS}ud800"}`,
    '{"device_id":"device-a","device_id":"device-b","hardware":{}}', '{"device_id":"device-b","device_id":"device-a","hardware":{}}',
    '{"device_id":"device-a","hardware":{"b":1,"2":"x","a":2,"1":"y","01":"z","4294967295":"w","4294967294":"v"}}',
    `{"device_id":"device-a","hardware":{"hostname":"a${BS}u2028b${BS}u007f${BS}u001f"},"collected_at":"2011-03-04T10:09:00.000Z"}`,
    `{"device_id":"device-a","hardware":${'['.repeat(200)}1${']'.repeat(200)}}`]) {
    step('POST', '/api/bd/inventory', text);
    report('device-a');
}
alerts();

// The device's info: each member with a value of every type, one after the other
const members = [
    ['hostname', (v) => ({ ...hardware, hostname: v })],
    ['os_name', (v) => ({ ...hardware, os_name: v })],
    ['os_version', (v) => ({ ...hardware, os_version: v })],
    ['cpu', (v) => ({ ...hardware, cpu: v })],
    ['brand', (v) => ({ ...hardware, cpu: { ...hardware.cpu, brand: v } })],
    ['cores', (v) => ({ ...hardware, cpu: { ...hardware.cpu, logical_cores: v } })],
    ['memory', (v) => ({ ...hardware, memory: v })],
    ['total', (v) => ({ ...hardware, memory: { total_bytes: v } })],
];
const memberValues = [undefined, null, true, false, 0, 1, -1, 1.5, '', '0', 'abc', ' 12 ', '2097152', '1572864', [], [5], [1, 2], {}, { a: 1 }, { toString: 1 },
    [{ toString: 1 }], 1048576, 1572864, 524288, 524287, -524288, -1572864, 2621440, 1e21, 8589934592, 1e300, `caf${ch(0xe9)}`, `nul ${ch(0)}`];
for (const [name, make] of members) {
    for (const v of memberValues) {
        step('POST', '/api/bd/inventory', upload({ hardware: make(v), collected_at: at }));
        info('device-a');
    }
    sql(`device-a gets its info back after ${name}`, 'UPDATE peer SET info = ? WHERE id = ?', ['{"hostname":"old-a","os":"Old OS","platform":"Old OS","version":"1.0"}', 'device-a']);
}
// Hardware that is no object has none of the members
for (const v of [true, 5, 'text', [1], ['hostname']]) {
    step('POST', '/api/bd/inventory', upload({ hardware: v }));
    info('device-a');
}
// Each kind of info a device's row can hold
infos.forEach((_, i) => {
    step('POST', '/api/bd/inventory', upload({ device_id: `info-${i}` }), { Authorization: `Bearer tok-info-${i}` });
    info(`info-${i}`);
});
// A device the console's table does not have: only the signal server
// knows it, or nobody does, or the console's row is marked deleted. The
// signal server's table is not there at first
for (const [tok, id] of [['tok-gone', 'gone'], ['tok-signal', 'sig-1'], ['tok-deleted', 'del-1'], ['tok-short', 'x'], ['tok-spaced', 'dev a']]) {
    step('POST', '/api/bd/inventory', upload({ device_id: id }), { Authorization: `Bearer ${tok}` });
    info(id);
}
sql('the signal server makes its device table', pg
    ? `CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT NOT NULL DEFAULT '', pk BYTEA DEFAULT NULL, ip TEXT NOT NULL DEFAULT '',
        "user" TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'OFFLINE', nat_type INTEGER NOT NULL DEFAULT 0, last_online TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        disabled BOOLEAN NOT NULL DEFAULT FALSE, banned BOOLEAN NOT NULL DEFAULT FALSE, ban_reason TEXT NOT NULL DEFAULT '', banned_at TIMESTAMPTZ,
        soft_deleted BOOLEAN NOT NULL DEFAULT FALSE, deleted_at TIMESTAMPTZ, note TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '',
        heartbeat_seq BIGINT NOT NULL DEFAULT 0)`
    : `CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', pk BLOB DEFAULT NULL, ip TEXT DEFAULT '', user TEXT DEFAULT '',
        hostname TEXT DEFAULT '', os TEXT DEFAULT '', version TEXT DEFAULT '', status TEXT DEFAULT 'OFFLINE', nat_type INTEGER DEFAULT 0,
        last_online TEXT DEFAULT '', created_at TEXT DEFAULT (datetime('now')), disabled INTEGER DEFAULT 0, banned INTEGER DEFAULT 0,
        ban_reason TEXT DEFAULT '', banned_at TEXT DEFAULT NULL, soft_deleted INTEGER DEFAULT 0, deleted_at TEXT DEFAULT NULL, note TEXT DEFAULT '',
        tags TEXT DEFAULT '', heartbeat_seq INTEGER DEFAULT 0)`);
const created = pg ? '2019-01-02 03:04:05+00' : '2019-01-02 03:04:05';
const GO_PEER = 'INSERT INTO peers (id, uuid, hostname, os, version, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)';
sql('the signal server knows sig-1', GO_PEER, ['sig-1', 'uuid-s', 'go-host', 'Linux', '1.4.2', 'ONLINE', created]);
sql('the signal server knows del-1', GO_PEER, ['del-1', 'uuid-d', 'back-host', 'Windows', '1.4.1', 'OFFLINE', created]);
for (const [tok, id] of [['tok-gone', 'gone'], ['tok-signal', 'sig-1'], ['tok-deleted', 'del-1']]) {
    step('POST', '/api/bd/inventory', upload({ device_id: id }), { Authorization: `Bearer ${tok}` });
    info(id);
    step('POST', '/api/bd/inventory', upload({ device_id: id, hardware: { hostname: 'second' } }), { Authorization: `Bearer ${tok}` });
    info(id);
}
sql('the copies', `SELECT id, uuid, ${pg ? 'CASE WHEN status_online THEN 1 ELSE 0 END' : 'status_online'} AS status_online FROM peer WHERE id IN ('gone', 'sig-1', 'del-1') ORDER BY id`);
alerts();

// The fullest disk, for the disk rules: sizes in every unit and of every type
const sizes = [null, '', 0, 1, 100, 50.5, -1, 1e21, '100', ' 25 ', 'abc', '0x64', 'Infinity', true, false, [], [50], ['a'], {}, { toString: 1 }];
const disks = [];
for (const total of sizes) {
    disks.push([{ mount_point: '/t', total_bytes: total, used_bytes: 10 }]);
    disks.push([{ mount_point: '/t', total_gb: total, used_gb: 0.5 }]);
}
for (const used of sizes) {
    disks.push([{ mount_point: '/u', total_bytes: 200, used_bytes: used }]);
    disks.push([{ mount_point: '/u', total_bytes: 200, available_bytes: used }]);
    disks.push([{ mount_point: '/u', total_bytes: 200, free_bytes: used }]);
}
disks.push([{ total_bytes: 100, used_bytes: 90, removable: true }, { total_bytes: 100, used_bytes: 20 }]);
disks.push([{ total_bytes: 3, used_bytes: 1 }], [{ total_bytes: 3, used_bytes: 2 }], [{ total_bytes: 1000, used_bytes: 994.5 }], [{ total_bytes: 100, used_bytes: 500 }]);
disks.push([{ total_bytes: 100, used_bytes: -5 }], [{ total_bytes: 1, used_bytes: 0.0005 }], [{ total_bytes: 1, used_bytes: 0.00049 }]);
disks.push([null, 5, 'x', { mount_point: '/no-size' }], [{ total_bytes: 5, used_bytes: 1, mount_point: { toString: 1 } }]);
disks.push([{ total_bytes: 5, used_bytes: 4, mount_point: '/a' }, { total_bytes: 5, used_bytes: 1, mount_point: { toString: 1 } }]);
disks.push('not a list', { 0: { total_bytes: 2, used_bytes: 1 }, length: 1 }, []);
for (const d of disks) {
    step('POST', '/api/bd/inventory', upload({ hardware: { ...hardware, disks: d } }));
    alerts();
}
step('POST', '/api/bd/inventory', upload({ hardware: 'disks' }));
alerts();

// The count of apps in the log line
for (const apps of [[1, 2], 'abc', { length: 'many' }, { length: { toString: 1 } }, { length: [{ toString: 1 }] }, { length: [1, [2]] }, { length: 0 }, null]) {
    step('POST', '/api/bd/inventory', upload({ software: { apps } }));
    report('device-a');
    alerts();
}
step('POST', '/api/bd/inventory', upload({ software: 'apps' }));
alerts();

// Telemetry: each value of every type, what is stored and what it raises
step('POST', '/api/bd/telemetry', { device_id: 'device-a', cpu_usage_percent: 12.5, memory_used_bytes: 1048576, memory_total_bytes: 4194304, uptime_secs: 3600, timestamp: at });
telemetry('device-a');
alerts();
step('POST', '/API/BD/Telemetry/', { device_id: 'device-a', cpu_usage_percent: 13, timestamp: at });
step('POST', '/api/inventory/telemetry', { device_id: 'device-a', cpu_usage_percent: 14, timestamp: at });
telemetry('device-a');
alerts();
const numbers = [undefined, null, true, false, 0, -0, 1, 0.1, 12.5, -1, 1e21, 9007199254740993, 2147483648, '', '0', '12.5', 'hot', ' 7 ', '1e3', [], [5], [1, 2], {}, { toString: 1 }];
for (const field of ['cpu_usage_percent', 'memory_used_bytes', 'memory_total_bytes', 'uptime_secs']) {
    for (const v of numbers) {
        const body = { device_id: 'device-a', cpu_usage_percent: 50, memory_used_bytes: 1048576, memory_total_bytes: 2097152, uptime_secs: 60, timestamp: at };
        body[field] = v;
        step('POST', '/api/bd/telemetry', body);
        telemetry('device-a');
        alerts();
    }
}
for (const v of ['2011-03-04T10:05:00.000Z', '2011-03-04 10:06:00', 5, 'garbage', true, [], ['2011-03-04T10:07:00.000Z'], {}]) {
    step('POST', '/api/bd/telemetry', { device_id: 'device-a', cpu_usage_percent: 1, timestamp: v });
    telemetry('device-a');
}
// Without a time the report gets the time of its arrival
step('POST', '/api/bd/telemetry', { device_id: 'device-a', cpu_usage_percent: 1 });
sql('a time was stored', pg ? 'SELECT timestamp IS NOT NULL AS stored FROM device_telemetry WHERE device_id = ?' : "SELECT timestamp LIKE '20__-__-__T__:__:__.___Z' AS stored FROM device_telemetry WHERE device_id = ?", ['device-a']);
alerts();
step('POST', '/api/bd/telemetry', { device_id: 'device-b', cpu_usage_percent: 99, timestamp: at }, { Authorization: 'Bearer tok-device-b' });
telemetry('device-b');
alerts();

// What the reports read back is the last upload
step('POST', '/api/bd/inventory', upload());
step('GET', '/api/bd/inventory/device-a');
step('GET', '/api/inventory/inventory/device-a');
step('POST', '/api/bd/inventory', upload({ device_id: 'device-b' }), { Authorization: 'Bearer tok-device-b' });
step('GET', '/api/bd/inventory/device-b', undefined, { Authorization: 'Bearer tok-device-b' });
step('GET', '/api/bd/inventory/device-b');
step('HEAD', '/api/bd/inventory/device-a');
sql('the tokens were noted', `SELECT token, ${pg ? 'last_used IS NOT NULL' : 'last_used IS NOT NULL'} AS used FROM access_tokens ORDER BY id`);
alerts();

const pending = new Set();
record({
    routes: 'routes/inventory.routes',
    mounts: ['/api/bd', '/api/inventory'],
    seed,
    steps,
    out: 'node_device',
    prepare: ({ stub, web }) => {
        // No rule has addresses to mail to; the stub stands for the mail
        // server nobody would reach
        stub('services/emailService', { sendAlertEmail: async () => true });
        const engine = web('services/alertRulesEngine');
        // The engine's own function, with its promise kept so that the next
        // step waits for it
        engine.evaluateInBackground = (fn) => {
            const p = Promise.resolve().then(fn).catch(() => {});
            pending.add(p);
            p.finally(() => pending.delete(p));
        };
    },
    settle: async () => {
        while (pending.size) await Promise.all([...pending]);
    },
}).catch((err) => {
    console.error(err);
    process.exit(1);
});
