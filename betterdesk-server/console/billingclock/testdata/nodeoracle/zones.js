'use strict';

// Which time zone names the Node console takes, and what Intl makes of
// them. Writes ../../zones.txt (the table the package embeds: every name
// Intl.DateTimeFormat accepts, with the name Intl reports for it) and
// ../zones.json (vectors: names, case variants, offsets and other text,
// each with whether it is valid and what it resolves to).
//
// Candidates come from the ICU data inside the node binary (zone names
// are stored there as UTF-16 text), the system's zoneinfo folder,
// Intl.supportedValuesOf and the old three-letter names ICU still knows.

const fs = require('fs');
const path = require('path');

function resolve(name) {
    try {
        return new Intl.DateTimeFormat('en-US', { timeZone: name }).resolvedOptions().timeZone;
    } catch (_) {
        return null;
    }
}

const candidates = new Set();

// UTF-16LE runs in the node binary that look like zone names
const binary = fs.readFileSync(process.execPath);
const zoneChar = (c) => (c >= 0x41 && c <= 0x5a) || (c >= 0x61 && c <= 0x7a) || (c >= 0x30 && c <= 0x39) || c === 0x2f || c === 0x5f || c === 0x2d || c === 0x2b;
for (const start of [0, 1]) {
    let run = '';
    for (let i = start; i + 1 < binary.length; i += 2) {
        const c = binary[i];
        if (binary[i + 1] === 0 && zoneChar(c)) {
            run += String.fromCharCode(c);
            continue;
        }
        if (run.length >= 2 && run.length <= 40 && /^[A-Za-z]/.test(run)) candidates.add(run);
        run = '';
    }
}

// The zoneinfo folder
function walk(dir, prefix) {
    let entries = [];
    try {
        entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch (_) {
        return;
    }
    for (const e of entries) {
        const name = prefix ? `${prefix}/${e.name}` : e.name;
        if (e.isDirectory()) walk(path.join(dir, e.name), name);
        else candidates.add(name);
    }
}
walk('/usr/share/zoneinfo', '');

for (const z of Intl.supportedValuesOf('timeZone')) candidates.add(z);
for (const z of ['ACT', 'AET', 'AGT', 'ART', 'AST', 'BET', 'BST', 'CAT', 'CNT', 'CST', 'CTT', 'EAT', 'ECT', 'IET', 'IST', 'JST',
    'MIT', 'NET', 'NST', 'PLT', 'PNT', 'PRT', 'PST', 'SST', 'VST', 'SystemV/AST4', 'SystemV/AST4ADT', 'SystemV/CST6',
    'SystemV/CST6CDT', 'SystemV/EST5', 'SystemV/EST5EDT', 'SystemV/HST10', 'SystemV/MST7', 'SystemV/MST7MDT',
    'SystemV/PST8', 'SystemV/PST8PDT', 'SystemV/YST9', 'SystemV/YST9YDT', 'Etc/Unknown', 'Factory']) candidates.add(z);

const valid = new Map();
for (const name of candidates) {
    const resolved = resolve(name);
    if (resolved !== null && !/^[+-]/.test(resolved)) valid.set(name, resolved);
}

// Names that differ only in letter case are one name to Intl. Keep the
// spelling Intl reports when it is one of them, else the first found.
const byLower = new Map();
for (const [name, resolved] of [...valid].sort((a, b) => (a[0] < b[0] ? -1 : 1))) {
    const key = name.toLowerCase();
    const kept = byLower.get(key);
    if (!kept || name === resolved) byLower.set(key, [name, resolved]);
}
const table = [...byLower.values()].sort((a, b) => (a[0].toLowerCase() < b[0].toLowerCase() ? -1 : 1));
fs.writeFileSync(path.join(__dirname, '..', '..', 'zones.txt'),
    '# Every time zone name Intl.DateTimeFormat of Node 22 (ICU ' + process.versions.icu + ', tz ' + process.versions.tz + ') takes,\n'
    + '# in any letter case, and the name it reports for it. Written by testdata/nodeoracle/zones.js.\n'
    + table.map(([n, r]) => `${n} ${r}`).join('\n') + '\n');

// Vectors
const vectors = [];
const add = (input) => vectors.push([input, resolve(input)]);
const toggle = (s) => [...s].map((c, i) => (i % 2 ? c.toLowerCase() : c.toUpperCase())).join('');
for (const [name] of table) {
    add(name);
    add(name.toLowerCase());
    add(name.toUpperCase());
    add(toggle(name));
}
// Of the other candidates, the ones shaped like a zone name: with a slash,
// or short and in capitals (most runs from the binary are neither)
for (const name of candidates) {
    if (!valid.has(name) && (name.includes('/') || /^[A-Z][A-Z0-9+-]{1,7}$/.test(name))) add(name);
}
const signs = ['+', '-', '−', ''];
const hours = ['0', '00', '01', '09', '12', '14', '18', '23', '24', '25', '99', '001', '1'];
const minutes = ['', '00', ':00', '30', ':30', ':45', '59', ':59', '60', ':60', ':5', '5', ':000', '0000', ':00:00', ':00:30', '.5', ':30.5'];
for (const s of signs) for (const h of hours) for (const m of minutes) add(s + h + m);
for (const raw of ['', ' ', 'UTC ', ' UTC', 'U TC', 'Europe/Berlin\u0000', 'Europe\\Berlin', 'Europe/Berlin/', '/Europe/Berlin',
    'Europe//Berlin', 'Europe/', '/', 'Etc/GMT+0', 'Etc/GMT-0', 'Etc/GMT+12', 'Etc/GMT-14', 'Etc/GMT+13', 'Etc/GMT-15', 'Etc/GMT+1:00',
    'GMT+1', 'GMT-01:00', 'UTC+1', 'UTC+01:00', 'Z', 'z', 'Überall/Zeit', 'Europe/Zürich', 'Europe/Zurich', 'Asia/Ho_Chi_Minh',
    'America/Port-au-Prince', 'America/Port-Au-Prince', 'AMERICA/PORT-AU-PRINCE', 'Antarctica/DumontDUrville', 'EST5EDT', 'est5edt',
    'Est5edt', 'CST6CDT', 'MST7MDT', 'PST8PDT', 'SystemV/AST4', 'systemv/ast4', 'Etc/Unknown', 'Factory', 'localtime', 'posixrules',
    'posix/Europe/Berlin', 'right/UTC', 'Europe/Kyiv', 'europe/kyiv', 'Pacific/Kanton', 'America/Ciudad_Juarez', 'Europe/Berlin\t',
    '﻿UTC', 'UTC​', 'Europe/Berlin?', 'Europe/Ber*', '+01:00 ', '+1:00', '+01:0', '01:00', '+01:00Z', 'GMT', 'gmt', 'Gmt',
    'ETC/UTC', 'etc/gmt', 'Etc/GMT0', 'etc/gmt0', 'GMT0', 'GMT+0', 'GMT-0', 'Greenwich', 'UCT', 'Universal', 'Zulu', 'ZULU',
    'Europe/Berlİn', 'Europe/Kıev', 'Aſia/Saigon', 'ＵＴＣ', 'Europe/Berliń', 'EUROPE/BERLIN',
    'Asia/Kolkata', 'asia/calcutta', 'America/Argentina/ComodRivadavia', 'america/argentina/comodrivadavia']) add(raw);

fs.writeFileSync(path.join(__dirname, '..', 'zones.json'), JSON.stringify({
    node: process.version, icu: process.versions.icu, tz: process.versions.tz, vectors,
}) + '\n');
console.log(`zones.txt: ${table.length} names from ${candidates.size} candidates; zones.json: ${vectors.length} vectors`);
