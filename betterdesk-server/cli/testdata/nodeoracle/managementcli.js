'use strict';

// Records what scripts/management-cli.js of the old console does for
// "check" and "set", run as the installers ran it (BETTERDESK_CONSOLE_PATH,
// BETTERDESK_PATH and DATA_DIR set), into ../managementcli.json.gz
// (replayed by cli/config_test.go).
//
//   node managementcli.js   (in the dev image)

const fs = require('fs');
const path = require('path');
const { REPO, tmpdir, run, replace, fileState, writeVectors } = require('./lib');

const example = fs.readFileSync(path.join(REPO, '.env.example'));

function layout() {
    const root = tmpdir('bd-mgmt-');
    const consoleDir = path.join(root, 'console');
    fs.mkdirSync(path.join(consoleDir, 'scripts'), { recursive: true });
    fs.mkdirSync(path.join(consoleDir, 'lib'), { recursive: true });
    fs.copyFileSync(path.join(REPO, 'scripts', 'management-cli.js'), path.join(consoleDir, 'scripts', 'management-cli.js'));
    for (const lib of ['managementCapabilities.js', 'privilegedUpdateHelper.js']) {
        fs.copyFileSync(path.join(REPO, 'lib', lib), path.join(consoleDir, 'lib', lib));
    }
    return { root, consoleDir };
}

// The backup's name has the time in it
const STAMP = /\.bak\.\d{4}-\d\d-\d\dT\d\d-\d\d-\d\d-\d{3}Z/g;


// rooted writes the temporary folder in a file's content as {ROOT}.
function rooted(state, pairs) {
    if (!state || !state.base64) return state;
    return { ...state, base64: Buffer.from(replace(Buffer.from(state.base64, 'base64').toString('utf8'), pairs), 'utf8').toString('base64') };
}

const vectors = [];

function record(name, { args, envContent = null, withExample = true, dirs = ['data', 'server'], env = {} }) {
    const { root, consoleDir } = layout();
    if (withExample) fs.writeFileSync(path.join(consoleDir, '.env.example'), example);
    if (envContent !== null) fs.writeFileSync(path.join(consoleDir, '.env'), envContent);
    for (const dir of dirs) fs.mkdirSync(path.join(root, dir), { recursive: true });
    const fullEnv = {
        BETTERDESK_CONSOLE_PATH: consoleDir, BETTERDESK_PATH: path.join(root, 'server'), DATA_DIR: path.join(root, 'data'),
        ...Object.fromEntries(Object.entries(env).map(([k, v]) => [k, v.split('{ROOT}').join(root)])),
    };
    const realArgs = args.map((a) => a.split('{ROOT}').join(root));
    const res = run(path.join(consoleDir, 'scripts', 'management-cli.js'), realArgs, { cwd: root, env: fullEnv });
    const pairs = [[root, '{ROOT}']];
    const backups = fs.readdirSync(consoleDir).filter((f) => f.startsWith('.env.bak.'));
    vectors.push({
        name, args, envContent, withExample, dirs, env,
        status: res.status,
        stdout: replace(res.stdout, pairs).replace(STAMP, '.bak.{STAMP}'),
        stderr: replace(res.stderr, pairs),
        envAfter: rooted(fileState(path.join(consoleDir, '.env')), pairs),
        backups: backups.map((f) => rooted(fileState(path.join(consoleDir, f)), pairs)),
    });
    fs.rmSync(root, { recursive: true, force: true });
}

const contents = {
    none: null,
    empty: '',
    some: '# Console\nPORT=5000\nHOST=0.0.0.0\nLOG_LEVEL=info\n',
    crlf: 'PORT=5000\r\nLOG_LEVEL=info\r\n',
    trailing: 'PORT=5000\n\n\n',
    dup: 'PORT=5000\n  PORT=5001\nLOG_LEVEL=info\n',
    indented: '  LOG_LEVEL=info\n#PORT=1\n',
};

const sets = [
    ['PORT=5001'],
    ['LOG_LEVEL=debug', 'PORT=5100'],
    ['HTTPS_ENABLED=true'],
    ['HTTPS_ENABLED=maybe'],
    ['HTTPS_ENABLED=YES'],
    ['PORT=0'],
    ['PORT=65536'],
    ['PORT=05000'],
    ['PORT=abc'],
    ['PORT=+50'],
    ['LOG_LEVEL=verbose'],
    ['DB_TYPE=postgresql'],
    ['NODE_ENV=Production'],
    ['UPDATE_GITHUB_BRANCH=dev'],
    ['CDAP_TLS=Y'],
    ['CDAP_TLS=yes'],
    ['STORE_ADMIN_CREDENTIALS=maybe'],
    ['DATA_DIR={ROOT}/data/sub'],
    ['DATA_DIR={ROOT}/elsewhere'],
    ['DATA_DIR=relative/path'],
    ['SSL_CERT_PATH=/etc/letsencrypt/live/x/fullchain.pem'],
    ['SSL_CERT_PATH=/etc/letsencrypt2/x.pem'],
    ['SSL_KEY_PATH={ROOT}/console/../outside/key.pem'],
    ['SSL_KEY_PATH={ROOT}/server/ssl/key.pem'],
    ['RUSTDESK_DIR={ROOT}/console'],
    ['NOT_ALLOWED=1'],
    ['lowercase=1'],
    ['1=one', 'PORT=5001'],
    ['PORT=5001', '1=one'],
    ['__proto__=x'],
    ['__proto__=x', 'PORT=5001'],
    ['=x'],
    ['noequals'],
    [],
    ['PORT=5001', 'PORT=5002'],
    ['SMTP_PASS=s3cret', 'SESSION_SECRET=abc', 'LOG_LEVEL=warn'],
    ['DATABASE_URL=postgres://u:p@h/db'],
    ['PORT=5001\nHOST=evil'],
    ['LOG_LEVEL=debug\r'],
    ['PANEL_PUBLIC_URL=https://example.test/a=b'],
    ['PANEL_PUBLIC_URL='],
    ['DEFAULT_LANGUAGE=äöü  '],
    ['TRUST_PROXY=1'],
    ['TRUST_PROXY=loopback'],
    ['PANEL_PUBLIC_URL=https://x.test/?a=1&b=<2>'],
    ['SMTP_FROM=a b   � ä'],
];
for (const [contentName, content] of Object.entries(contents)) {
    for (const [i, set] of sets.entries()) {
        record(`set/${contentName}/${i}`, { args: ['set', ...set], envContent: content });
    }
}
record('set/noexample', { args: ['set', 'PORT=5001'], envContent: contents.some, withExample: false });
record('set/noconsoledir', { args: ['set', 'PORT=5001'], envContent: null, env: { BETTERDESK_CONSOLE_PATH: '{ROOT}/nope' } });
for (const args of [[], ['--help'], ['-h'], ['bogus'], ['SET', 'PORT=1']]) {
    record(`usage/${args.join('+') || 'none'}`, { args, envContent: contents.some });
}

// check: every combination of the folders that exist
const parts = ['data', 'server', 'data/backups', 'env', 'example'];
for (let mask = 0; mask < 1 << parts.length; mask++) {
    const has = (p) => (mask & (1 << parts.indexOf(p))) !== 0;
    const dirs = ['data', 'server', 'data/backups'].filter(has);
    record(`check/${mask}`, { args: ['check'], envContent: has('env') ? contents.some : null, withExample: has('example'), dirs });
}

writeVectors('managementcli', vectors);
