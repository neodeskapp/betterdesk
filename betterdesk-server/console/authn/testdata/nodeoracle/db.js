'use strict';

// Scenarios for the parts of sign-in that work on the database. Each one
// starts on empty tables, seeds some rows, and calls the Node console's
// auth service step by step. What every call returned, logged and sent to
// the Go server, and the rows left at the end, go to
// ../vectors/db-<dialect>.json.
//
//   node db.js sqlite
//   BETTERDESK_TEST_POSTGRES_DSN=postgres://... node db.js postgres

const crypto = require('crypto');
const H = require('./harness');
const { prng, write, ALPHA } = require('./lib');

const rnd = prng(424242);
const cp = (...points) => String.fromCodePoint(...points);
const sha256 = (text) => crypto.createHash('sha256').update(String(text), 'utf8').digest('hex');

const bcryptOf = (pw) => H.realBcrypt.hashSync(pw, 4);
function pbkdf2Modern(pw, iterations = 1000) {
    const salt = Buffer.from(rnd.bytes(16));
    return `pbkdf2-sha256$${iterations}$${salt.toString('hex')}$${crypto.pbkdf2Sync(pw, salt, iterations, 32, 'sha256').toString('hex')}`;
}
function pbkdf2Legacy(pw) {
    const salt = Buffer.from(rnd.bytes(16));
    return `${salt.toString('hex')}:${crypto.pbkdf2Sync(pw, salt, 100000, 32, 'sha256').toString('hex')}`;
}

function weighted(table) {
    let total = 0;
    for (const [, w] of table) total += w;
    let at = rnd.next() * total;
    for (const [value, w] of table) {
        at -= w;
        if (at < 0) return typeof value === 'function' ? value() : value;
    }
    return table[table.length - 1][0];
}

// ---- answers of the Go server

const ok = (role, extra = {}) => {
    const body = { token: 'jwt-token', username: 'someone', ...extra };
    if (role !== undefined) body.role = role;
    return { status: 200, body };
};
const GO = {
    twofa: { status: 200, body: { requires_2fa: true, partial_token: 'partial-token' } },
    s400: { status: 400, body: { error: 'Bad request' } },
    s401: { status: 401, body: { error: 'Invalid credentials' } },
    s403: { status: 403, body: { error: 'Forbidden' } },
    s429: { status: 429, body: { error: 'Too many requests' } },
    s500: { status: 500, body: { error: 'Internal error' } },
    timeout: 'timeout',
    down: 'down',
    text: { status: 200, text: '<html>proxy error</html>' },
    empty: { status: 200, body: {} },
    tokenOnly: { status: 200, body: { token: 'jwt-token' } },
    roleOnly: { status: 200, body: { role: 'admin' } },
    nullBody: { status: 200, text: 'null' },
};
const sso = (ldap, oidc, any = ldap || oidc) => ({ status: 200, body: { ldap_enabled: ldap, oidc_enabled: oidc, any_enabled: any } });
const SSO = {
    none: sso(false, false), ldap: sso(true, false), oidc: sso(false, true), both: sso(true, true), anyOnly: sso(false, false, true),
    s500: { status: 500, body: { error: 'x' } }, timeout: 'timeout', down: 'down', text: { status: 200, text: 'not json' },
    truthy: { status: 200, body: { ldap_enabled: 1, oidc_enabled: 'yes', any_enabled: 'true' } },
};
const LDAP = {
    yes: { status: 200, body: { authenticated: true } }, no: { status: 200, body: { authenticated: false } },
    s401: { status: 401, body: { authenticated: true } }, s500: { status: 500, body: { error: 'x' } }, timeout: 'timeout', text: { status: 200, text: 'true' },
};

const TOTP_SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
const CODES = '["3FA9-0C7E","AB12-CD34","00FF-11EE"]';

// ============================================================ authenticate

async function directedAuth() {
    const pw = 'Right-Password-1';
    const local = (extra = {}) => ({ username: 'alice', password_hash: bcryptOf(pw), role: 'operator', ...extra });
    const auth = (name, seedUsers, env, steps) => H.scenario(`auth/${name}`, { env, seed: { users: seedUsers } }, async (s) => {
        for (const [username, password, go] of steps) await s.call('authenticate', [username, password], { go });
    });

    await auth('empty username', [local()], {}, [['', pw], ['   ', pw], ['\t\n', pw], [cp(0xa0, 0x3000, 0xfeff), pw]]);
    await auth('local right password', [local()], {}, [['alice', pw]]);
    await auth('local wrong password, Go refuses', [local()], {}, [['alice', 'wrong', { login: [GO.s401] }]]);
    await auth('local wrong password, no answer from Go', [local()], {}, [['alice', 'wrong', { login: [GO.timeout] }], ['alice', 'wrong', { login: [GO.down] }]]);
    await auth('local wrong password, Go 429 and 500', [local()], {}, [['alice', 'wrong', { login: [GO.s429] }], ['alice', 'wrong', { login: [GO.s500] }], ['alice', 'wrong', { login: [GO.s403] }]]);
    await auth('local wrong password, Go accepts as local: hash and role synced', [local()], {}, [['alice', 'newer', { login: [ok('admin')] }], ['alice', 'newer'], ['alice', pw, { login: [GO.s401] }]]);
    await auth('local wrong password, Go accepts with provider local', [local()], {}, [['alice', 'newer', { login: [ok('operator', { auth_provider: 'local' })] }]]);
    await auth('local wrong password, Go accepts through LDAP: collision', [local()], { }, [['alice', 'dir-pw', { sso: SSO.ldap, login: [ok('admin', { auth_provider: 'ldap' })] }]]);
    await auth('local wrong password, Go accepts through OIDC: collision', [local()], {}, [['alice', 'x', { login: [ok('admin', { auth_provider: 'OIDC ' })] }]]);
    await auth('local wrong password, LDAP verify says yes: collision', [local()], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [GO.s401], ldap: [LDAP.yes] }]]);
    await auth('local wrong password, LDAP verify says no', [local()], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [GO.s401], ldap: [LDAP.no] }]]);
    await auth('local wrong password, LDAP off: no verify call', [local()], {}, [['alice', 'dir-pw', { sso: SSO.oidc, login: [GO.s401], ldap: [LDAP.yes] }]]);
    await auth('local wrong password, Go asks for 2FA: role admin written (S3)', [local({ role: 'viewer' })], {}, [['alice', 'newer', { login: [GO.twofa] }], ['alice', 'newer']]);
    await auth('local wrong password, Go asks for 2FA, local TOTP on (S3, S10)', [local({ role: 'viewer', totp_enabled: true, totp_secret: TOTP_SECRET })], {}, [['alice', 'newer', { login: [GO.twofa] }]]);
    await auth('local with TOTP', [local({ totp_enabled: true, totp_secret: TOTP_SECRET, preferred_language: 'de' })], {}, [['alice', pw], ['alice', 'wrong']]);
    await auth('local pro account (S11)', [local({ role: 'pro' })], {}, [['alice', pw], ['alice', 'wrong', { login: [ok('pro')] }]]);
    await auth('local becomes pro through Go', [local()], {}, [['alice', 'newer', { login: [ok('pro')] }]]);
    await auth('local PBKDF2 modern: migrated to bcrypt', [local({ password_hash: pbkdf2Modern(pw) })], {}, [['alice', pw], ['alice', pw], ['alice', 'wrong']]);
    await auth('local PBKDF2 legacy: migrated to bcrypt', [local({ password_hash: pbkdf2Legacy(pw) })], {}, [['alice', pw], ['alice', pw]]);
    await auth('local PBKDF2 wrong password: not migrated', [local({ password_hash: pbkdf2Modern(pw) })], {}, [['alice', 'wrong'], ['alice', pw]]);
    await auth('local PBKDF2 with TOTP: migrated, last_login untouched', [local({ password_hash: pbkdf2Modern(pw), totp_enabled: true, totp_secret: TOTP_SECRET })], {}, [['alice', pw]]);
    await auth('local garbage hash', [local({ password_hash: 'not-a-hash' })], {}, [['alice', pw], ['alice', pw, { login: [ok('admin')] }], ['alice', pw]]);
    await auth('local $2y$ hash', [local({ password_hash: bcryptOf(pw).replace('$2b$', '$2y$') })], {}, [['alice', pw]]);
    await auth('local $2a$ hash', [local({ password_hash: bcryptOf(pw).replace('$2b$', '$2a$') })], {}, [['alice', pw]]);
    await auth('local empty hash', [local({ password_hash: '' })], {}, [['alice', pw], ['alice', '']]);
    await auth('provider unknown counts as local', [local({ auth_provider: 'saml' })], {}, [['alice', pw], ['alice', 'newer', { login: [ok('admin')] }]]);
    await auth('provider NULL and empty count as local', [local({ auth_provider: null }), { username: 'bob', password_hash: bcryptOf(pw), auth_provider: '' }], {},
        [['alice', pw], ['bob', pw], ['alice', 'newer', { login: [ok('operator')] }], ['bob', 'newer', { login: [ok()] }]]);
    await auth('role NULL and empty', [local({ role: null }), { username: 'bob', password_hash: bcryptOf(pw), role: '' }], {},
        [['alice', pw], ['bob', pw], ['alice', 'newer', { login: [ok()] }], ['bob', 'newer', { login: [ok('')] }]]);
    await auth('username is matched as written', [local()], {}, [['Alice', pw], ['ALICE', pw], [' alice', pw], ['alice ', pw], ['alice', pw]]);
    await auth('oidc account cannot use a password', [local({ auth_provider: 'oidc' })], {}, [['alice', pw, { login: [ok('admin')] }], ['alice', 'x']]);
    await auth('oidc provider in other spelling', [local({ auth_provider: ' OIDC ' })], {}, [['alice', pw]]);
    await auth('ldap account, Go accepts', [local({ auth_provider: 'ldap', role: 'viewer' })], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [ok('operator', { auth_provider: 'ldap' })] }], ['alice', 'dir-pw', { login: [ok('operator', { auth_provider: 'ldap' })] }]]);
    await auth('ldap account, Go refuses', [local({ auth_provider: 'ldap' })], {}, [['alice', pw, { sso: SSO.ldap, login: [GO.s401] }], ['alice', pw, { login: [GO.timeout] }], ['alice', pw, { login: [GO.s429] }]]);
    await auth('ldap account, Go answers without provider, SSO off: turned local', [local({ auth_provider: 'ldap' })], {}, [['alice', 'dir-pw', { login: [ok('admin')] }], ['alice', 'dir-pw'], ['alice', pw]]);
    await auth('ldap account, Go answers without provider, only LDAP on: stays ldap', [local({ auth_provider: 'LDAP' })], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [ok('admin')] }]]);
    await auth('ldap account, Go says oidc: provider and hash change', [local({ auth_provider: 'ldap' })], {}, [['alice', 'dir-pw', { sso: SSO.both, login: [ok('viewer', { auth_provider: 'oidc' })] }], ['alice', 'dir-pw']]);
    await auth('ldap account, Go asks for 2FA: role admin (S3), second factor due', [local({ auth_provider: 'ldap', role: 'viewer' })], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [GO.twofa] }]]);
    await auth('ldap account with local TOTP', [local({ auth_provider: 'ldap', totp_enabled: true, totp_secret: TOTP_SECRET })], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [ok('operator', { auth_provider: 'ldap' })] }]]);
    await auth('ldap pro account', [local({ auth_provider: 'ldap', role: 'operator' })], {}, [['alice', 'dir-pw', { sso: SSO.ldap, login: [ok('pro', { auth_provider: 'ldap' })] }]]);
    await auth('unknown user, nothing on', [], {}, [['ghost', pw, { login: [ok('admin')] }]]);
    await auth('unknown user, LDAP on, Go accepts: created', [], {}, [['ghost', 'dir-pw', { sso: SSO.ldap, login: [ok('operator', { auth_provider: 'ldap' })] }], ['ghost', 'dir-pw', { login: [ok('admin', { auth_provider: 'ldap' })] }]]);
    await auth('unknown user, LDAP on, Go refuses', [], {}, [['ghost', pw, { sso: SSO.ldap, login: [GO.s401] }], ['ghost', pw, { login: [GO.timeout] }]]);
    await auth('unknown user, OIDC on, Go accepts without provider: oidc', [], {}, [['ghost', pw, { sso: SSO.oidc, login: [ok()] }]]);
    await auth('unknown user, both on, Go accepts without provider: local with password', [], {}, [['ghost', pw, { sso: SSO.both, login: [ok('viewer')] }], ['ghost', pw], ['ghost', 'wrong']]);
    await auth('unknown user, any_enabled alone', [], {}, [['ghost', pw, { sso: SSO.anyOnly, login: [ok('operator')] }]]);
    await auth('unknown user, AUTOCREATE, Go accepts: local account', [], { BETTERDESK_AUTH_AUTOCREATE: 'true' }, [['ghost', pw, { login: [ok('admin')] }], ['ghost', pw]]);
    await auth('unknown user, AUTOCREATE other spellings are off', [], { BETTERDESK_AUTH_AUTOCREATE: 'TRUE' }, [['ghost', pw, { login: [ok('admin')] }]]);
    await auth('unknown user, AUTOCREATE 1 is off', [], { BETTERDESK_AUTH_AUTOCREATE: '1' }, [['ghost', pw, { login: [ok('admin')] }]]);
    await auth('unknown user, AUTOCREATE, Go asks for 2FA: admin created (S3, S10)', [], { BETTERDESK_AUTH_AUTOCREATE: 'true' }, [['ghost', pw, { login: [GO.twofa] }], ['ghost', pw]]);
    await auth('unknown user, AUTOCREATE, Go says pro', [], { BETTERDESK_AUTH_AUTOCREATE: 'true' }, [['ghost', pw, { login: [ok('pro')] }]]);
    await auth('unknown user, AUTOCREATE and LDAP', [], { BETTERDESK_AUTH_AUTOCREATE: 'true' }, [['ghost', pw, { sso: SSO.ldap, login: [ok()] }]]);
    await auth('SSO status unreachable counts as off', [], {}, [['ghost', pw, { sso: SSO.timeout, login: [ok('admin')] }], ['ghost', pw, { sso: SSO.down, login: [ok('admin')] }], ['ghost', pw, { sso: SSO.ldap, login: [ok('admin')] }]]);
    await auth('SSO status 500 and not JSON count as off and are kept', [], {}, [['ghost', pw, { sso: SSO.s500, login: [ok('admin')] }], ['ghost', pw, { sso: SSO.ldap, login: [ok('admin')] }]]);
    await auth('SSO status text', [], {}, [['ghost', pw, { sso: SSO.text, login: [ok('admin')] }]]);
    await auth('SSO status with other true values', [], {}, [['ghost', pw, { sso: SSO.truthy, login: [ok('admin')] }]]);
    await auth('Go answers that are no acceptance', [local()], {}, [
        ['alice', 'x', { login: [GO.text] }], ['alice', 'x', { login: [GO.empty] }], ['alice', 'x', { login: [GO.tokenOnly] }], ['alice', 'x', { login: [GO.roleOnly] }],
        ['alice', 'x', { login: [GO.nullBody] }], ['alice', 'x', { login: [GO.s400] }], ['alice', 'x', { login: [{ status: 201, body: { token: 't', role: 'admin' } }] }],
        ['alice', 'x', { login: [{ status: 200, body: { token: 't', role: 'admin', requires_2fa: true } }] }]]);
    await auth('the status of single sign-on is asked once a minute', [local()], {}, [['alice', pw, { sso: SSO.ldap }], ['alice', 'x', { sso: SSO.none, login: [GO.s401], ldap: [LDAP.yes] }]]);
    await H.scenario('auth/the status is asked again after a minute', { seed: { users: [local()] } }, async (s) => {
        await s.call('authenticate', ['alice', 'x'], { at: { rel: 0 }, go: { sso: SSO.ldap, login: [GO.s401], ldap: [LDAP.no] } });
        await s.call('authenticate', ['alice', 'x'], { at: { rel: 59999 }, go: { sso: SSO.none, login: [GO.s401], ldap: [LDAP.no] } });
        await s.call('authenticate', ['alice', 'x'], { at: { rel: 60000 }, go: { sso: SSO.none, login: [GO.s401], ldap: [LDAP.yes] } });
    });
    await auth('long and odd passwords', [local({ password_hash: bcryptOf('a'.repeat(72)) })], {}, [['alice', 'a'.repeat(72)], ['alice', 'a'.repeat(100)], ['alice', 'a'.repeat(71)]]);
    await auth('unicode names and passwords', [{ username: cp(0xfc, 0x62, 0x65, 0x72), password_hash: bcryptOf(cp(0x1f600) + 'pw'), role: 'admin' }], {},
        [[cp(0xfc, 0x62, 0x65, 0x72), cp(0x1f600) + 'pw'], ['u' + cp(0x308) + 'ber', cp(0x1f600) + 'pw'], [cp(0xdc, 0x62, 0x65, 0x72), cp(0x1f600) + 'pw']]);
}

function userState(pw) {
    const role = weighted([['admin', 30], ['operator', 25], ['viewer', 20], ['pro', 8], [null, 4], ['', 3], ['super_admin', 10]]);
    const base = { username: 'alice', role };
    if (rnd.next() < 0.25) Object.assign(base, { totp_enabled: true, totp_secret: TOTP_SECRET, totp_recovery_codes: CODES });
    if (rnd.next() < 0.3) base.preferred_language = rnd.pick(['de', 'pl', 'en']);
    const kind = weighted([
        ['none', 14], ['bcrypt', 26], ['pbkdf2', 10], ['legacy', 3], ['garbage', 3], ['2y', 2], ['otherpw', 5],
        ['ldap', 13], ['LDAP', 2], ['ldap-spaces', 1], ['oidc', 6], ['saml', 2], ['null-provider', 2], ['empty-provider', 1],
    ]);
    switch (kind) {
    case 'none': return null;
    case 'bcrypt': return { ...base, password_hash: bcryptOf(pw) };
    case 'pbkdf2': return { ...base, password_hash: pbkdf2Modern(pw, rnd.pick([1, 1000])) };
    case 'legacy': return { ...base, password_hash: pbkdf2Legacy(pw) };
    case 'garbage': return { ...base, password_hash: rnd.pick(['garbage', '$2b$04$short', 'pbkdf2-sha256$x$y$z', ':']) };
    case '2y': return { ...base, password_hash: bcryptOf(pw).replace('$2b$', '$2y$') };
    case 'otherpw': return { ...base, password_hash: bcryptOf('some other password') };
    case 'ldap': return { ...base, password_hash: bcryptOf('unusable'), auth_provider: 'ldap' };
    case 'LDAP': return { ...base, password_hash: bcryptOf('unusable'), auth_provider: 'LDAP' };
    case 'ldap-spaces': return { ...base, password_hash: bcryptOf(pw), auth_provider: ' ldap ' };
    case 'oidc': return { ...base, password_hash: bcryptOf(pw), auth_provider: 'oidc' };
    case 'saml': return { ...base, password_hash: bcryptOf(pw), auth_provider: 'saml' };
    case 'null-provider': return { ...base, password_hash: bcryptOf(pw), auth_provider: null };
    default: return { ...base, password_hash: bcryptOf(pw), auth_provider: '' };
    }
}

function goLogin() {
    const role = () => weighted([['admin', 30], ['operator', 25], ['viewer', 20], ['pro', 8], ['', 5], [undefined, 12]]);
    return weighted([
        [() => ok(role()), 28],
        [() => ok(role(), { auth_provider: rnd.pick(['local', 'ldap', 'oidc', '', 'LDAP', ' oidc ', 'saml']) }), 22],
        [GO.twofa, 8], [GO.s401, 16], [GO.s403, 3], [GO.s429, 5], [GO.s500, 4], [GO.timeout, 3], [GO.down, 3], [GO.text, 2], [GO.empty, 2], [GO.tokenOnly, 2], [GO.roleOnly, 2],
    ]);
}

async function randomAuth(count) {
    for (let i = 0; i < count; i++) {
        const pw = `Right-pw-${i}`;
        const users = [];
        const user = userState(pw);
        if (rnd.next() < 0.3) users.push({ username: 'bystander', password_hash: bcryptOf('bystander-pw'), role: 'viewer' });
        if (user) users.push(user);
        const env = rnd.next() < 0.15 ? { BETTERDESK_AUTH_AUTOCREATE: 'true' } : {};
        const status = weighted([[SSO.none, 33], [SSO.ldap, 26], [SSO.oidc, 12], [SSO.both, 11], [SSO.anyOnly, 3], [SSO.s500, 5], [SSO.timeout, 4], [SSO.down, 3], [SSO.text, 3]]);
        const steps = [];
        for (let k = 1 + rnd.int(3); k > 0; k--) {
            const username = weighted([['alice', 80], ['Alice', 4], [' alice ', 3], ['', 2], ['   ', 1], ['ghost', 6], ['bystander', 4]]);
            const password = weighted([[pw, 48], ['wrong-password', 40], [`${pw}x`, 6], ['', 3], ['bystander-pw', 3]]);
            const go = { sso: status, login: [goLogin(), goLogin()], ldap: [weighted([[LDAP.yes, 30], [LDAP.no, 40], [LDAP.s401, 12], [LDAP.s500, 8], [LDAP.timeout, 5], [LDAP.text, 5]])] };
            steps.push([username, password, go]);
        }
        await H.scenario(`auth/random ${i}`, { env, seed: { users } }, async (s) => {
            for (const [username, password, go] of steps) await s.call('authenticate', [username, password], { go });
        });
    }
}

// ================================================== second factor, codes

async function totpScenarios() {
    const T = 1794287700123; // a fixed time: the codes below are literal
    const step = 30000;
    const user = (extra = {}) => ({ username: 'alice', password_hash: bcryptOf('pw'), role: 'admin', ...extra });

    await H.scenario('totp/setup, enable, verify, disable', { seed: { users: [user()] } }, async (s) => {
        await s.call('isTotpEnabled', [1]);
        const setup = await s.call('generateTotpSetup', [1], { at: { abs: T } });
        const secret = setup.result.secret;
        await s.call('verifyTotpCode', [1, H.codeAt(secret, T)], { at: { abs: T } }); // not enabled yet
        await s.call('verifyAndEnableTotp', [1, '000000'], { at: { abs: T } });
        await s.call('verifyAndEnableTotp', [1, H.codeAt(secret, T - step)], { at: { abs: T } });
        const enabled = await s.call('verifyAndEnableTotp', [1, H.codeAt(secret, T)], { at: { abs: T } });
        await s.call('isTotpEnabled', [1]);
        await s.call('verifyTotpCode', [1, H.codeAt(secret, T)], { at: { abs: T } });
        await s.call('verifyTotpCode', [1, H.codeAt(secret, T)], { at: { abs: T + step } });
        await s.call('verifyTotpCode', [1, H.codeAt(secret, T + step)], { at: { abs: T } });
        await s.call('verifyTotpCode', [1, ` ${H.codeAt(secret, T)}`], { at: { abs: T } });
        await s.call('verifyTotpCode', [1, H.codeAt(secret, T)], { at: { abs: T } }); // a code can be used again
        const codes = enabled.result.recoveryCodes;
        await s.call('verifyRecoveryCode', [1, codes[2]]);
        await s.call('verifyRecoveryCode', [1, codes[2]]);
        await s.call('verifyRecoveryCode', [1, codes[0].toLowerCase()]);
        await s.call('verifyRecoveryCode', [1, `  ${codes[7]}\n`]);
        await s.call('verifyRecoveryCode', [1, codes[1].replace('-', '')]);
        await s.call('verifyRecoveryCode', [1, codes[1].replace('-', ' ')]);
        await s.call('authenticate', ['alice', 'pw']);
        await s.call('disableTotp', [1]);
        await s.call('isTotpEnabled', [1]);
        await s.call('verifyRecoveryCode', [1, codes[1]]);
        await s.call('verifyTotpCode', [1, H.codeAt(secret, T)], { at: { abs: T } });
        await s.call('authenticate', ['alice', 'pw']);
    });
    await H.scenario('totp/a second setup replaces the secret', { seed: { users: [user()] } }, async (s) => {
        const first = await s.call('generateTotpSetup', [1], { at: { abs: T } });
        const second = await s.call('generateTotpSetup', [1], { at: { abs: T } });
        await s.call('verifyAndEnableTotp', [1, H.codeAt(first.result.secret, T)], { at: { abs: T } });
        await s.call('verifyAndEnableTotp', [1, H.codeAt(second.result.secret, T)], { at: { abs: T } });
    });
    await H.scenario('totp/setup while enabled keeps it enabled with the new secret', { seed: { users: [user({ totp_enabled: true, totp_secret: TOTP_SECRET, totp_recovery_codes: CODES })] } }, async (s) => {
        const setup = await s.call('generateTotpSetup', [1], { at: { abs: T } });
        await s.call('verifyTotpCode', [1, H.codeAt(TOTP_SECRET, T)], { at: { abs: T } });
        await s.call('verifyTotpCode', [1, H.codeAt(setup.result.secret, T)], { at: { abs: T } });
    });
    await H.scenario('totp/unknown accounts', { seed: { users: [user()] } }, async (s) => {
        for (const id of [2, 0, -1, 999999]) {
            await s.call('generateTotpSetup', [id], { at: { abs: T } });
            await s.call('verifyAndEnableTotp', [id, '123456'], { at: { abs: T } });
            await s.call('verifyTotpCode', [id, '123456'], { at: { abs: T } });
            await s.call('verifyRecoveryCode', [id, 'AAAA-BBBB']);
            await s.call('isTotpEnabled', [id]);
            await s.call('disableTotp', [id]);
        }
    });
    await H.scenario('totp/enable without a secret', { seed: { users: [user(), user({ username: 'bob', totp_secret: '' })] } }, async (s) => {
        await s.call('verifyAndEnableTotp', [1, '123456'], { at: { abs: T } });
        await s.call('verifyAndEnableTotp', [2, '123456'], { at: { abs: T } });
    });
    await H.scenario('totp/account names in the address', { seed: { users: [user({ username: 'a b+c@d/e' }), user({ username: cp(0xfc, 0x62, 0x65, 0x72) })] } }, async (s) => {
        await s.call('generateTotpSetup', [1], { at: { abs: T } });
        await s.call('generateTotpSetup', [2], { at: { abs: T } });
    });

    // Stored secrets of every kind, checked with right and wrong codes
    const secrets = [TOTP_SECRET, TOTP_SECRET.toLowerCase(), 'GEZDGNBV', 'GEZD GNBV', 'A', 'GEZDGNBVGY3TQOJQ====', '0189', 'GEZD' + cp(0xe9), '{}'];
    for (let i = 0; i < 20; i++) secrets.push(rnd.string(ALPHA.base32, 16, 40));
    for (const [i, secret] of secrets.entries()) {
        const at = T + rnd.int(1000000) * 1000;
        await H.scenario(`totp/stored secret ${i}`, { seed: { users: [user({ totp_enabled: true, totp_secret: secret }), user({ username: 'off', totp_enabled: false, totp_secret: secret })] } }, async (s) => {
            let code;
            try {
                code = H.codeAt(secret, at);
            } catch (_) {
                code = '123456';
            }
            await s.call('verifyTotpCode', [1, code], { at: { abs: at } });
            await s.call('verifyTotpCode', [1, code], { at: { abs: at + step } });
            await s.call('verifyTotpCode', [1, code.slice(0, 5)], { at: { abs: at } });
            await s.call('verifyTotpCode', [1, ''], { at: { abs: at } });
            await s.call('verifyTotpCode', [2, code], { at: { abs: at } });
            await s.call('verifyAndEnableTotp', [2, code], { at: { abs: at } });
            await s.call('verifyAndEnableTotp', [1, code], { at: { abs: at } }); // enabling again gives new codes
        });
    }

    // Stored recovery codes of every kind
    const stored = [CODES, '[]', '["abcd-ef01"]', '["ABCD-EF01","abcd-ef01"]', 'not json', '', '{}', '{"0":"ABCD-EF01"}', '"ABCD-EF01"', 'null', '123', 'true',
        '[1,2,3]', '["ABCD-EF01",5,"X"]', '[5,"ABCD-EF01"]', '[null]', '[["ABCD-EF01"]]', ' ["ABCD-EF01" , "B" ] ', '["stra' + cp(0xdf) + 'e","' + cp(0xe9) + 't' + cp(0xe9) + '"]',
        '["ABCD-EF01",1.50,{"a":[1, 2]},"<&>"]', '["' + 'A'.repeat(300) + '"]', '["  ABCD-EF01  "]', '["ABCD-EF01"]x'];
    const tries = ['ABCD-EF01', 'abcd-ef01', ' ABCD-EF01 ', 'ABCDEF01', 'B', 'X', 'STRASSE', 'stra' + cp(0xdf) + 'e', cp(0xc9) + 'T' + cp(0xc9), '', '  ', '3FA9-0C7E', '<&>', '1'];
    for (const [i, codes] of stored.entries()) {
        await H.scenario(`recovery/stored ${i}`, { seed: { users: [user({ totp_enabled: true, totp_secret: TOTP_SECRET, totp_recovery_codes: codes }), user({ username: 'off', totp_recovery_codes: codes })] } }, async (s) => {
            for (const code of tries) await s.call('verifyRecoveryCode', [1, code]);
            await s.call('verifyRecoveryCode', [2, 'ABCD-EF01']);
        });
    }
}

// ================================================= attempts and lockouts

async function bruteForceScenarios() {
    const small = { API_MAX_FAILED_ATTEMPTS: '3', API_LOCKOUT_MINUTES: '10', API_IP_RATE_LIMIT: '5', API_ATTEMPT_WINDOW: '15' };
    const ago = (table, minutes, where = '') => ({
        sqlite: `UPDATE ${table} SET created_at = datetime('now', '-${minutes} minutes') ${where}`,
        postgres: `UPDATE ${table} SET created_at = NOW() - INTERVAL '${minutes} minutes' ${where}`,
    });
    const user = { username: 'alice', password_hash: bcryptOf('pw'), role: 'admin' };

    await H.scenario('lockout/third failure locks, success unlocks', { env: small, seed: { users: [user] } }, async (s) => {
        await s.call('checkBruteForce', ['alice', '198.51.100.7']);
        for (let i = 0; i < 2; i++) await s.call('recordAttempt', ['alice', '198.51.100.7', false]);
        await s.call('checkBruteForce', ['alice', '198.51.100.7']);
        await s.call('recordAttempt', ['alice', '198.51.100.7', false]);
        await s.call('checkBruteForce', ['alice', '198.51.100.7']);
        await s.call('checkBruteForce', ['alice', '198.51.100.7'], { at: { rel: 5 * 60000 } });
        await s.call('checkBruteForce', ['alice', '198.51.100.7'], { at: { rel: 5 * 60000 + 1 } });
        await s.call('checkBruteForce', ['Alice', '203.0.113.1']);
        await s.call('checkBruteForce', ['', '198.51.100.7']);
        await s.call('checkBruteForce', ['alice', '']);
        await s.call('authenticate', ['alice', 'pw']); // a locked account still signs in (S6)
        await s.call('recordAttempt', ['alice', '198.51.100.7', true]);
        await s.call('checkBruteForce', ['alice', '198.51.100.7']);
    });
    await H.scenario('lockout/a further failure moves the end', { env: small }, async (s) => {
        for (let i = 0; i < 3; i++) await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        await s.call('recordAttempt', ['bob', '198.51.100.9', false], { at: { rel: 3 * 60000 } });
        await s.call('checkBruteForce', ['bob', '192.0.2.1'], { at: { rel: 3 * 60000 } });
    });
    await H.scenario('lockout/a lockout made in the past has run out', { env: small }, async (s) => {
        for (let i = 0; i < 3; i++) await s.call('recordAttempt', ['bob', '198.51.100.8', false], { at: { rel: -11 * 60000 } });
        await s.call('checkBruteForce', ['bob', '192.0.2.1']);
        await s.call('checkBruteForce', ['bob', '192.0.2.1'], { at: { rel: -11 * 60000 } });
    });
    await H.scenario('lockout/a lockout that ends in a minute still holds', { env: small }, async (s) => {
        for (let i = 0; i < 3; i++) await s.call('recordAttempt', ['bob', '198.51.100.8', false], { at: { rel: -9 * 60000 } });
        await s.call('checkBruteForce', ['bob', '192.0.2.1']);
        await s.call('checkBruteForce', ['bob', '192.0.2.1'], { at: { rel: 59000 } });
        await s.call('checkBruteForce', ['bob', '192.0.2.1'], { at: { rel: 61000 } });
        await s.call('checkBruteForce', ['bob', '192.0.2.1'], { at: { rel: 600000 } });
    });
    await H.scenario('lockout/failures outside the window do not count', { env: small }, async (s) => {
        for (let i = 0; i < 2; i++) await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        await s.sql(ago('login_attempts', 16));
        await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        await s.call('checkBruteForce', ['bob', '198.51.100.8']);
        await s.sql(ago('login_attempts', 14));
        await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        await s.call('checkBruteForce', ['bob', '198.51.100.8']);
    });
    await H.scenario('lockout/successes do not count as failures', { env: small }, async (s) => {
        for (let i = 0; i < 2; i++) await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        for (let i = 0; i < 4; i++) await s.call('recordAttempt', ['bob', '198.51.100.8', true]);
        await s.call('checkBruteForce', ['bob', '198.51.100.8']);
        await s.call('recordAttempt', ['bob', '198.51.100.8', false]);
        await s.call('checkBruteForce', ['bob', '198.51.100.8']);
    });
    await H.scenario('lockout/address limit over several accounts', { env: small }, async (s) => {
        for (let i = 0; i < 4; i++) await s.call('recordAttempt', [`user${i}`, '198.51.100.20', false]);
        await s.call('checkBruteForce', ['someone', '198.51.100.20']);
        await s.call('recordAttempt', ['user4', '198.51.100.20', false]);
        await s.call('checkBruteForce', ['someone', '198.51.100.20']);
        await s.call('checkBruteForce', ['someone', '198.51.100.21']);
        await s.call('checkBruteForce', ['', '198.51.100.20']);
        await s.call('recordAttempt', ['user0', '198.51.100.20', true]);
        await s.call('checkBruteForce', ['someone', '198.51.100.20']);
        await s.sql(ago('login_attempts', 16, 'WHERE id = 1'));
        await s.call('checkBruteForce', ['someone', '198.51.100.20']);
    });
    await H.scenario('lockout/account lock comes before the address limit', { env: small }, async (s) => {
        for (let i = 0; i < 6; i++) await s.call('recordAttempt', ['bob', '198.51.100.30', false]);
        await s.call('checkBruteForce', ['bob', '198.51.100.30']);
        await s.call('checkBruteForce', ['other', '198.51.100.30']);
    });
    await H.scenario('lockout/defaults: ten failures, thirty per address', {}, async (s) => {
        for (let i = 0; i < 9; i++) await s.call('recordAttempt', ['bob', '198.51.100.40', false]);
        await s.call('checkBruteForce', ['bob', '198.51.100.40']);
        await s.call('recordAttempt', ['bob', '198.51.100.40', false]);
        await s.call('checkBruteForce', ['bob', '198.51.100.40']);
        for (let i = 0; i < 19; i++) await s.call('recordAttempt', [`n${i}`, '198.51.100.40', false]);
        await s.call('checkBruteForce', ['carol', '198.51.100.40']);
        await s.call('recordAttempt', ['n19', '198.51.100.40', false]);
        await s.call('checkBruteForce', ['carol', '198.51.100.40']);
    });
    const odd = [
        { API_MAX_FAILED_ATTEMPTS: 'abc', API_LOCKOUT_MINUTES: '0', API_IP_RATE_LIMIT: '', API_ATTEMPT_WINDOW: '15x' },
        { API_MAX_FAILED_ATTEMPTS: '2x', API_LOCKOUT_MINUTES: ' 7', API_IP_RATE_LIMIT: '3.9', API_ATTEMPT_WINDOW: '+20' },
        { API_MAX_FAILED_ATTEMPTS: '-1', API_LOCKOUT_MINUTES: '5', API_IP_RATE_LIMIT: '-3', API_ATTEMPT_WINDOW: '15' },
        { API_MAX_FAILED_ATTEMPTS: '1', API_LOCKOUT_MINUTES: '-5', API_IP_RATE_LIMIT: '1', API_ATTEMPT_WINDOW: '1' },
        { API_MAX_FAILED_ATTEMPTS: '2', API_LOCKOUT_MINUTES: '1', API_IP_RATE_LIMIT: '2', API_ATTEMPT_WINDOW: '-5' },
        { API_MAX_FAILED_ATTEMPTS: '1e2', API_LOCKOUT_MINUTES: '0x10', API_IP_RATE_LIMIT: '007', API_ATTEMPT_WINDOW: '1440' },
    ];
    for (const [i, env] of odd.entries()) {
        await H.scenario(`lockout/odd settings ${i}`, { env }, async (s) => {
            for (let k = 0; k < 4; k++) {
                await s.call('recordAttempt', ['bob', '198.51.100.50', false]);
                await s.call('checkBruteForce', ['bob', '198.51.100.50']);
                await s.call('checkBruteForce', ['other', '198.51.100.50']);
            }
        });
    }
    await H.scenario('lockout/odd names and addresses', { env: small }, async (s) => {
        const names = ['bob', 'Bob', ' bob', cp(0xfc), "o'neil", 'a%b_c', '', 'x'.repeat(200)];
        for (const name of names) {
            for (const ip of ['::1', '::ffff:10.0.0.1', '', 'not-an-ip']) await s.call('recordAttempt', [name, ip, false]);
            await s.call('checkBruteForce', [name, '::1']);
        }
    });
    await H.scenario('housekeeping/old attempts and dead tokens go', { seed: { users: [user] } }, async (s) => {
        for (let i = 0; i < 4; i++) await s.call('recordAttempt', ['alice', '198.51.100.60', i % 2 === 0]);
        await s.sql(ago('login_attempts', 25 * 60, 'WHERE id <= 2'));
        await s.sql(ago('login_attempts', 23 * 60, 'WHERE id = 3'));
        await s.call('generateAccessToken', [1, 'client-a', 'uuid-a', '198.51.100.60']);
        await s.call('generateAccessToken', [1, 'client-a', 'uuid-a', '198.51.100.60']); // revokes the first
        await s.call('generateAccessToken', [1, 'client-b', 'uuid-b', '198.51.100.60'], { at: { rel: -8 * 86400000 } }); // already expired
        await s.call('generateAccessToken', [1, 'client-c', 'uuid-c', '198.51.100.60']);
        await s.call('cleanupHousekeeping', []);
    });
    await H.scenario('housekeeping/nothing to do', {}, async (s) => {
        await s.call('cleanupHousekeeping', []);
    });

    // Random runs of attempts and checks
    for (let i = 0; i < 120; i++) {
        const env = rnd.next() < 0.8 ? { API_MAX_FAILED_ATTEMPTS: String(2 + rnd.int(3)), API_LOCKOUT_MINUTES: String(5 + rnd.int(20)), API_IP_RATE_LIMIT: String(3 + rnd.int(4)), API_ATTEMPT_WINDOW: String(10 + rnd.int(10)) } : {};
        const plan = [];
        let rel = -rnd.int(3) * 20 * 60000;
        for (let k = 6 + rnd.int(10); k > 0; k--) {
            const name = rnd.pick(['ann', 'ben', 'ann', 'cy']);
            const ip = rnd.pick(['198.51.100.1', '198.51.100.2', '198.51.100.1']);
            const kind = rnd.next();
            if (kind < 0.55) plan.push(['recordAttempt', [name, ip, rnd.next() < 0.15], rel]);
            else if (kind < 0.9) plan.push(['checkBruteForce', [name, ip], rel]);
            else rel += rnd.pick([60000, 5 * 60000, 20 * 60000]);
        }
        await H.scenario(`lockout/random ${i}`, { env }, async (s) => {
            for (const [fn, args, at] of plan) await s.call(fn, args, { at: { rel: at } });
        });
    }
}

// ============================================================ tokens

const DEVICE_TOKENS = {
    sqlite: `CREATE TABLE device_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, token_hash TEXT UNIQUE NOT NULL, token_prefix TEXT NOT NULL DEFAULT '',
        name TEXT NOT NULL DEFAULT '', peer_id TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'pending', max_uses INTEGER DEFAULT 1, use_count INTEGER DEFAULT 0,
        created_at TEXT DEFAULT (datetime('now')), expires_at TEXT DEFAULT NULL, revoked_at TEXT DEFAULT NULL, last_used_at TEXT DEFAULT NULL,
        created_by TEXT DEFAULT '', note TEXT DEFAULT '')`,
    postgres: `CREATE TABLE device_tokens (id BIGSERIAL PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL DEFAULT '',
        name TEXT NOT NULL DEFAULT '', peer_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending', max_uses INTEGER NOT NULL DEFAULT 1,
        use_count INTEGER NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), expires_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ,
        last_used_at TIMESTAMPTZ, created_by TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '')`,
};

function enrollment(token, { peer = '', status = 'pending', max = 1, used = 0, expires = null }) {
    const q = (v) => `'${String(v).replace(/'/g, "''")}'`;
    const when = (dialect) => {
        if (expires === null) return 'NULL';
        if (typeof expires === 'string') return q(expires);
        return dialect === 'sqlite'
            ? `strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '${expires >= 0 ? '+' : ''}${expires} minutes')`
            : `NOW() + INTERVAL '${expires} minutes'`;
    };
    const row = (dialect) => `INSERT INTO device_tokens (token_hash, peer_id, status, max_uses, use_count, expires_at) VALUES (${q(sha256(token))}, ${q(peer)}, ${q(status)}, ${max}, ${used}, ${when(dialect)})`;
    return { sqlite: row('sqlite'), postgres: row('postgres') };
}

async function tokenScenarios() {
    const user = (name = 'alice', extra = {}) => ({ username: name, password_hash: bcryptOf('pw'), role: 'operator', ...extra });
    const legacyRow = (token, userId, clientId, extra = '') => ({
        sqlite: `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at${extra ? ', revoked' : ''}) VALUES ('${token}', NULL, ${userId}, '${clientId}', 'u', datetime('now', '+1 day')${extra ? ', 1' : ''})`,
        postgres: `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at${extra ? ', revoked' : ''}) VALUES ('${token}', NULL, ${userId}, '${clientId}', 'u', NOW() + INTERVAL '1 day'${extra ? ', TRUE' : ''})`,
    });

    await H.scenario('tokens/issue, validate, revoke', { seed: { users: [user(), user('bob', { role: null })] } }, async (s) => {
        const a = await s.call('generateAccessToken', [1, 'client-1', 'uuid-1', '198.51.100.1']);
        const b = await s.call('generateAccessToken', [1, 'client-2', 'uuid-2', '198.51.100.1']);
        const c = await s.call('generateAccessToken', [2, 'client-1', 'uuid-1', '']);
        for (const t of [a, b, c]) await s.call('validateAccessToken', [t.result]);
        await s.call('validateAccessToken', [a.result.toUpperCase()]);
        await s.call('validateAccessToken', [a.result.slice(0, 63)]);
        await s.call('validateAccessToken', [`${a.result}0`]);
        await s.call('validateAccessToken', ['']);
        await s.call('validateAccessToken', ['z'.repeat(64)]);
        await s.call('validateAccessToken', [cp(0x1f600).repeat(32)]);
        const a2 = await s.call('generateAccessToken', [1, 'client-9', 'uuid-1', '198.51.100.2']); // same uuid: a is revoked
        await s.call('validateAccessToken', [a.result]);
        await s.call('validateAccessToken', [a2.result]);
        await s.call('validateAccessToken', [c.result]);
        await s.call('revokeClientTokens', [1, 'client-2', 'uuid-2']);
        await s.call('validateAccessToken', [b.result]);
        await s.call('validateAccessToken', [a2.result]);
        await s.call('revokeClientTokens', [1, '', 'uuid-1']); // no client id: all of the user's tokens
        await s.call('validateAccessToken', [a2.result]);
        await s.call('validateAccessToken', [c.result]);
        await s.call('revokeClientTokens', [2, 'client-1', '']);
        await s.call('validateAccessToken', [c.result]);
    });
    await H.scenario('tokens/empty uuid revokes the tokens without one', { seed: { users: [user()] } }, async (s) => {
        const a = await s.call('generateAccessToken', [1, 'operator', '', '198.51.100.1']);
        const b = await s.call('generateAccessToken', [1, 'device-x', 'uuid-x', '198.51.100.1']);
        const c = await s.call('generateAccessToken', [1, 'operator', '', '198.51.100.1']);
        for (const t of [a, b, c]) await s.call('validateAccessToken', [t.result]);
    });
    await H.scenario('tokens/lifetime', { seed: { users: [user()] }, env: { API_TOKEN_EXPIRY_DAYS: '2' } }, async (s) => {
        const old = await s.call('generateAccessToken', [1, 'c', 'u1', ''], { at: { rel: -2 * 86400000 - 60000 } });
        const fresh = await s.call('generateAccessToken', [1, 'c', 'u2', ''], { at: { rel: -2 * 86400000 + 60000 } });
        await s.call('validateAccessToken', [old.result]);
        await s.call('validateAccessToken', [fresh.result]);
    });
    for (const [i, days] of ['abc', '0', '-1', '30', '1.9', ' 3'].entries()) {
        await H.scenario(`tokens/lifetime setting ${i}`, { seed: { users: [user()] }, env: { API_TOKEN_EXPIRY_DAYS: days } }, async (s) => {
            const t = await s.call('generateAccessToken', [1, 'c', 'u', '']);
            await s.call('validateAccessToken', [t.result]);
        });
    }
    await H.scenario('tokens/account deleted', { seed: { users: [user(), user('bob')] } }, async (s) => {
        const t = await s.call('generateAccessToken', [2, 'c', 'u', '']);
        await s.sql({ sqlite: 'DELETE FROM access_tokens WHERE 0', postgres: 'DELETE FROM access_tokens WHERE FALSE' });
        await s.call('validateAccessToken', [t.result]);
        await s.sql({ sqlite: "DELETE FROM users WHERE username = 'bob'", postgres: "DELETE FROM users WHERE username = 'bob'" });
        await s.call('validateAccessToken', [t.result]);
    });
    const plain = 'ab'.repeat(32);
    await H.scenario('tokens/rows from before the hash column', { seed: { users: [user()], after: [legacyRow(plain, 1, 'device-old'), legacyRow('cd'.repeat(32), 1, 'device-gone', 'revoked')] } }, async (s) => {
        await s.call('validateAccessToken', [plain]);
        await s.call('validateAccessToken', ['cd'.repeat(32)]);
        await s.call('requireDeviceToken', [`Bearer ${plain}`]);
        await s.call('verifyDeviceWsAuth', ['device-old', plain]);
        await s.call('revokeClientTokens', [1, 'x', 'u']);
        await s.call('validateAccessToken', [plain]);
    });
    // A row whose token column holds the hash of the token a device presents
    const presented = 'ef'.repeat(32);
    await H.scenario('tokens/a row stored under the hash of the token', { seed: { users: [user()], after: [legacyRow(sha256(presented), 1, 'device-h')] } }, async (s) => {
        await s.call('validateAccessToken', [presented]);
        await s.call('verifyDeviceWsAuth', ['device-h', presented]);
        await s.call('verifyDeviceWsAuth', ['device-other', presented]);
        await s.call('requireDeviceToken', [`Bearer ${presented}`]);
    });

    await H.scenario('device/requireDeviceToken', { seed: { users: [user()] } }, async (s) => {
        const bound = await s.call('generateAccessToken', [1, 'device-a', 'uuid-a', '']);
        const operator = await s.call('generateAccessToken', [1, '', 'uuid-b', '']);
        const short = await s.call('generateAccessToken', [1, 'ab', 'uuid-c', '']);
        const odd = await s.call('generateAccessToken', [1, 'device a', 'uuid-d', '']);
        const long = await s.call('generateAccessToken', [1, 'd'.repeat(65), 'uuid-e', '']);
        for (const header of [null, '', 'Bearer', 'Bearer ', `Bearer ${bound.result}`, `Bearer  ${bound.result}`, `bearer ${bound.result}`, `Bearer ${bound.result} `,
            `Bearer ${bound.result} x`, `Basic ${bound.result}`, bound.result, `Bearer ${operator.result}`, `Bearer ${short.result}`, `Bearer ${odd.result}`, `Bearer ${long.result}`,
            'Bearer unknown-token', `Bearer ${bound.result.toUpperCase()}`, `Bearer${cp(0xa0)}${bound.result}`]) {
            await s.call('requireDeviceToken', [header]);
        }
        await s.call('revokeClientTokens', [1, 'device-a', 'uuid-a']);
        await s.call('requireDeviceToken', [`Bearer ${bound.result}`]);
    });

    await H.scenario('device/no enrollment table', { seed: { users: [user()] } }, async (s) => {
        const t = await s.call('generateAccessToken', [1, 'device-a', 'uuid-a', '']);
        await s.call('verifyDeviceWsAuth', ['device-a', t.result]);
        await s.call('verifyDeviceWsAuth', ['device-b', t.result]);
        await s.call('verifyDeviceWsAuth', ['', t.result]);
        await s.call('verifyDeviceWsAuth', ['device-a', '']);
        await s.call('verifyDeviceWsAuth', ['device-a', 'short12']);
        await s.call('verifyEnrollmentToken', ['device-a', 'enroll-token-1']);
    });
    const tokens = {
        open: enrollment('enroll-open-1', {}),
        bound: enrollment('enroll-bound-1', { peer: 'device-a' }),
        revoked: enrollment('enroll-revoked', { status: 'revoked' }),
        expiredStatus: enrollment('enroll-expired', { status: 'expired' }),
        active: enrollment('enroll-active1', { status: 'active', max: 3, used: 2 }),
        usedUp: enrollment('enroll-used-up', { status: 'active', max: 3, used: 3 }),
        over: enrollment('enroll-over-11', { max: 1, used: 5 }),
        unlimited: enrollment('enroll-nolimit', { max: 0, used: 99 }),
        negative: enrollment('enroll-negative', { max: -1, used: 99 }),
        future: enrollment('enroll-future1', { expires: 60 }),
        past: enrollment('enroll-past-11', { expires: -60 }),
        boundPast: enrollment('enroll-bpast-1', { peer: 'device-a', expires: -1 }),
    };
    await H.scenario('device/enrollment tokens', { seed: { users: [user()], sql: [DEVICE_TOKENS], after: Object.values(tokens) } }, async (s) => {
        const names = { open: 'enroll-open-1', bound: 'enroll-bound-1', revoked: 'enroll-revoked', expiredStatus: 'enroll-expired', active: 'enroll-active1', usedUp: 'enroll-used-up',
            over: 'enroll-over-11', unlimited: 'enroll-nolimit', negative: 'enroll-negative', future: 'enroll-future1', past: 'enroll-past-11', boundPast: 'enroll-bpast-1' };
        for (const token of Object.values(names)) {
            await s.call('verifyDeviceWsAuth', ['device-a', token]);
            await s.call('verifyDeviceWsAuth', ['device-b', token]);
            await s.call('verifyEnrollmentToken', ['device-a', token]);
        }
        await s.call('verifyDeviceWsAuth', ['device-a', 'enroll-unknown']);
        await s.call('verifyDeviceWsAuth', ['device-a', 'enroll-future1'], { at: { rel: 59 * 60000 } });
        await s.call('verifyDeviceWsAuth', ['device-a', 'enroll-future1'], { at: { rel: 61 * 60000 } });
        await s.call('verifyDeviceWsAuth', ['device-a', 'enroll-past-11'], { at: { rel: -61 * 60000 } });
        await s.call('verifyDeviceWsAuth', ['', 'enroll-open-1']);
        await s.call('verifyDeviceWsAuth', ['device-a', 'enroll-']);
        const access = await s.call('generateAccessToken', [1, 'device-a', 'uuid-a', '']);
        await s.call('verifyDeviceWsAuth', ['device-a', access.result]);
        await s.call('verifyDeviceWsAuth', ['device-b', access.result]);
    });
}

// ======================================================= change password

async function passwordScenarios() {
    const pw = 'Current-pw-1';
    const hashes = { bcrypt: bcryptOf(pw), pbkdf2: pbkdf2Modern(pw), legacy: pbkdf2Legacy(pw), garbage: 'garbage', '2a': bcryptOf(pw).replace('$2b$', '$2a$') };
    const news = ['New-Password-9', 'short', '1234567', '12345678', '', cp(0x1f600).repeat(4), cp(0x1f600).repeat(3), cp(0xe9).repeat(8), 'e' + cp(0x301) + 'e' + cp(0x301) + 'abcd', 'a'.repeat(200), '        ', pw];
    let i = 0;
    for (const [kind, hash] of Object.entries(hashes)) {
        await H.scenario(`password/change ${kind}`, { seed: { users: [{ username: 'alice', password_hash: hash, role: 'admin' }, { username: 'ldap-user', password_hash: bcryptOf(pw), auth_provider: 'ldap' }] } }, async (s) => {
            await s.call('changePassword', [1, 'wrong', 'New-Password-9']);
            await s.call('changePassword', [3, pw, 'New-Password-9']);
            await s.call('changePassword', [1, pw, 'short']);
            await s.call('changePassword', [1, pw, news[i++ % news.length]]);
            await s.call('changePassword', [1, pw, 'Another-One-1']);
            await s.call('authenticate', ['alice', pw]);
            await s.call('changePassword', [2, pw, 'Dir-User-New-1']);
        });
    }
    for (const [k, next] of news.entries()) {
        await H.scenario(`password/new password ${k}`, { seed: { users: [{ username: 'alice', password_hash: hashes.bcrypt, role: 'admin' }] } }, async (s) => {
            await s.call('changePassword', [1, pw, next]);
            await s.call('authenticate', ['alice', next || 'x']);
        });
    }
}

// ========================================================= default admin

async function adminScenarios() {
    const B = H.ADMIN_BASE;
    const dirs = { keysPath: `${B}/keys`, dataDir: `${B}/data`, authDbPath: `${B}/data/auth.db` };
    const creds = (password, extra = '') => `Admin Username: admin\nAdmin Password: ${password}\n${extra}`;
    const keysDir = { path: `${B}/keys`, dir: true };
    const dataDir = { path: `${B}/data`, dir: true };
    const pw = 'Env-Password-1';
    const admin = (hash, name = 'admin') => ({ username: name, password_hash: hash, role: 'admin' });
    const run = (name, setup) => H.scenario(`admin/${name}`, { dirs, ...setup, files: [keysDir, dataDir, ...(setup.files || [])] }, async (s) => {
        await s.call('ensureDefaultAdmin', [], { onSleep: setup.onSleep });
        if (setup.again) await s.call('ensureDefaultAdmin', []);
        if (setup.login) await s.call('authenticate', setup.login);
    });
    const clean = { DEFAULT_ADMIN_USERNAME: null, DEFAULT_ADMIN_PASSWORD: null, FORCE_PASSWORD_UPDATE: null, DOCKER: null, SQLITE_AUTH_DB_MODE: null, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: null, DB_TYPE: 'sqlite' };

    await run('empty database, password from the environment', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, login: ['admin', pw], again: true });
    await run('empty database, other name', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw, DEFAULT_ADMIN_USERNAME: 'root' }, login: ['root', pw] });
    await run('empty database, password from the key folder', { env: clean, files: [{ path: `${B}/keys/.admin_credentials`, content: creds('From-Keys-1'), mode: 0o600 }, { path: `${B}/data/.admin_credentials`, content: creds('From-Data-1') }], login: ['admin', 'From-Keys-1'] });
    await run('empty database, password from the data folder', { env: clean, files: [{ path: `${B}/data/.admin_credentials`, content: creds('From-Data-1') }], login: ['admin', 'From-Data-1'] });
    await run('empty database, password from /opt/rustdesk', { env: clean, files: [{ path: '/opt/rustdesk/.admin_credentials', content: creds('From-Opt-1') }, { path: `${B}/data/.admin_credentials`, content: creds('From-Data-1') }] });
    await run('empty database, password from <keys>/data', { env: clean, files: [{ path: `${B}/keys/data/.admin_credentials`, content: creds('From-Keys-Data-1') }, { path: `${B}/data/.admin_credentials`, content: creds('From-Data-1') }] });
    await run('empty database, /opt/betterdesk and the rest', { env: clean, files: [{ path: '/opt/rustdesk/data/.admin_credentials', content: creds('Last-1') }, { path: '/opt/betterdesk/data/.admin_credentials', content: creds('Second-1') }] });
    await run('empty database, /opt/betterdesk first', { env: clean, files: [{ path: '/opt/betterdesk/.admin_credentials', content: creds('First-1') }, { path: '/opt/betterdesk/data/.admin_credentials', content: creds('Second-1') }] });
    await run('/app/data is not looked at outside Docker', { env: clean, files: [{ path: '/app/data/.admin_credentials', content: creds('App-Data-1') }] });
    await run('/app/data with /.dockerenv', { env: clean, files: [{ path: '/app/data/.admin_credentials', content: creds('App-Data-1') }, { path: '/.dockerenv', content: '' }] });
    await run('/app/data with DOCKER=true and a separate auth.db', { env: { ...clean, DOCKER: 'true', SQLITE_AUTH_DB_MODE: 'legacy' }, files: [{ path: '/app/data/.admin_credentials', content: creds('App-Data-1') }] });
    await run('/app/data with DOCKER=TRUE is not looked at', { env: { ...clean, DOCKER: 'TRUE', SQLITE_AUTH_DB_MODE: 'legacy' }, files: [{ path: '/app/data/.admin_credentials', content: creds('App-Data-1') }] });
    await run('Windows folders', { env: clean, platform: 'win32', files: [{ path: 'C:\\BetterDesk/.admin_credentials', content: creds('Win-1') }, { path: 'C:\\RustDesk/.admin_credentials', content: creds('Win-2') }] });
    await run('Windows folders are not looked at elsewhere', { env: clean, files: [{ path: 'C:\\BetterDesk/.admin_credentials', content: creds('Win-1') }] });
    await run('empty database, no password anywhere: made up and written', { env: clean, login: ['admin', 'x'], again: true });
    await run('made-up password, existing credentials file keeps its mode', { env: clean, files: [{ path: `${B}/keys/.admin_credentials`, content: 'nothing useful\n', mode: 0o644 }] });
    await run('made-up password, other name', { env: { ...clean, DEFAULT_ADMIN_USERNAME: 'root' } });
    await run('credentials file appears at the third wait', { env: clean, onSleep: { 3: [{ file: { path: `${B}/keys/.admin_credentials`, content: creds('Late-1') } }] }, login: ['admin', 'Late-1'] });
    await run('credentials file appears at the last wait', { env: clean, onSleep: { 5: [{ file: { path: `${B}/data/.admin_credentials`, content: creds('Late-2') } }] } });

    const fileForms = {
        'CRLF': 'Admin Username: admin\r\nAdmin Password: Crlf-1\r\nGenerated by: x\r\n',
        'BOM': cp(0xfeff) + 'Admin Username: admin\nAdmin Password: Bom-1\n',
        'BOM first line': cp(0xfeff) + 'Admin Password: Bom-2\n',
        'no space': 'Admin Password:NoSpace-1\n',
        'many spaces': 'Admin Password:   \t Spaced-1  \t\n',
        'value on the next line': 'Admin Password:\nNext-Line-1\nmore\n',
        'value after blank lines': 'Admin Password:   \n\n  After-Blank-1\n',
        'no value at the end': 'Admin Username: admin\nAdmin Password:',
        'only spaces at the end': 'Admin Password:   ',
        'two lines': 'Admin Password: First-1\nAdmin Password: Second-1\n',
        'empty first, real second': 'Admin Password:\n\nAdmin Password: Second-2\n',
        'indented': '  Admin Password: Indented-1\n',
        'lower case': 'admin password: lower-1\n',
        'old format': 'admin:Old-Format-1\n',
        'no newline': 'Admin Password: No-Newline-1',
        'inner spaces': 'Admin Password: has inner spaces \n',
        'unicode': 'Admin Password: ' + cp(0xfc, 0x62, 0x65, 0x72, 0x1f600) + '\n',
        'line separator': 'x' + cp(0x2028) + 'Admin Password: After-LS-1' + cp(0x2029) + 'rest',
        'CR only': 'Admin Username: admin\rAdmin Password: Cr-Only-1\rrest',
        'nbsp': 'Admin Password:' + cp(0xa0, 0x3000) + 'Nbsp-1' + cp(0xa0) + '\n',
        'empty file': '',
        'mid line': 'The Admin Password: Mid-1\n',
    };
    for (const [form, content] of Object.entries(fileForms)) {
        await run(`credentials file form: ${form}`, { env: clean, files: [{ path: `${B}/keys/.admin_credentials`, content }, { path: `${B}/data/.admin_credentials`, content: creds('Fallback-1') }] });
    }
    await run('credentials file is a folder', { env: clean, files: [{ path: `${B}/keys/.admin_credentials`, dir: true }, { path: `${B}/data/.admin_credentials`, content: creds('Fallback-1') }] });

    // Users exist
    await run('users exist, no password known', { env: clean, seed: { users: [admin(bcryptOf('Old-1'))] } });
    await run('users exist, bcrypt admin, password known: unchanged', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(bcryptOf('Old-1'))] }, login: ['admin', 'Old-1'] });
    await run('users exist, PBKDF2 admin fits: migrated', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(pbkdf2Modern(pw))] }, login: ['admin', pw] });
    await run('users exist, legacy PBKDF2 admin fits: migrated', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(pbkdf2Legacy(pw))] } });
    await run('users exist, PBKDF2 admin does not fit', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(pbkdf2Modern('Other-1'))] } });
    await run('users exist, PBKDF2 admin, password from a file', { env: clean, seed: { users: [admin(pbkdf2Modern('From-Keys-1'))] }, files: [{ path: `${B}/keys/.admin_credentials`, content: creds('From-Keys-1') }] });
    await run('users exist, PBKDF2 admin, force flag changes nothing more', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw, FORCE_PASSWORD_UPDATE: 'true' }, seed: { users: [admin(pbkdf2Modern('Other-1'))] } });
    await run('users exist, admin under another name', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(bcryptOf('Old-1'), 'root')] } });
    await run('users exist, garbage hash', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin('garbage')] } });
    await run('force by the environment', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw, FORCE_PASSWORD_UPDATE: 'true' }, seed: { users: [admin(bcryptOf('Old-1'))] }, login: ['admin', pw], again: true });
    await run('force by the environment, other spellings are off', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw, FORCE_PASSWORD_UPDATE: 'TRUE' }, seed: { users: [admin(bcryptOf('Old-1'))] } });
    await run('force by the environment without a password', { env: { ...clean, FORCE_PASSWORD_UPDATE: 'true' }, seed: { users: [admin(bcryptOf('Old-1'))] } });
    await run('force by the file', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(bcryptOf('Old-1'))] }, files: [{ path: `${B}/data/.force_password_update`, content: '' }], login: ['admin', pw], again: true });
    await run('force by the file with the password from a file', { env: clean, seed: { users: [admin(bcryptOf('Old-1'))] }, files: [{ path: `${B}/data/.force_password_update`, content: 'x' }, { path: `${B}/data/.admin_credentials`, content: creds('File-Pw-1') }], login: ['admin', 'File-Pw-1'] });
    await run('force file that cannot be removed', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(bcryptOf('Old-1'))] }, files: [{ path: `${B}/data/.force_password_update/inner`, content: '' }] });
    await run('force file on an empty database', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, files: [{ path: `${B}/data/.force_password_update`, content: '' }] });
    await run('force file in the key folder is not looked at', { env: { ...clean, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: [admin(bcryptOf('Old-1'))] }, files: [{ path: `${B}/keys/.force_password_update`, content: '' }] });

    // Docker
    const docker = { ...clean, DOCKER: 'true' };
    const goAdmin = [{ username: 'admin', password_hash: pbkdf2Modern(pw), role: 'admin' }];
    await run('Docker, consolidated, admin is there', { env: { ...docker, DEFAULT_ADMIN_PASSWORD: pw }, seed: { users: goAdmin } });
    await run('Docker, admin appears at the second poll', { env: { ...docker, DEFAULT_ADMIN_PASSWORD: pw }, onSleep: { 2: [{ users: goAdmin }] } });
    await run('Docker, admin appears at the last poll', { env: { ...docker, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '3000' }, onSleep: { 3: [{ users: goAdmin }] } });
    await run('Docker, no admin in time', { env: { ...docker, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '3000', DEFAULT_ADMIN_PASSWORD: pw } });
    await run('Docker, wait below a second is a second', { env: { ...docker, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '5' } });
    await run('Docker, wait with odd values', { env: { ...docker, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '2500ms' } });
    await run('Docker, wait not a number: thirty polls', { env: { ...docker, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: 'abc' }, onSleep: { 30: [{ users: goAdmin }] } });
    await run('Docker, default wait', { env: docker, onSleep: { 29: [{ users: goAdmin }] } });
    await run('Docker, legacy mode: own admin', { env: { ...docker, SQLITE_AUTH_DB_MODE: 'legacy', DEFAULT_ADMIN_PASSWORD: pw } });
    await run('Docker, LEGACY in capitals', { env: { ...docker, SQLITE_AUTH_DB_MODE: 'LEGACY', DEFAULT_ADMIN_PASSWORD: pw } });
    await run('Docker, consolidated mode with an auth.db', { env: { ...docker, SQLITE_AUTH_DB_MODE: 'consolidated', DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '1000' }, files: [{ path: `${B}/data/auth.db`, content: 'x' }] });
    await run('Docker, auth.db present: own admin', { env: { ...docker, DEFAULT_ADMIN_PASSWORD: pw }, files: [{ path: `${B}/data/auth.db`, content: 'x' }] });
    await run('Docker, PostgreSQL waits whatever the mode', { env: { ...docker, DB_TYPE: 'postgres', SQLITE_AUTH_DB_MODE: 'legacy', DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '1000' } });
    await run('Docker, DB_TYPE PostgreSQL in capitals', { env: { ...docker, DB_TYPE: 'PostgreSQL', DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '1000' }, files: [{ path: `${B}/data/auth.db`, content: 'x' }] });
    await run('Docker, DB_TYPE unset follows the database', { env: { ...docker, DB_TYPE: null, DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '1000' }, files: [{ path: `${B}/data/auth.db`, content: 'x' }] });
    await run('DOCKER=True in mixed case waits too', { env: { ...clean, DOCKER: 'True', DOCKER_ADMIN_BOOTSTRAP_WAIT_MS: '1000' } });
    await run('DOCKER=1 is not Docker', { env: { ...clean, DOCKER: '1', DEFAULT_ADMIN_PASSWORD: pw } });
    await run('Docker, force flag with the admin of the signal server', { env: { ...docker, DEFAULT_ADMIN_PASSWORD: pw, FORCE_PASSWORD_UPDATE: 'true' }, seed: { users: [admin(bcryptOf('Old-1'))] } });

    // Mixes
    const pick = (list) => rnd.pick(list);
    for (let i = 0; i < 90; i++) {
        const env = { ...clean };
        if (rnd.next() < 0.5) env.DEFAULT_ADMIN_PASSWORD = pw;
        if (rnd.next() < 0.2) env.DEFAULT_ADMIN_USERNAME = 'root';
        if (rnd.next() < 0.3) env.FORCE_PASSWORD_UPDATE = pick(['true', 'false', '1']);
        if (rnd.next() < 0.35) env.DOCKER = pick(['true', 'true', 'TRUE', 'false']);
        if (rnd.next() < 0.3) env.SQLITE_AUTH_DB_MODE = pick(['legacy', 'consolidated', 'other']);
        if (rnd.next() < 0.2) env.DB_TYPE = pick(['postgres', 'sqlite', 'postgresql']);
        env.DOCKER_ADMIN_BOOTSTRAP_WAIT_MS = pick(['1000', '2000', '4000']);
        const files = [];
        if (rnd.next() < 0.3) files.push({ path: `${B}/keys/.admin_credentials`, content: creds(pick(['Keys-Pw-1', pw])), mode: 0o600 });
        if (rnd.next() < 0.3) files.push({ path: `${B}/data/.admin_credentials`, content: creds('Data-Pw-1') });
        if (rnd.next() < 0.2) files.push({ path: `${B}/data/.force_password_update`, content: '' });
        if (rnd.next() < 0.25) files.push({ path: `${B}/data/auth.db`, content: 'x' });
        if (rnd.next() < 0.15) files.push({ path: '/.dockerenv', content: '' });
        if (rnd.next() < 0.15) files.push({ path: '/app/data/.admin_credentials', content: creds('App-Pw-1') });
        const users = [];
        const name = env.DEFAULT_ADMIN_USERNAME || 'admin';
        const state = pick(['none', 'none', 'bcrypt', 'pbkdf2-fit', 'pbkdf2-keys', 'pbkdf2-other', 'bystander']);
        if (state === 'bcrypt') users.push(admin(bcryptOf('Old-1'), name));
        if (state === 'pbkdf2-fit') users.push(admin(pbkdf2Modern(pw), name));
        if (state === 'pbkdf2-keys') users.push(admin(pbkdf2Modern('Keys-Pw-1'), name));
        if (state === 'pbkdf2-other') users.push(admin(pbkdf2Modern('Other-1'), name));
        if (state === 'bystander') users.push(admin(bcryptOf('Old-1'), 'someone'));
        const onSleep = {};
        if (rnd.next() < 0.3) onSleep[String(1 + rnd.int(4))] = [rnd.next() < 0.5 ? { users: [admin(pbkdf2Modern(pw), name)] } : { file: { path: `${B}/keys/.admin_credentials`, content: creds('Late-Pw-1') } }];
        await run(`mix ${i}`, { env, files, seed: { users }, onSleep, again: rnd.next() < 0.3 });
    }

    // A folder that cannot be written to
    await H.scenario('admin/key folder missing: the file cannot be written', { dirs: { ...dirs, keysPath: `${B}/missing` }, env: clean, files: [dataDir] }, async (s) => {
        await s.call('ensureDefaultAdmin', []);
    });
    await H.scenario('admin/no key folder set: written to the data folder', { dirs: { ...dirs, keysPath: '' }, env: clean, files: [dataDir] }, async (s) => {
        await s.call('ensureDefaultAdmin', []);
    });
}

(async () => {
    await H.start();
    try {
        await directedAuth();
        await randomAuth(Number(process.env.AUTHN_RANDOM_AUTH || 1300));
        await totpScenarios();
        await bruteForceScenarios();
        await tokenScenarios();
        await passwordScenarios();
        await adminScenarios();
    } finally {
        await H.stop();
    }
    const steps = H.scenarios.reduce((n, sc) => n + sc.steps.length, 0);
    write(`db-${H.dialect}.json`, { dialect: H.dialect, scenarios: H.scenarios });
    console.log(`${H.scenarios.length} scenarios, ${steps} steps`);
    process.exit(0);
})().catch((err) => {
    console.error(err);
    process.exit(1);
});
