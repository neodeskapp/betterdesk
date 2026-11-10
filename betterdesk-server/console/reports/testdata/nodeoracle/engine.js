'use strict';

/**
 * Runs the Node console's report engine (services/reportEngine.js) with the
 * real database adapter on a scratch database and records the reports it
 * makes. See README.md in this folder.
 *
 * The database is SQLite in a temp folder, or PostgreSQL in a schema of its
 * own when ORACLE_PG_DSN names a server. The file is
 * ../engine_<dialect>_<zone>.json, zone from TZ ("Europe/Berlin" becomes
 * "Europe-Berlin").
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const PG_DSN = process.env.ORACLE_PG_DSN || '';
const dialect = PG_DSN ? 'postgres' : 'sqlite';
const PG = dialect === 'postgres';
const zone = process.env.TZ || 'UTC';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'report-oracle-'));
const schema = `oracle_${process.pid}_${Date.now() % 1000000}`;
const NOW = '2026-11-10T12:34:56.789Z';

function web(rel) {
    return require(path.join(WEB, rel));
}

function rebind(sql) {
    let n = 0;
    return sql.replace(/\?/g, () => `$${++n}`);
}

async function openDatabase() {
    process.env.NODE_ENV = 'test';
    process.env.DATA_DIR = scratch;
    process.env.DB_PATH = path.join(scratch, 'db_v2.sqlite3');
    process.env.AUTH_DB_PATH = path.join(scratch, 'auth.db');
    let admin = null;
    if (PG) {
        const { Pool } = web('node_modules/pg');
        admin = new Pool({ connectionString: PG_DSN, max: 1 });
        await admin.query(`CREATE SCHEMA ${schema}`);
        const url = new URL(PG_DSN);
        url.searchParams.set('options', `-c search_path=${schema}`);
        process.env.DB_TYPE = 'postgres';
        process.env.DATABASE_URL = url.toString();
    } else {
        process.env.DB_TYPE = 'sqlite';
        delete process.env.DATABASE_URL;
    }
    process.chdir(WEB);
    const adapter = web('services/dbAdapter').getAdapter();
    await adapter.init();
    let exec;
    let pool = null;
    if (PG) {
        const { Pool } = web('node_modules/pg');
        pool = new Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
        exec = async (sql, params = []) => (await pool.query(rebind(sql), params)).rows;
    } else {
        const raw = adapter.getSqliteMainDb();
        exec = async (sql, params = []) => {
            const stmt = raw.prepare(sql);
            if (stmt.reader) return stmt.all(...params);
            stmt.run(...params);
            return [];
        };
    }
    const close = async () => {
        try {
            await adapter.close();
        } catch (_) { /* closing twice is fine */ }
        if (pool) await pool.end();
        if (admin) {
            await admin.query(`DROP SCHEMA ${schema} CASCADE`);
            await admin.end();
        }
    };
    return { exec, close };
}

// new Date() is NOW; every other use of Date stays as it was
function fixClock() {
    const RealDate = Date;
    const fixed = RealDate.parse(NOW);
    class FixedDate extends RealDate {
        constructor(...args) {
            if (args.length === 0) super(fixed);
            else super(...args);
        }

        static now() {
            return fixed;
        }
    }
    global.Date = FixedDate;
}

// ---- Rows ----

/** A flag as the column takes it: boolean on PostgreSQL, 0 or 1 on SQLite */
const flag = (b) => (PG ? b : (b ? 1 : 0));

/**
 * Text for a JSON column: as it is on SQLite; on PostgreSQL, where jsonb
 * takes JSON only, text that is no JSON goes in as a JSON string
 */
function jtext(s) {
    if (!PG || s === null) return s;
    try {
        JSON.parse(s);
        return s;
    } catch (_) {
        return JSON.stringify(s);
    }
}

/** [sql, params] inserting one row; "user" is quoted */
function row(table, values) {
    const cols = Object.keys(values);
    const names = cols.map((c) => (c === 'user' ? '"user"' : c));
    return [`INSERT INTO ${table} (${names.join(', ')}) VALUES (${cols.map(() => '?').join(', ')})`, cols.map((c) => values[c])];
}

const CLEAR = [
    'DELETE FROM audit_log', 'DELETE FROM users', 'DELETE FROM peer', 'DELETE FROM activity_sessions',
    'DELETE FROM activity_summaries', 'DELETE FROM dlp_events', 'DELETE FROM tickets', 'DELETE FROM device_inventory',
    'DELETE FROM alert_history', 'DELETE FROM alert_rules', 'DELETE FROM network_checks', 'DELETE FROM network_targets',
].map((sql) => [sql, []]);

const pad = (n, w = 2) => String(n).padStart(w, '0');

function users() {
    return [
        row('users', { id: 1, username: 'alice', password_hash: 'x', role: 'admin' }),
        row('users', { id: 2, username: 'bob, the "builder"', password_hash: 'x', role: 'viewer' }),
        row('users', { id: 3, username: 'Jörg 🚀', password_hash: 'x', role: 'operator' }),
    ];
}

function peer(id, values) {
    const all = {
        id,
        uuid: '',
        note: '',
        created_at: '2019-10-01 08:00:00',
        status_online: flag(false),
        last_online: '2019-11-01 09:00:00',
        is_deleted: flag(false),
        info: '{}',
        ip: '',
        user: '',
        is_banned: flag(false),
        banned_at: null,
        banned_reason: '',
        folder_id: null,
        ...values,
    };
    all.info = jtext(all.info);
    return row('peer', all);
}

function fleetPeers() {
    const info = (o) => JSON.stringify(o);
    const rows = [
        peer('100000001', { info: info({ hostname: 'ws-01', os: 'Windows 11 Pro', ip: '10.0.0.1' }), status_online: flag(true) }),
        peer('100000002', { note: 'Front desk', info: info({ hostname: 'fd', os: 'Windows 11 Pro' }), is_banned: flag(true), banned_reason: 'Stolen, reported "lost"' }),
        peer('100000003', { info: info({ platform: 'macOS 15.1' }), ip: '192.168.1.3' }),
        peer('100000004', { info: '', is_banned: flag(true), banned_reason: '' }),
        peer('100000005', { info: 'not json', status_online: flag(true) }),
        peer('100000006', { info: info({ os: '10' }) }),
        peer('100000007', { info: info({ os: 'constructor' }) }),
        peer('100000008', { info: info({ os: '__proto__' }), status_online: flag(true) }),
        peer('100000009', { info: info({ os: 'Linux, "Debian"\n12' }) }),
        peer('100000010', { info: info({ os: ['Ubuntu', '24.04'], hostname: { a: 1, 2: 'two' } }), is_banned: flag(true), banned_reason: 'line\nbreak' }),
        peer('100000011', { info: info({ os: 7, hostname: 5, ip: ['10.0.0.11', 'x'] }), is_banned: flag(true) }),
        peer('100000012', { info: info({ os: 'Gone' }), is_deleted: flag(true) }),
        peer('100000013', { info: info({ os: 'Windows 11 Pro' }), status_online: flag(true) }),
        peer('100000014', { info: info({ os: 'constructor' }) }),
        peer('100000015', { info: '[1,2]' }),
        peer('100000016', { info: '"text"' }),
        peer('100000017', { info: info({ os: 'Null delete' }), is_deleted: null }),
        peer('100000018', { note: 'Büro 🖥', info: info({ os: 'Windows 11 Pro', hostname: 'ignored' }), is_banned: flag(true), banned_reason: 'Ünïcødé ✓ ' }),
        peer('100000019', { info: info({ os: '0' }) }),
        peer('100000020', { info: info({ os: '4294967295' }) }),
        peer('100000021', { info: info({ os: '4294967294' }) }),
        peer('100000022', { info: info({ os: '01' }) }),
        peer('100000023', { info: info({ os: 'toString' }) }),
        peer('100000024', { info: info({ os: '' , platform: 'Fallback' }), ip: '10.9.9.9', is_banned: flag(true) }),
        peer('100000025', { info: info({ os: null, hostname: null, ip: null }), is_banned: flag(true), ip: 'row-ip' }),
        peer('100000026', { info: info({ os: true }) }),
        peer('100000027', { info: info({ os: { nested: 1 } }) }),
        peer('100000028', { info: info({ os: 'hasOwnProperty' }) }),
        peer('100000029', { info: info({ os: 'Windows 11 Pro' }), status_online: null, is_banned: null }),
    ];
    if (PG) {
        // a JSON string that holds JSON, and the JSON null itself
        rows.push(peer('100000030', { info: JSON.stringify(info({ os: 'From a string' })) }));
        rows.push(peer('100000031', { info: 'null' }));
        rows.push(peer('100000032', { info: '5' }));
    } else {
        rows.push(peer('100000030', { info: info({ os: 'Two' }), status_online: 2, tags: 'a,b' }));
        rows.push(peer('100000031', { info: info({ os: 'Float online' }), status_online: 1.0, is_banned: 1.5 }));
        rows.push(peer('100000032', { info: info({ os: 'Text online' }), status_online: 'yes' }));
    }
    return rows;
}

function summary(id, deviceId, reportedAt, receivedAt, active, idle, sessions = 3) {
    return row('activity_summaries', {
        id, device_id: deviceId, idle_seconds: idle, session_count: sessions, total_active_secs: active,
        reported_at: reportedAt, received_at: receivedAt,
    });
}

let sessionId = 0;
function session(deviceId, app, category, startedAt, endedAt, secs) {
    sessionId += 1;
    return row('activity_sessions', {
        id: sessionId, device_id: deviceId, app_name: app, window_title: `${app} window`, category,
        started_at: startedAt, ended_at: endedAt, duration_secs: secs, created_at: '2019-11-05 00:00:00',
    });
}

function activityRows() {
    sessionId = 0;
    const rows = [
        summary(1, 'A1', '2019-10-30T07:15:00.000Z', '2019-10-30 17:00:00', 20000, 3000),
        summary(2, 'A1', '2019-10-31T07:15:00.000Z', '2019-10-31 17:00:00', 21000, 3100),
        summary(3, 'A1', '2019-11-01T07:15:00.000Z', '2019-11-01 17:00:00', 22000, 3200),
        summary(4, 'A1', '2019-11-30T07:15:00.000Z', '2019-11-30 17:00:00', 1000, 100),
        summary(5, 'A1', '2019-12-01T00:00:00.000Z', '2019-12-01 17:00:00', 500, 50),
        summary(6, 'A2', '2019-10-31T23:00:00.000Z', '2019-11-01 01:00:00', 3600, 60),
        summary(7, 'A2', '2019-10-31T22:59:59.999Z', '2019-11-01 00:59:00', 1800, 30),
        summary(8, 'A3', '2019-10-27T00:30:00.000Z', '2019-10-27 12:00:00', 7200, 0),
        summary(9, 'A3', '2019-10-27T01:30:00.000Z', '2019-10-27 12:00:00', 7300, 1),
        summary(10, '', '2019-11-01T08:00:00.000Z', '2019-11-01 18:00:00', 99, 1),
        summary(11, 'A4', '2019-11-01T08:00:00.000Z', '2019-11-01 18:00:00', null, null),
    ];
    if (!PG) {
        rows.push(summary(12, 'A4', '2019-11-02T08:00:00.000Z', '2019-11-02 18:00:00', 'abc', 1.5));
        rows.push(summary(13, 'A4', '2019-11-03T08:00:00.000Z', '2019-11-03 18:00:00', '0x10', ' 7 '));
        rows.push(summary(14, 'A4', '2019-11-04T08:00:00.000Z', '2019-11-04 18:00:00', 2.25, ''));
    }
    const day = (d, h, m = 0) => `2019-${d}T${pad(h)}:${pad(m)}:00.000Z`;
    rows.push(
        session('A1', 'Chrome', 'browser', day('10-30', 8), day('10-30', 9), 3600),
        session('A1', 'Chrome', 'work', day('10-31', 8), day('10-31', 9), 3500),
        session('A1', 'Zoom, Inc.', 'meeting', day('10-31', 10), day('10-31', 11), 3400),
        session('A1', 'Editor "Pro"', 'dev', day('11-01', 8), day('11-01', 12), 14400),
        session('A1', 'line\nbreak', 'other', day('11-01', 13), day('11-01', 14), 60),
        session('A1', '123', 'numbers', day('11-01', 14), day('11-01', 15), 61),
        session('A1', '__proto__', 'proto', day('11-01', 15), day('11-01', 16), 62),
        session('A1', 'constructor', 'proto', day('11-01', 16), day('11-01', 17), 63),
        session('A1', 'toString', 'proto', day('11-01', 17), day('11-01', 18), 64),
        session('A1', 'Ünïcødé ✓', null, day('11-01', 18), day('11-01', 19), 65),
        session('A1', '', 'empty', day('11-01', 19), day('11-01', 20), 66),
        session('A1', '7', 'numbers', day('11-02', 8), day('11-02', 9), 67),
        session('A2', 'Chrome', 'browser', '2019-10-31T23:00:00.000Z', '2019-10-31T23:59:59.000Z', 3599),
        // apps of one device with the same time come in no fixed order on PostgreSQL
        session('A2', 'Slack', 'chat', '2019-10-31T22:00:00.000Z', '2019-10-31T22:59:59.999Z', 3598),
        session('A2', 'Tie A', 'tie', day('11-01', 1), day('11-01', 2), 500),
        session('A1', 'Tie B', 'tie', day('11-01', 2), day('11-01', 3), 500),
        session('A3', 'Tie C', 'tie', '2019-10-27T00:30:00.000Z', '2019-10-27T01:00:00.000Z', 500),
        session('A3', 'Chrome', 'browser', '2019-10-27T01:30:00.000Z', '2019-10-27T02:30:00.000Z', 3600),
    );
    for (let i = 1; i <= 24; i++) {
        rows.push(session('A3', `App ${pad(i)}`, 'many', day('10-26', i % 24, i), day('10-26', i % 24, i + 1), 1000 + i * 37));
    }
    if (!PG) {
        rows.push(session('A4', 'Odd duration', 'odd', day('11-02', 8), day('11-02', 9), 'abc'));
        rows.push(session('A4', 'Odd duration', 'odd', day('11-02', 9), day('11-02', 10), 2.5));
        rows.push(session('A4', 'Null duration', 'odd', day('11-02', 10), day('11-02', 11), null));
    }
    return rows;
}

function dlpRows(count) {
    const rows = [];
    const details = [
        JSON.stringify({ file: 'a,b.txt', size: 10 }),
        JSON.stringify({ usb: { vendor: 'Kingston', 10: 'y', 2: 'x' }, note: 'He said "hi"\nbye' }),
        '{}',
        null,
        '[1,2]',
        JSON.stringify(JSON.stringify({ inner: true })),
        JSON.stringify({ big: 1e21, small: 1e-7, neg: -0, unicode: 'Ü ✓' }),
    ];
    const sources = ['usb', 'file', 'clipboard', 'usb', 'file', 'print', 'usb'];
    const actions = ['block', 'log', 'alert', 'log', 'block', null, 'log'];
    for (let i = 1; i <= count; i++) {
        const k = (i - 1) % details.length;
        let created = `2019-11-01 ${pad(8 + (i % 10))}:${pad(i % 60)}:00`;
        if (i === 3) created = '2019-11-01T10:00:00.930Z';
        // rows with the same time come in no fixed order on PostgreSQL
        if (!PG && (i === 4 || i === 5)) created = rowsTie;
        rows.push(row('dlp_events', {
            id: i, device_id: `D${i % 4}`, event_source: sources[k], event_type: i % 2 ? 'info' : 'violation',
            policy_id: null, policy_name: i % 3 ? `Policy, ${i}` : '', action: actions[k], details: details[k], created_at: created,
        }));
    }
    return rows;
}
const rowsTie = '2019-11-01 12:00:00';

function auditRows(count) {
    const rows = [];
    const details = ['Signed in', 'Changed "name", then left', 'multi\nline', 'carriage\rreturn', null, 'Ünïcødé ✓', '', 'a,b,c'];
    // PostgreSQL has a foreign key to users; SQLite does not
    const users = PG ? [1, 2, 3, null] : [1, 2, 3, null, 99];
    for (let i = 1; i <= count; i++) {
        let created = `2019-11-${pad(1 + (i % 28))} ${pad(i % 24)}:${pad(i % 60)}:${pad(i % 60)}`;
        if (!PG && i % 9 === 0) created = '2019-11-15 12:00:00';
        rows.push(row('audit_log', {
            id: i, user_id: users[i % users.length], action: i % 2 ? 'login' : 'device_banned',
            details: details[i % details.length], ip_address: i % 3 ? `10.0.0.${i}` : null, created_at: created,
        }));
    }
    return rows;
}

function ticketRows() {
    const t = (id, status, priority, category) => row('tickets', {
        id, title: `Ticket ${id}`, description: '', status, priority, category, device_id: null, created_by: 'alice',
        assigned_to: null, sla_due_at: null, resolved_at: null, closed_at: null,
        created_at: `2019-11-${pad(id)} 10:00:00`, updated_at: `2019-11-${pad(id)} 10:00:00`,
    });
    return [
        t(1, 'open', 'high', 'network'), t(2, 'open', 'low', 'hardware'), t(3, 'in_progress', null, null),
        t(4, 'resolved', '', ''), t(5, 'closed', 'valueOf', 'constructor'), t(6, 'closed', 'high', '__proto__'),
        t(7, '', 'medium', '42'), t(8, null, 'medium', 'a,b'), t(9, 'open', 'low', 'constructor'),
        t(10, 'pending', 'critical', 'network'), t(11, 'open', '7', '3'),
    ];
}

function inventoryRows() {
    const hw = (o) => JSON.stringify(o);
    const inv = (id, hardware, software = '{}') => row('device_inventory', {
        id, device_id: `I${id}`, hardware, software, collected_at: `2019-11-01T0${id % 10}:00:00.000Z`,
        received_at: `2019-11-${pad(id)} 09:00:00`,
    });
    const rows = [
        inv(1, hw({ os_name: 'Windows 11 Pro', memory: { total_bytes: 17179869184 } })),
        inv(2, hw({ os_name: 'Ubuntu 24.04', memory: { total_bytes: '8589934592' } })),
        inv(3, hw({ os_name: 'macOS 15.1', memory: { total_bytes: [1073741824] } })),
        inv(4, hw({ os_name: 'Windows 11 Pro', memory: { total_bytes: { a: 1 } } })),
        inv(5, hw({ memory: 0 })),
        inv(6, hw({ memory: 'abc', os_name: 'constructor' })),
        inv(7, hw({ os_name: 5, memory: { total_bytes: 1572864 } })),
        inv(8, hw({ os_name: 'Half', memory: { total_bytes: 2621440 } })),
        inv(9, '[1,2]'),
        inv(10, '"str"'),
        inv(11, 'null'),
        inv(12, null),
        inv(13, hw({ os_name: ['A', 'B'], memory: { total_bytes: '0x100000' } })),
        inv(14, hw({ os_name: '__proto__', memory: { total_bytes: true } })),
        inv(15, hw({ os_name: '12', memory: { total_bytes: -1572864 } })),
    ];
    if (!PG) rows.push(inv(16, ''));
    return rows;
}

function alertRows(history) {
    const rule = (id, name, enabled) => row('alert_rules', {
        id, name, description: '', enabled, condition_type: 'cpu', condition_op: 'gt', condition_value: 0.1,
        severity: 'warning', scope_device_id: null, cooldown_secs: 300, notify_emails: '', created_by: 'alice',
        created_at: `2019-10-0${id} 10:00:00`, updated_at: `2019-10-0${id} 10:00:00`,
    });
    const rows = [
        rule(1, 'CPU high', flag(true)), rule(2, 'Disk, full', flag(false)), rule(3, 'Offline "long"', flag(true)),
        rule(4, 'Ünïcødé ✓', flag(false)),
    ];
    if (!PG) rows.push(rule(5, 'Two', 2), rule(6, 'Empty text', ''), rule(7, 'Null', null));
    const messages = ['CPU at 95%', 'Disk, 99% full', 'Device "x" offline', 'multi\nline', 'Ünïcødé ✓', ''];
    for (let i = 1; i <= history; i++) {
        const minute = i % 60;
        rows.push(row('alert_history', {
            id: i, rule_id: 1 + (i % 4), device_id: i % 5 ? `D${i % 3}` : null, severity: i % 2 ? 'warning' : 'critical',
            message: messages[i % messages.length], triggered_at: `2019-11-${pad(1 + (i % 27))}T${pad(i % 24)}:${pad(minute)}:00.${pad(i % 1000, 3)}Z`,
            acknowledged: flag(i % 3 === 0), acknowledged_by: i % 3 === 0 ? 'alice' : null,
            acknowledged_at: i % 3 === 0 ? '2019-11-28 10:00:00' : null, created_at: `2019-11-${pad(1 + (i % 27))} 00:00:00`,
        }));
    }
    return rows;
}

function networkRows() {
    const target = (id, name, status, checkAt, rtt) => row('network_targets', {
        id, name, host: `host-${id}`, port: id % 2 ? 443 : null, url: null, check_type: id % 2 ? 'tcp' : 'ping',
        timeout_ms: 5000, interval_ms: 60000, enabled: flag(id % 3 !== 0), last_status: status, last_check_at: checkAt,
        last_rtt_ms: rtt, created_at: '2019-10-01 10:00:00', updated_at: '2019-10-01 10:00:00',
    });
    return [
        target(1, 'Gateway', 'up', '2019-11-01 10:00:00', 1.5),
        target(2, 'DNS, primary', 'UP', '2019-10-27 01:30:00', 0.1),
        target(3, 'Web "main"', 'down', '2019-11-01T10:00:00.123Z', 12.345),
        target(4, 'Printer', null, null, null),
        target(5, 'NAS', 'degraded', '2019-03-31 01:30:00', 0),
        target(6, 'Ünïcødé ✓', 'Down', '2019-07-01 12:00:00', 99.99),
        target(7, 'multi\nline', '', '2019-12-31 23:30:00', 1e-7),
    ];
}

const WORLDS = [
    { name: 'empty', seed: () => [] },
    {
        name: 'fleet',
        seed: () => [
            ...users(), ...fleetPeers(), ...activityRows(), ...dlpRows(7), ...auditRows(25), ...ticketRows(),
            ...inventoryRows(), ...alertRows(25), ...networkRows(),
        ],
    },
    {
        name: 'many',
        seed: () => [...users(), ...activityRows(), ...dlpRows(55), ...auditRows(105), ...alertRows(205)],
    },
    {
        name: 'device info that is null',
        seed: () => [peer('200000001', { info: JSON.stringify({ os: 'Fine' }) }), peer('200000002', { info: PG ? JSON.stringify('null') : 'null', note: 'has a note' })],
    },
    {
        name: 'event details that are no JSON',
        seed: () => [
            ...dlpRows(3),
            row('dlp_events', {
                id: 50, device_id: 'D9', event_source: 'usb', event_type: 'info', policy_id: null, policy_name: '',
                action: 'log', details: PG ? JSON.stringify('not json') : 'not json', created_at: '2019-11-02 10:00:00',
            }),
        ],
    },
    {
        name: 'inventory that is no JSON',
        seed: () => [
            ...inventoryRows(),
            row('device_inventory', {
                id: 90, device_id: 'I90', hardware: '{}', software: PG ? JSON.stringify('not json') : 'not json',
                collected_at: null, received_at: '2019-12-01 00:00:00',
            }),
        ],
    },
    {
        name: 'inventory hardware that is no JSON',
        seed: () => [
            row('device_inventory', { id: 1, device_id: 'I1', hardware: '{"os_name":"Fine"}', software: '{}', collected_at: null, received_at: '2019-12-01 00:00:00' }),
            row('device_inventory', {
                id: 2, device_id: 'I2', hardware: PG ? JSON.stringify('{"os_name":') : '{"os_name":', software: '{}',
                collected_at: null, received_at: '2019-12-02 00:00:00',
            }),
        ],
    },
    {
        // 1.5 MB rounds to 2 and 2.5 MB to 3, where cutting gives 1 and 2
        name: 'inventory of two devices with half megabytes',
        seed: () => [
            row('device_inventory', { id: 1, device_id: 'I1', hardware: '{"os_name":"A","memory":{"total_bytes":1572864}}', software: '{}', collected_at: null, received_at: '2019-12-01 00:00:00' }),
            row('device_inventory', { id: 2, device_id: 'I2', hardware: '{"os_name":"B","memory":{"total_bytes":2621440}}', software: '{}', collected_at: null, received_at: '2019-12-02 00:00:00' }),
        ],
    },
    {
        name: 'inventory with infinite memory',
        seed: () => [
            row('device_inventory', { id: 1, device_id: 'I1', hardware: '{"os_name":"Big","memory":{"total_bytes":1e400}}', software: '{}', collected_at: null, received_at: '2019-12-01 00:00:00' }),
            row('device_inventory', { id: 2, device_id: 'I2', hardware: '{"os_name":"Small","memory":{"total_bytes":1048576}}', software: '{}', collected_at: null, received_at: '2019-12-02 00:00:00' }),
        ],
    },
];
if (!PG) {
    WORLDS.push({ name: 'device tags that are no text', seed: () => [peer('300000001', { tags: Buffer.from([1, 2]) })] });
}

// ---- Requests ----

const TYPES = ['devices', 'activity', 'security', 'tickets', 'network', 'inventory', 'alerts'];
const ODD_TYPES = [['devices'], [['alerts']], 'constructor', '__proto__', 'toString', 'hasOwnProperty', 'nope', 'devices ',
    'Devices', 5, true, { a: 1 }, ['devices', 'x'], [], ''];

const RANGES = [
    {}, { from: '' }, { from: 0 }, { from: null }, { to: false },
    { from: '2019-10-31' }, { from: '2019-11-01' }, { to: '2019-10-31' }, { to: '2019-11-01' },
    { from: '2019-10-31', to: '2019-11-01' }, { from: '2019-11-01', to: '2019-10-31' },
    { from: '2019-11-01', to: '2019-11-30' }, { from: '2019-11-30', to: '2019-12-01' },
    { from: '2019-10-31T23:00:00.000Z' }, { from: '2019-10-31T23:00:00Z' }, { from: '2019-11-01T00:00:00+01:00' },
    { from: '2019-11-01 00:00:00' }, { to: '2019-10-31T22:59:59.999Z' }, { to: '2019-10-31T23:59:59.999+01:00' },
    { from: '2019-10-27', to: '2019-10-28' }, { from: '2019-10-27T01:00:00.000Z' }, { from: '2019-10-27T02:00:00+02:00' },
    { from: '2019-11' }, { from: '2019' }, { to: '2019-11' }, { from: 'garbage' }, { from: 'Ü' },
    { from: 5 }, { from: 1.5 }, { from: true }, { to: true }, { from: ['2019-11-01'] }, { from: ['a', 'b'] },
    { from: [] }, { from: {} }, { from: { a: 1 } }, { from: '2019-11-01', to: ['2019-11-30'] },
    { from: { a: 1 }, to: ['2019-11-30'] }, { from: ['2019-10-31', '2019-11-01'] }, { from: '0' },
];

const ECHOES = [
    { device_id: 'D1', limit: 10 }, { device_id: 42, limit: '10' }, { device_id: null, limit: null },
    { device_id: { b: 1, 10: 'x', 2: 'y', a: [1, { c: null }] }, limit: [1.0, 1e21, -0, 1e-7] },
    { device_id: 'Ünïcødé ✓ ', limit: true }, { device_id: '', limit: 0 },
];

function sectionsOf(report) {
    const keys = report && report.data && typeof report.data === 'object' ? Object.keys(report.data) : [];
    return [undefined, ...keys, 'nope', ['targets'], 0, '', '__proto__', 'constructor', 'length'];
}

// The rows the CSV route exports, as routes/reports.routes.js picks them
function csvRows(report, section) {
    if (section && report.data[section] && Array.isArray(report.data[section])) return report.data[section];
    const flat = {};
    for (const [k, v] of Object.entries(report.data)) {
        if (typeof v !== 'object' || v === null) flat[k] = v;
    }
    return Object.keys(flat).length > 0 ? [flat] : [];
}

const enc = (v) => (v === undefined ? '@undefined' : JSON.stringify(v));

// ---- toCsv of rows written by hand ----

const CSV_ROWS = [
    [],
    [{}],
    [{ a: 1, b: 'x' }, { a: 2, b: 'y' }],
    [{ 'a,b': 'quote "me"', 'line\nkey': 'multi\nline', cr: 'only\rcarriage', plain: 'plain' }],
    [{ n: null, u: 'x', z: 0, f: false, t: true, e: '' }, { n: 'n', z: -0, f: 1e21, t: 1e-7, e: 1.5 }],
    [{ list: [1, 'a', null, [2, 3]], obj: { a: 1 }, empty: [] }],
    [{ a: 1 }, { b: 2 }, { a: 'only a' }],
    [{ 2: 'two', 10: 'ten', a: 'a', 1: 'one' }],
    [{ unicode: 'Ünïcødé ✓ ', emoji: '🚀,🚀' }],
    [{ q: '"', qq: '""', comma: ',', nl: '\n', both: '",\n' }],
];

async function main() {
    const db = await openDatabase();
    fixClock();
    const engine = web('services/reportEngine');
    const realError = console.error;
    const realLog = console.log;
    const realWarn = console.warn;
    console.error = () => {};
    console.log = () => {};
    console.warn = () => {};
    const worlds = [];
    try {
        for (const world of WORLDS) {
            const seed = [...CLEAR, ...world.seed()];
            for (const [sql, params] of seed) await db.exec(sql, params);
            const requests = [];
            const types = world.name === 'empty' ? [...TYPES, ...ODD_TYPES] : TYPES;
            for (const type of types) {
                const options = type === 'activity' ? RANGES : (type === 'devices' && world.name === 'empty' ? [{}, ...ECHOES] : [{}]);
                for (const opts of options) requests.push({ type, ...opts });
            }
            const cases = [];
            for (const body of requests) {
                const { type, from, to, device_id, limit } = body;
                const recorded = { body: JSON.stringify(body) };
                try {
                    const report = await engine.generateReport(type, { from, to, device_id, limit });
                    recorded.report = JSON.stringify(report);
                    recorded.csv = sectionsOf(report).map((section) => ({
                        section: enc(section),
                        csv: engine.toCsv(csvRows(report, section)),
                    }));
                } catch (err) {
                    recorded.error = err.message.startsWith('Unknown report type') ? err.message : `other: ${err.message}`;
                }
                cases.push(recorded);
            }
            worlds.push({ name: world.name, seed: seed.map(([sql, params]) => [sql, params.map((p) => (Buffer.isBuffer(p) ? { buffer: [...p] } : p))]), cases });
        }
    } finally {
        console.error = realError;
        console.log = realLog;
        console.warn = realWarn;
        await db.close();
    }
    const csv = CSV_ROWS.map((rows) => ({ rows: JSON.stringify(rows), csv: engine.toCsv(rows) }));
    const out = { dialect, zone, now: NOW, types: JSON.stringify(engine.listReportTypes()), worlds, csv };
    const file = path.join(__dirname, '..', `engine_${dialect}_${zone.replace(/\//g, '-')}.json`);
    fs.writeFileSync(file, `${JSON.stringify(out, null, 1)}\n`);
    const n = worlds.reduce((sum, w) => sum + w.cases.length, 0);
    realLog(`${file}: ${worlds.length} worlds, ${n} reports, ${csv.length} csv`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
