'use strict';

/**
 * Records what the build side of the Node generator does: the zip
 * adm-zip writes for a folder, the build capabilities and platform rules
 * of embedded mode for provider settings, the checks of a provider's
 * manifest and of a template tree. See README.md.
 *
 *   node nodeoracle/worker.js      (from betterdesk-server/console/generator/testdata)
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'generator-worker-oracle-'));
process.env.DATA_DIR = path.join(scratch, 'data');
fs.mkdirSync(process.env.DATA_DIR, { recursive: true });
process.env.BETTERDESK_BUILD_ENV_FILE = path.join(scratch, 'no-build.env');
for (const key of Object.keys(process.env)) {
    if (/^BETTERDESK_CLIENT_|^BETTERDESK_CUSTOM_CLIENT|^BETTERDESK_GITHUB_TOKEN$|^GITHUB_TOKEN$/.test(key)) delete process.env[key];
}
process.env.BETTERDESK_CLIENT_GENERATOR_MODE = 'embedded';

function web(rel) { return require(path.join(WEB, rel)); }
const write = (name, value) => {
    fs.writeFileSync(path.join(__dirname, '..', `${name}.json`), `${JSON.stringify(value, null, 1)}\n`);
    console.log(`${name}: ${Array.isArray(value) ? value.length : Object.keys(value).length}`);
};

// ---- the zip of a folder, as the Windows artifacts are packed
const fixture = [
    { path: 'betterdesk.exe', content: 'MZ'.repeat(5000), mode: 0o755 },
    { path: 'custom.txt', content: 'c2lnbmVk', mode: 0o644 },
    { path: 'Install-BetterDesk.ps1', content: 'param()\n', mode: 0o644 },
    { path: 'empty.dat', content: '', mode: 0o600 },
    { path: 'data/flutter_assets/AssetManifest.json', content: '{}', mode: 0o644 },
    { path: 'data/app.so', content: 'x'.repeat(3000), mode: 0o700 },
    { path: 'Data/Upper.txt', content: 'upper', mode: 0o644 },
    { path: 'a b/ü-file.txt', content: 'unicode name', mode: 0o644 },
    { path: 'zz/_under', content: '_', mode: 0o644 },
    { path: 'zz/-dash', content: '-', mode: 0o644 },
    { path: 'zz/10.txt', content: '10', mode: 0o644 },
    { path: 'zz/9.txt', content: '9', mode: 0o644 },
    { dir: 'emptydir', mode: 0o755 },
];
const when = new Date('2024-05-06T07:08:10Z');
function makeFixture(root) {
    for (const f of fixture) {
        const p = path.join(root, f.path || f.dir);
        if (f.dir) {
            fs.mkdirSync(p, { recursive: true });
        } else {
            fs.mkdirSync(path.dirname(p), { recursive: true });
            fs.writeFileSync(p, f.content);
            fs.chmodSync(p, f.mode);
            fs.utimesSync(p, when, when);
        }
    }
    for (const f of fixture) {
        const p = path.join(root, f.path ? path.dirname(f.path) : f.dir);
        if (p !== root) {
            fs.chmodSync(p, 0o755);
            fs.utimesSync(p, when, when);
        }
    }
}
{
    const AdmZip = web('node_modules/adm-zip');
    const stage = path.join(scratch, 'stage', 'betterdesk-support-windows-x64');
    fs.mkdirSync(stage, { recursive: true });
    makeFixture(stage);
    const zipPath = path.join(scratch, 'out.zip');
    const zip = new AdmZip();
    zip.addLocalFolder(stage, path.basename(stage));
    zip.writeZip(zipPath);
    const back = new AdmZip(zipPath);
    const entries = back.getEntries().map((e) => ({
        name: e.entryName, attr: e.header.attr >>> 0, method: e.header.method, time: e.header.timeval >>> 0,
        crc: e.header.crc >>> 0, size: e.header.size, made: e.header.made, version: e.header.version,
        flags: e.header.flags, data: e.isDirectory ? null : e.getData().toString('base64'),
    }));
    write('zip', { fixture, mtime: when.toISOString(), entries });
}

// ---- embedded mode: capabilities and platform rules for provider settings
{
    const builder = web('services/clientBuilderService');
    const worker = web('services/clientTemplateWorker');
    const settings = [
        { command: '', actions: '', token: '', targets: undefined },
        { command: 'trusted-remote-provider', targets: undefined },
        { command: 'trusted-remote-provider', targets: JSON.stringify({ targets: [{ platform: 'windows', arch: 'x64', format: 'portable' }, { platform: 'linux', arch: 'arm64', format: 'deb' }, { platform: 'not-supported' }] }) },
        { command: 'p', targets: JSON.stringify([{ os: 'Linux', arch: 'X64' }, { platform: 'macos', arch: 'arm64' }, { platform: 'linux', arch: 'x64', format: 'portable' }]) },
        { command: 'p', targets: '[]' },
        { command: 'p', targets: '{"targets":5}' },
        { command: 'p', targets: 'not json' },
        { command: 'p', targets: JSON.stringify(['x', null, 5, { platform: 'windows', arch: 'arm64' }]) },
        { command: 'p', file: 'missing.json' },
        { command: 'p', file: 'targets.json', fileContent: JSON.stringify([{ platform: 'linux', arch: 'x64', format: 'rpm' }]) },
        { command: '', actions: 'on', token: 't0ken-made-up', targets: JSON.stringify([{ platform: 'linux', arch: 'x64' }]) },
        { command: '', actions: 'OFF', token: 't0ken-made-up', targets: JSON.stringify([{ platform: 'linux', arch: 'x64' }]) },
        { command: '', actions: 'on', token: '', targets: JSON.stringify([{ platform: 'linux', arch: 'x64' }]) },
    ];
    const selections = [
        undefined, null, [], 'all', [{}], [{ platform: 'windows' }], [{ platform: 'windows', arch: 'x64', format: 'portable' }],
        [{ os: 'linux', arch: 'arm64', format: 'deb' }, { platform: 'linux', arch: 'arm64', format: 'deb' }],
        [{ platform: 'linux', arch: 'x64', format: 'rpm' }, { platform: 'beos' }], [{ platform: 'macos', arch: 'arm64' }], [null, 5],
    ];
    const cases = [];
    for (const s of settings) {
        const set = (k, v) => { if (v === undefined || v === '') delete process.env[k]; else process.env[k] = v; };
        set('BETTERDESK_CLIENT_BUILD_COMMAND', s.command);
        set('BETTERDESK_CLIENT_GITHUB_ACTIONS', s.actions);
        set('BETTERDESK_GITHUB_TOKEN', s.token);
        set('BETTERDESK_CLIENT_PROVIDER_TARGETS_JSON', s.targets);
        if (s.file) {
            const file = path.join(scratch, s.file);
            if (s.fileContent !== undefined) fs.writeFileSync(file, s.fileContent);
            process.env.BETTERDESK_CLIENT_PROVIDER_TARGETS_FILE = file;
        } else {
            delete process.env.BETTERDESK_CLIENT_PROVIDER_TARGETS_FILE;
        }
        const caps = builder.getBuildCapabilities();
        const c = {
            settings: { ...s, file: s.file ? path.join('<SCRATCH>', s.file) : undefined },
            capabilities: JSON.stringify(caps).split(scratch).join('<SCRATCH>'),
            platforms: JSON.stringify(worker.getPlatformStatus()),
            available: JSON.stringify(worker.getAvailablePlatforms()),
            selections: selections.map((sel) => {
                const r = worker.validatePlatformSelection(sel);
                return { given: sel !== undefined && sel !== null, selection: JSON.stringify(sel === undefined ? null : sel), valid: r.valid, error: r.error || '', details: r.details || [], selected: (r.selected || []).map((p) => `${p.platform}/${p.arch}/${p.format}`) };
            }),
            failedFor: worker._internals.failedPlatformsForError('local_builder_x', { platform: 'linux', arch: 'x64', format: 'portable' }).length
                + 100 * worker._internals.failedPlatformsForError('betterdesk_client_build_failed:1', { platform: 'linux', arch: 'x64', format: 'portable' }).length,
        };
        cases.push(c);
    }
    write('embedded_platforms', cases);
}

// ---- a provider's manifest
{
    const worker = web('services/clientTemplateWorker');
    const cases = [];
    const root = fs.mkdtempSync(path.join(scratch, 'manifest-'));
    const asset = (name, content, platform, arch, format) => {
        fs.writeFileSync(path.join(root, name), content);
        return { name, platform, arch, format, size: Buffer.byteLength(content), sha256: crypto.createHash('sha256').update(content).digest('hex') };
    };
    const good = [asset('a-win.zip', 'win', 'windows', 'x86_64', 'portable'), asset('a-lin.tar.gz', 'linux', 'linux', 'x86_64', 'portable'), asset('a-lin.deb', 'deb', 'linux', 'x86_64', 'deb')];
    const base = { schema_version: 1, sku: 'betterdesk-support', external_config: false, client_commit: 'A'.repeat(40), assets: good };
    const variants = [
        ['good', base], ['schema', { ...base, schema_version: 2 }], ['sku', { ...base, sku: 'x' }], ['external', { ...base, external_config: true }],
        ['external-missing', { ...base, external_config: undefined }], ['commit', { ...base, client_commit: 'b'.repeat(40) }],
        ['no-assets', { ...base, assets: [] }], ['assets-text', { ...base, assets: 'x' }],
        ['bad-name', { ...base, assets: [{ ...good[0], name: '../x.zip' }] }], ['dup-name', { ...base, assets: [good[0], good[0]] }],
        ['dup-target', { ...base, assets: [good[0], { ...good[1], platform: 'windows', format: 'portable' }] }],
        ['bad-target', { ...base, assets: [{ ...good[0], platform: 'beos' }] }], ['upper-target', { ...base, assets: [{ ...good[0], platform: 'WINDOWS', arch: 'X86_64' }] }],
        ['no-sha', { ...base, assets: [{ ...good[0], sha256: undefined }] }], ['size-text', { ...base, assets: [{ ...good[0], size: '3' }] }],
        ['size-zero', { ...base, assets: [{ ...good[0], size: 0 }] }], ['size-frac', { ...base, assets: [{ ...good[0], size: 1.5 }] }],
        ['missing-file', { ...base, assets: [{ ...good[0], name: 'nope.zip' }] }], ['size-mismatch', { ...base, assets: [{ ...good[0], size: 99 }] }],
        ['sha-mismatch', { ...base, assets: [{ ...good[0], sha256: 'c'.repeat(64) }] }], ['null-asset', { ...base, assets: [null] }],
        ['format-missing', { ...base, assets: [{ ...good[0], format: undefined }] }],
    ];
    const required = [[], [{ platform: 'windows', arch: 'x64', format: 'portable' }], [{ platform: 'linux', arch: 'arm64', format: 'portable' }]];
    (async () => {
        for (const [name, manifest] of variants) {
            fs.writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(manifest));
            for (const bundleCommit of [null, 'a'.repeat(40)]) {
                for (const req of required) {
                    let result;
                    try {
                        await worker._internals.validateEmbeddedManifest(root, { _bundle: { client_commit: bundleCommit } }, req);
                        result = 'ok';
                    } catch (err) {
                        result = err.message;
                    }
                    cases.push({ name, manifest: JSON.stringify(manifest), bundleCommit, required: req, result });
                }
            }
        }
        for (const text of ['not json', '', 'null']) {
            fs.writeFileSync(path.join(root, 'manifest.json'), text);
            let result;
            try {
                await worker._internals.validateEmbeddedManifest(root, {}, []);
                result = 'ok';
            } catch (err) {
                result = err.message.split(':')[0];
            }
            cases.push({ name: `text ${text}`, text, result });
        }
        const finds = [];
        fs.writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(base));
        for (const [platform, arch, format] of [['windows', 'x64', 'portable'], ['linux', 'x64', 'portable'], ['linux', 'x64', 'deb'], ['linux', 'amd64', 'deb'], ['macos', 'arm64', 'portable'], ['LINUX', 'X64', 'portable'], ['linux', 'x64', 'rpm']]) {
            let result;
            try {
                result = path.relative(root, worker._internals.findManifestArtifact(root, base, platform, arch, format));
            } catch (err) {
                result = `error ${err.message}`;
            }
            finds.push({ platform, arch, format, result });
        }
        write('embedded_manifests', { cases, finds, assets: good.map((a) => ({ name: a.name, content: fs.readFileSync(path.join(root, a.name), 'utf8') })) });
        fs.rmSync(scratch, { recursive: true, force: true });
    })();
}
