'use strict';

/**
 * Records what the old console's services/serverManagement.js answers for
 * the file browser: the folder lists, list, read, write, mkdir, rename and
 * delete, under several settings of the environment, on a small tree with
 * symbolic links, a FIFO, a sparse file and names that need care. After
 * every call that may change the tree it records the whole tree, so the Go
 * test sees what was written, moved and removed too. See README.md.
 *
 *   node files.js           writes ../node_files.json
 *
 * The tree lives in /opt/smoracle, outside every folder the file browser
 * always may read (/tmp, /var/tmp, /var/log), so "outside" means outside.
 * {B} stands for that folder in arguments and results.
 */

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const B = '/opt/smoracle';
const OUT = path.join(__dirname, '..', 'node_files.json');

// The tree each scenario starts from. The Go test builds the same one.
//   [kind, path, extra]  dir (extra: mode), file (extra: {text|hex|repeat, mode}),
//   link (extra: target), sparse (extra: size), fifo
const TREE = [
    ['dir', 'server'],
    ['file', 'server/a.txt', { text: 'hello\n' }],
    ['dir', 'server/sub'],
    ['file', 'server/sub/b.txt', { text: 'bee' }],
    ['dir', 'server/sub/deeper'],
    ['file', 'server/sub/deeper/c.txt', { text: 'sea' }],
    ['link', 'server/link-in', 'sub'],
    ['link', 'server/link-file-in', 'sub/b.txt'],
    ['link', 'server/link-out', '../outside'],
    ['link', 'server/link-out-file', '../outside/secret.txt'],
    ['link', 'server/dangling', 'nothing'],
    ['link', 'server/dangling-out', '../outside/planted.txt'],
    ['link', 'server/loop', 'loop'],
    ['file', 'server/empty.txt', { text: '' }],
    ['file', 'server/bin.dat', { hex: '410042' }],
    ['file', 'server/latin1.txt', { hex: '4772fc df65' .replace(' ', '') }],
    ['file', 'server/broken-utf8.txt', { hex: 'e282 41 f09f98 42 ed a080 43 c0 af 44 f4 90 80 80 45 ff fe 46' .replace(/ /g, '') }],
    // 300 KiB of text with a four-byte character across the 256 KiB cut
    ['file', 'server/big.txt', { repeat: ['a', 262142], tail: 'f09f998262' + '62'.repeat(1000) }],
    ['file', 'server/exact.txt', { repeat: ['z', 262144] }],
    ['sparse', 'server/eight-mb.bin', 8 * 1024 * 1024],
    ['sparse', 'server/too-big.bin', 8 * 1024 * 1024 + 1],
    ['fifo', 'server/pipe'],
    ['file', 'server/private.txt', { text: 'p', mode: 0o600 }],
    ['file', 'server/tool.sh', { text: '#!/bin/sh\n', mode: 0o4755 }],
    ['file', 'server/ü ñ.txt', { text: 'umlaut' }],
    ['file', 'server/back\\slash', { text: 'bs' }],
    ['file', 'server/new\nline', { text: 'nl' }],
    ['file', 'server/..dots', { text: 'dots' }],
    ['file', 'server/.hidden', { text: 'h' }],
    ['dir', 'server/order'],
    ...['alpha', 'Beta', 'beta', 'Älpha', 'zeta', '10', '9', '_x', '-y', 'a b', 'é', 'E', '日本', 'ａ', 'Zdir/', 'adir/', 'Mdir/', '#hash', '~tilde', 'a.b', 'a_b', 'A']
        .map((n) => (n.endsWith('/') ? ['dir', `server/order/${n.slice(0, -1)}`] : ['file', `server/order/${n}`, { text: n }])),
    ['dir', 'console'],
    ['file', 'console/c.txt', { text: 'console' }],
    ['dir', 'home'],
    ['file', 'home/h.txt', { text: 'home' }],
    ['dir', 'tmpdir'],
    ['file', 'tmpdir/t.txt', { text: 'tmp' }],
    ['dir', 'extra'],
    ['file', 'extra/e.txt', { text: 'extra' }],
    ['dir', 'mut'],
    ['file', 'mut/m.txt', { text: 'mut' }],
    ['dir', 'outside'],
    ['file', 'outside/secret.txt', { text: 'secret' }],
    ['dir', 'serverx'],
    ['file', 'serverx/near.txt', { text: 'near' }],
    ['link', 'serverlink', 'server'],
];


function fileBytes(extra) {
    if (extra.text !== undefined) return Buffer.from(extra.text, 'utf8');
    if (extra.hex !== undefined) return Buffer.from(extra.hex, 'hex');
    const head = Buffer.alloc(extra.repeat[1], extra.repeat[0]);
    return extra.tail ? Buffer.concat([head, Buffer.from(extra.tail, 'hex')]) : head;
}

function build() {
    fs.rmSync(B, { recursive: true, force: true });
    fs.mkdirSync(B, { recursive: true });
    for (const [kind, rel, extra] of TREE) {
        const full = path.join(B, rel);
        if (kind === 'dir') fs.mkdirSync(full);
        if (kind === 'file') {
            fs.writeFileSync(full, fileBytes(extra));
            if (extra.mode !== undefined) fs.chmodSync(full, extra.mode);
        }
        if (kind === 'link') fs.symlinkSync(extra, full);
        if (kind === 'sparse') {
            fs.writeFileSync(full, '');
            fs.truncateSync(full, extra);
        }
        if (kind === 'fifo') require('child_process').execFileSync('mkfifo', [full]);
    }
}

// The whole tree: every entry with its kind, size, permission bits and
// for files the SHA-256 of the content, for links the target
function snapshot() {
    const out = [];
    const walk = (dir) => {
        for (const name of fs.readdirSync(dir).sort()) {
            const full = path.join(dir, name);
            const st = fs.lstatSync(full);
            const rel = path.relative(B, full);
            if (st.isSymbolicLink()) out.push([rel, 'link', fs.readlinkSync(full)]);
            else if (st.isDirectory()) { out.push([rel, 'dir', st.mode & 0o7777]); walk(full); }
            else if (st.isFIFO()) out.push([rel, 'fifo']);
            else out.push([rel, 'file', st.size, st.mode & 0o7777, crypto.createHash('sha256').update(fs.readFileSync(full)).digest('hex').slice(0, 16)]);
        }
    };
    if (fs.existsSync(B)) walk(B);
    return out;
}

// An argument is text with {B}, or {repeat: [text, count]} for a long one
const real = (s) => {
    if (s && typeof s === 'object' && s.repeat) return s.repeat[0].repeat(s.repeat[1]);
    return typeof s === 'string' ? s.split('{B}').join(B) : s;
};

// A long content is recorded as its length, the SHA-256 of its UTF-8 and
// its start
function shorten(result) {
    if (!result || typeof result.content !== 'string' || result.content.length <= 2000) return result;
    const { content, ...rest } = result;
    return { ...rest, content: { length: content.length, sha256: crypto.createHash('sha256').update(content, 'utf8').digest('hex'), head: content.slice(0, 100) } };
}
function mark(v) {
    if (typeof v === 'string') return v.split(B).join('{B}');
    if (Array.isArray(v)) return v.map(mark);
    if (v && typeof v === 'object') {
        const o = {};
        for (const [k, x] of Object.entries(v)) o[k] = mark(x);
        return o;
    }
    return v;
}

const ENV_KEYS = ['BETTERDESK_PATH', 'BETTERDESK_CONSOLE_PATH', 'BETTERDESK_FILE_ALLOWED_ROOTS', 'BETTERDESK_MUTABLE_ROOTS', 'HOME', 'TMPDIR', 'TMP', 'TEMP'];

const sm = require(path.join(WEB, 'services/serverManagement.js'));

const scenarios = [];
async function scenario(name, env, cwd, steps) {
    build();
    for (const key of ENV_KEYS) delete process.env[key];
    for (const [key, value] of Object.entries(env)) process.env[key] = real(value);
    process.chdir(real(cwd));
    const record = { name, env, cwd, steps: [] };
    for (const [op, ...args] of steps) {
        const actual = args.map(real);
        const step = { op, args };
        try {
            let result;
            switch (op) {
                case 'roots': result = { read: sm.getAllowedFileRoots(), write: sm.getMutableFileRoots() }; break;
                case 'list': result = await sm.listDirectory(...actual); break;
                case 'read': result = await sm.readFilePreview(...actual); break;
                case 'write': result = await sm.writeFile(...actual); break;
                case 'mkdir': result = await sm.makeDirectory(...actual); break;
                case 'rename': result = await sm.renamePath(...actual); break;
                case 'delete': result = await sm.deletePath(...actual); break;
                default: throw new Error(`unknown op ${op}`);
            }
            step.result = mark(shorten(result));
        } catch (err) {
            step.error = mark(err.message);
        }
        if (['write', 'mkdir', 'rename', 'delete'].includes(op)) step.tree = snapshot();
        record.steps.push(step);
    }
    scenarios.push(record);
}

const STANDARD = {
    BETTERDESK_PATH: '{B}/server',
    BETTERDESK_CONSOLE_PATH: '{B}/console',
    BETTERDESK_FILE_ALLOWED_ROOTS: '{B}/extra',
    BETTERDESK_MUTABLE_ROOTS: '{B}/mut',
    HOME: '{B}/home',
    TMPDIR: '{B}/tmpdir',
};

const READ_PATHS = [
    '{B}/server', '{B}/server/', '{B}/server/.', '{B}/server/a.txt', '{B}/server/sub', '{B}/server/sub/../a.txt', '{B}/server/link-in',
    '{B}/server/link-in/b.txt', '{B}/server/link-file-in', '{B}/server/link-out', '{B}/server/link-out/secret.txt', '{B}/server/link-out-file',
    '{B}/server/dangling', '{B}/server/dangling-out', '{B}/server/loop', '{B}/server/empty.txt', '{B}/server/bin.dat', '{B}/server/latin1.txt',
    '{B}/server/broken-utf8.txt', '{B}/server/big.txt', '{B}/server/exact.txt', '{B}/server/eight-mb.bin', '{B}/server/too-big.bin', '{B}/server/pipe',
    '{B}/server/private.txt', '{B}/server/tool.sh', '{B}/server/ü ñ.txt', '{B}/server/back\\slash', '{B}/server/new\nline', '{B}/server/..dots',
    '{B}/server/.hidden', '{B}/server/order', '{B}/server/missing', '{B}/server/a.txt/x', '{B}/outside', '{B}/outside/secret.txt',
    '{B}/server/../outside/secret.txt', '{B}/serverx', '{B}/serverx/near.txt', '{B}/serverlink/a.txt', '{B}/serverlink', '{B}/home', '{B}/home/h.txt',
    '{B}/tmpdir/t.txt', '{B}/extra/e.txt', '{B}/mut/m.txt', '{B}/console/c.txt', '{B}', '{B}/', 'c.txt', './c.txt', '../server/a.txt',
    '../outside/secret.txt', '', '\u0000', '{B}/server/a.txt\u0000', '/', '/etc', '/etc/hostname', '//', '{B}//server//a.txt',
    '{B}/server/a.txt/', '{B}/server/\ud800.txt', 'nothing-here', ' ', '~', '~/h.txt',
];

(async () => {
    await scenario('standard reads', STANDARD, '{B}/console', [
        ['roots'],
        ...READ_PATHS.map((p) => ['list', p]),
        ...READ_PATHS.map((p) => ['read', p]),
    ]);

    await scenario('roots from the environment', {
        BETTERDESK_PATH: 'server/', BETTERDESK_CONSOLE_PATH: './console/../console', BETTERDESK_FILE_ALLOWED_ROOTS: ' {B}/extra , ,{B}/extra2/ ,{B}/extra,{B}/server',
        BETTERDESK_MUTABLE_ROOTS: ',{B}/mut,{B}/server, mut2 ', HOME: '{B}/home/', TMPDIR: '{B}/tmpdir/',
    }, '{B}', [['roots'], ['read', 'server/a.txt'], ['read', 'mut2'], ['write', 'mut2', 'x'], ['read', 'extra2/x']]);
    await scenario('roots by default', { HOME: '{B}/home', TMPDIR: '{B}/tmpdir' }, '{B}/console', [['roots'], ['read', 'c.txt'], ['write', 'c2.txt', 'x'], ['read', '/opt/rustdesk/nothing']]);
    await scenario('roots, empty values', { BETTERDESK_PATH: '', BETTERDESK_CONSOLE_PATH: '', BETTERDESK_FILE_ALLOWED_ROOTS: '', BETTERDESK_MUTABLE_ROOTS: '', HOME: '{B}/home', TMPDIR: '' }, '{B}/console', [['roots']]);
    await scenario('roots, temp folder from TMP', { HOME: '{B}/home', TMP: '{B}/tmpdir//' }, '{B}/console', [['roots']]);
    await scenario('roots, temp folder from TEMP', { HOME: '{B}/home', TEMP: 'tmpdir' }, '{B}', [['roots'], ['read', 'tmpdir/t.txt'], ['list', '{B}/tmpdir']]);
    await scenario('roots, temp folder /', { HOME: '{B}/home', TMPDIR: '/' }, '{B}', [['roots']]);
    await scenario('roots, the same folder twice', { BETTERDESK_PATH: '{B}/server', BETTERDESK_CONSOLE_PATH: '{B}/server/', BETTERDESK_MUTABLE_ROOTS: '{B}/server,{B}/server/.', HOME: '{B}/server', TMPDIR: '{B}/server' }, '{B}', [['roots']]);

    await scenario('writes', STANDARD, '{B}/console', [
        ['write', '{B}/server/new.txt', 'x'],
        ['write', '{B}/server/a.txt', 'changed'],
        ['write', '{B}/server/unicode.txt', 'Grüße – 🙂\r\n'],
        ['write', '{B}/server/lone.txt', '\ud800'],
        ['write', '{B}/server/empty2.txt', ''],
        ['write', '{B}/console/c2.txt', 'console'],
        ['write', 'c3.txt', 'relative'],
        ['write', '{B}/mut/m2.txt', 'mut'],
        ['write', '{B}/home/h2.txt', 'refused'],
        ['write', '{B}/extra/e2.txt', 'refused'],
        ['write', '{B}/tmpdir/t2.txt', 'refused'],
        ['write', '{B}/outside/x.txt', 'refused'],
        ['write', '{B}/server/../outside/x.txt', 'refused'],
        ['write', '{B}/server/link-out-file', 'refused'],
        ['write', '{B}/server/link-out/x.txt', 'refused'],
        ['write', '{B}/server/link-file-in', 'through a link'],
        ['write', '{B}/server/link-in/via-link.txt', 'through a link'],
        ['write', '{B}/server/dangling', 'creates the target'],
        ['write', '{B}/server/dangling-out', 'creates a file outside'],
        ['write', '{B}/server/loop', 'loop'],
        ['write', '{B}/server/sub', 'onto a folder'],
        ['write', '{B}/server/nodir/x.txt', 'missing folder'],
        ['write', '{B}/server/a.txt/x', 'below a file'],
        ['write', '{B}/server/missing/../a2.txt', 'dots'],
        ['write', '{B}/server/too-big.bin', 'smaller now'],
        ['write', '{B}/server/eight.txt', { repeat: ['e', 8 * 1024 * 1024] }],
        ['write', '{B}/server/over.txt', { repeat: ['e', 8 * 1024 * 1024 + 1] }],
        ['write', '{B}/server/over2.txt', { repeat: ['é', 4 * 1024 * 1024 + 1] }],
        ['write', '{B}/server', 'onto the root'],
        ['write', '{B}/serverx/near.txt', 'refused'],
        ['write', '{B}/serverlink/x.txt', 'through the root link'],
        ['write', '', 'x'],
        ['write', '{B}/server/a\u0000', 'x'],
        ['write', '/etc/smoracle', 'refused'],
        ['write', '/tmp/smoracle', 'refused'],
        ['read', '{B}/server/unicode.txt'],
        ['read', '{B}/server/lone.txt'],
        ['read', '{B}/outside/planted.txt'],
        ['list', '{B}/server'],
    ]);

    await scenario('folders', STANDARD, '{B}/console', [
        ['mkdir', '{B}/server/d1'],
        ['mkdir', '{B}/server/d1'],
        ['mkdir', '{B}/server/x/y'],
        ['mkdir', '{B}/server/a.txt/y'],
        ['mkdir', '{B}/server/a.txt'],
        ['mkdir', '{B}/server'],
        ['mkdir', '{B}/server/link-in'],
        ['mkdir', '{B}/server/dangling-out'],
        ['mkdir', '{B}/server/dangling'],
        ['mkdir', '{B}/server/link-out'],
        ['mkdir', '{B}/server/link-out/new'],
        ['mkdir', '{B}/home/d'],
        ['mkdir', '{B}/mut/d'],
        ['mkdir', 'd-relative'],
        ['mkdir', '{B}/server/d1/./d2/'],
        ['mkdir', ''],
        ['mkdir', '/'],
        ['list', '{B}/server/d1'],
    ]);

    await scenario('renames', STANDARD, '{B}/console', [
        ['rename', '{B}/server/a.txt', '{B}/server/a-renamed.txt'],
        ['rename', '{B}/server/a.txt', '{B}/server/again.txt'],
        ['rename', '{B}/server/sub/b.txt', '{B}/outside/b.txt'],
        ['rename', '{B}/outside/secret.txt', '{B}/server/secret.txt'],
        ['rename', '{B}/server/sub', '{B}/server/sub/deeper/inside'],
        ['rename', '{B}/server/empty.txt', '{B}/server/sub'],
        ['rename', '{B}/server/order', '{B}/server/sub'],
        ['rename', '{B}/server/sub', '{B}/server/empty.txt'],
        ['rename', '{B}/server/bin.dat', '{B}/console/bin.dat'],
        ['rename', '{B}/server/private.txt', '{B}/server/dangling-out'],
        ['rename', '{B}/server/tool.sh', '{B}/server/link-out'],
        ['rename', '{B}/server/link-in', '{B}/server/link-in-renamed'],
        ['rename', '{B}/server/link-file-in', '{B}/server/b-copy.txt'],
        ['rename', 'c.txt', 'c-renamed.txt'],
        ['rename', '{B}/server/latin1.txt', '{B}/server/nodir/latin1.txt'],
        ['rename', '{B}/server/latin1.txt', '{B}/home/latin1.txt'],
        ['rename', '{B}/server/latin1.txt', '{B}/server/latin1.txt'],
        ['rename', '{B}/server/latin1.txt', ''],
        ['rename', '', '{B}/server/x'],
        ['rename', '{B}/server/pipe', '{B}/mut/pipe'],
        ['rename', '{B}/server', '{B}/mut/server-moved'],
        ['list', '{B}/mut'],
    ]);

    await scenario('deletes', STANDARD, '{B}/console', [
        ['delete', '{B}/server/a.txt'],
        ['delete', '{B}/server/a.txt'],
        ['delete', '{B}/server/link-in'],
        ['delete', '{B}/server/link-in'],
        ['delete', '{B}/server/dangling'],
        ['delete', '{B}/server/dangling-out'],
        ['delete', '{B}/server/link-out'],
        ['delete', '{B}/server/link-out-file'],
        ['delete', '{B}/server/link-file-in'],
        ['delete', '{B}/server/loop'],
        ['delete', '{B}/server/pipe'],
        ['delete', '{B}/server/order'],
        ['delete', '{B}/home/h.txt'],
        ['delete', '{B}/outside'],
        ['delete', 'c.txt'],
        ['delete', '{B}/mut/m.txt'],
        ['delete', '/'],
        ['delete', ''],
        ['delete', '{B}/server'],
        ['list', '{B}'],
    ]);

    await scenario('the filesystem root writable', { ...STANDARD, BETTERDESK_MUTABLE_ROOTS: '/' }, '{B}/console', [
        ['roots'], ['delete', '/'], ['delete', '//'], ['delete', '/.'], ['delete', '/opt/..'], ['delete', '/opt/../../..'],
    ]);

    fs.writeFileSync(OUT, JSON.stringify({ tree: TREE, scenarios }, null, 1) + '\n');
    fs.rmSync(B, { recursive: true, force: true });
    const steps = scenarios.reduce((n, s) => n + s.steps.length, 0);
    console.log(`wrote ${OUT}: ${scenarios.length} scenarios, ${steps} steps`);
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
