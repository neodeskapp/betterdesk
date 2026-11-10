'use strict';

/**
 * Records what routes/reports.routes.js answers with the real report engine
 * and database adapter. See README.md in this folder.
 *
 *   node nodeoracle/reports.js            (SQLite)
 *   ORACLE_PG_DSN=... node nodeoracle/reports.js
 *
 * Run it with TZ=UTC: a time read from PostgreSQL goes into a CSV in the
 * zone of the process, and the Go test runs in UTC.
 */

const { record, dialect } = require('./lib');

const PG = dialect === 'postgres';
const flag = (b) => (PG ? b : (b ? 1 : 0));

function row(table, values) {
    const cols = Object.keys(values);
    const names = cols.map((c) => (c === 'user' ? '"user"' : c));
    return [`INSERT INTO ${table} (${names.join(', ')}) VALUES (${cols.map(() => '?').join(', ')})`, cols.map((c) => values[c])];
}

// Text for a jsonb column: JSON as it is, other text as a JSON string
function jtext(s) {
    if (!PG || s === null) return s;
    try {
        JSON.parse(s);
        return s;
    } catch (_) {
        return JSON.stringify(s);
    }
}

function peer(id, values) {
    const all = {
        id, uuid: '', note: '', created_at: '2019-10-01 08:00:00', status_online: flag(false), last_online: '2019-11-01 09:00:00',
        is_deleted: flag(false), info: '{}', ip: '', user: '', is_banned: flag(false), banned_at: null, banned_reason: '', folder_id: null,
        ...values,
    };
    all.info = jtext(all.info);
    return row('peer', all);
}

const seed = [
    peer('900000001', { info: JSON.stringify({ hostname: 'ws-01', os: 'Windows 11 Pro', ip: '10.0.0.1' }), status_online: flag(true) }),
    peer('900000002', { info: JSON.stringify({ os: 'Ubuntu, "LTS"', hostname: { a: 1 } }), is_banned: flag(true), banned_reason: 'Stolen, "really"' }),
    peer('900000003', { note: 'Büro ✓', info: JSON.stringify({ os: 'constructor', ip: ['10.0.0.3', 'x'] }), is_banned: flag(true), banned_reason: 'multi\nline' }),
    peer('900000004', { info: 'not json', status_online: flag(true) }),
    peer('900000005', { info: JSON.stringify({ os: '10' }), is_deleted: flag(true) }),

    row('activity_summaries', { device_id: 'A1', idle_seconds: 100, session_count: 2, total_active_secs: 7200, reported_at: '2019-10-31T23:00:00.000Z', received_at: '2019-11-01 01:00:00' }),
    row('activity_summaries', { device_id: 'A1', idle_seconds: 50, session_count: 1, total_active_secs: 3600, reported_at: '2019-10-30T08:00:00.000Z', received_at: '2019-10-30 18:00:00' }),
    row('activity_summaries', { device_id: 'A2', idle_seconds: 1, session_count: 1, total_active_secs: 60, reported_at: '2019-11-30T08:00:00.000Z', received_at: '2019-11-30 18:00:00' }),
    row('activity_sessions', { device_id: 'A1', app_name: 'Chrome', window_title: 'x', category: 'browser', started_at: '2019-10-31T23:00:00.000Z', ended_at: '2019-11-01T00:00:00.000Z', duration_secs: 3600, created_at: '2019-11-01 01:00:00' }),
    row('activity_sessions', { device_id: 'A1', app_name: 'Zoom, "Inc."', window_title: 'x', category: 'meeting', started_at: '2019-10-30T08:00:00.000Z', ended_at: '2019-10-30T09:00:00.000Z', duration_secs: 3600, created_at: '2019-10-30 18:00:00' }),
    row('activity_sessions', { device_id: 'A2', app_name: 'Chrome', window_title: 'x', category: 'browser', started_at: '2019-11-30T08:00:00.000Z', ended_at: '2019-11-30T08:01:00.000Z', duration_secs: 60, created_at: '2019-11-30 18:00:00' }),

    row('tickets', { title: 'One', status: 'open', priority: 'high', category: 'network', created_by: 'alice', created_at: '2019-11-01 10:00:00', updated_at: '2019-11-01 10:00:00' }),
    row('tickets', { title: 'Two', status: 'closed', priority: null, category: 'a,b', created_by: 'alice', created_at: '2019-11-02 10:00:00', updated_at: '2019-11-02 10:00:00' }),

    row('device_inventory', { device_id: 'I1', hardware: JSON.stringify({ os_name: 'Windows 11 Pro', memory: { total_bytes: 17179869184 } }), software: '{}', collected_at: null, received_at: '2019-11-01 10:00:00' }),
    row('device_inventory', { device_id: 'I2', hardware: JSON.stringify({ os_name: 'Debian, "12"', memory: { total_bytes: '2621440' } }), software: '{}', collected_at: null, received_at: '2019-11-02 10:00:00' }),

    row('alert_rules', { name: 'CPU, high', description: '', enabled: flag(true), condition_type: 'cpu', condition_op: 'gt', condition_value: 90, severity: 'warning', cooldown_secs: 300, notify_emails: '', created_by: 'alice', created_at: '2019-10-01 10:00:00', updated_at: '2019-10-01 10:00:00' }),
    row('alert_history', { rule_id: 1, device_id: 'D1', severity: 'warning', message: 'CPU "at" 95%', triggered_at: '2019-10-27T00:30:00.000Z', acknowledged: flag(true), acknowledged_by: 'alice', acknowledged_at: '2019-07-01 12:00:00', created_at: '2019-10-27 00:30:00' }),
    row('alert_history', { rule_id: 1, device_id: null, severity: 'critical', message: 'multi\nline', triggered_at: '2019-11-01T10:00:00.000Z', acknowledged: flag(false), acknowledged_by: null, acknowledged_at: null, created_at: '2019-11-01 10:00:00' }),

    row('network_targets', { name: 'Gateway', host: 'gw', port: 443, check_type: 'tcp', timeout_ms: 5000, interval_ms: 60000, enabled: flag(true), last_status: 'UP', last_check_at: '2019-03-31 01:30:00', last_rtt_ms: 1.5, created_at: '2019-10-01 10:00:00', updated_at: '2019-10-01 10:00:00' }),
    row('network_targets', { name: 'Web, "main"', host: 'web', port: null, check_type: 'ping', timeout_ms: 5000, interval_ms: 60000, enabled: flag(false), last_status: 'down', last_check_at: '2019-10-27 01:30:00', last_rtt_ms: 0.1, created_at: '2019-10-01 10:00:00', updated_at: '2019-10-01 10:00:00' }),
    row('network_targets', { name: 'Printer', host: 'p', port: null, check_type: 'ping', timeout_ms: 5000, interval_ms: 60000, enabled: flag(true), last_status: null, last_check_at: null, last_rtt_ms: null, created_at: '2019-10-01 10:00:00', updated_at: '2019-10-01 10:00:00' }),

    row('audit_log', { user_id: 1, action: 'login', details: 'Signed in, "really"', ip_address: '10.0.0.1', created_at: '2019-11-01 10:00:00' }),
    row('audit_log', { user_id: 6, action: 'device_banned', details: 'multi\nline', ip_address: null, created_at: '2019-07-01 12:00:00' }),
    row('audit_log', { user_id: null, action: 'boot', details: null, ip_address: null, created_at: '2019-01-01 00:00:00' }),

    row('dlp_events', { device_id: 'D1', event_source: 'usb', event_type: 'info', policy_id: null, policy_name: '', action: 'block', details: JSON.stringify({ file: 'a,b.txt', 10: 'y', 2: 'x' }), created_at: '2019-11-01 10:00:00' }),
    row('dlp_events', { device_id: 'D2', event_source: 'file', event_type: 'violation', policy_id: null, policy_name: 'P', action: 'log', details: null, created_at: '2019-11-01T10:00:00.930Z' }),

    row('saved_reports', { title: 'Weekly devices', report_type: 'devices', filters: '{}', payload: '{"total_devices":3,"platform_distribution":{"b":1,"10":2,"2":3}}', created_by: 'admin', created_at: '2019-11-01 10:00:00' }),
    row('saved_reports', { title: 'Broken payload', report_type: 'tickets', filters: '{"from":"2019-01-01"}', payload: jtext('not json'), created_by: 'e2e_admin', created_at: '2019-11-02T10:00:00.930Z' }),
    row('saved_reports', { title: 'No payload', report_type: 'alerts', filters: null, payload: null, created_by: 'admin', created_at: '2019-11-03 10:00:00' }),
    // the same time as the one before on SQLite; PostgreSQL gives such rows in no fixed order
    row('saved_reports', { title: 'Zero payload', report_type: 'alerts', filters: '[]', payload: '0', created_by: 'admin', created_at: PG ? '2019-11-03 11:00:00' : '2019-11-03 10:00:00' }),
    row('saved_reports', { title: 'Payload in a string', report_type: 'network', filters: '{}', payload: JSON.stringify(JSON.stringify({ a: 1 })), created_by: 'admin', created_at: '2019-11-04 10:00:00' }),
    row('saved_reports', { title: 'Ünïcødé ✓', report_type: 'devices', filters: '{"device_id":{"b":1,"2":2}}', payload: '{"unicode":"Ü ✓","n":1e21,"neg":-0}', created_by: 'admin', created_at: '2019-11-05 10:00:00' }),
];

const steps = [];
const add = (name, role, method, path, body) => steps.push({ name, role, method, path, ...(body === undefined ? {} : { body: typeof body === 'string' ? body : JSON.stringify(body) }) });

// pro never has a session in the console; the panel access guard answers for it in Go
const READERS = ['', 'viewer', 'operator', 'server_admin', 'global_admin', 'super_admin', 'admin'];
const WRITERS = ['', 'super_admin', 'admin'];
const IDS = ['1', '2', '3', '4', '5', '6', '1.0', '0x1', '%20%201%20', '1e0', '-0', '1.5', 'abc', 'Infinity', '999999999', '99999999999', '%C3%BC'];

for (const role of READERS) {
    add('types', role, 'GET', '/api/reports/types');
    add('types other spelling', role, 'GET', '/API/Reports/Types/');
    add('saved', role, 'GET', '/api/reports/saved');
    for (const id of IDS) add(`saved ${id}`, role, 'GET', `/api/reports/saved/${id}`);
}

const TYPES = ['devices', 'activity', 'security', 'tickets', 'network', 'inventory', 'alerts'];
const BODIES = [
    {}, { type: '' }, { type: 0 }, { type: null }, { type: false }, { type: 'nope' }, { type: 'Devices' }, { type: 5 }, { type: true },
    { type: { a: 1 } }, { type: ['devices'] }, { type: [['tickets']] }, { type: ['devices', 'x'] }, { type: [] }, { type: 'constructor' },
    { type: '__proto__' }, { type: 'toString' }, { type: 'valueOf' },
    ...TYPES.map((type) => ({ type })),
    { type: 'activity', from: '2019-10-31', to: '2019-11-01' }, { type: 'activity', from: '2019-11-01' },
    { type: 'activity', from: '2019-10-31T23:00:00.000Z' }, { type: 'activity', to: '2019-10-31T23:00:00+01:00' },
    { type: 'activity', from: 'garbage' }, { type: 'activity', from: 5 }, { type: 'activity', from: true },
    { type: 'activity', from: ['2019-11-01'] }, { type: 'activity', from: ['a', 'b'] }, { type: 'activity', from: { a: 1 } },
    { type: 'devices', from: '2019-01-01', to: '2019-12-31', device_id: { b: 1, 10: 'x', 2: [1.5, null] }, limit: 1e21 },
    { type: 'tickets', from: null, to: false, device_id: 900100001, limit: '10' },
];
// an empty body is a contract case: the replay cannot send Content-Length: 0
const RAW = ['[]', '["devices"]', '"devices"'];

const SECTIONS = [undefined, 'banned_devices', 'recent_audit', 'targets', 'recent_alerts', 'top_applications', 'dlp', 'stats', 'nope',
    ['targets'], 'constructor', 0, ''];

for (const role of WRITERS) {
    for (const body of BODIES) add(`generate ${JSON.stringify(body)}`, role, 'POST', '/api/reports/generate', body);
    for (const raw of RAW) add(`generate raw ${raw}`, role, 'POST', '/api/reports/generate', raw);
    for (const type of TYPES) {
        for (const section of SECTIONS) {
            const body = section === undefined ? { type } : { type, section };
            add(`csv ${JSON.stringify(body)}`, role, 'POST', '/api/reports/generate/csv', body);
        }
    }
    for (const body of BODIES.slice(0, 18)) add(`csv ${JSON.stringify(body)}`, role, 'POST', '/api/reports/generate/csv', body);
    add('csv activity from a flag', role, 'POST', '/api/reports/generate/csv', { type: 'activity', from: true });
}

const SAVES = [
    { type: 'tickets', title: 'Contract tickets', from: '2019-11-01', limit: 5 },
    { type: 'devices' },
    { type: 'alerts', title: '' },
    { type: 'network', title: 5 },
    { type: 'network', title: ['In a list'] },
    { type: 'network', title: ['a', 'b'] },
    { type: 'network', title: true },
    { type: 'network', title: { a: 1 } },
    { type: ['inventory'], title: 'Type in a list' },
    { type: ['inventory', 'x'], title: 'Two types' },
    { type: 'nope' },
    { type: 'constructor' },
    { title: 'No type' },
    { type: 'activity', title: 'Range', from: '2019-10-31', to: '2019-11-01', device_id: ['x'] },
    { type: 'security', title: 'Ünïcødé ✓ ' },
];
for (const role of WRITERS) {
    for (const body of SAVES) add(`save ${JSON.stringify(body)}`, role, 'POST', '/api/reports/save', body);
}
// saves of one second come in no fixed order on SQLite: give each its own time
steps.push({
    name: 'times of the saves',
    sql: PG ? "UPDATE saved_reports SET created_at = TIMESTAMPTZ '2019-12-01 00:00:00+00' + id * INTERVAL '1 minute' WHERE id > 6"
        : "UPDATE saved_reports SET created_at = datetime('2019-12-01', '+' || id || ' minutes') WHERE id > 6",
    params: [],
});
add('saved after saves', 'admin', 'GET', '/api/reports/saved');
for (let id = 7; id <= 45; id++) add(`saved ${id}`, 'viewer', 'GET', `/api/reports/saved/${id}`);
for (const id of ['abc', '1.5', '999999999', '99999999999', 'Infinity']) {
    for (const role of WRITERS) add(`delete ${id}`, role, 'DELETE', `/api/reports/saved/${id}`);
}
for (let id = 1; id <= 45; id++) {
    add(`delete ${id} signed out`, '', 'DELETE', `/api/reports/saved/${id}`);
    add(`delete ${id}`, id % 2 ? 'admin' : 'super_admin', 'DELETE', `/api/reports/saved/${id}`);
}
add('delete 1 again', 'admin', 'DELETE', '/api/reports/saved/1');
add('saved after deletes', 'admin', 'GET', '/api/reports/saved');
steps.push({ name: 'rows left', sql: 'SELECT CAST(COUNT(*) AS INTEGER) AS n FROM saved_reports', params: [] });

const KEEP = ['content-type', 'content-disposition'];

record({
    routes: 'routes/reports.routes',
    mounts: ['/api/reports'],
    seed,
    steps,
    out: 'node',
    keep: (_step, answer) => Object.fromEntries(KEEP.filter((h) => answer.headers[h] !== undefined).map((h) => [h, answer.headers[h]])),
}).catch((err) => {
    console.error(err);
    process.exit(1);
});
