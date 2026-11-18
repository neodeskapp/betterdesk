'use strict';

/**
 * Runs services/backupArchive.js, services/backupService.js and
 * services/scheduledBackupService.js of the Node console (web-nodejs) on
 * generated input and records every answer, for the Go packages
 * console/backup and console/scheduledbackup to replay. See README.md in
 * this folder.
 *
 *   node vectors.js      writes ../archive.json.gz, ../values.json.gz,
 *                        ../../../scheduledbackup/testdata/schedule.json.gz
 *                        and ../counts.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const childProcess = require('child_process');
const { EventEmitter } = require('events');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..');
const SCHEDULE_OUT = path.join(__dirname, '..', '..', '..', 'scheduledbackup', 'testdata');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'backup-oracle-'));
process.env.NODE_ENV = 'test';
process.env.DATA_DIR = path.join(tmp, 'data');
process.env.KEYS_PATH = path.join(tmp, 'keys');
process.env.DB_PATH = path.join(tmp, 'db.sqlite3');
delete process.env.DATABASE_URL;
process.env.DB_TYPE = 'sqlite';
process.chdir(WEB);

// The SMB test starts smbclient or net; the oracle records the call and
// answers with what the case asks for.
let spawnPlan = { code: 0 };
const spawnCalls = [];
childProcess.spawn = (command, args) => {
    spawnCalls.push([command, args]);
    const child = new EventEmitter();
    child.stdout = new EventEmitter();
    child.stderr = new EventEmitter();
    child.kill = () => {};
    setImmediate(() => {
        if (spawnPlan.errorCode) {
            const err = new Error(`spawn ${command} ${spawnPlan.errorCode}`);
            err.code = spawnPlan.errorCode;
            child.emit('error', err);
            return;
        }
        child.emit('close', spawnPlan.code);
    });
    return child;
};

const archive = require(path.join(WEB, 'services/backupArchive'));
const backupService = require(path.join(WEB, 'services/backupService'));
const db = require(path.join(WEB, 'services/database'));
const settings = new Map();
db.getSetting = async (key, fallback) => (settings.has(key) ? settings.get(key) : (fallback ?? null));
db.setSetting = async (key, value) => { settings.set(key, value); };
const scheduled = require(path.join(WEB, 'services/scheduledBackupService'));

function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return { next, int: (n) => Math.floor(next() * n), pick: (l) => l[Math.floor(next() * l.length)], chance: (p) => next() < p };
}

const b64 = (buf) => Buffer.from(buf).toString('base64');
const errOf = (err) => ({ name: err.constructor.name, message: err.message });

const ODD = [null, true, false, 0, 1, -1, 2.5, 0.5, 1.5, -0.5, 3.4999, 1e21, 1e-7, 720.5, 365.4, 366, 721, 9999, -5, '', ' ', 'x', '24', ' 48 ',
    '0x10', '1e2', 'Infinity', '-Infinity', 'NaN', 'true', 'false', 'path', 'local', 'PATH', [], [5], ['7'], [1, 2], [[3]], {}, { a: 1 },
    { toString: 'x' }, { valueOf: 'y' }, 'ä'.repeat(130), 'a'.repeat(600), '\u{1F600}'.repeat(80), 'a'.repeat(127) + '\u{1F600}',
    'a'.repeat(511) + '\u{1F600}x', ' nbsp\u00a0', '\u2028sep\u2029', 'line\nbreak', 'tab\there', '\ufeffbom'];

// ---- archive ----

function tarVectors() {
    const r = rng(1);
    const out = [];
    const names = ['manifest.json', 'console/data.json', '/leading/slash', '//two', 'back\\slash\\name', '', 'a',
        'd/' + 'x'.repeat(98), 'd/' + 'x'.repeat(99), 'x'.repeat(100), 'x'.repeat(101), 'p'.repeat(150) + '/' + 'n'.repeat(99),
        'p'.repeat(154) + '/' + 'n'.repeat(100), 'p'.repeat(156) + '/' + 'n'.repeat(10), 'a/'.repeat(80) + 'end',
        'ü'.repeat(60), 'ü'.repeat(40) + '/' + 'ö'.repeat(45), '\u{1F600}'.repeat(30) + '/' + 'f', 'dir/' + '\u{1F600}'.repeat(26),
        'q'.repeat(60) + '/' + 'ü'.repeat(80) + '/' + 'r'.repeat(50)];
    const sizes = [0, 1, 100, 511, 512, 513, 1024, 1500, 4096];
    for (let i = 0; i < 400; i++) {
        const entries = [];
        const n = 1 + r.int(4);
        for (let j = 0; j < n; j++) {
            const size = r.pick(sizes);
            const data = Buffer.alloc(size);
            const seed = r.int(256);
            for (let k = 0; k < size; k++) data[k] = (k * 13 + seed) & 0xff;
            entries.push({ name: r.pick(names), data: b64(data), mode: r.pick([undefined, 0, 0o600, 0o644, 0o755, 0o100644, 0o7777]) });
        }
        const mtime = r.pick([1, 1700000000, 1762800000, 8589934591]);
        const input = entries.map((e) => ({ name: e.name, data: Buffer.from(e.data, 'base64'), mode: e.mode, mtime }));
        try {
            const gz = archive.createTarGz(input);
            out.push({ entries, mtime, tar: b64(zlib.gunzipSync(gz)) });
        } catch (err) {
            out.push({ entries, mtime, error: errOf(err) });
        }
    }
    return out;
}

function header(fields) {
    const h = Buffer.alloc(512, 0);
    const put = (offset, len, value) => {
        if (value === undefined) return;
        const b = Buffer.isBuffer(value) ? value : Buffer.from(String(value), 'latin1');
        b.copy(h, offset, 0, Math.min(len, b.length));
    };
    put(0, 100, fields.name);
    put(100, 8, fields.mode ?? '0000644\0');
    put(124, 12, fields.size);
    put(136, 12, fields.mtime ?? '00000000000\0');
    put(148, 8, '        ');
    put(156, 1, fields.type ?? '0');
    put(257, 6, 'ustar\0');
    put(345, 155, fields.prefix);
    let sum = 0;
    for (let i = 0; i < 512; i++) sum += h[i];
    put(148, 7, sum.toString(8).padStart(6, '0') + '\0');
    return h;
}

const octal = (n, width = 11) => n.toString(8).padStart(width, '0') + '\0';
const pad = (b) => (b.length % 512 === 0 ? b : Buffer.concat([b, Buffer.alloc(512 - (b.length % 512))]));

// Size fields that do not make the old reader loop for ever: a negative
// size that is a multiple of 512 leads it back to the same header.
function sizeField(r, size) {
    const forms = [
        () => octal(size), () => octal(size, 10) + ' ', () => ' ' + octal(size, 9) + ' ', () => octal(size).replace(/^0+/, ''),
        () => '+' + octal(size, 10), () => '\0\0' + size.toString(8), () => size.toString(8) + 'zz', () => '\t' + size.toString(8),
        () => '1 2', () => 'garbage', () => '', () => '-' + r.pick(['1', '7', '10', '1001', '777', '2000001']), () => '77777777777\0',
        () => Buffer.from([0xb1, 0xb0, 0xb0, 0x00]),
    ];
    return r.pick(forms)();
}

function extractVectors() {
    const r = rng(2);
    const out = [];
    const nameForms = [
        () => 'manifest.json', () => 'console/uploads/logo.png', () => 'console/uploads/../evil', () => 'a\0hidden',
        () => 'a\0b\nc\0d', () => 'line\nbreak\0x', () => Buffer.from([0x61, 0xff, 0x62]), () => Buffer.from([0xe2, 0x82]),
        () => Buffer.from([0xed, 0xa0, 0x80, 0x41]), () => Buffer.from([0xf0, 0x9f, 0x98]), () => 'x'.repeat(100), () => '',
        () => Buffer.from('caf\u00e9 \u2028 \u2029 end\0tail', 'utf8'), () => 'dup',
    ];
    for (let i = 0; i < 2500; i++) {
        const blocks = [];
        const n = r.int(5);
        for (let j = 0; j < n; j++) {
            const size = r.pick([0, 1, 5, 511, 512, 513, 700, 1100]);
            const data = Buffer.alloc(size);
            const seed = r.int(256);
            for (let k = 0; k < size; k++) data[k] = (k * 31 + seed) & 0xff;
            const fields = {
                name: r.pick(nameForms)(),
                prefix: r.chance(0.2) ? r.pick(['console', 'a/b', 'p\0q', '', Buffer.from([0xc3])]) : undefined,
                size: r.chance(0.7) ? octal(size) : sizeField(r, size),
                type: r.pick(['0', '0', '0', '\0', '5', '2', 'L', 'x', String.fromCharCode(0xb0), String.fromCharCode(0x80), 'g']),
            };
            blocks.push(header(fields));
            let body = data;
            if (r.chance(0.1)) body = body.slice(0, r.int(body.length + 1));
            blocks.push(pad(body));
            if (r.chance(0.05)) blocks.push(Buffer.alloc(512));
        }
        if (r.chance(0.7)) blocks.push(Buffer.alloc(1024));
        let tar = Buffer.concat(blocks);
        if (r.chance(0.1)) tar = tar.slice(0, r.int(tar.length + 1));
        const gz = zlib.gzipSync(tar, { level: r.pick([1, 6, 9]) });
        let files;
        try {
            const map = archive.extractTarGz(gz);
            files = { files: [...map.entries()].map(([k, v]) => [k, b64(v)]) };
        } catch (err) {
            files = { error: errOf(err) };
        }
        out.push({ input: b64(gz), ...files });
    }
    return out;
}

function gunzipVectors() {
    const r = rng(3);
    const out = [];
    const record = (input) => {
        try {
            out.push({ input: b64(input), output: b64(zlib.gunzipSync(input)) });
        } catch (err) {
            out.push({ input: b64(input), error: errOf(err) });
        }
    };
    const sample = () => {
        const kind = r.int(4);
        const n = kind === 0 ? r.pick([0, 1, 10, 300]) : r.pick([0, 1, 10, 300, 2000, 6000]);
        const b = Buffer.alloc(n);
        for (let i = 0; i < n; i++) {
            b[i] = kind === 0 ? r.int(256) : kind === 1 ? 97 + r.int(3) : kind === 2 ? (i % 7) * 13 : r.pick([32, 101, 116, 97]);
        }
        return b;
    };
    for (let i = 0; i < 4000; i++) {
        const data = sample();
        const opts = { level: r.pick([0, 1, 5, 9]), strategy: r.pick([zlib.constants.Z_DEFAULT_STRATEGY, zlib.constants.Z_FIXED, zlib.constants.Z_HUFFMAN_ONLY, zlib.constants.Z_RLE]) };
        let gz = Buffer.from(zlib.gzipSync(data, opts));
        switch (r.int(10)) {
        case 0: break;
        case 1: case 2: case 3: {
            const flips = 1 + r.int(3);
            for (let k = 0; k < flips; k++) {
                const at = 10 + r.int(Math.max(1, gz.length - 10));
                if (at < gz.length) gz[at] ^= 1 << r.int(8);
            }
            break;
        }
        case 4: gz = gz.slice(0, r.int(gz.length + 1)); break;
        case 5: gz = Buffer.concat([gz, r.pick([Buffer.from([0]), Buffer.from([0, 0, 7, 9]), Buffer.from('garbage'), Buffer.from('g'), Buffer.from([0x1f]), Buffer.from([0x1f, 0x8b]), zlib.gzipSync(sample())])]); break;
        case 6: {
            const at = 10 + r.int(Math.max(1, gz.length - 10));
            if (at < gz.length) gz[at] = r.int(256);
            break;
        }
        case 7: {
            // header variants: flags with extra, name, comment, header crc
            const flags = r.pick([0x04, 0x08, 0x10, 0x02, 0x1e, 0x20, 0x40, 0x80]);
            const head = Buffer.from(gz.slice(0, 10));
            head[3] = flags;
            const parts = [head];
            if (flags & 0x04) parts.push(Buffer.from([3, 0, 1, 2, 3]));
            if (flags & 0x08) parts.push(Buffer.from('name\0'));
            if (flags & 0x10) parts.push(Buffer.from('comment\0'));
            if (flags & 0x02) {
                let crc = zlib.crc32 ? zlib.crc32(Buffer.concat(parts)) : 0;
                if (r.chance(0.3)) crc ^= 1;
                parts.push(Buffer.from([crc & 0xff, (crc >>> 8) & 0xff]));
            }
            parts.push(gz.slice(10));
            gz = Buffer.concat(parts);
            break;
        }
        case 8: gz[2] = r.pick([7, 8, 9, 0]); break;
        default: {
            // a raw deflate body of random bits behind a valid header
            const body = Buffer.alloc(1 + r.int(40));
            for (let k = 0; k < body.length; k++) body[k] = r.int(256);
            gz = Buffer.concat([gz.slice(0, 10), body, Buffer.alloc(r.int(9))]);
        }
        }
        record(gz);
    }
    return out;
}

// ---- manifests and snapshots ----

function validateVectors() {
    const full = [];
    const manifests = ['', 'null', '{}', '[]', '5', '"x"', 'not json', '{"_format":"betterdesk-full-backup","_version":1}',
        '{"_format":"betterdesk-full-backup","_version":2}', '{"_format":"betterdesk-full-backup","_version":"1"}',
        '{"_format":"betterdesk-full-backup"}', '{"_format":"other","_version":0.5}', '{"_format":"betterdesk-full-backup","_version":{"toString":1}}',
        '{"_format":"betterdesk-full-backup","_version":[1,[2,null]]}', '{"_format":"betterdesk-full-backup","_version":{}}',
        '\ufeff{}', '{"a":1', '[1,2', '{"_version":-1,"_format":"betterdesk-full-backup"}', '{"_format":"betterdesk-full-backup","_version":1e400}',
        Buffer.from([0x7b, 0xff, 0x7d]).toString('latin1'), '{"_format":"betterdesk-full-backup","_version":1,"components":[]}', ' 1 ', 'tru', '{"a":}'];
    for (const m of manifests) {
        const entries = [{ name: 'manifest.json', data: Buffer.from(m, 'latin1') }];
        const gz = archive.createTarGz(entries);
        try {
            const v = backupService.validateFullBackup(gz);
            full.push({ manifest: b64(Buffer.from(m, 'latin1')), valid: v.valid, errors: v.errors });
        } catch (err) {
            full.push({ manifest: b64(Buffer.from(m, 'latin1')), error: errOf(err) });
        }
    }
    const noManifest = backupService.validateFullBackup(archive.createTarGz([{ name: 'other', data: 'x' }]));
    full.push({ noManifest: true, valid: noManifest.valid, errors: noManifest.errors });
    const notGzip = backupService.validateFullBackup(Buffer.from([0x1f, 0x8b, 1, 2]));
    full.push({ broken: b64(Buffer.from([0x1f, 0x8b, 1, 2])), valid: notGzip.valid, errors: notGzip.errors });

    const snapshots = [];
    const values = [null, 5, 'x', [], {}, { _format: 'betterdesk-backup', _version: 1, console: {} },
        { _format: 'betterdesk-backup', _version: 1, console: [] }, { _format: 'betterdesk-backup', _version: 1, console: null },
        { _format: 'betterdesk-backup', _version: 2, console: {} }, { _format: 'betterdesk-backup', _version: '1', console: {} },
        { _format: 'x', console: 'c' }, { _format: 'betterdesk-backup', _version: { toString: 2 }, console: {} },
        { _format: 'betterdesk-backup', _version: [1, 2], console: {} }, { _version: 0 }, true, false, 0, ''];
    for (const v of values) {
        try {
            snapshots.push({ data: v, result: backupService.validateBackup(JSON.parse(JSON.stringify(v))) });
        } catch (err) {
            snapshots.push({ data: v, error: errOf(err) });
        }
    }
    return { full, snapshots };
}

function numberVectors() {
    const r = rng(4);
    const fixed = [];
    const sizes = [0, 1, 51, 52, 102, 103, 512, 1280, 1331, 1332, 2560, 5120, 10240, 1048576, 1572864, 5242, 5243, 10485, 10486, 1048575, 123456789];
    for (let i = 0; i < 3000; i++) sizes.push(r.int(i < 1500 ? 200000 : 2000000000));
    for (const n of sizes) fixed.push([n, (n / 1024).toFixed(1), (n / 1024 / 1024).toFixed(2)]);
    const base64 = [];
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/-_= \n\t*.#\u00e9';
    for (let i = 0; i < 2000; i++) {
        let s = '';
        const n = r.int(30);
        for (let k = 0; k < n; k++) s += r.pick([...chars]);
        base64.push([s, Buffer.from(s, 'base64').toString('hex')]);
    }
    for (let i = 0; i < 300; i++) {
        const b = Buffer.alloc(r.int(50));
        for (let k = 0; k < b.length; k++) b[k] = r.int(256);
        base64.push([b.toString('base64'), b.toString('hex')]);
    }
    return { fixed, base64 };
}

// ---- schedule and SMB ----

async function scheduleVectors() {
    const r = rng(5);
    const configKey = 'scheduled_backup_config';
    const smbKey = 'scheduled_backup_smb';
    const out = { dataDir: process.env.DATA_DIR, appDir: WEB, save: [], get: [], destination: [], smbSave: [], smbGet: [], smbTest: [] };
    const keys = ['enabled', 'intervalHours', 'retentionCount', 'destinationType', 'destinationPath'];
    const paths = ['/backups', '/', '//', '/./', '/srv/../', 'relative/dir', '', ' ', ' /trim/me ', '/repo/web-nodejs', '/repo/web-nodejs/', '/repo/web-nodejs/sub',
        '/repo/web-nodejsX', '/repo', '/a/b/../c', '/a//b/', 'C:\\backups', '\\\\server\\share', '/\u00e9t\u00e9', 5, null, ['/x'], {}];
    const randomBody = () => {
        const body = {};
        for (const k of keys) {
            if (!r.chance(0.6)) continue;
            if (k === 'destinationType') body[k] = r.pick(['path', 'local', 'other', null, 5, 'path']);
            else if (k === 'destinationPath') body[k] = r.pick(paths);
            else if (k === 'enabled') body[k] = r.pick([true, false, 'true', 'false', 1, 0, null, 'yes']);
            else body[k] = r.pick(ODD);
        }
        return body;
    };
    for (let i = 0; i < 1500; i++) {
        const body = i < 40 ? { destinationType: 'path', destinationPath: paths[i % paths.length] } : randomBody();
        settings.clear();
        try {
            const result = await scheduled.saveConfig(JSON.parse(JSON.stringify(body)));
            out.save.push({ body, result, stored: settings.get(configKey) ?? null });
        } catch (err) {
            out.save.push({ body, error: errOf(err), stored: settings.get(configKey) ?? null });
        }
        try {
            out.destination.push({ body, result: scheduled.resolveDestination(JSON.parse(JSON.stringify(body))) });
        } catch (err) {
            out.destination.push({ body, error: errOf(err) });
        }
    }
    const storedTexts = [null, '', 'null', '5', '"x"', '[]', '[1]', '{}', 'not json', '{"enabled":true,"intervalHours":"3","retentionCount":0}',
        '{"intervalHours":1e9,"retentionCount":-3,"destinationType":"path","destinationPath":" /x "}', '{"toString":"x","intervalHours":{"toString":1}}'];
    for (let i = 0; i < 300; i++) storedTexts.push(JSON.stringify(randomBody()));
    for (const text of storedTexts) {
        settings.clear();
        if (text !== null) settings.set(configKey, text);
        try {
            out.get.push({ stored: text, result: await scheduled.getConfig() });
        } catch (err) {
            out.get.push({ stored: text, error: errOf(err) });
        }
    }

    const smbKeys = ['host', 'share', 'domain', 'username', 'password', 'mountPath'];
    const hosts = ['nas', 'nas.example', 'nas-1_x.lan', ' nas ', '', 'bad host', 'n\u00e4s', 'a;b', '192.168.1.5', 5, null, ['nas'], {}];
    const shares = ['backups', 'c$', 'Backups.2026', 'bad/share', '', ' share ', 'x'.repeat(300), 0, true];
    const randomSmb = () => {
        const body = {};
        for (const k of smbKeys) {
            if (!r.chance(0.75)) continue;
            if (k === 'host') body[k] = r.pick(hosts);
            else if (k === 'share') body[k] = r.pick(shares);
            else if (k === 'password') body[k] = r.pick(['secret', '********', '', ' spaced ', 'p'.repeat(600), '\u{1F600}'.repeat(300), 0, null, ['a', 'b'], { toString: 1 }]);
            else body[k] = r.pick(ODD);
        }
        return body;
    };
    const storedSmb = [null, '', '{}', '[]', 'x', '{"host":"nas","share":"s","password":"old"}', '{"password":""}', '{"password":5}',
        '{"host":"nas","share":"s","username":"u","password":"stored"}'];
    for (let i = 0; i < 1500; i++) {
        const body = randomSmb();
        const stored = r.pick(storedSmb);
        settings.clear();
        if (stored !== null) settings.set(smbKey, stored);
        try {
            const result = await scheduled.saveSmbSettings(JSON.parse(JSON.stringify(body)));
            out.smbSave.push({ body, before: stored, result, stored: settings.get(smbKey) ?? null });
        } catch (err) {
            out.smbSave.push({ body, before: stored, error: errOf(err), stored: settings.get(smbKey) ?? null });
        }
    }
    for (const stored of [...storedSmb, '{"b":1,"password":"x","a":2}', '{"2":1,"password":"x","1":2}', '[1,2]']) {
        settings.clear();
        if (stored !== null) settings.set(smbKey, stored);
        out.smbGet.push({ stored, result: await scheduled.getSmbSettings() });
    }
    const plans = [{ code: 0 }, { code: 1 }, { code: 255 }, { errorCode: 'ENOENT' }, { errorCode: 'EACCES' }];
    for (const platform of ['linux', 'win32']) {
        Object.defineProperty(process, 'platform', { value: platform });
        for (let i = 0; i < 400; i++) {
            const body = randomSmb();
            spawnPlan = r.pick(plans);
            spawnCalls.length = 0;
            try {
                const result = await scheduled.testSmbConnection(JSON.parse(JSON.stringify(body)));
                out.smbTest.push({ platform, body, plan: spawnPlan, calls: [...spawnCalls], result });
            } catch (err) {
                out.smbTest.push({ platform, body, plan: spawnPlan, calls: [...spawnCalls], error: errOf(err) });
            }
        }
    }
    Object.defineProperty(process, 'platform', { value: 'linux' });
    return out;
}

function write(file, data) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, zlib.gzipSync(JSON.stringify(data), { level: 9 }));
}

(async () => {
    const archiveData = { tar: tarVectors(), extract: extractVectors(), gunzip: gunzipVectors() };
    const values = { validate: validateVectors(), numbers: numberVectors() };
    const schedule = await scheduleVectors();
    write(path.join(OUT, 'archive.json.gz'), archiveData);
    write(path.join(OUT, 'values.json.gz'), values);
    write(path.join(SCHEDULE_OUT, 'schedule.json.gz'), schedule);
    const counts = {
        tar: archiveData.tar.length, extract: archiveData.extract.length, gunzip: archiveData.gunzip.length,
        validateFull: values.validate.full.length, validateSnapshot: values.validate.snapshots.length,
        toFixed: values.numbers.fixed.length, base64: values.numbers.base64.length,
        scheduleSave: schedule.save.length, scheduleGet: schedule.get.length, destination: schedule.destination.length,
        smbSave: schedule.smbSave.length, smbGet: schedule.smbGet.length, smbTest: schedule.smbTest.length,
    };
    fs.writeFileSync(path.join(OUT, 'counts.json'), JSON.stringify(counts, null, 2) + '\n');
    console.log(JSON.stringify(counts));
    fs.rmSync(tmp, { recursive: true, force: true });
})().catch((err) => { console.error(err); process.exit(1); });
