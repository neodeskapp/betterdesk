'use strict';

/**
 * Records two things the mail service leans on, into ../misc.json.gz:
 *
 *   upper  String.prototype.toUpperCase for every code point it changes
 *          (the alert mail's subject has the severity in upper case)
 *   lossy  the text Node makes of bytes that are no UTF-8
 *   flags  what config/config.js makes of SMTP_TLS_VERIFY,
 *          HTTP_REDIRECT_HTTPS, HOST, PORT and ALLOW_SELF_SIGNED_CERTS:
 *          the module is loaded anew for each set of values
 */

const path = require('path');
const lib = require('./lib');

const upper = [];
for (let cp = 0; cp <= 0x10FFFF; cp += 1) {
    if (cp >= 0xD800 && cp <= 0xDFFF) continue;
    const s = String.fromCodePoint(cp);
    const u = s.toUpperCase();
    if (u !== s) upper.push([cp, u]);
}
const words = ['warning', 'critical', 'info', 'straße', 'ŉ', 'ǆ ǅ Ǆ', 'ﬁ ﬂ ﬃ', 'i̇stanbul', 'ıi', 'ΐ ΰ', 'և', 'ᾳ ᾼ ῼ', 'σς', 'ǰ', 'ẖ ẗ ẘ ẙ ẚ', 'ß\u0307',
    'mIxEd 123 <b>', '', 'ａｂｃ', '𐐨𐑏', 'ǈ', 'ﬓ', 'ά', 'ⓐ', 'ʼn'];
const upperWords = words.map((w) => [w, w.toUpperCase()]);

const configFile = require.resolve(path.join(lib.WEB, 'config/config.js'));
const quiet = lib.captureConsole();
const VALUES = [undefined, '', 'true', 'TRUE', 'True', 'tRuE', ' true', 'true ', 'true\n', '1', 'yes', 'on', 'false', 'FALSE', '0', 'truee', 'tru', 'trux', 'fals', '0000', 'ｔｒｕｅ', 'TRÜE',
    'İ', 'TRUE\u0307'];
const HOSTS = [undefined, '', '0.0.0.0', '127.0.0.1', 'example.org', ' 0.0.0.0 ', '::'];
const PORTS = [undefined, '', '5000', '80', '0', '-1', 'abc', '8080abc', ' 9000', '0x50', '1e3', '65536', '99999999999999999999999'];
const flags = [];
function record(env) {
    lib.setEnv(env);
    delete require.cache[configFile];
    const config = require(configFile);
    flags.push({ env, smtpTlsVerify: config.smtpTlsVerify, httpRedirect: config.httpRedirect, allowSelfSignedCerts: config.allowSelfSignedCerts,
        host: config.host, port: lib.dump(config.port) });
}
const clean = (env) => Object.fromEntries(Object.entries(env).filter(([, v]) => v !== undefined));
for (const v of VALUES) {
    record(clean({ SMTP_TLS_VERIFY: v }));
    record(clean({ HTTP_REDIRECT_HTTPS: v }));
    for (const self of ['true', 'false', undefined]) record(clean({ SMTP_TLS_VERIFY: v, ALLOW_SELF_SIGNED_CERTS: self }));
}
for (const host of HOSTS) for (const port of PORTS) record(clean({ HOST: host, PORT: port }));
quiet.restore();

// Bytes that are no UTF-8, as a database row or a request body can hold
// them, and the text Node makes of them
const r = lib.rng(8128);
const lossy = [];
const pieces = [[0x41], [0x22], [0xC3, 0xA4], [0xE2, 0x82, 0xAC], [0xF0, 0x9F, 0x98, 0x80], [0xED, 0xA0, 0x80], [0xED, 0xBF, 0xBF], [0xED, 0x9F, 0xBF], [0xC0, 0xAF],
    [0xC1, 0xBF], [0xE0, 0x80, 0x80], [0xE0, 0xA0], [0xE2, 0x82], [0xE2], [0xF0, 0x9F, 0x98], [0xF0, 0x9F], [0xF0, 0x80, 0x80, 0x80], [0xF4, 0x8F, 0xBF, 0xBF],
    [0xF4, 0x90, 0x80, 0x80], [0xF5, 0x80, 0x80, 0x80], [0xFF], [0xFE], [0x80], [0xBF], [0xC2], [0xEF, 0xBF, 0xBD], [0xEF, 0xBB, 0xBF], [0xF8, 0x88, 0x80, 0x80, 0x80]];
for (let i = 0; i < 3000; i += 1) {
    const bytes = [];
    const n = 1 + r.int(6);
    for (let k = 0; k < n; k += 1) bytes.push(...(r.chance(0.8) ? r.pick(pieces) : [r.int(256)]));
    const buf = Buffer.from(bytes);
    lossy.push([buf.toString('hex'), buf.toString('utf8')]);
}

const vectors = { upper, upperWords, flags, lossy };
console.log(lib.writeVectors('misc', vectors, `${upper.length} code points, ${upperWords.length} words, ${flags.length} sets of flags, ${lossy.length} byte strings`));
process.exit(0);
