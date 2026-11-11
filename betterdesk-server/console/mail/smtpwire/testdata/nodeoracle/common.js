'use strict';

// Shared by the recorders: where nodemailer is, the machine name the client
// announces, message bytes and the fields of an outcome.

const os = require('os');
const crypto = require('crypto');

const WEB = process.env.WEB_NODEJS || '/repo/web-nodejs';
const nodemailerPath = WEB + '/node_modules/nodemailer';

const realHostname = os.hostname;

// nodemailer asks os.hostname() for the EHLO name when it opens a connection
function setHostname(hostname) {
    if (!hostname) {
        os.hostname = realHostname;
    } else if (hostname.error) {
        os.hostname = () => {
            throw new Error('no hostname');
        };
    } else {
        os.hostname = () => hostname.value;
    }
}

// The bytes of a scenario message. {s} holds them as Latin-1 characters,
// {gen: {seed, size, alphabet}} makes them with a small generator the Go
// tests have too.
function messageBytes(message) {
    if (message.gen) {
        const { seed, size, alphabet } = message.gen;
        const table = Buffer.from(alphabet, 'latin1');
        const out = Buffer.alloc(size);
        let x = seed >>> 0;
        for (let i = 0; i < size; i++) {
            x = (Math.imul(x, 1103515245) + 12345) >>> 0;
            out[i] = table[(x >>> 16) % table.length];
        }
        return out;
    }
    return Buffer.from(message.s, 'latin1');
}

function errorFields(err) {
    if (!err) {
        return null;
    }
    const out = { message: err.message };
    for (const key of ['code', 'response', 'responseCode', 'command', 'recipient']) {
        if (err[key] !== undefined) {
            out[key] = err[key];
        }
    }
    if (err.rejected) {
        out.rejected = err.rejected;
    }
    if (err.rejectedErrors) {
        out.rejectedErrors = err.rejectedErrors.map(errorFields);
    }
    return out;
}

function infoFields(info) {
    const out = {
        accepted: info.accepted,
        rejected: info.rejected,
        response: info.response,
        envelope: info.envelope,
        messageSize: info.messageSize,
    };
    if (info.ehlo) {
        if (info.ehlo.length > 50) {
            out.ehloCount = info.ehlo.length;
            out.ehloSha256 = crypto.createHash('sha256').update(info.ehlo.join('\n'), 'utf8').digest('hex');
        } else {
            out.ehlo = info.ehlo;
        }
    }
    if (info.rejectedErrors) {
        out.rejectedErrors = info.rejectedErrors.map(errorFields);
    }
    if (info.serverMessageId !== undefined) {
        out.serverMessageId = info.serverMessageId;
    }
    return out;
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

async function pool(items, size, fn) {
    let next = 0;
    const workers = Array.from({ length: size }, async () => {
        while (next < items.length) {
            const item = items[next++];
            await fn(item);
        }
    });
    await Promise.all(workers);
}

module.exports = { WEB, nodemailerPath, setHostname, messageBytes, errorFields, infoFields, sleep, pool };
