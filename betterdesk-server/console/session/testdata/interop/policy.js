'use strict';

// Reads JSON from stdin: { "env": [ {SESSION_...: "..."}, ... ], "ip": [...],
// "userAgent": [...], "fingerprint": [...] } and prints what the Node
// console's lib/sessionPolicy.js makes of each entry.

const policy = require('/repo/web-nodejs/lib/sessionPolicy');

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
    const cases = JSON.parse(input);
    process.stdout.write(JSON.stringify({
        env: cases.env.map((env) => policy.resolveSessionPolicy(env)),
        ip: cases.ip.map((v) => policy.sanitizeIp(v)),
        userAgent: cases.userAgent.map((v) => policy.sanitizeUserAgent(v)),
        fingerprint: cases.fingerprint.map((v) => policy.sanitizeFingerprint(v)),
    }));
});
