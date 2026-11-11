'use strict';

/**
 * Records what services/emailService.js and lib/smtpSettingsHandlers.js do,
 * with the real database adapter on SQLite or (with ORACLE_PG_DSN) on
 * PostgreSQL. nodemailer is replaced by a stub that records what it was
 * given; what the real nodemailer would do with the same options is asked
 * from probe.js. See README.md.
 *
 * Writes ../node_<dialect>.json.gz: a list of steps, each with its input
 * and what Node answered, to be replayed in order.
 */

const fs = require('fs');
const path = require('path');
const lib = require('./lib');

const { dump, rng } = lib;
const J = JSON.stringify;

if (fs.existsSync(path.join(lib.WEB, '.env'))) {
    throw new Error('web-nodejs/.env exists: its values would leak into the recording');
}

// ---- nodemailer: the real one for the probe, a stub for the service ----

const nodemailerFile = require.resolve('nodemailer', { paths: [lib.WEB] });
const realNodemailer = require(nodemailerFile);
const { probe } = require('./probe')(realNodemailer, lib.WEB);

const seen = { created: [], used: null, sent: [], verified: 0 };
const behaviour = { verify: null, send: null };
lib.fakeModule(nodemailerFile, {
    createTransport(options) {
        seen.created.push(options);
        return {
            async sendMail(mail) {
                seen.used = options;
                seen.sent.push(mail);
                if (behaviour.send !== null) throw new Error(behaviour.send);
                return { messageId: '<oracle@localhost>' };
            },
            async verify() {
                seen.used = options;
                seen.verified += 1;
                if (behaviour.verify !== null) throw new Error(behaviour.verify);
                return true;
            },
        };
    },
});

/** A step's input value: JSON text, or the words JSON lacks. */
function value(text) {
    if (text === 'undefined') return undefined;
    if (text === 'NaN') return NaN;
    return JSON.parse(text);
}

function object(fields) {
    const out = {};
    for (const [key, text] of Object.entries(fields || {})) {
        const v = value(text);
        if (v !== undefined) out[key] = v;
    }
    return out;
}

function textOf(v) {
    try {
        return { text: String(v) };
    } catch (err) {
        return { throws: err.message };
    }
}

function fakeRes() {
    const res = { statusCode: 200, body: undefined };
    res.status = (code) => {
        res.statusCode = code;
        return res;
    };
    res.json = (body) => {
        res.body = JSON.stringify(body);
        return res;
    };
    return res;
}

// ---- inputs ----

const TEXTS = ['smtp.example.com', ' smtp.example.com ', '127.0.0.1', '::1', 'true', 'false', '0', '1', '587', ' 25 ', '0x19',
    '1e3', '25abc', 'abc', '********', 'p@ss "w" <o>&rd', 'a@b.example', 'a@b.example, c@d.example ,, ', 'Näme <ü@exämple.com>',
    '😀', 'line\nbreak', ' ', '', ' x ', 'Infinity', '-5', '65536', '99999999999999999999999', '9'.repeat(400),
    '+25', '25.9', '0b11', '1_000', '٢٥', '﻿ 26'];
const RAW = ['null', 'true', 'false', '0', '-0', '1', '-1', '25', '587', '465', '2525', '25.5', '65535', '65536', '70000', '1e21',
    '1e-7', '1e400', '-1e400', '0.5', '4294967321', '12345678901234567890', '[]', '[25]', '["127.0.0.1"]', '["::1"]', '["a","b"]',
    '[null]', '[[1]]', '[0]', '[false]', '[" x ",null,[2,[3]]]', '{}', '{"a":1}', '{"address":"x@y.example","name":"N"}',
    '{"toString":1}', '[{"toString":1}]', '[[{"toString":1}]]', '{"valueOf":1}', '{"toString":null,"valueOf":2}', '"\\ud800"',
    '"a\\u0000b"', '{"2":"b","a":{"z":1,"1":[1.0,2e0]},"1":"a"}', ...TEXTS.map((t) => J(t))];
const FIELDS = ['host', 'port', 'secure', 'user', 'pass', 'from', 'alert_email', 'tlsVerify'];
const BASE = { host: '"smtp.example.com"', port: '587', secure: 'false', user: '"user"', pass: '"secret"', from: '"from@example.com"', alert_email: '"ops@example.com"' };

/** JSON text of an object whose members are given as JSON text. */
function doc(fields) {
    return `{${Object.entries(fields).filter(([, v]) => v !== undefined).map(([k, v]) => `${J(k)}:${v}`).join(',')}}`;
}

const DOCS = ['', ' ', 'null', 'true', '5', '"text"', '[]', '[{"host":"x"}]', '{}', '{"host":""}', '{"host":null}', '{"host":0}',
    'not json', '{"host":"a"', '{"host":"a",}', "{'host':'a'}", '{"host":"a"} ', ' \t\r\n{"host":"a"}', '﻿{"host":"a"}',
    '{"host":"a"}x', '{"host":"a","host":"b"}', '{"host":"a","host":""}', '{"__proto__":{"host":"x"}}', '{"HOST":"x"}',
    '{"host":"a","extra":{"deep":[1,2,{"x":null}]}}', '{"host":"\\u0073mtp"}', '{"host":"a\\qb"}', '{"host":"a","port":01}',
    '{"host":"a","port":1.}', '{"host":"a","port":+1}', '{"host":"a","port":.5}', '{"host":"a","port":1e5}', '{"host":"a","port":-0}',
    '{"host":"a\tb"}', '{"host":"a","tlsVerify":null}', '{"host":"a","port":NaN}', '{"host":"a"}{"b":1}', '{"host":"a","port":5,"port":"x"}',
    '{"1":1,"host":"a","0":0}', '{"host":"a","port":1E2}', '{"host":"a","port":1e+2}', '{"host":"a","port":-}', '{"host":"a","x":"\\u12"}',
    '{"host":"a","x":"\\ud83d\\ude00\\udead"}', '{"host":"a", "port":1}', '{"host":"a","port":0.0}', '{"host":"a","port":1e-400}',
    '{"host":"a","port":"1","secure":"","user":0,"pass":0}', `{"host":${'['.repeat(200)}"a"${']'.repeat(200)}}`,
    '{"host":"a","x":nul}', '{"host":"a","x":tru}', '{"host":"a" "x":1}', '{"host" "a"}', '{host:"a"}', '{"host":"a","x":[1,]}',
    '{"host":"a","x":[,1]}', '"{\\"host\\":\\"a\\"}"', '{"host":"\\/a\\b\\f\\n\\r\\t\\"\\\\"}', '{"host":"a","x":"\u007f\u0080  "}',
    '{"host":true}', '{"host":[]}', '{"host":{}}', '{"host":"a","pass":"only"}', '{"host":"a","user":"only"}', '{"":"","host":"a"}'];

const ENV_HOSTS = ['', 'mail.example.org', ' mail.example.org ', '10.0.0.5', '0'];
const ENV_PORTS = [undefined, '', '25', 'abc', ' 25', '25.9', '0', '-1', '0x19', '1e3', '99999', '587abc', '+25', '00465', '9'.repeat(30), '-0'];
const ENV_FLAGS = [undefined, '', 'true', 'TRUE', 'True', '1', 'false', ' true', 'yes'];
const ENV_TEXTS = [undefined, '', 'envuser', ' spaced ', 'ops@example.org , x@example.org', '0'];

const steps = [];
function add(step) {
    steps.push(step);
}
const CFG = { smtpTlsVerify: false, host: '127.0.0.1', port: 5000, httpRedirect: true };
function state(stored, env, cfg) {
    add({ op: 'state', stored, env: env || {}, cfg: Object.assign({}, CFG, cfg || {}) });
}
const MAIL = { to: '"r@x.example"', subject: '"Subject"', text: '"Text"' };

function build() {
    const r = rng(20261110);

    // 1. Stored configurations: every field with every kind of value
    const stored = [doc(BASE)];
    for (const field of FIELDS) {
        stored.push(doc(Object.assign({}, BASE, { [field]: undefined })));
        for (const raw of RAW) stored.push(doc(Object.assign({}, BASE, { [field]: raw })));
    }
    stored.push(...DOCS);
    for (let i = 0; i < 450; i += 1) {
        const fields = {};
        for (const field of FIELDS) {
            if (r.chance(0.15)) continue;
            fields[field] = r.chance(0.45) ? BASE[field] || r.pick(RAW) : r.pick(RAW);
        }
        if (r.chance(0.8) && fields.host === undefined) fields.host = BASE.host;
        stored.push(doc(fields));
    }
    stored.forEach((text, i) => {
        const env = i % 7 === 3 ? { SMTP_HOST: 'env.example.org', SMTP_PORT: '2500', SMTP_ALERT_EMAIL: 'env@example.org' } : {};
        state(text, env, { smtpTlsVerify: i % 3 === 1 });
        add({ op: 'load' });
        add({ op: 'get' });
        add({ op: 'test', verify: i % 5 === 2 ? 'Invalid login: 535 5.7.8 Error: authentication failed' : null });
        add({ op: 'send', mail: MAIL, sendError: i % 6 === 4 ? 'connect ECONNREFUSED 10.0.0.1:587' : null });
        if (i % 4 === 0) add({ op: 'send', mail: MAIL, sendError: null });
        if (i % 9 === 0) {
            add({ op: 'reset' });
            add({ op: 'test', verify: null });
        }
    });

    // 2. The environment, read at call time
    const envs = [];
    for (const host of ENV_HOSTS) for (const port of ENV_PORTS) envs.push({ SMTP_HOST: host, SMTP_PORT: port });
    for (const secure of ENV_FLAGS) envs.push({ SMTP_HOST: 'mail.example.org', SMTP_SECURE: secure });
    for (const t of ENV_TEXTS) {
        envs.push({ SMTP_HOST: 'mail.example.org', SMTP_USER: t, SMTP_PASS: 'envpass' });
        envs.push({ SMTP_HOST: 'mail.example.org', SMTP_USER: 'envuser', SMTP_PASS: t });
        envs.push({ SMTP_HOST: 'mail.example.org', SMTP_FROM: t });
        envs.push({ SMTP_HOST: 'mail.example.org', SMTP_ALERT_EMAIL: t });
    }
    for (let i = 0; i < 250; i += 1) {
        envs.push({
            SMTP_HOST: r.pick(ENV_HOSTS), SMTP_PORT: r.pick(ENV_PORTS), SMTP_SECURE: r.pick(ENV_FLAGS), SMTP_USER: r.pick(ENV_TEXTS),
            SMTP_PASS: r.pick(ENV_TEXTS), SMTP_FROM: r.pick(ENV_TEXTS), SMTP_ALERT_EMAIL: r.pick(ENV_TEXTS),
            SMTP_TLS_VERIFY: r.pick(ENV_FLAGS), ALLOW_SELF_SIGNED_CERTS: r.pick(ENV_FLAGS),
        });
    }
    envs.forEach((env, i) => {
        const clean = {};
        for (const [k, v] of Object.entries(env)) if (v !== undefined) clean[k] = v;
        const storedText = [null, '', '{}', '{"host":""}', 'not json', '{"port":25}'][i % 6];
        state(storedText, clean, { smtpTlsVerify: i % 2 === 0 });
        add({ op: 'load' });
        add({ op: 'get' });
        add({ op: 'test', verify: null });
        add({ op: 'send', mail: MAIL, sendError: null });
        // The variables are read again at every call
        if (i % 5 === 0) {
            add({ op: 'env', env: Object.assign({}, clean, { SMTP_HOST: 'changed.example.org', SMTP_PORT: '2526' }) });
            add({ op: 'send', mail: MAIL, sendError: null });
            add({ op: 'env', env: {} });
            add({ op: 'send', mail: MAIL, sendError: null });
            add({ op: 'test', verify: null });
        }
    });

    // 3. Messages
    state(doc(BASE));
    const tos = ['"r@x.example"', '"a@x.example, b@x.example"', '" , ,"', '""', '["a@x.example"]', '["a@x.example","","  ","b@x.example"]', '[]',
        'undefined', '"Näme <ü@exämple.com>, x"', '[" "]', '["a, b"]', '"a,b,,c , "', '"\\u00a0,\\u2003x"'];
    const parts = ['undefined', '""', '"Plain"', '"<p>&amp; \\"q\\"</p>"', '"multi\\nline\\r\\n\\u2028"', '"😀"'];
    for (const to of tos) {
        for (const subject of parts) {
            add({ op: 'send', mail: { to, subject, text: r.pick(parts), html: r.pick(parts) }, sendError: r.chance(0.2) ? r.pick(['No recipients defined', '', 'Message failed: 554 5.7.1 "x" <y>']) : null });
        }
    }
    state(null);
    for (const to of tos) add({ op: 'send', mail: { to, subject: '"S"', text: '"T"' }, sendError: null });

    // 4. Alert mails
    const AV = ['undefined', 'null', 'true', 'false', '0', '5', '-1.5', '1e21', '1e-7', 'NaN', '1e999', '-1e999', '""', '" "', '"warning"', '"critical"',
        '"info"', '"ß straße ŉ ǆ ﬁ i̇ ı"', '"<b>&\\"x\\"</b> \'q\' &amp;"', '"a@x.example"', '"a@x.example, b@x.example"', '[]', '["a@x.example","b@x.example"]',
        '[1,[2,null]]', '{}', '{"a":1}', '{"toString":1}', '[{"toString":1}]', '"line\\nbreak"', '"2026-11-09T12:00:00.000Z"', '"dev-1"', '"0"',
        '"<script>alert(1)</script>"', '"😀 \\ud800"', '90', '0.1', '-0'];
    const ALERT = { severity: '"warning"', device_id: '"dev-1"', message: '"CPU high: cpu_usage = 95 (gt 90)"', triggered_at: '"2026-11-09T12:00:00.000Z"' };
    const RULE = { name: '"CPU high"', description: '""', condition_type: '"cpu_usage"', condition_op: '"gt"', condition_value: '90', notify_emails: '"ops@example.com"' };
    state(doc(BASE));
    add({ op: 'alert', alert: ALERT, rule: RULE, sendError: null });
    add({ op: 'alert', alert: ALERT, rule: RULE, sendError: 'Greeting never received' });
    for (const field of Object.keys(ALERT)) {
        for (const v of AV) add({ op: 'alert', alert: Object.assign({}, ALERT, { [field]: v }), rule: RULE, sendError: null });
    }
    for (const field of Object.keys(RULE)) {
        for (const v of AV) add({ op: 'alert', alert: ALERT, rule: Object.assign({}, RULE, { [field]: v }), sendError: null });
    }
    for (let i = 0; i < 500; i += 1) {
        const alert = {};
        const rule = {};
        for (const field of Object.keys(ALERT)) alert[field] = r.chance(0.6) ? ALERT[field] : r.pick(AV);
        for (const field of Object.keys(RULE)) rule[field] = r.chance(0.6) ? RULE[field] : r.pick(AV);
        add({ op: 'alert', alert, rule, sendError: r.chance(0.1) ? 'boom' : null });
    }
    state(null);
    add({ op: 'alert', alert: ALERT, rule: RULE, sendError: null });
    add({ op: 'alert', alert: ALERT, rule: Object.assign({}, RULE, { notify_emails: '""' }), sendError: null });
    add({ op: 'alert', alert: Object.assign({}, ALERT, { severity: '5' }), rule: Object.assign({}, RULE, { notify_emails: '""' }), sendError: null });

    // 5. Help request mails
    const HELP = { hostname: '"PC-01"', device_id: '"dev-1"', message: '"Need help"' };
    const urls = [{}, { PANEL_PUBLIC_URL: 'https://panel.example.org/' }, { PUBLIC_URL: 'https://pub.example.org/x?a=1&b="2"' },
        { PANEL_PUBLIC_URL: 'https://a.example', PUBLIC_URL: 'https://b.example' }];
    urls.forEach((env) => {
        state(doc(BASE), env);
        add({ op: 'help', to: '"alice@example.com"', help: HELP, folder: '"Support"', sendError: null });
        add({ op: 'help', to: '["alice@example.com","bob@example.com"]', help: HELP, folder: '""', sendError: 'boom' });
    });
    state(doc(BASE), { PANEL_PUBLIC_URL: 'https://panel.example.org' });
    for (const field of Object.keys(HELP)) {
        for (const v of AV) add({ op: 'help', to: '"alice@example.com"', help: Object.assign({}, HELP, { [field]: v }), folder: '"Support"', sendError: null });
    }
    for (const v of AV) add({ op: 'help', to: '"alice@example.com"', help: HELP, folder: v, sendError: null });
    for (let i = 0; i < 300; i += 1) {
        const help = {};
        for (const field of Object.keys(HELP)) help[field] = r.chance(0.5) ? HELP[field] : r.pick(AV);
        add({ op: 'help', to: r.pick(tos.filter((t) => t !== 'undefined')), help, folder: r.pick(AV), sendError: null });
    }

    // 6. The panel's address
    const publicUrls = [undefined, '', 'https://x.example', 'https://x.example/', 'https://x.example//', '/', 'x', ' https://x.example/ ', 'https://x.example/a/'];
    const hosts = ['0.0.0.0', '127.0.0.1', 'example.org', '::', '', ' 0.0.0.0', 'localhost'];
    const ports = [5000, 80, 443, 1, 65535, -1, 0];
    for (const panel of publicUrls) {
        for (const pub of [undefined, '', 'https://pub.example/']) {
            const env = {};
            if (panel !== undefined) env.PANEL_PUBLIC_URL = panel;
            if (pub !== undefined) env.PUBLIC_URL = pub;
            state(null, env, { host: r.pick(hosts), port: r.pick(ports), httpRedirect: r.chance(0.5) });
            add({ op: 'baseurl' });
        }
    }
    for (const host of hosts) for (const port of ports) for (const redirect of [true, false]) {
        state(null, {}, { host, port, httpRedirect: redirect });
        add({ op: 'baseurl' });
    }

    // 7. Saving the settings
    const existing = [null, doc(BASE), doc(Object.assign({}, BASE, { pass: '""' })), doc(Object.assign({}, BASE, { pass: undefined })), 'not json',
        'null', '"text"', '[1]', '5', doc(Object.assign({}, BASE, { pass: '5' })), doc(Object.assign({}, BASE, { pass: '{"k":[1,2.50,"x"]}' })),
        doc(Object.assign({}, BASE, { pass: '"********"' })), doc(Object.assign({}, BASE, { pass: '0' })), '', '{"pass":"no host"}',
        doc(Object.assign({}, BASE, { pass: '"\\ud83d\\ude00 \\ud800 \\u0000 \\u2028 <&>"' }))];
    const BODY = { host: '"smtp.new.example"', port: '2525', secure: 'true', user: '"newuser"', pass: '"********"', from: '"new@example.com"', alert_email: '" ops@new.example "' };
    const put = (storedText, body) => {
        state(storedText);
        add({ op: 'put', request: body });
        add({ op: 'load' });
        add({ op: 'get' });
    };
    for (const e of existing) {
        for (const pass of ['"********"', '""', '"new-secret"', undefined, 'null', '0', 'false', '" "', '"*******"', '["********"]', '{"a":1}', '5', 'true', '"********* "']) {
            put(e, doc(Object.assign({}, BODY, { pass })));
        }
    }
    for (const field of FIELDS) {
        put(doc(BASE), doc(Object.assign({}, BODY, { [field]: undefined })));
        for (const raw of RAW) put(r.chance(0.7) ? doc(BASE) : r.pick(existing), doc(Object.assign({}, BODY, { [field]: raw })));
    }
    for (const body of ['[]', '[{"host":"x"}]', '{}', '{"host":""}', '{"host":"only"}', null, '{"host":"a","host":"b","port":1,"port":"7"}',
        '{"host":"x","extra":1,"__proto__":{"pass":"p"}}', '{"host":"x","pass":"********","pass":"second"}']) {
        put(doc(BASE), body);
    }
    for (let i = 0; i < 450; i += 1) {
        const fields = {};
        for (const field of FIELDS) {
            if (r.chance(0.2)) continue;
            fields[field] = r.chance(0.4) ? BODY[field] || r.pick(RAW) : r.pick(RAW);
        }
        put(r.pick(existing), doc(fields));
    }
    // After a save the next use sets the transporter up again
    state(doc(BASE));
    add({ op: 'send', mail: MAIL, sendError: null });
    add({ op: 'put', request: doc(BODY) });
    add({ op: 'send', mail: MAIL, sendError: null });
    add({ op: 'put', request: doc(BODY) });
    add({ op: 'send', mail: MAIL, sendError: null });
    add({ op: 'test', verify: null });

    // 8. The notification log
    const names = ['help_request', 'x', '', 'Ünï cödé 😀', 'a'.repeat(300), "o'neil; DROP TABLE x;--", '%', '_', ' spaced ', 'HELP_REQUEST'];
    for (const type of names) {
        for (const id of ['42', '', '0042', 'Ünï', ' 42 ']) {
            const recipient = r.pick(['alice@example.com', 'ALICE@example.com', '', ' alice@example.com', 'ü@exämple.com']);
            add({ op: 'has', args: [type, id, recipient] });
            add({ op: 'logsent', args: [type, id, recipient] });
            add({ op: 'has', args: [type, id, recipient] });
            add({ op: 'logsent', args: [type, id, recipient] });
            add({ op: 'has', args: [type, id, `${recipient}x`] });
            add({ op: 'has', args: [`${type}x`, id, recipient] });
            add({ op: 'has', args: [type, `${id}x`, recipient] });
        }
    }
    add({ op: 'logcount' });
}

// ---- running ----

async function main() {
    const quiet = lib.captureConsole();
    const db = await lib.openDatabase();
    const emailService = lib.web('services/emailService');
    const handlers = lib.web('lib/smtpSettingsHandlers');
    const appConfig = lib.web('config/config');
    const audits = [];
    db.adapter.logAction = async (...args) => {
        audits.push(args);
    };

    build();
    quiet.take();
    const out = [];
    for (const s of steps) {
        const o = { ...s };
        seen.created = [];
        seen.used = null;
        seen.sent = [];
        seen.verified = 0;
        behaviour.verify = s.verify === undefined ? null : s.verify;
        behaviour.send = s.sendError === undefined ? null : s.sendError;
        audits.length = 0;
        try {
            switch (s.op) {
                case 'state':
                    lib.setEnv(s.env);
                    Object.assign(appConfig, s.cfg);
                    if (s.stored === null) await db.exec('DELETE FROM settings WHERE key = ?', ['smtp_config']);
                    else await db.adapter.setSetting('smtp_config', s.stored);
                    emailService.resetTransporter();
                    break;
                case 'env':
                    lib.setEnv(s.env);
                    break;
                case 'reset':
                    emailService.resetTransporter();
                    break;
                case 'load': {
                    o.config = dump(await emailService.loadSmtpConfig());
                    try {
                        o.alertEmail = { text: await emailService.getAlertEmail() };
                    } catch (err) {
                        o.alertEmail = { throws: err.message };
                    }
                    break;
                }
                case 'get': {
                    const res = fakeRes();
                    await handlers.getSmtpSettings({}, res);
                    o.status = res.statusCode;
                    o.body = res.body;
                    break;
                }
                case 'test': {
                    const res = fakeRes();
                    await handlers.testSmtpSettings({}, res);
                    o.status = res.statusCode;
                    o.body = res.body;
                    break;
                }
                case 'send': {
                    try {
                        o.ret = dump(await emailService.sendEmail(object(s.mail)));
                    } catch (err) {
                        o.throws = err.message;
                    }
                    break;
                }
                case 'alert': {
                    try {
                        o.ret = dump(await emailService.sendAlertEmail(object(s.alert), object(s.rule)));
                    } catch (err) {
                        o.throws = err.message;
                    }
                    break;
                }
                case 'help': {
                    try {
                        o.ret = dump(await emailService.sendHelpRequestEmail({ to: value(s.to), helpRequest: object(s.help), folderName: value(s.folder) }));
                    } catch (err) {
                        o.throws = err.message;
                    }
                    break;
                }
                case 'baseurl':
                    o.url = emailService.getPanelBaseUrl();
                    break;
                case 'put': {
                    const res = fakeRes();
                    const req = { body: s.request === null ? undefined : JSON.parse(s.request), session: { userId: 1 }, ip: '10.0.0.9' };
                    await handlers.putSmtpSettings(req, res);
                    o.status = res.statusCode;
                    o.body = res.body;
                    o.storedAfter = await db.adapter.getSetting('smtp_config');
                    o.audit = audits.map((a) => a.map(dump));
                    break;
                }
                case 'has':
                    try {
                        o.ret = await db.adapter.hasEmailNotificationSent(...s.args);
                    } catch (err) {
                        o.throws = err.message;
                    }
                    break;
                case 'logsent':
                    try {
                        await db.adapter.logEmailNotificationSent(...s.args);
                    } catch (err) {
                        o.throws = err.message;
                    }
                    break;
                case 'logcount': {
                    const rows = await db.exec('SELECT event_type, event_id, recipient, sent_at FROM email_notification_log ORDER BY id');
                    o.rows = rows.map((row) => [row.event_type, row.event_id, row.recipient]);
                    o.sentAt = rows.map((row) => dump(row.sent_at));
                    break;
                }
                default:
                    throw new Error(`unknown op ${s.op}`);
            }
        } catch (err) {
            o.oracleError = String(err && err.stack);
        }
        o.logs = quiet.take();
        o.created = [];
        for (const options of seen.created) o.created.push({ options: dump(options), probe: await probe(options) });
        if (seen.used) o.used = { options: dump(seen.used), probe: await probe(seen.used) };
        o.verified = seen.verified;
        o.sent = seen.sent.map((mail) => ({
            from: dump(mail.from), fromText: textOf(mail.from), to: dump(mail.to), subject: dump(mail.subject), text: dump(mail.text), html: dump(mail.html),
        }));
        out.push(o);
    }
    quiet.take();
    await db.close();
    quiet.restore();
    const broken = out.filter((o) => o.oracleError);
    if (broken.length) {
        console.error(broken[0]);
        throw new Error(`${broken.length} steps failed in the oracle itself`);
    }
    console.log(lib.writeVectors(`node_${lib.dialect}`, { dialect: lib.dialect, steps: out }, `${out.length} steps`));
    process.exit(0);
}

main().catch((err) => {
    process.stderr.write(`${err.stack}\n`);
    process.exit(1);
});
