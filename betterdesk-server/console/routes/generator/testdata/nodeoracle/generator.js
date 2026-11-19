'use strict';

/**
 * Records the Node console's generator routes (routes/generator.routes.js
 * with the real dbAdapter, supportGeneratorModule, clientTemplateWorker
 * and agentBundleService) on a scratch database. See README.md.
 *
 * The module is installed by the recording itself, from a template
 * archive made here (six portable templates with stub binaries), with a
 * signing seed that is made up for this test.
 */

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execFileSync } = require('child_process');
// Settings the console reads when its config module loads, which the
// harness does first
const keyDir = fs.mkdtempSync(path.join(require('os').tmpdir(), 'generator-key-'));
const KEY_FILE = path.join(keyDir, 'id_ed25519.pub');
process.env.PUB_KEY_PATH = KEY_FILE;
process.env.HBBS_API_URL = 'http://desk.example.test:21114/api';
process.env.BETTERDESK_API_URL = 'http://desk.example.test:21114/api';
delete process.env.BETTERDESK_CLIENT_GENERATOR_MODE;
delete process.env.AGENT_BUILD_WORKER;

const { record, dialect } = require('./lib');

const SEED = Buffer.alloc(32, 9).toString('base64');
const PUBLIC_KEY = Buffer.alloc(32, 3).toString('base64');
const PLATFORMS = ['windows-x86_64', 'windows-aarch64', 'linux-x86_64', 'linux-aarch64', 'macos-x86_64', 'macos-aarch64'];

function templateArchive(dir) {
    const root = path.join(dir, 'generator-templates');
    fs.mkdirSync(root, { recursive: true });
    const templates = PLATFORMS.map((name) => {
        const t = path.join(root, name);
        fs.mkdirSync(t, { recursive: true });
        fs.writeFileSync(path.join(t, '.custom-txt-here'), 'marker\n');
        const binary = name.startsWith('windows') ? 'betterdesk.exe' : 'betterdesk';
        fs.writeFileSync(path.join(t, binary), Buffer.alloc(110 * 1024, 1));
        fs.writeFileSync(path.join(root, `${name}.tar.gz`), 'archive');
        const [platform, arch] = name.split('-');
        return { platform, arch, format: 'portable', template_path: name, binary_path: binary, archive: `${name}.tar.gz`, sha256: 'a'.repeat(64) };
    });
    fs.writeFileSync(path.join(root, 'manifest.json'), JSON.stringify({
        schema_version: 2, sku: 'generator-templates', version: '1.2.3', required_platforms: PLATFORMS, templates,
    }));
    const file = path.join(dir, 'templates.tar.gz');
    execFileSync('tar', ['--mtime=2020-01-01 00:00:00', '--owner=0', '--group=0', '-czf', file, 'generator-templates'], { cwd: dir });
    return fs.readFileSync(file);
}

function multipart(parts) {
    const boundary = '----oracle0123456789';
    const chunks = [];
    for (const p of parts) {
        let head = `--${boundary}\r\nContent-Disposition: form-data; name="${p.field}"`;
        if (p.file) head += `; filename="${p.file}"`;
        head += '\r\n';
        if (p.type) head += `Content-Type: ${p.type}\r\n`;
        chunks.push(Buffer.from(`${head}\r\n`), Buffer.from(p.content), Buffer.from('\r\n'));
    }
    chunks.push(Buffer.from(`--${boundary}--\r\n`));
    return { bodyBase64: Buffer.concat(chunks).toString('base64'), headers: { 'Content-Type': `multipart/form-data; boundary=${boundary}` } };
}

const steps = [];
let n = 0;
const step = (role, method, p, body, extra = {}) => {
    n += 1;
    const s = { name: `${n} ${method} ${p}`, role, method, path: p, ...extra };
    if (body !== undefined) s.body = typeof body === 'string' ? body : JSON.stringify(body);
    steps.push(s);
};
const sql = (text, params = []) => { n += 1; steps.push({ name: `${n} sql`, sql: text, params }); };
// The old route queued a changed bundle's builds after it answered; the Go
// route queues them first. Waiting for the rows keeps a slow database from
// recording a half-filled queue.
const until = (text, params, test) => steps.push({ until: { sql: text, params, test } });

const ADMINS = ['admin', 'server_admin', 'global_admin'];
const branding = {
    company_name: 'Acme GmbH', app_name: 'Acme Remote', server_host: 'desk.example.com', use_https: false, api_port: '21114',
    short_text: 'We help.', contact_email: 'help@acme.example', contact_url: 'acme.example/help', primary_color: '#1D4ED8',
    product_label: 'Remote help', default_lang: 'de', install_service: true, autostart: true,
};

let archive;
const vars = {};

// before the module is there
for (const role of ['', 'viewer', 'admin']) step(role, 'GET', '/generator', undefined, { headers: { Accept: 'text/html' } });
step('admin', 'GET', '/api/generator/module/status');
step('admin', 'GET', '/api/generator/build-status');
step('admin', 'GET', '/api/generator/platforms');
step('admin', 'POST', '/api/generator/bundles', { name: 'Acme', slug: 'acme', branding });
step('admin', 'POST', '/api/generator/module/install-local', undefined, { multipartArchive: true, version: ' 9.9 ' });
step('admin', 'POST', '/api/generator/module/accept-terms', {});
step('admin', 'POST', '/api/generator/module/install-local', {});
step('admin', 'POST', '/api/generator/module/install-local', undefined, { multipartArchive: true, version: ' 9.9 ' });
step('admin', 'GET', '/api/generator/module/status');
step('admin', 'GET', '/api/generator/build-status');
step('admin', 'GET', '/api/generator/platforms');

// bundles
const create = (role, body) => step(role, 'POST', '/api/generator/bundles', body);
create('admin', { name: 'Acme', slug: 'Acme Remote', branding });
create('admin', { name: 'Acme two', slug: 'acme-remote', branding });
create('admin', { name: '', slug: 'x1', branding });
create('admin', { name: '   ', slug: 'x2', branding });
create('admin', { name: 5, slug: 'x3', branding: { server_host: 'h.example' } });
create('admin', { name: 'Bad', slug: 'x4', branding: { server_host: 'bad host!', primary_color: 'red' } });
create('admin', { name: 'Bad', slug: 'a', branding });
create('admin', { name: 'Bad', slug: '!!a!!', branding });
create('admin', { name: 'Bad', slug: 'x'.repeat(40), branding });
create('admin', { name: 'Plat', slug: 'plat-1', platforms: [], branding });
create('admin', { name: 'Plat', slug: 'plat-2', platforms: [{ platform: 'linux', arch: 'x64', format: 'deb' }], branding });
create('admin', { name: 'Plat', slug: 'plat-3', platforms: [{ os: 'linux', arch: 'x64' }, { platform: 'windows' }], branding });
create('admin', { name: 'Plat', slug: 'plat-4', platforms: 'all', branding: { ...branding, company_name: 'Other' } });
create('admin', { name: 'Colour list', slug: 'colour', branding: { ...branding, primary_color: ['#aabbcc'] } });
create('admin', { name: 'Logo', slug: 'logo', branding: { ...branding, logo_data_url: 'data:image/png;base64,AAAA', contact_url: 'javascript:alert(1)' } });
create('admin', { name: 'Lone \ud83d', slug: 'lone', branding: { ...branding, app_name: `${'y'.repeat(99)}😀` } });
create('global_admin', { name: 'By global', slug: 'by-global', branding: { server_host: 'g.example', disable_settings: false } });
create('viewer', { name: 'No', slug: 'no', branding });
// Bundles are listed newest first: fixed times give the list one order
['acme-remote', 'x3', 'x'.repeat(32), 'plat-3', 'plat-4', 'lone', 'by-global'].forEach((slug, i) => {
    sql('UPDATE agent_bundles SET created_at = ?, updated_at = ? WHERE slug = ?', [`2019-01-01 00:00:0${i}`, `2019-01-01 00:00:0${i}`, slug]);
});

step('admin', 'GET', '/api/generator/bundles');
step('admin', 'GET', '/api/generator/bundles?includeLegacy=1');
step('admin', 'GET', '/api/generator/bundles?includeRevoked=1&includeLegacy=1');
step('admin', 'GET', '/api/generator/bundles/<ID:acme-remote>');
step('admin', 'GET', '/api/generator/bundles/nope');

// changes
const put = (body, id = '<ID:acme-remote>') => step('admin', 'PUT', `/api/generator/bundles/${id}`, body);
put({ name: 'Acme renamed' });
put({ slug: 'acme-new' });
put({ slug: 'plat-4' });
put({ slug: 'b' });
put({ name: '' });
put({ branding: { ...branding, short_text: 'Changed text only' } });
put({ branding: { ...branding, product_label: 'Portal only' } });
put({ branding: { ...branding, server_host: 'other.example.com' } });
put({ branding: { server_host: 'bad host!' } });
put({ platforms: [] });
put({ name: 'x' }, 'nope');
until('SELECT COUNT(*) AS n FROM agent_bundle_builds WHERE branding_hash = (SELECT branding_hash FROM agent_bundles WHERE slug = ?)', ['acme-new'], (rows) => Number(rows[0].n) >= 6);
step('admin', 'GET', '/api/generator/bundles/<ID:acme-new>');

// rebuilds
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/rebuild', {});
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/rebuild', { platforms: [{ platform: 'linux', arch: 'x64', format: 'portable' }] });
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/rebuild', { platforms: [{ platform: 'linux', arch: 'x64', format: 'rpm' }] });
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/rebuild', { platforms: [] });
step('admin', 'POST', '/api/generator/bundles/nope/rebuild', {});
for (const target of ['windows/x64/portable', 'linux/x64/deb', 'beos/x64/portable', 'macos/arm64/portable']) {
    step('admin', 'POST', `/api/generator/bundles/<ID:acme-new>/rebuild/${target}`, {});
}
step('admin', 'POST', '/api/generator/bundles/nope/rebuild/windows/x64/portable', {});
step('admin', 'GET', '/api/generator/build-status');

// a build that is ready, with its file, and the portal
sql("UPDATE agent_bundle_builds SET status = 'ready', artifact_path = ?, artifact_size = 11, artifact_sha256 = ? WHERE platform = 'windows' AND arch = 'x64'",
    ['<ARTIFACTS>/betterdesk-support-win.zip', 'b'.repeat(64)]);
sql("UPDATE agent_bundle_builds SET status = 'ready', artifact_path = ?, artifact_size = 5 WHERE platform = 'linux' AND arch = 'arm64'", ['/etc/passwd']);
sql("UPDATE agent_bundle_builds SET status = 'failed', error_message = 'boom' WHERE platform = 'macos' AND arch = 'x64'");
step('', 'GET', '/api/d/acme-new/manifest');
step('', 'GET', '/api/d/<ID:acme-new>/manifest');
step('', 'GET', '/api/d/acme-new/page');
step('', 'GET', '/api/d/logo/page');
step('', 'GET', '/api/d/nope/page');
step('', 'GET', '/api/d/acme-new/download/windows/x64/portable', undefined, { download: true });
step('', 'GET', '/api/d/acme-new/download/windows/x64/portable', undefined, { download: true, headers: { Range: 'bytes=2-5' } });
step('', 'HEAD', '/api/d/acme-new/download/windows/x64/portable', undefined, { download: true });
step('', 'GET', '/api/d/acme-new/download/linux/arm64/portable');
step('', 'GET', '/api/d/acme-new/download/macos/x64/portable');
step('', 'GET', '/api/d/acme-new/download/beos/x64/portable');
sql('SELECT download_count FROM agent_bundles WHERE slug = ?', ['acme-new']);

// revoke, the portal of a revoked bundle, delete
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/revoke', {});
step('', 'GET', '/api/d/acme-new/page');
step('', 'GET', '/api/d/acme-new/manifest');
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/rebuild', {});
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/rebuild/windows/x64/portable', {});
step('admin', 'GET', '/api/generator/bundles');
step('admin', 'GET', '/api/generator/bundles?includeRevoked=1');
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/revoke', { revoked: 0 });
step('admin', 'POST', '/api/generator/bundles/<ID:acme-new>/revoke', { revoked: false });
step('admin', 'POST', '/api/generator/bundles/nope/revoke', {});
step('admin', 'DELETE', '/api/generator/bundles/<ID:plat-4>');
step('admin', 'DELETE', '/api/generator/bundles/<ID:plat-4>');

// previews
const previews = [
    {}, { branding: 'abc' }, { branding: [1, 2] }, { branding: 5 }, { branding: null },
    { branding: { server_host: 'https://Desk.Example.com:8443/x', api_server: 'https://api.example', public_key: 'mine' } },
    { branding: { serverHost: 'h.example', api_port: 443, use_https: 1 } },
    { branding: { server_host: 'h.example', api_port: '80', use_https: 0, relay_host: ['r', 1], company_name: { a: 1 } } },
    { branding: { server_host: 'h.example', enrollment_token: 'x', has_enrollment_token: true, contact_url: 'mailto:a@b.c' } },
    { branding: { server_host: 'h.example', accent_color: [['#AABBCC']] } },
];
for (const body of previews) step('admin', 'POST', '/api/generator/preview', body);

// defaults and the old toml generator
step('admin', 'GET', '/api/generator/defaults');
step('viewer', 'GET', '/api/generator/config');
for (const body of [{}, { serverHost: 'h' }, { serverHost: 'h', serverPort: 1, relayHost: 'r', relayPort: '2', clientName: 'c' }, { serverHost: ['a', 'b'], relayHost: 0, clientName: { x: 1 } }]) {
    step('viewer', 'POST', '/api/generator/generate-config', body);
}
// the relay line always has a port (3.5.264): the one in relayHost, else
// relayPort, else the server's relay port
const relays = [
    { relayHost: 'r' }, { relayHost: ' r:5 ', relayPort: 9 }, { relayHost: 'r:0' }, { relayHost: 'r:65536', relayPort: '7' },
    { relayHost: 'r:007' }, { relayHost: '[fe80::1]' }, { relayHost: '[fe80::1]:6' }, { relayHost: '[fe80::1]:0', relayPort: 8 },
    { relayHost: 'fe80::1' }, { relayHost: 'https://R.example:8443/x' }, { relayHost: 'HTTP://r.example/p/q' }, { relayHost: '   ' },
    { relayHost: 5 }, { relayHost: ['r'] }, { relayHost: { a: 1 } }, { relayHost: true }, { relayHost: 'r', relayPort: 0 },
    { relayHost: 'r', relayPort: [] }, { relayHost: 'r', relayPort: [1, 2] }, { relayHost: 'r', relayPort: true }, { relayHost: 'r', relayPort: { a: 1 } },
    { relayHost: 'r', relayPort: '' }, { relayHost: 'r', relayPort: ' 3 ' }, { relayHost: 'r :5' }, { relayHost: 'x'.repeat(320) + ':5' },
];
for (const body of relays) step('viewer', 'POST', '/api/generator/generate-config', { serverHost: 'h', ...body });

// a legacy bundle and a second module install
sql("INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash, product_type) VALUES ('legacy00001', NULL, 'Old', '{\"app_name\":\"Old\"}', 'oldhash', 'agent')"); sql("UPDATE agent_bundles SET created_at = '2019-02-01 00:00:01' WHERE bundle_id = 'legacy00001'");
sql("INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash) VALUES ('broken00001', 'broken', 'Broken', 'not json', 'brokenhash')"); sql("UPDATE agent_bundles SET created_at = '2019-02-01 00:00:02' WHERE bundle_id = 'broken00001'");
sql("INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash) VALUES ('nullbr00001', 'null-branding', 'Null', 'null', '')"); sql("UPDATE agent_bundles SET created_at = '2019-02-01 00:00:03' WHERE bundle_id = 'nullbr00001'");
step('admin', 'GET', '/api/generator/bundles?includeLegacy=1');
step('admin', 'GET', '/api/generator/bundles/legacy00001');
step('', 'GET', '/api/d/legacy00001/page');
step('', 'GET', '/api/d/broken/page');
step('', 'GET', '/api/d/null-branding/page');
step('', 'GET', '/api/d/null-branding/manifest');
put({ name: 'Null changed' }, 'nullbr00001');
step('admin', 'POST', '/api/generator/bundles/nullbr00001/rebuild/windows/x64/portable', {});
step('admin', 'POST', '/api/generator/module/install-local', undefined, { multipartArchive: true, version: '' });
step('admin', 'GET', '/api/generator/module/status');

record({
    routes: 'routes/generator.routes',
    mounts: ['/'],
    out: 'node',
    seed: [],
    steps,
    vars,
    prepare: async ({ scratch, exec }) => {
        const work = fs.mkdtempSync(path.join(scratch, 'archive-'));
        archive = templateArchive(work);
        for (const s of steps) {
            if (!s.multipartArchive) continue;
            const m = multipart([
                { field: 'version', content: s.version },
                { field: 'archive', file: 'templates.tar.gz', type: 'application/gzip', content: archive },
            ]);
            s.bodyBase64 = m.bodyBase64;
            s.headers = m.headers;
            delete s.multipartArchive;
            delete s.version;
        }
        fs.writeFileSync(path.join(scratch, 'custom-client-signing.seed'), `${SEED}\n`);
        fs.writeFileSync(KEY_FILE, `${PUBLIC_KEY}\n`);
        const artifacts = path.join(scratch, 'agent-builds');
        fs.mkdirSync(artifacts, { recursive: true });
        fs.writeFileSync(path.join(artifacts, 'betterdesk-support-win.zip'), 'zip-content');
        const when = new Date('2024-05-06T07:08:09Z');
        fs.utimesSync(path.join(artifacts, 'betterdesk-support-win.zip'), when, when);
        process.env.AGENT_ARTIFACT_DIR = artifacts;
        vars['<ARTIFACTS>'] = artifacts;
        await exec("INSERT INTO branding_config (key, value) VALUES ('agentBgType', 'color'), ('agentBgColor', '#102030'), ('agentShowPoweredBy', 'false'), ('appName', 'Panel Name')");
        await require(path.join(process.env.WEB_NODEJS || '/repo/web-nodejs', 'services/brandingService')).loadBranding();
        // Bundle ids are random: here they count up from 000000000001, the
        // Go test gives its run the same ones
        const realRandom = crypto.randomBytes;
        let counter = 0;
        crypto.randomBytes = (size, cb) => {
            if (size === 6 && !cb) {
                counter += 1;
                return Buffer.from(`0000000000${counter.toString(16).padStart(2, '0')}`, 'hex');
            }
            return realRandom(size, cb);
        };
        scrub.dir = scratch;
    },
    keep: (s, answer) => {
        const extra = {};
        if (s.download) {
            for (const h of ['content-type', 'content-length', 'content-disposition', 'content-range', 'accept-ranges', 'etag', 'last-modified']) {
                if (answer.headers[h] !== undefined) extra[h] = answer.headers[h];
            }
            extra.bodyBase64 = answer.body.toString('base64');
        }
        if (s.path && s.path.includes('/page') && answer.headers['cache-control']) extra['cache-control'] = answer.headers['cache-control'];
        return extra;
    },
    scrub: (text) => scrub(text),
});

function scrub(text) {
    return scrub.dir ? text.split(scrub.dir).join('<DATA>') : text;
}

console.log(`dialect ${dialect}`);
