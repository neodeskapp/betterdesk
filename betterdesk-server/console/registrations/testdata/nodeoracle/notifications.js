'use strict';

/**
 * Records what services/registrationNotifications.js makes of LAN rows and
 * enrollment queue entries, and what listRegistrationNotifications returns
 * for both sources, into ../notifications.json. See README.md.
 *
 *   TZ=Europe/Berlin node notifications.js
 *
 * The clock is fixed (FIXED below), so a value that is no date gives the
 * same time in Go. Rows of PostgreSQL carry their times as Date objects;
 * in the file such a value is {"$date": "<ISO text>"}.
 */

const fs = require('fs');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const FIXED = Date.UTC(2026, 10, 18, 12, 34, 56, 789);

const RealDate = Date;
class FixedDate extends RealDate {
    constructor(...args) {
        if (args.length === 0) super(FIXED);
        else super(...args);
    }

    static now() { return FIXED; }
}
global.Date = FixedDate;

const {
    enrollmentToNotification,
    lanRegistrationToNotification,
    listRegistrationNotifications,
} = require(path.join(WEB, 'services/registrationNotifications'));

/** A value of the file as JavaScript: {"$date": text} is a Date. */
function revive(v) {
    if (Array.isArray(v)) return v.map(revive);
    if (v && typeof v === 'object') {
        if (Object.keys(v).length === 1 && typeof v.$date === 'string') return new Date(v.$date);
        const out = {};
        for (const [k, x] of Object.entries(v)) out[k] = revive(x);
        return out;
    }
    return v;
}

function outcome(fn) {
    try {
        const value = fn();
        return value === undefined ? { undefined: true } : { value };
    } catch (err) {
        return { error: err.message };
    }
}

// Times in every form a database, the API server or a broken source can
// give: SQLite text (local time), ISO with and without zone and fraction,
// dates alone, the six digit years of toISOString, numbers, flags, lists.
const times = [
    undefined, null, '', 0, 1, -1, false, true, 'nonsense', '2026-13-01', '2026-02-30',
    '2026-11-18 10:00:00', '2026-11-18 10:00', '2026-11-18 10:00:00.5', '2026-11-18T10:00:00',
    '2026-11-18T10:00:00Z', '2026-11-18T10:00:00.123456789Z', '2026-11-18T10:00:00+02:00',
    '2026-11-18T10:00:00-0130', '2026-11-18t10:00:00z', '2026-11-18', '2026-11', '2026',
    '2026-11-18Z', '2026-11-18T24:00:00Z', '2026-11-18T23:59:60Z', '+002026-11-18T10:00:00.000Z', '+010000-01-01T00:00:00.000Z', '+099999-12-31T23:59:59.999Z',
    '-000001-01-01T00:00:00.000Z', '+275760-09-13T00:00:00.000Z', '+275760-09-13T00:00:00.001Z',
    '0000-01-01T00:00:00Z', '1969-12-31T23:59:59.999Z', 1795000000000, 8.64e15, 8.64e15 + 1, -8.64e15,
    1.5, -0.5, 1e21, [2026], ['2026-11-18'], [], {}, { $date: '2001-02-03T04:05:06.789Z' },
];

const vectors = { fixed: FIXED, tz: process.env.TZ || '', lan: [], enrollment: [], list: [] };

for (const t of times) {
    vectors.lan.push({ input: { id: 7, device_id: 'D7', hostname: 'pc-7', platform: 'Windows', ip_address: '10.0.0.7', created_at: t }, out: null });
    vectors.enrollment.push({ input: { device_id: 'E7', hostname: 'pc-e', platform: 'Linux', ip: '10.0.0.8', created_at: t }, out: null });
}
// created_at missing, updated_at taken
vectors.lan.push({ input: { id: 8, device_id: 'D8', updated_at: '2026-11-18 09:00:00' }, out: null });
vectors.lan.push({ input: { id: 8, device_id: 'D8', created_at: '', updated_at: { $date: '2001-02-03T04:05:06.789Z' } }, out: null });

// The fields: what is no text, missing, empty, odd
const odd = [undefined, null, '', 0, 5, -0, 1.5, true, false, 'x', 'a b', ['a', 'b'], [null, 1], [], {}, { a: 1 }, ' ', 'Ünïcödé 😀'];
for (const v of odd) {
    vectors.lan.push({ input: { id: 9, device_id: v, hostname: v, platform: v, ip_address: v, created_at: '2026-11-18 10:00:00' }, out: null });
    vectors.lan.push({ input: { id: v, device_id: 'D9', created_at: '2026-11-18 10:00:00' }, out: null });
    vectors.enrollment.push({ input: { device_id: v, hostname: v, platform: v, ip: v, created_at: '2026-11-18T10:00:00Z' }, out: null });
    vectors.enrollment.push({ input: { device_id: 'E9', hostname: v, platform: 'P', ip: v, created_at: '2026-11-18T10:00:00Z' }, out: null });
}
for (const v of [undefined, null, 0, '', 'text', 5, true, [], ['E1'], {}]) {
    vectors.lan.push({ input: v, out: null });
    vectors.enrollment.push({ input: v, out: null });
}

for (const vec of vectors.lan) vec.out = outcome(() => lanRegistrationToNotification(revive(vec.input)));
for (const vec of vectors.enrollment) vec.out = outcome(() => enrollmentToNotification(revive(vec.input)));

// The whole list: both sources, one failing, odd answers of the API
// server's client, the order by time
const lanRows = [
    { id: 1, device_id: 'L1', hostname: 'lan-1', created_at: '2026-11-18 10:00:00' },
    { id: 2, device_id: 'L2', hostname: '', platform: 'Linux', created_at: '2026-11-18 12:00:00' },
    { id: 3, device_id: 'L3', created_at: 'not a time' },
    { id: 4, device_id: 'L4', created_at: '2026-11-18 11:00:00' },
    { id: 5, device_id: 'L5', created_at: '2026-11-18 11:00:00' },
];
const pending = (data) => ({ success: true, data, count: Array.isArray(data) ? data.length : 0 });
const enrollmentData = [
    { device_id: 'E1', hostname: 'enr-1', created_at: '2026-11-18T10:30:00Z' },
    { device_id: 'E2', created_at: '2026-11-18T11:00:00Z' },
    { device_id: '', created_at: '2026-11-18T11:00:00Z' },
    { created_at: '2026-11-18T11:00:00Z' },
    null,
    'E3',
    { device_id: 'E4' },
    { device_id: 'E5', created_at: '2026-11-18T09:00:00.000+01:00' },
];
const lists = [
    { lan: lanRows, api: pending(enrollmentData) },
    { lan: [], api: pending([]) },
    { lan: lanRows, api: { success: false, error: 'connection refused', data: [], count: 0 } },
    { lan: lanRows, api: { success: true, data: { devices: [] }, count: 0 } },
    { lan: lanRows, api: { success: true, data: 'E1', count: 1 } },
    { lan: lanRows, api: { success: true, count: 1 } },
    { lan: lanRows.slice(3), api: pending([{ device_id: 'E7', created_at: '2026-11-18T11:00:00Z' }, { device_id: 'E8', created_at: '2026-11-18 11:00:00' }]) },
    { lan: [{ id: 6, device_id: 'L6', created_at: { $date: '2026-11-18T08:00:00.000Z' } }], api: pending([{ device_id: 'E6', created_at: '2026-11-18T08:00:00.000Z' }]) },
];

(async () => {
    for (const l of lists) {
        const db = { getPendingRegistrations: async (filters) => { l.filters = filters; return revive(l.lan); } };
        const api = { getEnrollmentPending: async () => revive(l.api) };
        const result = await listRegistrationNotifications(db, api);
        vectors.list.push({ lan: l.lan, api: l.api, filters: l.filters, items: result.items, count: result.count });
    }
    // A source that fails is empty
    const failing = await listRegistrationNotifications(
        { getPendingRegistrations: async () => { throw new Error('db down'); } },
        { getEnrollmentPending: async () => revive(pending(enrollmentData)) },
    );
    vectors.list.push({ lan: 'throws', api: pending(enrollmentData), items: failing.items, count: failing.count });
    const both = await listRegistrationNotifications(
        { getPendingRegistrations: async () => revive(lanRows) },
        { getEnrollmentPending: async () => { throw new Error('Go offline'); } },
    );
    vectors.list.push({ lan: lanRows, api: 'throws', items: both.items, count: both.count });

    const file = path.join(__dirname, '..', 'notifications.json');
    const text = JSON.stringify(vectors, (k, v) => (v === undefined ? { $undefined: true } : v), 1);
    fs.writeFileSync(file, `${text}\n`);
    console.log(`${file}: ${vectors.lan.length} LAN, ${vectors.enrollment.length} enrollment, ${vectors.list.length} lists`);
})().catch((err) => { console.error(err); process.exit(1); });
