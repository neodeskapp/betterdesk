'use strict';

/**
 * Shared parts of the branding oracle scripts: a copy of the console's
 * modules in a scratch folder, a seeded random source, generators for
 * settings values (plain, wrong, hostile), and the vector file format.
 * See README.md in this folder.
 */

const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const zlib = require('zlib');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
// ORACLE_OUT writes the vector files somewhere else, for a trial run.
const OUT = process.env.ORACLE_OUT || path.join(__dirname, '..');
// ORACLE_FULL=1 writes every output in full instead of a hash, for looking
// at a difference. The files are then too big to commit.
const FULL = process.env.ORACLE_FULL === '1';

/**
 * Copy the console's code into a scratch folder and return it. The font
 * service and the routes write below their own folder (public/fonts,
 * data/tmp); a copy keeps that out of the repository. node_modules is a
 * link.
 */
function scratchConsole(prefix) {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), `${prefix}-`));
    for (const dir of ['services', 'lib', 'config', 'routes', 'middleware', 'themes', 'lang']) {
        fs.cpSync(path.join(WEB, dir), path.join(root, dir), { recursive: true });
    }
    fs.mkdirSync(path.join(root, 'public'), { recursive: true });
    fs.copyFileSync(path.join(WEB, 'public', 'favicon.svg'), path.join(root, 'public', 'favicon.svg'));
    fs.copyFileSync(path.join(WEB, 'package.json'), path.join(root, 'package.json'));
    fs.symlinkSync(path.join(WEB, 'node_modules'), path.join(root, 'node_modules'));
    fs.mkdirSync(path.join(root, 'data'), { recursive: true });
    return root;
}

/** Replace a module of the scratch console by a fake. */
function stub(root, rel, exports) {
    const file = require.resolve(path.join(root, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports, children: [], paths: [] };
}

/** mulberry32: the same numbers on every run. */
function rng(seed) {
    let a = seed >>> 0;
    const next = () => {
        a = (a + 0x6D2B79F5) >>> 0;
        let t = a;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
    const r = {
        next,
        int: (n) => Math.floor(next() * n),
        pick: (list) => list[Math.floor(next() * list.length)],
        chance: (p) => next() < p,
        some: (list, max) => {
            const n = 1 + r.int(max);
            const out = [];
            for (let i = 0; i < n; i++) out.push(r.pick(list));
            return out;
        },
    };
    return r;
}

const UNDEFINED = { $undefined: true };

/** Arguments as they go into the vector file: undefined gets a marker. */
function encodeArg(v) {
    return v === undefined ? UNDEFINED : v;
}

function sha256(text) {
    return crypto.createHash('sha256').update(Buffer.from(String(text), 'utf8')).digest('hex');
}

/** A recorded text: in full when short, otherwise its length and SHA-256. */
function textOrHash(text, limit = 600) {
    if (FULL || text.length <= limit) return text;
    return { len: text.length, sha256: sha256(text) };
}

/**
 * What a call answered: { r: <JSON text> } for a value ({ u: true } for
 * undefined), { e: message, n: error name } when it threw.
 */
function record(fn, limit) {
    try {
        const value = fn();
        if (value && typeof value.then === 'function') throw new Error('record: use recordAsync');
        return outcome(value, limit);
    } catch (err) {
        return failure(err);
    }
}

async function recordAsync(fn, limit) {
    try {
        return outcome(await fn(), limit);
    } catch (err) {
        return failure(err);
    }
}

function outcome(value, limit) {
    const text = JSON.stringify(value);
    if (text === undefined) return { u: true };
    return { r: textOrHash(text, limit) };
}

function failure(err) {
    return { e: String(err && err.message), n: String(err && err.name) };
}

function writeVectors(name, data) {
    const file = path.join(OUT, `${name}.json.gz`);
    const body = Buffer.from(JSON.stringify(data), 'utf8');
    fs.writeFileSync(file, zlib.gzipSync(body, { level: 9 }));
    return { file, raw: body.length, packed: fs.statSync(file).size };
}

// ---- values ----

const LONE_HIGH = '\ud83d';
const LONE_LOW = '\udc4d';

/** Text that tries to break out of a CSS declaration, a string or a tag. */
const CSS_ATTACKS = [
    'red;}body{display:none}', '#fff}</style><script>alert(1)</script>', 'expression(alert(1))',
    'url(javascript:alert(1))', 'url( "data:text/css,*{x:y}" )', 'expexpression(ression(alert(1))',
    'u\u00a0r l(', 'URL (x)', 'Expression\t(1)', 'red/**/;}', '\\3c script\\3e', '</STYLE >',
    '@import url(//evil.example/x.css);', 'x@import "a";', '"; } * { color: red', "'; } * { color: red",
    'red\n}\nbody{background:url(//evil.example)}', 'rgba(0,0,0,.5)', 'linear-gradient(135deg, #111 0%, #222 100%)',
    'radial-gradient(circle at 10% 20%, rgb(0 0 0 / 50%), #fff)', 'var(--x)', 'calc(1px + 2px)',
    '#FFF', '#12345', '#1234567', 'transparent', 'currentColor', '  #abcdef  ', '#ABCDEF', 'abcdef', '#abcdeg',
    'red\u2028}body{x:y}', 'a\u2029b', '\ufeff#112233', '\u3000#112233\u3000', '\uff45xpression(', 'expre\u017f\u017fion(', 'UR\u212A(',
    '-moz-binding:url(x)', 'behavior:url(x.htc)', '&lt;/style&gt;', '<!--', '-->', '<![CDATA[', ']]>',
];

const ODD_TEXT = [
    '', ' ', '\t\n', 'a', 'null', 'undefined', 'true', 'false', '0', '-0', '1e3', '0x10', 'NaN', 'Infinity', '-Infinity',
    '12px', ' 42 ', '.5', '5.', '-.5e-2x', '1e400', '00012', '+7', '--7', '99999999999999999999999', '0.125', '12.5', '33.335',
    'caf\u00e9', '\u4f60\u597d', '\ud83d\udc4d', LONE_HIGH, LONE_LOW, `a${LONE_HIGH}`, `${LONE_LOW}b`, '\u0000', 'a\u0000b', '\u0001\u001f\u007f',
    '\u00a0nbsp\u00a0', '\u200b', '\u200d', '\ufeff', 'I\u0307', '\u0130', '\u212a', '\u017f', '\u03a3', 'A\u03a3', '\u0391\u03a3 \u0391\u03a3\u0391',
    'constructor', '__proto__', 'toString', 'hasOwnProperty', '[object Object]', '<b>x</b>', '&amp;', '%00', '%2e%2e', '../..', '..',
    'DEFAULT', 'Default', 'default', 'Classic', ' SLIDES ', 'single\n', 'slides\u00a0', 'AUTO', 'auto', 'system', 'custom', 'light', 'dark',
    'cover', 'contain', 'repeat', 'center', 'auto', ' COVER', 'Cover', 'none', 'inherit', 'image', 'gradient', 'color', 'Image',
];

function longText(r) {
    const unit = r.pick(['a', 'ab ', '\ud83d\udc4d', '\u00e9', '#', '<x>', 'on=', '}{', ' url(', '\ud83d']);
    const n = r.pick([99, 100, 101, 119, 120, 121, 399, 400, 401, 499, 500, 501, 999, 1000, 1001, 2500, 19999, 20000, 20001, 50001]);
    let s = unit.repeat(Math.ceil(n / unit.length)).slice(0, n);
    if (r.chance(0.3)) s = '\ud83d\udc4d'.repeat(Math.floor(n / 2)).slice(0, n - 1) + 'x'.repeat(2);
    return s;
}

/** A value of the wrong type for a setting that wants text. */
function wrongType(r) {
    return r.pick([
        null, true, false, 0, 1, -1, 42, 3.5, 1e21, 1e-7, -0, 12.5, [], [1, 2], ['a', ['b', null], 3], [null], [[]], {},
        { a: 1 }, { toString: 1 }, { toString: 'x', valueOf: 2 }, { valueOf: 1 }, [{ toString: 1 }], { length: 2, 0: 'a' },
        JSON.parse('{"__proto__": {"x": 1}}'), JSON.parse('{"constructor": "c", "__proto__": "p"}'),
        [45], ['12px'], [[['7']]], { type: 'image' }, 99999999999, 0.1 + 0.2,
    ]);
}

function anyText(r) {
    const k = r.int(10);
    if (k < 3) return r.pick(ODD_TEXT);
    if (k < 6) return r.pick(CSS_ATTACKS);
    if (k < 7) return longText(r);
    if (k < 9) return r.some([...ODD_TEXT, ...CSS_ATTACKS], 3).join(r.pick(['', ' ', ';', '\n']));
    return r.pick(URL_PARTS.whole);
}

function anyValue(r) {
    return r.chance(0.2) ? wrongType(r) : anyText(r);
}

// ---- URLs ----

const URL_PARTS = {
    scheme: ['http:', 'https:', 'HTTP:', 'hTTpS:', 'http', 'https', 'javascript:', 'data:', 'file:', 'ftp:', 'blob:', 'vbscript:', 'ws:', 'wss:',
        'h\ttp:', 'ht\ntps:', ' https:', '\u0001https:', 'https\u0000:', 'x-y.z+1:', '1http:', 'http\u00a0:', ':', '', 'http\u017f:', 'HTTP\u212A:'],
    slashes: ['//', '//', '//', '/', '', '\\\\', '/\\', '\\/', '///', '////', '\\\\\\'],
    userinfo: ['', '', '', '', 'user@', 'user:pass@', ':@', ':pass@', 'user:@', 'u@v@', 'a:b:c@', 'us er:p@ss@', '%41:%zz@', 'u\u00e9:p\ud83d\udc4d@',
        '@', '@@', 'a/b@', 'a?b@', 'a#b@', 'u;v=w|x[y]^z:{p}`"<>\'@', 'a\\b@'],
    host: ['example.com', 'EXAMPLE.com', 'sub.Example.COM', 'example.com.', 'example..com', '.example.com', 'localhost', 'a', 'a-b_c', 'x--y.com',
        '127.0.0.1', '127.1', '0x7f.1', '0177.0.0.1', '2130706433', '0x7f000001', '1.2.3.4.5', '256.1.1.1', '1.2.3.256', '1.2.3', '1.2.65536', '4294967295', '4294967296',
        '0x', '0x.0x', '1.0x', '08', '09.1', '1.2.3.4.', '1.2.3.4..', '0x100000000', '1e3', '1.e', '0..1', '192.168.0.1.example', '00000000000000000177.1',
        '[::1]', '[::]', '[1:2:3:4:5:6:7:8]', '[1::8]', '[::ffff:1.2.3.4]', '[1:2:3:4:5:6:1.2.3.4]', '[0:0:1:0:0:0:1:0]', '[1:0:0:2:0:0:0:3]', '[FE80::A]',
        '[::1', '::1]', '[]', '[:]', '[:::]', '[1:2:3:4:5:6:7:8:9]', '[1:2:3:4:5:6:7]', '[::1.2.3]', '[::1.2.3.4.5]', '[::01.2.3.4]', '[::256.1.1.1]', '[g::1]', '[::1%25eth0]', '[12345::]', '[1::2::3]',
        'ex%41mple.com', '%65xample.com', 'ex%2eample.com', 'ex%2Fample.com', 'ex%00ample.com', 'ex%20ample.com', 'ex%zzample.com', '%', '%4', 'a%25b', '%31%32%37.1', '%EF%BC%85', '%C3%A9.com', '%FF.com', '%80',
        'ex ample.com', 'ex<ample.com', 'ex>ample', 'ex^ample', 'ex|ample', 'ex[ample', 'ex]ample', 'ex`ample', 'ex{ample}', 'ex"ample', "ex'ample", 'ex!$&()*+,;=~ample', 'ex\u007fample', 'ex\u0001ample',
        '', ' ', '-', '_', '*', '..', '.', 'a.', 'xn-', 'a.xn-', 'x'.repeat(64) + '.com', 'y'.repeat(300), ('z'.repeat(10) + '.').repeat(40) + 'com'],
    port: ['', '', '', '', ':', ':80', ':443', ':8080', ':0', ':65535', ':65536', ':000080', ':00000000000443', ':99999999999999999999', ':8a', ':-1', ':+1', ': 80', ':80:81', ':8\t0'],
    path: ['', '', '/', '/logo.png', '/a/b/c.svg', '/a/../b', '/a/./b', '/..', '/.', '/a/..', '/a/.', '/%2e%2e/x', '/%2E./x', '/.%2e', '/a/%2e', '//', '///x', '/a//b/',
        '\\a\\b', '/a\\..\\b', '/sp ace', '/q"uote', "/q'uote", '/<tag>', '/`tick`', '/{brace}', '/^caret', '/|pipe', '/[x]', '/a;b=c', '/@', '/:', '/~!$&()*+,=',
        '/\u00e9', '/\ud83d\udc4d', `/${LONE_HIGH}`, '/%', '/%zz', '/%41', '/a%2Fb', '/\u007f', '/\u0001', '/tab\there', '/new\nline', '/back\\slash', '/)}body{x:y}', '/a b/c d.png',
        '/' + 'p'.repeat(2000), '/..../x', '/.../x', '/a/%2e%2e%2f', '/%2e%2e%2e'],
    query: ['', '', '', '?', '?a=b', '?a=b&c=d', '?q=sp ace', '?q="x"', "?q='x'", '?q=<x>', '?q=`x`', '?q={x}', '?q=^|[]', '?a?b', '?%zz', '?\u00e9=\ud83d\udc4d', '?a\\b', '?a\tb', '?\u007f\u0001', '?#'],
    fragment: ['', '', '', '#', '#frag', '#a b', '#"x"', '#<x>', '#`x`', '#{x}', '#a#b', '#?x', '#\u00e9', '#%zz', '#\\', "#'", '#^|'],
    whole: ['/uploads/logo-0123456789abcdef.png', '/img/betterdesk_icon.png', '/uploads/bg-0123456789abcdef.jpg', 'uploads/x.png', '//evil.example/x.png', '/a/../b.png', '/a/..b',
        '/', '/ ', ' /x ', '/x\n}\nbody{display:none}', '/x")}body{color:red}', '/x\\"y', '/(x)', "/x'y", '/x\ry', '/x\fy', '/\u2028', '/x y', '/\ud83d\udc4d.png', `/${LONE_HIGH}.png`,
        'javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:image/png;base64,AAAA', 'vbscript:x', 'file:///etc/passwd', 'blob:https://x/y', 'about:blank', 'mailto:a@b',
        'https://example.com/logo.png', 'http://example.com', 'https://example.com:443/', 'http://example.com:80', 'https://EXAMPLE.com/A?B#C', 'http:example.com', 'http:/example.com', 'https:\\\\example.com\\a',
        'https://user:pass@example.com/', 'https://example.com/a b"c', 'https://example.com/(x)', 'https://example.com/?q=)"\\', 'https://example.com/#")', 'https://example.com/\n}x{',
        'https://', 'https://?', 'https://#', 'https:', 'https:///', 'http://@', 'http://:80', 'http://a:b@/', 'http://[::1]:8080/x', 'http://1.2.3.4/', 'http://0x7f.1/', 'http://999.1.1.1/',
        '\u0000https://example.com', 'https://example.com\u0000', '\u001fhttps://x\u001f', 'ht\ttps://exa\nmple.com/a\rb', '   https://example.com   ', 'https://example.com/ ', 'example.com', 'www.example.com/x.png', 'localhost:8080/x', 'C:\\x.png', 'c:/x.png', '?x', '#x', '.', '..', './x'],
};

/** A URL-ish text built from parts, most of them valid in some way. */
function urlText(r) {
    if (r.chance(0.2)) return r.pick(URL_PARTS.whole);
    const p = (name) => r.pick(URL_PARTS[name]);
    let s = p('scheme') + p('slashes') + p('userinfo') + p('host') + p('port') + p('path') + p('query') + p('fragment');
    if (r.chance(0.1)) s = r.pick([' ', '\t', '\n', '\u00a0', '\u0000', '\u001f', '\ufeff']) + s + r.pick([' ', '\n', '\u0000', '\u2028', '']);
    return s;
}

// ---- SVG and CSS ----

const SVG_PARTS = [
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4">', '</svg>', '<rect width="4" height="4"/>', '<circle cx="8" cy="8" r="7" fill="#ff0000"/>',
    '<script>alert(1)</script>', '<SCRIPT >alert(1)</SCRIPT >', '<scr<script>ipt>alert(1)</scr</script>ipt>', '<script/>', '<script src=x />', '< script >x< / script >',
    '<\u017fcript>x</\u017fcript>', '<\u212Aey>', '<scrip\u0074>x</scrip\u0074>', '<script\n>x</script\n>', '<script\u00a0>x</script\u00a0>', '<script\u2028x>y</script>',
    '<style>*{x:y}</style>', '<STYLE>@import "x";</STYLE>', '<style/>', '<st<style>yle>', '<\u017ftyle>x</\u017ftyle>', '<foreignObject><body onload=x/></foreignObject>', '<FOREIGNOBJECT>x</foreignobject>',
    '<iframe src="x"></iframe>', '<embed src=x/>', '<object data=x></object>', '<applet/>', '<animate attributeName="href" to="javascript:x"></animate>', '<set attributeName=x></set>', '<set/>',
    '<use xlink:href="#a"/>', '<use href="data:image/svg+xml,x"></use>', '<image href="http://x/y.png"/>', '<image xlink:href="x"></image>', '<img src=x onerror=alert(1)>',
    '<a href="javascript:alert(1)">x</a>', "<a xlink:href='JAVASCRIPT:alert(1)'>x</a>", '<a href = " data:text/html,x">y</a>', '<a href="vbscript:x">', '<a href="file:///x">', '<a HREF="#ok">',
    '<g onclick="x()">', "<g onmouseover='x'>", '<g onload=x>', '<g\tonfocus\t=\tx >', '<g ON\u212AEY=x>', '<g oNcLiCk=x>', '<g data-on="x">', '<g  onx="1"ony="2">', '<g on="x">', '<g onclick>', '<g onclick=">',
    '<?xml version="1.0"?>', '<?xml-stylesheet href="x"?>', '<?', '?>', '<!DOCTYPE svg [<!ENTITY x "y">]>', '<!doctype html>', '<!DOCTYPE', '<!-- c -->',
    '<g style="width: expression(alert(1))">', '<g style="background:url(javascript:x)">', '<g style="x@import y">', '<g style="a:url( \'data:x\' )">', 'expression', 'xexpression', '-expression', ' @import',
    '<text>caf\u00e9 \ud83d\udc4d</text>', LONE_HIGH, '\u0000', '>', '<', '</', '/>', '<<script>>', '<script', 'script>', '</script', '\n', ' ', 'on=', 'x onerror=1', ' ONERROR = "1"', ' on1=2', ' on\u00e9=2',
];

function svgText(r) {
    if (r.chance(0.05)) return longText(r);
    return r.some(SVG_PARTS, 9).join('');
}

const CUSTOM_CSS_PARTS = [
    'body { color: red; }', '.x{background:url(/img/a.png)}', '@media (min-width: 600px) { .a { b: c } }', '/* note */', ':root{--x:1}', 'a>b{c:d}', 'a > b ~ c + d {}',
    '@import url(https://evil.example/x.css);', '@IMPORT "x";', '@import', '@im@import;port url(x);', '@import\n x', '@importx;', 'x@import y;', '@import;;',
    'width: expression(alert(1));', 'EXPRESSION (1)', 'exprexpression(ession(1)', 'expression\u00a0(', 'behavior: url(x.htc);', '-ms-behavior:url(x)', '-webkit-x-behavior :', 'behaviour:x', 'BEHAVIOR\t:',
    '-moz-binding: url(x.xml#y);', '-MOZ-BINDING :', 'background: url(javascript:alert(1));', 'url( "data:image/png;base64,AA" )', "URL('vbscript:x')", 'url(file:///x)', 'url(https://ok.example/a.png)',
    'url(javascript:x', 'url(  \t"  javascript:a)b)', 'url(data:x))', '</style><script>alert(1)</script>', '<style>', '</STYLE\n>', '< / style >', '<b>', '<', '>', '<>', '<<a>>', '<st<style>yle>', '<scr<x>ipt>',
    '</style', 'a<b', 'c>d', '<a\n>', ' onload=x', ' on1=x', '\tonclick = "y"', " onx='z'", ' onx=', 'only screen and (x)', '\u0000', '\ud83d\udc4d', LONE_LOW, '\n', ' ', '}', '{', ';',
    '\u017ftyle', '<\u017ftyle>', '@\u0131mport', 'e\u0058pression(',
];

function cssText(r) {
    if (r.chance(0.05)) return longText(r);
    return r.some(CUSTOM_CSS_PARTS, 8).join(r.pick(['', ' ', '\n']));
}

// ---- settings ----

const COLOR_KEYS = ['bgPrimary', 'bgSecondary', 'bgTertiary', 'bgElevated', 'textPrimary', 'textSecondary', 'accentBlue', 'accentBlueHover',
    'accentBlueMuted', 'accentGreen', 'accentGreenHover', 'accentGreenMuted', 'accentRed', 'accentRedHover', 'accentRedMuted', 'accentYellow',
    'accentYellowHover', 'accentYellowMuted', 'accentPurple', 'accentPurpleHover', 'accentPurpleMuted', 'borderPrimary', 'borderSecondary'];
const SEMANTIC_KEYS = ['primary', 'primaryHover', 'primaryMuted', 'success', 'successHover', 'successMuted', 'danger', 'dangerHover', 'dangerMuted', 'warning',
    'warningHover', 'warningMuted', 'info', 'infoHover', 'infoMuted', 'surface', 'surfaceRaised', 'surfaceElevated', 'textPrimary', 'textMuted', 'border'];
const SECTIONS = {
    identity: ['appName', 'appDescription', 'logoType', 'logoIcon', 'logoSvg', 'logoUrl', 'logoText', 'logoTextAccent', 'faviconSvg'],
    typography: ['fontHeading', 'fontBody'],
    palette: ['themeMode', 'colors'],
    console: ['bgType', 'bgColor', 'bgGradient', 'bgImageUrl', 'bgBlur', 'bgOverlay', 'bgSize'],
    surfaces: ['glassEnabled', 'glassColor', 'glassBlur', 'glassOpacity'],
    login: ['loginBgType', 'loginBgColor', 'loginBgGradient', 'loginBgImageUrl', 'loginBgOverlay', 'loginTitle', 'loginSubtitle', 'loginPromoMode', 'loginSlides'],
    agent: ['agentBgType', 'agentBgColor', 'agentBgGradient', 'agentBgImageUrl', 'agentShowPoweredBy'],
    rdclient: ['rdclientBgType', 'rdclientBgColor', 'rdclientBgGradient', 'rdclientBgImageUrl', 'rdclientBgOverlay', 'rdclientBgSize'],
    footer: ['footerText', 'showPoweredBy'],
    advanced: ['customCss'],
};
const FLAT_KEYS = [...Object.values(SECTIONS).flat(), 'appearanceSchemaVersion'];
const JUNK_KEYS = ['appearance', 'faviconUrl', 'supportContact', 'constructor', '__proto__', 'toString', 'x', '0', 'loginBgSize', 'agentBgOverlay'];

/** Set a key as JSON.parse does: also "__proto__" becomes a plain key. */
function own(target, key, value) {
    Object.defineProperty(target, key, { value, enumerable: true, writable: true, configurable: true });
}

/** A value as it is after a trip through JSON, which is how it reaches the Go tests. */
function jsonClone(v) {
    return v === undefined ? undefined : JSON.parse(JSON.stringify(v));
}

function hexColor(r) {
    return '#' + [0, 0, 0].map(() => r.int(256).toString(16).padStart(2, '0')).join('');
}

function colorValue(r) {
    const k = r.int(10);
    if (k < 6) return hexColor(r);
    if (k < 7) return r.pick(['', ' ', 'red', 'rgb(1, 2, 3)', 'rgba(1,2,3,.4)', hexColor(r).toUpperCase(), hexColor(r).slice(1), ` ${hexColor(r)} `, '#fff']);
    return anyValue(r);
}

function numberValue(r) {
    const n = r.pick([0, 1, 5, 12.5, 16, 24, 25, 33.335, 40, 41, 45, 55, 89.999, 90, 95, 96, 100, 101, -5, 0.004, 0.005, 0.125, 7.5, 62.5, 37.5, 1e3]);
    const k = r.int(10);
    if (k < 5) return String(n);
    if (k < 7) return n;
    if (k < 8) return `${n}px`;
    return anyValue(r);
}

function slide(r) {
    const s = {};
    if (r.chance(0.8)) s.id = r.chance(0.7) ? r.pick(['free', 'remote', 's1', 'My Slide!', 'a'.repeat(70), 7, '', 'x y', '\u00e9']) : anyValue(r);
    if (r.chance(0.5)) s.slideKey = r.chance(0.7) ? r.pick(['free', 'remote', 'secure', 'platforms', 'FREE', ' Secure ', 'own_key-1', 'k'.repeat(33), 'k'.repeat(32), '\u212aey', '\u0130d', 'a b']) : anyValue(r);
    if (r.chance(0.9)) s.imageUrl = r.chance(0.6) ? r.pick(['/uploads/bg-0123456789abcdef.jpg', '/img/login-slides/free.jpg', 'https://example.com/a.jpg', '']) : (r.chance(0.6) ? urlText(r) : anyValue(r));
    if (r.chance(0.7)) s.title = r.chance(0.7) ? r.pick(['Welcome', 'T'.repeat(121), '\ud83d\udc4d'.repeat(61), '']) : anyValue(r);
    if (r.chance(0.7)) s.body = r.chance(0.7) ? r.pick(['Hello there', 'B'.repeat(401), '']) : anyValue(r);
    if (r.chance(0.7)) s.overlay = numberValue(r);
    if (r.chance(0.1)) s.order = r.pick([5, 'x', null]);
    if (r.chance(0.05)) s.extra = anyValue(r);
    return s;
}

function slidesValue(r) {
    const k = r.int(12);
    if (k < 7) {
        const n = r.pick([0, 1, 1, 2, 3, 4, 9, 10, 11, 15]);
        const list = [];
        for (let i = 0; i < n; i++) list.push(r.chance(0.9) ? slide(r) : r.pick([null, 'x', 5, [], [1], true, {}]));
        return r.chance(0.15) ? JSON.stringify(list) : list;
    }
    if (k < 8) return r.pick(['', '[]', '[', 'null', '{}', '"x"', '[{"imageUrl":"/a.png"}]', '[{"imageUrl":{"toString":1}}]', '[1,2]']);
    return anyValue(r);
}

function colorsValue(r) {
    const k = r.int(12);
    if (k < 8) {
        const out = {};
        const keys = r.chance(0.3) ? COLOR_KEYS : r.some(COLOR_KEYS, 8);
        for (const key of keys) out[key] = colorValue(r);
        if (r.chance(0.15)) own(out, r.pick(['nope', '0', '__proto__', 'constructor', 'bgprimary']), anyValue(r));
        return out;
    }
    if (k < 9) return r.pick([[], ['#fff'], 'red', 5, true, null, '', {}]);
    return anyValue(r);
}

/** A value for one setting: mostly something the setting accepts. */
function settingValue(r, key) {
    if (r.chance(0.12)) return anyValue(r);
    switch (key) {
        case 'colors': return colorsValue(r);
        case 'loginSlides': return slidesValue(r);
        case 'logoSvg': case 'faviconSvg': return r.chance(0.2) ? '' : svgText(r);
        case 'customCss': return r.chance(0.3) ? '' : cssText(r);
        case 'logoUrl': case 'bgImageUrl': case 'loginBgImageUrl': case 'agentBgImageUrl': case 'rdclientBgImageUrl':
            return r.chance(0.5) ? r.pick(['/uploads/logo-0123456789abcdef.png', '/uploads/bg-0123456789abcdef.jpg', 'https://example.com/pic.png', '/img/betterdesk_icon.png', '']) : urlText(r);
        case 'bgColor': case 'loginBgColor': case 'agentBgColor': case 'rdclientBgColor': case 'glassColor':
            return colorValue(r);
        case 'bgGradient': case 'loginBgGradient': case 'agentBgGradient': case 'rdclientBgGradient':
            return r.chance(0.6) ? r.pick(['linear-gradient(135deg, #111111 0%, #222222 100%)', 'radial-gradient(circle, #000, #fff)', '']) : anyText(r);
        case 'bgType': case 'agentBgType': return r.chance(0.85) ? r.pick(['none', 'color', 'gradient', 'image', 'image', 'gradient']) : anyValue(r);
        case 'loginBgType': case 'rdclientBgType': return r.chance(0.85) ? r.pick(['inherit', 'inherit', 'none', 'color', 'gradient', 'image', '']) : anyValue(r);
        case 'bgSize': case 'rdclientBgSize': return r.chance(0.8) ? r.pick(['cover', 'contain', 'repeat', 'center', 'auto', '']) : anyValue(r);
        case 'bgBlur': case 'bgOverlay': case 'glassBlur': case 'glassOpacity': case 'loginBgOverlay': case 'rdclientBgOverlay':
            return numberValue(r);
        case 'glassEnabled': case 'showPoweredBy': case 'agentShowPoweredBy':
            return r.chance(0.8) ? r.pick(['true', 'false', 'true', true, false]) : anyValue(r);
        case 'themeMode': return r.chance(0.85) ? r.pick(['system', 'dark', 'light', 'custom', 'custom', 'auto']) : anyValue(r);
        case 'loginPromoMode': return r.chance(0.85) ? r.pick(['classic', 'single', 'slides', 'single', 'Classic']) : anyValue(r);
        case 'logoType': return r.chance(0.85) ? r.pick(['default', 'icon', 'svg', 'image', 'text']) : anyValue(r);
        case 'logoIcon': return r.chance(0.8) ? r.pick(['dns', 'cloud', '', ' dns ', 'hub']) : anyValue(r);
        case 'fontHeading': case 'fontBody':
            return r.chance(0.8) ? r.pick(['', '', 'Inter', 'Roboto', 'My Font', 'Custom Upload', ' Inter ', "Bad');}\nbody{color:red}", 'Body\\Font', '"q"', 'A\u0000B', 'caf\u00e9 font']) : anyText(r);
        case 'appearanceSchemaVersion': return r.pick(['2', '1', 2, 'x']);
        default:
            return r.chance(0.7) ? r.pick(['BetterDesk', 'Acme Remote', '', 'Support \u00a9 2026', 'Line1\nLine2', '<b>bold</b>', 'x'.repeat(520)]) : anyValue(r);
    }
}

/**
 * A settings object as a client might send it: flat, nested in sections,
 * under "appearance", or a mix, with some keys that are no settings.
 */
function settingsObject(r) {
    const shape = r.int(10);
    if (shape === 0) return r.pick([null, 'x', 5, true, [], [1], {}, { appearance: 'x' }, { appearance: null }, { appearance: [] }, { palette: 'x' }, { palette: { colors: 'red', semantic: 'x' } }]);
    const out = {};
    const count = r.pick([1, 1, 2, 3, 5, 8, 14, 30]);
    const keys = [];
    for (let i = 0; i < count; i++) keys.push(r.pick(FLAT_KEYS));
    if (shape <= 5) {
        for (const key of keys) out[key] = settingValue(r, key);
    } else {
        const nested = {};
        for (const key of keys) {
            const section = Object.keys(SECTIONS).find((s) => SECTIONS[s].includes(key));
            if (!section || r.chance(0.15)) { out[key] = settingValue(r, key); continue; }
            if (r.chance(0.05)) { nested[section] = r.pick([null, 'x', 3, []]); continue; }
            if (!nested[section] || typeof nested[section] !== 'object') nested[section] = {};
            nested[section][key] = settingValue(r, key);
        }
        if (r.chance(0.4)) {
            const palette = (nested.palette && typeof nested.palette === 'object' && !Array.isArray(nested.palette)) ? nested.palette : (nested.palette = {});
            if (r.chance(0.5)) palette.colors = colorsValue(r);
            if (r.chance(0.7)) {
                const semantic = {};
                for (const key of r.some([...SEMANTIC_KEYS, 'constructor', '__proto__', 'toString', 'nope', '0'], 6)) own(semantic, key, colorValue(r));
                palette.semantic = r.chance(0.9) ? semantic : r.pick(['x', [], ['#fff'], null]);
            }
        }
        if (shape <= 7) Object.assign(out, nested);
        else out.appearance = r.chance(0.95) ? nested : r.pick(['x', null, 5, []]);
    }
    if (r.chance(0.15)) own(out, r.pick(JUNK_KEYS), anyValue(r));
    return out;
}

module.exports = {
    WEB, OUT, FULL, UNDEFINED, LONE_HIGH, LONE_LOW,
    scratchConsole, stub, rng, encodeArg, jsonClone, own, sha256, textOrHash, record, recordAsync, writeVectors,
    CSS_ATTACKS, ODD_TEXT, URL_PARTS, SVG_PARTS, CUSTOM_CSS_PARTS, COLOR_KEYS, SEMANTIC_KEYS, SECTIONS, FLAT_KEYS,
    longText, wrongType, anyText, anyValue, urlText, svgText, cssText, hexColor, colorValue, numberValue,
    slide, slidesValue, colorsValue, settingValue, settingsObject,
};
