'use strict';

// Vectors for the commercialization routes that pass a call on to the API
// server: routes/commercialization.routes.js of the Node console with the
// real guards and the real API client in front of a scripted API server.
// The modules behind the clock and mail routes are stubbed; those routes
// have their own tests. Writes ../vectors.json.

const path = require('path');
const fs = require('fs');
const Module = require('module');
const oracleDir = path.join(__dirname, '..', '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle');
const { start, accounts, apiAnswers } = require(path.join(oracleDir, 'harness'));
const { random } = require(path.join(oracleDir, 'lib'));

const load = Module._load;
Module._load = function (request, parent, isMain) {
    if (parent && /commercialization\.routes\.js$/.test(parent.filename)) {
        if (/services\/database$/.test(request)) return { getSetting: async () => null, setSetting: async () => {}, logAction: async () => {} };
        if (/helpRequestEmailService$/.test(request)) return { parseCommercializationEmailConfig: () => ({}), CONFIG_KEY: 'commercialization_email_config' };
        if (/smtpSettingsHandlers$/.test(request)) return { getSmtpSettings: (_req, res) => res.json({ configured: false }) };
        if (/billingClockConfigService$/.test(request)) return {};
        if (/restartCoordinator$/.test(request)) return {};
    }
    return load.apply(this, arguments);
};

const json = 'application/json';
const ok = { status: 200, type: json, body: '{"ok":true,"items":[]}' };

// Every route that passes its call on: method, path with good
// parameters, whether it sends a body, whether it is a file
const routes = [
    ['GET', '/api/panel/billing/timesync/status'],
    ['POST', '/api/panel/billing/timesync/check', true],
    ['GET', '/api/panel/billing/packages'],
    ['GET', '/api/panel/billing/packages/pkg-1/preview'],
    ['POST', '/api/panel/billing/packages', true],
    ['PUT', '/api/panel/billing/packages/pkg-1', true],
    ['DELETE', '/api/panel/billing/packages/pkg-1'],
    ['GET', '/api/panel/billing/contracts'],
    ['POST', '/api/panel/billing/contracts', true],
    ['PUT', '/api/panel/billing/contracts/con-1', true],
    ['DELETE', '/api/panel/billing/contracts/con-1'],
    ['GET', '/api/panel/billing/stats'],
    ['GET', '/api/panel/billing/sessions'],
    ['GET', '/api/panel/billing/check'],
    ['GET', '/api/panel/billing/sessions/pending'],
    ['GET', '/api/panel/billing/sessions/export', false, true],
    ['GET', '/api/panel/billing/reports'],
    ['GET', '/api/panel/billing/reports/export', false, true],
    ['POST', '/api/panel/billing/sessions/ses-1/report', true],
    ['GET', '/api/panel/billing/currencies'],
    ['PUT', '/api/panel/billing/currencies/EUR', true],
];
const queried = routes.filter(([m, p]) => m === 'GET' && !/timesync|\/packages$|stats|currencies/.test(p));

// Answers of the API server for the files: CSV with cells a spreadsheet
// would run, text without a type, PDF bytes, a refusal as JSON and as text
const fileAnswers = [
    { status: 200, type: 'text/csv; charset=utf-8', disposition: 'attachment; filename="billing-reports-20261231.csv"',
        body: 'session_id,summary,amount\nbs-1,"=HYPERLINK(""http://x"")",12.35\nbs-2,+1,-0.00\nbs-3,@SUM(A1),1e21\n' },
    { status: 200, type: 'text/csv', disposition: 'attachment; filename="billing-sessions-20260329.csv"', body: 'id,started_at\n1,2026-03-29T01:59:59+01:00\n2,2026-03-29T03:00:00+02:00\n' },
    { status: 200, type: 'application/pdf', disposition: 'attachment; filename="billing-reports-20261101.pdf"', base64: Buffer.from('%PDF-1.4\n\x00\xff\xfe binary\n%%EOF').toString('base64') },
    { status: 200, base64: Buffer.from([0, 1, 2, 255]).toString('base64') },
    { status: 200, type: 'text/plain', body: 'Generated: 2026-02-28T23:59:59Z' },
    { status: 200, type: 'application/json', body: '{"x":1}' },
    { status: 400, type: 'application/json', body: '{"error":"only csv supported"}' },
    { status: 400, type: 'application/json', body: '{"error":"format must be csv or pdf"}' },
    { status: 500, type: 'text/plain', body: 'internal' },
    { status: 404 },
    { hang: true },
];

// Money and time as the API server writes them: rounding, large and
// small amounts, a day, a month and a year border and a change of summer
// time, in UTC and with offsets
const moneyAnswers = [
    { status: 200, type: json, body: '{"amount_overage":12.345,"overage_rate":0.1,"sum":0.30000000000000004,"big":1e21,"cents":1999.995,"neg":-0.0,"int":9007199254740993}' },
    { status: 200, type: json, body: '{"period_ym":"2026-03","from":"2026-02-28T23:00:00Z","to":"2026-03-31T22:00:00Z","dst":"2026-03-29T01:00:00Z","timezone":"Europe/Berlin"}' },
    { status: 200, type: json, body: '{"sessions":[{"started_at":"2026-10-25T00:59:59+02:00","ended_at":"2026-10-25T02:00:00+01:00","billed_minutes":61}]}' },
    { status: 200, type: json, body: '{"started_at":"2025-12-31T23:59:59.999Z","ended_at":"2026-01-01T00:00:00Z","current_period_ym":"2026-01"}' },
    { status: 201, type: json, body: '{"id":"c-1","hourly_rate":19.99,"currency":"EUR","remaining_minutes":-0,"valid_until":"2027-03-28T01:30:00Z"}' },
];

async function main() {
    const oracle = await start((app, loadRoute) => {
        app.use('/', loadRoute('./routes/commercialization.routes'));
    });
    const vectors = [];
    const send = async (request, answer) => vectors.push(await oracle.call(request, answer));
    const body = '{"name":"Package","included_minutes":90,"overage_rate":12.345,"currency":"EUR","id":"x"}';

    // Who may call what
    for (const [method, p, sends, file] of routes) {
        for (const session of [undefined, accounts.pro, accounts.viewer, accounts.server_admin, accounts.operator, accounts.global_admin, accounts.admin]) {
            const request = { method, path: p, session, binary: !!file };
            if (sends) Object.assign(request, { type: json, body });
            await send(request, file ? fileAnswers[0] : ok);
        }
    }

    // Every kind of answer of the API server
    for (const [method, p, sends, file] of routes) {
        for (const answer of file ? fileAnswers : [...apiAnswers, ...moneyAnswers]) {
            const request = { method, path: p, session: accounts.admin, binary: !!file };
            if (sends) Object.assign(request, { type: json, body: '{"a":1}' });
            await send(request, answer);
        }
    }

    // Parameters that may and may not go into a path
    const params = ['a.b', 'a..b', 'a:b@c', 'a%25b', 'a+b', 'a%2Bb', 'a%20b', '%20a%20', 'a%2Fb', '%2E%2E', '%2E', 'a,b', 'a*b', 'A-Z_0.9',
        'a'.repeat(256), 'a'.repeat(257), '%C3%A9', 'a%00', '%09abc%0A', 'EUR', 'eur'];
    for (const [method, p, sends, file] of routes) {
        if (!/pkg-1|con-1|ses-1|EUR/.test(p)) continue;
        for (const bad of params) {
            const request = { method, path: p.replace(/pkg-1|con-1|ses-1|EUR/, bad), session: accounts.admin, binary: !!file };
            if (sends) Object.assign(request, { type: json, body: '{}' });
            await send(request, ok);
        }
    }

    // The whole query goes on
    const queries = ['', 'format=csv', 'format=pdf&org_id=acme', 'period=2026-03', 'period=2026-03&period=2026-04', 'period[]=2026-03',
        'period[x]=a', 'period[toString]=x', 'limit=3&status=closed', 'limit[toString]=1', 'device_id=900100001', 'device_id=a%20b%26c%3Dd%2Be',
        'org_id=%E4%B8%96%E7%95%8C', 'a=1&b=2&a=3', 'x', 'x=', '=x', 'a*b~c!d(e)=1', 'z=%zz', 'q=a+b', '__proto__=1', 'constructor=2',
        'toString=3', 'a[b][c]=1', 'a[0]=x&a[1]=y', 'a[2]=x&a[0]=y', '10=a&2=b&b=c', 'k=' + 'v'.repeat(300), 'device_id=x#frag', 'a%5Bb%5D=1'];
    const next = random(23);
    const tokens = ['period', 'org_id', 'format', 'limit', '[]', '[0]', '[x]', '[toString]', '=', '=', '&', '&', 'csv', 'a+b', 'x', '%5B', '%5D', '%20', '', ',', '.', '1', '%26'];
    for (let i = 0; i < 400; i++) {
        let q = '';
        const n = 1 + next(9);
        for (let j = 0; j < n; j++) q += tokens[next(tokens.length)];
        queries.push(q);
    }
    for (const q of queries) {
        for (const [method, p, , file] of queried) {
            await send({ method, path: q ? `${p}?${q}` : p, session: accounts.operator, binary: !!file }, file ? fileAnswers[0] : ok);
        }
    }
    for (const q of queries.slice(0, 30)) {
        await send({ path: `/api/panel/billing/packages/a%2Fb/preview?${q}`, session: accounts.admin }, ok);
    }

    // Bodies
    const bodies = ['{}', '[]', '["a","b"]', '{"z":1,"a":2,"10":3,"2":4}', '{"__proto__":{"x":1},"constructor":2,"prototype":3,"keep":{"constructor":4}}',
        '{"n":-0.0,"big":12345678901234567890,"amount":1999.995,"t":"\\u00e9 <b>\\u2028"}', ' {"spaced" : true } ', '{"summary":"=cmd|calc"}'];
    for (const b of bodies) {
        for (const [method, p, sends] of routes) {
            if (sends) await send({ method, path: p, session: accounts.admin, type: json, body: b }, ok);
        }
    }
    for (const [method, p, sends] of routes) {
        if (!sends) continue;
        await send({ method, path: p, session: accounts.admin }, ok);
        await send({ method, path: p, session: accounts.admin, type: 'application/x-www-form-urlencoded', body: 'name=n&x=1' }, ok);
    }

    // Other spellings Express takes for the same route
    await send({ path: '/API/Panel/Billing/Packages/', session: accounts.admin }, ok);
    await send({ method: 'HEAD', path: '/api/panel/billing/stats', session: accounts.admin }, ok);
    await send({ method: 'DELETE', path: '/api/panel/billing/contracts/Con-1/', session: accounts.admin }, ok);
    await send({ path: '/Api/Panel/Billing/Reports/Export?format=csv', session: accounts.admin, binary: true }, fileAnswers[1]);

    oracle.stop();
    fs.writeFileSync(path.join(__dirname, '..', 'vectors.json'), JSON.stringify(vectors) + '\n');
    console.log(`vectors.json: ${vectors.length} vectors`);
}

main().catch((err) => {
    console.error(err);
    process.exit(1);
});
