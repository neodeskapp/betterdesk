'use strict';

/**
 * Records what routes/rustdesk-api.routes.js answers where the bytes count:
 * the address book a client reads back after it stored one (the data is
 * JSON text inside JSON, in every form a client may send), the tags, the
 * audit uploads with every kind of value and the lists of them, heartbeats
 * and system information, and the sign-in with client ids and UUIDs of
 * every JSON type, with the rows each leaves. Writes ../node_<dialect>.json.
 * See README.md.
 */

const crypto = require('crypto');
const { record, dialect, web } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const sha = (text) => crypto.createHash('sha256').update(text, 'utf8').digest('hex');
const flag = (b) => (pg ? b : (b ? 1 : 0));
const hash = web('node_modules/bcrypt').hashSync('Oracle-pass-1', 4);

const TOKENS = {
    admin: ['a'.repeat(64), 1],
    operator: ['b'.repeat(64), 5],
    viewer: ['c'.repeat(64), 6],
    guest: ['d'.repeat(64), 7],
    pro: ['e'.repeat(64), 8],
    revoked: ['f'.repeat(64), 6],
};
const bearer = (who) => ({ Authorization: `Bearer ${TOKENS[who][0]}` });

const seed = [];
for (const [who, [token, user]] of Object.entries(TOKENS)) {
    seed.push(['INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?)',
        [token, sha(token), user, `seed-${who}`, `seed-uuid-${who}`, '2999-01-01 00:00:00', flag(who === 'revoked')]]);
}
seed.push(['UPDATE users SET password_hash = ? WHERE id IN (5, 6, 7)', [hash]]);
seed.push([pg ? 'UPDATE users SET totp_enabled = TRUE, totp_secret = ? WHERE id = 7' : 'UPDATE users SET totp_enabled = 1, totp_secret = ? WHERE id = 7', ['JBSWY3DPEHPK3PXP']]);
// A tag group only the admin may see: the vip devices leave everybody else's scope
seed.push(["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-private', 'Private', '', '', 'tag', 'vip')", []]);
seed.push(["INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 1 FROM device_groups WHERE guid = 'g-private'", []]);
seed.push(["INSERT INTO folders (name) VALUES ('Office')", []]);
seed.push(["INSERT INTO device_folder_assignments (device_id, folder_id) SELECT '900200001', id FROM folders WHERE name = 'Office'", []]);
// Devices of the console's table, and the signal server's table the
// lookup falls back to
for (const [id, banned, deleted] of [['hbok1', false, false], ['hbok2', false, false], ['hbban1', true, false], ['hbdel1', false, true]]) {
    seed.push(['INSERT INTO peer (id, is_banned, is_deleted) VALUES (?, ?, ?)', [id, flag(banned), flag(deleted)]]);
}
seed.push([pg
    ? 'CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT, pk BYTEA, ip TEXT, "user" TEXT, hostname TEXT, os TEXT, version TEXT, status TEXT, last_online TIMESTAMPTZ, created_at TIMESTAMPTZ, banned BOOLEAN DEFAULT FALSE, banned_at TIMESTAMPTZ, ban_reason TEXT, soft_deleted BOOLEAN DEFAULT FALSE, tags TEXT, guid TEXT)'
    : 'CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT, pk BLOB, ip TEXT, "user" TEXT, hostname TEXT, os TEXT, version TEXT, status TEXT, last_online TEXT, created_at TEXT, banned INTEGER DEFAULT 0, banned_at TEXT, ban_reason TEXT, soft_deleted INTEGER DEFAULT 0, tags TEXT, guid TEXT)', []]);
seed.push(["INSERT INTO peers (id, uuid, ip, \"user\", hostname, os, version, status, last_online, created_at, banned, ban_reason, tags) VALUES ('hbgo1', 'go-uuid-1', '10.0.0.1', 'goer', 'go-host', 'Linux', '1.4.6', 'ONLINE', '2001-01-01 00:00:00', '2001-01-01 00:00:00', ?, '', 'a,b')", [flag(false)]]);
seed.push(["INSERT INTO peers (id, uuid, hostname, status, created_at, banned, ban_reason) VALUES ('hbgo2', '', 'banned-host', 'OFFLINE', '2001-01-01 00:00:00', ?, 'lost')", [flag(true)]]);
seed.push(["INSERT INTO peers (id, hostname, status, created_at, soft_deleted) VALUES ('hbgo3', 'gone', 'OFFLINE', '2001-01-01 00:00:00', ?)", [flag(true)]]);

// The devices the API server lists
const PEERS = [
    { id: '900200001', hostname: 'host-1', user: 'alice', os: 'Windows 11', version: '1.4.6', note: '', live_online: true, tags: 'office,lab', uuid: 'u1' },
    { id: '900200002', hostname: '', user: '', os: 'macOS', note: 'Note 2', live_online: false, tags: '["vip","x"]', display_name: 'Boss Mac' },
    { id: '900200003', hostname: 'host-3', banned: true, tags: 'office' },
    { id: '900200004', hostname: 'host-4', soft_deleted: true, tags: '' },
    { id: '900200005', hostname: 'host-5', live_status: 'degraded', tags: ['lab', ' vip ', 'Ünïcode'] },
    { id: 'bad id', hostname: 'weird', tags: 'x' },
];
function api(method, path) {
    if (method === 'GET' && path === '/api/peers') return { status: 200, body: J(PEERS) };
    if (method === 'GET' && path === '/api/remote-targets') return { status: 200, body: '[]' };
    if (method === 'PUT' && /^\/api\/peers\/[^/]+\/tags$/.test(path)) return { status: 200, body: '{"success":true}' };
    return null;
}

const steps = [];
let n = 0;
function step(method, url, body, headers = {}, role = '') {
    n += 1;
    const s = { name: `${n} ${method} ${url}`, role, method, path: url, headers };
    if (body !== undefined) s.body = typeof body === 'string' ? body : J(body);
    steps.push(s);
}
const sql = (name, text, params = []) => steps.push({ name, sql: text, params });

// ---- Address books ----
const books = [
    J({ tags: ['office', ' x ', 'x', 7, null, 'a\u0001b'], peers: [
        { id: '900200001', alias: 'Reception', tags: ['office', 'x'], hash: 'h1' },
        { id: '900200002', tags: 'vip, boss,vip' },
        { id: 'TYPED1', username: 'typed', tags: [] },
        { id: '900200003' }, { id: '900200004', tags: ['gone'] },
        { id: ' 900200005 ', platform: 'Windows' },
        'not a peer', null, 5, { alias: 'no id' }, { id: 77, tags: ['n'] },
    ], tag_colors: '{"office":1}' }),
    '{"peers":[{"id":"900200001"',
    '[{"id":"900200001"}]',
    '"text"',
    'null',
    '',
    '   ',
    '{"b":1,"2":"two","1":"one","a":{"y":[1e21,1e-7,-0,0.1,123456789012345678901],"x":"\\u2028\\u00e9\\ud83d\\ude00</script>"},"b":2,"__proto__":{"p":1},"peers":[{"id":"900200005","tags":"' + 'L'.repeat(60) + ',\\t tab ,'+ 'x'.repeat(3) + '"}]}',
    J({ peers: [{ id: '900200001', tags: [{ a: 1 }, ['n'], true, 3.5, ''] }, { id: '900200002', tags: { toString: 1 } }] }),
    J({ tags: 'a,b, c', peers: [] }),
    J({ tags: '["q","r"]', peers: [{ id: 'bad id', tags: ['t'] }, { id: '900200001', tags: 'one,two' }] }),
];
const datas = [...books.map((b) => b), { peers: [{ id: '900200001', tags: ['obj'] }], tags: ['o'] }, ['list', 1], 5, true, null, false, 0];
for (const who of ['viewer', 'operator', 'pro', 'guest', 'admin']) {
    for (const data of datas) {
        step('POST', '/api/ab', { data }, bearer(who));
        step('GET', '/api/ab', undefined, bearer(who));
        step('GET', '/api/ab/tags', undefined, bearer(who));
    }
    for (const data of [undefined, null, '', books[0], { tags: ['p'] }, 0, false]) {
        step('POST', '/api/ab/personal', data === undefined ? {} : { data }, bearer(who));
        step('GET', '/api/ab/personal', undefined, bearer(who));
    }
    step('POST', '/api/ab', { other: 1 }, bearer(who));
    step('GET', '/api/ab', undefined, bearer(who));
    step('POST', '/api/ab', '["a"]', bearer(who));
    step('POST', '/api/ab', 'data=%7B%22peers%22%3A%5B%5D%7D', { ...bearer(who), 'Content-Type': 'application/x-www-form-urlencoded' });
    step('GET', '/api/ab', undefined, bearer(who));
}
step('GET', '/api/ab', undefined, bearer('revoked'));
step('POST', '/api/ab', { data: '{}' }, bearer('revoked'));
step('GET', '/api/ab/tags', undefined, { Authorization: `Bearer  ${TOKENS.viewer[0]}  ` });
sql('the address books', pg
    ? 'SELECT username, ab_type, data FROM address_books ORDER BY username, ab_type'
    : 'SELECT user_id, ab_type, data FROM address_books ORDER BY user_id, ab_type');

// ---- Heartbeats ----
const beats = [
    {}, { id: 'hbok1' }, { uuid: 'hbok1', cpu: 150, memory: -3, disk: 55.5 }, { id: 'hbok1', cpu: '5', memory: null, disk: true },
    { id: 5 }, { id: 'hbban1', cpu: 1 }, { id: 'hbdel1' }, { id: 'hbnone' }, { id: 'bad id' }, { id: 'h'.repeat(40) },
    { id: '', uuid: 'hbok2', cpu: 0.000001, memory: 1e300, disk: -0 }, { id: 'hbgo1', cpu: 3 }, { id: 'hbgo2' }, { id: 'hbgo3' },
    { id: ['hbok1'] }, { id: { toString: 1 } },
];
for (const b of beats) step('POST', '/api/heartbeat', b);
step('POST', '/api/heartbeat', 'id=hbok1&cpu=9', { 'Content-Type': 'application/x-www-form-urlencoded' });
sql('the metrics', 'SELECT peer_id, CAST(cpu_usage AS TEXT) AS cpu, CAST(memory_usage AS TEXT) AS memory, CAST(disk_usage AS TEXT) AS disk FROM peer_metrics ORDER BY id');
sql('the devices online', pg
    ? 'SELECT id, status_online, is_banned, is_deleted, uuid, ip, "user", banned_reason, CAST(info AS TEXT) AS info FROM peer ORDER BY id'
    : 'SELECT id, status_online, is_banned, is_deleted, uuid, ip, "user", banned_reason, info, tags FROM peer ORDER BY id');

// ---- System information ----
const big = Array.from({ length: 12 }, (_, i) => ({ i, pad: 'p'.repeat(500) }));
const infos = [
    {}, { id: 'hbnone' }, { id: 'hbban1' }, { uuid: 'bad id' },
    { id: 'hbok1', cpu: 'Intel Core i7-12700K, 5.2GHz, 24/16 cores', memory: '31.87GB', hostname: 'pc-1', username: 'alice', os: 'Windows 11 Pro', version: '1.4.7', displays: [{ w: 1920 }], encoding: ['vp9'], features: { privacy_mode: true }, platform_additions: { x: 1 } },
    { id: 'hbok1', cpu: 'AMD Ryzen 9, 4GHZ, 0/12 CORES', memory: '16 gb', platform: 'Linux', os: 'Ubuntu', cpu_num: 8, cpu_freq: 3.5, memory_total: 70000 },
    { id: 'hbok2', cpu: ', 5.GHz, 8 core', memory: 32, cpu_name: 'named', cpu_num: 2000, cpu_freq: -1, memory_total: 12.5, displays: big, encoding: Array.from({ length: 30 }, (_, i) => `enc-${i}-${'e'.repeat(200)}`), features: { big: 'f'.repeat(5000) }, platform_additions: ['list'] },
    { id: 'hbok2', cpu: 'Apple M2', memory: '8.0GB', hostname: 'h\u0000\u0007ost', username: 'u'.repeat(300), features: ['a'], platform_additions: 'text', displays: 'nope', encoding: { a: 1 } },
    { id: 'hbok2', cpu: 'x, 12GHz, 4/ cores', cpu_num: 2.5, memory: '1.5 GB' },
    { id: 'hbgo1', hostname: 'from-go', cpu: 5, os: 7 },
];
for (const b of infos) step('POST', '/api/sysinfo', b);
step('POST', '/api/sysinfo_ver', { id: 'hbok1' });
step('POST', '/api/sysinfo_ver', {});
step('POST', '/api/heartbeat', { id: 'hbok1' });
sql('the system information', pg
    ? 'SELECT peer_id, hostname, username, platform, version, cpu_name, cpu_cores, CAST(cpu_freq_ghz AS TEXT) AS freq, CAST(memory_gb AS TEXT) AS memory, os_full, CAST(displays AS TEXT) AS displays, CAST(encoding AS TEXT) AS encoding, CAST(features AS TEXT) AS features, CAST(platform_additions AS TEXT) AS additions, CAST(raw_json AS TEXT) AS raw FROM peer_sysinfo ORDER BY peer_id'
    : 'SELECT peer_id, hostname, username, platform, version, cpu_name, cpu_cores, CAST(cpu_freq_ghz AS TEXT) AS freq, CAST(memory_gb AS TEXT) AS memory, os_full, displays, encoding, features, platform_additions AS additions, raw_json AS raw FROM peer_sysinfo ORDER BY peer_id');
sql('an hour old', pg
    ? "UPDATE peer_sysinfo SET updated_at = NOW() - INTERVAL '2 hours' WHERE peer_id = 'hbok1'"
    : "UPDATE peer_sysinfo SET updated_at = datetime('now', '-2 hours') WHERE peer_id = 'hbok1'");
step('POST', '/api/heartbeat', { id: 'hbok1' });
step('POST', '/api/heartbeat', { id: 'hbok2' });

// ---- Audit uploads ----
const conns = [
    {}, { host_id: null }, { host_id: '' }, { host_id: 0 }, { host_id: 'h1', conn_type: 5 }, { host_id: 'h1', conn_type: -0 },
    { host_id: 'h1', conn_type: '1' }, { host_id: 'h1', conn_type: 2.0, action: 'close', peer_id: 'p1', peer_name: 'P\u0001N', session_id: 's', ip: '198.51.100.1' },
    { host_id: 123, host_uuid: 4.5, peer_id: true, action: 9, session_id: ['s'], ip: 5 },
    { host_id: 'x'.repeat(40), peer_name: 'n'.repeat(300), action: 'a'.repeat(40), session_id: 's'.repeat(70), ip: 'i'.repeat(70) },
    { host_id: ['h', 2] }, { host_id: { a: 1 } }, { host_id: { toString: 1 } }, { host_id: 'h2', host_uuid: { toString: 1 } },
];
for (const b of conns) step('POST', '/api/audit/conn', b);
const files = [
    {}, { host_id: 5 }, { host_id: 'f1' },
    { host_id: 'f1', host_uuid: 'u', peer_id: 'p', direction: 1, path: '/tmp/x', is_file: false, num_files: 20000, files: ['a', { name: 'b', size: 12 }, null, 7, { size: -1 }, ['arr'], { name: 5 }, true], ip: '198.51.100.2', peer_name: 'pn' },
    { host_id: 'f2', direction: '1', is_file: 0, num_files: -4, files: 'x' },
    { host_id: 'f3', direction: -0, is_file: null, num_files: 2.5, files: Array.from({ length: 120 }, (_, i) => `file-${i}`) },
    { host_id: 'f4', files: [{ name: 'n'.repeat(1100), size: 1e21 }, { toString: 1 }] },
];
for (const b of files) step('POST', '/api/audit/file', b);
const alarms = [
    {}, { alarm_type: 7 }, { alarm_type: -1 }, { alarm_type: '3' }, { alarm_type: 6, details: 'text' },
    { alarm_type: 2, alarm_name: 'IP', host_id: 'h', peer_id: 'p', ip: '198.51.100.3', details: { b: 1, a: [1, 2], u: ' ' } },
    { alarm_type: 1, details: [1, 2] }, { alarm_type: 4, details: 0 }, { alarm_type: 5, details: null, host_id: 7 },
];
for (const b of alarms) step('POST', '/api/audit/alarm', b);
// The rows get times of their own, so the lists have one order
for (const table of ['audit_connections', 'audit_files', 'audit_alarms']) {
    sql(`spread ${table} in time`, pg
        ? `UPDATE ${table} SET created_at = TIMESTAMPTZ '2001-01-01 00:00:00+00' + id * INTERVAL '1 minute'`
        : `UPDATE ${table} SET created_at = datetime('2001-01-01 00:00:00', '+' || id || ' minutes')`);
}
sql('the connection records', 'SELECT id, host_id, host_uuid, peer_id, peer_name, action, conn_type, session_id, ip FROM audit_connections ORDER BY id');
sql('the file records', pg
    ? 'SELECT id, host_id, host_uuid, peer_id, direction, path, is_file, num_files, CAST(files_json AS TEXT) AS files, ip, peer_name FROM audit_files ORDER BY id'
    : 'SELECT id, host_id, host_uuid, peer_id, direction, path, is_file, num_files, files_json AS files, ip, peer_name FROM audit_files ORDER BY id');
sql('the alarm records', pg
    ? 'SELECT id, alarm_type, alarm_name, host_id, peer_id, ip, CAST(details AS TEXT) AS details FROM audit_alarms ORDER BY id'
    : 'SELECT id, alarm_type, alarm_name, host_id, peer_id, ip, details FROM audit_alarms ORDER BY id');
for (const q of ['', '?host_id=h1', '?host_id=h1&limit=1&offset=1', '?peer_id=p1&action=close', '?host_id[]=h1', '?host_id=h1&host_id=x', '?limit=abc&offset=-5', '?limit=500', '?limit[toString]=1', '?action=']) {
    step('GET', `/api/audit/conn${q}`, undefined, bearer('viewer'));
}
step('GET', '/api/audit/conn?host_id=h1', undefined, {}, 'viewer');
step('GET', '/api/audit/conn', undefined, {});
for (const q of ['', '?host_id=f1', '?peer_id=p&limit=2', '?host_id[a]=1']) step('GET', `/api/audit/file${q}`, undefined, bearer('admin'));
for (const q of ['', '?alarm_type=2', '?alarm_type=x', '?alarm_type=6&host_id=', '?alarm_type[]=1', '?host_id=h&limit=1']) step('GET', `/api/audit/alarm${q}`, undefined, bearer('admin'));
step('GET', '/api/audit', undefined, bearer('pro'));

// ---- Sign-in ----
const pass = 'Oracle-pass-1';
const logins = [
    { username: 'e2e_viewer', password: pass, id: 'cl-1', uuid: 'uu-1' },
    { username: '  e2e_viewer ', password: pass, id: 'cl-1', uuid: 'uu-1' },
    { username: 'e2e_viewer', password: pass, id: 5, uuid: 7 },
    { username: 'e2e_viewer', password: pass, id: 1.5, uuid: 7 },
    { username: 'e2e_viewer', password: pass, uuid: null },
    { username: 'e2e_viewer', password: pass },
    { username: 'e2e_viewer', password: pass, id: '', uuid: '' },
    { username: 'e2e_viewer', password: pass, id: 0, uuid: 0 },
    { username: 'e2e_viewer', password: pass, id: false, uuid: false },
    { username: 'e2e_viewer', password: pass, id: true, uuid: 'uu-2' },
    { username: 'e2e_viewer', password: pass, id: ['a'], uuid: ['b'] },
    { username: 'e2e_viewer', password: pass, id: [], uuid: ['b', 'c'] },
    { username: 'e2e_viewer', password: pass, id: 'x', uuid: [] },
    { username: 'e2e_viewer', password: pass, id: { a: 1 }, uuid: 'uu-3' },
    { username: 'e2e_viewer', password: pass, id: 'y', uuid: { a: 1 } },
    { username: 'e2e_viewer', password: pass, id: { toString: 1 }, uuid: 'uu-4' },
    { username: 'e2e_operator', password: pass, id: 'hbban1' },
    { username: 'e2e_operator', password: pass, id: 'hbgo2' },
    { username: 'e2e_operator', password: pass, id: 'hbgo1', uuid: 'uu-op' },
    { username: 'e2e_operator', password: 'wrong' },
    { username: 'nobody-here', password: 'wrong', id: 'hbdel1' },
    { username: 'e2e_guest_issuer', password: pass, id: 'cl-g', uuid: 'uu-g' },
    { username: 'e2e_admin', password: 'x' },
    { username: 'e2e_pro', password: 'x' },
    { username: 'e2e_viewer', password: pass, id: 'cl-1', uuid: 'uu-1', tfaCode: '123456' },
    { username: 'e2e_viewer', password: pass, secret: 'zzz' },
];
for (const b of logins) step('POST', '/api/login', b);
step('POST', '/api/login', `username=e2e_viewer&password=${pass}&id=form-id&uuid=form-uuid`, { 'Content-Type': 'application/x-www-form-urlencoded' });
sql('the tokens', pg
    ? 'SELECT user_id, client_id, client_uuid, revoked, ip_address FROM access_tokens ORDER BY id'
    : 'SELECT user_id, client_id, client_uuid, revoked, ip_address FROM access_tokens ORDER BY id');
sql('the attempts', pg
    ? 'SELECT username, ip_address, success FROM login_attempts ORDER BY id'
    : 'SELECT username, ip_address, success FROM login_attempts ORDER BY id');
for (const [token, b] of [['viewer', { id: 'seed-viewer', uuid: 'seed-uuid-viewer' }], ['operator', { id: 'x' }], ['guest', { id: 5, uuid: 7 }], ['pro', { id: 'p', uuid: ['q'] }], ['admin', { id: 'a', uuid: { b: 1 } }]]) {
    step('POST', '/api/logout', b, bearer(token));
}
step('POST', '/api/logout', {}, bearer('revoked'));
step('POST', '/api/logout', {});
sql('the tokens after sign-out', 'SELECT user_id, client_id, client_uuid, revoked FROM access_tokens ORDER BY id');
sql('the audit entries', 'SELECT user_id, action, details, ip_address FROM audit_log ORDER BY id');

// ---- The lists ----
// Node looks for the column in every schema of the server and may miss it in its own
if (pg) seed.push(["ALTER TABLE users ADD COLUMN IF NOT EXISTS guid TEXT DEFAULT ''", []]);
seed.push(["INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-fixed', 'Support', 'note', 'team')", []]);
seed.push(["INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 6 FROM user_groups WHERE guid = 'ug-fixed'", []]);
seed.push(["INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-manual', 'Manual', 'm', '', 'manual', ''), ('dg-lab', 'Lab devices', '', '', 'tag', 'LAB'), ('123', 'Digits', '', '', 'manual', '')", []]);
seed.push(["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, '900200002' FROM device_groups WHERE guid = 'dg-manual'", []]);
seed.push(["INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, '900200005' FROM device_groups WHERE guid = 'dg-manual'", []]);
seed.push(["INSERT INTO folders (name) VALUES ('Lab')", []]);
seed.push(["INSERT INTO device_folder_assignments (device_id, folder_id) SELECT '900200005', id FROM folders WHERE name = 'Lab'", []]);
seed.push(["INSERT INTO device_folder_assignments (device_id, folder_id) SELECT 'zz-other', id FROM folders WHERE name = 'Lab'", []]);
seed.push(['INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions) VALUES (?, ?, ?, ?, ?, ?)', ['s-fixed-1', 'Alpha', 'ug-fixed', '', pg ? true : 1, '{"b":1,"a":2}']]);
seed.push(['INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions) VALUES (?, ?, ?, ?, ?, ?)', ['s-fixed-2', 'Beta', '', 'dg-manual', pg ? false : 0, pg ? '"text"' : 'not json']]);
seed.push(["INSERT INTO strategy_assignments (target_type, target_key, strategy_guid) VALUES ('peer', 'k-1', 's-fixed-1'), ('user', 'k-2', 's-fixed-1'), ('device_group', 'k-3', 's-fixed-1'), ('other', 'k-4', 's-fixed-1')", []]);
seed.push(["INSERT INTO peers (id, uuid, hostname, status, created_at, guid) VALUES ('900200001', '0F0E0D0C-0B0A-1908-C706-050403020100', 'host-1', 'ONLINE', '2001-01-01 00:00:00', ''), ('900200002', 'short', 'host-2', 'OFFLINE', '2001-01-01 00:00:00', 'g-known')", []]);

for (const who of ['viewer', 'operator', 'admin', 'pro', 'guest', 'revoked']) {
    step('GET', '/api/users', undefined, bearer(who));
    step('GET', '/api/currentUser', undefined, bearer(who));
    step('POST', '/api/currentUser', {}, bearer(who));
    step('GET', '/api/peers', undefined, bearer(who));
    step('GET', '/api/device-group', undefined, bearer(who));
    step('GET', '/api/user/group', undefined, bearer(who));
    step('GET', '/api/user-groups', undefined, bearer(who));
    step('GET', '/api/strategies', undefined, bearer(who));
}
const peerQueries = [
    '?folder=office', '?folder=OFFICE', '?group=folder_1', '?folder_id=2', '?group=2', '?group=Manual', '?device_group_guid=dg-lab', '?group=dg-lab',
    '?group=lab', '?group=nothing', '?tag=lab', '?tags=lab,vip', '?tag=x&tag=y', '?tag[]=lab', '?tag_name=%C3%9Cn%C3%AFcode', '?tag[toString]=1',
    '?search=host', '?search=HOST-5', '?search=a&search=b', '?page=2&page_size=1', '?page=0&pageSize=500', '?page=x', '?page_size=-1',
    '?group[guid]=dg-manual', '?group[]=Manual', '?folderName=Lab', '?group=Private', '?group=g-private', '?group=123',
];
for (const q of peerQueries) {
    step('GET', `/api/peers${q}`, undefined, bearer('viewer'));
    step('GET', `/api/peers${q}`, undefined, bearer('admin'));
}
for (const body of [{ folder_id: 1 }, { group: 'Manual', page_size: 1 }, { search: 5 }, { tags: ['lab'] }, { __proto__: { folder: 'Lab' } }, ['x']]) {
    step('POST', '/api/peers/list', body, bearer('operator'));
}
step('PUT', '/api/peers/list?group=Manual', undefined, bearer('admin'));
step('GET', '/api/peers/list', undefined, bearer('pro'));
for (const path of ['/api/device-group/accessible', '/api/group', '/api/group/get', '/API/Device-Group/Accessible', '/api/device-group/accessible/']) {
    step('GET', path, undefined, bearer('viewer'));
    step('GET', path, undefined, bearer('admin'));
}
step('POST', '/api/group/get', {}, bearer('admin'));
for (const body of [{}, { name: '  ' }, { name: 'x', source_type: 'tag' }, { name: 'x', dynamic: true, tag: '' }, { guid: 'dg-none', name: 'x' },
    { guid: 'dg-manual', name: '  Manual 2\u0001 ', note: null, team_id: 7 }, { guid: 'dg-manual', name: 'Manual', source_type: 'tag', tag_filter: 'vip' },
    { guid: 'dg-manual', name: 'Manual', note: 'n'.repeat(600) }, { name: 'Created', note: 'c', team_id: 't', source_type: 'tag', tag_filter: 'q'.repeat(60) }, { name: 'Created 2', allowed_users: ['x'] }]) {
    step('POST', '/api/device-group', body, bearer('admin'));
}
step('POST', '/api/device-group', { name: 'x' }, bearer('operator'));
step('GET', '/api/device-group', undefined, bearer('admin'));
for (const body of [{}, { name: 5 }, { name: '   ' }, { name: 'New users', note: 3, team_id: 'tt' }, { guid: 'ug-fixed', name: 'Support 2', note: 'n2' },
    { guid: 'ug-none', name: 'x' }, { guid: 5, name: 'x' }, { guid: ['ug-fixed'], name: 'Support 3' }, { guid: true, name: 'x' }]) {
    step('POST', '/api/user-groups', body, bearer('admin'));
}
step('POST', '/api/user-groups', { name: 'x' }, bearer('viewer'));
step('GET', '/api/user-groups', undefined, bearer('admin'));
step('GET', '/api/user/group', undefined, bearer('viewer'));
for (const guid of ['s-fixed-1', 's-fixed-2', 'nope', '%01', 'assign']) step('GET', `/api/strategies/${guid}`, undefined, bearer('viewer'));
for (const body of [{}, { name: '' }, { name: 'New', enabled: 0, permissions: [1] }, { name: 'New 2', permissions: null }, { name: 'New 3', enabled: 'yes', user_group_guid: 5 },
    { guid: 's-fixed-2', name: 'Beta 2', enabled: true, permissions: { z: 1 } }, { guid: 's-fixed-2', name: 'Beta 3', permissions: 'text' }, { guid: 'nope', name: 'x' }, { guid: 5, name: 'x' }]) {
    step('POST', '/api/strategies', body, bearer('admin'));
}
step('GET', '/api/strategies/s-fixed-2', undefined, bearer('admin'));
step('POST', '/api/strategies', { name: 'x' }, bearer('operator'));
for (const body of [{}, { peers: [] }, { peers: 5, users: ['e2e_viewer'] }, { strategy: 'nope', users: ['e2e_viewer'] }, { strategy: 's-fixed-1', peers: ['900200001', '900200002'], users: ['e2e_admin', '6', 'nobody'] },
    { strategy: 's-fixed-1', peers: ['900200001'], users: ['e2e_admin', '6'], groups: ['Manual', 'dg-lab'] }, { strategy: 's-fixed-1', groups: ['none'] }, { strategy: '', users: ['e2e_admin'] },
    { strategy: 's-fixed-1', peers: 'ab' }, { strategy: 's-fixed-1', users: [{ toString: 1 }] }, { strategy: 's-fixed-1', peers: [''] }]) {
    step('POST', '/api/strategies/assign', body, bearer('admin'));
}
step('GET', '/api/strategies/s-fixed-1', undefined, bearer('admin'));
step('PUT', '/api/strategies/s-fixed-1/status', true, bearer('admin'));
step('PUT', '/api/strategies/s-fixed-1/status', {}, bearer('admin'));
step('PUT', '/api/strategies/nope/status', {}, bearer('admin'));
step('GET', '/api/strategies', undefined, bearer('admin'));
step('DELETE', '/api/strategies/s-fixed-2', undefined, bearer('admin'));
step('DELETE', '/api/strategies/s-fixed-2', undefined, bearer('admin'));
step('DELETE', '/api/strategies/s-fixed-1', undefined, bearer('viewer'));
sql('the strategies', "SELECT guid, name, user_group_guid, device_group_guid FROM strategies WHERE guid LIKE 's-fixed-%' ORDER BY id");
sql('the assignments', "SELECT target_type, CASE WHEN target_key LIKE '%-%-%-%-%' THEN 'uuid' ELSE target_key END AS k, strategy_guid FROM strategy_assignments ORDER BY target_type, k, id");
for (const q of ['', '?id=900200001', '?id=hbok1&pageSize=1', '?pageSize[toString]=1', '?id[]=x']) step('GET', `/api/devices${q}`, undefined, bearer('viewer'));
sql('the guids of the devices', "SELECT id, guid FROM peers WHERE guid NOT LIKE '%-%-%-%-%' ORDER BY id");
for (const id of ['900200001', 'hbgo1', 'nobody', '%20', 'x'.repeat(40)]) {
    step('GET', `/api/peer-key/${id}`, undefined, bearer('viewer'));
    step('GET', `/api/peer-key/${id}`, undefined, bearer('pro'));
}
step('GET', '/api/login-options', undefined, {});
step('PUT', '/api/currentUser', undefined, bearer('admin'));
step('GET', '/api/software', undefined, {});
step('GET', '/api/software/client-download-link', undefined, {});

record({ seed, steps, out: 'node', api }).catch((err) => {
    console.error(err);
    process.exit(1);
});
