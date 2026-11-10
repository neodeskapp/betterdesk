'use strict';

// Records services/keyService.js, the host helpers of
// services/agentBundleConnection.js and services/clientConfigHost.js, and
// what "new URL" makes of an address. Writes keys.json.gz, hosts.json.gz
// and urls.json.gz.

const fs = require('fs');
const path = require('path');
const lib = require('./lib');

const R = lib.rng(0x5eed01);

// The Go API client and the QR module are replaced: the script decides
// what the server answers and records what would be drawn.
const goApi = { answer: null, calls: [] };
lib.fakeModule('services/betterdeskApi.js', {
    apiClient: {
        get: async (url, opts) => {
            goApi.calls.push({ url, timeout: opts && opts.timeout });
            const a = goApi.answer;
            if (!a || a.fail) throw new Error(`Request failed with status code ${a ? a.status : 500}`);
            return { data: a.data };
        },
    },
});

const config = require(lib.web('config/config.js'));
const QRCode = require(lib.web('node_modules/qrcode'));
const keyService = require(lib.web('services/keyService.js'));
const conn = require(lib.web('services/agentBundleConnection.js'));
const clientConfigHost = require(lib.web('services/clientConfigHost.js'));

const qr = { fail: false, calls: [] };
QRCode.toDataURL = async (text, options) => {
    qr.calls.push({ text, options });
    if (qr.fail) throw new Error('The amount of data is too big to be stored in a QR Code');
    return `QR:${text}`;
};

const warn = console.warn;
console.warn = () => {};

function setConfig(cfg) {
    config.hbbsApiUrl = cfg.apiUrl;
    config.betterdeskApiUrl = cfg.apiUrl;
    config.goApiPort = cfg.goApiPort;
    config.apiPort = cfg.apiPort;
}

const CONFIGS = [
    { apiUrl: 'http://127.0.0.1:21114/api', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'https://desk.example.com/api', goApiPort: 443, apiPort: 21121 },
    { apiUrl: 'http://localhost/api', goApiPort: 80, apiPort: 21121 },
    { apiUrl: 'https://betterdesk-server:8443/api', goApiPort: 8443, apiPort: 21121 },
    { apiUrl: 'http://127.0.0.1:21114/api', goApiPort: 0, apiPort: 21121 },
    { apiUrl: 'http://127.0.0.1:21114/api', goApiPort: 0, apiPort: 0 },
    { apiUrl: 'https://127.0.0.1:21114/api', goApiPort: 80, apiPort: 21121 },
    { apiUrl: 'http://127.0.0.1:21114/api', goApiPort: 443, apiPort: 21121 },
    { apiUrl: 'betterdesk-server:21114/api', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'HTTPS://[::1]:21114/api', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'not a url', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: '', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'https://', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'ftp://files.example.com/api', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'https://b\u00fccher.example:21114/api', goApiPort: 21114, apiPort: 21121 },
    { apiUrl: 'http://user:pw@Desk.Example.com:21114/api', goApiPort: 21114, apiPort: 21121 },
];

const key32 = (fill) => Buffer.alloc(32, fill).toString('base64');
const randomKey = () => {
    const b = Buffer.alloc(32);
    for (let i = 0; i < 32; i++) b[i] = R.int(256);
    return b.toString('base64');
};
const VALID_KEY = key32(0);
const bare = (k) => k.replace(/=+$/, '');

const KEYS = [
    VALID_KEY, bare(VALID_KEY), VALID_KEY + '=', VALID_KEY + '==', bare(VALID_KEY) + '=====',
    key32(7), key32(255), bare(key32(255)), randomKey(), randomKey(), bare(randomKey()),
    ` ${VALID_KEY} `, `\n${key32(7)}\n`, `\t${key32(7)}\r\n`, `\ufeff${key32(7)}`, `${key32(7)}\u00a0`, `\u2028${key32(7)}\u2029`,
    `${VALID_KEY.slice(0, 10)} ${VALID_KEY.slice(10)}`, `${VALID_KEY.slice(0, 10)}\n${VALID_KEY.slice(10)}`,
    `${VALID_KEY.slice(0, 10)}\u00a0${VALID_KEY.slice(10)}`, `${VALID_KEY.slice(0, 10)}=${VALID_KEY.slice(10)}`,
    '', ' ', '\n', '=', '====',
    Buffer.alloc(16).toString('base64'), Buffer.alloc(31, 1).toString('base64'), Buffer.alloc(33, 1).toString('base64'),
    Buffer.alloc(64, 1).toString('base64'),
    bare(key32(255)).replace(/\//g, '_').replace(/\+/g, '-'), bare(key32(251)).replace(/\+/g, '-'),
    bare(VALID_KEY).slice(0, 42) + 'B', bare(VALID_KEY).slice(0, 42) + 'Q', bare(VALID_KEY).slice(0, 42) + 'g',
    bare(VALID_KEY).slice(0, 42) + '/', bare(VALID_KEY) + 'A', bare(VALID_KEY).slice(0, 42),
    bare(VALID_KEY).slice(0, 20) + '!' + bare(VALID_KEY).slice(20), bare(VALID_KEY).slice(0, 20) + '!' + bare(VALID_KEY).slice(21),
    'placeholder' + 'A'.repeat(32), 'PLACEHOLDER' + 'A'.repeat(32), 'PlaceHolder' + 'B'.repeat(31) + 'A',
    'A'.repeat(16) + 'placeholder' + 'A'.repeat(16), 'pla\u017feholder' + 'A'.repeat(32),
    'v1.4.9_public_key_placeholder...', 'YOUR_PUBLIC_KEY', 'your-public-key', 'YourPublicKey', 'YOUR__PUBLIC_KEY',
    ' YOUR_PUBLIC_KEY ', 'YOUR_PUBLIC_KEY\n', 'XYOUR_PUBLIC_KEY', '__PUB_KEY_PATH__', 'x__TOKEN__y', '____', '__a b__',
    '__\u00a0__', 'not-base64!!!', 'null', 'undefined', '0', 'AAAA', '\u00e4'.repeat(43), '\ud83d\ude00'.repeat(22),
    VALID_KEY.toLowerCase(), key32(7).toUpperCase(),
];

const HOSTS = [
    '', ' ', 'localhost', 'LOCALHOST', 'desk.example.com', 'Desk.Example.COM', 'a', 'A', '1', 'a-b.c', '-a.com', 'a-.com',
    'a_b.com', 'example.com.', '.example.com', 'ex ample.com', 'a..b', 'a.-.b', 'a.b-', '-', '.', '..',
    '203.0.113.10', '999.1.1.1', '1.2.3', '127.0.0.1', '0x7f.1', '1.2.3.4.5', '010.1.1.1',
    '::1', '[::1]', '[::1]:5000', '2001:db8::1', '[2001:db8::1]', '[2001:db8::1]:21114', 'fe80::1%eth0', '[fe80::1%25eth0]',
    '::ffff:1.2.3.4', '[::ffff:1.2.3.4]:80',
    'https://desk.example.com:8443/path', 'http://desk.example.com', 'HTTP://Desk.example.com/', 'HtTpS://x.y:1/z?q#f',
    'ftp://x.y', 'https://', 'http://', 'http:///x', 'http://http://a.b', 'https://[::1]:8443/', 'ws://x.y', '//x.y/z',
    'http:/x.y', 'http:x.y', 'httpx://a.b', 'https://user:pw@host.example:1/p',
    'desk.example.com:21116', 'desk.example.com:', ':21116', 'desk.example.com/path', 'desk.example.com?x=1', 'desk.example.com#f',
    'user@host', 'user:pw@host.example', 'a:b:c',
    'm\u00fcnchen.de', 'b\u00fccher.example', 'xn--mnchen-3ya.de', '\u65e5\u672c\u8a9e.jp', '\u043f\u0440\u0438\u043c\u0435\u0440.\u0440\u0444',
    '\u2603.net', '\uff45\uff58\uff41\uff4d\uff50\uff4c\uff45\uff0e\uff43\uff4f\uff4d', 'exa\u00admple.com', 'a\u200db.com',
    '\u0130stanbul.local', 'desk.loca\u212a', 'DESK.\u017fOCAL', 'e\u0301.example', '\ud83d\ude00.example', '\u05d0\u05d1.example',
    '  desk.example.com  ', '\tdesk.example.com\n', '\ufeffdesk.example.com', 'desk.example.com\u00a0', 'desk\u2028.com',
    '\u3000desk.example.com\u3000', ' https://desk.example.com ', 'https:// desk.example.com',
    'a'.repeat(253), 'a'.repeat(254), 'a'.repeat(255), 'a'.repeat(256), 'a'.repeat(300),
    ('a'.repeat(63) + '.').repeat(4), 'x'.repeat(252) + ' y', 'x'.repeat(252) + '.y', 'x'.repeat(253) + '!',
    ' ' + 'x'.repeat(253) + ' ', '\ud83d\ude00'.repeat(126) + 'a', 'a' + '\ud83d\ude00'.repeat(127),
    'https://' + 'h'.repeat(250), 'h'.repeat(250) + ':21116', 'h'.repeat(250) + '/path',
    '[', ']', '[]', '[a]', 'a]', '[a', '[[a]]', ']a[', 'a b', 'a,b', 'host;rm -rf /', '"quoted"', "o'k", 'a\\b', '<script>',
    'a%20b', 'a%2eb', 'ho\u0000st', 'ho\u0001st', 'ho\u007fst', 'null', 'undefined', '0', 'false', 'true', 'NaN', '[object Object]',
    'desk.local', 'DESK.LOCAL', 'x.local.', 'local', '.local', 'desk.localhost', 'localhost.', '127.0.0.2', '::1 ', ' [::1] ',
    'a"b', 'a\nb', 'a\rb', 'a\tb', 'a\u2028b', 'tab\tinside.example', 'line\nbreak.example', '\\\\server\\share', 'C:\\path',
    'desk.example.com,other.example.com', 'desk.example.com, other.example.com:8080', ',desk.example.com', ' , ',
    '[::1], desk.example.com', '[::1', '[]:80', 'a:1:2', 'a:', ':', '::', '[:]',
];
// One host with every control character in it, for the escaping in the
// config string.
HOSTS.push('a' + Array.from({ length: 33 }, (_, i) => String.fromCharCode(i < 32 ? i : 127)).join('') + 'z');

const HOST_PIECES = [
    'a', 'b', 'Z', '0', '9', '-', '.', '.', '_', ':', '/', '[', ']', ' ', '\t', '@', '?', '#', '%', '\u00e9', '\u00fc', '\u4e2d',
    '\ud83d\ude00', '\u00a0', '\u2028', '\ufeff', 'http://', 'https://', 'HTTPS://', 'example', 'com', 'local', 'localhost',
    '127.0.0.1', '::1', ':21116', '\\', '"', ',',
];

function randomHost() {
    if (R.chance(0.5)) {
        const labels = [];
        const n = 1 + R.int(4);
        for (let i = 0; i < n; i++) labels.push(R.text(['a', 'b', 'c', 'X', 'Y', '1', '2', '-', '_'], 1, 8));
        let h = labels.join('.');
        if (R.chance(0.2)) h = R.pick(['http://', 'https://', 'HTTP://', 'ftp://']) + h;
        if (R.chance(0.2)) h += ':' + R.pick(['21116', '443', '', 'x', '65536']);
        if (R.chance(0.15)) h += R.pick(['/', '/path', '/a/b?c=d', '?q', '#f']);
        if (R.chance(0.1)) h = R.pick([' ', '\t', '\n', '\u00a0']) + h + R.pick([' ', '\r\n', '\ufeff']);
        if (R.chance(0.1)) h += '.';
        return h;
    }
    return R.text(HOST_PIECES, 0, 8);
}

async function main() {
    const keys = {};
    const hosts = {};

    // --- isValidRustDeskPublicKey
    const keyInputs = [...KEYS];
    for (let i = 0; i < 400; i++) {
        let k = bare(randomKey());
        const how = R.int(8);
        if (how === 1) k += '='.repeat(R.int(5));
        if (how === 2) k = k.slice(0, 42) + R.pick(['A', 'B', 'E', 'I', 'Q', 'g', 'w', '/', '+', '-', '_', '=']);
        if (how === 3) k = R.pick([' ', '\n', '\t', '\ufeff', '']) + k + R.pick([' ', '\r\n', '=', '']);
        if (how === 4) { const p = R.int(k.length); k = k.slice(0, p) + R.pick([' ', '\n', '!', '=', '-', '_', '\u00e9']) + k.slice(p + R.int(2)); }
        if (how === 5) k = k.slice(0, R.int(44));
        if (how === 6) k += R.text(['A', 'B', '/', '+'], 1, 4);
        keyInputs.push(k);
    }
    keys.valid_key = keyInputs.filter(lib.wellFormed).map((k) => ({ in: k, out: keyService.isValidRustDeskPublicKey(k) }));

    // --- normalizeServerHost, normalizeHostInput, stripRequestHost, isPhoneUnreachableHost
    const hostInputs = [...HOSTS];
    for (let i = 0; i < 2500; i++) hostInputs.push(randomHost());
    const wellFormedHosts = [...new Set(hostInputs)].filter(lib.wellFormed);
    hosts.normalize_server_host = wellFormedHosts
        .map((h) => ({ in: h, out: conn.normalizeServerHost(h) }))
        .filter((v) => lib.wellFormed(v.out.host));
    hosts.normalize_host_input = wellFormedHosts
        .map((h) => ({ in: h, out: keyService.normalizeHostInput(h) }))
        .filter((v) => lib.wellFormed(v.out));
    hosts.strip_request_host = wellFormedHosts.map((h) => ({ in: h, out: clientConfigHost.stripRequestHost(h) }));
    hosts.phone_unreachable = wellFormedHosts.map((h) => ({ in: h, out: clientConfigHost.isPhoneUnreachableHost(h) }));

    // --- defaultServerHost, defaultUseHttps, apiUrlForHost per configuration
    const API_URLS = [
        ...CONFIGS.map((c) => c.apiUrl), 'http://127.0.0.1:21114', 'https://127.0.0.1', 'localhost:21114', 'localhost',
        '127.0.0.1:21114/api', '[::1]:21114', 'http://[::1]:21114/api', 'HTTP://LOCALHOST:21114/API', ' http://a.example/api ',
        'http://a.example:80/api', 'https://a.example:443', 'http://0x7f.1:21114', 'http://a b/', 'http://a.example:99999',
        'file:///opt/x', 'unix:/run/x.sock', 'http://xn--bcher-kva.example/api', 'http://%41.example/', 'http://a.example./api',
        'https://\u65e5\u672c\u8a9e.jp/api', '://', 'x://', 'https://a.example\t/api', 'wss://a.example', 'a.example:x',
    ];
    hosts.default_server = API_URLS.map((apiUrl) => {
        setConfig({ apiUrl, goApiPort: 21114, apiPort: 21121 });
        return { apiUrl, host: conn.defaultServerHost(), https: conn.defaultUseHttps(), port: conn.defaultApiPort() };
    });
    keys.api_url_for_host = [];
    for (const goApiPort of [21114, 443, 80, 8443, 0, 1, 65535]) {
        for (const apiPort of [21121, 0, 443, 80]) {
            for (const host of ['desk.example.com', 'localhost', '', '[::1]', '203.0.113.10', 'a b', 'm\u00fcnchen.de']) {
                for (const https of [true, false]) {
                    setConfig({ apiUrl: 'http://127.0.0.1:21114/api', goApiPort, apiPort });
                    keys.api_url_for_host.push({ goApiPort, apiPort, host, https, out: keyService.apiUrlForHost(host, https) });
                }
            }
        }
    }

    // --- buildRustDeskConfigPayload, encodeRustDeskCliConfigString, encodeRustDeskConfigUri
    const pubKeyFile = path.join(lib.SCRATCH, 'id_ed25519.pub');
    config.pubKeyPath = pubKeyFile;
    const setFileKey = (content) => {
        fs.rmSync(pubKeyFile, { recursive: true, force: true });
        if (content !== null) fs.writeFileSync(pubKeyFile, content);
    };
    const APIS = [undefined, '', 'https://api.example.com', 'http://203.0.113.10:21114', 'https://api.example.com:21121/base',
        'not a url', ' https://spaced.example ', 'https://\u00fc.example/"quote"', 'http://[2001:db8::1]:21114'];
    const payloadCase = (cfg, fileKey, arg, opts) => {
        setConfig(cfg);
        setFileKey(fileKey);
        const payload = keyService.buildRustDeskConfigPayload(arg, opts);
        return { cfg, fileKey, arg, opts, payload, cli: keyService.encodeRustDeskCliConfigString(payload), uri: keyService.encodeRustDeskConfigUri(payload) };
    };
    keys.payload = [];
    const okPayload = (v) => lib.wellFormed(v.payload.host) && lib.wellFormed(v.payload.relay);
    const allHosts = wellFormedHosts;
    // Every host as a bare string and as an object, with the default settings.
    for (const host of allHosts.slice(0, 700)) {
        const cfg = CONFIGS[0];
        keys.payload.push(payloadCase(cfg, null, host, { publicKey: VALID_KEY }));
        keys.payload.push(payloadCase(cfg, null, { host }, { publicKey: VALID_KEY }));
    }
    // Every configuration with a few hosts and every kind of option.
    for (const cfg of CONFIGS) {
        for (const host of ['desk.example.com', 'https://desk.example.com:8443/path', '203.0.113.10', '[2001:db8::1]:21114', 'm\u00fcnchen.de', '', ' spaced.example ']) {
            for (const opts of [{}, { useHttps: true }, { useHttps: false }, { publicKey: VALID_KEY }, { publicKey: '' },
                { publicKey: 'v1.4.9_public_key_placeholder...' }, { publicKey: ` ${key32(7)}\n`, useHttps: true }]) {
                for (const fileKey of [null, key32(9) + '\n']) {
                    keys.payload.push(payloadCase(cfg, fileKey, host, opts));
                    keys.payload.push(payloadCase(cfg, fileKey, { host, relay: R.pick(allHosts), api: R.pick(APIS) }, opts));
                }
            }
        }
    }
    // Random combinations.
    for (let i = 0; i < 3500; i++) {
        const cfg = R.pick(CONFIGS);
        const fileKey = R.pick([null, null, key32(9), ` ${key32(9)}\r\n`, '', 'garbage', bare(key32(3))]);
        const opts = {};
        if (R.chance(0.6)) opts.publicKey = R.pick(KEYS);
        if (R.chance(0.4)) opts.useHttps = R.chance(0.5);
        let arg;
        if (R.chance(0.3)) {
            arg = R.pick(allHosts);
        } else {
            arg = { host: R.pick(allHosts) };
            if (R.chance(0.6)) arg.relay = R.pick(allHosts);
            if (R.chance(0.6)) arg.api = R.pick(APIS);
        }
        if (opts.publicKey !== undefined && !lib.wellFormed(opts.publicKey)) continue;
        keys.payload.push(payloadCase(cfg, fileKey, arg, opts));
    }
    keys.payload = keys.payload.filter(okPayload);

    // --- getPublicKey and getApiKey on files
    const fileStates = [
        { name: 'missing', bytes: null },
        { name: 'empty', bytes: '' },
        { name: 'blank', bytes: lib.b64(' \n\t') },
        { name: 'valid', bytes: lib.b64(VALID_KEY) },
        { name: 'valid newline', bytes: lib.b64(VALID_KEY + '\n') },
        { name: 'valid crlf bom', bytes: lib.b64('\ufeff' + key32(7) + '\r\n') },
        { name: 'valid no padding', bytes: lib.b64(bare(key32(7))) },
        { name: 'placeholder', bytes: lib.b64('YOUR_PUBLIC_KEY\n') },
        { name: 'two lines', bytes: lib.b64(VALID_KEY + '\n' + VALID_KEY + '\n') },
        { name: 'short', bytes: lib.b64('abcd1234') },
        { name: 'nine', bytes: lib.b64('abcd12345') },
        { name: 'hex key', bytes: lib.b64('0123456789abcdef0123456789abcdef\n') },
        { name: 'spaces inside', bytes: lib.b64('  abcd efgh ijkl  \n') },
        { name: 'non ascii', bytes: lib.b64('\u00e4\u00f6\u00fc\u00df\u4e2d\u6587abcdefg') },
        { name: 'astral inside', bytes: lib.b64('ab\ud83d\ude00cdefghij\ud83d\ude00kl') },
        { name: 'invalid utf8', bytes: Buffer.from([0x61, 0x62, 0x63, 0x64, 0xff, 0xfe, 0x65, 0x66, 0x67, 0x68, 0xe2, 0x82]).toString('base64') },
        { name: 'nul', bytes: lib.b64('abcd\u0000efghij') },
        // Overlong forms, a surrogate, a value past the last code point, a
        // lone continuation byte and sequences cut short, between letters.
        {
            name: 'broken utf8 of every kind',
            bytes: Buffer.from([0x6b, 0x31, 0xc0, 0x80, 0x6b, 0x32, 0xc1, 0xbf, 0x6b, 0x33, 0xe0, 0x80, 0x80, 0x6b, 0x34, 0xed, 0xa0, 0x80,
                0x6b, 0x35, 0xf0, 0x80, 0x80, 0x80, 0x6b, 0x36, 0xf4, 0x90, 0x80, 0x80, 0x6b, 0x37, 0xf5, 0x6b, 0x38, 0x80, 0x6b, 0x39,
                0xf0, 0x9f, 0x98, 0x80, 0x6b, 0x61, 0xc3, 0x28, 0x6b, 0x62, 0xe2, 0x28, 0xa1, 0x6b, 0x63, 0xe1, 0x80, 0x6b, 0x64,
                0xf5, 0x80, 0x80, 0x80, 0x6b, 0x65, 0xf8, 0x88, 0x80, 0x80, 0x80, 0x6b, 0x66, 0xf4, 0x8f, 0xbf, 0xbf, 0x6b, 0x67,
                0xf0, 0x9f, 0x98]).toString('base64'),
        },
        { name: 'directory', dir: true },
    ];
    const apiKeyFile = path.join(lib.SCRATCH, '.api_key');
    config.apiKeyPath = apiKeyFile;
    keys.key_files = [];
    for (const state of fileStates) {
        for (const file of [pubKeyFile, apiKeyFile]) {
            fs.rmSync(file, { recursive: true, force: true });
            if (state.dir) fs.mkdirSync(file);
            else if (state.bytes !== null) fs.writeFileSync(file, Buffer.from(state.bytes, 'base64'));
        }
        keys.key_files.push({
            state,
            publicKey: keyService.getPublicKey(),
            apiKeyMasked: keyService.getApiKey(),
            apiKeyMaskedTrue: keyService.getApiKey(true),
            apiKey: keyService.getApiKey(false),
        });
        fs.rmSync(pubKeyFile, { recursive: true, force: true });
        fs.rmSync(apiKeyFile, { recursive: true, force: true });
    }
    // A cut through a character outside the Basic Multilingual Plane leaves
    // half of it in JavaScript; the Go port writes U+FFFD there. These are
    // kept apart.
    keys.api_key_half_characters = [];
    for (const text of ['abc\ud83d\ude00defghijk', 'abcdefghi\ud83d\ude00jkl', '\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00']) {
        fs.writeFileSync(apiKeyFile, text);
        const masked = keyService.getApiKey(true);
        keys.api_key_half_characters.push({ bytes: lib.b64(text), wellFormed: masked.isWellFormed(), toWellFormed: masked.toWellFormed() });
        fs.rmSync(apiKeyFile);
    }

    // --- resolvePublicKey, getClientConfig, the QR codes, getServerConfig: scripted runs
    const realNow = Date.now;
    let clock = 1_700_000_000_000;
    Date.now = () => clock;
    config.dbPath = '/opt/betterdesk/db_v2.sqlite3';
    const GO_ANSWERS = [
        { data: { key: VALID_KEY } }, { data: { key: ` ${key32(5)}\n` } }, { data: { key: bare(key32(6)) } },
        { data: { key: 'v1.4.9_public_key_placeholder...' } }, { data: { key: 123 } }, { data: { key: null } }, { data: {} },
        { data: 'plain text' }, { data: [VALID_KEY] }, { data: null }, { data: '' }, { data: { key: '' } },
        { data: { Key: VALID_KEY } }, { data: { key: [VALID_KEY] } }, { fail: true, status: 500 }, { fail: true, status: 404 },
    ];
    const ENDPOINT_ARGS = [
        { host: '203.0.113.10', relay: '203.0.113.10', api: 'http://203.0.113.10:21114' },
        { host: 'desk.example.com' }, { host: 'remote.example.com', relay: 'relay.example.com', api: 'https://api.example.com' },
        { host: '' }, { host: 'm\u00fcnchen.de', relay: ' relay.example.com ', api: '' }, { host: 'https://desk.example.com:8443/x' },
        { host: '[::1]:5000', relay: '::1' },
    ];
    keys.runs = [];
    const CALLS = ['resolve', 'clientConfig', 'configQR', 'keyQR', 'serverConfig', 'payloadAsync'];
    for (let run = 0; run < 260; run++) {
        keyService._resetGoKeyCacheForTests();
        clock += 1_000_000;
        const cfg = R.pick(CONFIGS);
        setConfig(cfg);
        const steps = [];
        const n = 1 + R.int(6);
        for (let i = 0; i < n; i++) {
            const step = {
                advanceMs: R.pick([0, 0, 1, 1000, 29_999, 30_000, 30_001, 60_000]),
                fileKey: R.pick([null, null, null, 'garbage\n', '', key32(9) + '\n']),
                apiKeyFile: R.pick([null, 'abcd1234', '0123456789abcdef\n', '']),
                go: R.pick(GO_ANSWERS),
                qrFails: R.chance(0.2),
                call: run < CALLS.length ? CALLS[run] : R.pick(CALLS),
                arg: R.pick(ENDPOINT_ARGS),
            };
            if (step.call === 'payloadAsync') step.opts = R.pick([{}, { publicKey: VALID_KEY }, { publicKey: 'junk' }, { useHttps: true }]);
            clock += step.advanceMs;
            setFileKey(step.fileKey);
            fs.rmSync(apiKeyFile, { force: true });
            if (step.apiKeyFile !== null) fs.writeFileSync(apiKeyFile, step.apiKeyFile);
            goApi.answer = step.go;
            goApi.calls = [];
            qr.fail = step.qrFails;
            qr.calls = [];
            let out;
            if (step.call === 'resolve') out = await keyService.resolvePublicKey();
            if (step.call === 'clientConfig') out = await keyService.getClientConfig(step.arg);
            if (step.call === 'configQR') out = await keyService.getServerConfigQR(step.arg);
            if (step.call === 'keyQR') out = await keyService.getPublicKeyQR();
            if (step.call === 'serverConfig') {
                out = await keyService.getServerConfig();
                out.pubKeyPath = '{PUB}';
                out.apiKeyPath = '{API}';
            }
            if (step.call === 'payloadAsync') out = await keyService.buildRustDeskConfigPayloadAsync(step.arg, step.opts);
            steps.push({ ...step, out, goCalls: goApi.calls, qrTexts: qr.calls.map((c) => c.text) });
        }
        keys.runs.push({ cfg, steps });
    }
    Date.now = realNow;
    // The options the console drew its QR codes with.
    qr.calls = [];
    qr.fail = false;
    setFileKey(VALID_KEY);
    await keyService.getPublicKeyQR();
    await keyService.getServerConfigQR('desk.example.com');
    keys.qr_options = qr.calls.map((c) => c.options);
    setFileKey(null);

    lib.write('keys', keys);
    lib.write('hosts', hosts);

    // --- new URL
    const urls = {};
    const URLS = [
        ...API_URLS, ...APIS.filter((a) => typeof a === 'string'),
        'https://api.example.com/', 'https://api.example.com//', 'https://api.example.com/a/', 'https://api.example.com/a//b///',
        'https://api.example.com/a/../b', 'https://api.example.com/a/./b/.', 'https://api.example.com/%2e%2e/x', 'https://api.example.com/..',
        'https://api.example.com/a/.well-known/../x', 'https://api.example.com/a.b/../c', 'https://api.example.com/?', 'https://api.example.com/?a=b&c=d',
        'https://api.example.com?x', 'https://api.example.com#frag', 'https://api.example.com/?q=a b', "https://api.example.com/?q='x'",
        'https://api.example.com/?q="x"<y>', 'https://api.example.com/p a/t h', 'https://api.example.com/\u00e4/\u4e2d?\u00e4=\u4e2d',
        'https://api.example.com/{x}/`y`/^z|', 'https://api.example.com/%zz/%41', 'https://api.example.com\\a\\b', 'https:\\\\api.example.com\\a',
        'https://API.Example.COM:443/', 'http://api.example.com:80/', 'http://api.example.com:443/', 'https://api.example.com:80/',
        'https://api.example.com:0/', 'https://api.example.com:00021/', 'https://api.example.com:65535/', 'https://api.example.com:65536/',
        'https://api.example.com:/', 'https://api.example.com:-1/', 'https://api.example.com:1a/', 'https://:443/', 'https://@/',
        'https://user@api.example.com/', 'https://user:p@ss@api.example.com/', 'https://u:p@:1/', 'https://a@b@c.example/',
        'http://1.2.3.4/', 'http://1.2.3/', 'http://1.2/', 'http://16909060/', 'http://0x1020304/', 'http://0300.0250.0.1/', 'http://1.2.3.4.5/',
        'http://256.1.1.1/', 'http://1.2.3.256/', 'http://1.2.3.4./', 'http://1.2.3.4../', 'http://0x/', 'http://09/', 'http://1.2.3.08/',
        'http://4294967295/', 'http://4294967296/', 'http://a.1/', 'http://a.0x1/', 'http://1.a/', 'http://999999999999999999999/',
        'http://[::1]/', 'http://[::]/', 'http://[1:2:3:4:5:6:7:8]/', 'http://[1::8]/', 'http://[::ffff:1.2.3.4]/', 'http://[1:2:3:4:5:6:1.2.3.4]/',
        'http://[2001:DB8::1]/', 'http://[::1/', 'http://::1]/', 'http://[1:2:3:4:5:6:7:8:9]/', 'http://[::g]/', 'http://[1::2::3]/',
        'http://[0:0:1:0:0:0:1:0]/', 'http://[::1]:21114/x', 'http://[::1.2.3.4]/', 'http://[::1.2.3]/', 'http://[fe80::1%25eth0]/',
        'http://ex%41mple.com/', 'http://ex%61mple.com/', 'http://a%2Eb/', 'http://a%00b/', 'http://a%20b/', 'http://%/', 'http://a%zzb/',
        'http://a.b./', 'http://.a.b/', 'http://a..b/', 'http://-a.com/', 'http://a_b.com/', 'http://a b/', 'http://a<b/', 'http://a|b/',
        'http://a^b/', 'http://a[b/', 'http://a]b/', 'http://a`b/', 'http://a{b}/', 'http://a!b$c&d\'e(f)g*h+i,j;k=l~/', 'http://a"b/',
        'http://xn--bcher-kva.example/', 'http://XN--BCHER-KVA.example/', 'http://xn--a.example/', 'http://xn--/', 'http://xn--a-/',
        'http://xn--abc/', 'http://xn--80akhbyknj4f.xn--p1ai/', 'http://xn--nxasmq6b/', 'http://xn--zca.de/', 'http://xn--fa-hia.de/',
        'http://xn--ls8h.la/', 'http://xn--9999999999999/', 'http://xn--a-ecp.ru/', 'http://a.xn--b/', 'http://xn--mnchen-3ya.de./',
        'http://ab--c.example/', 'http://xn-/', 'http://xn-x.example/', 'http://axn--b.example/',
        'x://Host:1/a/../b', 'x:/a/../..', 'x:opaque path', 'file:///C:/a/../b', 'file://host/share', 'file://localhost/x', 'file:c:/x',
        'mailto:a@b', 'data:text/plain,hi', 'javascript:alert(1)', 'blob:https://a/b', 'ws://a.example:80/x', 'wss://a.example:443/x',
        'ftp://a.example:21/x', ' \t https://trim.example/ \n', 'https://ta\tb.example/a\nb', '\u0000https://nul.example/',
        'https', 'https:', 'https:/', 'https://', '//a.example', '/path', 'a.example', '', ' ', '1http://a.example', 'ht tp://a.example',
        'h+t-t.p://a.example', 'HtTpS://A.Example/PaTh?QuErY',
    ];
    const urlInputs = [...URLS];
    const URL_PIECES = ['http://', 'https://', 'a', 'b', 'example', '.com', '.', '..', '/', '//', ':', ':80', ':443', ':8080', '@', '?', '#',
        '%2e', '%41', '%', ' ', '\\', '[', ']', '::1', '127', '0x7f', '.1', 'xn--', '-', '\u00e9', '\u4e2d', '\ud83d\ude00', '\t', "'", '"', '<', '^', '{', '|'];
    for (let i = 0; i < 2600; i++) {
        urlInputs.push(R.pick(['http://', 'https://', 'https://', 'HTTP://', 'http:', 'x://', '']) + R.text(URL_PIECES, 0, 9));
    }
    urls.parse = [...new Set(urlInputs)].filter(lib.wellFormed).map((input) => {
        try {
            const u = new URL(input);
            return { in: input, out: { protocol: u.protocol, hostname: u.hostname, port: u.port, pathname: u.pathname, search: u.search } };
        } catch (_) {
            return { in: input, out: null };
        }
    });
    lib.write('urls', urls);

    console.warn = warn;
    lib.writeCounts('keys.js');
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
