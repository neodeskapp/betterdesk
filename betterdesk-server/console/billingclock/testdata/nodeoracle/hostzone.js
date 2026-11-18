'use strict';

// The machine's time zone as the Node console saw it: for each value of
// TZ (or none) and each target of the /etc/localtime link, a fresh node
// process reports Intl.DateTimeFormat().resolvedOptions().timeZone and
// what detectOsTimezone() of services/billingClockConfigService.js made
// of it. Writes ../hostzone.json.
//
// It changes /etc/localtime, so it runs as root in a throwaway container
// (tools/dev.sh) and puts the link back at the end.

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const { consoleDir } = require(path.join(__dirname, '..', '..', '..', 'goapipath', 'testdata', 'nodeoracle', 'lib'));

const service = path.join(consoleDir, 'services', 'billingClockConfigService.js');
// The child loads the real service with its Node-only neighbours stubbed
const child = `
const Module = require('module');
const load = Module._load;
Module._load = function (request, parent, isMain) {
    if (/config\\/config$/.test(request)) return { isDocker: false };
    if (/updateService$|serverConnectionConfigService$/.test(request)) return {};
    if (/betterdeskApi$/.test(request)) return { apiClient: {} };
    return load.apply(this, arguments);
};
const s = require(${JSON.stringify(service)});
process.stdout.write(JSON.stringify({ resolved: Intl.DateTimeFormat().resolvedOptions().timeZone, detected: s.detectOsTimezone() }));
`;

const tzValues = [null, '', 'UTC', 'Europe/Berlin', ':Europe/Berlin', 'posix/Europe/Berlin', 'right/UTC', ':posix/Asia/Tokyo',
    'europe/berlin', 'EUROPE/BERLIN', 'Asia/Kolkata', 'Asia/Calcutta', 'US/Eastern', 'EST', 'IST', 'CET', 'GMT', 'Etc/UTC', 'Etc/GMT+5',
    'EST5EDT', 'PST8PDT', 'CET-1CEST', 'CET-1CEST,M3.5.0,M10.5.0/3', 'Mars/Base', ' Europe/Berlin', 'Europe/Berlin ', '+01:00', 'UTC0',
    'Factory', 'localtime', 'Europe/Kyiv', 'America/Argentina/Buenos_Aires', 'Zulu', 'Universal', 'SystemV/AST4', 'ACT', 'Etc/Unknown',
    'Antarctica/Troll', 'Pacific/Chatham', '<+0330>-3:30'];
const links = ['/usr/share/zoneinfo/Etc/UTC', '/usr/share/zoneinfo/Europe/Berlin', '/usr/share/zoneinfo/posix/Asia/Tokyo',
    '/usr/share/zoneinfo/right/America/New_York', '/usr/share/zoneinfo/Asia/Calcutta', '/usr/share/zoneinfo/US/Pacific',
    '/usr/share/zoneinfo/EST5EDT', '/usr/share/zoneinfo/Factory', null];

const original = fs.readlinkSync('/etc/localtime');
const vectors = [];
try {
    for (const link of links) {
        fs.rmSync('/etc/localtime', { force: true });
        if (link) fs.symlinkSync(link, '/etc/localtime');
        else fs.copyFileSync('/usr/share/zoneinfo/Asia/Tokyo', '/etc/localtime');
        for (const tz of tzValues) {
            const env = { ...process.env };
            delete env.TZ;
            if (tz !== null) env.TZ = tz;
            const out = JSON.parse(execFileSync(process.execPath, ['-e', child], { env }).toString());
            vectors.push({ tz, link, ...out });
        }
    }
} finally {
    fs.rmSync('/etc/localtime', { force: true });
    fs.symlinkSync(original, '/etc/localtime');
}
fs.writeFileSync(path.join(__dirname, '..', 'hostzone.json'), JSON.stringify(vectors, null, 0) + '\n');
console.log(`hostzone.json: ${vectors.length} vectors`);
