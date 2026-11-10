'use strict';

// Records services/serverConnectionConfigService.js, lib/productVersion.js
// and the error messages of child_process.execSync. Writes
// connection.json.gz and version.json.gz.
//
// The service reads and writes fixed paths (the systemd unit, its drop-in,
// the compose file beside the console, /.dockerenv). The script redirects
// those four into a scratch folder, so nothing outside it is touched.

const fs = require('fs');
const path = require('path');
const childProcess = require('child_process');
const lib = require('./lib');

const R = lib.rng(0x5eed03);

const api = { get: null, put: null, calls: [] };
const answer = (a) => {
    if (!a || a.fail) throw new Error(`Request failed with status code ${a ? a.status : 500}`);
    return { data: a.data };
};
lib.fakeModule('services/betterdeskApi.js', {
    apiClient: {
        get: async (url) => { api.calls.push({ method: 'GET', url }); return answer(api.get); },
        put: async (url, body) => { api.calls.push({ method: 'PUT', url, body: JSON.stringify(body) }); return answer(api.put); },
    },
});
const control = { reload: { success: true }, restart: { success: true }, calls: [] };
lib.fakeModule('services/updateService.js', {
    COMPONENTS: { server: { service: 'betterdesk-server' } },
    daemonReload: () => { control.calls.push('daemonReload'); return control.reload; },
    restartService: (name) => { control.calls.push(`restartService ${name}`); return { ...control.restart, service: name }; },
});
const privileged = { can: false, result: { success: true }, fail: null, calls: [] };
lib.fakeModule('lib/privilegedUpdateHelper.js', {
    canUsePrivilegedUpdate: () => privileged.can,
    invokePrivilegedUpdate: (payload) => {
        privileged.calls.push(JSON.stringify(payload));
        if (privileged.fail) throw new Error(privileged.fail);
        return privileged.result;
    },
});

const config = require(lib.web('config/config.js'));
const svc = require(lib.web('services/serverConnectionConfigService.js'));
const productVersion = require(lib.web('lib/productVersion.js'));

const errorLog = console.error;
console.error = () => {};

// ---------------------------------------------------------- path redirect

const ROOT = path.join(lib.SCRATCH, 'conn');
const COMPOSE = path.join(lib.WEB, '..', 'docker-compose.yml');
const MAPPED = {
    [svc.SYSTEMD_SERVER_UNIT]: 'unit',
    [svc.SYSTEMD_CONNECTION_DROPIN]: 'dropin',
    [COMPOSE]: 'compose',
    '/.dockerenv': 'dockerenv',
};
const real = { existsSync: fs.existsSync, readFile: fs.promises.readFile, writeFile: fs.promises.writeFile };
const mapPath = (p) => (Object.prototype.hasOwnProperty.call(MAPPED, p) ? path.join(ROOT, MAPPED[p]) : p);
fs.existsSync = (p) => real.existsSync(mapPath(p));
fs.promises.readFile = (p, ...rest) => real.readFile(mapPath(p), ...rest);
fs.promises.writeFile = (p, ...rest) => real.writeFile(mapPath(p), ...rest);
// scrub names the four files the same way wherever they show up in an
// answer or a message: by their real path or by the redirected one.
const scrub = (text) => String(text)
    .split(ROOT + path.sep).join('{ROOT}/')
    .split(svc.SYSTEMD_CONNECTION_DROPIN).join('{ROOT}/dropin')
    .split(svc.SYSTEMD_SERVER_UNIT).join('{ROOT}/unit')
    .split(COMPOSE).join('{ROOT}/compose');

function stage(state) {
    fs.rmSync(ROOT, { recursive: true, force: true });
    fs.mkdirSync(ROOT, { recursive: true });
    for (const name of ['unit', 'dropin', 'compose']) {
        const content = state[name];
        if (content === null || content === undefined) continue;
        if (content.dir) fs.mkdirSync(path.join(ROOT, name));
        else fs.writeFileSync(path.join(ROOT, name), content, { mode: 0o640 });
    }
    if (state.dockerenv) fs.writeFileSync(path.join(ROOT, 'dockerenv'), '');
    config.isDocker = Boolean(state.isDocker);
    for (const key of ['DOCKER', 'BETTERDESK_DOCKER_LAYOUT']) {
        if (state.env && state.env[key] !== undefined) process.env[key] = state.env[key];
        else delete process.env[key];
    }
    privileged.can = Boolean(state.privileged);
    privileged.result = state.privilegedResult || { success: true };
    privileged.fail = state.privilegedFail || null;
    privileged.calls = [];
    api.get = state.apiGet || null;
    api.put = state.apiPut || null;
    api.calls = [];
    control.reload = state.reload || { success: true };
    control.restart = state.restart || { success: true };
    control.calls = [];
}

function filesAfter() {
    const out = {};
    for (const name of ['unit', 'dropin', 'compose']) {
        const file = path.join(ROOT, name);
        try {
            const st = fs.statSync(file);
            out[name] = st.isDirectory() ? { dir: true } : { bytes: fs.readFileSync(file).toString('base64'), mode: st.mode & 0o777 };
        } catch (_) {
            out[name] = null;
        }
    }
    return out;
}

// ------------------------------------------------------------- generators

const MANAGED = svc.MANAGED_ENV_KEYS;
const OTHER_KEYS = ['ENCRYPTED_ONLY', 'DB_URL', 'LOG_LEVEL', 'P2P_FIRSTX', 'XP2P_FIRST', 'p2p_first', 'RELAY_SERVERS'];
const YN = ['Y', 'N', 'y', 'n', 'YES', 'no', '1', '0', 'TRUE', 'false', 'ON', 'off', 'On', ' Y ', '', 'maybe', 'ye\u017f', 'o\ufb00', '\uff11', 'Y\u00a0', 'yes!', 'T'];
const FALLBACKS = ['3000', '0', '2000', ' 12', '12 ', '-5', '1e3', '12px', '', 'abc', '99999999999999999999999', '0x10', '+7', '-0', '\uff13', '1.9', '.5', '30001', '007', '\u00a01500'];
const WS = [' ', '\t', '\u00a0', '\u3000', '\ufeff', '\u2028'];

function envValue(key) {
    return key === 'P2P_FALLBACK_MS' ? R.pick(FALLBACKS) : R.pick(YN);
}

function systemdText() {
    const eol = R.pick(['\n', '\n', '\n', '\r\n', '\r']);
    const lines = [];
    if (R.chance(0.8)) lines.push('[Unit]', 'Description=BetterDesk Server', R.chance(0.5) ? '' : 'After=network.target');
    const service = [];
    if (R.chance(0.9)) service.push(R.pick(['[Service]', '[Service]', ' [Service]', '[Service] ', '[Service]X', '[service]']));
    const n = R.int(9);
    for (let i = 0; i < n; i++) {
        const key = R.chance(0.7) ? R.pick(MANAGED) : R.pick(OTHER_KEYS);
        const value = envValue(key);
        switch (R.int(14)) {
        case 0: service.push(`Environment="${key}=${value}"`); break;
        case 1: service.push(`Environment='${key}=${value}'`); break;
        case 2: service.push(`Environment= ${key} = ${value} `); break;
        case 3: service.push(`  Environment=${key}=${value}`); break;
        case 4: service.push(`#Environment=${key}=${value}`); break;
        case 5: service.push(`Environment=${key}`); break;
        case 6: service.push(R.pick(['Environment=', 'Environment==x', 'Environment="', "Environment='", 'Environment=""', 'environment=P2P_FIRST=N'])); break;
        case 7: service.push(`Environment=${key}=${value} ${R.pick(MANAGED)}=${R.pick(YN)}`); break;
        case 8: service.push(R.pick(['Type=simple', 'Restart=always', 'User=betterdesk', 'EnvironmentFile=/opt/betterdesk/.env', ''])); break;
        case 9: service.push(`Environment="${key}=${value}`); break;
        case 10: service.push(`Environment=${key}=${value}${R.pick(WS)}`); break;
        default: service.push(`Environment=${key}=${value}`);
        }
    }
    if (R.chance(0.8)) service.splice(R.int(service.length + 1), 0, 'ExecStart=/opt/betterdesk/betterdesk-server -mode all');
    lines.push(...service);
    if (R.chance(0.7)) lines.push('', '[Install]', 'WantedBy=multi-user.target');
    let text = lines.join(eol);
    if (R.chance(0.8)) text += eol;
    if (R.chance(0.03)) text = text.replace(eol, '\u2028');
    return text;
}

function composeText() {
    const eol = R.pick(['\n', '\n', '\n', '\n', '\r\n']);
    const lines = [];
    if (R.chance(0.3)) lines.push(R.pick(['version: "3.8"', '# BetterDesk', 'name: betterdesk']));
    if (R.chance(0.95)) lines.push(R.pick(['services:', 'services:', 'services: ', 'services:\t', 'services: # all', ' services:', 'Services:']));
    const serviceIndent = R.pick(['  ', '  ', '  ', '    ', '\t', '']);
    const names = R.pick([['server', 'console'], ['console', 'server'], ['server'], ['console'], ['db', 'server', 'console'], ['server', 'server'], ['betterdesk-server']]);
    for (const name of names) {
        lines.push(`${serviceIndent}${name}:${R.pick(['', '', '', ' ', ' # the server'])}`);
        const inner = serviceIndent + R.pick(['  ', '  ', '    ', '\t']);
        if (R.chance(0.8)) lines.push(`${inner}image: betterdesk-${name}:local`);
        const blocks = R.chance(0.9) ? 1 + (R.chance(0.1) ? 1 : 0) : 0;
        for (let b = 0; b < blocks; b++) {
            lines.push(`${inner}${R.pick(['environment:', 'environment:', 'environment:', 'environment: ', 'environment: # vars', 'Environment:'])}`);
            const item = inner + R.pick(['  ', '  ', '', '    ']);
            const n = R.int(7);
            for (let i = 0; i < n; i++) {
                const key = R.chance(0.6) ? R.pick(MANAGED) : R.pick(OTHER_KEYS);
                const value = envValue(key);
                switch (R.int(14)) {
                case 0: lines.push(`${item}- "${key}=${value}"`); break;
                case 1: lines.push(`${item}- ${key}="${value}"`); break;
                case 2: lines.push(`${item}- ${key}='${value}'`); break;
                case 3: lines.push(`${item}- ${key}=\${${key}:-Y}`); break;
                case 4: lines.push(`${item}-${key}=${value}`); break;
                case 5: lines.push(`${item}# - ${key}=${value}`); break;
                case 6: lines.push(''); break;
                case 7: lines.push(`${item}-  ${key} = ${value}  `); break;
                case 8: lines.push(`${item}- ${key}`); break;
                case 9: lines.push(`${item}${key}: ${value}`); break;
                case 10: lines.push(`${item}-${R.pick(WS)}${key}=${value}`); break;
                default: lines.push(`${item}- ${key}=${value}`);
                }
            }
            if (R.chance(0.4)) lines.push(`${inner}ports:`, `${inner}  - "21116:21116"`);
        }
        if (R.chance(0.3)) lines.push(`${inner}restart: unless-stopped`);
    }
    if (R.chance(0.3)) lines.push('volumes:', '  data:');
    let text = lines.join(eol);
    if (R.chance(0.8)) text += eol;
    return text;
}

const JSON_VALUES = [null, true, false, 0, 1, -1, 2000, 2500.5, 30000, 30001, 1e21, 1e-7, '', '3000', ' 12 ', '0x10', '1e3', 'abc', 'relay_only', 'p2p_first',
    'RELAY_ONLY', 'true', 'false', 'Y', [], [5], ['7'], [1, 2], [[3]], [null], {}, { a: 1 }, '-0', 'Infinity', '-Infinity', '12px', '.5', '5.', '+7', '1_000',
    '0b101', '0o17', '0XFF', '\u00a042\ufeff', '99999999999999999999999999', 4.2e-9];

function settingsObject() {
    const o = {};
    for (const key of ['mode', 'p2p_fallback_ms', 'same_nat_relay', 'allow_shared_nat_initiator', 'logged_in_only_initiator', 'operator_only_outbound']) {
        if (R.chance(0.15)) continue;
        if (key === 'mode' && R.chance(0.7)) o[key] = R.pick(['relay_only', 'p2p_first']);
        else if (key === 'p2p_fallback_ms' && R.chance(0.5)) o[key] = R.pick([0, 500, 2000, 3000, 30000, 1500.5]);
        else if (key !== 'mode' && key !== 'p2p_fallback_ms' && R.chance(0.7)) o[key] = R.chance(0.5);
        else o[key] = R.pick(JSON_VALUES);
    }
    return o;
}

function envObject() {
    const env = {};
    for (const key of MANAGED) if (R.chance(0.6)) env[key] = envValue(key);
    return env;
}

async function connectionVectors() {
    const out = {};

    // --- the pure functions
    const units = [
        '', '\n', '[Service]', '[Service]\n', 'Environment=P2P_FIRST=N', 'Environment=P2P_FIRST=N\n[Service]\nExecStart=/x\n',
        '[Unit]\nDescription=BetterDesk Server\n\n[Service]\nType=simple\nExecStart=/opt/betterdesk/betterdesk-server -mode all\nEnvironment=P2P_FIRST=Y\nEnvironment=ALWAYS_USE_RELAY=N\nRestart=always\n\n[Install]\nWantedBy=multi-user.target\n',
        '[Service]\nEnvironment=P2P_FIRST=Y\n\nExecStart=/x\n', '[Service]\nType=simple\n[Install]\n', '[Install]\nWantedBy=x\n[Service]\nUser=a',
        '[Service]\r\nEnvironment=P2P_FIRST=Y\r\nExecStart=/x\r\n', 'Environment=P2P_FIRST=Y\u2028Environment=ALWAYS_USE_RELAY=Y\u2029Environment=SAME_NAT_RELAY=N\rEnvironment=P2P_FALLBACK_MS=1',
        '[Service]\nEnvironment="P2P_FIRST=N" "ALWAYS_USE_RELAY=Y"\n', '[Service]\nEnvironment=P2P_FIRST=N=Y\nEnvironment=P2P_FIRST =Y\n',
    ];
    for (let i = 0; i < 700; i++) units.push(systemdText());
    const composes = [
        '', 'services:\n  server:\n    environment:\n', 'services:\n  server:\n    environment:\n      - A=1',
        'services:\n  server:\n    image: betterdesk-server:local\n    environment:\n      - ENCRYPTED_ONLY=1\n      - DB_URL=/opt/rustdesk/db_v2.sqlite3\n      - P2P_FIRST=Y\n  console:\n    environment:\n      - NODE_ENV=production\n',
        'services:\n  console:\n    environment:\n      - P2P_FIRST=N\n', 'server:\n  environment:\n    - P2P_FIRST=N\n', 'services:\nserver:\n  environment:\n  - P2P_FIRST=N\n',
        'services:\n  server:\n    environment:\n      P2P_FIRST: "N"\n', 'services:\n  server:\n    environment:\n      - P2P_FIRST=N\n    environment:\n      - ALWAYS_USE_RELAY=Y\n',
        'services:\n  server:\n    build: .\n  console:\n    build: .\n', 'services:\n  server:\n    environment:\n      - P2P_FIRST=N\n\n      - SAME_NAT_RELAY=N\n# end\n',
        'services:\n  server:\n    environment:\n    # managed below\n      - P2P_FIRST=N\n', 'services:\n\tserver:\n\t\tenvironment:\n\t\t\t- P2P_FIRST=N\n',
    ];
    for (let i = 0; i < 700; i++) composes.push(composeText());

    const varsList = [];
    for (let i = 0; i < 40; i++) varsList.push(svc.envVarsFromSettings(settingsObject()));
    for (let i = 0; i < 20; i++) {
        const partial = {};
        for (const key of MANAGED) if (R.chance(0.5)) partial[key] = R.pick(['Y', 'N', '', '1500', 'x y']);
        varsList.push(partial);
    }
    varsList.push({});

    out.parse_systemd = units.map((text) => ({ text, out: svc.parseSystemdEnvironment(text) }));
    out.patch_systemd = [];
    for (const text of units) for (let i = 0; i < 2; i++) {
        const vars = R.pick(varsList);
        out.patch_systemd.push({ text, vars, out: svc.patchSystemdEnvironment(text, vars) });
    }
    out.parse_compose = composes.map((text) => ({ text, out: svc.parseDockerComposeEnvironment(text) }));
    out.patch_compose = [];
    for (const text of composes) for (let i = 0; i < 2; i++) {
        const vars = R.pick(varsList);
        out.patch_compose.push({ text, vars, ...lib.caught(() => svc.patchDockerComposeEnvironment(text, vars)) });
    }

    out.settings_from_env = [];
    for (let i = 0; i < 1500; i++) {
        const env = envObject();
        const source = R.pick(['systemd', 'docker', 'defaults']);
        out.settings_from_env.push({ env, source, out: svc.settingsFromEnv(env, source), mode: svc.modeFromEnvVars(env) });
    }
    for (const a of YN) for (const b of YN) {
        const env = { ALWAYS_USE_RELAY: a, P2P_FIRST: b };
        out.settings_from_env.push({ env, source: 'systemd', out: svc.settingsFromEnv(env, 'systemd'), mode: svc.modeFromEnvVars(env) });
    }
    for (const f of FALLBACKS) {
        const env = { P2P_FALLBACK_MS: f };
        out.settings_from_env.push({ env, source: 'docker', out: svc.settingsFromEnv(env, 'docker'), mode: svc.modeFromEnvVars(env) });
    }

    out.env_vars_from_settings = [];
    for (let i = 0; i < 1500; i++) {
        const settings = settingsObject();
        out.env_vars_from_settings.push({ settings, out: svc.envVarsFromSettings(settings) });
    }
    for (const v of JSON_VALUES) {
        for (const key of ['mode', 'p2p_fallback_ms', 'same_nat_relay', 'allow_shared_nat_initiator']) {
            const settings = { [key]: v };
            out.env_vars_from_settings.push({ settings, out: svc.envVarsFromSettings(settings) });
        }
    }

    out.settings_from_api = [];
    for (const data of [...JSON_VALUES, undefined]) out.settings_from_api.push({ data, hasData: data !== undefined, out: svc.settingsFromApi(data) });
    for (let i = 0; i < 1200; i++) {
        const data = settingsObject();
        out.settings_from_api.push({ data, hasData: true, out: svc.settingsFromApi(data) });
    }

    // --- the service on files and on the server's API
    const UNIT = units[6];
    const COMPOSE_TEXT = composes[3];
    const API_ANSWERS = [
        { data: { mode: 'p2p_first', p2p_fallback_ms: 2500, same_nat_relay: true, allow_shared_nat_initiator: false, logged_in_only_initiator: true, operator_only_outbound: false } },
        { data: { config: { mode: 'relay_only', p2p_fallback_ms: 3500, same_nat_relay: false, allow_shared_nat_initiator: true, logged_in_only_initiator: true, operator_only_outbound: true } } },
        { data: { config: null, mode: 'relay_only' } }, { data: { config: {} } }, { data: { config: 'text', mode: 'relay_only' } }, { data: {} }, { data: '' },
        { data: 'not json' }, { data: null }, { data: [] }, { data: { mode: 'relay_only', p2p_fallback_ms: '1200' } }, { data: { p2p_fallback_ms: null } },
        { data: { p2p_fallback_ms: 30001 } }, { data: { p2p_fallback_ms: -1 } }, { data: { config: [], mode: 'relay_only' } }, { data: 0 }, { data: false },
        { fail: true, status: 500 }, { fail: true, status: 403 },
    ];
    const STATES = [
        {},
        { unit: UNIT },
        { unit: UNIT, dropin: '[Service]\nEnvironment=P2P_FIRST=N\nEnvironment=ALWAYS_USE_RELAY=Y\nEnvironment=P2P_FALLBACK_MS=4000\n' },
        { unit: UNIT, dropin: { dir: true } },
        { unit: { dir: true } },
        { unit: UNIT, dockerenv: true },
        { unit: UNIT, env: { DOCKER: 'true' } },
        { unit: UNIT, env: { DOCKER: 'TRUE' } },
        { unit: UNIT, compose: COMPOSE_TEXT },
        { unit: UNIT, compose: COMPOSE_TEXT, dockerenv: true },
        { unit: UNIT, compose: COMPOSE_TEXT, env: { DOCKER: 'true' } },
        { unit: UNIT, compose: COMPOSE_TEXT, env: { DOCKER: 'TRUE' } },
        { unit: UNIT, compose: COMPOSE_TEXT, env: { DOCKER: '1' } },
        { compose: COMPOSE_TEXT },
        { compose: COMPOSE_TEXT, dockerenv: true },
        { compose: 'services:\n  console:\n    image: x\n' },
        { compose: { dir: true } },
        { compose: { dir: true }, unit: UNIT, dockerenv: true },
        { dockerenv: true },
        { unit: UNIT, privileged: true },
        { unit: UNIT, privileged: true, privilegedResult: { success: true, path: '/run/betterdesk/other.conf' } },
        { unit: UNIT, privileged: true, privilegedFail: 'sudo: a password is required' },
        { unit: '[Service]\r\nEnvironment=P2P_FIRST=N\r\nExecStart=/x\r\n' },
        { unit: '' },
        { isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: 'split' }, apiGet: API_ANSWERS[0], apiPut: API_ANSWERS[1] },
        { isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: ' Split ' }, apiGet: API_ANSWERS[1], apiPut: API_ANSWERS[0], unit: UNIT },
        { isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: 'single' }, compose: COMPOSE_TEXT, dockerenv: true },
        { isDocker: false, env: { BETTERDESK_DOCKER_LAYOUT: 'split' }, unit: UNIT },
        { isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: 'SPL\u0130T' }, unit: UNIT },
        { isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: 'split' } },
    ];
    for (const a of API_ANSWERS) STATES.push({ isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: 'split' }, apiGet: a, apiPut: R.pick(API_ANSWERS) });
    for (const a of API_ANSWERS) STATES.push({ isDocker: true, env: { BETTERDESK_DOCKER_LAYOUT: 'split' }, apiGet: R.pick(API_ANSWERS), apiPut: a });
    for (let i = 0; i < 250; i++) {
        const state = {};
        if (R.chance(0.6)) state.unit = systemdText();
        if (R.chance(0.3)) state.dropin = systemdText();
        if (R.chance(0.5)) state.compose = composeText();
        if (R.chance(0.3)) state.dockerenv = true;
        if (R.chance(0.2)) state.privileged = true;
        STATES.push(state);
    }
    const RESULTS = [
        { reload: { success: true }, restart: { success: true } },
        { reload: { success: true, skipped: true }, restart: { success: true } },
        { reload: { success: false, error: 'Failed to reload daemon: Access denied' }, restart: { success: true } },
        { reload: { success: true }, restart: { success: false, error: 'Job for betterdesk-server.service failed', nonCritical: false, hint: undefined } },
        { reload: { success: true }, restart: { success: false, error: 'OpenService: access is denied', nonCritical: true, hint: 'Restart BetterDeskServer manually' } },
    ];

    out.service = [];
    for (const base of STATES) {
        const state = { ...base, ...R.pick(RESULTS) };
        const settings = settingsObject();
        const record = { state, settings };

        stage(state);
        record.splitDeployment = svc.isDockerSplitDeployment();
        record.source = svc.detectDeploymentSource();
        record.get = await lib.caughtAsync(() => svc.getConnectionMode());
        if (record.get.err) record.get.err = scrub(record.get.err);
        record.getCalls = api.calls;

        stage(state);
        record.set = await lib.caughtAsync(() => svc.setConnectionMode(settings));
        if (record.set.err) record.set.err = scrub(record.set.err);
        // JSON.stringify drops what a route would not send either.
        if (record.set.out) record.set.json = scrub(JSON.stringify({ ...record.set.out, restartRequired: 'PENDING' }));
        delete record.set.out;
        record.setCalls = api.calls;
        record.privilegedCalls = privileged.calls;
        record.after = filesAfter();
        record.getAfter = await lib.caughtAsync(() => svc.getConnectionMode());
        if (record.getAfter.err) record.getAfter.err = scrub(record.getAfter.err);

        stage(state);
        const realExec = childProcess.execSync;
        const shell = { calls: [] };
        childProcess.execSync = (command, options) => {
            shell.calls.push({ command, cwd: path.relative(path.join(lib.WEB, '..'), options.cwd) || '.', timeout: options.timeout });
            if (state.shellError) throw new Error(state.shellError);
        };
        state.shellError = R.chance(0.3) ? 'Command failed: docker compose up -d server\n/bin/sh: 1: docker: not found\n' : null;
        record.restart = lib.caught(() => JSON.stringify(svc.restartServer()));
        record.shellCalls = shell.calls;
        record.controlCalls = control.calls;
        childProcess.execSync = realExec;

        out.service.push(record);
    }

    // --- child_process.execSync: the messages of harmless commands
    out.shell = [];
    for (const [command, timeout, cwd] of [
        ['exit 0', 5000, '/'], ['exit 3', 5000, '/'], ['echo out; echo err1 >&2; exit 3', 5000, '/'], ['printf "a\\nb" >&2; exit 1', 5000, '/'],
        ['serverinfo-no-such-command-xyz', 5000, '/'], ['echo fine >&2', 5000, '/'], ['sleep 5', 300, '/'], ['exit 0', 5000, '/serverinfo/no/such/dir'],
        ['printf "\\377\\376" >&2; exit 2', 5000, '/'], ['kill -9 $$', 5000, '/'],
    ]) {
        const result = lib.caught(() => { childProcess.execSync(command, { cwd, timeout, stdio: 'pipe' }); return null; });
        out.shell.push({ command, timeoutMs: timeout, cwd, ...result });
    }
    return out;
}

// ---------------------------------------------------------------- version

function versionVectors() {
    const out = { read: [] };
    const VERSION_FILES = [null, '4.1.2\n', '4.1.2', ' 4.1.2 \r\n', '\ufeff3.5.224\n', 'v4.1.2', '4.1', '4.1.2.3', '4.1.2-beta', 'not-a-version\n', '',
        '4.1.2\n4.1.3\n', '\uff14.1.2', '04.01.002', '999999999999.0.1', { dir: true }, '1.2.3\u00a0', '1.2.3\u2028', '1. 2.3'];
    const PACKAGES = [null, '{"version":"3.9.8"}', '{"name":"x","version":" 3.5.108 "}', '{"version":"3.9"}', '{"version":3}', '{"version":null}', '{}', 'null', '[]', '"3.9.8"',
        '{"version":"3.9.8"', '\ufeff{"version":"3.9.8"}', '{"version":"1.0.0","version":"2.0.0"}', '{"Version":"3.9.8"}', '{"version":["3.9.8"]}', '', '{"version":""}',
        '{"version":"3.9.8"} trailing', '{"x":1e999,"version":"3.9.8"}', '{"version":"3.9.\\u0038"}', '{"version":false}', '{"version":0}', '{"version":true}',
        '{"__proto__":{"version":"9.9.9"}}', ' \n{"version":"3.9.8"}\n ', '{"version":"3.9.8",}', "{'version':'3.9.8'}", { dir: true }, '123', 'true'];
    let n = 0;
    const one = (root, consoleFile, pkg, embedded, layout, fallback) => {
        const base = path.join(lib.SCRATCH, `version-${n++}`);
        const consoleDirName = layout === 'flat' ? '' : (layout === 'default' ? 'web-nodejs' : 'console');
        const consoleDir = path.join(base, consoleDirName);
        const serverDir = path.join(base, 'betterdesk-server');
        fs.mkdirSync(consoleDir, { recursive: true });
        fs.mkdirSync(path.join(serverDir, 'internal', 'productversion'), { recursive: true });
        const put = (file, content) => {
            if (content === null) return;
            if (content.dir) fs.mkdirSync(file, { recursive: true });
            else fs.writeFileSync(file, content);
        };
        put(path.join(base, 'VERSION'), root);
        if (layout !== 'flat') put(path.join(consoleDir, 'VERSION'), consoleFile);
        put(path.join(consoleDir, 'package.json'), pkg);
        put(path.join(serverDir, 'internal', 'productversion', 'VERSION'), embedded);
        const opts = { rootDir: base };
        if (layout !== 'default') opts.consoleDir = consoleDir;
        if (fallback !== undefined) opts.fallback = fallback;
        return {
            root, consoleFile: layout === 'flat' ? null : consoleFile, pkg, embedded, layout, fallback: fallback === undefined ? null : fallback,
            version: productVersion.readProductVersion(opts),
            packageVersion: productVersion.readPackageJsonVersion(consoleDir),
            embeddedVersion: productVersion.readEmbeddedServerVersion(serverDir),
        };
    };
    for (const v of VERSION_FILES) {
        out.read.push(one(v, null, null, null, 'default'));
        out.read.push(one(null, v, '{"version":"3.2.0"}', null, 'named'));
        out.read.push(one(null, null, null, v, 'default'));
    }
    for (const p of PACKAGES) {
        out.read.push(one(null, null, p, null, 'default'));
        out.read.push(one('bad', 'bad', p, null, 'named', '9.9.9'));
    }
    for (let i = 0; i < 600; i++) {
        out.read.push(one(R.pick(VERSION_FILES), R.pick(VERSION_FILES), R.pick(PACKAGES), R.pick(VERSION_FILES),
            R.pick(['default', 'named', 'flat']), R.pick([undefined, undefined, '9.9.9', ''])));
    }
    out.embedded_without_dir = [productVersion.readEmbeddedServerVersion(null), productVersion.readEmbeddedServerVersion('')];
    return out;
}

async function main() {
    lib.write('connection', await connectionVectors());
    fs.existsSync = real.existsSync;
    fs.promises.readFile = real.readFile;
    fs.promises.writeFile = real.writeFile;
    lib.write('version', versionVectors());
    console.error = errorLog;
    lib.writeCounts('connection.js');
}

main().catch((err) => {
    console.error = errorLog;
    console.error(err);
    process.exit(1);
});
