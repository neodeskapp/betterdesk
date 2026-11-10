'use strict';

// Records lib/envMerge.js, services/rustDeskPublicEndpointsService.js and
// the resolving functions of services/clientConfigHost.js. Writes
// envmerge.json.gz, endpoints.json.gz and resolve.json.gz.

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const lib = require('./lib');

const R = lib.rng(0x5eed02);

lib.fakeModule('services/betterdeskApi.js', { apiClient: { get: async () => { throw new Error('no server'); } } });

const config = require(lib.web('config/config.js'));
const envMerge = require(lib.web('lib/envMerge.js'));
const endpoints = require(lib.web('services/rustDeskPublicEndpointsService.js'));
const clientConfigHost = require(lib.web('services/clientConfigHost.js'));
const express = require(lib.web('node_modules/express'));

const warn = console.warn;
console.warn = () => {};

// ---------------------------------------------------------------- env text

const KEYS = ['PORT', 'HOST', 'SESSION_SECRET', 'PUBLIC_SERVER_ID', 'PUBLIC_RELAY_SERVER', 'PUBLIC_API_URL', 'PANEL_PUBLIC_HOST',
    'API_TOKEN_EXPIRY_DAYS', 'WEB_UI_DIR', 'RUSTDESK_DIR', 'DB_TYPE', 'A', 'B', 'my_key', 'K\u00c9Y', 'A.B', 'A-B', '1'];
const VALUES = ['', 'x', '5000', 'true', 'a b', ' lead', 'trail ', 'a=b', 'a=b=c', '#c', 'a #c', '$HOME/x', '${VAR}', '"quoted value"',
    "'single'", '"unbalanced', 'https://api.example.com:21121/base?x=1', '/opt/better desk/data', 'C:\\BetterDesk\\data', '\u00e4\u00f6\u00fc',
    '\u4e2d\u6587', '\ud83d\ude00', 'tab\tinside', 'a\u00a0b', 'x\u2028y', '__TOKEN__', '=', '==', ' ', '\t', 'very-long-' + 'x'.repeat(300)];
const INDENTS = ['', '', '', '', ' ', '  ', '\t', '\u00a0', '\ufeff', '\u3000'];
const EOLS = ['\n', '\n', '\n', '\r\n', '\r\n', '\r', '\r\r\n', '\n\n'];

function envLine() {
    const key = R.pick(KEYS);
    const value = R.pick(VALUES);
    switch (R.int(16)) {
    case 0: return '';
    case 1: return R.pick([' ', '\t', '  \t ', '\u00a0', '\ufeff']);
    case 2: return `# ${R.pick(['comment', 'KEY=commented', '==== section ====', key + '=' + value])}`;
    case 3: return `  #${key}=${value}`;
    case 4: return `export ${key}=${value}`;
    case 5: return `${key} = ${value}`;
    case 6: return `${R.pick(INDENTS)}${key}=${value}`;
    case 7: return `${key}${R.pick([' ', '\t', '\u00a0'])}=${value}`;
    case 8: return `=${value}`;
    case 9: return R.pick(['novalue', key, 'just some words', '[section]', 'key: value']);
    case 10: return `${R.pick(INDENTS)}=${value}`;
    case 11: return `${key}=${value} # trailing comment`;
    default: return `${key}=${value}`;
    }
}

function envText() {
    const n = R.int(12);
    const eol = R.chance(0.7) ? R.pick(['\n', '\n', '\r\n']) : null;
    let text = R.chance(0.05) ? '\ufeff' : '';
    for (let i = 0; i < n; i++) {
        text += envLine();
        if (i < n - 1 || R.chance(0.7)) text += eol || R.pick(EOLS);
    }
    return text;
}

const FIXED_TEXTS = [
    '', '\n', '\r\n', '\n\n', ' ', '#', '# only a comment', 'PORT=5000', 'PORT=5000\n', 'PORT=5000\r\n', 'PORT=5000\r', 'PORT=5000\n\n',
    'PORT=5000\nPORT=6000\n', 'PORT=5000\n port=1\n PORT =2\nPORT= 3\n', 'A=1\r\nB=2\r\nC=3\r\n', 'A=1\r\nB=2\nC=3', 'A=1\rB=2\rC=3',
    '# custom\nMY_FEATURE=true\nPORT=5000\nSESSION_SECRET=keep-me\n', 'export PORT=5000\nPORT=1\n', '=x\n==\n =y\n', 'A\nB\nC',
    '\ufeffPORT=5000\nHOST=0.0.0.0\n', 'PORT=5000\u2028HOST=a\n', 'A=1\n\n\n# end', 'A="multi\nline"\n', 'A=1 # c\nB=2', '\u00a0A=1\nA\u00a0=2\n',
    'PUBLIC_SERVER_ID=old.example\nPUBLIC_RELAY_SERVER=\nPUBLIC_API_URL=https://old.example\n',
    '__proto__=x\nA=1\n', 'A=1\n __proto__ = y \nconstructor=c\ntoString=t\n', '__proto__=\nhasOwnProperty=1\n',
];

const UPDATE_KEYS = [...KEYS, 'NEW_KEY', 'NEW', '', ' PORT', 'PORT ', 'A=B', '#X', 'export PORT', 'port', '\u00a0A', 'A\r', 'A\nB', '__proto__', 'constructor'];
const UPDATE_VALUES = [...VALUES, 'multi\nline', 'cr\r', 'crlf\r\nX=1', 'nul\u0000x', '\n', 'end\n'];

function parsed(p) {
    return { entries: p.entries, keys: [...p.map.keys()], values: [...p.map.values()] };
}

function envMergeVectors() {
    const out = {};
    const texts = [...FIXED_TEXTS];
    for (let i = 0; i < 700; i++) texts.push(envText());

    out.parse_env_lines = texts.map((text) => ({ text, out: parsed(envMerge.parseEnvLines(text)) }));
    out.parse_env_file = texts.map((text) => ({ text, out: endpoints.parseEnvFile(text) }));
    out.build_fresh = texts.slice(0, 120).map((text) => ({ text, out: envMerge.buildFreshEnv(text) }));

    out.upsert = [];
    for (const text of texts) {
        const rounds = FIXED_TEXTS.includes(text) ? 8 : 3;
        for (let r = 0; r < rounds; r++) {
            const updates = [];
            let content = text;
            const n = 1 + R.int(4);
            for (let i = 0; i < n; i++) {
                const key = R.pick(UPDATE_KEYS);
                const value = R.pick(UPDATE_VALUES);
                content = envMerge.upsertEnvKey(content, key, value);
                updates.push({ key, value, out: content });
            }
            out.upsert.push({ text, updates });
        }
    }

    out.merge = [];
    for (let i = 0; i < 1500; i++) {
        const existing = R.pick(texts);
        const template = R.pick(texts);
        const merged = envMerge.mergeEnv(existing, template);
        out.merge.push({ existing, template, content: merged.content, added: merged.added });
    }
    {
        const existing = '# custom\nMY_FEATURE=true\nPORT=5000\nSESSION_SECRET=keep-me\n';
        const template = '# Port\nPORT=5000\n# Host to bind\nHOST=0.0.0.0\n\n# Proxy\nTRUST_PROXY=false\n# Secret\nSESSION_SECRET=x\n';
        const merged = envMerge.mergeEnv(existing, template);
        out.merge.push({ existing, template, content: merged.content, added: merged.added });
    }

    // applySubstitutions: the order of the entries is the order JavaScript
    // walks the object in.
    const SUB_KEYS = ['SESSION_SECRET', 'PORT', 'A', 'B', 'DATA_DIR', 'X_Y', '', '_', 'A__B', 'a', '1', '10', '2'];
    const SUB_VALUES = ['', 'v', '__A__', '__B__', 'x__PORT__y', '$1', '$&', 'a\nb', '\u00e4', '____', '__', 'plain'];
    const TEMPLATES = ['PORT=__PORT__\nSESSION_SECRET=__SESSION_SECRET__\n', '__A____B__', '__A__ __B__ __A__', '______', '__a__ __A__', '____A____',
        'no tokens', '', '__1__ __10__ __2__', '__X_Y__/__DATA_DIR__/x', '__A__B__', '_____A_____'];
    out.substitute = [];
    for (let i = 0; i < 600; i++) {
        const subs = {};
        const n = R.int(5);
        for (let j = 0; j < n; j++) subs[R.pick(SUB_KEYS)] = R.pick(SUB_VALUES);
        const text = R.chance(0.5) ? R.pick(TEMPLATES) : R.text(['__', '_', 'A', 'B', 'PORT', 'SESSION_SECRET', ' ', '\n', '=', '1', '10'], 0, 12);
        out.substitute.push({ text, subs: Object.entries(subs), out: envMerge.applySubstitutions(text, subs) });
    }
    out.assert_resolved = [];
    for (let i = 0; i < 300; i++) {
        const subs = {};
        const n = R.int(4);
        for (let j = 0; j < n; j++) subs[R.pick(SUB_KEYS)] = R.pick([...SUB_VALUES, '__X__', 'a__b_c__d', '__a\nb__', '___x___', '__ __']);
        out.assert_resolved.push({ subs: Object.entries(subs), ...lib.caught(() => { envMerge.assertResolvedSubstitutions(subs); return null; }) });
    }

    // buildEnvSubstitutions: the random bytes of the vault key are fixed.
    const realRandom = crypto.randomBytes;
    out.build_substitutions = [];
    const CONFIG_VALUES = {
        rustdeskDir: [undefined, '', '/opt/betterdesk', 'C:\\BetterDesk'],
        keysPath: [undefined, '/opt/rustdesk'],
        goApiPort: [undefined, 21114, 0, '21115'],
        apiPort: [undefined, 21121, 8443],
        dbType: [undefined, 'sqlite', 'postgres'],
        sslCertPath: [undefined, '', '/etc/ssl/bd.crt'],
        sslKeyPath: [undefined, '', '/etc/ssl/bd.key'],
        remoteTargetVaultKey: [undefined, '', 'vault-key-from-config'],
        pubKeyPath: [undefined, '/keys/id_ed25519.pub'],
        apiKeyPath: [undefined, '/keys/.api_key'],
        dbPath: [undefined, '/data/db.sqlite3'],
        databaseUrl: [undefined, '', 'postgres://u@h/db'],
        dataDir: [undefined, '/app/data'],
        sessionSecret: [undefined, null, 'secret-from-config'],
    };
    const ENV_FOR_SUBS = ['', 'PORT=5000\nRUSTDESK_DIR=/opt/custom\nSESSION_SECRET=keep-me\n', 'RUSTDESK_DIR=\nGO_API_PORT=31114\nAPI_PORT=\n',
        'REMOTE_TARGET_VAULT_KEY=existing-vault-key\nDB_TYPE=postgres\nDATABASE_URL=postgres://x\n', 'SSL_CERT_PATH= /spaced.crt\nGUACD_ADDRESS=guacd:4822\n',
        'HBBS_API_URL=http://go:21114/api\r\nBETTERDESK_API_URL=https://go/api\r\nDATA_DIR=/data\r\n', 'DEFAULT_ADMIN_PASSWORD=__DEFAULT_ADMIN_PASSWORD__\nORG_PEER_VAULT_KEY=k\n',
        'PUB_KEY_PATH=/p.pub\nAPI_KEY_PATH=/k\nDB_PATH=/d\nSSL_KEY_PATH=/s.key\nSESSION_SECRET=\n'];
    for (let i = 0; i < 400; i++) {
        const cfg = {};
        for (const [key, values] of Object.entries(CONFIG_VALUES)) {
            const v = R.pick(values);
            if (v !== undefined) cfg[key] = v;
        }
        const existingContent = R.chance(0.7) ? R.pick(ENV_FOR_SUBS) : envText();
        const seed = R.int(256);
        crypto.randomBytes = (n) => Buffer.alloc(n, seed);
        const useMap = R.chance(0.2);
        const opts = useMap ? { envMap: envMerge.parseEnvLines(existingContent).map, config: cfg } : { existingContent, config: cfg };
        const subs = envMerge.buildEnvSubstitutions(opts);
        const cfgText = {};
        for (const [key, v] of Object.entries(cfg)) cfgText[key] = v === null ? '' : String(v);
        out.build_substitutions.push({ existingContent, useMap, config: cfgText, randomByte: seed, out: Object.entries(subs) });
    }
    crypto.randomBytes = realRandom;

    // mergeEnvFile on real files.
    out.merge_file = [];
    const dir = path.join(lib.SCRATCH, 'mergefile');
    fs.mkdirSync(dir, { recursive: true });
    const targetStates = [null, '', ' \n\t\n', '\ufeff', ...FIXED_TEXTS.slice(7, 20)];
    for (let i = 0; i < 400; i++) {
        const target = path.join(dir, `target-${i}.env`);
        const template = path.join(dir, `template-${i}.env`);
        const templateText = R.chance(0.1) ? null : (R.chance(0.5)
            ? 'PORT=__PORT__\n# secret\nSESSION_SECRET=__SESSION_SECRET__\nHOST=0.0.0.0\nDATA_DIR=__DATA_DIR__\n'
            : envText());
        const targetText = R.chance(0.5) ? R.pick(targetStates) : envText();
        if (templateText !== null) fs.writeFileSync(template, templateText);
        if (targetText !== null) fs.writeFileSync(target, targetText, { mode: 0o644 });
        const subs = {};
        if (R.chance(0.8)) subs.PORT = R.pick(['5000', '', '__PORT__', '_x_']);
        if (R.chance(0.8)) subs.SESSION_SECRET = R.pick(['s3cret', '__A__', 'a__b__c__d', '']);
        if (R.chance(0.5)) subs.DATA_DIR = R.pick(['/app/data', 'C:\\data']);
        const freshInstall = R.chance(0.25);
        const result = lib.caught(() => envMerge.mergeEnvFile({ targetPath: target, templatePath: template, freshInstall, substitutions: subs }));
        if (result.err) result.err = result.err.split(dir).join('{DIR}');
        const exists = fs.existsSync(target);
        out.merge_file.push({
            template: templateText, target: targetText, subs: Object.entries(subs), freshInstall, ...result,
            after: exists ? fs.readFileSync(target).toString('base64') : null,
            mode: exists ? fs.statSync(target).mode & 0o777 : null,
            name: `target-${i}.env`,
            templateName: `template-${i}.env`,
        });
    }
    return out;
}

// ------------------------------------------------------- public endpoints

function setEnv(env) {
    for (const key of ['PUBLIC_SERVER_ID', 'PUBLIC_RELAY_SERVER', 'PUBLIC_API_URL', 'PANEL_PUBLIC_HOST']) {
        if (env && env[key] !== undefined) process.env[key] = env[key];
        else delete process.env[key];
    }
}

function getEnv() {
    const out = {};
    for (const key of ['PUBLIC_SERVER_ID', 'PUBLIC_RELAY_SERVER', 'PUBLIC_API_URL', 'PANEL_PUBLIC_HOST']) {
        if (process.env[key] !== undefined) out[key] = process.env[key];
    }
    return out;
}

let caseNo = 0;

// stage lays out the two env files of one case and points the service at
// them. null means the file does not exist; {dir: true} puts a directory in
// its place.
function stage(durable, legacy, options = {}) {
    const root = path.join(lib.SCRATCH, `ep-${caseNo++}`);
    const dataDir = options.noDataDir ? path.join(root, 'missing', 'data') : path.join(root, 'data');
    if (!options.noDataDir) fs.mkdirSync(dataDir, { recursive: true });
    else fs.mkdirSync(root, { recursive: true });
    const envPath = path.join(root, '.env');
    const durablePath = path.join(dataDir, endpoints.DURABLE_BASENAME);
    const put = (file, content) => {
        if (content === null || content === undefined) return;
        if (content.dir) fs.mkdirSync(file, { recursive: true });
        else fs.writeFileSync(file, content, { mode: 0o644 });
    };
    put(durablePath, durable);
    put(envPath, legacy);
    endpoints._setPathsForTests({ envPath, durablePath, dataDir });
    return { root, dataDir, envPath, durablePath };
}

function snapshot(paths) {
    const read = (file) => {
        try {
            const st = fs.statSync(file);
            if (st.isDirectory()) return { dir: true };
            return { bytes: fs.readFileSync(file).toString('base64'), mode: st.mode & 0o777 };
        } catch (_) {
            return null;
        }
    };
    const list = (dir) => {
        try {
            return fs.readdirSync(dir).sort();
        } catch (_) {
            return null;
        }
    };
    return { durable: read(paths.durablePath), legacy: read(paths.envPath), dataDirFiles: list(paths.dataDir), rootFiles: list(paths.root) };
}

const HOST_VALUES = ['', 'remote.example.com', 'relay.example.com', 'Remote.Example.COM', ' spaced.example ', 'https://desk.example.com:8443/x',
    '203.0.113.10', '[2001:db8::1]', '::1', 'not valid host!!', 'm\u00fcnchen.de', 'localhost', 'a'.repeat(253), 'a'.repeat(260),
    'evil.example.com\nSESSION_SECRET=hacked', 'evil.example\r', 'x\u0000y', '\n', ' \n ', 'tab\there', 'a_b', 'desk.example.com.', '-x', 'x-',
    'desk.example.com:21116', 'user@desk.example.com', 'desk.example.com/path'];
const HOST_JSON = [null, false, true, 0, 1, 123, 1.5, -1, 1e21, [], ['a.example'], ['a', 'b'], [null], {}, { host: 'x' }, [[['deep.example']]]];
const API_VALUES = ['', 'https://api.example.com', 'https://api.example.com/', 'https://api.example.com//', 'http://api.example.com:21114',
    'https://api.example.com:443', 'http://api.example.com:80/', 'https://api.example.com:21121/base/', 'https://api.example.com/a/b/?x=1&y=2',
    'https://api.example.com/?', 'https://api.example.com/#frag', 'https://API.Example.COM/Path', 'HTTPS://api.example.com', 'hTTp://api.example.com',
    ' https://api.example.com ', 'https://user:pw@api.example.com/', 'https://[2001:db8::1]:21114/', 'https://203.0.113.10', 'http://0x7f.1/',
    'ftp://bad', 'api.example.com', '//api.example.com', 'https:/api.example.com', 'https:api.example.com', 'https://', 'https:// space.example',
    'https://api.example.com\nOTHER=1', 'https://api.example.com\r', 'https://api.exa\tmple.com', 'https://api.example.com/a b',
    'https://b\u00fccher.example', 'https://B\u00dcCHER.example/\u00e4?\u00f6=\u00fc', 'https://\u65e5\u672c\u8a9e.jp/api', 'https://xn--bcher-kva.example',
    'https://xn--a.example', 'https://fa\u00df.de', 'https://\u2603.net/', 'https://api.example.com:99999', 'https://api.example.com:x',
    'https://api.example.com/a/../b/./c/', 'https://api.example.com/%2e%2e/a', 'https://api.example.com\\a\\b', 'https://a%20b.example',
    'https://ex%41mple.com', 'http://http://nested', 'https://api.example.com/x\u0000y', 'javascript://api.example.com/%0aalert(1)',
    'https://api.example.com/' + 'p/'.repeat(40), 'https://api.example.com/////', 'https://api.example.com/?q=/', 'https://1.2.3.4.5/', 'https://256.1.1.1/'];

function endpointVectors() {
    const out = {};

    // normalizeSettings and validateSettings on every kind of value.
    out.normalize = [];
    const settingsCase = (raw) => ({
        raw, normalize: lib.caught(() => endpoints.normalizeSettings(raw)), validate: lib.caught(() => endpoints.validateSettings(raw)),
    });
    for (const v of HOST_VALUES) {
        out.normalize.push(settingsCase({ public_server_id: v }));
        out.normalize.push(settingsCase({ public_relay_server: v }));
    }
    for (const v of HOST_JSON) {
        out.normalize.push(settingsCase({ public_server_id: v }));
        out.normalize.push(settingsCase({ public_relay_server: v }));
        out.normalize.push(settingsCase({ public_api_url: v }));
    }
    for (const v of API_VALUES) out.normalize.push(settingsCase({ public_api_url: v }));
    out.normalize.push(settingsCase({}));
    for (let i = 0; i < 1200; i++) {
        const raw = {};
        if (R.chance(0.8)) raw.public_server_id = R.chance(0.9) ? R.pick(HOST_VALUES) : R.pick(HOST_JSON);
        if (R.chance(0.8)) raw.public_relay_server = R.chance(0.9) ? R.pick(HOST_VALUES) : R.pick(HOST_JSON);
        if (R.chance(0.8)) raw.public_api_url = R.chance(0.9) ? R.pick(API_VALUES) : R.pick(HOST_JSON);
        out.normalize.push(settingsCase(raw));
    }

    out.override_active = [];
    for (const a of ['', 'x']) for (const b of ['', 'y']) for (const c of ['', 'z']) {
        const env = { public_server_id: a, public_relay_server: b, public_api_url: c };
        out.override_active.push({ env, out: endpoints.isEnvOverrideActive(env) });
    }

    // Reading: environment, durable file, console env file, and the one-time
    // copy into the durable file.
    const FILE_TEXTS = [null, '', 'PORT=5000\n', 'PUBLIC_SERVER_ID=from-durable.example\n', 'PUBLIC_SERVER_ID=from-legacy.example\nPORT=5000\n',
        'PUBLIC_SERVER_ID=\nPUBLIC_RELAY_SERVER=relay.file.example\nPUBLIC_API_URL=https://api.file.example\n',
        'PANEL_PUBLIC_HOST=panel.file.example\n', 'PUBLIC_SERVER_ID = spaced.example \r\nPANEL_PUBLIC_HOST=\tpanel.crlf.example\r\n',
        '# PUBLIC_SERVER_ID=commented.example\nexport PUBLIC_SERVER_ID=exported.example\n', 'PUBLIC_SERVER_ID=first.example\nPUBLIC_SERVER_ID=last.example\n',
        'PUBLIC_SERVER_ID=a\u0000b\nPUBLIC_RELAY_SERVER=ok.example\n', '\ufeffPUBLIC_SERVER_ID=bom.example\n', 'PUBLIC_API_URL=https://api.example.com/?a=b#c\n',
        'PUBLIC_SERVER_ID="quoted.example"\n', { dir: true }];
    const ENVS = [{}, { PUBLIC_SERVER_ID: 'from-compose.example' }, { PUBLIC_SERVER_ID: '' }, { PUBLIC_SERVER_ID: '   ' },
        { PUBLIC_SERVER_ID: ' padded.example ', PUBLIC_RELAY_SERVER: 'relay.env.example', PUBLIC_API_URL: 'https://api.env.example' },
        { PANEL_PUBLIC_HOST: 'compose-panel.example' }, { PUBLIC_API_URL: 'https://api.env.example' }, { PUBLIC_RELAY_SERVER: 'two\nlines' },
        { PUBLIC_SERVER_ID: 'https://not-normalized.example:1/x' }, { PANEL_PUBLIC_HOST: 'not a host' }];
    out.read = [];
    const readCase = (durable, legacy, env, options) => {
        if (options && options.noDataDir) durable = null;
        const paths = stage(durable, legacy, options);
        setEnv(env);
        const first = endpoints.readPublicEndpointEnv();
        const panel = endpoints.readPanelPublicHostValue();
        const settings = endpoints.getPublicEndpointSettings();
        const after = snapshot(paths);
        // A second read in the same process does not copy again.
        fs.rmSync(paths.durablePath, { recursive: true, force: true });
        const second = endpoints.readPublicEndpointEnv();
        const afterSecond = snapshot(paths);
        return { durable, legacy, env, options: options || {}, first, panel, settings, after, second, afterSecond };
    };
    for (const durable of FILE_TEXTS) for (const legacy of FILE_TEXTS) out.read.push(readCase(durable, legacy, {}));
    for (const env of ENVS) for (const durable of FILE_TEXTS.slice(0, 8)) out.read.push(readCase(durable, R.pick(FILE_TEXTS), env));
    for (let i = 0; i < 400; i++) out.read.push(readCase(R.pick(FILE_TEXTS), R.pick(FILE_TEXTS), R.pick(ENVS), R.chance(0.1) ? { noDataDir: true } : undefined));

    // Writing.
    out.write = [];
    const EXISTING = [null, '', 'PORT=5000\n', 'PORT=5000', '# Console settings\r\nPORT=5000\r\nPUBLIC_SERVER_ID=old.example\r\n',
        'PUBLIC_SERVER_ID=old.example\nPUBLIC_RELAY_SERVER=old-relay.example\nPUBLIC_API_URL=https://old.example\nPANEL_PUBLIC_HOST=panel.example\n',
        '  PUBLIC_SERVER_ID = indented.example\n# PUBLIC_API_URL=commented\nexport PUBLIC_RELAY_SERVER=exported.example\n',
        'PUBLIC_SERVER_ID=dup1.example\nX=1\nPUBLIC_SERVER_ID=dup2.example\n', 'SESSION_SECRET=abc\n\n\n# trailing comment', '\ufeffA=1\n', { dir: true }];
    const writeCase = (durable, legacy, env, raw, options) => {
        if (options && options.noDataDir) durable = null;
        const paths = stage(durable, legacy, options);
        setEnv(env);
        const result = lib.caught(() => endpoints.writePublicEndpointSettingsToEnv(raw));
        if (result.err) result.err = result.err.split(paths.root).join('{ROOT}');
        const read = endpoints.readPublicEndpointEnv();
        return { durable, legacy, env, raw, options: options || {}, ...result, envAfter: getEnv(), after: snapshot(paths), read };
    };
    const GOOD = [
        { public_server_id: 'gateway.example.net', public_relay_server: 'gateway.example.net', public_api_url: 'https://api.example.net:21121' },
        { public_server_id: '', public_relay_server: '', public_api_url: '' },
        { public_server_id: 'https://Remote.example.com:8443/x', public_relay_server: ' relay.example.com ', public_api_url: 'https://api.example.com/base/' },
        { public_server_id: 'only-id.example' }, { public_api_url: 'http://203.0.113.10:21114/' }, {},
        { public_server_id: 'not valid!!' }, { public_api_url: 'ftp://bad' }, { public_relay_server: 'a\nb' }, { public_server_id: 123, public_relay_server: ['r.example'] },
    ];
    for (const durable of EXISTING) for (const legacy of EXISTING) for (const raw of GOOD.slice(0, 4)) out.write.push(writeCase(durable, legacy, R.pick(ENVS), raw));
    for (let i = 0; i < 500; i++) {
        const raw = R.chance(0.6) ? R.pick(GOOD) : {
            public_server_id: R.pick(HOST_VALUES), public_relay_server: R.pick(HOST_VALUES), public_api_url: R.pick(API_VALUES),
        };
        out.write.push(writeCase(R.chance(0.5) ? R.pick(EXISTING) : envText(), R.chance(0.5) ? R.pick(EXISTING) : envText(), R.pick(ENVS), raw,
            R.chance(0.1) ? { noDataDir: true } : undefined));
    }
    return out;
}

// ------------------------------------------------------------ resolving

function makeReq(headers, trust) {
    if (headers === null) return undefined;
    const app = express();
    app.set('trust proxy', trust);
    const req = Object.create(express.request);
    req.headers = headers;
    req.app = app;
    req.connection = { remoteAddress: '127.0.0.1' };
    req.socket = req.connection;
    return req;
}

function resolveVectors() {
    const out = { resolve: [] };
    const API_URLS = ['http://127.0.0.1:21114/api', 'https://betterdesk-server:21114/api', 'not a url', 'https://desk.example.com/api', '', 'http://[::1]:21114/api'];
    const PORTS = [[21114, 21121], [443, 21121], [80, 21121], [0, 0]];
    const XFH = [undefined, '', 'public.example.com', 'public.example.com:8443', 'first.example, second.example', '[2001:db8::1]:443', ' padded.example ',
        'a:b:c', ',x', 'PUBLIC.Example.com'];
    const HOSTHDR = [undefined, '', 'localhost:5000', 'console.example.com', '192.168.1.5:5000', '[::1]:5000', '[::1]', '::1', 'console.example.com:', ':5000',
        '[broken', 'm\u00fcnchen.de:5000'];
    const QUERY = ['', '', '203.0.113.10', 'query.example.com', 'https://query.example.com:1/x', 'not valid!!', ' ', '[::1]', 'a'.repeat(254)];
    const PANEL = [undefined, undefined, 'desk.example.com', 'panel.internal', 'not a host', 'https://panel.example.com:5443/', ''];
    const PUBLIC = [{}, {}, {}, { PUBLIC_SERVER_ID: 'remote.example.com' },
        { PUBLIC_SERVER_ID: 'remote.example.com', PUBLIC_RELAY_SERVER: 'relay.example.com', PUBLIC_API_URL: 'https://api.example.com' },
        { PUBLIC_RELAY_SERVER: 'relay.example.com' }, { PUBLIC_API_URL: 'https://api.example.com' }, { PUBLIC_SERVER_ID: ' https://raw.example:1/x ' },
        { PUBLIC_SERVER_ID: '', PUBLIC_RELAY_SERVER: '  ' }];
    const FILES = [null, null, null, 'PUBLIC_SERVER_ID=durable.example\n', 'PANEL_PUBLIC_HOST=durable-panel.example\n', 'PUBLIC_API_URL=https://durable-api.example\n',
        'PUBLIC_RELAY_SERVER=durable-relay.example\nPANEL_PUBLIC_HOST=not a host\n'];

    const one = (cfg, env, durable, legacy, headers, trust, queryHost) => {
        config.hbbsApiUrl = cfg.apiUrl;
        config.betterdeskApiUrl = cfg.apiUrl;
        config.goApiPort = cfg.goApiPort;
        config.apiPort = cfg.apiPort;
        const paths = stage(durable, legacy);
        setEnv(env);
        const req = makeReq(headers, trust);
        const endpointsOut = clientConfigHost.resolveRustDeskEndpoints(req, queryHost);
        const host = clientConfigHost.resolveClientFacingHost(req, queryHost);
        const noReq = clientConfigHost.resolveRustDeskEndpoints(undefined, queryHost);
        const panel = clientConfigHost.readPanelPublicHost();
        return { cfg, env, durable, legacy, headers, trust, queryHost, endpoints: endpointsOut, host, noReq, panel, after: snapshot(paths) };
    };
    const headersOf = (xfh, host) => {
        const h = {};
        if (xfh !== undefined) h['x-forwarded-host'] = xfh;
        if (host !== undefined) h.host = host;
        return h;
    };
    // The request only counts when the API address has no host: every pair
    // of headers there, with and without trust in the proxy.
    for (const apiUrl of ['not a url', '']) {
        for (const xfh of XFH) for (const host of HOSTHDR) for (const trust of [false, true]) {
            out.resolve.push(one({ apiUrl, goApiPort: 21114, apiPort: 21121 }, {}, null, null, headersOf(xfh, host), trust, ''));
        }
    }
    for (let i = 0; i < 3000; i++) {
        const [goApiPort, apiPort] = R.pick(PORTS);
        const env = { ...R.pick(PUBLIC) };
        const panel = R.pick(PANEL);
        if (panel !== undefined) env.PANEL_PUBLIC_HOST = panel;
        const headers = R.chance(0.1) ? null : headersOf(R.pick(XFH), R.pick(HOSTHDR));
        out.resolve.push(one({ apiUrl: R.pick(API_URLS), goApiPort, apiPort }, env, R.pick(FILES), R.pick(FILES), headers, R.chance(0.5), R.pick(QUERY)));
    }
    return out;
}

lib.write('envmerge', envMergeVectors());
lib.write('endpoints', endpointVectors());
lib.write('resolve', resolveVectors());
endpoints._setPathsForTests(null);
setEnv({});
console.warn = warn;
lib.writeCounts('envfiles.js');
