'use strict';

/**
 * A guest link on the guest page: its data (GET /api/remote/guest-info,
 * which the new console's /remote/guest asks for) answers for the link's
 * token, with or without a console session, and keeps the token in the
 * guest cookie. Making a link is a route of the Go console.
 */

const express = require('express');
const request = require('supertest');

jest.mock('../services/betterdeskApi', () => ({
    apiClient: Object.assign(jest.fn(), {
        get: jest.fn(),
        post: jest.fn(),
    }),
}));
jest.mock('../services/database', () => ({
    getDevice: jest.fn().mockResolvedValue(null),
}));
jest.mock('../services/keyService', () => ({
    resolvePublicKey: jest.fn().mockResolvedValue(''),
}));
jest.mock('../services/serverConnectionConfigService', () => ({
    getConnectionMode: jest.fn().mockResolvedValue({ mode: 'p2p_first', p2p_fallback_ms: 2000 }),
}));

const betterdeskApi = require('../services/betterdeskApi');
const remoteRoutes = require('../routes/remote.routes');

function buildApp({ signedIn = true } = {}) {
    const app = express();
    app.use((req, res, next) => {
        req.session = signedIn
            ? { userId: 7, user: { username: 'issuer', role: 'guest_issuer' } }
            : {};
        req.t = (_key, fallback) => fallback || _key;
        next();
    });
    app.use(express.json());
    app.use(remoteRoutes);
    return app;
}

function validLink(devices) {
    betterdeskApi.apiClient.get.mockResolvedValueOnce({
        status: 200,
        data: {
            valid: true,
            view_only: true,
            expires_at: new Date(Date.now() + 60000).toISOString(),
            devices,
        },
    });
}

describe('Guest Access Link integration', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('answers a valid token with its devices and keeps it in the guest cookie', async () => {
        validLink([{ id: 'TARGET01', online: true }]);

        const response = await request(buildApp()).get('/api/remote/guest-info?t=guest-token');

        expect(response.status).toBe(200);
        expect(response.body.success).toBe(true);
        expect(response.body.guest).toMatchObject({ view_only: true, devices: [{ id: 'TARGET01' }] });
        expect(response.headers['set-cookie'][0]).toContain('guest-token');
        expect(betterdeskApi.apiClient.get).toHaveBeenCalledWith(
            '/guest/access-links/peers',
            { params: { token: 'guest-token' } }
        );
    });

    test('answers a visitor without a console session', async () => {
        validLink([{ id: 'TARGET01', online: false }]);

        const response = await request(buildApp({ signedIn: false })).get('/api/remote/guest-info?t=guest-token');

        expect(response.status).toBe(200);
        expect(response.body.guest.devices).toEqual([{ id: 'TARGET01', online: false }]);
    });

    test('answers 403 with the reason for an expired or invalid token', async () => {
        betterdeskApi.apiClient.get.mockResolvedValueOnce({
            status: 403,
            data: { valid: false, error: 'guest link expired' },
        });

        const response = await request(buildApp()).get('/api/remote/guest-info?t=expired-token');

        expect(response.status).toBe(403);
        expect(response.body).toMatchObject({ success: false, code: 'invalid', error: 'guest link expired' });
        expect(response.headers['set-cookie']).toBeUndefined();
    });
});
