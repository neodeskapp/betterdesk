'use strict';

// Answers questions about web-nodejs/lib/ssrfGuard.js over stdin and
// stdout, one JSON document per line, for the fuzz test. See README.md.
//
//   in:  {"fn":"httpUrl","input":"http://x/","allowPrivate":false,"records":["1.1.1.1"]}
//   out: {"ok":{"href":"http://x/","hostname":"x"},"lookups":["x"]}

const path = require('path');
const readline = require('readline');
const dns = require('dns');

const repo = path.resolve(__dirname, '../../../../..');
const guard = require(path.join(repo, 'web-nodejs/lib/ssrfGuard'));

let current = null;
let lookups = [];
dns.promises.lookup = async (host) => {
    lookups.push(host);
    if (current.error) throw new Error(current.error);
    return current.records.map((address) => ({ address, family: address.includes(':') ? 6 : 4 }));
};

async function answer(q) {
    current = q;
    lookups = [];
    const opts = { allowPrivate: !!q.allowPrivate };
    const a = {};
    try {
        switch (q.fn) {
            case 'isBlockedIp': a.ok = guard.isBlockedIp(q.input, opts); break;
            case 'hostname': a.ok = guard.assertSafeHostname(q.input, opts); break;
            case 'resolved': a.ok = await guard.assertSafeResolvedHost(q.input, opts); break;
            case 'httpUrl': {
                const u = await guard.assertSafeHttpUrl(q.input, opts);
                a.ok = { href: u.href, hostname: u.hostname };
                break;
            }
            default: a.err = 'unknown fn';
        }
    } catch (e) {
        a.err = e.message;
    }
    a.lookups = lookups;
    return a;
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
let chain = Promise.resolve();
rl.on('line', (line) => {
    chain = chain.then(async () => {
        const a = await answer(JSON.parse(line));
        process.stdout.write(JSON.stringify(a) + '\n');
    });
});
rl.on('close', () => chain.then(() => process.exit(0)));
