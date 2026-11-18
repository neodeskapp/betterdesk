'use strict';

// Writes vectors of upstream's signed update manifest module for the Go
// tests (see README.md). Usage:
//   node manifest-signature.js <updateManifestSignature.js> <output.json>

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const modulePath = path.resolve(process.argv[2]);
const outPath = path.resolve(process.argv[3]);
const sig = require(modulePath);

// A deterministic stream of numbers, so the vectors are the same on every run.
let state = 0x2545f491;
function rand() {
    state ^= state << 13; state >>>= 0;
    state ^= state >>> 17;
    state ^= state << 5; state >>>= 0;
    return state / 0x100000000;
}
function pick(list) { return list[Math.floor(rand() * list.length)]; }
function int(n) { return Math.floor(rand() * n); }

const PKCS8_PREFIX = Buffer.from('302e020100300506032b657004220420', 'hex');
function keyFromSeed(text) {
    const seed = crypto.createHash('sha256').update(text).digest();
    const privateKey = crypto.createPrivateKey({ key: Buffer.concat([PKCS8_PREFIX, seed]), format: 'der', type: 'pkcs8' });
    const raw = crypto.createPublicKey(privateKey).export({ format: 'der', type: 'spki' }).subarray(12);
    return { seed: seed.toString('hex'), privateKey, publicB64: raw.toString('base64') };
}
const key = keyFromSeed('betterdesk-updater-test-key');
const foreign = keyFromSeed('betterdesk-updater-foreign-key');

const pieces = ['a', 'b', 'Z', '0', '9', '-', '_', '.', '/', '\\', ' ', '=', 'é', 'Ä', 'Ж', '中',
    '😀', '～', '', 'betterdesk-server', 'web-nodejs', 'linux', '\n', '\t'];
function text(max) {
    let s = '';
    const n = int(max + 1);
    for (let i = 0; i < n; i++) s += pick(pieces);
    return s;
}
const hexChars = '0123456789abcdefABCDEF';
function hex(n) {
    let s = '';
    for (let i = 0; i < n; i++) s += hexChars[int(hexChars.length)];
    return s;
}
function sha256Field() {
    switch (int(6)) {
    case 0: return '';
    case 1: return text(8);
    case 2: return hex(64).toLowerCase();
    default: return hex(64);
    }
}
function pathField() {
    switch (int(8)) {
    case 0: return '';
    case 1: return `betterdesk-server-${pick(['linux', 'windows', 'darwin'])}-${pick(['amd64', 'arm64', 'armv7'])}${pick(['', '.exe'])}`;
    case 2: return `web-nodejs\\${text(4)}`;
    default: return text(10);
    }
}
function manifest() {
    const files = [];
    const n = int(7);
    for (let i = 0; i < n; i++) {
        files.push({ path: pathField(), sha256: sha256Field() });
    }
    if (files.length > 1 && rand() < 0.3) {
        const copy = files[int(files.length)];
        files.push({ path: copy.path, sha256: sha256Field() });
    }
    return {
        owner: rand() < 0.5 ? 'UNITRONIX' : text(6),
        repo: rand() < 0.5 ? 'BetterDesk' : text(6),
        branch: pick(['main', 'dev', '', text(4)]),
        sha: rand() < 0.5 ? hex(40).toLowerCase() : text(8),
        files,
    };
}

function sign(m, k) { return crypto.sign(null, sig.canonicalPayload(m), k.privateKey).toString('base64'); }

const manifests = [];
for (let i = 0; i < 1000; i++) {
    const m = manifest();
    manifests.push({
        manifest: m,
        payload: sig.canonicalPayload(m).toString('base64'),
        signature: sign(m, key),
    });
}

// How upstream's verifier answers signatures and keys of many forms.
function variants(good) {
    const out = [good, good.replace(/=+$/, ''), good.replace(/\+/g, '-').replace(/\//g, '_'),
        ` ${good}\n`, good.slice(0, 40) + '\n' + good.slice(40), good.slice(0, 10) + '!*' + good.slice(10),
        good.slice(0, -4), good + 'AAAA', '=' + good, good.slice(0, 20) + '=' + good.slice(20), '', 'not-base64!!',
        Buffer.alloc(4).toString('base64'), Buffer.alloc(64).toString('base64')];
    return out;
}
const verify = [];
for (let i = 0; i < 100; i++) {
    const entry = manifests[i];
    const other = manifests[(i + 1) % manifests.length];
    const keyTexts = [key.publicB64, foreign.publicB64, key.publicB64.replace(/=+$/, ''),
        key.publicB64.slice(0, 20), Buffer.alloc(16).toString('base64')];
    const sigs = [...variants(entry.signature), other.signature, sign(entry.manifest, foreign)];
    for (const s of sigs) {
        for (const k of keyTexts) {
            if (rand() < 0.5 && k !== key.publicB64) continue;
            verify.push({
                index: i,
                signature: s,
                key: k,
                ok: sig.verifyManifestSignature(entry.manifest, s, Buffer.from(k, 'base64')),
            });
        }
    }
}

// evaluateUpdateTrust for every combination of configuration and input.
const trust = [];
const sample = manifests[0];
for (const keySetting of ['', key.publicB64, foreign.publicB64, Buffer.alloc(16).toString('base64')]) {
    for (const required of ['', 'true', '1', 'yes', 'YES', ' true ', 'no', 'false']) {
        for (const signature of [null, sample.signature, sign(sample.manifest, foreign), 'garbage']) {
            for (const withManifest of [true, false]) {
                if (keySetting) process.env[sig.ENV_PUBLIC_KEY] = keySetting; else delete process.env[sig.ENV_PUBLIC_KEY];
                if (required) process.env[sig.ENV_REQUIRE] = required; else delete process.env[sig.ENV_REQUIRE];
                const result = sig.evaluateUpdateTrust({ manifest: withManifest ? sample.manifest : null, signature });
                trust.push({ key: keySetting, required, signature, withManifest, result });
            }
        }
    }
}

fs.writeFileSync(outPath, JSON.stringify({
    source: 'web-nodejs/lib/updateManifestSignature.js of upstream dev',
    seed: key.seed,
    publicKey: key.publicB64,
    foreignSeed: foreign.seed,
    manifests,
    verify,
    trust,
}) + '\n');
console.log(`wrote ${manifests.length} manifests, ${verify.length} verifications, ${trust.length} trust cases`);
