'use strict';

/**
 * Records what lib/safePath.js of the Node console answers for paths in a
 * small tree with symbolic links, for the Go package console/safepath to
 * replay on the same tree. See README.md.
 *
 *   node vectors.js        writes ../vectors.json
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const safe = require(path.join(WEB, 'lib/safePath'));

// The tree. The Go test builds the same one from this list.
const TREE = [
    ['dir', 'root'],
    ['dir', 'root/sub'],
    ['dir', 'root/sub/deep'],
    ['dir', 'root2'],
    ['dir', 'outside'],
    ['dir', 'rootx'],
    ['file', 'root/file.txt'],
    ['file', 'root/sub/inner.txt'],
    ['file', 'root/sub/deep/leaf.txt'],
    ['file', 'root/..dots'],
    ['file', 'root/.hidden'],
    ['file', 'root2/x.txt'],
    ['file', 'outside/secret.txt'],
    ['file', 'rootx/near.txt'],
    ['link', 'root/link-in', 'sub'],
    ['link', 'root/link-out', '../outside'],
    ['link', 'root/link-file-out', '../outside/secret.txt'],
    ['link', 'root/link-file-in', 'sub/inner.txt'],
    ['link', 'root/dangling', 'nowhere'],
    ['link', 'root/loop', 'loop'],
    ['link', 'rootlink', 'root'],
    ['link', 'outside/back', '../root'],
];

const T = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'safepath-oracle-')));
for (const [kind, rel, target] of TREE) {
    const full = path.join(T, rel);
    if (kind === 'dir') fs.mkdirSync(full, { recursive: true });
    if (kind === 'file') fs.writeFileSync(full, rel);
    if (kind === 'link') fs.symlinkSync(target, full);
}
process.chdir(T);

// <T> stands for the tree's folder in arguments and results
const real = (s) => (typeof s === 'string' ? s.split('<T>').join(T) : s);
const mark = (s) => (typeof s === 'string' ? s.split(T).join('<T>') : s);

const vectors = [];
function call(fn, ...args) {
    const actual = args.map((a) => (Array.isArray(a) ? a.map(real) : real(a)));
    const record = { fn, args };
    try {
        record.result = mark(safe[fn](...actual));
    } catch (err) {
        record.error = err.code ? `fs:${err.code}` : err.message;
    }
    vectors.push(record);
}

const ROOTS = ['<T>/root', '<T>/root/', '<T>/rootlink', '<T>/root/sub/..', '<T>/root/sub', '<T>/missing', '<T>/root/link-out', 'root', './root/sub', '<T>', '/', ''];
const NAMES = ['', '.', '..', '...', '..dots', '.hidden', 'file.txt', 'sub', 'deep', 'new', 'new file', ' ', 'file.txt ', 'sub/', 'a/b', '/a', 'a\\b', '\\', 'a\u0000b',
    '\u0000', 'link-in', 'link-out', 'link-file-out', 'link-file-in', 'dangling', 'loop', '~', '%2e%2e', '..%2f', 'caf\u00e9', '\ud83d\udcce.txt', 'a'.repeat(300), 'CON',
    'file.txt\n', '-rf', '*'];
for (const root of ROOTS) for (const name of NAMES) call('resolveChildPath', root, name);

const PATHS = ['', '\u0000', 'a\u0000b', '<T>/root', '<T>/root/', '<T>/root/file.txt', '<T>/root/missing.txt', '<T>/root/sub/../file.txt', '<T>/root/sub/deep/leaf.txt',
    '<T>/root/../outside/secret.txt', '<T>/root/../root/file.txt', '<T>/root/link-out', '<T>/root/link-out/secret.txt', '<T>/root/link-out/new.txt',
    '<T>/root/link-in/inner.txt', '<T>/root/link-in/new.txt', '<T>/root/link-file-out', '<T>/root/link-file-in', '<T>/root/dangling', '<T>/root/dangling/x',
    '<T>/root/loop', '<T>/root/loop/x', '<T>/root/..dots', '<T>/root/..dots/x', '<T>/root/.hidden', '<T>/root/new/deep/file.txt', '<T>/rootlink/file.txt',
    '<T>/rootlink/sub/inner.txt', '<T>/rootlink', '<T>/rootx/near.txt', '<T>/rootx', '<T>/root2/x.txt', '<T>/outside/secret.txt', '<T>/outside/back/file.txt',
    '<T>/outside/back/sub/../../outside/secret.txt', '<T>', '<T>/', '/', '/etc/passwd', 'root/file.txt', 'root/../outside/secret.txt', './root/sub/inner.txt',
    '../etc/passwd', 'root', '.', '..', 'root//sub///inner.txt', '<T>/root/sub/./deep/../inner.txt', '<T>/root/file.txt/', '<T>/root/file.txt/x', '<T>/ROOT/file.txt',
    '<T>/root/caf\u00e9/x', '<T>/root\\file.txt'];
for (const root of ['<T>/root', '<T>/rootlink', '<T>/root/sub', '<T>/missing', '<T>/root/link-out', 'root', '<T>', '']) {
    for (const p of PATHS) {
        call('resolvePathWithinRoot', p, root);
        call('isPathInsideRoot', p === '' ? '.' : p.split('\u0000').join(''), root);
    }
}
for (const roots of [['<T>/root', '<T>/root2'], ['<T>/missing', '<T>/root2'], ['<T>/root2', '<T>/root/sub'], ['<T>/rootlink'], ['<T>/root/link-out', '<T>/root'], []]) {
    for (const p of PATHS) call('resolvePathWithinAnyRoot', p, roots);
}

const RELATIVE = ['', '\u0000', 'a\u0000b', 'file.txt', 'sub/inner.txt', 'sub\\inner.txt', 'sub\\deep/leaf.txt', '/abs', '\\abs', '//x', 'sub//inner.txt', 'sub/', '/', './sub',
    'sub/./inner.txt', 'sub/../file.txt', '../outside/secret.txt', '..', '.', '...', '..dots', '..dots/x', 'link-out', 'link-out/secret.txt', 'link-in/inner.txt',
    'link-in/new/deeper', 'link-file-out', 'link-file-in', 'dangling', 'dangling/x', 'loop', 'loop/x', 'new/deeper/x.txt', 'sub/deep/leaf.txt', 'sub/deep/../leaf.txt',
    'C:/x', 'C:\\x', ' ', 'a b/c d', '~/x', 'sub/\\/inner.txt', 'file.txt/x', '.hidden'];
for (const root of ['<T>/root', '<T>/rootlink', '<T>/root/sub', '<T>/missing', 'root', '<T>/root/link-out']) {
    for (const rel of RELATIVE) call('resolvePathUnderRoot', root, rel);
}

const file = path.join(__dirname, '..', 'vectors.json');
fs.writeFileSync(file, `${JSON.stringify({ tree: TREE, vectors })}\n`);
console.log(`${file}: ${vectors.length} vectors`);
