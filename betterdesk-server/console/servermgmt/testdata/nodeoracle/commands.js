'use strict';

/**
 * Records what the old console's services/serverManagement.js made of the
 * commands it ran, with child_process replaced by a stand-in that answers
 * from the vectors and notes every call: the disks from df and wmic, the
 * swap from /proc/meminfo, the service list from systemctl and PowerShell,
 * and the service control (the checks, the command line, the result, the
 * time limit and the signal). Also the server part of
 * GET /api/server-management/info for API server health answers (the Go
 * duration parser of routes/server-management.routes.js). Nothing is run.
 * See README.md.
 *
 *   node commands.js        writes ../node_commands.json
 */

const fs = require('fs');
const path = require('path');
const http = require('http');
const { EventEmitter } = require('events');
const Module = require('module');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const OUT = path.join(__dirname, '..', 'node_commands.json');

// ---- child_process and fs stand-ins ----

let calls = [];
let syncAnswer = () => ({ status: 0, stdout: '' });
let asyncAnswer = null;
let meminfo = null;
const cp = require('child_process');
cp.spawnSync = (cmd, args, opts) => {
    calls.push({ sync: true, cmd, args, timeout: opts && opts.timeout });
    return syncAnswer(cmd, args);
};
cp.spawn = (cmd, args, opts) => {
    const child = new EventEmitter();
    child.stdout = new EventEmitter();
    child.stderr = new EventEmitter();
    const call = { cmd, args, stdio: opts && opts.stdio };
    child.kill = (signal) => { call.killed = signal === undefined ? null : signal; };
    calls.push(call);
    asyncAnswer(child, call);
    return child;
};
const realRead = fs.readFileSync;
fs.readFileSync = (file, ...rest) => {
    if (file === '/proc/meminfo') {
        if (meminfo === null) throw new Error('ENOENT');
        return meminfo;
    }
    return realRead(file, ...rest);
};
// The timer of the service control fires at once when a vector says so
let fireTimers = false;
const realSetTimeout = global.setTimeout;
global.setTimeout = (fn, delay, ...args) => {
    const t = { delay, fn, cleared: false };
    calls.push({ timer: delay });
    if (fireTimers) realSetTimeout(() => { if (!t.cleared) fn(...args); }, 0);
    return t;
};
global.clearTimeout = (t) => { if (t && typeof t === 'object') t.cleared = true; };

let broker = { can: false, restarts: [] };
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('lib/privilegedUpdateHelper.js', {
    canUsePrivilegedUpdate: () => broker.can,
    restartService: (service) => { broker.restarts.push(service); return { success: true, action: 'restart', service, version: 2 }; },
});

function loadFor(platform) {
    Object.defineProperty(process, 'platform', { value: platform, configurable: true });
    const file = require.resolve(path.join(WEB, 'services/serverManagement.js'));
    delete require.cache[file];
    return require(file);
}

const out = { disks: [], swap: [], services: [], control: [], health: [] };

// ---- disks and swap ----

const DF = [
    '',
    'Mounted on Type 1B-blocks Used Avail',
    'Mounted on Type 1B-blocks Used Avail\n/ ext4 100000000000 25000000000 75000000000\n/dev/shm tmpfs 8000000000 0 8000000000\n/small ext4 1000 10 990\n/data xfs 2000000000000 1900000000000 100000000000\n/broken ext4 x y z\nshort line',
    'h\n/ ext4 1048576 1 2\n/a ext4 1048575 1 2\n/b ext4 2097152 used avail\n/c ext4 -5 1 2\n/d ext4 0x100000 1 2\n/e ext4 1e7 1 2\n/f vfat 3000000abc 12 13 extra fields here\n  /g   btrfs   5000000   1   2  \n/h overlay 9999999 1 2\n/i squashfs 9999999 1 2\n/j cgroup2 9999999 1 2\n/k fuse.sshfs 99999999999999999999 1 2',
    'header\n/ ext4 4000000\n/x ext4 4000000 1 2 3',
    'header\r\n/ ext4 4000000 1 2\r\n/y ext4 5000000 3 4\r\n',
    'header\n/ x ext4 4000000 1 2\n/z\text4\t5000000\t3\t4',
];
const WMIC = [
    '',
    'Node,DeviceID,FileSystem,FreeSpace,Size',
    '\r\nNode,DeviceID,FileSystem,FreeSpace,Size\r\nHOST,C:,NTFS,100000,500000\r\nHOST,D:,,5,10\r\nHOST,E:,FAT32,x,y\r\nHOST,,NTFS,1,2\r\nHOST,F:,NTFS,7\r\nHOST,G:,ReFS,-0,1000\r\nHOST,H:,NTFS,12abc,1e3\r\n',
    'a,b,c,d,e,f\nx,Y:,NTFS,1,0',
];
const MEMINFO = [
    null,
    '',
    'MemTotal: 1000 kB\nSwapTotal:       2048 kB\nSwapFree:        1024 kB\n',
    'SwapTotal:       2048 kB\n',
    'SwapTotal:2048 kB\nSwapFree: 10 kB\n',
    'SwapFree: 10 kB\nSwapTotal:   0 kB\n',
    'SwapTotal: 99999999999999999999 kB\nSwapFree: x kB\n',
    'XSwapTotal: 5 kB\nSwapTotal: 7 kB\nSwapFree: 3 kB\nSwapFree: 1 kB\n',
];

(async () => {
    for (const platform of ['linux', 'win32']) {
        const sm = loadFor(platform);
        const outputs = platform === 'linux' ? DF : WMIC;
        for (const text of outputs) {
            for (const status of [0, 1]) {
                calls = [];
                syncAnswer = () => ({ status, stdout: text });
                const snap = sm.getResourceSnapshot();
                out.disks.push({ platform, stdout: text, status, disks: snap.disks, calls: calls.filter((c) => c.sync) });
            }
        }
        syncAnswer = () => ({ error: new Error('ENOENT') });
        calls = [];
        out.disks.push({ platform, failed: true, disks: sm.getResourceSnapshot().disks, calls: calls.filter((c) => c.sync) });
        if (platform === 'linux') {
            for (const text of MEMINFO) {
                meminfo = text;
                out.swap.push({ meminfo: text, swap: sm.getResourceSnapshot().swap });
            }
        }
    }

    // ---- the service list ----

    const SYSTEMCTL = [
        '',
        'betterdesk-server.service loaded active running BetterDesk server\nsshd.service loaded active running OpenSSH server\nhbbs.service not-found inactive dead hbbs.service\nbetterdesk-console.service loaded failed failed  BetterDesk   console  \nbetterdesk.socket loaded active listening sock\nhbbr.service loaded active\ndocker.service loaded active running Docker\nrustdesksignal.service loaded active exited\n',
        '  betterdesk-go.service\tloaded\tactive\trunning\tGo server\r\nbetterdesk-api.service loaded inactive dead',
        'BetterDeskServer.service loaded active running caps\nbetterdesk-server loaded active running no suffix',
    ];
    const GETSERVICE = [
        '',
        '"Name","Status","DisplayName"\r\n"BetterDeskServer","Running","BetterDesk Server"\r\n"Spooler","Running","Print Spooler"\r\n"BetterDeskConsole","Stopped","BetterDesk Console"',
        '"Name","Status","DisplayName"\n"BetterDeskServer","Running","BetterDesk Server"\n"BetterDeskRelay","StartPending","Relay, with comma"\n"BetterDeskSignal","stopped",""\n"hbbs","RUNNING","x"\nbroken line\n"BetterDeskConsole","Running","a"b"',
        '"BetterDeskServer","Running","only line, no header"',
    ];
    for (const [platform, outputs] of [['linux', SYSTEMCTL], ['win32', GETSERVICE], ['darwin', ['x']]]) {
        const sm = loadFor(platform);
        for (const text of outputs) {
            for (const status of [0, 3]) {
                calls = [];
                syncAnswer = () => ({ status, stdout: text });
                out.services.push({ platform, stdout: text, status, services: sm.listServices(), calls: calls.filter((c) => c.sync) });
            }
        }
    }

    // ---- the service control ----

    const NAMES = ['betterdesk-server', 'betterdesk-server.service', 'betterdesk-console', 'betterdesk', 'hbbs', 'hbbr.service', 'BetterDeskServer',
        'BetterDeskConsole', 'BetterDeskRelay', 'sshd', 'docker', 'HBBS', 'hbbs ', '', 'a'.repeat(129), 'a'.repeat(128), "it's", 'a:b', 'x@y.service',
        'betterdesk-server;reboot', '../hbbs', 'betterdesk-server.service.service', 'é'];
    const ACTIONS = ['start', 'stop', 'restart', 'reload', 'enable', 'disable', 'status', 'kill', 'START', '', 'restart ', 'mask', '__proto__', 'toString'];
    const answers = [
        { name: 'ok', run: (child) => setImmediate(() => { child.stdout.emit('data', Buffer.from('done\n')); child.emit('close', 0); }) },
        { name: 'fails', run: (child) => setImmediate(() => { child.stderr.emit('data', Buffer.from('Failed to start: Unit not found.\n')); child.emit('close', 5); }) },
        { name: 'long output', run: (child) => setImmediate(() => { child.stdout.emit('data', Buffer.from('x'.repeat(5000) + 'END')); child.stderr.emit('data', Buffer.from('é'.repeat(4100))); child.emit('close', 0); }) },
        { name: 'signal', run: (child) => setImmediate(() => child.emit('close', null)) },
        { name: 'hangs', timers: true, run: (child, call) => { child.kill = (signal) => { call.killed = signal === undefined ? null : signal; setImmediate(() => child.emit('close', null)); }; } },
    ];
    for (const platform of ['linux', 'win32', 'darwin']) {
        const sm = loadFor(platform);
        for (const can of [false, true]) {
            for (const name of NAMES) {
                for (const action of ACTIONS) {
                    for (const answer of (platform === 'darwin' || can ? [answers[0]] : answers)) {
                        if (answer !== answers[0] && !(sm.isAllowedBetterDeskService(name) && ['restart', 'status'].includes(action))) continue;
                        calls = [];
                        broker = { can, restarts: [] };
                        fireTimers = !!answer.timers;
                        asyncAnswer = answer.run;
                        const rec = { platform, broker: can, name, action, answer: answer.name };
                        try {
                            rec.result = await sm.controlService(name, action);
                        } catch (err) {
                            rec.error = err.message;
                        }
                        rec.calls = calls;
                        rec.brokerRestarts = broker.restarts;
                        out.control.push(rec);
                    }
                }
            }
        }
    }
    fireTimers = false;

    // ---- the server part of GET /api/server-management/info ----

    Object.defineProperty(process, 'platform', { value: 'linux', configurable: true });
    let health = null;
    stub('middleware/auth.js', { requireAuth: (_q, _s, n) => n(), requirePermission: () => (_q, _s, n) => n() });
    stub('services/serverBackend.js', { getHealth: async () => { if (health instanceof Error) throw health; return health; } });
    stub('services/database.js', {});
    stub('services/serverTerminalProxy.js', { isPtyAvailable: () => false, activeSessionCount: () => 0 });
    const express = require(path.join(WEB, 'node_modules/express'));
    const app = express();
    app.use(require(path.join(WEB, 'routes/server-management.routes.js')));
    const server = http.createServer(app);
    await new Promise((r) => server.listen(0, '127.0.0.1', r));
    const get = () => new Promise((resolve, reject) => {
        http.get({ port: server.address().port, path: '/api/server-management/info', host: '127.0.0.1' }, (res) => {
            let body = '';
            res.on('data', (c) => { body += c; });
            res.on('end', () => resolve(JSON.parse(body).server));
        }).on('error', reject);
    });
    const UPTIMES = [undefined, null, '', 5, '1h2m3s', '26h3m4.5s', '1.5h', '59.999s', '999ms', '1m0.5s', '3µs', '3us', '7ns', '1h1h', 'abc', '1x2s', '..s', '.5s', '1.5.5s',
        '9'.repeat(400) + 'h', '1e5s', '-3s', '1 h', 'h1', '2m', '2mm', '10ms5s', '0s', '0.4s', '72h0m0s', '1d', 'Infinitys', '1m'.repeat(50)];
    for (const uptime of UPTIMES) {
        for (const extra of [{ status: 'running', version: '3.5.264' }]) {
            health = { ...extra };
            if (uptime !== undefined) health.uptime = uptime;
            out.health.push({ health, server: await get() });
        }
    }
    const HEALTHS = [{ status: 'unreachable', backend: 'betterdesk', error: 'x' }, { status: 'running' }, { status: 'Running', version: 0 }, { status: 'running', version: '' },
        { status: 'running', version: 7 }, { status: 'running', version: { a: 1 } }, { status: 'running', version: ['x'] }, { status: 'running', version: true }, {}, null, 'running'];
    for (const h of HEALTHS) {
        health = h;
        out.health.push({ health: h, server: await get() });
    }
    health = new Error('down');
    out.health.push({ health: 'throws', server: await get() });
    server.close();

    fs.writeFileSync(OUT, JSON.stringify(out, null, 1) + '\n');
    console.log(`wrote ${OUT}: ${out.disks.length} disk, ${out.swap.length} swap, ${out.services.length} list, ${out.control.length} control, ${out.health.length} health vectors`);
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
