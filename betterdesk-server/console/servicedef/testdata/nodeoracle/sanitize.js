'use strict';

// Vectors for package servicedef: sanitizeGoServerServiceConfig of
// services/updateService.js and the helpers it calls, taken out of that
// file as source and run with the system around them stubbed (the shell,
// nssm, the files, the privileged daemon reload). Loading the whole
// updater would start too much. Writes ../sanitize.json.

const fs = require('fs');
const path = require('path');
const { consoleDir } = require(path.join(__dirname, '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle', 'lib'));

const source = fs.readFileSync(path.join(consoleDir, 'services', 'updateService.js'), 'utf8');
function grab(name) {
    const start = source.indexOf(`function ${name}(`);
    if (start < 0) throw new Error(`no function ${name}`);
    let depth = 0;
    for (let i = source.indexOf('{', start); i < source.length; i++) {
        if (source[i] === '{') depth++;
        if (source[i] === '}' && --depth === 0) return source.slice(start, i + 1);
    }
    throw new Error(`unbalanced ${name}`);
}
const keys = source.slice(source.indexOf('const BILLING_ENV_KEYS'), source.indexOf('];', source.indexOf('const BILLING_ENV_KEYS')) + 2);
const helpers = ['stripIncompatibleGoApiTLSArgs', 'ensureMeshEnabledInServiceEnv', 'ensureGoServerEnvironmentFile',
    'ensureGoServerSignalRelayPorts', 'parseEnvFileKeys', 'mergeBillingEnvIntoWindowsServiceExtra', 'syncBillingEnvToWindowsGoServer',
    'shellQuote', 'readTextFilePrivileged', 'writeTextFilePrivileged', 'sanitizeGoServerServiceConfig'].map(grab).join('\n');

// world is what the code sees around it; each vector sets it up.
const world = {};
const make = new Function('world', `
    const fs = new Proxy({}, { get: (_, k) => world.fs[k] });
    const path = require('path');
    const ROOT_DIR = world.root;
    let IS_WINDOWS = world.windows;
    const COMPONENTS = new Proxy({}, { get: (_, k) => world.components[k] });
    const execSync = (cmd, opts) => world.execSync(cmd, opts);
    const execFileSync = (file, args, opts) => world.execFileSync(file, args, opts);
    const runPrivileged = (cmd) => world.runPrivileged(cmd);
    const isAllowedSystemdUnitPath = (p) => ['/etc/systemd/system/betterdesk-server.service', '/etc/systemd/system/betterdesk-console.service'].includes(p);
    const privilegedSystemdUnitHint = () => 'Run once as root: sudo node web-nodejs/scripts/linux-ensure-console-user.js';
    ${keys}
    ${helpers}
    return { stripIncompatibleGoApiTLSArgs, ensureMeshEnabledInServiceEnv, ensureGoServerEnvironmentFile, ensureGoServerSignalRelayPorts,
        parseEnvFileKeys, mergeBillingEnvIntoWindowsServiceExtra, sanitizeGoServerServiceConfig, setWindows: (w) => { IS_WINDOWS = w; } };
`.replace('const path = require(\'path\');', `const path = world.path;`));

const fns = make(Object.assign(world, { path, root: '/opt/console', windows: false, components: {} }));

const units = ['', '[Unit]\nDescription=x\n', '[Service]\nExecStart=/usr/bin/s -tls-api -force-https\n',
    '[Unit]\n[Service]\nEnvironment=AUTH_DB_PATH=/opt/a.db\nExecStart=/usr/bin/s\n', '[Service]\nEnvironment=MESH_ENABLED=N\nEnvironmentFile=/x\n',
    '[Service]\r\nEnvironment=AUTH_DB_PATH=/a\r\nEnvironment=SIGNAL_PORT=1\r\n', 'ExecStart=/s -tls-api\n',
    '[Service]\nEnvironment=HBBS_API_URL=https://localhost:21114/api\nEnvironment=BETTERDESK_API_URL=https://localhost:21114/api\n',
    '[Service]\nEnvironment=AUTH_DB_PATH=/a Environment=GO_API_PORT=9\nExecStart=/s\t-force-https\t-tls-apix -tls-api\n',
    ' [Service]\nEnvironment=AUTH_DB_PATH=/b\n', '[Service]x\nEnvironment=RELAY_PORT=1\n', '[Service]\rEnvironment=AUTH_DB_PATH=/c\rX=1',
    'Environment=MESH_ENABLED=Y\nEnvironment=SIGNAL_PORT=1\nEnvironment=RELAY_PORT=2\nEnvironment=GO_API_PORT=3\nEnvironmentFile=-/e\n',
    '[Service]\nExecStart=/s -tls-api  -other\n   -force-https\n'];
const envPaths = ['/opt/console/.env', '/opt/con$1sole/.env', '/opt/$&/.env', "/o/$'/x", '/o/$$/$`/.env', '/o/$01/$2/.env'];
const argsList = ['', '-mode all -tls-api', '  -mode  all  -tls-api   -force-https  ', '-tls-api', ' -tls-api', '-x -tls-apiX -force-https\t-y',
    '-a -tls-api -b', '-tls-api\r\n', 'a\t\t b'];
const envBlocks = ['', 'A=1', 'A=1\r\nB=2\r\n\r\n', 'MESH_ENABLED=N', 'X=1\nMESH_ENABLED=', '\n\n', 'A=1\n\n',
    'NTP_SERVERS=old\nA=1\nBILLING_TIMEZONE=UTC', ' NTP_SERVERS=old \n=x\nnoeq\nA=1\nA=2'];
const billingVars = [{}, { NTP_SERVERS: 'a.example' }, { NTP_SERVERS: 'old', BILLING_TIMEZONE: 'UTC' },
    { BILLING_TIMEZONE: 'Europe/Berlin', NTP_SERVERS: 'new', BILLING_TRUST_OS_NTP: '1', BILLING_MAX_CLOCK_SKEW_MS: '5', BILLING_REQUIRE_SYNCED_CLOCK: '0' }];
const envFiles = ['', 'NTP_SERVERS=a\nBILLING_TIMEZONE=UTC\nOTHER=1', '#NTP_SERVERS=a\n NTP_SERVERS = b \r\nNTP_SERVERS=c', 'X=1'];

const out = { text: [], flows: [] };
for (const u of units) {
    for (const p of envPaths) out.text.push({ fn: 'ensureGoServerEnvironmentFile', args: [u, p], result: fns.ensureGoServerEnvironmentFile(u, p) });
    out.text.push({ fn: 'ensureGoServerSignalRelayPorts', args: [u], result: fns.ensureGoServerSignalRelayPorts(u) });
    out.text.push({ fn: 'stripIncompatibleGoApiTLSArgs', args: [u, false], result: fns.stripIncompatibleGoApiTLSArgs(u, false) });
}
for (const a of argsList) for (const c of [false, true]) out.text.push({ fn: 'stripIncompatibleGoApiTLSArgs', args: [a, c], result: fns.stripIncompatibleGoApiTLSArgs(a, c) });
for (const e of envBlocks) {
    out.text.push({ fn: 'ensureMeshEnabledInServiceEnv', args: [e], result: fns.ensureMeshEnabledInServiceEnv(e) });
    for (const v of billingVars) out.text.push({ fn: 'mergeBillingEnvIntoWindowsServiceExtra', args: [e, v], result: fns.mergeBillingEnvIntoWindowsServiceExtra(e, v) });
}
for (const f of envFiles) {
    out.text.push({ fn: 'parseEnvFileKeys', args: [f, ['NTP_SERVERS', 'BILLING_TIMEZONE']], result: fns.parseEnvFileKeys(f, ['NTP_SERVERS', 'BILLING_TIMEZONE']) });
}

// The whole check, Linux and Windows, with scripted commands and files
function flow(windows, script) {
    const files = { ...script.files };
    const log = [];
    world.windows = windows;
    fns.setWindows(windows);
    world.components = { server: { service: windows ? 'BetterDeskServer' : 'betterdesk-server' }, console: { service: windows ? 'BetterDeskConsole' : 'betterdesk-console' } };
    world.fs = {
        existsSync: (p) => Object.prototype.hasOwnProperty.call(files, p),
        readFileSync: (p) => {
            if (script.readError && script.readError[p]) throw Object.assign(new Error(script.readError[p]), { code: script.readError[p].split(':')[0] });
            if (!(p in files)) throw Object.assign(new Error(`ENOENT: no such file or directory, open '${p}'`), { code: 'ENOENT' });
            return files[p];
        },
        writeFileSync: (p, data) => {
            log.push({ write: p, data });
            if (script.writeError) throw Object.assign(new Error(script.writeError), { code: script.writeError.split(':')[0] });
            files[p] = data;
        },
    };
    world.execSync = (cmd) => {
        log.push({ shell: cmd });
        const answer = script.shell[cmd];
        if (answer === undefined || answer.fail) throw new Error((answer && answer.fail) || `Command failed: ${cmd}`);
        return Buffer.from(answer);
    };
    world.execFileSync = (file, args) => {
        log.push({ exec: [file, ...args] });
        if (script.execFail) throw new Error(script.execFail);
    };
    world.runPrivileged = (cmd) => {
        log.push({ privileged: cmd });
        if (script.reloadFail) throw new Error(script.reloadFail);
    };
    const result = fns.sanitizeGoServerServiceConfig();
    out.flows.push({ windows, script, result, log, files });
}

const unitPath = '/etc/systemd/system/betterdesk-server.service';
const show = "systemctl show 'betterdesk-server' --property=FragmentPath --value 2>/dev/null || true";
for (const unit of units) {
    flow(false, { shell: { [show]: '' }, files: { [unitPath]: unit } });
}
flow(false, { shell: { [show]: '/lib/systemd/system/betterdesk-server.service\n' }, files: { '/lib/systemd/system/betterdesk-server.service': units[3] } });
flow(false, { shell: { [show]: '' }, files: {} });
flow(false, { shell: { [show]: { fail: 'spawnSync /bin/sh ETIMEDOUT' } }, files: {} });
flow(false, { shell: { [show]: '' }, files: { [unitPath]: units[3] }, readError: { [unitPath]: `EACCES: permission denied, open '${unitPath}'` } });
flow(false, { shell: { [show]: '/x/other.service' }, files: { '/x/other.service': units[3] }, readError: { '/x/other.service': "EACCES: permission denied, open '/x/other.service'" } });
flow(false, { shell: { [show]: '' }, files: { [unitPath]: units[3] }, writeError: `EACCES: permission denied, open '${unitPath}'` });
flow(false, { shell: { [show]: '' }, files: { [unitPath]: units[2] }, reloadFail: 'Privileged update failed' });
flow(false, { shell: { [show]: '' }, files: { [unitPath]: units[12] } });

const get = (svc, what) => `nssm get "${svc}" ${what} 2>nul`;
for (const args of argsList) {
    for (const env of envBlocks.slice(0, 5)) {
        flow(true, {
            shell: { [get('BetterDeskServer', 'AppParameters')]: args, [get('BetterDeskServer', 'AppEnvironmentExtra')]: env, [get('BetterDeskConsole', 'AppEnvironmentExtra')]: 'HBBS_API_URL=https://localhost:1\nBETTERDESK_API_URL=https://localhost:2' },
            files: { '/opt/console/.env': 'NTP_SERVERS=a.example\nBILLING_TIMEZONE=Europe/Berlin\n' },
        });
    }
}
flow(true, { shell: {}, files: {} });
flow(true, { shell: { [get('BetterDeskServer', 'AppParameters')]: '-tls-api' }, files: {}, execFail: 'Command failed: nssm set' });
flow(true, { shell: { [get('BetterDeskServer', 'AppParameters')]: '-a', [get('BetterDeskServer', 'AppEnvironmentExtra')]: 'A=1' }, files: {}, execFail: 'Command failed: nssm set' });
flow(true, { shell: { [get('BetterDeskServer', 'AppParameters')]: '-a', [get('BetterDeskServer', 'AppEnvironmentExtra')]: 'MESH_ENABLED=Y\nNTP_SERVERS=a.example', [get('BetterDeskConsole', 'AppEnvironmentExtra')]: 'X=1' }, files: { '/opt/console/.env': 'NTP_SERVERS=a.example\n' } });

fs.writeFileSync(path.join(__dirname, '..', 'sanitize.json'), JSON.stringify(out) + '\n');
console.log(`sanitize.json: ${out.text.length} text vectors, ${out.flows.length} flows`);
