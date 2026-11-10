'use strict';

// Runs services/fileTransferService.js of the Node console through
// scripted lives of transfers and writes ../vectors.json.gz: for every
// step what the function returned or threw, every record afterwards, and
// the files in the transfers folder.
//
// The service is loaded from a copy in a scratch folder, because it keeps
// its files in the folder "data/transfers" next to its own code. The
// clock and the random bytes are counters, so the Go test can give its
// service the same and gets the same ids and times.

const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');
const crypto = require('crypto');

const WEB = process.env.ORACLE_WEB || '/repo/web-nodejs';
const SCRATCH = fs.mkdtempSync(path.join(os.tmpdir(), 'filetransfer-oracle-'));
fs.mkdirSync(path.join(SCRATCH, 'services'));
fs.mkdirSync(path.join(SCRATCH, 'lib'));
fs.copyFileSync(path.join(WEB, 'services/fileTransferService.js'), path.join(SCRATCH, 'services/fileTransferService.js'));
fs.copyFileSync(path.join(WEB, 'lib/safePath.js'), path.join(SCRATCH, 'lib/safePath.js'));
const DIR = path.join(SCRATCH, 'data', 'transfers');

// mulberry32: the same numbers on every run.
function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    return { next, int: (n) => Math.floor(next() * n), pick: (list) => list[Math.floor(next() * list.length)], chance: (p) => next() < p };
}

// Random bytes: x = x * 1103515245 + 12345 (mod 2^32) per byte, of which
// bits 16 to 23 are the byte.
let randomState = 20261110;
crypto.randomBytes = (n) => {
    const buf = Buffer.alloc(n);
    for (let i = 0; i < n; i++) {
        randomState = (Math.imul(randomState, 1103515245) + 12345) >>> 0;
        buf[i] = (randomState >>> 16) & 0xff;
    }
    return buf;
};

// The clock: every reading is one millisecond after the one before, and a
// step can move it on.
let clock = Date.UTC(2026, 0, 1);
Date.now = () => clock++;

// The service starts its housekeeping with setInterval when it loads; the
// steps run it themselves.
let sweep = null;
const quiet = { log: console.log, warn: console.warn };
console.log = () => {};
console.warn = () => {};
let service;
function boot() {
    const realSetInterval = global.setInterval;
    global.setInterval = (fn, ms) => {
        if (ms !== 60000) throw new Error(`unexpected interval ${ms}`);
        sweep = fn;
        return null;
    };
    try {
        const file = path.join(SCRATCH, 'services/fileTransferService.js');
        delete require.cache[file];
        service = require(file);
    } finally {
        global.setInterval = realSetInterval;
    }
}
boot();

function mask(text) {
    return text.split(DIR).join('<DIR>');
}

function dirState() {
    let names;
    try {
        names = fs.readdirSync(DIR).sort();
    } catch (err) {
        return null;
    }
    return names.map((name) => {
        const full = path.join(DIR, name);
        const stat = fs.statSync(full);
        if (stat.isDirectory()) return [name, 'dir'];
        return [name, crypto.createHash('sha1').update(fs.readFileSync(full)).digest('hex')];
    });
}

// fresh is a value as a request would bring it: no object or list is the
// one an earlier step used.
function fresh(value) {
    return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function step(given) {
    const record = { ...given };
    const spec = fresh(given);
    try {
        let out;
        switch (spec.op) {
        case 'boot': boot(); break;
        case 'create': out = service.createTransfer(spec.params); break;
        case 'get': out = service.getTransfer(spec.id); break;
        case 'list': out = service.listTransfers(spec.filter); break;
        case 'chunk': out = service.receiveChunk(spec.id, spec.index, spec.data); break;
        case 'cancel': out = service.cancelTransfer(spec.id); break;
        case 'progress': out = service.getProgress(spec.id); break;
        case 'path': out = service.getCompletedFilePath(spec.id); break;
        case 'message': out = service.handleWsMessage(spec.device, spec.message); break;
        case 'sweep': sweep(); break;
        case 'advance': clock += spec.ms; break;
        case 'rmfile': fs.rmSync(path.join(DIR, spec.name), { force: true }); break;
        case 'mkdir-in-place': fs.mkdirSync(path.join(DIR, spec.name)); break;
        default: throw new Error(`unknown op ${spec.op}`);
        }
        if (out !== undefined) record.out = mask(JSON.stringify(out));
    } catch (err) {
        record.err = mask(String(err.message));
    }
    record.all = mask(JSON.stringify(service.listTransfers()));
    record.dir = dirState();
    return record;
}

const MAX = 4294967296;
const sizes = [0, 1, 5, 10, 100, 262144, 262145, 600000, 1048576, MAX, MAX + 1, -5, 0.5, 2.5, 1e21, '7', '12abc', 'abc', '', ' 20 ', '0x10', '1e3', '4294967297', '99999999999 bytes',
    true, false, null, [], [9], [1, 2], {}, { a: 1 }, { toString: 1 }, { valueOf: 1 }, '\u00a010\u2003', '+5', '-0', '.5', '5.', 'Infinity', '-Infinity', '0b11', '0o17', '1_000', '0x', '١٢'];
const filenames = ['report.pdf', '../../etc/passwd', 'a/b/c.txt', 'a/b/', '/', '', '..', '.', '.hidden', 'my file (1).txt', 'ünïcödé.txt', '日本語.txt', 'emoji\u{1F600}.png', 'a\\b\\c.txt', 'C:\\Users\\x\\f.txt',
    'tab\there', 'nul\u0000l', 'semi;colon&amp', 'x'.repeat(300), 'a//', '//', 'dir/.', 'trailing.', '-dash', '_', 5, 0, true, false, null, ['a', 'b c'], [], [[1, 2], 3], {}, { toString: 1 }, { name: 'x' }, 12.5, -3, 1e21];
const devices = ['900100001', '900100002', 'dev-A', '', 5, null, { id: 1 }, ['x'], { toString: 1 }];
const directions = ['upload', 'download', '', null, 'sideways', 5, ['upload'], { toString: 'x' }];
const mimes = ['text/plain', 'application/pdf', '', null, 0, 7, { x: 1 }, ['a']];
const creators = ['e2e_admin', 'admin', '', null, 0, 'agent:900100001'];
const b64 = (n, fill) => Buffer.alloc(n, fill).toString('base64');
const datas = [b64(10, 1), b64(1, 255), b64(2, 7), b64(3, 9), b64(100, 65), b64(1000, 66), '', 'QUJD', 'QUJDRA==', 'QUJDRA', 'QUJDRA=', 'QUJD RA==', 'QUJD\nRA==', 'QU-_', 'QU+/', 'Q', 'QQ', 'QQ=', 'QQ==QUJD',
    '=QUJD', 'QU=JD', '!!!!', 'QUJD!!RA', 'ÄÖÜ', '日本', 'QUJD\u0000RA', ' ', '====', 'abc', 'a', 'ab', 'abcde', 'not base64 at all, really', '\uD83D\uDE00QUJD',
    [1, 2, 3], [], [300, -1, 1.7, '7', 'x', null, true, [5], {}], { type: 'Buffer', data: [65, 66, 67] }, { type: 'Buffer', data: 'QUJD' }, { type: 'buffer', data: [1] }, { length: 3, 0: 65, 2: 67 },
    { length: 0 }, { length: -1 }, { length: '3' }, { length: 2.5, 0: 1, 1: 2, 2: 3 }, { length: null }, {}, { a: 1 }, { valueOf: 1 }, { data: [1, 2] }, 5, 0, true, false, null];
const indexes = [0, 1, 2, 5, '0', '1', null, false, true, -0, 0.5, [], {}, 'x'];

function main() {
    const r = rng(20261110);
    const scenarios = [];
    let total = 0;
    const known = [];

    function scenario(name, build) {
        const steps = [];
        const add = (spec) => {
            const record = step(spec);
            steps.push(record);
            total++;
            if (spec.op === 'create' && record.out) known.push(JSON.parse(record.out).id);
            if (spec.op === 'message' && record.out) {
                const out = JSON.parse(record.out);
                if (out.transfer_id && out.action === 'init_ack') known.push(out.transfer_id);
            }
            return record;
        };
        build(add);
        scenarios.push({ name, steps });
    }
    const idOf = (record) => JSON.parse(record.out).id;
    const someId = () => (known.length && r.chance(0.9) ? known[known.length - 1 - r.int(Math.min(known.length, 6))] : r.pick(['nope', '', 5, null, undefined, ['x'], { a: 1 }]));
    const params = (over) => ({ direction: 'download', deviceId: '900100001', filename: 'file.bin', size: 10, mimeType: 'application/octet-stream', createdBy: 'e2e_admin', ...over });

    // What a new transfer makes of its values
    scenario('sizes', (add) => { for (const size of sizes) add({ op: 'create', params: params({ size }) }); });
    scenario('filenames', (add) => { for (const filename of filenames) add({ op: 'create', params: params({ filename }) }); });
    scenario('devices', (add) => { for (const deviceId of devices) add({ op: 'create', params: params({ deviceId }) }); });
    scenario('directions', (add) => { for (const direction of directions) add({ op: 'create', params: params({ direction }) }); });
    scenario('mime types', (add) => { for (const mimeType of mimes) add({ op: 'create', params: params({ mimeType }) }); });
    scenario('creators', (add) => { for (const createdBy of creators) add({ op: 'create', params: params({ createdBy }) }); });
    scenario('nothing given', (add) => {
        add({ op: 'create', params: {} });
        add({ op: 'create', params: { size: 1 } });
        add({ op: 'create', params: { filename: 'only-a-name' } });
    });

    // One transfer from the first chunk to the download and beyond
    scenario('a whole transfer', (add) => {
        const id = idOf(add({ op: 'create', params: params({ size: 25, filename: 'whole.bin' }) }));
        add({ op: 'path', id });
        add({ op: 'progress', id });
        add({ op: 'chunk', id, index: 0, data: b64(10, 1) });
        add({ op: 'path', id });
        add({ op: 'list', filter: { status: 'active' } });
        add({ op: 'chunk', id, index: 1, data: b64(10, 2) });
        add({ op: 'chunk', id, index: 2, data: b64(5, 3) });
        add({ op: 'path', id });
        add({ op: 'progress', id });
        add({ op: 'get', id });
        add({ op: 'chunk', id, index: 3, data: b64(4, 4) });
        add({ op: 'chunk', id, index: 0, data: b64(2, 5) });
        add({ op: 'path', id });
        add({ op: 'rmfile', name: `${id}_whole.bin` });
        add({ op: 'path', id });
        add({ op: 'chunk', id, index: 7, data: b64(30, 6) });
        add({ op: 'cancel', id });
        add({ op: 'path', id });
        add({ op: 'chunk', id, index: 0, data: b64(1, 1) });
        add({ op: 'cancel', id });
        add({ op: 'progress', id });
    });

    scenario('every kind of chunk', (add) => {
        const id = idOf(add({ op: 'create', params: params({ size: 1e9, filename: 'chunks.bin' }) }));
        for (const data of datas) add({ op: 'chunk', id, index: 1, data });
        for (const index of indexes) add({ op: 'chunk', id, index, data: 'QUJD' });
        add({ op: 'chunk', id, index: 1 });
        add({ op: 'chunk', id: 'nope', index: 0, data: 'QUJD' });
        add({ op: 'chunk', id: 5, index: 0, data: 'QUJD' });
        add({ op: 'chunk', index: 0, data: 'QUJD' });
    });

    scenario('sizes that are no numbers', (add) => {
        for (const size of ['abc', '30', [7], true, { a: 1 }, -5, 0.5]) {
            const created = add({ op: 'message', device: 'dev-A', message: { action: 'init', filename: 'odd.bin', size } });
            const id = JSON.parse(created.out).transfer_id;
            add({ op: 'progress', id });
            add({ op: 'chunk', id, index: 0, data: b64(3, 1) });
            add({ op: 'progress', id });
            add({ op: 'chunk', id, index: 1, data: b64(40, 2) });
            add({ op: 'get', id });
        }
    });

    scenario('a file that cannot be written', (add) => {
        const id = idOf(add({ op: 'create', params: params({ size: 100, filename: 'blocked.bin' }) }));
        add({ op: 'mkdir-in-place', name: `${id}_blocked.bin` });
        add({ op: 'chunk', id, index: 0, data: b64(3, 1) });
        add({ op: 'chunk', id, index: 1, data: b64(3, 1) });
        add({ op: 'get', id });
        add({ op: 'path', id });
        add({ op: 'cancel', id });
    });

    // The messages of a device
    scenario('messages', (add) => {
        const messages = [
            { action: 'init' }, { action: 'init', filename: 'from-agent.log', size: 6, mime_type: 'text/plain' }, { action: 'init', filename: '', size: '', mime_type: '' },
            { action: 'init', filename: { toString: 1 } }, { action: 'init', size: MAX + 1 }, { action: 'init', size: { toString: 1 } }, { action: 'init', filename: ['a', 'b'], size: '12', mime_type: 5 },
            { action: 'browse' }, { action: 'browse', path: 'C:\\Users' }, { action: 'browse', path: null }, { action: 'browse', path: { deep: [1] } },
            { action: 'cancel' }, { action: 'cancel', transfer_id: 'nope' }, { action: 'cancel', transfer_id: null }, { action: 'cancel', transfer_id: 7 }, { action: 'cancel', transfer_id: ['x'] },
            { action: 'chunk' }, { action: 'chunk', transfer_id: 'nope', data: 'QUJD' },
            {}, { action: null }, { action: 5 }, { action: 'INIT' }, { action: ['init'] }, { action: { toString: 1 } }, { action: {} }, { action: true }, { action: '' }, { action: 'delete' },
            [], ['init'], 'init', 5, true, null,
        ];
        for (const message of messages) add({ op: 'message', device: 'dev-A', message });
        const created = add({ op: 'message', device: 'dev-B', message: { action: 'init', filename: 'talk.bin', size: 12 } });
        const id = JSON.parse(created.out).transfer_id;
        add({ op: 'message', device: 'dev-B', message: { action: 'chunk', transfer_id: id, data: b64(5, 1) } });
        add({ op: 'message', device: 'dev-B', message: { action: 'chunk', transfer_id: id, chunk_index: 1, data: [1, 2, 3] } });
        add({ op: 'message', device: 'dev-C', message: { action: 'chunk', transfer_id: id, chunk_index: '0', data: b64(4, 9) } });
        add({ op: 'message', device: 'dev-B', message: { action: 'chunk', transfer_id: id, chunk_index: 2, data: 5 } });
        add({ op: 'message', device: 'dev-B', message: { action: 'chunk', transfer_id: id, chunk_index: 2 } });
        add({ op: 'path', id });
        add({ op: 'message', device: 'dev-C', message: { action: 'cancel', transfer_id: id } });
        add({ op: 'message', device: 'dev-B', message: { action: 'chunk', transfer_id: id, chunk_index: 3, data: 'QQ==' } });
        add({ op: 'get', id });
    });

    // The lists
    scenario('lists', (add) => {
        add({ op: 'create', params: params({ deviceId: 'list-A', direction: 'upload' }) });
        const second = idOf(add({ op: 'create', params: params({ deviceId: 'list-A' }) }));
        add({ op: 'advance', ms: -500 });
        add({ op: 'create', params: params({ deviceId: 'list-B' }) });
        add({ op: 'advance', ms: 500 });
        add({ op: 'create', params: params({ deviceId: 5 }) });
        add({ op: 'cancel', id: second });
        const filters = [undefined, {}, { deviceId: 'list-A' }, { deviceId: 'list-A', direction: 'upload' }, { deviceId: 'list-A', status: 'cancelled' }, { status: 'pending' }, { status: '' }, { status: 'PENDING' },
            { deviceId: '5' }, { deviceId: 5 }, { deviceId: ['list-A'] }, { deviceId: { x: 1 } }, { status: ['pending', 'cancelled'] }, { direction: 'download' }, { direction: '' }, { direction: ['upload'] },
            { deviceId: '', status: '', direction: '' }, { deviceId: 'nobody' }];
        for (const filter of filters) add({ op: 'list', filter });
    });

    // The housekeeping
    scenario('housekeeping', (add) => {
        add({ op: 'boot' });
        const pending = idOf(add({ op: 'create', params: params({ filename: 'pending.bin' }) }));
        const active = idOf(add({ op: 'create', params: params({ size: 100, filename: 'active.bin' }) }));
        const done = idOf(add({ op: 'create', params: params({ size: 3, filename: 'done.bin' }) }));
        const cancelled = idOf(add({ op: 'create', params: params({ filename: 'cancelled.bin' }) }));
        add({ op: 'chunk', id: active, index: 0, data: b64(3, 1) });
        add({ op: 'chunk', id: done, index: 0, data: b64(3, 1) });
        add({ op: 'cancel', id: cancelled });
        add({ op: 'sweep' });
        add({ op: 'advance', ms: 30 * 60 * 1000 - 20 });
        add({ op: 'sweep' });
        add({ op: 'advance', ms: 40 });
        add({ op: 'sweep' });
        add({ op: 'get', id: active });
        add({ op: 'path', id: done });
        add({ op: 'advance', ms: 30 * 60 * 1000 - 100 });
        add({ op: 'sweep' });
        add({ op: 'advance', ms: 200 });
        add({ op: 'sweep' });
        add({ op: 'get', id: done });
        add({ op: 'get', id: cancelled });
        add({ op: 'get', id: active });
        add({ op: 'get', id: pending });
        add({ op: 'advance', ms: 5 * 3600 * 1000 });
        add({ op: 'sweep' });
        add({ op: 'list', filter: {} });
        const late = idOf(add({ op: 'create', params: params({ size: 50, filename: 'late.bin' }) }));
        add({ op: 'chunk', id: late, index: 0, data: b64(3, 1) });
        add({ op: 'advance', ms: 2 * 3600 * 1000 });
        add({ op: 'sweep' });
        add({ op: 'get', id: late });
        add({ op: 'boot' });
        add({ op: 'get', id: pending });
    });

    // Random lives
    for (let i = 0; i < 260; i++) {
        scenario(`random ${i}`, (add) => {
            if (r.chance(0.15)) add({ op: 'boot' });
            const n = 8 + r.int(22);
            for (let j = 0; j < n; j++) {
                const op = r.pick(['create', 'create', 'message-init', 'chunk', 'chunk', 'chunk', 'message-chunk', 'message-chunk', 'cancel', 'message-cancel', 'progress', 'get', 'path', 'list', 'sweep', 'advance']);
                if (op === 'create') {
                    add({ op, params: params({ size: r.chance(0.7) ? r.pick([0, 3, 10, 25, 100]) : r.pick(sizes), filename: r.chance(0.6) ? `f${j}.bin` : r.pick(filenames), deviceId: r.pick(devices), direction: r.pick(directions), mimeType: r.pick(mimes), createdBy: r.pick(creators) }) });
                } else if (op === 'message-init') {
                    const message = { action: 'init' };
                    if (r.chance(0.8)) message.filename = r.chance(0.6) ? `m${j}.bin` : r.pick(filenames);
                    if (r.chance(0.8)) message.size = r.chance(0.7) ? r.pick([0, 3, 10, 25, 100]) : r.pick(sizes);
                    if (r.chance(0.5)) message.mime_type = r.pick(mimes);
                    add({ op: 'message', device: r.pick(['dev-A', 'dev-B', '']), message });
                } else if (op === 'chunk') {
                    add({ op, id: someId(), index: r.chance(0.5) ? r.int(4) : r.pick(indexes), data: r.chance(0.75) ? b64(1 + r.int(12), r.int(256)) : r.pick(datas) });
                } else if (op === 'message-chunk') {
                    const message = { action: 'chunk', transfer_id: someId() };
                    if (r.chance(0.8)) message.chunk_index = r.chance(0.5) ? r.int(4) : r.pick(indexes);
                    if (r.chance(0.95)) message.data = r.chance(0.75) ? b64(1 + r.int(12), r.int(256)) : r.pick(datas);
                    add({ op: 'message', device: r.pick(['dev-A', 'dev-B']), message });
                } else if (op === 'cancel') {
                    add({ op, id: someId() });
                } else if (op === 'message-cancel') {
                    add({ op: 'message', device: 'dev-A', message: { action: 'cancel', transfer_id: someId() } });
                } else if (op === 'progress' || op === 'get' || op === 'path') {
                    const id = someId();
                    add({ op, id: typeof id === 'string' ? id : 'nope' });
                } else if (op === 'list') {
                    const filter = {};
                    if (r.chance(0.5)) filter.deviceId = r.pick(devices);
                    if (r.chance(0.5)) filter.status = r.pick(['pending', 'active', 'completed', 'failed', 'cancelled', '', ['pending']]);
                    if (r.chance(0.3)) filter.direction = r.pick(directions);
                    add({ op, filter });
                } else if (op === 'sweep') {
                    add({ op });
                } else {
                    add({ op: 'advance', ms: r.pick([1, 1000, 60000, 29 * 60000, 31 * 60000, 61 * 60000, -1000]) });
                }
            }
        });
    }

    // parseInt(size, 10) || 0, as the route reads the size of a request
    const parsed = [];
    const texts = ['', ' ', '0', '-0', '+7', '-7', '42', '42.9', '.5', '1e3', '12abc', 'abc12', '0x10', '0X1f', '-0x10', ' 0x10', '010', '  \t 99  ', '\u00a05', '9007199254740993', '1' + '0'.repeat(30),
        '4294967297', '4294967297 bytes', 'Infinity', 'NaN', 'null', 'true', '١٢', '1_000', '--5', '+-5', '5-', '\uFEFF8'];
    for (const value of [...sizes, ...texts, 9007199254740993, 1e300, -1e21, 1.9, -1.9, 0.9, -0.9, 1e-7, [5, 6], ['8'], [[9]], [null], [undefined], 'x']) {
        let out;
        try {
            out = { out: JSON.stringify(parseInt(value, 10) || 0) };
        } catch (err) {
            out = { err: String(err.message) };
        }
        parsed.push({ value: value === undefined ? null : value, ...out });
    }

    const json = JSON.stringify({ node: process.version, scenarios, parsed });
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json.gz'), zlib.gzipSync(json, { level: 9 }));
    quiet.log(`${scenarios.length} scenarios, ${total} steps, ${parsed.length} sizes, ${json.length} bytes of JSON on ${process.version}`);
    fs.rmSync(SCRATCH, { recursive: true, force: true });
}

main();
