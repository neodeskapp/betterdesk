'use strict';

/**
 * Records what the two read routes of routes/inventory.routes.js answer
 * with the real database adapter, on SQLite and (with ORACLE_PG_DSN) on
 * PostgreSQL. See README.md.
 *
 * This file has no backslash in it: every special character is made from
 * its number, so that nothing on the way to the disk can change one.
 */

const { record, USERS, dialect } = require('./lib');

const J = JSON.stringify;
const pg = dialect === 'postgres';
const ch = String.fromCodePoint;
const BS = ch(92);
const SMILE = ch(0x1F600);
const GB = 1024 ** 3;

// Every report gets its own second, so the list has one order
let clock = 0;
function moment() {
    clock += 1;
    const s = String(clock % 60).padStart(2, '0');
    const m = String(Math.floor(clock / 60) % 60).padStart(2, '0');
    const h = String(Math.floor(clock / 3600)).padStart(2, '0');
    return { iso: `2019-03-04T${h}:${m}:${s}.000Z`, own: pg ? `2019-03-05 ${h}:${m}:${s}+00` : `2019-03-05 ${h}:${m}:${s}` };
}

const INSERT = 'INSERT INTO device_inventory (device_id, hardware, software, collected_at, received_at) VALUES (?, ?, ?, ?, ?)';
/** A report row. hardware and software are the texts of the two columns. */
function row(id, hardware, software = '{}') {
    const at = moment();
    return [INSERT, [id, hardware, software, pg ? at.own : at.iso, at.own]];
}
const seed = [];
const ids = [];
function device(id, hardware, software) {
    ids.push(id);
    seed.push(row(id, typeof hardware === 'string' ? hardware : J(hardware), software === undefined ? '{}' : (typeof software === 'string' ? software : J(software))));
}

// A report as an agent sends it
device('PC-1', {
    hostname: 'pc-one', os_name: 'Windows 11 Pro', os_version: '23H2 (22631.4460)', architecture: 'x86_64',
    cpu: { brand: 'Intel(R) Core(TM) i5-12500', logical_cores: 12, usage_percent: 17.4 },
    memory: { total_bytes: 16 * GB, used_bytes: 9123456789 },
    disks: [{ device: 'C:', mount_point: `C:${BS}`, filesystem: 'NTFS', total_gb: 475.8, used_gb: 301.2 }],
    network_interfaces: [{ name: 'Ethernet', mac: '02:00:5e:10:00:01', ip_addresses: ['10.20.1.11'] }],
}, { apps: [{ name: 'Editor', version: '1.2', publisher: 'Example' }, { name: 'Browser', version: '130.0' }] });
device('SRV-1', {
    hostname: 'srv-one', os_name: 'Ubuntu', os_version: '24.04', cpu: { brand: 'AMD EPYC 7313P', logical_cores: 32, usage_percent: 0 },
    memory: { total_bytes: 128 * GB, used_bytes: 0 },
    disks: [{ device: '/dev/nvme0n1p2', mount_point: '/', total_bytes: 250 * GB, used_bytes: 100 * GB },
        { device: '/dev/md0', mount_point: '/srv', total_bytes: 4000 * GB, available_bytes: 200 * GB }],
}, { apps: [] });
device('EMPTY', {}, {});

// Every member the list reads, with a value of every JSON type in it
const values = [null, true, false, 0, 1, -1, 1.5, 0.1, 12, 1048576, 1572864, 524288, 524287.5, 8589934592, 1e21, 1e-7, 123456789012345680000, -0.4, 2.5,
    '', '0', 'abc', ' 12 ', '0x10', '1048576', '2097152.5', '1e9', 'Infinity', '-5', `${ch(0xa0)}3145728${ch(0x2003)}`, `  padded ${ch(0x2028)}`, `caf${ch(0xe9)} ${SMILE}`,
    '<b>&amp;"quoted"</b>', `back${BS}slash and${ch(9)}tab`, [], [5], ['5'], [1, 2], [[]], [null], [[7]], ['a', null, ['b', [1.5, true]], {}], {}, { a: 1 }, { length: 7 },
    { length: 'long' }, { 2: 'x', b: 1, 1: 'y' }, { valueOf: 5 }, 'x'.repeat(200)];
const members = [
    ['hostname', (v) => ({ hostname: v })],
    ['os_name', (v) => ({ os_name: v, os_version: '1.0' })],
    ['os_version', (v) => ({ os_name: 'OS', os_version: v })],
    ['os_both', (v) => ({ os_name: v, os_version: v })],
    ['cpu', (v) => ({ cpu: v })],
    ['brand', (v) => ({ cpu: { brand: v } })],
    ['cores', (v) => ({ cpu: { logical_cores: v } })],
    ['usage', (v) => ({ cpu: { usage_percent: v } })],
    ['memory', (v) => ({ memory: v })],
    ['total', (v) => ({ memory: { total_bytes: v } })],
    ['used', (v) => ({ memory: { used_bytes: v } })],
    ['disks', (v) => ({ disks: v })],
];
for (const [name, make] of members) {
    values.forEach((v, i) => device(`M-${name}-${i}`, make(v)));
}
// A JSON column that holds a text has the text parsed, which fails for most; those come further down
const readable = (v) => {
    if (!pg || typeof v !== 'string' || v === '') return true;
    try { JSON.parse(v); return true; } catch (_) { return false; }
};
values.forEach((v, i) => readable(v) && device(`M-software-${i}`, {}, J(v)));
values.forEach((v, i) => device(`M-apps-${i}`, {}, { apps: v }));
// The whole hardware document as each value
values.forEach((v, i) => readable(v) && device(`M-hardware-${i}`, J(v), J(v)));

// Disks: sizes in every unit and of every type, and which disk is the fullest
const sizes = [null, '', 0, 1, 100, 50.5, -1, 1e21, '100', '50', ' 25 ', 'abc', '0x64', 'Infinity', true, false, [], [50], ['a'], {}, 1e400, -1e400];
const sizeText = (v) => (v === 1e400 ? '1e400' : (v === -1e400 ? '-1e400' : J(v)));
let disk = 0;
/** A report whose only disk is the JSON text given. */
function oneDisk(text) {
    disk += 1;
    ids.push(`D-${disk}`);
    seed.push(row(`D-${disk}`, `{"disks":[${text}]}`));
}
for (const total of sizes) {
    oneDisk(`{"mount_point":"/t","total_bytes":${sizeText(total)},"used_bytes":10}`);
    oneDisk(`{"mount_point":"/t","total_gb":${sizeText(total)},"used_gb":0.5}`);
    oneDisk(`{"mount_point":"/t","total_bytes":${sizeText(total)},"total_gb":2,"used_gb":1}`);
}
for (const used of sizes) {
    oneDisk(`{"mount_point":"/u","total_bytes":200,"used_bytes":${sizeText(used)}}`);
    oneDisk(`{"mount_point":"/u","total_gb":200,"used_gb":${sizeText(used)}}`);
    oneDisk(`{"mount_point":"/u","total_bytes":200,"used_bytes":${sizeText(used)},"used_gb":0.00000005,"available_bytes":150,"free_bytes":120}`);
    oneDisk(`{"mount_point":"/u","total_bytes":200,"available_bytes":${sizeText(used)},"free_bytes":120}`);
    oneDisk(`{"mount_point":"/u","total_bytes":200,"free_bytes":${sizeText(used)}}`);
    oneDisk(`{"mount_point":"/u","total_bytes":200,"used_gb":${sizeText(used)},"free_bytes":20}`);
}
for (const removable of values.slice(0, 44)) oneDisk(`{"mount_point":"/r","total_bytes":200,"used_bytes":50,"removable":${J(removable)}}`);
for (const name of values) {
    oneDisk(`{"mount_point":${J(name)},"device":"dev","name":"name","total_bytes":200,"used_bytes":50}`);
    oneDisk(`{"device":${J(name)},"name":"name","total_bytes":200,"used_bytes":50}`);
    oneDisk(`{"name":${J(name)},"total_bytes":200,"used_bytes":50}`);
}
// Things that are no disk
for (const thing of ['null', 'true', '0', '5', '"disk"', '[]', '[1]', '[{"total_bytes":200,"used_bytes":50}]', '{}']) oneDisk(thing);
// Rounding of the used part and of the free bytes
for (const [total, used] of [[3, 1], [3, 2], [7, 1], [1000, 5], [1000, 994.5], [2000, 1], [2000, 1999], [1e15, 1], [9, 1.5], [200.5, 0.25], [0.3, 0.1], [1, 0.0005],
    [1, 0.00049], [1e300, 1e-300], [1e-300, 1e300], [1, 1e308], [1e308, -1e308], [1, -5], [1, 2], [16, 1.6], [16, 0.4], [8, 0.2], [1e21, 5e20], [2.5, 1.25]]) {
    oneDisk(`{"mount_point":"/p","total_bytes":${total},"used_bytes":${used}}`);
    oneDisk(`{"mount_point":"/p","total_gb":${total},"available_bytes":${used}}`);
}
// The name of the mount is cut at 120 characters, which can fall into the middle of a sign
for (const length of [118, 119, 120, 121]) {
    oneDisk(`{"mount_point":${J('m'.repeat(length) + SMILE + 'tail')},"total_bytes":2,"used_bytes":1}`);
    oneDisk(`{"mount_point":${J(SMILE.repeat(Math.floor(length / 2)) + 'tail')},"total_bytes":2,"used_bytes":1}`);
}
// Several disks
const many = [
    [{ mount_point: '/a', total_bytes: 100, used_bytes: 10 }, { mount_point: '/b', total_bytes: 100, used_bytes: 90 }, { mount_point: '/c', total_bytes: 100, used_bytes: 50 }],
    [{ mount_point: '/first', total_bytes: 100, used_bytes: 50 }, { mount_point: '/second', total_bytes: 200, used_bytes: 100 }],
    [{ mount_point: '/full-stick', total_bytes: 100, used_bytes: 99, removable: true }, { mount_point: '/disk', total_bytes: 100, used_bytes: 1 }],
    [null, 5, 'x', { mount_point: '/no-size' }, { mount_point: '/zero', total_bytes: 0, used_bytes: 0 }, { mount_point: '/ok', total_gb: 1, free_bytes: 1 }],
    [{ mount_point: '/over', total_bytes: 100, used_bytes: 500 }, { mount_point: '/also-over', total_bytes: 100, used_bytes: 900 }],
    [{ mount_point: '/under', total_bytes: 100, used_bytes: -5 }, { mount_point: '/empty', total_bytes: 100, used_bytes: 0 }],
    [{ mount_point: '/nan', total_bytes: 100, used_bytes: 'x' }, { mount_point: '/low', total_bytes: 100, used_bytes: 1 }],
    [{ mount_point: '/a', total_bytes: 100, used_bytes: 10 }, { mount_point: '/skipped-name', total_bytes: 100, used_bytes: 5, name: { toString: 1 } }],
    [{ removable: 0, total_bytes: '1000', used_bytes: '250' }, { removable: '', total_bytes: '1000', used_bytes: '250.4' }],
];
many.forEach((disks, i) => device(`MANY-${i}`, { disks }));
device('MANY-DISKS', { disks: Array.from({ length: 30 }, (_, i) => ({ mount_point: `/m${i}`, total_bytes: 1000, used_bytes: (i * 37) % 1000 })) },
    { apps: Array.from({ length: 250 }, (_, i) => ({ name: `app-${i}` })) });

// What each database can hold and the other cannot
if (pg) {
    // A JSON column holds any JSON value, and NULL
    seed.push(row('P-NULL', null, null));
    ids.push('P-NULL');
    device('P-TEXT-OBJECT', J(J({ hostname: 'in a text', disks: [{ mount_point: '/t', total_bytes: 4, used_bytes: 1 }] })), J(J({ apps: [1, 2, 3] })));
    device('P-TEXT-LIST', J('[1,2]'), J('[]'));
    device('P-TEXT-NUMBER', J('5'), J('0'));
    device('P-TEXT-NULL', J('null'), J('null'));
    device('P-TEXT-TEXT', J(J('twice')), J(J('')));
    device('P-TEXT-SPACED', J(' {"hostname" : "spaced"} '), '{}');
} else {
    // A text column holds any text, and NULL
    seed.push(row('S-NULL', null, null));
    seed.push(row('S-EMPTY', '', ''));
    ids.push('S-NULL', 'S-EMPTY');
    device('S-SPACED', ` {"hostname" :${ch(9)}"spaced" ,${ch(10)}"disks":[ ] } `, ' { } ');
    device('S-TWICE', '{"hostname":"first","b":1,"hostname":"last","cpu":{"brand":"x","brand":"y"}}', '{"apps":[1],"apps":[1,2]}');
    device('S-KEYS', '{"hostname":{"b":1,"10":2,"2":3,"a":4,"01":5,"1.5":6,"-1":7,"4294967294":8,"4294967295":9,"":10}}');
    device('S-HALF-PAIR', `{"hostname":"half ${BS}ud83d pair","os_name":"${BS}ude00","cpu":{"brand":"${BS}ud83d${BS}ude00 ${BS}uD83D"}}`);
    device('S-HALF-MOUNT', `{"disks":[{"mount_point":"${'m'.repeat(119)}${BS}ud83d${BS}ude00","total_bytes":2,"used_bytes":1},{"mount_point":"${BS}ude00${BS}ud83d","total_bytes":0}]}`);
    device('S-ESCAPES', `{"hostname":"${BS}u0000${BS}u001f${BS}u007f${BS}u00e9${BS}u2028${BS}/${BS}b${BS}f${BS}n${BS}r${BS}t${BS}"${BS}${BS}"}`);
    device('S-NUMBERS', '{"hostname":[1.0,1e2,1E+2,-0,0.0,-0.0,1e-7,0.000001,1e21,123456789012345678901234567890,1e400,-1e400,5e-324,1e-400,0.1e1,100e-2]}');
    device('S-DEEP', `{"hostname":${'['.repeat(300)}1${']'.repeat(300)}}`, `{"apps":${'{"apps":'.repeat(100)}[1,2,3]${'}'.repeat(100)}}`);
    device('S-NUL-TEXT', `{"hostname":"a${BS}u0000b"}`, `{"apps":"a${BS}u0000b"}`);
    device('S-MEMORY-TEXT', `{"memory":{"total_bytes":"${ch(0xfeff)}2097152${ch(0x2029)}","used_bytes":"1.5e6"}}`);
}

// Telemetry
const TELEMETRY = 'INSERT INTO device_telemetry (device_id, cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs, timestamp, received_at) VALUES (?, ?, ?, ?, ?, ?, ?)';
function telemetry(id, cpu, used, total, uptime, timed = true) {
    const at = moment();
    seed.push([TELEMETRY, [id, cpu, used, total, uptime, timed ? (pg ? at.own : at.iso) : null, timed ? at.own : null]]);
}
telemetry('PC-1', 17.4, 9123456789, 16 * GB, 26400);
telemetry('SRV-1', 0, 0, 128 * GB, 1645200);
telemetry('EMPTY', null, null, null, null, false);
telemetry('M-hostname-1', 0.1, 1, 2, 3);
telemetry('M-hostname-2', 33.3, 9007199254740993, 9007199254740992, 2147483647);
telemetry('M-hostname-3', 99.99, -1, -9007199254740993, -2147483648);
telemetry('M-hostname-4', 1e-7, 5, 5, 5);
telemetry('M-hostname-5', 123456.789, 5, 5, 5);
telemetry('NO-INVENTORY', 5, 6, 7, 8);
if (pg) {
    telemetry('M-hostname-6', 'NaN', '9223372036854775807', '-9223372036854775808', 0);
    telemetry('M-hostname-7', 'Infinity', '9223372036854775806', '1', 0);
    telemetry('M-hostname-8', '-Infinity', 0, 0, 0);
    telemetry('M-hostname-9', 3.25e38, 0, 0, 0);
    telemetry('M-hostname-10', 16777216, 0, 0, 0);
} else {
    telemetry('M-hostname-6', 'hot', 'much', 'more', 'long');
    telemetry('M-hostname-7', 5.5, 1.5, 2.5, 3.5);
    telemetry('M-hostname-8', '12', '13', '14.5', ' 15 ');
    telemetry('M-hostname-9', 1e300, 1e300, -1e300, 0.1);
    telemetry('M-hostname-10', '', '', '', '');
}

const steps = [];
let n = 0;
function step(path, role = 'admin', method = 'GET') {
    n += 1;
    steps.push({ name: `${n} ${method} ${path}`, role, method, path });
}
const sql = (name, text, params) => steps.push({ name, sql: text, params });

// Who may: every role that can sign in, and nobody
for (const role of [...Object.keys(USERS).filter((r) => r !== 'pro'), '']) {
    step('/api/inventory?limit=2', role);
    step('/api/inventory/PC-1', role);
    step('/api/inventory/unknown', role);
}

// The list, page by page
const pages = Math.ceil((ids.length) / 100);
for (let page = 1; page <= pages + 1; page++) step(`/api/inventory?limit=100&page=${page}`);
step('/api/inventory');
step('/api/inventory?page=2');
step('/api/inventory?limit=7&page=3');

// The paging parameters in every form
const numbers = ['', '0', '1', '2', '3', '01', '002', '100', '101', '1000000', '-1', '+1', '%2B1', '1.0', '1.5', '1e2', '0x10', 'abc', '1a', 'a1', '%201', '1%20', '%091', '1%0A',
    '%C2%A01', '%EF%BC%91', '%D9%A1', '1_0', '1,0', 'Infinity', 'NaN', 'null', 'true', '9007199254740991', '9007199254740992', '9007199254740993', '99999999999999999999',
    `${'9'.repeat(400)}`, `${'0'.repeat(400)}1`, '%31', '%00', '1%00'];
for (const value of numbers) {
    step(`/api/inventory?page=${value}&limit=3`);
    step(`/api/inventory?limit=${value}`);
}
for (const query of ['page=1&page=2', 'limit=3&limit=4', 'page[]=1', 'limit[]=3', 'page[0]=1', 'page[1]=1', 'page[n]=1', 'limit[n]=3', 'page[][]=1', 'page[25]=1',
    'page', 'limit', 'page=&limit=', 'page=2&limit', 'PAGE=2&LIMIT=3', 'page=2&limit=3&other=x', 'page=x&limit=3', 'page=2&limit=x', 'page=x&limit=y', 'page%5B%5D=1',
    'p%61ge=2&limit=3', 'page=2#&limit=x', 'limit=3#page=x', 'page=2;limit=3', 'page=1&page=', 'limit=&limit=3', 'page[toString]=1',
    `page=${Array.from({ length: 21 }, () => '1').join('&page=')}`, `x=1&${Array.from({ length: 1000 }, (_, i) => `f${i}=1`).join('&')}&limit=3`]) {
    step(`/api/inventory?${query}`);
}

// Spellings of the address
for (const path of ['/api/inventory/', '/API/Inventory?limit=1', '/api/inventory/PC-1/', '/api/inventory/pc-1', '/API/INVENTORY/PC-1', '/api/inventory/PC%2D1', '/api/inventory/inventory',
    '/api/inventory/telemetry', '/api/inventory/a%2Fb', '/api/inventory/%20PC-1', '/api/inventory/%E2%82%AC', '/api/inventory/%00', '/api/inventory/PC-1%00', '/api/inventory/%C3%28',
    '/api/inventory/PC-1?page=x&limit=y', '/api/inventory//PC-1', '/api/inventory/PC-1/more', `/api/inventory/${'x'.repeat(300)}`, '/api/inventory/NO-INVENTORY']) {
    step(path);
}

// Every report in full
for (const id of ids) step(`/api/inventory/${id}`);

// A report the list cannot read fails the whole list; the reports beside it stay readable
function failing(id, hardware, software = '{}') {
    sql(`a report the list cannot read: ${id}`, INSERT, row(id, hardware, software)[1]);
    step('/api/inventory?limit=1');
    step(`/api/inventory/${id}`);
    step('/api/inventory/PC-1');
    sql(`${id} goes again`, 'DELETE FROM device_inventory WHERE device_id = ?', [id]);
}
failing('F-NAME', '{"os_name":{"toString":1}}');
failing('F-VERSION', '{"os_name":"OS","os_version":{"toString":null}}');
failing('F-NAME-IN-LIST', '{"os_name":[1,[2,{"toString":"x"}]]}');
failing('F-TOTAL', '{"memory":{"total_bytes":{"toString":1}}}');
failing('F-USED', '{"memory":{"used_bytes":[{"toString":1}]}}');
failing('F-DISK-TOTAL', '{"disks":[{"total_bytes":{"toString":1},"used_bytes":1}]}');
failing('F-DISK-TOTAL-GB', '{"disks":[{"total_gb":{"toString":1},"used_bytes":1}]}');
failing('F-DISK-USED', '{"disks":[{"total_bytes":5,"used_bytes":{"toString":1}}]}');
failing('F-DISK-USED-GB', '{"disks":[{"total_bytes":5,"used_gb":{"toString":1}}]}');
failing('F-DISK-AVAILABLE', '{"disks":[{"total_bytes":5,"available_bytes":{"toString":1}}]}');
failing('F-DISK-FREE', '{"disks":[{"total_bytes":5,"free_bytes":{"toString":1}}]}');
failing('F-DISK-MOUNT', '{"disks":[{"total_bytes":5,"used_bytes":1,"mount_point":{"toString":1}}]}');
failing('F-DISK-DEVICE', '{"disks":[{"total_bytes":5,"used_bytes":1,"device":[{"toString":1}]}]}');
// These read fine: the member that would fail is never made text or a number of
failing('OK-HOSTNAME', '{"hostname":{"toString":1},"cpu":{"brand":{"toString":1},"logical_cores":{"toString":1},"usage_percent":{"toString":1}}}', '{"apps":{"toString":1,"length":{"toString":2}}}');
failing('OK-DISK-SKIPPED', '{"disks":[{"removable":1,"total_bytes":{"toString":1}},{"total_bytes":0,"used_bytes":{"toString":1}},{"total_bytes":5,"used_bytes":1,"free_bytes":{"toString":1}}]}');
failing('OK-DISK-NOT-FULLEST', '{"disks":[{"total_bytes":5,"used_bytes":4,"mount_point":"/a"},{"total_bytes":5,"used_bytes":1,"mount_point":{"toString":1}}]}');
failing('OK-VERSION-EMPTY', '{"os_name":"","os_version":0,"memory":{"total_bytes":0,"used_bytes":""}}');
if (!pg) {
    failing('F-NO-JSON', 'not json');
    failing('F-NO-JSON-SOFTWARE', '{}', '{"apps":');
    failing('F-CUT', '{"hostname":"cut');
    failing('F-TWO-VALUES', '{} {}');
    failing('F-COMMA', '{"a":1,}');
    failing('F-HALF-ESCAPE', `{"hostname":"${BS}ud83"}`);
    failing('F-CONTROL', `{"hostname":"a${ch(1)}b"}`);
    failing('OK-NULL-TEXT', 'null', 'null');
    failing('OK-NUMBER-TEXT', '5', '0');
    failing('OK-TEXT-TEXT', '"text"', '""');
} else {
    failing('F-TEXT-NO-JSON', J('not json'));
    failing('F-TEXT-NO-JSON-SOFTWARE', '{}', J('{"apps":'));
    failing('F-TEXT-HEX', J('0x10'));
    failing('F-TEXT-WORD', J('Infinity'), J('abc'));
    failing('OK-NUMBER', '5', '0');
    failing('OK-FLAGS', 'true', 'false');
    failing('OK-EMPTY-TEXT', J(''), J(''));
}

// No reports at all: the list falls back to the devices the API server knows, which is none here
sql('every report goes', 'DELETE FROM device_inventory');
step('/api/inventory');
step('/api/inventory?page=2&limit=5');
step('/api/inventory?page=x');
step('/api/inventory/PC-1');
step('/api/inventory/NO-INVENTORY');

record({
    routes: process.env.ORACLE_ROUTES || 'routes/inventory.routes',
    mounts: ['/api/bd', '/api/inventory'],
    seed,
    steps,
    out: 'node',
    prepare: ({ stub }) => {
        // The agent's upload routes load the alert rules, which the read routes do not use
        stub('services/alertRulesEngine', {});
        stub('services/betterdeskApi', { getAllPeers: async () => [] });
    },
}).catch((err) => {
    console.error(err);
    process.exit(1);
});
