'use strict';

/**
 * Answers the Go fuzz tests (fuzz_test.go) with what the old console's
 * services/wsRelay.js does, one line per question on stdin, one JSON line
 * per answer on stdout:
 *
 *   D <hex>                 decode a rendezvous payload for the guest check
 *   F <hex>,<hex>,...       feed chunks to the frame decoder
 *   C <hex of cookie header> <guest cookie name>
 *                           read the guest token from a Cookie header
 *
 * The cookie reader is the inline code of the upgrade handler, copied
 * letter for letter because the module does not export it.
 */

const path = require('path');
const readline = require('readline');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const stub = (rel, exports) => {
    const file = require.resolve(path.join(WEB, rel));
    require.cache[file] = { id: file, filename: file, loaded: true, exports };
};
stub('middleware/auth.js', { roleHasEffectivePermission: async () => false });
stub('services/betterdeskApi.js', { apiClient: {} });
console.log = () => {};
console.warn = () => {};

const wsRelay = require(path.join(WEB, 'services/wsRelay.js'));
const protobuf = require(require.resolve('protobufjs', { paths: [WEB] }));
const RV = protobuf.loadSync(path.join(WEB, 'protos/rendezvous.proto')).lookupType('hbb.RendezvousMessage');
const { createRelayFrameDecoder } = wsRelay._relayFraming;
const { decodeRendezvousPayload, guestTargetMatches } = wsRelay._guestProtocol;

function cookieToken(raw, GUEST_COOKIE) {
    let guestToken = '';
    try {
        const names = [GUEST_COOKIE, 'bd.guest', 'betterdesk.guest'];
        for (const cookieName of names) {
            const match = raw.split(';').map((p) => p.trim()).find((p) => p.startsWith(cookieName + '='));
            if (match) {
                guestToken = decodeURIComponent(match.slice(cookieName.length + 1) || '').trim();
                if (guestToken) break;
            }
        }
    } catch {
        guestToken = '';
    }
    return guestToken;
}

const out = (v) => process.stdout.write(`${JSON.stringify(v)}\n`);
readline.createInterface({ input: process.stdin }).on('line', (line) => {
    const kind = line[0];
    const rest = line.slice(2);
    if (kind === 'D') {
        const buf = Buffer.from(rest, 'hex');
        const res = {};
        try { RV.decode(buf); } catch (err) { res.error = err.message; }
        const m = decodeRendezvousPayload(buf);
        res.ok = m !== null;
        if (m) {
            res.punch = m.punchHoleRequest ? String(m.punchHoleRequest.id || '') : null;
            res.relay = m.requestRelay ? String(m.requestRelay.id || '') : null;
            res.matches = guestTargetMatches(m, 'TARGET01');
        }
        out(res);
    } else if (kind === 'F') {
        const d = createRelayFrameDecoder();
        const steps = [];
        for (const hex of rest.split(',')) {
            try {
                steps.push(d.feed(Buffer.from(hex, 'hex')).map((f) => f.toString('hex')));
            } catch (err) {
                steps.push({ error: err.message });
                break;
            }
        }
        out(steps);
    } else if (kind === 'C') {
        const [hex, name] = rest.split(' ');
        out(cookieToken(Buffer.from(hex, 'hex').toString('latin1'), name));
    }
});
