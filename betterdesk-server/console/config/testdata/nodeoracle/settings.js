// Writes ../settings.json: what web-nodejs/config/config.js makes of the
// environment for the settings the Go console reads the same way. Each
// case loads the real module in a fresh process.
//
//   node settings.js        (in the dev container, from this folder)
'use strict';

const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const CONFIG = '/repo/web-nodejs/config/config.js';
const KEYS = ['goApiPort', 'apiPort', 'apiEnabled', 'hbbsApiUrl', 'betterdeskApiUrl', 'keysPath', 'dbPath',
    'pubKeyPath', 'apiKeyPath', 'wsProxy'];
const READER = `const c = require(${JSON.stringify(CONFIG)});` +
    `const o = {}; for (const k of ${JSON.stringify(KEYS)}) o[k] = c[k];` +
    'process.stdout.write(JSON.stringify(o));';

const urls = [
    'http://127.0.0.1:21114/api', 'http://127.0.0.1:21121/api', 'https://api.example/api', 'https://api.example:8443/api',
    'http://betterdesk-server:21114/api', 'http://host', 'https://host', 'http://host:80/api', 'https://host:443', 'http://host:443',
    'https://host:80', 'http://host:021114/api', 'http://host:0/api', 'http://host:65535', 'http://host:65536', 'http://host:99999',
    'http://host:/api', 'http://host:abc', 'http://host:5:6', 'http://host:+5', 'http://host:-5', 'http://[::1]:5000/api',
    'http://[::1]/api', 'http://user:pw@host:7000/api', 'HTTP://HOST:7001/API', ' http://host:7002/api ', 'http://host:7003/api\n',
    'http://ho\tst:7004/api', 'ws://host', 'wss://host', 'wss://host:9', 'ftp://host', 'ftp://host:21', 'ftp://host:2121',
    'foo://host:80', 'foo://host', 'foo://host:7005/x', 'file:///etc/passwd', 'mailto:a@b', 'not a url', '127.0.0.1:21114/api',
    'localhost:21114/api', '//host:7006', '/api', 'http://', 'http://:7007', 'https://:7008/api', 'http://host:7009?x=1',
    'http://host:7010#f', 'http://ho st:7011', 'http://host:70 12', 'http://example.com:00000000000007013', 'x',
    'http://10.0.0.5:21114', 'http://exa_mple:7014/api', 'https://host:0443/', 'http://host:080/', 'ws://host:80', 'wss://host:443',
];

const cases = [];
const add = (env) => cases.push({ env });
add({});
for (const url of urls) add({ BETTERDESK_API_URL: url });
add({ HBBS_API_URL: 'http://10.1.1.1:7100/api' });
add({ BETTERDESK_API_URL: 'http://10.1.1.2:7101/api', HBBS_API_URL: 'http://10.1.1.1:7100/api' });
for (const port of ['21114', '8080', '0', '-1', '-0', ' 9000', '9001 ', '\t9002', '+9003', '9004abc', 'abc', '1e3', '12.9', '0x10',
    '00017', '99999', '']) {
    add({ GO_API_PORT: port });
    add({ GO_API_PORT: port, BETTERDESK_API_URL: 'https://api.example:8443/api' });
    add({ API_PORT: port });
    add({ WS_HBBS_PORT: port, WS_HBBR_PORT: port });
}
for (const value of ['true', 'TRUE', 'True', 'false', '1', 'yes', ' true', 'true ']) add({ API_ENABLED: value });
add({ WS_HBBS_HOST: 'signal.example', WS_HBBR_HOST: 'relay.example' });
add({ WS_HBBS_PORT: '31116', WS_HBBR_PORT: '41117' });
for (const env of [
    { KEYS_PATH: '/k', RUSTDESK_DIR: '/d', RUSTDESK_PATH: '/p' },
    { RUSTDESK_DIR: '/d', RUSTDESK_PATH: '/p' },
    { RUSTDESK_PATH: '/p' },
    { DOCKER: 'true' },
    { DOCKER: 'true', KEYS_PATH: '/k' },
    { KEYS_PATH: '/k', DB_PATH: '/x/db.sqlite3', PUB_KEY_PATH: '/x/key.pub', API_KEY_PATH: '/x/api' },
    { RUSTDESK_PATH: '/p', PUB_KEY_PATH: '/x/key.pub' },
]) add(env);

for (const c of cases) {
    // NODE_ENV=test silences the warning about a keys folder without keys;
    // SESSION_SECRET keeps the module from writing a secret file.
    const env = { PATH: process.env.PATH, NODE_ENV: 'test', SESSION_SECRET: 'x'.repeat(32), ...c.env };
    c.docker = fs.existsSync('/.dockerenv');
    c.config = JSON.parse(execFileSync(process.execPath, ['-e', READER], { env, encoding: 'utf8' }));
}

fs.writeFileSync(path.join(__dirname, '..', 'settings.json'), JSON.stringify(cases, null, 1) + '\n');
console.log(`${cases.length} cases`);
