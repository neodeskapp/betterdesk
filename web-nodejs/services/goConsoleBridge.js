'use strict';

/**
 * The way back to the Go console while this console runs behind it.
 *
 * The agents' signal and relay sockets and the browsers' panel event
 * sockets live in the Go console. The routes that stay here and need them
 * (the device API's relay routes, the LAN registration routes) call the
 * Go console through this module. The Go console names itself on every
 * request it passes on, in the header X-BD-Bridge: its loopback address
 * and a token it made at its start. learn() keeps the latest one, from
 * requests that came over loopback only.
 */

const http = require('http');

let target = null;

const LOOPBACK = new Set(['127.0.0.1', '::1', '::ffff:127.0.0.1']);
const BRIDGE_URL = /^http:\/\/(127\.0\.0\.1|\[::1\]):\d{1,5}$/;

/** Express middleware: remember the Go console's address and token. */
function learn(req, _res, next) {
    const value = req.headers['x-bd-bridge'];
    if (typeof value === 'string' && LOOPBACK.has(req.socket && req.socket.remoteAddress)) {
        const [url, token] = value.split(' ');
        if (BRIDGE_URL.test(url) && /^[0-9a-f]{64}$/.test(token || '')) target = { url, token };
    }
    delete req.headers['x-bd-bridge'];
    next();
}

/** POST a JSON text to the Go console; resolves with the parsed answer. */
function call(path, text) {
    return new Promise((resolve, reject) => {
        if (!target) {
            reject(new Error('the Go console has not named itself'));
            return;
        }
        const body = Buffer.from(text);
        const url = new URL(target.url + path);
        const req = http.request({
            host: url.hostname.replace(/^\[|\]$/g, ''),
            port: url.port,
            path: url.pathname,
            method: 'POST',
            timeout: 5000,
            headers: {
                'Content-Type': 'application/json',
                'Content-Length': body.length,
                'X-BD-Bridge-Token': target.token,
            },
        }, (res) => {
            const chunks = [];
            res.on('data', (c) => chunks.push(c));
            res.on('end', () => {
                const answer = Buffer.concat(chunks).toString('utf8');
                if (res.statusCode === 204) return resolve(null);
                if (res.statusCode !== 200) return reject(new Error(`Go console answered ${res.statusCode}`));
                try {
                    resolve(JSON.parse(answer));
                } catch (err) {
                    reject(err);
                }
            });
        });
        req.on('timeout', () => req.destroy(new Error('Go console did not answer')));
        req.on('error', reject);
        req.end(body);
    });
}

/**
 * Publish an event to the browsers on /ws/panel-events, as the event
 * socket did for events this console made itself. The message goes out as
 * JSON.stringify writes it.
 */
function publishPanelEvent(data) {
    call('/__bd/node/panel-event', JSON.stringify(data)).catch((err) => {
        console.warn('[GoBridge] Panel event not published:', err.message);
    });
}

const relayCall = (name, ...args) => call('/__bd/node/relay', JSON.stringify({ call: name, args }));

/**
 * The agents' relay sessions and signal sockets in the Go console, with
 * the answers the old in-memory relay gave. Reads that fail answer as an
 * empty relay would.
 */
const relay = {
    createRelaySession: (initiatorId, targetId) => relayCall('create', initiatorId, targetId),
    getRelaySession: (sessionId) => relayCall('get', sessionId).catch(() => null),
    deleteSession: (sessionId) => relayCall('delete', sessionId).catch(() => false),
    notifyTarget: (targetId, payload) => relayCall('notify', targetId, payload).catch(() => false),
    getOnlineDeviceIds: () => relayCall('online').catch(() => []),
    // undefined for a device that never had a signal socket, as before
    isDeviceOnline: (deviceId) => relayCall('isOnline', deviceId).then((r) => r.online, () => undefined),
    pendingConnections: (targetId) => relayCall('pending', targetId).catch(() => []),
};

module.exports = { learn, publishPanelEvent, relay };
