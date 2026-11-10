'use strict';

// What the scripts of this folder share: the names the lookups know, and
// the lookups themselves, put in place of Node's before the console's
// modules run.

const dns = require('dns');
const net = require('net');

// Everything else is not found, except addresses and the spellings of an
// IPv4 address the system resolver reads by itself.
const table = {
    'example.com': ['93.184.216.34'],
    'internal.corp': ['10.1.2.3'],
    'loop.test': ['127.0.0.1'],
    'mixed.test': ['8.8.8.8', '127.0.0.1'],
    'v6loop.test': ['::1'],
    'v6.test': ['2001:db8::1'],
    'dual.test': ['2001:db8::1', '192.0.2.7'],
    'linklocal.test': ['169.254.169.254'],
    'v6linklocal.test': ['fe80::1'],
    'empty.test': [],
    'fail.test': { error: 'ENOTFOUND' },
    'again.test': { error: 'EAI_AGAIN' },
    'zero.test': ['0.0.0.0'],
    'ula.test': ['fd00::1'],
    'cgnat.test': ['100.64.0.1'],
    'xn--mnchen-3ya.de': ['192.0.2.1'],
    'example.com.': ['93.184.216.34'],
    'a-b.c-d.example': ['192.0.2.9'],
};

const realLookup = dns.promises.lookup.bind(dns.promises);
const numericLooking = /^[0-9a-fA-FxX.]+$/;

function lookupError(code, host) {
    const err = new Error(`getaddrinfo ${code} ${host}`);
    err.code = code;
    return err;
}

async function lookupRecords(host) {
    if (Object.prototype.hasOwnProperty.call(table, host)) {
        const entry = table[host];
        if (!Array.isArray(entry)) throw lookupError(entry.error, host);
        return entry.map((address) => ({ address, family: address.includes(':') ? 6 : 4 }));
    }
    if (net.isIP(host)) return [{ address: host, family: net.isIP(host) }];
    if (numericLooking.test(host)) {
        try {
            return await realLookup(host, { all: true, verbatim: true });
        } catch (_) {
            throw lookupError('ENOTFOUND', host);
        }
    }
    throw lookupError('ENOTFOUND', host);
}

// lookups lists the names the console's code asked for.
const lookups = [];

function install() {
    dns.promises.lookup = async (host, opts) => {
        lookups.push(host);
        const records = await lookupRecords(host);
        if (opts && opts.all) return records;
        if (!records.length) throw lookupError('ENOTFOUND', host);
        return records[0];
    };
    dns.lookup = (host, opts, cb) => {
        if (typeof opts === 'function') { cb = opts; opts = {}; }
        lookups.push(host);
        lookupRecords(host).then((records) => {
            if (opts && opts.all) return cb(null, records);
            if (!records.length) return cb(lookupError('ENOTFOUND', host));
            cb(null, records[0].address, records[0].family);
        }, cb);
    };
}

module.exports = { table, lookups, install };
