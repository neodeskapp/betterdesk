'use strict';

// Vectors for package billingclock: services/billingClockConfigService.js
// of the Node console, called with many inputs. Writes ../service.json.
//
// The module's neighbours that need a Go server or a systemd host are
// stubbed (config.isDocker, updateService, serverConnectionConfigService,
// the API client); its .env file is redirected into a temp folder. It is
// loaded twice: as on Linux and as on Windows, because the default of
// trust_os_ntp depends on the platform.

const fs = require('fs');
const os = require('os');
const path = require('path');
const Module = require('module');
const { consoleDir } = require(path.join(__dirname, '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle', 'lib'));

const servicePath = path.join(consoleDir, 'services', 'billingClockConfigService.js');
const realEnvPath = path.join(fs.realpathSync(consoleDir), '.env');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-clock-'));
const envPath = path.join(tmp, '.env');

// The .env file of the module lives in the temp folder
for (const name of ['existsSync', 'readFileSync', 'writeFileSync']) {
    const real = fs[name];
    fs[name] = function (file, ...rest) {
        return real.call(fs, file === realEnvPath ? envPath : file, ...rest);
    };
}

const stub = {
    config: { isDocker: false },
    calls: [],
    api: { get: null, put: null },
};
const load = Module._load;
Module._load = function (request, parent, isMain) {
    if (/config\/config$/.test(request)) return stub.config;
    if (/updateService$/.test(request)) return { sanitizeGoServerServiceConfig: () => ({ changed: false, changes: ['stub'], error: null, needsRestart: false }) };
    if (/serverConnectionConfigService$/.test(request)) return { restartServer: () => ({ stub: true }) };
    if (/betterdeskApi$/.test(request)) {
        return {
            apiClient: {
                get: async (p) => { stub.calls.push({ method: 'GET', path: p }); return stub.api.get(); },
                put: async (p, body) => { stub.calls.push({ method: 'PUT', path: p, body: JSON.stringify(body) }); return stub.api.put(); },
            },
        };
    }
    return load.apply(this, arguments);
};

function loadService(platform) {
    Object.defineProperty(process, 'platform', { value: platform, configurable: true });
    delete require.cache[require.resolve(servicePath)];
    return require(servicePath);
}

const outcome = (fn) => {
    try {
        return { value: fn() };
    } catch (err) {
        return { error: err.message };
    }
};
const asyncOutcome = async (fn) => {
    try {
        return { value: await fn() };
    } catch (err) {
        return { error: err.message };
    }
};

// Bodies, as JSON text; null stands for no value (undefined)
const bodies = [null, '{}', '[]', '"text"', '5', 'true', 'null',
    '{"ntp_servers":"pool.ntp.org"}', '{"ntp_servers":" 10.0.0.1 , ntp.example.com , "}', '{"ntp_servers":""}', '{"ntp_servers":null}',
    '{"ntp_servers":" , ,"}', '{"ntp_servers":["a.example","b.example"]}', '{"ntp_servers":[["a",["b"]],null,"c"]}', '{"ntp_servers":5}',
    '{"ntp_servers":true}', '{"ntp_servers":{}}', '{"ntp_servers":{"toString":"x"}}', '{"ntp_servers":[{"toString":1}]}',
    '{"ntp_servers":"bad host"}', '{"ntp_servers":"-a.example"}', '{"ntp_servers":"a.example-"}', '{"ntp_servers":"a"}',
    '{"ntp_servers":"a_b.example"}', '{"ntp_servers":"1.2.3.4,::1,fe80::1%eth0,::ffff:1.2.3.4"}', '{"ntp_servers":"300.1.1.1"}',
    '{"ntp_servers":"1.2.3"}', '{"ntp_servers":"[::1]"}', '{"ntp_servers":"\\u00e9.example"}', '{"ntp_servers":"\\u00a0pool.ntp.org\\ufeff"}',
    `{"ntp_servers":"${'a'.repeat(253)}"}`, `{"ntp_servers":"${'a'.repeat(254)}"}`, '{"ntp_servers":"a.example\\nb.example"}',
    '{"max_skew_ms":3000}', '{"max_skew_ms":"3000"}', '{"max_skew_ms":" 3000ms"}', '{"max_skew_ms":"0x10"}', '{"max_skew_ms":0}',
    '{"max_skew_ms":-5}', '{"max_skew_ms":0.5}', '{"max_skew_ms":2500.9}', '{"max_skew_ms":1e21}', '{"max_skew_ms":"1e21"}',
    '{"max_skew_ms":600000}', '{"max_skew_ms":600001}', '{"max_skew_ms":"99999999999999999999"}', '{"max_skew_ms":null}',
    '{"max_skew_ms":true}', '{"max_skew_ms":[7]}', '{"max_skew_ms":[7,8]}', '{"max_skew_ms":{}}', '{"max_skew_ms":{"toString":1}}',
    '{"max_skew_ms":"+42"}', '{"max_skew_ms":"\\u3000 42"}', '{"max_skew_ms":"\\u0661\\u0662"}', '{"max_skew_ms":-0}',
    '{"require_synced_clock":false}', '{"require_synced_clock":0}', '{"require_synced_clock":""}', '{"require_synced_clock":"0"}',
    '{"require_synced_clock":null}', '{"require_synced_clock":[]}', '{"require_synced_clock":{}}', '{"trust_os_ntp":true}',
    '{"trust_os_ntp":false}', '{"trust_os_ntp":"no"}', '{"trust_os_ntp":null}', '{"trust_os_ntp":0}',
    '{"billing_timezone":"Europe/Berlin"}', '{"billing_timezone":" europe/berlin "}', '{"billing_timezone":""}', '{"billing_timezone":"   "}',
    '{"billing_timezone":null}', '{"billing_timezone":"+01:00"}', '{"billing_timezone":"-0530"}', '{"billing_timezone":"+24:00"}',
    '{"billing_timezone":"GMT+1"}', '{"billing_timezone":"Etc/GMT+13"}', '{"billing_timezone":"Mars/Base"}', '{"billing_timezone":5}',
    '{"billing_timezone":["UTC"]}', '{"billing_timezone":["UTC","GMT"]}', '{"billing_timezone":{"toString":"x"}}',
    `{"billing_timezone":"Europe/${'x'.repeat(57)}"}`, `{"billing_timezone":"Europe/${'x'.repeat(58)}"}`, '{"billing_timezone":"EST"}',
    '{"billing_timezone":"\\u00a0UTC\\u2028"}', '{"billing_timezone":"Europe/Kyiv"}', '{"billing_timezone":"Asia/Kolkata"}',
    '{"ntp_servers":"bad host","max_skew_ms":0.5,"billing_timezone":"Nowhere/Town"}', '{"ntp_servers":"a.example","max_skew_ms":700000,"billing_timezone":"x"}',
    '{"ntp_servers":"time.example.com","max_skew_ms":3500,"require_synced_clock":false,"trust_os_ntp":false,"billing_timezone":"Europe/Warsaw"}',
    '{"__proto__":{"ntp_servers":"a.example"}}', '{"constructor":{"ntp_servers":"a"}}', '{"ntp_servers":"a.example","ntp_servers":"b.example"}',
];

const ntpEntries = ['', ' ', 'pool.ntp.org', ' pool.ntp.org ', 'a', '1', '1.1', 'a-b', '-a', 'a-', 'a..b', '.a', 'a.', 'a_b', 'A.B.C',
    '127.0.0.1', '255.255.255.255', '256.1.1.1', '01.1.1.1', '1.1.1.01', '::', '::1', '::ffff:1.2.3.4', '1::2::3', 'fe80::1%eth0',
    'fe80::1%', '[::1]', '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7:8:9', 'xn--bcher-kva.example', 'bücher.example', 'a'.repeat(253),
    'a'.repeat(254), '1'.repeat(253), 'a b', 'a\tb', ' a ', 'a​b', 'host:123', 'host/x', 'gög'];
const zoneInputs = ['', ' ', 'UTC', ' UTC ', 'utc', 'Europe/Berlin', 'europe/berlin', '　Europe/Berlin　', 'Europe/Berlin​',
    '+01:00', '+01', '-0530', '+1:00', '+24:00', 'GMT+1', 'Etc/GMT+12', 'Etc/GMT+13', 'X'.repeat(64), 'Europe/' + 'x'.repeat(57),
    'Europe/' + 'x'.repeat(58), '😀'.repeat(32), '😀'.repeat(33), 'Asia/Kolkata', 'Factory', 'Etc/Unknown', 'EST5EDT'];
const flagValues = [null, '', ' ', 'Y', 'y', 'YES', 'yes', ' Yes ', '1', 'TRUE', 'true', 'ON', 'on', 'N', 'no', '0', 'FALSE', 'off', 'oﬀ',
    'yeſ', 'maybe', '2', '-1', 'Tru', ' on ', 'ON\n'];
const envContents = ['', '\n', '# comment\n', 'NTP_SERVERS=10.0.0.1', 'NTP_SERVERS=10.0.0.1\nBILLING_MAX_CLOCK_SKEW_MS=1500\nBILLING_REQUIRE_SYNCED_CLOCK=0\nBILLING_TRUST_OS_NTP=1\nBILLING_TIMEZONE=Europe/Warsaw',
    'NTP_SERVERS=\nBILLING_MAX_CLOCK_SKEW_MS=\nBILLING_TIMEZONE=', ' NTP_SERVERS = a.example , b.example \r\nBILLING_TIMEZONE = europe/berlin \r\n',
    'NTP_SERVERS=a.example\nNTP_SERVERS=b.example', '=x\nNTP_SERVERS', '#NTP_SERVERS=a.example', 'NTP_SERVERS="a.example"',
    "BILLING_TIMEZONE='UTC'", 'BILLING_MAX_CLOCK_SKEW_MS=abc', 'BILLING_MAX_CLOCK_SKEW_MS=0', 'BILLING_MAX_CLOCK_SKEW_MS=99999999999999999999',
    'BILLING_MAX_CLOCK_SKEW_MS=1e21', 'BILLING_REQUIRE_SYNCED_CLOCK=maybe\nBILLING_TRUST_OS_NTP=off', 'BILLING_TRUST_OS_NTP=',
    'BILLING_TIMEZONE=Mars/Base', 'NTP_SERVERS=a=b', 'NTP_SERVERS=a.example\rBILLING_TIMEZONE=UTC', '﻿NTP_SERVERS=a.example',
    'export NTP_SERVERS=a.example', 'NTP_SERVERS=a.example # comment', 'ntp_servers=a.example', 'NTP_SERVERS= a.example '];
const existing = [null, '', 'PORT=5000\n', 'PORT=5000', '# head\nNTP_SERVERS=old\nOTHER=1\nNTP_SERVERS=older\n', 'BILLING_TIMEZONE=UTC\r\nX=1\r\n',
    'export NTP_SERVERS=old\n', '  NTP_SERVERS = old \n', '#NTP_SERVERS=old\n', 'NTP_SERVERS=old\n\n\n', '﻿A=1\n', 'A=1 B=2'];
const writes = ['{}', '{"ntp_servers":"time.example.com","max_skew_ms":3500,"require_synced_clock":false,"trust_os_ntp":false,"billing_timezone":"Europe/Warsaw"}',
    '{"ntp_servers":" a.example ,, b.example","max_skew_ms":"42abc","billing_timezone":" europe/berlin "}', '{"ntp_servers":"bad host"}',
    '{"billing_timezone":"Mars/Base"}', '{"max_skew_ms":600001}', '{"billing_timezone":"+05:30"}', 'null', '"text"'];

// API answers for the Docker layout with two containers
const httpError = (status, data) => Object.assign(new Error(`Request failed with status code ${status}`), { response: { status, data } });
const remoteAnswers = [
    { data: { ntp_servers: 'pool.ntp.org', max_skew_ms: 2000, require_synced_clock: true, trust_os_ntp: true } },
    { data: { ntp_servers: 'a.example', max_skew_ms: 1500, require_synced_clock: false, trust_os_ntp: false, billing_timezone: 'Europe/Berlin' } },
    { data: { billing_timezone: '' } }, { data: {} }, { data: null }, { data: '' }, { data: 'text' }, { data: [1, 2] }, { data: 7 },
    { data: { ntp_servers: '', max_skew_ms: '9', require_synced_clock: null, billing_timezone: 'mars/base' } },
    { data: { max_skew_ms: { toString: 1 } } }, { throws: httpError(500, { error: 'timesync not configured' }) }, { throws: new Error('connect ECONNREFUSED 127.0.0.1:21121') },
];
const putAnswers = [
    { data: { config: { ntp_servers: 'time.example.com', max_skew_ms: 3500, require_synced_clock: false, trust_os_ntp: false, billing_timezone: 'Europe/Warsaw' }, status: { synced: true } } },
    { data: { config: { ntp_servers: 'x.example' } } }, { data: {} }, { data: null }, { data: { error: 'max_skew_ms must be between 1 and 600000' } },
    { data: { error: { toString: 1 } } }, { data: { error: 5, config: {} } }, { data: { error: '', config: 'text', status: 0 } },
    { data: { config: null, status: { synced: false, offset_ms: -12.5 } } }, { data: 'saved' },
    { throws: httpError(400, { error: 'billing_timezone must be a valid IANA timezone' }) }, { throws: new Error('timeout of 10000ms exceeded') },
];
const answer = (a) => (a.throws ? Promise.reject(a.throws) : Promise.resolve({ data: a.data }));

async function forPlatform(platform) {
    const s = loadService(platform);
    const out = { platform, defaults: s.DEFAULTS, common: s.COMMON_TIMEZONES, detected: s.detectOsTimezone() };
    const parse = (text) => (text === null ? undefined : JSON.parse(text));
    out.normalize = bodies.map((b) => ({ raw: b, normalized: outcome(() => s.normalizeSettings(parse(b))), validated: outcome(() => s.validateSettings(parse(b))) }));
    if (platform !== 'linux') return out;

    out.ntp = ntpEntries.map((e) => [e, s.isValidNTPServerEntry(e)]);
    out.zones = zoneInputs.map((z) => [z, s.isValidIanaTimezone(z)]);
    out.flags = [];
    for (const v of flagValues) for (const d of [true, false]) out.flags.push([v, d, s.envToBool(v === null ? undefined : v, d)]);
    out.envFiles = envContents.map((c) => ({ content: c, parsed: s.parseEnvFile(c), settings: outcome(() => s.settingsFromEnvContent(c)) }));

    out.writes = [];
    for (const before of existing) {
        for (const w of writes) {
            fs.rmSync(envPath, { force: true });
            if (before !== null) fs.writeFileSync(envPath, before);
            const result = outcome(() => s.restoreClockSettings(parse(w)));
            const after = fs.existsSync(envPath) ? fs.readFileSync(envPath, 'utf8') : null;
            const mode = fs.existsSync(envPath) ? (fs.statSync(envPath).mode & 0o777) : null;
            out.writes.push({ before, raw: w, result, after, mode });
        }
    }

    // Saves and reads, outside and inside the Docker layout
    out.saves = [];
    for (const docker of [false, true]) {
        stub.config.isDocker = docker;
        process.env.BETTERDESK_DOCKER_LAYOUT = 'split';
        for (const w of writes) {
            for (const put of docker ? putAnswers : [null]) {
                fs.rmSync(envPath, { force: true });
                fs.writeFileSync(envPath, 'PORT=5000\n');
                stub.calls = [];
                stub.api.put = () => answer(put);
                const result = await asyncOutcome(() => s.saveClockSettings(parse(w), { restart: false }));
                out.saves.push({ docker, raw: w, put, result, calls: stub.calls, after: fs.readFileSync(envPath, 'utf8') });
            }
        }
    }
    out.reads = [];
    for (const content of [null, 'BILLING_TIMEZONE=Asia/Tokyo\nNTP_SERVERS=file.example\n']) {
        for (const remote of remoteAnswers) {
            fs.rmSync(envPath, { force: true });
            if (content !== null) fs.writeFileSync(envPath, content);
            stub.calls = [];
            stub.api.get = () => answer(remote);
            const result = await asyncOutcome(() => s.getRuntimeClockSettings());
            out.reads.push({ content, remote, result, calls: stub.calls });
        }
    }
    for (const layout of ['split', 'SPLIT ', ' Split', 'single', '']) {
        for (const docker of [true, false]) {
            stub.config.isDocker = docker;
            process.env.BETTERDESK_DOCKER_LAYOUT = layout;
            out.reads.push({ layout, docker, split: s.isDockerSplitDeployment() });
        }
    }
    stub.config.isDocker = false;
    return out;
}

(async () => {
    const linux = await forPlatform('linux');
    const windows = await forPlatform('win32');
    const replacer = (_k, v) => (v instanceof Error ? { thrown: v.message, status: v.response && v.response.status, data: v.response && v.response.data } : v);
    fs.writeFileSync(path.join(__dirname, '..', 'service.json'), JSON.stringify({ linux, windows }, replacer) + '\n');
    fs.rmSync(tmp, { recursive: true, force: true });
    console.log(`service.json: ${linux.normalize.length + windows.normalize.length} bodies, ${linux.writes.length} writes, ${linux.saves.length} saves, ${linux.reads.length} reads`);
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
