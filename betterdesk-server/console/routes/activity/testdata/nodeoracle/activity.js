'use strict';

/**
 * Records what the three read routes of routes/activity.routes.js answer
 * with the real database adapter, on SQLite and (with ORACLE_PG_DSN) on
 * PostgreSQL. See README.md.
 */

const { record, USERS, dialect } = require('./lib');

const pg = dialect === 'postgres';
const yes = pg ? true : 1;
const no = pg ? false : 0;
// A time of the fixed week the recording plays in, as the agent sent it
// (ISO) and as the database wrote it itself
const iso = (day, clock) => `2019-03-0${day}T${clock}.000Z`;
const own = (day, clock) => (pg ? `2019-03-0${day} ${clock}+00` : `2019-03-0${day} ${clock}`);

const seed = [];
const peer = (id, note, info, deleted = no) => seed.push(['INSERT INTO peer (id, note, info, is_deleted) VALUES (?, ?, ?, ?)', [id, note, info, deleted]]);

// Devices the console knows, by what their note and their info say
peer('D1', 'pc-one', '{"hostname":"host-one"}');
peer('D2', '', '{"hostname":"host-two","os":"Windows"}');
peer('D3', null, '{"hostname":"host-three"}');
peer('A-EMPTY-INFO', '', pg ? '{}' : '');
peer('A-NO-INFO', 'note of no info', null);
peer('A-NO-INFO-NO-NOTE', '', null);
peer('A-NO-HOSTNAME', '', '{"os":"Linux"}');
peer('A-HOSTNAME-EMPTY', '', '{"hostname":""}');
peer('A-HOSTNAME-NULL', '', '{"hostname":null}');
peer('A-HOSTNAME-NUMBER', '', '{"hostname":5}');
peer('A-HOSTNAME-ZERO', '', '{"hostname":0}');
peer('A-HOSTNAME-FRACTION', '', '{"hostname":0.1}');
peer('A-HOSTNAME-LARGE', '', '{"hostname":12345678901234567890}');
peer('A-HOSTNAME-TINY', '', '{"hostname":0.0000001}');
peer('A-HOSTNAME-TRUE', '', '{"hostname":true}');
peer('A-HOSTNAME-FALSE', 'note under false', '{"hostname":false}');
peer('A-HOSTNAME-LIST', '', '{"hostname":["a",{"b":1,"2":"x"},null,1.50,[]]}');
peer('A-HOSTNAME-EMPTY-LIST', '', '{"hostname":[]}');
peer('A-HOSTNAME-OBJECT', '', '{"hostname":{"z":1,"10":"y","2":"x","a":{"b":null}}}');
peer('A-HOSTNAME-EMPTY-OBJECT', '', '{"hostname":{}}');
peer('A-HOSTNAME-SIGNS', '', '{"hostname":"line\\u2028break <b>&amp; \\"q\\" \\\\ \\u00e9 \\ud83d\\ude00 \\u0007"}');
peer('A-INFO-NULL', 'note over null', 'null');
peer('A-INFO-NULL-NO-NOTE', '', 'null');
peer('A-INFO-LIST', '', '[{"hostname":"in a list"}]');
peer('A-INFO-NUMBER', 'note over a number', '5');
peer('A-INFO-NUMBER-NO-NOTE', '', '5');
peer('A-INFO-ZERO', '', '0');
peer('A-INFO-TRUE', '', 'true');
peer('A-INFO-FALSE', 'note over false', 'false');
peer('A-INFO-TEXT', 'note over text', '"text"');
peer('A-INFO-EMPTY-TEXT', 'note over empty text', '""');
peer('A-INFO-JSON-IN-TEXT', '', '"{\\"hostname\\":\\"in a text\\"}"');
peer('A-INFO-NUMBER-IN-TEXT', 'note over a number in text', '"7"');
peer('A-INFO-NULL-IN-TEXT', 'note over null in text', '"null"');
peer('A-INFO-LIST-IN-TEXT', '', '"[{\\"hostname\\":1}]"');
peer('A-INFO-TEXT-IN-TEXT', 'note over text in text', '"\\"x\\""');
peer('A-DELETED', 'deleted', '{"hostname":"deleted"}', yes);
peer('A-DELETED-UNKNOWN', 'deleted flag unknown', '{"hostname":"x"}', null);
if (!pg) {
    // What only a text column can hold
    peer('A-INFO-BROKEN', 'note over broken info', 'not json');
    peer('A-INFO-CUT', '', '{"hostname":');
    peer('A-INFO-TWICE', '', '{"hostname":"first","hostname":"last"}');
    peer('A-INFO-SPACED', '', ' {"hostname" :\t"spaced"}\n');
    peer('A-INFO-HALF-PAIR', '', '{"hostname":"half \\ud83d pair"}');
    peer('A-INFO-DEEP', '', `{"hostname":${'['.repeat(200)}${']'.repeat(200)}}`);
    peer('A-DELETED-TWO', 'deleted flag two', '{}', 2);
}

// Summaries and sessions. D1 has three days, D2 one, D3 a summary without
// sessions. Nothing ties in an order the routes sort by.
const summary = (id, idle, count, active, reported, received) => seed.push([
    'INSERT INTO activity_summaries (device_id, idle_seconds, session_count, total_active_secs, reported_at, received_at) VALUES (?, ?, ?, ?, ?, ?)',
    [id, idle, count, active, reported, received]]);
const session = (id, app, title, category, started, ended, secs, created) => seed.push([
    'INSERT INTO activity_sessions (device_id, app_name, window_title, category, started_at, ended_at, duration_secs, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
    [id, app, title, category, started, ended, secs, created]]);

summary('D1', 600, 3, 5400, iso(4, '08:00:00'), own(4, '17:00:01'));
summary('D1', 300, 2, 4000, iso(5, '08:00:00'), own(5, '17:00:02'));
summary('D1', 0, 1, 100, iso(6, '08:00:00'), own(6, '17:00:03'));
summary('D2', 60, 2, 1000, iso(5, '09:30:00'), own(5, '18:00:04'));
summary('D3', 86400, 0, 0, iso(6, '00:00:00'), own(6, '00:00:05'));
summary('A-HOSTNAME-NUMBER', 1, 1, 1, iso(1, '01:00:00'), own(1, '01:00:06'));
summary('A-HOSTNAME-LIST', 2, 2, 2, iso(1, '02:00:00'), own(1, '02:00:07'));
summary('A-HOSTNAME-OBJECT', 3, 3, 3, iso(1, '03:00:00'), own(1, '03:00:08'));
summary('A-INFO-NULL', 4, 4, 4, iso(1, '04:00:00'), own(1, '04:00:09'));
summary('A-INFO-TEXT', 5, 5, 5, iso(1, '05:00:00'), own(1, '05:00:10'));
summary('A-DELETED', 6, 6, 6, iso(1, '06:00:00'), own(1, '06:00:11'));
summary('nobody knows me', 7, 7, 7, iso(1, '07:00:00'), own(1, '07:00:12'));
summary('G-ONLY', 8, 8, 8, iso(1, '08:00:00'), own(1, '08:00:13'));

session('D1', 'Mail', 'Inbox', 'communication', iso(4, '08:00:00'), iso(4, '09:00:00'), 3600, own(4, '09:00:00'));
session('D1', 'Browser', 'Portal', 'browser', iso(4, '09:05:00'), iso(4, '09:30:00'), 1500, own(4, '09:30:00'));
session('D1', 'Mail', 'Re: offer', 'communication', iso(4, '09:31:00'), iso(4, '09:36:00'), 300, own(4, '09:36:00'));
session('D1', 'Sheets', 'budget.xlsx', 'productivity', iso(5, '08:00:00'), iso(5, '09:00:01'), 3601, own(5, '09:00:01'));
session('D1', 'Browser', 'Intranet', 'browser', iso(5, '10:00:00'), iso(5, '10:06:39'), 399, own(5, '10:06:39'));
session('D1', 'Editor', 'deploy.yml', 'development', iso(6, '08:00:00'), iso(6, '08:01:40'), 100, own(6, '08:01:40'));
session('D2', 'Player', 'Focus', 'media', iso(5, '09:30:00'), iso(5, '09:40:00'), 600, own(5, '09:40:00'));
session('D2', 'Files', 'Documents', 'system', iso(5, '09:41:00'), iso(5, '09:47:40'), 400, own(5, '09:47:40'));
// A session that began before its day's report and ended after it
session('D1', 'Updater', 'Night run', 'system', iso(4, '07:50:00'), iso(4, '08:10:00'), 1200, own(4, '08:10:00'));
// More sessions and more apps than a request without a limit shows
for (let i = 0; i < 103; i++) {
    const clock = (seconds) => `0${Math.floor(i / 60)}:${String(i % 60).padStart(2, '0')}:${seconds}`;
    session('MANY', `App ${i % 12}`, `window ${i}`, 'busy', iso(7, clock('00')), iso(7, clock('30')), 100 + i, own(7, clock('30')));
}
// No summary, sessions only
session('SESSIONS-ONLY', 'Mail', 'Inbox', 'communication', iso(2, '08:00:00'), iso(2, '08:10:00'), 600, own(2, '08:10:00'));
// One app under two categories: SQLite groups by the app alone
session('TWO-CATEGORIES', 'Mail', 'a', 'communication', iso(2, '08:00:00'), iso(2, '08:10:00'), 600, own(2, '08:10:00'));
session('TWO-CATEGORIES', 'Mail', 'b', 'other', iso(2, '09:00:00'), iso(2, '09:05:00'), 300, own(2, '09:05:00'));
session('TWO-CATEGORIES', 'Notes', 'c', 'other', iso(2, '10:00:00'), iso(2, '10:00:10'), 10, own(2, '10:00:10'));
// Columns without a value
session('NULLS', 'Mail', '', null, iso(3, '08:00:00'), iso(3, '08:10:00'), null, null);
session('NULLS', '', '', null, iso(3, '09:00:00'), iso(3, '09:10:00'), null, own(3, '09:10:00'));
summary('NULLS', null, null, null, iso(3, '08:00:00'), null);
if (!pg) {
    // What only SQLite's columns can hold: fractions and text in number
    // columns, numbers too large for JavaScript, times in other forms
    session('ODD', 'Mail', 'fraction', 'x', iso(3, '08:00:00'), iso(3, '08:10:00'), 12.5, own(3, '08:10:00'));
    session('ODD', 'Notes', 'text', 'x', iso(3, '09:00:00'), iso(3, '09:10:00'), 'abc', own(3, '09:10:00'));
    session('ODD', 'Sheets', 'large', 'x', iso(3, '10:00:00'), iso(3, '10:10:00'), 9007199254740993, 5.25);
    session('ODD', 'Files', 'other form', 'x', '2019-03-03 11:00:00', '2019-03-03 11:10:00', 7, 'yesterday');
    session('ODD', 'Editor', 'numbers as times', 'x', 5.5, 6.5, 1, own(3, '12:00:00'));
    summary('ODD', 1.5, 'many', 9007199254740993, iso(3, '08:00:00'), own(3, '18:00:14'));
    summary('ODD', 'idle', 2, -3, '2019-03-02 08:00:00', 'no time');
    session('TEXT-SUM', 'Mail', 'one', 'x', iso(3, '08:00:00'), iso(3, '08:10:00'), 'abc', own(3, '08:10:00'));
    session('TEXT-SUM', 'Mail', 'two', 'x', iso(3, '09:00:00'), iso(3, '09:10:00'), 5, own(3, '09:10:00'));
    session('TEXT-SUM', 'Notes', 'three', 'x', iso(3, '10:00:00'), iso(3, '10:10:00'), 2.5, own(3, '10:10:00'));
}

const steps = [];
let n = 0;
function step(path, role = 'admin', method = 'GET') {
    n += 1;
    steps.push({ name: `${n} ${method} ${path}`, role, method, path });
}
const sql = (name, text, params) => steps.push({ name, sql: text, params });
const DETAIL_IDS = ['D1', 'D2', 'D3', 'NULLS', 'SESSIONS-ONLY', 'TWO-CATEGORIES', 'ODD', 'TEXT-SUM', 'nobody%20knows%20me', 'unknown'];

// Who may: every role that can sign in, and nobody
for (const role of [...Object.keys(USERS).filter((r) => r !== 'pro'), '']) {
    step('/api/activity', role);
    step('/api/activity/D1', role);
    step('/api/activity/D1/top', role);
    step('/api/activity/unknown', role);
    step('/api/activity/unknown/top', role);
}

// Every device: the name it is listed under comes from its note and its info
const peers = seed.filter(([text]) => text.startsWith('INSERT INTO peer ')).map(([, params]) => params[0]);
for (const id of peers) step(`/api/activity/${id}`);
for (const id of DETAIL_IDS) {
    step(`/api/activity/${id}`);
    step(`/api/activity/${id}/top`);
}

// Spellings of the address
for (const path of ['/api/activity/', '/API/Activity', '/api/activity/D1/', '/api/activity/d1', '/api/activity/D1/top/', '/api/activity/D1/TOP',
    '/api/activity/top', '/api/activity/activity', '/api/activity/D%31', '/api/activity/a%2Fb', '/api/activity/a%2Fb/top', '/api/activity/%20D1%20',
    '/api/activity/%E2%82%AC', '/api/activity/%00', '/api/activity/D1%00', '/api/activity/..%2FD1', '/api/activity/D1;x=1',
    '/api/activity/%25', '/api/activity/+', '/api/activity/a+b', '/api/activity/%C3%28', `/api/activity/${'x'.repeat(300)}`,
    '/api/activity//D1', '/api/activity/D1//top', '/api/activity/D1/top/more', '/api/activity/D1/other']) {
    step(path);
}

// The window, in every form a query can give it
const moments = ['2019-03-04', '2019-03-05', '2019-03-05T08:00:00.000Z', '2019-03-05T08:00:00Z', '2019-03-05%2008:00:00', '2019-03-05+08:00:00', '2019-03-06T08:00:00.000Z',
    '2019-03-04T09:30:00.000Z', '2019-03-04T09:36:00.000Z', '2000-01-01', '2100-01-01', '', '0', 'zzz', '%20', 'null', 'undefined', 'NaN', 'true',
    '2019-03-05T08:00:00.000%2B02:00', '2019-03-05T08:00:00.000+02:00', '20190305', '1551772800000', 'Tue,%2005%20Mar%202019%2008:00:00%20GMT', '%E2%82%AC', '%FF', '%00',
    "x'%20OR%201=1--", '2019-03-05T08:00:00.000Z%00'];
const windows = [];
for (const moment of moments) {
    windows.push(`from=${moment}`, `to=${moment}`);
}
windows.push('from=2019-03-04T09:00:00.000Z&to=2019-03-05T09:00:00.000Z', 'from=2019-03-05&to=2019-03-04', 'from=&to=', 'from=2019-03-05&to=',
    'FROM=2019-03-05', 'from=2019-03-05&from=2019-03-06', 'to=2019-03-05&to=2019-03-06', 'from[]=2019-03-05', 'to[]=2019-03-05', 'from[]=2019-03-05&from[]=2019-03-06',
    'from[0]=2019-03-05', 'from[1]=2019-03-05', 'from[1]=2019-03-06&from[0]=2019-03-05', 'from[]=', 'from[]', 'from', 'from=&from=', 'from[day]=2019-03-05', 'to[day]=2019-03-05',
    'from[day]=2019-03-05&to[day]=2019-03-06', 'from[][]=2019-03-05', 'from[0][day]=2019-03-05', 'from[toString]=x', 'from[length]=1', 'from[__proto__]=x',
    'from%5B%5D=2019-03-05', 'from=2019-03-05&to[]=2019-03-06', 'from[]=2019-03-05&to[]=2019-03-06', 'from=2019-03-05&from[]=2019-03-06',
    // An object binds nothing and a list one value per element: together they can fill the statement
    'from[day]=x&to[]=2019-03-04&to[]=2019-03-06', 'to[day]=x&from[]=2019-03-04&from[]=2019-03-06', 'from[day]=x&to[]=2019-03-04&to[]=2019-03-06&to[]=2019-03-07',
    `from=${Array.from({ length: 21 }, () => '2019-03-05').join('&from=')}`, `from[]=${Array.from({ length: 22 }, () => '2019-03-05').join('&from[]=')}`,
    'from[25]=2019-03-05', 'from=2019-03-05&other=1', 'from=2019-03-05#to=2019-03-04', 'from=2019-03-05;to=2019-03-04');
for (const query of windows) {
    step(`/api/activity?${query}`);
    step(`/api/activity/D1?${query}`);
    step(`/api/activity/D1/top?${query}`);
}

// The limit, in every form
const limits = ['', '0', '1', '2', '3', '5', '6', '7', '100', '1.9', '0.9', '-1', '-0', '-2', '+2', '%2B2', '0x10', '0X2', '010', '2abc', 'abc', '%202', '2%20', '%092',
    '%C2%A02', '%E3%80%802', '%EF%BB%BF2', '1e3', '2e0', 'Infinity', '-Infinity', 'NaN', 'null', 'true', '2,3', '2;3', '2147483647', '2147483648', '4294967296',
    '9007199254740991', '9007199254740993', '9223372036854775807', '9223372036854775808', '99999999999999999999', '1e400', `${'9'.repeat(400)}`, '%00', '2%00', '%E2%82%AC'];
const limitQueries = limits.map((limit) => `limit=${limit}`);
limitQueries.push('limit=1&limit=2', 'limit=0&limit=2', 'limit=&limit=2', 'limit=abc&limit=2', 'limit[]=2', 'limit[]=', 'limit[]', 'limit', 'limit[0]=2', 'limit[1]=2',
    'limit[rows]=2', 'limit[toString]=2', 'limit[valueOf]=2', 'limit[][]=2', 'limit[0][rows]=2', 'limit[0][toString]=2', 'limit[]=2&limit[rows]=3', 'limit=2&limit[rows]=3',
    'limit[length]=2', 'LIMIT=2', 'limit=2&from=2019-03-04T09:00:00.000Z', 'limit=1&to=2019-03-05T12:00:00.000Z', 'limit=2&from=2019-03-04&to=2019-03-06',
    'limit=2&from[day]=x', 'limit[]=1&limit[]=2&limit[]=3', `limit=${Array.from({ length: 21 }, () => '2').join('&limit=')}`,
    `limit[]=${Array.from({ length: 1001 }, () => '2').join('&limit[]=')}`);
for (const query of limitQueries) {
    step(`/api/activity/D1?${query}`);
    step(`/api/activity/D1/top?${query}`);
}
for (const query of ['limit=1', 'limit=abc', 'limit=1&from=2019-03-05']) step(`/api/activity?${query}`);
// What a request shows without a limit, and with one that is none
for (const query of ['', '?limit=', '?limit=abc', '?limit=0', '?limit=100', '?limit=101', '?limit=11', '?limit=-1', '?limit[n]=1', '?from=2019-03-07T00:10:00.000Z', '?to=2019-03-07T01:00:00.000Z&limit=']) {
    step(`/api/activity/MANY${query}`);
    step(`/api/activity/MANY/top${query}`);
}
for (const id of ['TWO-CATEGORIES', 'NULLS', 'ODD', 'TEXT-SUM']) {
    for (const query of ['limit=1', 'limit=2', 'from=2019-03-02T09:00:00.000Z', 'to=2019-03-03T09:10:00.000Z', 'from=2019-03-03&limit=1']) {
        step(`/api/activity/${id}?${query}`);
        step(`/api/activity/${id}/top?${query}`);
    }
}

// Devices only the signal server's table has. The table is not there at
// first, which the lookup takes for "no such device".
const G = ['G-ONLY', 'G-NO-HOSTNAME', 'G-SOFT-DELETED', 'G-DELETED-HERE', 'G-DELETED-HERE-INFO', 'G-BANNED', 'G-EMPTY', 'G-UNKNOWN'];
for (const id of G) step(`/api/activity/${id}`);
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
for (const id of G) step(`/api/activity/${id}`);
const created = pg ? '2019-01-02 03:04:05+00' : '2019-01-02 03:04:05';
const seen = pg ? '2019-02-03 04:05:06+00' : '2019-02-03 04:05:06';
const goPeer = (id, fields) => {
    const row = { id, created_at: created, ...fields };
    const columns = Object.keys(row);
    sql(`the signal server knows ${id}`, `INSERT INTO peers (${columns.map((c) => (c === 'user' ? '"user"' : c)).join(', ')}) VALUES (${columns.map(() => '?').join(', ')})`,
        columns.map((c) => row[c]));
};
goPeer('G-ONLY', { uuid: 'uuid-1', ip: '10.1.1.1', user: 'ada', hostname: 'go-host', os: 'Windows 11', version: '1.4.2', status: 'ONLINE', last_online: seen, tags: 'a,b', note: 'the note stays there' });
goPeer('G-NO-HOSTNAME', { os: 'Linux', status: 'OFFLINE' });
goPeer('G-SOFT-DELETED', { hostname: 'soft-deleted', soft_deleted: yes });
goPeer('G-DELETED-HERE', { hostname: 'came-back', uuid: 'uuid-4', ip: '10.1.1.4', status: 'ONLINE', last_online: seen });
goPeer('G-DELETED-HERE-INFO', { hostname: '', status: 'online' });
goPeer('G-BANNED', { hostname: 'banned-host', banned: yes, ban_reason: 'stolen', banned_at: seen, status: 'OFFLINE' });
goPeer('G-EMPTY', {});
sql('a deleted device here', 'INSERT INTO peer (id, uuid, note, info, ip, "user", is_deleted, last_online, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
    ['G-DELETED-HERE', 'old-uuid', 'old note', '{"hostname":"old-host"}', '10.9.9.9', 'old-user', yes, pg ? '2018-01-01 00:00:00+00' : '2018-01-01 00:00:00', pg ? '2017-01-01 00:00:00+00' : '2017-01-01 00:00:00']);
sql('another deleted device here', 'INSERT INTO peer (id, uuid, note, info, ip, "user", is_deleted, last_online, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
    ['G-DELETED-HERE-INFO', 'old-uuid', '', '{"hostname":"kept-host"}', '10.9.9.8', 'old-user', yes, pg ? '2018-01-01 00:00:00+00' : '2018-01-01 00:00:00', pg ? '2017-01-01 00:00:00+00' : '2017-01-01 00:00:00']);
if (!pg) {
    // Columns the signal server's table lets be NULL on SQLite
    sql('a device with nothing but its id', 'INSERT INTO peers (id, uuid, ip, "user", hostname, os, version, status, last_online, created_at, banned, ban_reason, tags) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
        ['G-NULLS', null, null, null, null, null, null, null, null, created, null, null, null]);
    G.push('G-NULLS');
}
step('/api/activity');
for (const id of G) step(`/api/activity/${id}`);
for (const id of G) step(`/api/activity/${id}`);
step('/api/activity');
// What the lookups copied into the console's own table
sql('the copies', `SELECT id, uuid, note, ${pg ? 'info::text AS info' : 'info'}, ip, "user", status_online, is_deleted, is_banned, banned_reason${pg ? '' : ', tags, last_online, created_at, banned_at'} FROM peer WHERE id LIKE 'G-%' ORDER BY id`);
if (pg) {
    sql('the times of the copies', "SELECT id, to_char(last_online AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') AS last_online, to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') AS created_at, to_char(banned_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') AS banned_at FROM peer WHERE id LIKE 'G-%' AND id <> 'G-EMPTY' ORDER BY id");
}

record({
    routes: process.env.ORACLE_ROUTES || 'routes/activity.routes',
    mounts: ['/api/bd', '/api/activity'],
    seed,
    steps,
    out: 'node',
    // The agent's upload route loads the alert rules, which the read routes do not use
    prepare: ({ stub }) => stub('services/alertRulesEngine', {}),
}).catch((err) => {
    console.error(err);
    process.exit(1);
});
