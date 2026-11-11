'use strict';

/**
 * Records what services/alertRulesEngine.js does. See README.md.
 *
 *   cond.json.gz        evalCondition for values, operators and thresholds
 *                       of every type
 *   mem.json.gz         evaluateRules, evaluateTelemetry and periodicCheck
 *                       with the database adapter's three methods replaced
 *                       by functions that hand out rules of every type and
 *                       write down what they are called with
 *   db_<dialect>.json.gz  the same three functions on the real adapter and
 *                       real rows, on SQLite or (with ORACLE_PG_DSN)
 *                       PostgreSQL
 *
 * The mail service and the device list are fakes that record their calls.
 */

const lib = require('./lib');

const { dump, rng } = lib;
const J = JSON.stringify;
const pg = lib.dialect === 'postgres';

const mails = [];
const mailBehaviour = { mode: 'ok' };
lib.fakeModule(`${lib.WEB}/services/emailService.js`, {
    sendAlertEmail(alert, rule) {
        mails.push({ alert, rule });
        if (mailBehaviour.mode === 'throws') throw new Error('mail threw');
        return mailBehaviour.mode === 'rejects' ? Promise.reject(new Error('mail rejected')) : Promise.resolve(true);
    },
});
const deviceList = { value: [], throws: null, calls: 0 };
lib.fakeModule(`${lib.WEB}/services/serverBackend.js`, {
    async getAllDevices() {
        deviceList.calls += 1;
        if (deviceList.throws !== null) throw new Error(deviceList.throws);
        return deviceList.value;
    },
});

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

const flush = () => new Promise((resolve) => setImmediate(resolve));

function mailRecord({ alert, rule }) {
    const pick = (o, keys) => Object.fromEntries(keys.map((k) => [k, dump(o[k])]));
    return {
        alert: pick(alert, ['rule_id', 'device_id', 'severity', 'message', 'acknowledged']),
        triggered: dump(alert.triggered_at),
        rule: pick(rule, ['name', 'description', 'condition_type', 'condition_op', 'condition_value', 'notify_emails']),
    };
}

const ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

// ---- inputs ----

const NUMBERS = ['0', '-0', '1', '-1', '5', '90', '95', '89.99999', '90.00000001', '0.1', '0.30000000000000004', '16777217', '1e21', '1e-7',
    '1e999', '-1e999', 'NaN', '4294967296', '2.5', '-2.5', '0.5', '-0.5', '0.49999999999999994', '300', '7200'];
const ODD = ['undefined', 'null', 'true', 'false', '""', '" "', '"95"', '"  95  "', '"abc"', '"0x5f"', '"1e2"', '"Infinity"', '"-Infinity"', '"1,5"',
    '"0b11"', '"\\n90\\t"', '[]', '[95]', '["95"]', '[1,2]', '[[90]]', '[null]', '{}', '{"a":1}', '{"toString":1}', '[{"toString":1}]', '{"valueOf":95}'];
const VALUES = [...NUMBERS, ...ODD];
const OPS = ['gt', 'gte', 'lt', 'lte', 'eq', 'neq'];
const ODD_OPS = ['', 'GT', ' gt', 'x', 'null', 'undefined', ...Object.getOwnPropertyNames(Object.prototype)];
const DEVICES = ['"dev-1"', '""', 'null', 'undefined', '0', '123', 'true', 'false', '["dev-1"]', '{"a":1}', '"other"', '"DEV-1"', '{"toString":1}', '" dev-1"'];

function condVectors(engine) {
    const out = [];
    for (const op of [...OPS, ...ODD_OPS]) {
        for (const v of VALUES) {
            for (const t of VALUES) {
                const rec = { v, op, t };
                try {
                    rec.out = !!engine.evalCondition(value(v), op, value(t));
                } catch (err) {
                    rec.throws = err.message;
                }
                out.push(rec);
            }
        }
    }
    return out;
}

// ---- the adapter replaced: rules of every type ----

const RULE = { id: '7', name: '"Rule"', description: '""', enabled: '1', condition_type: '"cpu_usage"', condition_op: '"gt"', condition_value: '90',
    severity: '"warning"', scope_device_id: 'null', cooldown_secs: '300', notify_emails: '"ops@example.com"' };
const FIELD_VALUES = {
    id: ['7', '"7"', '0', 'null'],
    name: ['"CPU high"', '""', 'null', 'undefined', '5', '"<b>&"', '"😀"', '{"toString":1}', '[1,[2]]'],
    description: ['""', 'null', '"About"', 'undefined'],
    condition_type: ['"cpu_usage"', 'null', '"x"'],
    condition_op: [...OPS.map((o) => J(o)), ...ODD_OPS.map((o) => J(o)), 'null', 'undefined', '5', 'true', '["gt"]'],
    condition_value: VALUES,
    severity: ['"warning"', '"critical"', '""', 'null', 'undefined', '0', '5', '"<i>"'],
    scope_device_id: ['null', 'undefined', '""', '"dev-1"', '"other"', '"DEV-1"', '0', '123', '"123"', 'true', '["dev-1"]'],
    cooldown_secs: ['300', '0', 'null', 'undefined', '1.5', '-5', '"abc"', '"600"', '"0x10"', 'true', 'false', '[]', '[600]', '{}', 'NaN', '1e999', '1e10',
        '""', '{"toString":1}'],
    notify_emails: ['"ops@example.com"', '""', 'null', 'undefined', '0', '5', '["a@x.example"]', '{}'],
};

function memCases() {
    const r = rng(77001);
    const cases = [];
    const rule = (over) => Object.assign({}, RULE, over);
    const add = (c) => cases.push(Object.assign({ recent: 'none', create: 'ok', mail: 'ok' }, c));

    // Metric values and device ids of every type
    for (const metric of VALUES) {
        for (const device of DEVICES) add({ fn: 'rules', rules: [rule({})], device, type: 'cpu_usage', metric });
    }
    // Every rule field with every kind of value, for a metric over, on and under the limit
    for (const [field, list] of Object.entries(FIELD_VALUES)) {
        for (const v of list) {
            for (const metric of ['95', '90', '5']) {
                add({ fn: 'rules', rules: [rule({ [field]: v })], device: '"dev-1"', type: 'cpu_usage', metric, recent: r.pick(['none', 'none', 'all', 'throws']) });
            }
        }
    }
    // Operators against thresholds
    for (const op of OPS) {
        for (const t of VALUES) {
            for (const metric of ['90', '0.1', '16777217', '-0', '1e21']) {
                add({ fn: 'rules', rules: [rule({ condition_op: J(op), condition_value: t })], device: '"dev-1"', type: 'cpu_usage', metric });
            }
        }
    }
    // The cooldown with and without a floor
    for (const cooldown of FIELD_VALUES.cooldown_secs) {
        for (const min of ['undefined', '0', '60', '7200', '0.5', 'NaN', '1e999']) {
            add({ fn: 'rules', rules: [rule({ cooldown_secs: cooldown })], device: '"dev-1"', type: 'cpu_usage', metric: '95', min, recent: r.pick(['none', 'all']) });
        }
    }
    // The outcomes of the three database calls and of the mail
    for (const recent of ['none', 'all', 'throws']) {
        for (const create of ['ok', 'throws']) {
            for (const mail of ['ok', 'rejects', 'throws']) {
                for (const notify of ['"ops@example.com"', '""']) {
                    add({ fn: 'rules', rules: [rule({ notify_emails: notify }), rule({ id: '8', name: '"Second"', notify_emails: notify })], device: '"dev-1"',
                        type: 'cpu_usage', metric: '95', recent, create, mail });
                }
            }
        }
    }
    add({ fn: 'rules', rules: [], device: '"dev-1"', type: 'cpu_usage', metric: '95' });
    add({ fn: 'rules', rules: [rule({})], device: '"dev-1"', type: 'cpu_usage', metric: '95', loadThrows: true });
    add({ fn: 'rules', rules: [rule({})], device: '"dev-1"', type: '', metric: '95' });
    // A rule that fails ends the evaluation: the rules after it are not looked at
    for (const op of ODD_OPS) {
        add({ fn: 'rules', rules: [rule({ id: '1', name: '"First"' }), rule({ id: '2', condition_op: J(op) }), rule({ id: '3', name: '"Third"' })], device: '"dev-1"',
            type: 'cpu_usage', metric: '95' });
    }
    // Random rules
    for (let i = 0; i < 1500; i += 1) {
        const rules = [];
        const n = 1 + r.int(3);
        for (let k = 0; k < n; k += 1) {
            const over = { id: String(k + 1) };
            for (const [field, list] of Object.entries(FIELD_VALUES)) {
                if (field !== 'id' && r.chance(0.3)) over[field] = r.pick(list);
            }
            rules.push(rule(over));
        }
        add({ fn: 'rules', rules, device: r.chance(0.8) ? '"dev-1"' : r.pick(DEVICES), type: 'cpu_usage', metric: r.pick(VALUES),
            min: r.chance(0.2) ? r.pick(['0', '60', '7200']) : 'undefined', recent: r.pick(['none', 'none', 'all', 'throws']), create: r.chance(0.1) ? 'throws' : 'ok',
            mail: r.pick(['ok', 'ok', 'rejects']) });
    }

    // Telemetry
    const always = [rule({ id: '1', condition_type: '"cpu_usage"', condition_op: '"gte"', condition_value: '-1e999', notify_emails: '""' })];
    const TV = [...VALUES, '1024', '4096', '8589934592', '17179869184', '"4096"', '3', '7', '1e308'];
    for (const cpu of VALUES) add({ fn: 'telemetry', rules: always, device: '"dev-1"', telemetry: { cpu_usage_percent: cpu } });
    for (const used of TV) {
        for (const total of TV) add({ fn: 'telemetry', rules: always, device: '"dev-1"', telemetry: { memory_used_bytes: used, memory_total_bytes: total } });
    }
    for (let i = 0; i < 1200; i += 1) {
        const used = String(Math.floor(r.next() * 2 ** r.int(40)));
        const total = String(1 + Math.floor(r.next() * 2 ** r.int(40)));
        add({ fn: 'telemetry', rules: always, device: r.chance(0.9) ? '"dev-1"' : r.pick(DEVICES),
            telemetry: { cpu_usage_percent: r.pick(VALUES), memory_used_bytes: r.chance(0.5) ? used : (r.next() * 100).toFixed(r.int(18)), memory_total_bytes: total } });
    }

    // The offline sweep
    const now = Date.parse('2026-11-09T12:00:00Z');
    const offline = [rule({ id: '6', condition_type: '"offline_duration"', condition_value: '3600', notify_emails: '""' })];
    const DATES = ['"2026-11-09T10:00:00Z"', '"2026-11-09T11:59:00Z"', '"2026-11-09T12:00:00Z"', '"2026-11-09T12:00:01Z"', '"2026-11-09T11:59:59.999Z"',
        '"2026-11-09T11:59:58.999Z"', '"2026-11-09T10:00:00.123456789Z"', '"2026-11-09T11:00:00+01:00"', '"2026-11-09T10:00:00"', '"2026-11-09 10:00:00"',
        '"2026-11-09"', '"0001-01-01T00:00:00Z"', '""', 'null', 'undefined', '0', '1', '1700000000000', '1700000000', 'true', 'false', '[]', '["2026-11-09T10:00:00Z"]',
        '{}', '{"toString":1}', '"never"', '"Mon, 09 Nov 2026 10:00:00 GMT"', '"Nov 9 2026 10:00"', '"11/9/2026"', '"2026-11-09T10:00:00.000+0000"', '"2026-11-09T24:00:00Z"',
        '"2026-13-09T10:00:00Z"', '" 2026-11-09T10:00:00Z "', '"2026-11-08T10:00:00-05:00"', '"1969-12-31T23:59:59Z"', '"+275760-09-13T00:00:00.000Z"',
        '"-271821-04-20T00:00:00.000Z"', '"275760-09-13T00:00:00.001Z"', '"2026"', '"12"', '"2026-11"'];
    const FLAGS = ['undefined', 'null', 'false', 'true', '0', '1', '""', '"false"', '"0"', '[]', '{}', 'NaN'];
    const device = (fields) => `{${Object.entries(fields).filter(([, v]) => v !== 'undefined').map(([k, v]) => `${J(k)}:${v === 'NaN' ? 'null' : v}`).join(',')}}`;
    const IDS = ['"dev-1"', '""', 'null', 'undefined', '0', '123', 'true'];
    const sweep = (devices, more) => add(Object.assign({ fn: 'sweep', rules: offline, now, devices: `[${devices.join(',')}]` }, more || {}));
    for (const last of DATES) sweep([device({ id: '"dev-1"', online: 'false', last_online: last })]);
    for (const flag of ['online', 'banned', 'soft_deleted', 'remote_target']) {
        for (const v of FLAGS) sweep([device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"', [flag]: v }), device({ id: '"dev-2"', last_online: '"2026-11-09T09:00:00Z"' })]);
    }
    for (const id of IDS) sweep([device({ id, last_online: '"2026-11-09T10:00:00Z"' })]);
    sweep(['null', '0', '"text"', '[]', device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"' })]);
    sweep([]);
    sweep([device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"' })], { devicesThrow: 'Go server unreachable' });
    sweep([device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"' })], { rules: [] });
    sweep([device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"' })], { loadThrows: true });
    sweep([device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"' })], { rules: [rule({ id: '6', condition_type: '"offline_duration"', condition_value: '30', cooldown_secs: '300', scope_device_id: '"dev-1"' }),
        rule({ id: '7', condition_type: '"offline_duration"', condition_value: '30', cooldown_secs: '86400', scope_device_id: '"other"' }),
        rule({ id: '8', condition_type: '"offline_duration"', condition_value: '30', cooldown_secs: '86400' })] });
    // A failing rule ends the sweep at the first device it meets
    sweep([device({ id: '"dev-1"', last_online: '"2026-11-09T10:00:00Z"' }), device({ id: '"dev-2"', last_online: '"2026-11-09T09:00:00Z"' })],
        { rules: [rule({ id: '6', condition_type: '"offline_duration"', condition_op: '"valueOf"' })] });
    for (let i = 0; i < 1500; i += 1) {
        const devices = [];
        const n = r.int(6);
        for (let k = 0; k < n; k += 1) {
            devices.push(device({
                id: r.chance(0.85) ? J(`dev-${k}`) : r.pick(IDS), online: r.chance(0.7) ? 'false' : r.pick(FLAGS), banned: r.chance(0.8) ? 'undefined' : r.pick(FLAGS),
                soft_deleted: r.chance(0.8) ? 'undefined' : r.pick(FLAGS), remote_target: r.chance(0.9) ? 'undefined' : r.pick(FLAGS), last_online: r.pick(DATES),
            }));
        }
        const rules = [];
        const m = 1 + r.int(2);
        for (let k = 0; k < m; k += 1) {
            rules.push(rule({ id: String(k + 1), condition_type: '"offline_duration"', condition_op: J(r.pick(OPS)), condition_value: r.pick(['30', '3600', '7200', '7201', '1e12']),
                cooldown_secs: r.pick(FIELD_VALUES.cooldown_secs), scope_device_id: r.chance(0.8) ? 'null' : '"dev-1"', notify_emails: r.chance(0.5) ? '""' : '"ops@example.com"' }));
        }
        sweep(devices, { rules, now: now + r.int(3) * 500, recent: r.pick(['none', 'none', 'all', 'throws']) });
    }
    // A rule for one device compares the id strictly: the same letters in another type are another device
    for (const scope of ['"123"', '"true"', '"dev-1"', '"[object Object]"', '"1,2"', '"null"', '""']) {
        for (const dev of ['123', 'true', '["dev-1"]', '{"a":1}', '[1,2]', '"123"', '"dev-1"', '"true"', '"1,2"']) {
            add({ fn: 'rules', rules: [rule({ scope_device_id: scope })], device: dev, type: 'cpu_usage', metric: '95' });
        }
    }
    // Ids the Go API never sends: a list or an object
    for (const id of ['["dev-1"]', '{"a":1}', '{"toString":1}']) sweep([device({ id, last_online: '"2026-11-09T10:00:00Z"' })], { oddId: true });
    return cases;
}

async function runMem(engine, adapter, cases) {
    const real = { getAlertRules: adapter.getAlertRules, getRecentAlert: adapter.getRecentAlert, createAlert: adapter.createAlert };
    const calls = { filters: [], recent: [], created: [] };
    let current = null;
    adapter.getAlertRules = async (filters) => {
        calls.filters.push(dump(filters));
        if (current.loadThrows) throw new Error('load failed');
        return current.rules.map(object);
    };
    adapter.getRecentAlert = async (ruleId, deviceId, cooldown) => {
        calls.recent.push([dump(ruleId), dump(deviceId), dump(cooldown)]);
        if (current.recent === 'throws') throw new Error('recent failed');
        return current.recent === 'all' ? { id: 1 } : null;
    };
    adapter.createAlert = async (alert) => {
        const { triggered_at: triggered, ...rest } = alert;
        calls.created.push({ alert: dump(rest), iso: typeof triggered === 'string' && ISO.test(triggered) });
        if (current.create === 'throws') throw new Error('create failed');
        return { id: 1, ...alert };
    };
    const quiet = lib.captureConsole();
    const out = [];
    for (const c of cases) {
        current = c;
        calls.filters = [];
        calls.recent = [];
        calls.created = [];
        mails.length = 0;
        mailBehaviour.mode = c.mail;
        deviceList.calls = 0;
        const o = { ...c };
        try {
            if (c.fn === 'rules') {
                const opts = c.min === undefined || c.min === 'undefined' ? undefined : { minCooldownSecs: value(c.min) };
                await engine.evaluateRules(value(c.device), c.type, value(c.metric), opts);
            } else if (c.fn === 'telemetry') {
                await engine.evaluateTelemetry(value(c.device), object(c.telemetry));
            } else {
                deviceList.value = JSON.parse(c.devices);
                deviceList.throws = c.devicesThrow === undefined ? null : c.devicesThrow;
                await engine.periodicCheck({ now: c.now });
            }
        } catch (err) {
            o.throws = err.message;
        }
        await flush();
        o.filters = calls.filters;
        o.recentCalls = calls.recent;
        o.created = calls.created;
        o.mails = mails.map(mailRecord);
        o.listCalls = deviceList.calls;
        o.logs = quiet.take();
        out.push(o);
    }
    quiet.restore();
    Object.assign(adapter, real);
    return out;
}

// ---- real rows ----

function dbCases() {
    const r = rng(77002);
    const cases = [];
    const yes = pg ? 'true' : '1';
    const base = { name: '"Rule"', description: '""', enabled: yes, condition_type: '"cpu_usage"', condition_op: '"gt"', condition_value: '90',
        severity: '"warning"', scope_device_id: 'null', cooldown_secs: '300', notify_emails: '"ops@example.com"' };
    let serial = 0;
    const rule = (over) => ({ id: (serial += 1), cols: Object.assign({}, base, over) });
    const add = (c) => cases.push(Object.assign({ history: [], mail: 'ok' }, c));
    const call = (metric, more) => Object.assign({ fn: 'rules', device: '"dev-1"', type: 'cpu_usage', metric }, more || {});

    // Operators on real rows
    for (const op of OPS) {
        for (const metric of ['89', '90', '91']) add(Object.assign({ rules: [rule({ condition_op: J(op) })] }, call(metric)));
    }
    // The limit as each database keeps it
    const limits = pg
        ? ['90', '90.5', '0.1', '16777217', '1e21', '3.3', '1e-7', '"NaN"', '"Infinity"', '"-Infinity"', '123456.789', '0.30000000000000004', '-0', '3.4e38', '1e-45', '0']
        : ['90', '90.5', '0.1', '16777217', '1e21', '3.3', '1e-7', '"0x10"', '"abc"', '" 12 "', '""', '1e999', '-1e999', '123456.789', '0.30000000000000004', '-0', '"1e2"', '"12abc"', '0'];
    for (const limit of limits) {
        for (const op of OPS) {
            for (const metric of ['0.1', '16', '12', '100', '16777217', '16777216', '123456.789', '123456.79', '0.3', '1e21', '3.3', '0']) {
                if (r.chance(0.45)) add(Object.assign({ rules: [rule({ condition_op: J(op), condition_value: limit })] }, call(metric)));
            }
        }
    }
    // The cooldown, with alerts raised a while ago
    const cooldowns = pg ? ['null', '0', '300', '-5', '2147483647', '60', '1'] : ['null', '0', '300', '1.5', '-5', '"abc"', '"0x10"', '" 60 "', '1e10', '60', '1', '1e300', '""'];
    for (const cooldown of cooldowns) {
        for (const ago of [5, 30, 58, 62, 290, 310, 3600, 90000, -30]) {
            for (const form of ['iso', 'space']) {
                const ru = rule({ cooldown_secs: cooldown });
                add(Object.assign({ rules: [ru], history: [{ rule: ru.id, device: '"dev-1"', ago, form }] }, call('95')));
            }
        }
    }
    // An alert of another rule or another device does not count
    {
        const a = rule({});
        const b = rule({ name: '"Other"' });
        add(Object.assign({ rules: [a, b], history: [{ rule: b.id, device: '"dev-1"', ago: 10, form: 'iso' }, { rule: a.id, device: '"dev-2"', ago: 10, form: 'iso' }] }, call('95')));
        add(Object.assign({ rules: [a, b], history: [{ rule: a.id, device: '"dev-1"', ago: 10, form: 'iso' }, { rule: a.id, device: '"dev-1"', ago: 4000, form: 'iso' }] }, call('95')));
    }
    // Which rules are looked at: the flag, the type, the device
    const flags = pg ? ['true', 'false', 'null'] : ['1', '0', 'null', '2', '"1"', '"true"', '1.5', '-1'];
    for (const flag of flags) add(Object.assign({ rules: [rule({ enabled: flag })] }, call('95')));
    for (const type of ['"cpu_usage"', '"memory_usage"', '"custom"', '"CPU_USAGE"']) add(Object.assign({ rules: [rule({ condition_type: type })] }, call('95')));
    for (const scope of ['null', '""', '"dev-1"', '"other"', '"DEV-1"', '" dev-1"']) add(Object.assign({ rules: [rule({ scope_device_id: scope })] }, call('95')));
    // What the alert and the mail say
    for (const over of [{ severity: 'null' }, { severity: '""' }, { severity: '"critical"' }, { severity: '"Mixed <b>"' }, { notify_emails: 'null' }, { notify_emails: '""' },
        { notify_emails: '"a@x.example, b@x.example"' }, { description: 'null' }, { description: '"CPU is high"' }, { name: '"Ünï 😀 <b>&\\"q\\""' }, { name: '""' },
        ...(pg ? [] : [{ name: '5' }, { severity: '7' }, { notify_emails: '5' }, { description: '1.5' }, { condition_op: '5' }, { scope_device_id: '123' }])]) {
        add(Object.assign({ rules: [rule(over)] }, call('95', { mail: r.pick(['ok', 'rejects']) })));
    }
    // Rules in the order of their age, and a rule that fails in between
    {
        const list = [rule({ name: '"Oldest"' }), rule({ name: '"Middle"', condition_op: '"lt"' }), rule({ name: '"Newest"', condition_value: '10' })];
        add(Object.assign({ rules: list }, call('95')));
        const broken = [rule({ name: '"Oldest"' }), rule({ name: '"Broken"', condition_op: '"valueOf"' }), rule({ name: '"Newest"' })];
        add(Object.assign({ rules: broken }, call('95')));
        const always = [rule({ name: '"Oldest"', condition_op: '"constructor"' }), rule({ name: '"Never"', condition_op: '"isPrototypeOf"' }), rule({ name: '"Text"', condition_op: '"toString"' })];
        add(Object.assign({ rules: always }, call('1')));
    }
    // Device ids and metrics as a request can send them
    for (const device of ['"dev-1"', '123', 'true', '["dev-1"]', '{"a":1}', '""', '0', 'null', '"x\'y"', '"Ünï"', '[]', '["a","b"]', '{"toString":1}', '1.5', '1e21']) {
        const ru = rule({});
        add(Object.assign({ rules: [ru], history: [{ rule: ru.id, device: device === '123' ? '"123"' : '"dev-1"', ago: 10, form: 'iso' }] }, call('95', { device })));
        add(Object.assign({ rules: [rule({})] }, call('95', { device })));
    }
    for (const metric of VALUES) add(Object.assign({ rules: [rule({ condition_op: '"gte"', condition_value: pg ? '"-Infinity"' : '-1e999', notify_emails: '""' })] }, call(metric)));
    // Telemetry
    for (const [used, total] of [['1', '4'], ['1', '3'], ['2', '3'], ['4096', '4096'], ['"3"', '"4"'], ['5', '0'], ['0', '5'], ['1e308', '1'], ['125', '1000'], ['5', '1000'], ['995', '1000']]) {
        add({ rules: [rule({ condition_type: '"memory_usage"', condition_op: '"gte"', condition_value: '0', notify_emails: '""' }), rule({ condition_type: '"cpu_usage"', condition_value: '80' })],
            fn: 'telemetry', device: '"dev-1"', telemetry: { cpu_usage_percent: r.pick(['95', '20', '"85"', 'null']), memory_used_bytes: used, memory_total_bytes: total } });
    }
    // The sweep, with alerts raised a while ago
    const now = Date.parse('2026-11-09T12:00:00Z');
    const devices = J([
        { id: 'off-2h', online: false, last_online: '2026-11-09T10:00:00Z' },
        { id: 'off-1m', online: false, last_online: '2026-11-09T11:59:00Z' },
        { id: 'on', online: true, last_online: '2026-11-09T09:00:00Z' },
        { id: 'banned', online: false, banned: true, last_online: '2026-11-01T00:00:00Z' },
        { id: 'gone', online: false, soft_deleted: true, last_online: '2026-11-01T00:00:00Z' },
        { id: 'never', online: false, last_online: '' },
        { id: 'zero', online: false, last_online: '0001-01-01T00:00:00Z' },
        { id: 'target', online: false, remote_target: true, last_online: '2026-11-09T10:00:00Z' },
    ]);
    for (const limit of ['3600', '30', '1e12']) {
        for (const cooldown of ['300', 'null', '86400']) {
            for (const ago of [null, 60, 3000, 7000, 7400, 90000]) {
                const ru = rule({ condition_type: '"offline_duration"', condition_value: limit, cooldown_secs: cooldown });
                const history = ago === null ? [] : [{ rule: ru.id, device: '"off-2h"', ago, form: 'iso' }, { rule: ru.id, device: '"off-1m"', ago, form: 'space' }];
                add({ rules: [ru], history, fn: 'sweep', now, devices });
            }
        }
    }
    {
        const scoped = rule({ condition_type: '"offline_duration"', condition_value: '30', scope_device_id: '"off-1m"' });
        add({ rules: [scoped], fn: 'sweep', now, devices });
        add({ rules: [rule({ condition_type: '"cpu_usage"' })], fn: 'sweep', now, devices });
        add({ rules: [rule({ condition_type: '"offline_duration"', enabled: pg ? 'false' : '0' })], fn: 'sweep', now, devices });
        add({ rules: [rule({ condition_type: '"offline_duration"', condition_value: '30' })], fn: 'sweep', now, devices, devicesThrow: 'Go server unreachable' });
    }
    return cases;
}

const pad = (n) => String(n).padStart(2, '0');
function spaceForm(d) {
    return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`;
}

async function runDb(engine, db, cases) {
    const quiet = lib.captureConsole();
    const out = [];
    const columns = ['name', 'description', 'enabled', 'condition_type', 'condition_op', 'condition_value', 'severity', 'scope_device_id', 'cooldown_secs', 'notify_emails'];
    for (const c of cases) {
        const o = { ...c };
        await db.exec('DELETE FROM alert_history');
        await db.exec('DELETE FROM alert_rules');
        // Rules get older down the list, a second apart
        for (let i = 0; i < c.rules.length; i += 1) {
            const ru = c.rules[i];
            await db.exec(`INSERT INTO alert_rules (id, ${columns.join(', ')}, created_at) VALUES (?, ${columns.map(() => '?').join(', ')}, ?)`,
                [ru.id, ...columns.map((k) => value(ru.cols[k])), `2026-01-01 00:00:${pad(59 - i)}`]);
        }
        for (const h of c.history) {
            const at = new Date(Date.now() - h.ago * 1000);
            await db.exec('INSERT INTO alert_history (rule_id, device_id, severity, message, triggered_at) VALUES (?, ?, ?, ?, ?)',
                [h.rule, value(h.device), 'warning', 'seeded', h.form === 'iso' ? at.toISOString() : spaceForm(at)]);
        }
        o.stored = (await db.exec(`SELECT id, ${columns.join(', ')} FROM alert_rules ORDER BY id`)).map((row) => J([row.id, ...columns.map((k) => row[k])]));
        mails.length = 0;
        mailBehaviour.mode = c.mail;
        deviceList.calls = 0;
        quiet.take();
        try {
            if (c.fn === 'rules') {
                await engine.evaluateRules(value(c.device), c.type, value(c.metric));
            } else if (c.fn === 'telemetry') {
                await engine.evaluateTelemetry(value(c.device), object(c.telemetry));
            } else {
                deviceList.value = JSON.parse(c.devices);
                deviceList.throws = c.devicesThrow === undefined ? null : c.devicesThrow;
                await engine.periodicCheck({ now: c.now });
            }
        } catch (err) {
            o.throws = err.message;
        }
        await flush();
        o.logs = quiet.take();
        const rows = await db.exec("SELECT rule_id, device_id, severity, message, acknowledged, acknowledged_by, triggered_at FROM alert_history WHERE message <> 'seeded' ORDER BY id");
        o.rows = rows.map((row) => J([row.rule_id, row.device_id, row.severity, row.message, row.acknowledged, row.acknowledged_by]));
        o.triggered = rows.map((row) => (row.triggered_at instanceof Date ? 'date' : (ISO.test(row.triggered_at) ? 'iso' : dump(row.triggered_at))));
        o.mails = mails.map(mailRecord);
        o.listCalls = deviceList.calls;
        out.push(o);
    }
    quiet.restore();
    return out;
}

async function background(engine) {
    const quiet = lib.captureConsole();
    const out = [];
    const cases = {
        resolves: async () => 1,
        rejects: async () => {
            throw new Error('evaluation rejected');
        },
        throws: () => {
            throw new TypeError('evaluation threw');
        },
    };
    for (const [name, fn] of Object.entries(cases)) {
        let returned;
        try {
            returned = dump(engine.evaluateInBackground(fn));
        } catch (err) {
            returned = `throws ${err.message}`;
        }
        await flush();
        await flush();
        out.push({ name, returned, logs: quiet.take() });
    }
    quiet.restore();
    return out;
}

async function main() {
    const quiet = lib.captureConsole();
    const db = await lib.openDatabase();
    const engine = lib.web('services/alertRulesEngine');
    quiet.restore();

    const written = [];
    if (!pg) {
        const cond = condVectors(engine);
        written.push(lib.writeVectors('cond', cond, `${cond.length} conditions`));
        const mem = await runMem(engine, db.adapter, memCases());
        const bg = await background(engine);
        written.push(lib.writeVectors('mem', { cases: mem, background: bg }, `${mem.length} cases, ${bg.length} background runs`));
    }
    const rows = await runDb(engine, db, dbCases());
    written.push(lib.writeVectors(`db_${lib.dialect}`, { dialect: lib.dialect, cases: rows }, `${rows.length} cases`));
    await db.close();
    console.log(written.join('\n'));
    process.exit(0);
}

main().catch((err) => {
    process.stderr.write(`${err.stack}\n`);
    process.exit(1);
});
