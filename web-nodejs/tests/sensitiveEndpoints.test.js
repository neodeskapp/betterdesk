/**
 * Endpoints that used to need only a session (BUGS.md S2):
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

const settingsRoutes = require('../routes/settings.routes');

function app(role) {
    const a = createTestApp();
    a.use((req, res, next) => {
        req.session.userId = 5;
        req.session.user = { id: 5, username: 'u', role };
        next();
    });
    a.use(settingsRoutes);
    return a;
}

describe('settings audit log', () => {
    test('needs audit.view', async () => {
        expect((await request(app('guest_issuer')).get('/api/settings/audit')).status).toBe(403);
        const ok = await request(app('viewer')).get('/api/settings/audit');
        expect(ok.status).toBe(200);
        expect(ok.body.data).toHaveLength(1);
    });
});
