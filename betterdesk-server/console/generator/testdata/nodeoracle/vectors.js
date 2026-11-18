'use strict';

/**
 * Records what the Node services of the support client generator return,
 * as JSON vectors for the Go tests of console/generator. See README.md.
 *
 *   node nodeoracle/vectors.js      (from betterdesk-server/console/generator/testdata)
 *
 * Every seed here is made up for the test; none is used anywhere else.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'generator-oracle-'));
process.env.DATA_DIR = path.join(scratch, 'data');
fs.mkdirSync(process.env.DATA_DIR, { recursive: true });
process.env.HBBS_API_URL = 'http://desk.example.test:21114/api';
process.env.BETTERDESK_API_URL = 'http://desk.example.test:21114/api';
delete process.env.BETTERDESK_CLIENT_GENERATOR_MODE;
for (const key of Object.keys(process.env)) {
    if (/^BETTERDESK_CLIENT_|^BETTERDESK_CUSTOM_CLIENT|^BETTERDESK_GITHUB_TOKEN$|^GITHUB_TOKEN$/.test(key)) delete process.env[key];
}
process.env.BETTERDESK_BUILD_ENV_FILE = path.join(scratch, 'no-build.env');

function web(rel) { return require(path.join(WEB, rel)); }

// Deterministic random numbers for the generated inputs
let seed = 0x9e3779b9;
function rnd() {
    seed ^= seed << 13; seed >>>= 0;
    seed ^= seed >>> 17;
    seed ^= seed << 5; seed >>>= 0;
    return seed / 0x100000000;
}
function pick(list) { return list[Math.floor(rnd() * list.length)]; }

const out = {};
const write = (name, value) => {
    fs.writeFileSync(path.join(__dirname, '..', `${name}.json`), `${JSON.stringify(value, null, 1)}\n`);
    console.log(`${name}: ${Array.isArray(value) ? value.length : Object.keys(value).length}`);
};

// ---- texts with every kind of character the rules meet
const texts = [
    '', ' ', 'a', ' Acme ', 'Acme Support', 'Łódź & Warszawa', 'Moja Firma IT', 'Žluťoučký kůň', 'Straße',
    'x'.repeat(99) + '😀', 'x'.repeat(100) + '😀', 'x'.repeat(250) + '😀', '😀'.repeat(60), '\ud83d', 'a\udc00b',
    ' line ', '﻿bom', ' nbsp　', 'İstanbul', 'tab\there', 'quote"back\\slash', '<b>html</b>&',
    'desk.example.com', 'https://Desk.Example.com:8443/path', 'http://[::1]:80', '10.0.0.1', '300.1.1.1', 'bad host!',
    '-leading', 'a'.repeat(253), 'a'.repeat(260), 'xn--bcher-kva.example', 'bücher.example', 'UPPER.example',
    '#ABCDEF', '#abcdef', '#12345', '#1234567', 'red', '#aBcDeF', 'data:image/png;base64,AAAA', 'data:image/svg+xml;base64,PHN2Zz4=',
    'data:text/plain;base64,AAAA', 'data:image/png,raw', 'example.com/help', 'https://example.com/x?y=1#z', 'javascript:alert(1)',
    'ftp://example.com', 'mailto:a@b.c', 'http://exa mple.com', 'https://', 'en', 'de', 'zh-TW', 'zh-tw', 'xx', 'EN',
];
const values = [undefined, null, true, false, 0, 1, -0, 0.5, 2, 21114, '21114', [], ['#aabbcc'], [1, 2], {}, { a: 1 }, ...texts];

function randomValue(depth = 0) {
    const r = rnd();
    if (r < 0.55) return pick(texts);
    if (r < 0.75) return pick(values);
    if (r < 0.85 && depth < 2) return randomObject(depth + 1);
    if (r < 0.9 && depth < 2) return [randomValue(depth + 1), randomValue(depth + 1)];
    return Math.floor(rnd() * 100000) / (rnd() < 0.5 ? 1 : 7);
}

const brandingKeys = [
    'app_name', 'appName', 'company_name', 'companyName', 'relay_host', 'relayHost', 'relay_server', 'api_server', 'apiServer',
    'api_port', 'apiPort', 'disable_settings', 'disableSettings', 'short_text', 'shortText', 'product_label', 'productLabel',
    'hide_product_type', 'hideProductType', 'contact_email', 'contactEmail', 'contact_phone', 'contactPhone', 'contact_url',
    'contactUrl', 'logo_data_url', 'logoDataUrl', 'primary_color', 'primaryColor', 'accent_color', 'accentColor',
    'background_color', 'backgroundColor', 'surface_color', 'text_color', 'textColor', 'text_muted_color', 'textMutedColor',
    'status_ready_color', 'header_text_color', 'headerTextColor', 'allow_unattended', 'allowUnattended', 'install_service',
    'installService', 'autostart', 'autoStart', 'capabilities', 'default_lang', 'defaultLang', 'server_host', 'serverHost',
    'use_https', 'useHttps', 'server', 'public_key', 'enrollment_token', '__proto__', '0', '10', 'extra',
];

function randomObject(depth = 0) {
    const o = {};
    const n = Math.floor(rnd() * 8);
    for (let i = 0; i < n; i++) {
        const key = pick(brandingKeys);
        if (key === '__proto__') continue;
        o[key] = key === 'capabilities' ? { desktop: pick(values), files: pick(values), audio: pick(values) }
            : key === 'server' ? { address: pick(values), api_url: pick(values), public_key: pick(values) }
                : randomValue(depth + 1);
    }
    return o;
}

// ---- agentBundleService
const bundleService = web('services/agentBundleService');
{
    const cases = [];
    const inputs = [
        {}, { company_name: 'Example Support', server_host: 'desk.example.test' },
        { company_name: 'Example Support', server_host: 'desk.example.test', use_https: false },
        { company_name: 'Example Support', server_host: 'desk.example.test', product_label: '  Acme Remote Client  ', hide_product_type: true },
        { server_host: 'h', product_label: 'x'.repeat(250) },
        { server_host: 'h', primary_color: ['#aabbcc'] }, { server_host: 'h', accent_color: [['#AABBCC']] },
        { server_host: 'h', contact_url: 'example.com' }, { server_host: 'h', contact_url: 'exa mple' },
        'abc', 5, true, [1, 2], null,
    ];
    for (let i = 0; i < 1500; i++) inputs.push(randomObject());
    for (const input of inputs) {
        const text = input === undefined ? undefined : JSON.stringify(input);
        try {
            const r = bundleService.validateBranding(input);
            cases.push({ input: text, valid: r.valid, errors: r.errors, normalized: JSON.stringify(r.normalized), hash: bundleService.hashBranding(r.normalized) });
        } catch (err) {
            cases.push({ input: text, threw: err.message });
        }
    }
    write('validate_branding', cases);
}
{
    const cases = [];
    const names = [...texts, 'acme', 'a', 'ACME', 'acme_', 'a--b', '-a-', 'ab', 'a'.repeat(32), 'a'.repeat(33), 'ab-'.repeat(12)];
    for (let i = 0; i < 2000; i++) names.push(pick(texts).slice(0, Math.floor(rnd() * 40)) + pick(texts));
    for (const name of names) {
        const slug = bundleService.normalizeSlug(name);
        const check = bundleService.validateSlug(slug);
        cases.push({ name, slug, error: check.valid ? '' : check.error });
    }
    write('slugs', cases);
}

// ---- lib/generatorBuildTypes
{
    const types = web('lib/generatorBuildTypes');
    const cases = [];
    for (const v of [...values, 'agent', 'AGENT', ' support-agent ', 'support_agent', 'agent_client', 'agent-client', 'rdclient', 'betterdesk_support', 'betterdesk-support', 'unknown']) {
        cases.push({ kind: 'product', input: v === undefined ? null : v, missing: v === undefined, out: types.normalizeProductType(v) });
        if (typeof v === 'string') {
            cases.push({ kind: 'status', input: v, out: types.normalizeBuildStatus(v), queued: types.isQueuedBuildStatus(v) });
        }
    }
    for (const s of ['queued', 'PENDING', ' pending ', 'building', 'ready', 'failed', '', 'Ready']) {
        cases.push({ kind: 'status', input: s, out: types.normalizeBuildStatus(s), queued: types.isQueuedBuildStatus(s) });
    }
    const brandings = [
        '{"sku":"betterdesk-support"}', '{"generator_kind":"betterdesk-support"}', '{"generator_version":2}', '{"generator_version":"2"}',
        '{"generator_version":"2.5"}', '{"generator_version":[3]}', '{"generator_version":true}', '{}', '{"app_name":"Old"}', '', 'null',
        'not json', '5', '"text"', '[1]', '{"sku":"BetterDesk-Support"}', '{"generator_version":" 0x2 "}', '{"generator_version":1.99}',
    ];
    for (const b of brandings) cases.push({ kind: 'support', input: b, out: types.isBetterDeskSupportBundle({ branding: b }) });
    write('build_types', cases);
}

// ---- customTxtBuilder and signing
const customTxt = web('services/customTxtBuilder');
const nacl = web('node_modules/tweetnacl');
{
    const cases = [];
    const seeds = [''];
    for (let i = 0; i < 40; i++) {
        const bytes = Buffer.alloc(32);
        for (let j = 0; j < 32; j++) bytes[j] = Math.floor(rnd() * 256);
        const b64 = bytes.toString('base64');
        seeds.push(b64, b64.replace(/=$/, ''), ` ${b64}\n`, b64.replace(/\+/g, '-').replace(/\//g, '_'), `${b64.slice(0, 20)} ${b64.slice(20)}`, b64 + '==', b64.slice(0, 40));
    }
    seeds.push(Buffer.alloc(32, 7).toString('base64'), Buffer.alloc(31, 1).toString('base64'), Buffer.alloc(33, 1).toString('base64'));
    const optsList = [
        { appName: 'Acme Support', host: 'desk.example.com', relay: 'relay.example.com', api: 'http://desk.example.com:21114', key: 'PUBKEY', disableSettings: true },
        { host: 'h', api: 'http://h:21114', key: 'k' },
        { host: ' h ', relay: '', api: 5, key: null, disableSettings: 'y', appName: '  ' },
        { host: 'h', disableSettings: 'N' }, { host: 'h', disableSettings: false }, { host: 'h', disableSettings: 'Y' },
        { appName: 'Ünïcode 😀', host: 'ö.example', api: 'https://ö.example', key: 'a/b+c=' },
    ];
    for (let i = 0; i < 300; i++) {
        optsList.push({ appName: randomValue(), host: randomValue(), relay: randomValue(), api: randomValue(), key: randomValue(), disableSettings: pick([true, false, 'Y', 'y', 'N', 1, undefined]) });
    }
    for (const opts of optsList) {
        const json = customTxt.buildSupportCustomTxt(opts);
        const message = customTxt.stableJsonBytes(json);
        const s = seeds[cases.length % seeds.length];
        let signed;
        try {
            const r = customTxt.buildAndSignSupportCustomTxt(opts, s);
            signed = { content: r.content };
        } catch (err) {
            signed = { threw: err.message };
        }
        const plain = customTxt.signCustomTxt(json, s);
        cases.push({
            opts: JSON.stringify(opts, (k, v) => (v === undefined ? '__undefined__' : v)),
            json: JSON.stringify(json), message: message.toString('base64'), seed: s, signed, sign: plain,
        });
    }
    write('custom_txt', cases);

    const keys = [];
    for (const s of seeds) {
        const decoded = Buffer.from(s, 'base64');
        let pub = null;
        if (decoded.length === 32) pub = Buffer.from(nacl.sign.keyPair.fromSeed(new Uint8Array(decoded)).publicKey).toString('base64');
        keys.push({ seed: s, decoded: decoded.toString('hex'), publicKey: pub });
    }
    const alphabet = 'AZaz09+/-_= \n\t.!*é';
    for (let i = 0; i < 3000; i++) {
        let s = '';
        const n = Math.floor(rnd() * 24);
        for (let j = 0; j < n; j++) s += alphabet[Math.floor(rnd() * alphabet.length)];
        keys.push({ seed: s, decoded: Buffer.from(s, 'base64').toString('hex'), publicKey: null });
    }
    write('base64', keys);
}

// ---- installer scripts
async function installers() {
    const win = web('services/windowsSupportInstaller');
    const unix = web('services/platformSupportInstaller');
    const cases = [];
    const dir = fs.mkdtempSync(path.join(scratch, 'inst-'));
    for (const installService of [false, true, 1, '', null]) {
        for (const autostart of [false, true, 'yes', 0, undefined]) {
            cases.push({ kind: 'windows', installService, autostart: autostart === undefined ? '__undefined__' : autostart, text: win.buildWindowsSupportInstallerScript({ installService, autostart }) });
            for (const platform of ['linux', 'macos', 'windows']) {
                await unix.writeUnixSupportInstallers(dir, platform, { installService, autostart });
                cases.push({
                    kind: platform, installService, autostart: autostart === undefined ? '__undefined__' : autostart,
                    install: fs.readFileSync(path.join(dir, 'install-betterdesk-support.sh'), 'utf8'),
                    uninstall: fs.readFileSync(path.join(dir, 'uninstall-betterdesk-support.sh'), 'utf8'),
                    mode: (fs.statSync(path.join(dir, 'install-betterdesk-support.sh')).mode & 0o777).toString(8),
                });
            }
        }
    }
    cases.push({ kind: 'windows-default', text: win.buildWindowsSupportInstallerScript() });
    write('installers', cases);
}

// ---- generation keys and the colour rule of the download page
{
    const builder = web('services/clientBuilderService');
    const cases = [];
    for (let i = 0; i < 200; i++) {
        const fp = crypto.createHash('sha256').update(String(i)).digest('hex');
        const commit = crypto.createHash('sha1').update(String(i)).digest('hex');
        cases.push({ fingerprint: fp, commit, key: builder.generationKey(fp, commit) });
    }
    cases.push({ fingerprint: '', commit: '', key: builder.generationKey('', '') });
    write('generation_keys', cases);

    const channels = [];
    for (let i = 0; i < 256; i++) {
        const c = i / 255;
        const v = c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
        const b = Buffer.alloc(8);
        b.writeDoubleBE(v);
        channels.push(b.toString('hex'));
    }
    write('luminance', channels);
}

// ---- the URL rules
{
    const cases = [];
    const inputs = [...texts, 'http://a', 'HTTPS://EXAMPLE.COM', 'https://example.com:443/', 'http://example.com:80/a/../b',
        'https://user:pw@example.com', 'https://example.com/ä?x=ü', '  https://example.com  ', 'https://[::1]/', 'https://0x7f.1/',
        'http://example.com/%zz', 'https://example..com', 'https://-a.example', 'https://a_b.example', 'example.com'];
    for (const s of inputs) {
        let href = null;
        try {
            const u = new URL(String(s || '').trim());
            href = { protocol: u.protocol, href: u.href };
        } catch (_) { /* invalid */ }
        cases.push({ input: s, url: href });
    }
    write('urls', cases);
}

// ---- JSON as the routes parse and write it
{
    const cases = [];
    const textsJSON = ['"\\ud83d"', '"a\\udc00b"', '"\\ud83d\\ude00"', '{"a":1,"a":2,"b":3}', '{"b":1,"1":2,"a":3,"0":4}', '[1,2.5,-0,1e21,1e-7,0.000001]',
        '" "', '"\\u0000\\u001f"', '{"__proto__":1}', '"\\/"', '1E400', '[[[]]]'];
    for (const t of textsJSON) {
        const v = JSON.parse(t);
        cases.push({ text: t, out: JSON.stringify(v), pretty: JSON.stringify(v, null, 2) });
    }
    write('json', cases);
}


installers().then(() => fs.rmSync(scratch, { recursive: true, force: true }));
