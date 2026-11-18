'use strict';

// Vectors for package helpmail: services/helpRequestEmailService.js of
// the Node console. parseCommercializationEmailConfig with many stored
// values, and handleHelpRequestEvent with its neighbours stubbed the way
// tests/helpRequestEmailService.test.js stubs them: the database, the
// mail service and the folder lookups answer what each vector scripts,
// and every call and log line is recorded. Writes ../helpmail.json.

const path = require('path');
const fs = require('fs');
const Module = require('module');
const { consoleDir } = require(path.join(__dirname, '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle', 'lib'));

const world = {};
const load = Module._load;
Module._load = function (request, parent, isMain) {
    if (/\/database$/.test(request)) {
        return {
            getSetting: async (key) => { world.calls.push(['getSetting', key]); return world.stored; },
            hasEmailNotificationSent: async (type, id, to) => { world.calls.push(['has', type, id, to]); return world.sent.includes(to); },
            logEmailNotificationSent: async (type, id, to) => { world.calls.push(['log', type, id, to]); },
        };
    }
    if (/emailService$/.test(request)) {
        return {
            getAlertEmail: async () => { world.calls.push(['alert']); return world.alert; },
            sendHelpRequestEmail: async (args) => { world.calls.push(['send', args]); return !world.refuse.includes(args.to); },
        };
    }
    if (/deviceGroupService$/.test(request)) {
        return {
            resolveFolderNameForDevice: async (_db, id) => {
                world.calls.push(['folder', id]);
                if (world.folderThrows) throw new TypeError('(assignments || []) is not iterable');
                return world.folder;
            },
            resolveOperatorEmailsForDevice: async (_db, id) => { world.calls.push(['operators', id]); return world.operators; },
        };
    }
    return load.apply(this, arguments);
};
const service = require(path.join(consoleDir, 'services', 'helpRequestEmailService.js'));

const log = [];
for (const level of ['log', 'warn', 'error']) console[level] = (...args) => log.push([level, args.join(' ')]);

const stored = [null, '', '{}', 'null', 'true', '5', '"x"', '[]', '[false]', 'not json', '{"help_requests_enabled":false}',
    '{"help_requests_enabled":0}', '{"help_requests_enabled":"false"}', '{"help_requests_enabled":null}',
    '{"notify_assigned_operators":false,"fallback_alert_email":false}', '{"include_folder_in_subject":false,"other":1}',
    '{"help_requests_enabled":true,"notify_assigned_operators":true,"fallback_alert_email":true,"include_folder_in_subject":true}',
    '{"help_requests_enabled":false,"help_requests_enabled":true}', ' {"fallback_alert_email":false} '];
const configs = stored.map((s) => [s, service.parseCommercializationEmailConfig(s)]);

const events = [
    { id: '42', device_id: 'dev-1', hostname: 'PC-01', message: 'Need help' },
    { request_id: '43', device_id: 'dev-2', message: '  spaced  ' },
    { id: '', request_id: '44', device_id: ' dev-3 ', hostname: '' },
    { id: '45', device_id: '' }, { device_id: 'dev-4' }, { id: '46' }, {},
    { id: ' 47 ', device_id: 'dev-5', hostname: '  host  ', message: '<b>x</b>' },
    { id: '48', device_id: 'dev-6', status: 'acknowledged', handled_by: 'admin' },
];
const scenarios = [
    { name: 'operators', operators: [{ username: 'alice', email: 'alice@example.com' }], folder: 'Support' },
    { name: 'two operators and repeats', operators: [{ username: 'a', email: ' a@example.com ' }, { username: 'b', email: 'b@example.com' }, { username: 'c', email: 'a@example.com' }, { username: 'd', email: '' }], folder: '' },
    { name: 'fallback', operators: [], alert: 'ops@example.com', folder: 'Support' },
    { name: 'no recipients', operators: [], alert: '' },
    { name: 'already sent', operators: [{ username: 'alice', email: 'alice@example.com' }], sent: ['alice@example.com'] },
    { name: 'one refused', operators: [{ username: 'a', email: 'a@example.com' }, { username: 'b', email: 'b@example.com' }], refuse: ['a@example.com'] },
    { name: 'folder lookup fails', folderThrows: true, operators: [{ username: 'alice', email: 'alice@example.com' }] },
];
const runs = [];
(async () => {
    for (const s of stored) {
        for (const sc of scenarios) {
            for (const e of events) {
                Object.assign(world, { stored: s, operators: [], alert: '', folder: '', sent: [], refuse: [], folderThrows: false }, sc, { calls: [] });
                log.length = 0;
                let thrown = null;
                try {
                    await service.handleHelpRequestEvent(e);
                } catch (err) {
                    thrown = err.message;
                }
                runs.push({ stored: s, scenario: sc, event: e, thrown, calls: world.calls, log: [...log] });
            }
        }
    }
    fs.writeFileSync(path.join(__dirname, '..', 'helpmail.json'), JSON.stringify({ configs, runs }) + '\n');
    process.stdout.write(`helpmail.json: ${configs.length} configs, ${runs.length} events\n`);
})().catch((err) => {
    process.stderr.write(String(err.stack) + '\n');
    process.exit(1);
});
