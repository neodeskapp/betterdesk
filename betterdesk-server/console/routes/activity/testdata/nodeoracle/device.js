'use strict';

/**
 * Records what the device route of routes/activity.routes.js (POST
 * /activity behind requireDeviceToken and requireTokenDeviceMatch)
 * answers with the real database adapter and the real alert rules engine,
 * on SQLite and (with ORACLE_PG_DSN) on PostgreSQL, and what it leaves in
 * the database. Writes ../node_device_<dialect>.json. See README.md.
 */

const crypto = require('crypto');
const { record, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');

const TOKEN = 'INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)';
const FUTURE = '2999-01-01 00:00:00';
function token(text, client, { expires = FUTURE, revoked = false, hashed = true } = {}) {
    return [TOKEN, [text, hashed ? sha(text) : null, 5, client, `uuid-${text}`, expires, pg ? revoked : (revoked ? 1 : 0)]];
}
const seed = [
    token('tok-device-a', 'device-a'),
    token('tok-device-b', 'device-b'),
    token('tok-numeric', '123'),
    token('tok-unbound', ''),
    token('tok-null-client', null),
    token('tok-short', 'x'),
    token('tok-spaced', 'dev a'),
    token('tok-long', 'd'.repeat(65)),
    token('tok-expired', 'device-a', { expires: '2001-01-01 00:00:00' }),
    token('tok-revoked', 'device-a', { revoked: true }),
    token('tok-plain', 'device-p', { hashed: false }),
    // The rule fires for every number but -0.5, so each evaluation leaves
    // one alert that names the value it was given
    ['INSERT INTO alert_rules (name, enabled, condition_type, condition_op, condition_value, severity, cooldown_secs, notify_emails) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
        ['any idle', pg ? true : 1, 'idle_duration', 'neq', -0.5, 'warning', 300, '']],
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
const time = (column) => (pg ? `to_char(${column} AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS') AS ${column}` : column);
const type = (column) => (pg ? '' : `, typeof(${column}) AS ${column}_type`);
// What a report left, and then a clean slate for the next one
function stored() {
    sql('the sessions', `SELECT device_id, app_name, window_title, category, ${time('started_at')}, ${time('ended_at')}, duration_secs${type('duration_secs')} FROM activity_sessions ORDER BY id`);
    sql('the summaries', `SELECT device_id, idle_seconds${type('idle_seconds')}, session_count${type('session_count')}, total_active_secs${type('total_active_secs')}, ${time('reported_at')} FROM activity_summaries ORDER BY id`);
    sql('the alerts raised', 'SELECT rule_id, device_id, severity, message FROM alert_history ORDER BY id');
    sql('the sessions go', 'DELETE FROM activity_sessions');
    sql('the summaries go', 'DELETE FROM activity_summaries');
    sql('the alerts go', 'DELETE FROM alert_history');
}
const at = '2011-03-04T10:00:00.000Z';
const session = (minute, over = {}) => ({
    app_name: 'Editor', window_title: 'notes.txt', category: 'productivity',
    started_at: `2011-03-04T09:${String(minute).padStart(2, '0')}:00.000Z`, ended_at: `2011-03-04T09:${String(minute).padStart(2, '0')}:30.000Z`, duration_secs: 30, ...over,
});
const report = (over = {}) => ({ device_id: 'device-a', sessions: [session(1), session(2)], idle_seconds: 120, timestamp: at, ...over });

// Who may report, at both mounts
const tokens = [
    {}, { 'X-Device-Id': 'device-a' }, { Authorization: 'Bearer nope' }, { Authorization: 'Bearer tok-unbound' }, { Authorization: 'Bearer tok-null-client' },
    { Authorization: 'Bearer tok-short' }, { Authorization: 'Bearer tok-spaced' }, { Authorization: 'Bearer tok-long' },
    { Authorization: 'Bearer tok-expired' }, { Authorization: 'Bearer tok-revoked' }, { Authorization: 'Basic tok-device-a' }, { Authorization: 'bearer tok-device-a' },
    { Authorization: 'Bearer  tok-device-a' }, { Authorization: 'Bearer tok-device-a more' }, { Authorization: 'Bearer tok-plain' },
];
for (const headers of tokens) {
    step('POST', '/api/bd/activity', { device_id: 'device-a', timestamp: at }, headers);
    step('POST', '/api/activity/activity', {}, headers);
}
step('POST', '/api/bd/activity', report(), {}, 'admin');
stored();

// The device the request names: the guard, then the route
for (const id of [undefined, null, '', 0, false, 'device-b', 'DEVICE-A', ' device-a', ['device-a'], [['device-a']], ['device-a', 'x'], 1, true, {}, { toString: 1 }, [{ toString: 1 }]]) {
    step('POST', '/api/bd/activity', report({ device_id: id, sessions: [], idle_seconds: undefined }));
}
step('POST', '/api/bd/activity', report({ device_id: 123, sessions: [] }), { Authorization: 'Bearer tok-numeric' });
step('POST', '/api/bd/activity', report({ device_id: '123', sessions: [] }), { Authorization: 'Bearer tok-numeric' });
step('POST', '/api/bd/activity?device_id=device-b', report({ sessions: [] }));
step('POST', '/api/bd/activity?device_id=device-a', report({ device_id: undefined }));
step('POST', '/api/bd/activity?device_id=device-b', report({ device_id: undefined }));
step('POST', '/api/bd/activity?device_id%5B%5D=device-a', report({ device_id: undefined }));
step('POST', '/api/bd/activity?device_id%5BtoString%5D=x', report({ device_id: undefined }));
step('POST', '/api/bd/activity', report({ sessions: [] }), { ...A, 'X-Device-Id': 'device-b' });
step('POST', '/api/bd/activity', report({ device_id: undefined }), { ...A, 'X-Device-Id': 'device-b' });
step('POST', '/api/bd/activity', report({ device_id: undefined }), { ...A, 'X-Device-Id': 'device-a' });
step('POST', '/api/bd/activity', report({ device_id: undefined }), { ...A, 'X-Device-Id': '' });
step('POST', '/api/bd/activity', report({ device_id: '' }), { ...A, 'X-Device-Id': 'device-a' });
step('POST', '/api/bd/activity', report(), { Authorization: 'Bearer tok-device-b' });
step('POST', '/api/bd/activity', report({ device_id: 'device-b' }), { Authorization: 'Bearer tok-device-b' });
stored();

// The report
step('POST', '/api/bd/activity', report());
stored();
step('POST', '/API/BD/Activity/', report());
step('POST', '/api/activity/activity', report({ timestamp: '2011-03-04T10:01:00.000Z' }));
step('POST', '/api/activity/ACTIVITY', report({ timestamp: '2011-03-04T10:02:00.000Z' }));
stored();
// The same time again changes the summary of that time
step('POST', '/api/bd/activity', report());
step('POST', '/api/bd/activity', report({ sessions: [session(3)], idle_seconds: 5 }));
stored();
// Without a time the summary gets the time of its arrival
step('POST', '/api/bd/activity', report({ timestamp: undefined, sessions: [] }));
sql('a time was stored', pg ? 'SELECT reported_at IS NOT NULL AS stored, session_count FROM activity_summaries'
    : "SELECT reported_at LIKE '20__-__-__T__:__:__.___Z' AS stored, session_count FROM activity_summaries");
sql('the summaries go', 'DELETE FROM activity_summaries');
sql('the alerts go', 'DELETE FROM alert_history');

// The sessions in every form
const lists = [
    undefined, null, [], 'many', 5, { length: 2, 0: session(4) }, [null], [session(5), null], [5, 'x', true, []], [{}],
    // The log line makes text of the length after the summary is stored
    { length: { toString: 1 } }, { length: [1, [{ toString: 1 }]] }, { length: [1, [2]] },
    // A session without times is not stored, and its duration is added all the same
    [{ duration_secs: { toString: 1 } }], [{ duration_secs: [1, [{ toString: 1 }]] }], [{ duration_secs: '5' }, { duration_secs: [7] }, session(9)],
    [session(6, { started_at: '' })], [session(7, { ended_at: null })], [session(8, { started_at: 0 })],
    Array.from({ length: 501 }, (_, i) => session(i % 60, { duration_secs: 1, app_name: `app-${i}` })),
];
for (const sessions of lists) {
    step('POST', '/api/bd/activity', report({ sessions }));
    stored();
}
// Each member of a session with a value of every type
const values = [undefined, null, true, false, 0, 1, 1.5, -1, 1e21, '', '0', '60', 'abc', ' 7 ', [], [5], ['a', 'b'], {}, { a: 1 }, { toString: 1 }, `caf${String.fromCodePoint(0xe9)}`];
for (const member of ['app_name', 'window_title', 'category', 'started_at', 'ended_at', 'duration_secs']) {
    for (const v of values) {
        const one = session(9);
        one[member] = v;
        step('POST', '/api/bd/activity', report({ sessions: [one, session(10)] }));
        stored();
    }
}
// The total of the durations is JavaScript's +: text is joined, not added
for (const durations of [['60', 30], [30, '60'], ['1', '2', '3'], [0.1, 0.2], [[5], 1], [{}, 1], [{ toString: 1 }, 1], [true, true], [null, '', 0, false], [1e308, 1e308]]) {
    step('POST', '/api/bd/activity', report({ sessions: durations.map((d, i) => session(11 + i, { duration_secs: d })) }));
    stored();
}
// The idle time and the time of the report with a value of every type
for (const v of values) {
    step('POST', '/api/bd/activity', report({ idle_seconds: v, sessions: [] }));
    stored();
}
for (const v of [null, 0, '', '2011-03-04 10:05:00', '2011-03-04', 'garbage', 5, true, [], ['2011-03-04T10:06:00.000Z'], {}]) {
    if (v === null || v === 0 || v === '') {
        step('POST', '/api/bd/activity', report({ timestamp: v, sessions: [] }));
        sql('a time was stored', pg ? 'SELECT reported_at IS NOT NULL AS stored FROM activity_summaries' : "SELECT reported_at LIKE '20__-__-__T__:__:__.___Z' AS stored FROM activity_summaries");
        sql('the summaries go', 'DELETE FROM activity_summaries');
        sql('the alerts go', 'DELETE FROM alert_history');
        continue;
    }
    step('POST', '/api/bd/activity', report({ timestamp: v, sessions: [] }));
    stored();
}

// Bodies that are no object
step('POST', '/api/bd/activity', '[{"device_id":"device-a"}]');
step('POST', '/api/bd/activity', 'device_id=device-a');
steps[steps.length - 1].headers = { ...A, 'Content-Type': 'application/x-www-form-urlencoded' };
step('POST', '/api/bd/activity', '{"device_id":"device-a",');
step('POST', '/api/bd/activity');
step('POST', '/api/bd/activity', `{"device_id":"device-a","device_id":"device-b","timestamp":"${at}"}`);
step('POST', '/api/bd/activity', `{"device_id":"device-b","device_id":"device-a","timestamp":"${at}"}`);
stored();
sql('the tokens were noted', 'SELECT token, last_used IS NOT NULL AS used FROM access_tokens ORDER BY id');

const pending = new Set();
record({
    routes: 'routes/activity.routes',
    mounts: ['/api/bd', '/api/activity'],
    seed,
    steps,
    out: 'node_device',
    prepare: ({ stub, web }) => {
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
