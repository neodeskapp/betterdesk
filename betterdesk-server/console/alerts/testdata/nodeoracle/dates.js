'use strict';

/**
 * Records Date.parse for texts of every form the offline sweep could meet
 * as a device's last_online. Run once per time zone:
 *
 *   TZ=UTC node nodeoracle/dates.js utc
 *   TZ=Europe/Berlin node nodeoracle/dates.js berlin
 *   TZ=America/St_Johns node nodeoracle/dates.js stjohns
 *
 * Writes ../dates_<name>.json.gz: { tz, vectors: [[text, milliseconds or
 * null], ...] }. The texts are the same in every zone; the answers differ
 * where a text names no zone.
 */

const lib = require('./lib');

const name = process.argv[2] || 'utc';
const r = lib.rng(4711);

const CURATED = ['', ' ', '2026-11-09T10:00:00Z', '2026-11-09T10:00:00.000Z', '2026-11-09T10:00:00.123456789Z', '2026-11-09T10:00:00+01:00',
    '2026-11-09T10:00:00-0530', '2026-11-09T10:00:00', '2026-11-09T10:00', '2026-11-09 10:00:00', '2026-11-09', '2026-11', '2026', '0001-01-01T00:00:00Z',
    '0000-01-01T00:00:00Z', '+002026-11-09T10:00:00Z', '-000001-01-01T00:00:00Z', '-000000-01-01T00:00:00Z', '+275760-09-13T00:00:00.000Z',
    '+275760-09-13T00:00:00.001Z', '-271821-04-20T00:00:00.000Z', '-271821-04-19T23:59:59.999Z', '2026-11-09T24:00:00Z', '2026-11-09T24:00:00.000Z',
    '2026-11-09T24:00:01Z', '2026-11-09T24:01:00Z', '2026-11-09T23:59:60Z', '2026-02-30T00:00:00Z', '2026-02-31', '2026-13-01', '2026-00-01', '2026-11-00',
    '2026-11-32', '2026-1-9', '2026-11-9', '26-11-09', '2026-11-09T1:00:00Z', '2026-11-09T10:0:00Z', '2026-11-09T10:00:0Z', '2026-11-09T10:00:00.Z',
    '2026-11-09T10:00:00.1Z', '2026-11-09T10:00:00.12Z', '2026-11-09T10:00:00.1234Z', '2026-11-09T10:00:00.0000000001Z', '2026-11-09T10:00:00.9999999999Z',
    '2026-11-09T10:00:00z', '2026-11-09t10:00:00Z', '2026-11-09T10:00:00 Z', '2026-11-09T10:00:00Z ', ' 2026-11-09T10:00:00Z', '2026-11-09T10:00:00+1',
    '2026-11-09T10:00:00+01', '2026-11-09T10:00:00+0100', '2026-11-09T10:00:00+01:0', '2026-11-09T10:00:00+24:00', '2026-11-09T10:00:00+23:59',
    '2026-11-09T10:00:00+2360', '2026-11-09T10:00:00GMT', '2026-11-09T10:00:00 GMT', '2026-11-09 10:00:00 GMT', '2026-11-09 10:00:00Z', '2026-11-09 10:00:00+01:00',
    '2026-11-09 10:00:00 +0100', '2026-11-09 10:00:00.5', '2026/11/09 10:00:00', '2026/11/09', '11/09/2026', '11/9/26', '11/9', '9/11/2026 10:00 PM', '31/12/2026',
    '12/31/2026', '13/31/2026', 'Nov 9 2026', 'Nov 9, 2026', '9 Nov 2026', 'November 9, 2026 10:00:00', 'Novembruary 9 2026', 'Nov 9', 'Nov', 'Nov 2026', '2026 Nov 9',
    'Mon Nov 09 2026 10:00:00 GMT+0100 (Central European Standard Time)', 'Mon, 09 Nov 2026 10:00:00 GMT', 'Monday, 09-Nov-26 10:00:00 GMT', 'Mon Nov  9 10:00:00 2026',
    'Mon Nov 09 2026', 'Tue Nov 09 2026', 'Xyz Nov 09 2026', '09 Nov 2026 10:00 UTC', '09 Nov 2026 10:00 UT', '09 Nov 2026 10:00 EST', '09 Nov 2026 10:00 EDT',
    '09 Nov 2026 10:00 CST', '09 Nov 2026 10:00 CDT', '09 Nov 2026 10:00 MST', '09 Nov 2026 10:00 MDT', '09 Nov 2026 10:00 PST', '09 Nov 2026 10:00 PDT',
    '09 Nov 2026 10:00 CET', '09 Nov 2026 10:00 Z', '09 Nov 2026 10:00 z', '09 Nov 2026 10:00 GMT+1', '09 Nov 2026 10:00 GMT+01', '09 Nov 2026 10:00 GMT+100',
    '09 Nov 2026 10:00 GMT+0100', '09 Nov 2026 10:00 GMT+01:30', '09 Nov 2026 10:00 GMT+12345', '09 Nov 2026 10:00 GMT-8', '09 Nov 2026 10:00 +0100', '09 Nov 2026 10:00 -0100',
    '09 Nov 2026 10:00+01:00', '09 Nov 2026 10:00 UTC+', '09 Nov 2026 10:00 GMT+:30', '09 Nov 2026 10:00 AM', '09 Nov 2026 10:00 PM', '09 Nov 2026 12:00 AM',
    '09 Nov 2026 12:00 PM', '09 Nov 2026 13:00 PM', '09 Nov 2026 0:00 AM', '09 Nov 2026 10 PM', 'PM 09 Nov 2026 10:00', '09 Nov 2026 10:00:00.5', '09 Nov 2026 10:00:00.',
    '09 Nov 2026 10::', '09 Nov 2026 10::30', '09 Nov 2026 10:', '09 Nov 2026 10:30:', '09 Nov 2026 10:30:15:', '09 Nov 2026 10:30:15:500', '09 Nov 2026 10:30:15.500.2',
    '09 Nov 2026 24:00', '09 Nov 2026 24:00:00', '09 Nov 2026 24:01', '09 Nov 2026 25:00', '09 Nov 2026 10:60', '09 Nov 2026 10:00:60', '10:00', '10:00:00', '10:00 Nov 9 2026',
    '1700000000000', '1700000000', '12', '0', '00', '1', '32', '99', '100', '1000', '9999', '10000', '99999', '2026.5', '1e3', '-1', '+1', '-2026', '0x10',
    'never', 'null', 'undefined', 'true', 'false', 'NaN', 'Infinity', 'now', 'today', 'T', 'Z', 'GMT', 'T10:00:00Z', '(2026-11-09)', '2026-11-09 (comment)',
    '(a (b) c) 2026-11-09', '2026-11-09 (unclosed', ') 2026-11-09', '2026-11-09 )', '2026-11-09 -', '- 2026-11-09', '2026 - 11 - 09', '2026.11.09', '09.11.2026', '9.11.2026 10:00',
    '2026-11-09T10:00:00Z\u0000junk', '\u00002026-11-09', '2026-11-09 ', ' 2026-11-09T10:00:00Z', '2026-11-09 10:00', '2026-11-09 10:00', '2026-11-09\n10:00',
    'Nov 9 2026', '２０２６-11-09', 'ноя 9 2026', 'Nov 9 2026 10:00 ü', 'ü Nov 9 2026', 'ü9 Nov 2026', 'x 9 Nov 2026', 'x9 Nov 2026', '9x Nov 2026', 'Sept 9 2026',
    'Sep 9 2026', 'Septiembre 9 2026', 'jan 1 70', 'jan 1 49', 'jan 1 50', 'jan 1 99', 'jan 1 100', 'jan 1 0', 'jan 1 00', 'jan 32 2026', 'jan 0 2026', '2026 jan', 'jan 2026 15',
    '15 jan 2026 30', '1 2 3', '1 2 3 4', '2026 1 2', '32 1 2', '1 32 2', '1 2 32', '12 31 2026', '2026 12 31', '99 12 31', '1/2/3 4:5:6', '1/2/3 4:5:6.7', '1/2/3 4:5:6.789012',
    '2026-11-09T10:00:00+01:00[Europe/Berlin]', '2026-W46-1', '2026-313', '20261109', '20261109T100000Z', '2026-11-09T10:00:00,5Z', '2026-11-09T10.5Z',
    '1 Jan 2000 00:00:00 GMT+400000:', '1 Jan 2000 00:00:00 GMT+700000:', '1 Jan 2000 00:00:00 GMT+1200000:', '1 Jan 2000 00:00:00 GMT+99', '1 Jan 2000 00:00:00 GMT+9999',
    '1 Jan 1000000', '1 Jan 1000001', '1 Jan 999999', '1 Jan 275760', '1 Jan 275761', '1 Jan 100000000', '1 Jan 999999999', '1 Jan 9999999999', '1 Jan 2000 00:00:00 UTC+1 2',
    '2021-03-28T02:30:00', '2021-03-28T01:59:59', '2021-03-28T03:00:00', '2021-10-31T02:30:00', '2021-10-31T01:30:00', '2021-10-31T03:00:00', '2021-03-14T02:30:00',
    '2021-11-07T01:30:00', '28 Mar 2021 02:30', '31 Oct 2021 02:30', '1890-01-01T00:00:00', '1945-05-08T12:00:00', '1970-01-01T00:00:00', '2100-07-01T12:00:00', '9999-12-31T23:59:59',
    '+010000-01-01T00:00:00', '+275760-09-12T23:00:00', '+275760-09-13T01:00:00', '-271821-04-20T01:00:00', '1 Jan 1970', '1 Jul 1970', '1 Jan 1900 12:00', '1 Jul 2090 12:00'];

const MONTHS = ['Jan', 'feb', 'MAR', 'April', 'May', 'June', 'Jul', 'Augustus', 'Sep', 'Oct', 'Nov', 'December'];
const WORDS = ['AM', 'PM', 'am', 'pm', 'UTC', 'UT', 'GMT', 'Z', 'z', 'EST', 'EDT', 'CST', 'CDT', 'MST', 'MDT', 'PST', 'PDT', 'T', 't', 'Mon', 'Tuesday', 'at', 'CET', 'x', 'ü', 'utcx', 'gmtx', 'amx', 'tz'];
const SEPS = [' ', ' ', ' ', '-', '/', ':', '.', ',', 'T', '+', '  ', ', ', '::', '(', ')', '\t', ' ', '', '', ''];
const digits = (n) => {
    let s = '';
    for (let i = 0; i < n; i += 1) s += String(r.int(10));
    return s;
};
function number() {
    switch (r.int(12)) {
        case 0: return String(1 + r.int(12));
        case 1: return String(1 + r.int(31));
        case 2: return String(1970 + r.int(80));
        case 3: return String(r.int(60));
        case 4: return String(r.int(24)).padStart(2, '0');
        case 5: return String(r.int(100)).padStart(2, '0');
        case 6: return digits(1 + r.int(4));
        case 7: return digits(3 + r.int(9));
        case 8: return String(r.int(1000)).padStart(3, '0');
        case 9: return String(r.int(32));
        case 10: return `0${r.int(10)}`;
        default: return String(r.int(10000));
    }
}
function iso() {
    const p2 = (n) => String(n).padStart(2, '0');
    const loose = r.chance(0.15);
    let s = '';
    if (r.chance(0.1)) s += `${r.pick(['+', '-'])}${digits(loose ? 5 : 6)}`;
    else s += String(r.chance(0.9) ? 1900 + r.int(200) : r.int(10000)).padStart(loose ? 3 : 4, '0');
    if (r.chance(0.9)) {
        s += `-${p2(loose ? r.int(14) : 1 + r.int(12))}`;
        if (r.chance(0.9)) s += `-${p2(loose ? r.int(33) : 1 + r.int(28))}`;
    }
    if (r.chance(0.85)) {
        s += r.pick(['T', 'T', 'T', ' ', 't']);
        s += `${p2(loose ? r.int(26) : r.int(24))}:${p2(loose ? r.int(62) : r.int(60))}`;
        if (r.chance(0.8)) {
            s += `:${p2(loose ? r.int(62) : r.int(60))}`;
            if (r.chance(0.6)) s += `.${digits(1 + r.int(r.chance(0.8) ? 3 : 12))}`;
        }
        switch (r.int(8)) {
            case 0: case 1: case 2: s += 'Z'; break;
            case 3: s += `${r.pick(['+', '-'])}${p2(r.int(loose ? 26 : 24))}:${p2(r.int(60))}`; break;
            case 4: s += `${r.pick(['+', '-'])}${p2(r.int(24))}${p2(r.int(60))}`; break;
            case 5: s += r.pick([' Z', 'z', ' GMT', '+1', 'UTC', ' +01:00', '-08', ' EST']); break;
            default:
        }
    }
    return s;
}
function legacy() {
    let s = '';
    const parts = 1 + r.int(8);
    for (let i = 0; i < parts; i += 1) {
        switch (r.int(10)) {
            case 0: case 1: case 2: case 3: s += number(); break;
            case 4: s += r.pick(MONTHS); break;
            case 5: s += r.pick(WORDS); break;
            case 6: s += `${String(r.int(24))}:${String(r.int(60)).padStart(2, '0')}${r.chance(0.5) ? `:${String(r.int(60)).padStart(2, '0')}` : ''}${r.chance(0.2) ? `.${digits(1 + r.int(5))}` : ''}`; break;
            case 7: s += `${r.pick(['GMT', 'UTC', '', 'Z'])}${r.pick(['+', '-'])}${digits(r.int(6))}${r.chance(0.3) ? `:${digits(2)}` : ''}`; break;
            case 8: s += `(${r.pick(['x', 'CET', '1', '(y)', ''])}${r.chance(0.9) ? ')' : ''}`; break;
            default: s += r.pick(['-', '/', '.', ',', ':', '+', ')', '@', '#', ' ', '\n', '*']);
        }
        s += r.pick(SEPS);
    }
    return s;
}
function mutate(s) {
    if (!s) return s;
    const at = r.int(s.length);
    switch (r.int(5)) {
        case 0: return s.slice(0, at) + s.slice(at + 1);
        case 1: return s.slice(0, at) + r.pick([' ', '-', ':', '.', 'T', 'Z', '0', '9', '+', '/', 'x']) + s.slice(at);
        case 2: return s.slice(0, at) + r.pick(['0', '1', '9', ' ', '-', ':']) + s.slice(at + 1);
        case 3: return s.slice(0, at);
        default: return s + r.pick([' ', 'Z', ' GMT', '.5', ' PM', '+01:00', ' (x)', ' x', '-', ':']);
    }
}

const texts = new Set(CURATED);
for (let i = 0; i < 14000; i += 1) texts.add(iso());
for (let i = 0; i < 14000; i += 1) texts.add(legacy());
for (const base of [...texts].slice(0, 9000)) texts.add(mutate(base));
for (const base of CURATED) for (let i = 0; i < 12; i += 1) texts.add(mutate(mutate(base)));

const vectors = [...texts].map((text) => {
    const ms = Date.parse(text);
    return [text, Number.isNaN(ms) ? null : ms];
});
console.log(lib.writeVectors(`dates_${name}`, { tz: process.env.TZ || '', vectors }, `${vectors.length} texts, ${vectors.filter((v) => v[1] !== null).length} of them dates`));
process.exit(0);
