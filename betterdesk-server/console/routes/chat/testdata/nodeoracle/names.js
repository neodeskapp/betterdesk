'use strict';

// Writes ../names.json.gz: what the Node console's chat routes made of an
// uploaded file's name. See README.md.

const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const consoleDir = process.argv[2] || '/repo/web-nodejs';
const modules = path.join(consoleDir, 'node_modules');
const contentDisposition = require(path.join(modules, 'content-disposition'));
const { mime } = require(path.join(modules, 'send'));

const id = '0123456789abcdef0123456789abcdef';

function attempt(fn) {
    try {
        return fn();
    } catch (err) {
        return { throws: err.message };
    }
}

// One vector: the expressions of routes/chat.routes.js and of res.download
// on the name a client gave its file.
function run(name) {
    const ext = path.extname(name).slice(0, 10);
    const stored = `${id}${ext}`;
    const type = mime.lookup(stored);
    const charset = mime.charsets.lookup(type);
    return {
        name,
        ext,
        fileId: path.basename(stored, path.extname(stored)),
        disposition: attempt(() => contentDisposition(stored)),
        type: type + (charset ? '; charset=' + charset : ''),
    };
}

// A small generator with a fixed seed, so the file is the same on every run.
let seed = 0x51f3a07;
function random(n) {
    seed ^= seed << 13; seed >>>= 0;
    seed ^= seed >>> 17;
    seed ^= seed << 5; seed >>>= 0;
    return seed % n;
}
const pick = (list) => list[random(list.length)];

const fixed = [
    'a', 'note.txt', '', '.', '..', '...', '.bashrc', '.a.b', 'a.', 'a..', 'a.b.', 'a.tar.gz', 'x.TXT', 'x.Html',
    'photo.JPEG', 'clip.webm', 'doc.pdf', 'data.json', 'page.html', 'style.css', 'app.js', 'pic.svg', 'sheet.xlsx',
    'a.verylongextension', 'a.123456789', 'a.1234567890', 'ä.öü', 'a.é', 'a.€', 'a.\u{1F600}', 'a.\u{1F600}\u{1F600}',
    'a.\u{1F600}\u{1F600}\u{1F600}\u{1F600}\u{1F600}\u{1F600}', 'a.x\u{1F600}\u{1F600}\u{1F600}\u{1F600}\u{1F600}',
    'a.b c', 'a."q"', 'a.b\\c', 'a.%41', 'a.%zz', 'a.%4', 'a.b;c', 'a.b,c', "a.b'c", 'a.(b)', 'a.b*c', 'a.b~!', 'a.b=c',
    'a.b?c', 'a.b#c', 'a.b&c', 'a.b+c', 'a.b:c', 'a.b@c', 'a.[b]', 'a.{b}', 'a.<b>', 'a.b|c', 'a.b^c', 'a.b`c',
    'a.\u0080x', 'a.\u009fx', 'a. x', 'a.ÿx', 'a.Āx', 'a.b\tc', 'a.b\x7fc', 'a.b\x1fc', 'a. ', 'a.  x',
    'a/b.c', 'a.b/c', 'a.b/', '/', 'a/.b', 'a/b.', 'a.b//', './a.b', 'a.b/..', 'C:x.txt', 'a:.txt',
    'Ã¤.Ã¶Ã¼', 'report.final.v2.docx', 'archive.tar.gz.enc', '.env.local', 'no extension', 'trailing.dot.',
];

const letters = ['a', 'b', 'B', '1', '4', '.', '.', '.', ' ', '"', '\\', '%', 'é', '\u0085', ' ', '€', '\u{1F600}',
    '-', '_', ';', "'", '(', '*', '~', '\t', 'x', 'z', 'T', 'g'];

const vectors = fixed.map(run);
for (let i = 0; i < 4000; i++) {
    const length = 1 + random(16);
    let name = '';
    for (let j = 0; j < length; j++) name += pick(letters);
    vectors.push(run(name));
}

const file = path.join(__dirname, '..', 'names.json.gz');
fs.writeFileSync(file, zlib.gzipSync(vectors.map((v) => JSON.stringify(v)).join('\n') + '\n', { level: 9 }));
console.log(`${vectors.length} vectors written to ${file}`);
