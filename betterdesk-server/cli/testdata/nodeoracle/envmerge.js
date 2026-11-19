'use strict';

// Records what scripts/merge-env.js and scripts/write-installer-env-subst.js
// of the old console do when run as the installers ran them, into
// ../envmerge.json.gz (replayed by cli/envmerge_test.go), and what
// Object.assign(substitutions, JSON.parse(text)) makes of substitution
// files, into ../substfiles.json.gz.
//
//   node envmerge.js        (in the dev image, the worktree at /repo)

const fs = require('fs');
const path = require('path');
const { REPO, tmpdir, run, replace, fileState, writeVectors } = require('./lib');

const realTemplate = fs.readFileSync(path.join(REPO, '.env.example'));
const templates = {
    real: realTemplate,
    small: '# Console\nPORT=5000\nHOST=0.0.0.0\n\n# Paths\nRUSTDESK_DIR=__RUSTDESK_DIR__\nDB_PATH=__DB_PATH__\nSESSION_SECRET=__SESSION_SECRET__\n',
    crlf: '# A\r\nA=1\r\nB=__B__\r\n\r\n# C\r\nC=__C__',
    oddlines: '  # indented comment\n=novalue\nnoequals\n KEY_SPACE =x\nDUP=1\nDUP=2\n#X=1\nUNICODE=\u00e4\u00f6\u00fc\nEMPTY=\n',
    bom: '\ufeffFIRST=1\nSECOND=__SECOND__\n',
    placeholders: 'A=__A__\nB=__B__ and __A__\nC=__0__\nD=____\nE=__proto__\n',
};

const existing = {
    none: null,
    empty: '',
    blank: '  \n\t\n',
    some: 'PORT=5100\nHOST=127.0.0.1\nSESSION_SECRET=keep-me\nA=kept\n',
    crlf: 'PORT=5100\r\nA=9\r\n',
    notrail: 'A=keep',
    dup: 'A=1\nA=2\n# B=3\n',
    brokenutf8: Buffer.from([0x41, 0x3d, 0xff, 0xfe, 0x0a, 0x42, 0x3d, 0xe2, 0x82, 0x0a]),
    bom: '\ufeffA=1\n',
    directory: 'DIRECTORY',
};

const installer = {
    RUSTDESK_DIR: '/opt/betterdesk', PUB_KEY_PATH: '/opt/betterdesk/id_ed25519.pub',
    API_KEY_PATH: '/opt/betterdesk/.api_key', DB_TYPE: 'sqlite', DB_PATH: '/opt/betterdesk/db_v2.sqlite3',
    DATABASE_URL: '', DATA_DIR: '/opt/BetterDeskConsole/data', GO_API_PORT: '21114',
    HBBS_API_URL: 'http://localhost:21114/api', BETTERDESK_API_URL: 'http://localhost:21114/api', API_PORT: '21121',
    DEFAULT_ADMIN_PASSWORD: 'Adm1n-pass', SESSION_SECRET: 'f'.repeat(64), REMOTE_TARGET_VAULT_KEY: 'k'.repeat(64),
    GUACD_ADDRESS: '127.0.0.1:4822', SSL_CERT_PATH: '/opt/betterdesk/ssl/betterdesk.crt', SSL_KEY_PATH: '/opt/betterdesk/ssl/betterdesk.key',
};

// Substitution files: JSON text or bytes
const substFiles = {
    installer: JSON.stringify(installer),
    special: JSON.stringify({ DEFAULT_ADMIN_PASSWORD: 'p@$$ word&"quoted"=value', DATABASE_URL: 'postgres://user:p%40ss@db:5432/betterdesk?sslmode=disable', SSL_KEY_PATH: 'C:\\BetterDesk\\ssl\\private key.pem', RUSTDESK_DIR: '$HOME/`x`' }),
    types: '{"A":1,"B":true,"C":null,"D":[1,null,"x",[2,3]],"E":{"x":1},"F":1e21,"G":0.1,"H":-0,"I":1e400,"J":"j","K":[],"L":{}}',
    intkeys: '{"b":"1","2":"two","a":"3","1":"one","01":"zero-one","4294967295":"big","4294967294":"max","A":"__"}',
    dupkeys: '{"A":"first","B":"b","A":"second"}',
    proto: '{"__proto__":"x","A":"a"}',
    protoobj: '{"__proto__":{"A":"inherited"},"B":"b"}',
    array: '["x","y"]',
    string: '"ab"',
    astral: '"a\\ud83d\\ude00"',
    number: '42',
    null: 'null',
    bool: 'true',
    invalid: '{"A":',
    trailing: '{"A":"1"} x',
    bomjson: '\ufeff{"A":"1"}',
    unresolved: '{"A":"__STILL__"}',
    unresolvedArray: '{"A":["__X__"]}',
    nullvalue: '{"A":null,"B":"__B__"}',
    unicode: '{"A":"\u00e4\u00f6\u00fc \\u2028 \\u0000 end","B":"\u2029"}',
    emptykey: '{"":"empty"}',
    lone: '{"A":"\\ud800"}',
    deep: '{"A":[[["x"]]],"B":[{"a":1}]}',
    brokenutf8: Buffer.from([0x7b, 0x22, 0x41, 0x22, 0x3a, 0x22, 0xff, 0x22, 0x7d]),
    whitespace: ' \n\t{"A" : "1" }\r\n ',
    numbers: '{"A":1.0,"B":1e-7,"C":123456789012345678901,"D":-1.5e-10,"E":5e-324,"F":1e-400}',
};

function layout(templateBytes) {
    const root = tmpdir('bd-envmerge-');
    const consoleDir = path.join(root, 'console');
    fs.mkdirSync(path.join(consoleDir, 'scripts'), { recursive: true });
    fs.mkdirSync(path.join(consoleDir, 'lib'), { recursive: true });
    fs.copyFileSync(path.join(REPO, 'scripts', 'merge-env.js'), path.join(consoleDir, 'scripts', 'merge-env.js'));
    fs.copyFileSync(path.join(REPO, 'scripts', 'write-installer-env-subst.js'), path.join(consoleDir, 'scripts', 'write-installer-env-subst.js'));
    fs.copyFileSync(path.join(REPO, 'lib', 'envMerge.js'), path.join(consoleDir, 'lib', 'envMerge.js'));
    fs.writeFileSync(path.join(consoleDir, '.env.example'), templateBytes);
    const work = path.join(root, 'work');
    fs.mkdirSync(work);
    return { root, consoleDir, work };
}

function bytes(value) {
    return Buffer.isBuffer(value) ? value : Buffer.from(value, 'utf8');
}

function classify(stderr) {
    if (stderr === '') return '';
    if (stderr.startsWith('Usage:')) return 'usage';
    try {
        JSON.parse(stderr);
        return 'json';
    } catch (_) {
        return 'uncaught';
    }
}

const vectors = [];

// One run of merge-env.js: target is .env in the work folder, files are
// written there first, args name them with {DIR}.
function record(name, { template, target, files = {}, args, env = {}, subst = null }) {
    const { root, consoleDir, work } = layout(bytes(templates[template]));
    if (target === 'DIRECTORY') fs.mkdirSync(path.join(work, '.env'));
    else if (target !== null) fs.writeFileSync(path.join(work, '.env'), bytes(target));
    const fileList = {};
    for (const [file, content] of Object.entries(files)) {
        fs.writeFileSync(path.join(work, file), bytes(content));
        fileList[file] = bytes(content).toString('base64');
    }
    let substJSON = null;
    if (subst) {
        // write-installer-env-subst.js first, as the installers did
        const out = run(path.join(consoleDir, 'scripts', 'write-installer-env-subst.js'), [path.join(work, 'subst-env.json')], { cwd: work, env: subst });
        if (out.status !== 0) throw new Error(`subst writer failed: ${out.stderr}`);
        substJSON = fs.readFileSync(path.join(work, 'subst-env.json'), 'utf8');
    }
    const realArgs = args.map((a) => a.split('{DIR}').join(work));
    const res = run(path.join(consoleDir, 'scripts', 'merge-env.js'), realArgs, { cwd: work, env });
    const pairs = [[work, '{DIR}']];
    const after = {};
    for (const file of ['.env', 'other.env']) after[file] = fileState(path.join(work, file));
    vectors.push({
        name, template, target: target === null ? null : (target === 'DIRECTORY' ? 'DIRECTORY' : bytes(target).toString('base64')),
        files: fileList, args, env: subst || env, substEnv: !!subst, substJSON,
        status: res.status, stdout: replace(res.stdout, pairs), stderr: replace(res.stderr, pairs), stderrKind: classify(res.stderr),
        after,
    });
    fs.rmSync(root, { recursive: true, force: true });
}

const T = '{DIR}/.env';
const S = '{DIR}/subst.json';

// Every template on every target, with and without --fresh
for (const template of Object.keys(templates)) {
    for (const [targetName, target] of Object.entries(existing)) {
        for (const fresh of [false, true]) {
            const args = ['--target', T, '--subst-file', S];
            if (fresh) args.push('--fresh');
            record(`${template}/${targetName}/${fresh ? 'fresh' : 'merge'}`, { template, target, files: { 'subst.json': substFiles.installer }, args });
        }
    }
}

// Every substitution file
for (const [substName, content] of Object.entries(substFiles)) {
    for (const template of ['small', 'placeholders']) {
        for (const targetName of ['none', 'some']) {
            record(`subst/${substName}/${template}/${targetName}`, { template, target: existing[targetName], files: { 'subst.json': content }, args: ['--target', T, '--subst-file', S] });
        }
    }
}

// The arguments as merge-env.js reads them
const odd = [
    [],
    ['--target'],
    ['--target', ''],
    ['--help', '--target', T],
    ['-h'],
    ['--fresh'],
    ['--target', T, '--subst', 'NOEQ'],
    ['--target', T, '--subst', 'A=1', '--subst', 'A=2'],
    ['--target', T, '--subst', '=v'],
    ['--target', T, '--subst', 'A='],
    ['--target', T, '--subst', '__proto__=x', '--subst', 'A=a'],
    ['--target', T, '--subst', 'A=a=b'],
    ['--target', T, '--subst', '1=one', '--subst', 'B=b', '--subst', '0=zero'],
    ['--target', T, '--subst'],
    ['--target', T, '--subst', ''],
    ['--target', T, '--subst-file'],
    ['--target', T, '--subst-file', ''],
    ['--target', T, '--subst-file', '{DIR}/missing.json'],
    ['--target', T, '--subst-file', '{DIR}'],
    ['--target', T, '--unknown', 'x', '--fresh'],
    ['--target', T, '--target', '{DIR}/other.env'],
    ['--target', '{DIR}/nope/.env'],
    ['--target', '{DIR}/nope/.env', '--fresh'],
    ['--fresh', '--target', T, '--subst-file', S, '--subst-file', '{DIR}/subst2.json'],
    ['--target', T, '--subst', 'A=__X__'],
    ['--target', T, '--subst', 'A=__X_'],
    ['--target', T, '--subst-file', S, '--subst', 'A=override'],
    ['--target', T, '--subst', 'A=first', '--subst-file', S],
    ['--target=' + T],
    ['--target', T, '--fresh', '--fresh'],
    ['--target', T, '-h'],
    ['--target', T, '--subst', 'A=1', '--help'],
    ['--target', T, '--target', ''],
    ['--target', T, '--subst', ''],
];
for (const [i, args] of odd.entries()) {
    for (const template of ['small', 'placeholders']) {
        record(`args/${i}/${template}`, {
            template, target: existing.some,
            files: { 'subst.json': '{"A":"from-file","B":"b"}', 'subst2.json': '{"A":"from-second","C":"c"}' },
            args,
        });
    }
}

// The installers' way: BD_SUBST_* into write-installer-env-subst.js, its
// file into merge-env.js. Replayed as --subst-env.
const envCases = {
    all: Object.fromEntries(Object.entries(installer).map(([k, v]) => [`BD_SUBST_${k}`, v])),
    none: {},
    some: { BD_SUBST_RUSTDESK_DIR: '/srv/rd', BD_SUBST_SESSION_SECRET: 's3cret', BD_SUBST_OTHER: 'ignored' },
    special: { BD_SUBST_DEFAULT_ADMIN_PASSWORD: 'p@$$ word&"quoted"=value \\ \u00e4 \u{1F600}', BD_SUBST_DATABASE_URL: 'postgres://u:p%40ss@db/x?sslmode=disable', BD_SUBST_SSL_KEY_PATH: 'C:\\BetterDesk\\ssl\\private key.pem' },
    placeholder: { BD_SUBST_DB_PATH: '__DB_PATH__' },
    empty: { BD_SUBST_DB_TYPE: '', BD_SUBST_API_PORT: '0' },
};
for (const [envName, env] of Object.entries(envCases)) {
    for (const template of ['real', 'small']) {
        for (const targetName of ['none', 'some']) {
            for (const fresh of [false, true]) {
                const args = ['--target', T, '--subst-file', '{DIR}/subst-env.json'];
                if (fresh) args.push('--fresh');
                record(`env/${envName}/${template}/${targetName}/${fresh ? 'fresh' : 'merge'}`, { template, target: existing[targetName], args, subst: env });
            }
        }
    }
}

writeVectors('envmerge', { templates: Object.fromEntries(Object.entries(templates).map(([k, v]) => [k, bytes(v).toString('base64')])), cases: vectors });

// What a substitution file gives, without the script around it: many JSON
// texts, every member as the template gets it (String(v ?? '')) and as the
// placeholder check sees it (String(v)), in Object.entries order.
const texts = new Set(Object.values(substFiles).filter((v) => !Buffer.isBuffer(v)));
const atoms = ['1', '-0', '0.5', '1e21', '1e-7', '1E400', '-1e400', '5e-324', '123456789012345678901234567890', 'true', 'false', 'null',
    '""', '"x"', '"\\u00e9"', '"\\ud83d\\ude00"', '"\\udc00"', '"a\\nb"', '"__X__"', '"__"', '"_x_"', '[]', '[1,2]', '[null]', '[[],[]]', '{}', '{"a":1}', '[{"a":1},"b"]'];
const keys = ['A', 'b', '0', '1', '10', '01', '-1', '1.5', '4294967294', '4294967295', '__proto__', 'constructor', '', ' ', 'k\u00e9y', '\u{1F600}', 'A', 'toString'];
let seed = 42;
const rnd = (n) => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
for (let i = 0; i < 3000; i++) {
    const n = rnd(6);
    const members = [];
    for (let j = 0; j < n; j++) members.push(`${JSON.stringify(keys[rnd(keys.length)])}:${atoms[rnd(atoms.length)]}`);
    let text = `{${members.join(',')}}`;
    switch (rnd(10)) {
        case 0: text = `[${members.map((m) => m.slice(m.indexOf(':') + 1)).join(',')}]`; break;
        case 1: text = atoms[rnd(atoms.length)]; break;
        case 2: text = text.slice(0, rnd(text.length + 1)); break;
        case 3: text = ` \n${text}\t `; break;
        case 4: text = text.replace(':', ' : '); break;
        default: break;
    }
    texts.add(text);
}
const files = [];
for (const text of texts) {
    const subs = {};
    let entry;
    try {
        Object.assign(subs, JSON.parse(text));
        entry = { text, entries: Object.entries(subs).map(([k, v]) => [k, Buffer.from(String(v ?? ''), 'utf8').toString('base64'), Buffer.from(String(v), 'utf8').toString('base64')]) };
    } catch (err) {
        entry = { text, error: err.name };
    }
    files.push(entry);
}
writeVectors('substfiles', files);
