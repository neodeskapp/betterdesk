/**
 * Shared WS upgrade router — prevents MaxListenersExceededWarning (#295).
 *
 * All panel WebSocket services must register via registerUpgradeHandler so the
 * HTTP server has exactly one 'upgrade' listener regardless of how many routes
 * are registered.
 */

'use strict';

const http = require('http');
const { EventEmitter } = require('events');

describe('wsUpgradeRouter', () => {
    beforeEach(() => {
        jest.resetModules();
    });

    test('attaches exactly one upgrade listener for many route registrations', () => {
        const { registerUpgradeHandler, registeredRouteCount } = require('../services/wsUpgradeRouter');
        const server = new EventEmitter();

        for (let i = 0; i < 11; i += 1) {
            const path = `/ws/route-${i}`;
            registerUpgradeHandler(server, (pathname) => pathname === path, () => {});
        }

        expect(server.listenerCount('upgrade')).toBe(1);
        expect(registeredRouteCount(server)).toBe(11);
    });

    test('dispatches to the matching route only', () => {
        const { registerUpgradeHandler } = require('../services/wsUpgradeRouter');
        const server = new EventEmitter();
        const hits = [];

        registerUpgradeHandler(server, (p) => p === '/ws/a', () => { hits.push('a'); });
        registerUpgradeHandler(server, (p) => p === '/ws/b', () => { hits.push('b'); });

        const socket = { write: jest.fn(), destroy: jest.fn() };
        server.emit('upgrade', { url: '/ws/b', headers: { host: 'localhost' } }, socket, Buffer.alloc(0));

        expect(hits).toEqual(['b']);
        expect(socket.write).not.toHaveBeenCalled();
    });

    test('leaves socket alone when no route matches', () => {
        const { registerUpgradeHandler } = require('../services/wsUpgradeRouter');
        const server = new EventEmitter();
        registerUpgradeHandler(server, (p) => p === '/ws/owned', () => {});

        const socket = { write: jest.fn(), destroy: jest.fn() };
        server.emit('upgrade', { url: '/ws/other', headers: { host: 'localhost' } }, socket, Buffer.alloc(0));

        expect(socket.write).not.toHaveBeenCalled();
        expect(socket.destroy).not.toHaveBeenCalled();
    });
});
