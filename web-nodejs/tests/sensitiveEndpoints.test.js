/**
 * Endpoints that used to need only a session (BUGS.md S2):
 * - GET /api/keys/api?show=true returned the full API key, which has the
 *   admin role on the Go server, to any signed-in account. Now it needs
 *   server.keys; the masked key stays readable.
 * - GET /api/settings/audit needs audit.view, like the audit pages.
 */

const request = require('supertest');
const { createTestApp } = require('./helpers');

jest.mock('../services/keyService', () => ({
    getApiKey: jest.fn((masked) => (masked ? 'abcd****' : 'abcd-full-key'))
}));
jest.mock('../services/database', () => ({
    getAuditLogs: jest.fn().mockResolvedValue([{ id: 1, action: 'x' }]),
    logAction: jest.fn().mockResolvedValue(undefined)
}));
jest.mock('../middleware/rateLimiter', () => ({
    apiLimiter: (_req, _res, next) => next()
}));
jest.mock('../services/betterdeskApi', () => ({
    apiClient: {}
}));

const keysRoutes = require('../routes/keys.routes');
const settingsRoutes = require('../routes/settings.routes');

function app(role) {
    const a = createTestApp();
    a.use((req, res, next) => {
        req.session.userId = 5;
        req.session.user = { id: 5, username: 'u', role };
        next();
    });
    a.use(keysRoutes);
    a.use(settingsRoutes);
    return a;
}

describe('full API key', () => {
    test.each(['viewer', 'operator', 'global_admin', 'guest_issuer'])('%s gets 403 for the full key', async (role) => {
        const res = await request(app(role)).get('/api/keys/api?show=true');
        expect(res.status).toBe(403);
        expect(JSON.stringify(res.body)).not.toContain('abcd-full-key');
    });

    test.each(['server_admin', 'super_admin', 'admin'])('%s gets the full key', async (role) => {
        const res = await request(app(role)).get('/api/keys/api?show=true');
        expect(res.status).toBe(200);
        expect(res.body.data).toEqual({ key: 'abcd-full-key', masked: false });
    });

    test('the masked key stays readable for any signed-in account', async () => {
        const res = await request(app('viewer')).get('/api/keys/api');
        expect(res.status).toBe(200);
        expect(res.body.data).toEqual({ key: 'abcd****', masked: true });
    });
});

describe('settings audit log', () => {
    test('needs audit.view', async () => {
        expect((await request(app('guest_issuer')).get('/api/settings/audit')).status).toBe(403);
        const ok = await request(app('viewer')).get('/api/settings/audit');
        expect(ok.status).toBe(200);
        expect(ok.body.data).toHaveLength(1);
    });
});
