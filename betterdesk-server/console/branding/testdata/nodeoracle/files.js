'use strict';

/**
 * Everything that touches files: the font service, the uploads of logos,
 * backgrounds and fonts (through the real routes of
 * routes/settings.routes.js, with multer), the background list, the
 * built-in themes and the supporters list. Runs on a scratch copy of the
 * console and writes ../files.json.gz. See README.md in this folder.
 *
 *   node files.js
 */

const fs = require('fs');
const http = require('http');
const https = require('https');
const path = require('path');
const { EventEmitter } = require('events');
const lib = require('./lib');
const fontfiles = require('./fontfiles');

const root = lib.scratchConsole('branding-files');
const DATA = path.join(root, 'data');
const FONTS = path.join(root, 'public', 'fonts');
const UPLOADS = path.join(DATA, 'uploads');
const CACHE = path.join(DATA, 'google-fonts-cache.json');
const THEMES = path.join(root, 'themes');
const SUPPORTERS = path.join(root, 'config', 'supporters.json');
process.env.NODE_ENV = 'test';
process.env.TZ = 'UTC';
process.env.DATA_DIR = DATA;
process.chdir(root);

// ---- fakes ----

// The settings table in memory, and a record of the audit entries
const rows = new Map();
const audit = [];
lib.stub(root, 'services/database.js', new Proxy({
    getBrandingConfig: async () => [...rows.entries()].map(([key, value]) => ({ key, value })),
    saveBrandingConfigBatch: async (entries) => { for (const { key, value } of entries) rows.set(key, value); },
    resetBrandingConfig: async () => { rows.clear(); },
    getBrandingConfigRevision: async () => 'rev',
    logAction: async (userId, action, details) => { audit.push([action, details]); },
}, {
    get(target, prop) {
        if (prop in target) return target[prop];
        if (prop === 'then') return undefined;
        return async () => undefined;
    },
}));

// Google's servers: canned answers per URL, and a record of what was asked
const remote = new Map();
const requests = [];
function fakeGet(protocol) {
    return (url, options, callback) => {
        const req = new EventEmitter();
        req.setTimeout = () => {};
        req.destroy = () => {};
        if (typeof url !== 'string' || !/^https?:\/\//.test(url)) throw new TypeError('Invalid URL');
        requests.push({ url, protocol, userAgent: options && options.headers && options.headers['User-Agent'] });
        const answer = remote.get(url) || { status: 404, body: 'not found' };
        setImmediate(() => {
            if (answer.error) { req.emit('error', new Error(answer.error)); return; }
            const res = new EventEmitter();
            res.statusCode = answer.status || 200;
            res.headers = { 'content-type': answer.type || 'text/css', ...(answer.location ? { location: answer.location } : {}) };
            callback(res);
            setImmediate(() => {
                if (answer.body !== undefined) res.emit('data', Buffer.isBuffer(answer.body) ? answer.body : Buffer.from(answer.body));
                res.emit('end');
            });
        });
        return req;
    };
}
https.get = fakeGet('https');
http.get = fakeGet('http');

const fontService = require(path.join(root, 'services/fontService'));
const branding = require(path.join(root, 'services/brandingService'));
const supporters = require(path.join(root, 'services/supportersService'));

// ---- helpers ----

const HEX = /\b(logo|bg)-[0-9a-f]{16}/g;
const TMP = /font-upload-\d+/g;
const maskNames = (text) => text.replace(HEX, '$1-<HEX>').replace(TMP, 'font-upload-<MS>');

/**
 * Every file below dir as a sorted list of lines "rel/path = content":
 * the text of a small file, length and hash of a bigger or binary one,
 * <dir> and <link> for folders and links. Random parts of names are
 * masked, so two uploads can give the same line twice.
 */
function tree(dir, base = dir, out = []) {
    if (fs.existsSync(dir)) {
        for (const name of fs.readdirSync(dir)) {
            const full = path.join(dir, name);
            const rel = maskNames(path.relative(base, full).split(path.sep).join('/'));
            const st = fs.lstatSync(full);
            if (st.isSymbolicLink()) out.push(`${rel} = <link>`);
            else if (st.isDirectory()) { out.push(`${rel}/ = <dir>`); tree(full, base, out); } else {
                const data = fs.readFileSync(full);
                const small = data.length <= 300 && !data.includes(0);
                out.push(`${rel} = ${small ? JSON.stringify(data.toString('utf8')) : `${data.length} bytes ${require('crypto').createHash('sha256').update(data).digest('hex')}`}`);
            }
        }
    }
    return dir === base ? out.sort() : out;
}

/** Lists whose order is the directory's own order are recorded sorted. */
function sortFonts(value) {
    if (Array.isArray(value)) {
        for (const item of value) if (item && Array.isArray(item.weights)) item.weights.sort();
        if (value.every((item) => item && typeof item.safeName === 'string')) value.sort((a, b) => (a.safeName < b.safeName ? -1 : a.safeName > b.safeName ? 1 : 0));
    } else if (value && Array.isArray(value.weights)) value.weights.sort();
    return value;
}

async function answer(fn) {
    try {
        const value = sortFonts(await fn());
        const text = JSON.stringify(value);
        return text === undefined ? { u: true } : { r: maskNames(text) };
    } catch (err) {
        return { e: maskNames(String(err && err.message)).split(root).join('<ROOT>'), n: String(err && err.name) };
    }
}

const fixtures = fontfiles.fixtures();
const b64 = (buf) => buf.toString('base64');

const CSS_INTER = `/* cyrillic */
@font-face {
  font-family: 'Inter';
  font-weight: 400;
  src: url(https://fonts.gstatic.com/s/inter/v1/cyr-400.woff2) format('woff2');
  unicode-range: U+0400-045F;
}
/* latin-ext */
@font-face {
  font-family: 'Inter';
  font-weight: 400;
  src: url(https://fonts.gstatic.com/s/inter/v1/latin-ext-400.woff2) format('woff2');
}
/* latin */
@font-face {
  font-family: 'Inter';
  font-weight: 400;
  src: url(https://fonts.gstatic.com/s/inter/v1/latin-400.woff2) format('woff2');
}
/* latin */
@font-face {
  font-family: 'Inter';
  font-weight:700;
  src: url(https://fonts.gstatic.com/s/inter/v1/latin-700.woff2) format('woff2');
}
/* latin */
@font-face {
  font-family: 'Inter';
  src: url(https://fonts.gstatic.com/s/inter/v1/latin-none.woff2) format('woff2');
}
/* latin */
@font-face {
  font-weight: 900;
  src: url(https://fonts.gstatic.com/s/inter/v1/latin-missing.woff2) format('woff2');
}
/* latin */
@font-face { font-weight: 500; src: url(http://fonts.gstatic.com/plain.woff2) format('woff2'); }
`;

const GREEK_CAPITALS = String.fromCharCode(0x39f, 0x394, 0x39f, 0x3a3); // "odos" in capitals, ends in a sigma
const DOTTED_I = String.fromCharCode(0x130);
const SHARP_S = String.fromCharCode(0xdf);

/** The steps on the font service. Each is recorded with the fonts folder after it. */
function fontSteps() {
    const css = (family, weights) => `https://fonts.googleapis.com/css2?family=${encodeURIComponent(family)}:wght@${weights}&display=swap`;
    const steps = [];
    const call = (fn, ...args) => steps.push({ op: 'call', fn, args });
    const searches = [['', 'all'], ['inter', 'all'], ['INTER ', 'sans-serif'], ['mono', 'monospace'], ['', 'serif'], ['zzz', 'all'], ['a', ''], ['\u212a', 'all'], ['SANS', 'display'],
        ['My Own Font', 'all'], ['x', 'all'], ["O'Neil-Sans 2.0", 'all'], ['9lives', 'serif'], ['-dash', 'all'], ['a'.repeat(62), 'all'], ['a'.repeat(61), 'all'], ['Roboto', 'all'], ['roboto mono', 'all'], ['  Lato  ', 'all'], ['caf\u00e9', 'all'], ['Zeta', 'all']];
    call('listLocalFonts');
    for (const [q, c] of searches.slice(0, 9)) call('searchFonts', q, c);
    for (const [q, c] of searches) call('searchGoogleFonts', q, c);
    for (const name of ['Inter', 'inter', ' Inter ', 'Nope', '', null, '\u4f60\u597d', 'a/b', '..', 5, ['Inter'], { toString: 1 }]) {
        call('isFontDownloaded', name);
        call('getLocalFont', name);
    }

    // downloads
    steps.push({ op: 'remote', url: css('Inter', '400;700'), answer: { body: CSS_INTER } });
    steps.push({ op: 'remote', url: 'https://fonts.gstatic.com/s/inter/v1/latin-400.woff2', answer: { body: b64(fixtures['valid.woff2']), base64: true } });
    steps.push({ op: 'remote', url: 'https://fonts.gstatic.com/s/inter/v1/latin-700.woff2', answer: { status: 302, location: 'https://cdn.example/moved-700.woff2' } });
    steps.push({ op: 'remote', url: 'https://cdn.example/moved-700.woff2', answer: { body: 'seven hundred' } });
    steps.push({ op: 'remote', url: 'https://fonts.gstatic.com/s/inter/v1/latin-none.woff2', answer: { body: 'no weight given' } });
    steps.push({ op: 'remote', url: 'https://fonts.gstatic.com/s/inter/v1/latin-missing.woff2', answer: { status: 404, body: 'gone' } });
    call('downloadFont', 'Inter', ['400', '700', '950', 400]);
    call('downloadFont', 'Inter', ['950']);
    steps.push({ op: 'remote', url: css('Inter', '300'), answer: { status: 500, body: 'oops' } });
    call('downloadFont', 'Inter', ['300']);
    call('downloadFont', 'Roboto', []);
    steps.push({ op: 'remote', url: css('My Own Font', '400;700'), answer: { body: 'body { } /* latin */ @font-face { src: url(https://fonts.gstatic.com/x/own.woff2) }' } });
    steps.push({ op: 'remote', url: 'https://fonts.gstatic.com/x/own.woff2', answer: { error: 'socket hang up' } });
    call('downloadFont', 'My Own Font', ['abc', 40, '4000']);
    steps.push({ op: 'remote', url: css('Caf\u00e9 & Co', '100;200'), answer: { status: 301, location: 'https://fonts.googleapis.com/redirected' } });
    steps.push({ op: 'remote', url: 'https://fonts.googleapis.com/redirected', answer: { body: '/* latin */ @font-face { font-weight: 100; src: url(https://fonts.gstatic.com/c/cafe.woff2) }' } });
    steps.push({ op: 'remote', url: 'https://fonts.gstatic.com/c/cafe.woff2', answer: { body: 'cafe' } });
    call('downloadFont', 'Caf\u00e9 & Co', ['100', 200, [300], 'x']);
    call('downloadFont', '\u4f60\u597d', ['400']);
    call('downloadFont', 'Lone \ud83d', ['400']);
    call('downloadFont', 'Inter', '400');
    call('downloadFont', 'Other', [{ toString: 1 }]);
    steps.push({ op: 'remote', url: css('Relative', '400;700'), answer: { status: 302, location: '/elsewhere' } });
    call('downloadFont', 'Relative', ['x']);

    // uploads straight into the service
    const upload = (family, fixture, fileName) => steps.push({ op: 'register', family, fixture, fileName });
    upload('Bdtest', 'valid.ttf', 'font-upload-1.ttf');
    upload('Bdtest Web', 'valid.woff2', 'font-upload-2.woff2');
    upload('  Padded  Name  ', 'valid.otf', 'x.TTF');
    upload('Not A Font', 'picture.png', 'x.woff2');
    upload("Quote's \"Font\"\\ \u0001", 'text.ttf', 'x.ttf');
    upload('', 'valid.woff', 'x.woff');
    upload('   ', 'valid.ttf', 'x.ttf');
    upload(null, 'valid.ttf', 'noext');
    upload('\u4f60\u597d', 'valid.ttf', 'x.ttf');
    upload('L'.repeat(90), 'empty.woff2', 'x.woff2');
    upload('Inter', 'truncated.ttf', 'x.ttf');
    upload(42, 'valid.ttf', 'x.ttf');
    upload('Gone', 'missing-fixture', 'x.ttf');

    // folders made by hand
    steps.push({ op: 'write', file: 'Hand Made/font.css', text: '/* by hand */' });
    steps.push({ op: 'write', file: 'Hand Made/x-300.woff2', text: 'w' });
    steps.push({ op: 'write', file: 'Hand Made/x-italic.woff2', text: 'w' });
    steps.push({ op: 'write', file: 'roboto/font.css', text: '/* roboto */' });
    steps.push({ op: 'write', file: 'roboto/roboto-400.woff2', text: 'w' });
    steps.push({ op: 'write', file: 'roboto/roboto-700.woff2', text: 'w' });
    steps.push({ op: 'write', file: 'broken-meta/font.css', text: 'x' });
    steps.push({ op: 'write', file: 'broken-meta/meta.json', text: '{' });
    steps.push({ op: 'write', file: 'odd-meta/font.css', text: 'x' });
    steps.push({ op: 'write', file: 'odd-meta/meta.json', text: '{"family": 7, "category": ["a"], "extra": true}' });
    steps.push({ op: 'write', file: 'null-meta/font.css', text: 'x' });
    steps.push({ op: 'write', file: 'null-meta/meta.json', text: 'null' });
    steps.push({ op: 'write', file: 'no-css/readme.txt', text: 'x' });
    steps.push({ op: 'write', file: 'a-file', text: 'not a folder' });
    steps.push({ op: 'link', file: 'linked', target: 'roboto' });
    call('listLocalFonts');
    for (const name of ['Roboto', 'Hand Made', 'hand made', 'a file', 'A-File', 'linked', 'broken meta', 'Bdtest', 'bdtest web', 'no css', 'Not A Font']) {
        call('isFontDownloaded', name);
        call('getLocalFont', name);
    }
    for (const [q, c] of searches.slice(0, 6)) call('searchFonts', q, c);
    for (const [h, b] of [['', ''], ['Inter', ''], ['Inter', 'Inter'], ['Inter', 'Roboto'], [' Roboto ', ' Inter '], ['Nope', 'Bdtest'], ["Bad');}\nbody{color:red}", 'Body\\Font'], ['  ', 'Hand Made'],
        ['\u4f60\u597d', ''], ['', '\u4f60\u597d'], ['Inter', '\u4f60\u597d'], [5, ''], ['', ['x']], [null, undefined], ['a file', ''], ['A\u0000B', 'C\u007fD'], ['</style>', '}{']]) call('generateFontCss', h, b);

    // the cache of further families
    const caches = [
        '[{"family":"Zeta Sans","category":"sans-serif","variants":["400"]},{"family":"Inter","category":"sans-serif"},{"family":"Eta Serif","category":"serif","downloaded":"x","extra":1}]',
        '{"family":"Not A List"}', '[', '', 'null', '\ufeff[]',
        '[{"family":"Zeta Sans"},null,{"family":"After Null"}]',
        '[{"family":"Zeta Sans"},{"family":5},{"family":"After Number"}]',
        '[{"family":"Zeta Sans"},"text",{"family":"After Text"}]',
        '[{"family":"\u4f60\u597d"},{"family":"After Unnameable"}]',
        '[{"family":"Roboto","category":"x"},{"family":"ROBOTO"},{"family":"Hand Made","category":"serif"},{"category":"serif"}]',
        `[${Array.from({ length: 120 }, (_, i) => `{"family":"Cache Font ${i}","category":"display"}`).join(',')}]`,
        // names that lower-case in more than one way: a Greek word in capitals (final sigma), a dotted capital I, a sharp s
        JSON.stringify([{ family: `${GREEK_CAPITALS} Sans` }, { family: `${DOTTED_I}stanbul Display` }, { family: `Stra${SHARP_S}e` }, { family: `${GREEK_CAPITALS.toLowerCase()} sans` }]),
    ];
    for (const text of caches) {
        steps.push({ op: 'cache', text });
        for (const [q, c] of [['', 'all'], ['zeta', 'all'], ['', 'serif'], ['a', 'display'], ['After', 'all'], ['Roboto', 'all'],
            [GREEK_CAPITALS.toLowerCase(), 'all'], [GREEK_CAPITALS, 'all'], [`${DOTTED_I}STANBUL`, 'all'], ['istanbul', 'all'], [`STRA${SHARP_S}`, 'all']]) call('searchGoogleFonts', q, c);
    }
    steps.push({ op: 'cache', text: null });

    for (const name of ['Inter', 'inter', 'Nope', 'a file', 'linked', '', '..', 'Hand Made', null]) call('deleteLocalFont', name);
    call('listLocalFonts');
    steps.push({ op: 'write', file: '../fonts-is-a-file', text: 'x' });
    return steps.map((s) => ({ ...s, ...(s.args ? { args: s.args.map((a) => lib.encodeArg(lib.jsonClone(a))) } : {}) }));
}

async function runFontSteps() {
    const out = [];
    let counter = 0;
    for (const step of fontSteps()) {
        let result = { u: true };
        const before = requests.length;
        switch (step.op) {
            case 'call': {
                const args = step.args.map((a) => (a && a.$undefined ? undefined : a));
                result = await answer(() => fontService[step.fn](...args));
                break;
            }
            case 'remote': {
                const a = { ...step.answer };
                if (a.base64) a.body = Buffer.from(a.body, 'base64');
                remote.set(step.url, a);
                break;
            }
            case 'register': {
                const tmp = path.join(DATA, 'tmp', `in-${++counter}`);
                fs.mkdirSync(path.dirname(tmp), { recursive: true });
                if (fixtures[step.fixture]) fs.writeFileSync(tmp, fixtures[step.fixture]);
                result = await answer(() => fontService.registerUploadedFont(step.family, tmp, step.fileName));
                result.tmpLeft = fs.existsSync(tmp);
                if (result.e) result.e = result.e.replace(tmp, '<TMP>').replace(/'[^']*' -> '[^']*'/, "'<FROM>' -> '<TO>'");
                break;
            }
            case 'write':
                fs.mkdirSync(path.dirname(path.join(FONTS, step.file)), { recursive: true });
                fs.writeFileSync(path.join(FONTS, step.file), step.text);
                break;
            case 'link':
                fs.symlinkSync(path.join(FONTS, step.target), path.join(FONTS, step.file));
                break;
            case 'cache':
                if (step.text === null) fs.rmSync(CACHE, { force: true });
                else fs.writeFileSync(CACHE, step.text);
                break;
            default:
                throw new Error(`unknown step ${step.op}`);
        }
        out.push({ step, result, requests: requests.slice(before), fonts: tree(FONTS) });
    }
    return out;
}

// ---- uploads through the routes ----

async function runUploads() {
    const express = require(path.join(lib.WEB, 'node_modules/express'));
    const session = require(path.join(lib.WEB, 'node_modules/express-session'));
    const multer = require(path.join(lib.WEB, 'node_modules/multer'));
    const request = require(path.join(lib.WEB, 'node_modules/supertest'));
    const settingsRoutes = require(path.join(root, 'routes/settings.routes'));

    const app = express();
    app.use(express.json());
    app.use(session({ secret: 'oracle', resave: false, saveUninitialized: true, cookie: { secure: false } }));
    app.use((req, _res, next) => {
        req.t = (key) => key;
        req.session.userId = 1;
        req.session.user = { id: 1, username: 'admin', role: 'admin' };
        next();
    });
    // What multer makes of the same upload: the file name and type the routes see
    app.post('/probe', multer({ storage: multer.memoryStorage() }).any(), (req, res) => {
        res.json({ files: (req.files || []).map((f) => ({ field: f.fieldname, originalname: f.originalname, mimetype: f.mimetype, size: f.size })), body: req.body });
    });
    app.use('/', settingsRoutes);

    const big = (n) => Buffer.alloc(n, 0x61);
    const png = fixtures['picture.png'];
    const cases = [];
    const up = (kind, filename, contentType, content, fields = {}, extra = {}) => cases.push({ op: 'upload', kind, filename, contentType, content, fields, ...extra });
    const names = ['logo.png', 'LOGO.PNG', 'photo.JpEg', 'page.html', 'noext', '.hidden', 'a.b.c.SVG', 'weird name.jp g', 'x.', 'x..', '..', 'evil.php', 'archive.tar.gz',
        'sub/dir/pic.png', 'back\\slash.png', '\u00fcn\u00ef.p\u00f1g', '\u4f60\u597d.png', 'trailing.dot.', 'UPPER.WEBP', 'tab\tname.gif', 'semi;colon.png', 'x.\u017fvg', 'q".png', `${'n'.repeat(250)}.png`];
    const types = ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml', 'image/PNG', 'image/bmp', 'image/svg', 'text/html', 'application/octet-stream', 'image/png; charset=x', '', 'image/x-png', 'image/jpeg ', 'IMAGE/JPEG'];
    for (const name of names) up('logo', name, 'image/png', png);
    for (const type of types) up('logo', 'pic.png', type, png);
    up('logo', 'page.html', 'image/svg+xml', fixtures['script.svg']);
    up('logo', 'exact.png', 'image/png', big(2 * 1024 * 1024));
    up('logo', 'over.png', 'image/png', big(2 * 1024 * 1024 + 1));
    up('logo', 'empty.png', 'image/png', Buffer.alloc(0));
    for (const name of names.slice(0, 12)) up('background', name, 'image/jpeg', png);
    for (const type of types) up('background', 'pic.jpg', type, png);
    up('background', 'exact.jpg', 'image/jpeg', big(8 * 1024 * 1024));
    up('background', 'over.jpg', 'image/jpeg', big(8 * 1024 * 1024 + 1));
    for (const target of ['bgImageUrl', 'loginBgImageUrl', 'agentBgImageUrl', 'rdclientBgImageUrl', ' bgImageUrl ', 'logoUrl', 'bgType', 'constructor', '__proto__', 'toString', 'nope', '']) {
        up('background', 'wall.png', 'image/png', png, { target });
    }
    cases.push({ op: 'backgrounds' });

    // the old logo goes when a new one comes, if it is one of ours
    cases.push({ op: 'save', updates: { logoUrl: '$last' } });
    up('logo', 'second.png', 'image/png', png);
    cases.push({ op: 'save', updates: { logoUrl: '$last' } });
    up('logo', 'third.gif', 'image/gif', png);
    cases.push({ op: 'save', updates: { logoUrl: '/uploads/elsewhere.png' } });
    cases.push({ op: 'file', name: 'elsewhere.png', text: 'not ours' });
    up('logo', 'fourth.png', 'image/png', png);
    cases.push({ op: 'file', name: 'logo-0123456789abcdef.png', text: 'ours by name' });
    cases.push({ op: 'save', updates: { logoUrl: 'https://example.com/uploads/logo-0123456789abcdef.png' } });
    up('logo', 'fifth.png', 'image/png', png);
    cases.push({ op: 'save', updates: { logoUrl: '/uploads/logo-0123456789abcdef.png/' } });
    up('logo', 'sixth.png', 'image/png', png);
    cases.push({ op: 'file', name: 'logo-0123456789ABCDEF.SVG', text: 'upper' });
    cases.push({ op: 'save', updates: { logoUrl: '/uploads/sub/logo-0123456789ABCDEF.SVG' } });
    up('logo', 'seventh.png', 'image/png', png);
    cases.push({ op: 'file', name: 'logo-aaaaaaaaaaaaaaaa.html', text: 'wrong type' });
    cases.push({ op: 'save', updates: { logoUrl: '/uploads/logo-aaaaaaaaaaaaaaaa.html' } });
    up('logo', 'eighth.png', 'image/png', png);
    cases.push({ op: 'dir', name: 'logo-bbbbbbbbbbbbbbbb.png' });
    cases.push({ op: 'save', updates: { logoUrl: '/uploads/logo-bbbbbbbbbbbbbbbb.png' } });
    up('logo', 'ninth.png', 'image/png', png);
    cases.push({ op: 'link', name: 'logo-cccccccccccccccc.png', target: 'elsewhere.png' });
    cases.push({ op: 'save', updates: { logoUrl: '/uploads/logo-cccccccccccccccc.png' } });
    up('logo', 'tenth.png', 'image/png', png);

    // the background list: our files only, newest first
    const stamp = (name, seconds) => cases.push({ op: 'mtime', name, seconds });
    for (const [name, seconds] of [['bg-00000000000000A1.png', 1700000000], ['bg-00000000000000A2.JPG', 1700000001.0004], ['bg-00000000000000A3.jpeg', 1700000001.0005], ['bg-00000000000000A4.gif', 1700000001.0006],
        ['bg-00000000000000A5.webp', 1700000001.9996], ['bg-00000000000000A6.svg', 1800000000], ['bg-00000000000000A7.png.bak', 1800000000], ['bg-0000a8.png', 1800000000], ['BG-00000000000000A9.PNG', 1600000000.5],
        ['bg-00000000000000B1.png', 1700000000], ['logo-00000000000000b2.png', 1900000000]]) {
        cases.push({ op: 'file', name, text: name });
        stamp(name, seconds);
    }
    cases.push({ op: 'dir', name: 'bg-00000000000000C1.png' });
    cases.push({ op: 'link', name: 'bg-00000000000000C2.png', target: 'bg-00000000000000A1.png' });
    cases.push({ op: 'backgrounds' });

    // fonts
    const fontUp = (filename, contentType, fixture, family) => up('font', filename, contentType, fixtures[fixture], family === undefined ? {} : { family }, { fixture });
    fontUp('Bdtest.ttf', 'font/ttf', 'valid.ttf', 'Route Font');
    fontUp('Bdtest.TTF', 'application/octet-stream', 'valid.ttf');
    fontUp('web.woff2', 'font/woff2', 'valid.woff2', '  Web Font  ');
    fontUp('web.WOFF2', '', 'otf-in.woff2');
    fontUp('font.otf', 'font/otf', 'valid.otf', 'Otf One');
    fontUp('font.otf', 'font/ttf', 'valid.otf', 'Otf As Ttf');
    fontUp('font.woff', 'font/woff', 'valid.woff', 'Woff One');
    fontUp('font.woff', 'application/font-woff2', 'valid.woff', 'Woff As Woff2');
    fontUp('font.woff', 'application/x-font-ttf', 'valid.woff', 'Woff As XTtf');
    fontUp('noext', 'font/woff2', 'valid.woff2');
    fontUp('picture.png', 'image/png', 'picture.png', 'Png');
    fontUp('picture.ttf', 'image/png', 'picture.png', 'Png Named Ttf');
    fontUp('text.ttf', 'text/plain', 'text.ttf');
    fontUp('page.ttf', 'text/html', 'page.ttf', '<script>');
    fontUp('truncated.ttf', 'font/ttf', 'truncated.ttf', 'Cut');
    fontUp('bad.ttf', 'font/ttf', 'bad-checksum.ttf', 'Bad Sum');
    fontUp('magic.woff2', 'font/woff2', 'wrong-magic.woff', 'Magic');
    fontUp('short.woff2', 'font/woff2', 'truncated.woff2', 'Short');
    fontUp('empty.woff2', 'font/woff2', 'empty.woff2', 'Empty');
    fontUp('.ttf', 'font/ttf', 'valid.ttf');
    fontUp('x.ttf.woff2', 'font/ttf', 'valid.ttf', 'Double');
    fontUp('\u4f60\u597d.ttf', 'font/ttf', 'valid.ttf');
    fontUp('x.ttf', 'font/ttf', 'valid.ttf', '\u4f60\u597d');
    fontUp('x.ttf', 'font/ttf', 'valid.ttf', '   ');
    fontUp('script.svg', 'image/svg+xml', 'script.svg', 'Svg');
    fontUp('font.ttf ', 'FONT/TTF', 'valid.ttf', 'Trailing Space');
    up('font', 'exact.ttf', 'font/ttf', big(5 * 1024 * 1024), { family: 'Exact Size' });
    up('font', 'over.ttf', 'font/ttf', big(5 * 1024 * 1024 + 1), { family: 'Too Big' });

    const routeOf = { logo: ['/api/settings/branding/upload-logo', 'logo'], background: ['/api/settings/branding/upload-background', 'background'], font: ['/api/settings/fonts/upload', 'font'] };
    const out = [];
    let last = '';
    for (const c of cases) {
        const entry = { step: { ...c } };
        const auditBefore = audit.length;
        if (c.op === 'upload') {
            const [route, field] = routeOf[c.kind];
            const send = (url) => {
                let req = request(app).post(url);
                for (const [k, v] of Object.entries(c.fields)) req = req.field(k, v);
                return req.attach(field, c.content, { filename: c.filename, contentType: c.contentType || undefined });
            };
            const probe = (await send('/probe')).body;
            const res = await send(route);
            if (res.body && typeof res.body.url === 'string') last = res.body.url;
            entry.probe = probe.files[0];
            entry.status = res.status;
            entry.body = maskNames(JSON.stringify(res.body).replace(/"revision":"[^"]*"/, '"revision":"<REV>"'));
            // big bodies are described, not stored
            entry.step.content = c.content.length > 4096 ? { fill: 0x61, size: c.content.length } : { base64: b64(c.content) };
        } else if (c.op === 'save') {
            const updates = JSON.parse(JSON.stringify(c.updates).replace('$last', last));
            await branding.saveBranding(updates);
        } else if (c.op === 'file') {
            fs.writeFileSync(path.join(UPLOADS, c.name), c.text);
        } else if (c.op === 'dir') {
            fs.mkdirSync(path.join(UPLOADS, c.name));
        } else if (c.op === 'link') {
            fs.symlinkSync(path.join(UPLOADS, c.target), path.join(UPLOADS, c.name));
        } else if (c.op === 'mtime') {
            fs.utimesSync(path.join(UPLOADS, c.name), c.seconds, c.seconds);
        } else if (c.op === 'backgrounds') {
            const res = await request(app).get('/api/settings/branding/backgrounds');
            entry.status = res.status;
            // files with the same time come in the folder's own order: recorded sorted by time, then name
            const list = (res.body.data || []).map((f) => ({ ...f, name: maskNames(f.name), url: maskNames(f.url) }));
            entry.inTimeOrder = list.every((f, i) => i === 0 || list[i - 1].updatedAt >= f.updatedAt);
            list.sort((a, b) => (a.updatedAt > b.updatedAt ? -1 : a.updatedAt < b.updatedAt ? 1 : a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
            entry.body = JSON.stringify(list.map((f) => (/<HEX>/.test(f.name) ? { ...f, updatedAt: '<T>' } : f)));
        }
        entry.audit = audit.slice(auditBefore).map(([action, details]) => [action, maskNames(String(details))]);
        entry.uploads = tree(UPLOADS);
        entry.logoUrl = maskNames(String(branding.getBranding().logoUrl));
        if (c.kind === 'font') { entry.fonts = tree(FONTS); entry.tmp = [...new Set(tree(path.join(root, 'data', 'tmp')).map((line) => line.split(' = ')[0]))]; }
        out.push(entry);
    }
    return out;
}

// ---- themes and supporters ----

async function runThemes() {
    const request = require(path.join(lib.WEB, 'node_modules/supertest'));
    const express = require(path.join(lib.WEB, 'node_modules/express'));
    const session = require(path.join(lib.WEB, 'node_modules/express-session'));
    const app = express();
    app.use(express.json());
    app.use(session({ secret: 'oracle', resave: false, saveUninitialized: true, cookie: { secure: false } }));
    app.use((req, _res, next) => { req.t = (k) => k; req.session.userId = 1; req.session.user = { id: 1, username: 'admin', role: 'admin' }; next(); });
    app.use('/', require(path.join(root, 'routes/settings.routes')));

    const extra = {
        'broken.json': '{',
        'not-a-theme.json': '{"type":"other","branding":{"appName":"X"}}',
        'no-branding.json': '{"type":"betterdesk-theme"}',
        'null.json': 'null',
        'list.json': '[]',
        'text-branding.json': '{"type":"betterdesk-theme","branding":"x"}',
        'named.json': '{"type":"betterdesk-theme","displayName":"Named Theme","branding":{"appName":"App","appDescription":"Desc","colors":{"bgPrimary":"#000000"}}}',
        'odd.json': '{"type":"betterdesk-theme","displayName":0,"branding":{"appName":7,"appDescription":["d"],"colors":"red","themeMode":"custom","customCss":"body{}"}}',
        'double.json.json': '{"type":"betterdesk-theme","branding":{"appName":"Double"}}',
        'x.jsonfoo.json': '{"type":"betterdesk-theme","branding":{}}',
        'appearance-only.json': '{"type":"betterdesk-theme","appearance":{"identity":{"appName":"From Appearance"}}}',
        'bad-colors.json': '{"type":"betterdesk-theme","branding":{"colors":"red"}}',
        'readme.txt': 'not json',
        'UPPER.JSON': '{"type":"betterdesk-theme","branding":{"appName":"Upper"}}',
    };
    for (const [name, text] of Object.entries(extra)) fs.writeFileSync(path.join(THEMES, name), text);
    fs.mkdirSync(path.join(THEMES, 'folder.json'));

    const out = { files: { ...extra }, real: {}, steps: [] };
    for (const name of fs.readdirSync(path.join(lib.WEB, 'themes'))) out.real[name] = true;
    const list = await request(app).get('/api/settings/themes');
    out.list = { status: list.status, body: JSON.stringify({ ...list.body, data: (list.body.data || []).sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)) }) };
    for (const id of ['default', 'light', 'insolve', 'named', 'odd', 'missing', 'broken', 'not-a-theme', 'no-branding', 'null', 'list', 'text-branding', 'double.json', 'x.jsonfoo', 'appearance-only', 'bad-colors',
        'UPPER', 'folder', 'a b', '..', 'a%2Fb', 'readme.txt', '-', '_', 'x'.repeat(300)]) {
        rows.clear();
        branding.invalidateCache();
        await branding.loadBranding();
        const res = await request(app).post(`/api/settings/themes/${encodeURIComponent(id)}/apply`).send({});
        out.steps.push({ id, status: res.status, body: JSON.stringify(res.body), rows: JSON.stringify([...rows.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1))) });
    }
    return out;
}

function runSupporters() {
    const real = fs.readFileSync(SUPPORTERS, 'utf8');
    const texts = [
        real, '{', '', 'null', '5', '[]', '"text"', '{}',
        '{"honorary":"x","individuals":{"a":1},"links":"y"}',
        '{"honorary":[1,null,{"name":"A","x":[1]}],"individuals":[],"links":{"repo":"https://example.com/r","extra":"e","issues":null}}',
        '{"links":["a","b"],"honorary":[],"other":1}',
        '{"links":{"__proto__":{"x":1},"constructor":"c","sponsorsMd":7}}',
        '\ufeff{"honorary":[]}',
    ];
    const out = [];
    for (const text of texts) {
        fs.writeFileSync(SUPPORTERS, text);
        supporters.resetCache();
        const first = JSON.stringify(supporters.loadSupporters());
        fs.writeFileSync(SUPPORTERS, '{"honorary":[{"name":"changed"}]}');
        out.push({ text, r: first, cached: JSON.stringify(supporters.loadSupporters()) === first });
    }
    fs.rmSync(SUPPORTERS);
    supporters.resetCache();
    out.push({ text: null, r: JSON.stringify(supporters.loadSupporters()), cached: true });
    return out;
}

async function main() {
    const silence = { error: console.error, warn: console.warn };
    console.error = () => {};
    console.warn = () => {};
    const data = { fixtures: Object.fromEntries(Object.entries(fixtures).map(([name, buf]) => [name, b64(buf)])) };
    data.fonts = await runFontSteps();
    data.uploads = await runUploads();
    data.themes = await runThemes();
    data.supporters = runSupporters();
    Object.assign(console, silence);
    const info = lib.writeVectors('files', data);
    console.log(JSON.stringify({ ...info, fontSteps: data.fonts.length, uploads: data.uploads.length, themes: data.themes.steps.length, supporters: data.supporters.length }, null, 1));
    fs.rmSync(root, { recursive: true, force: true });
    process.exit(0);
}

main().catch((err) => { console.error(err); process.exit(1); });
