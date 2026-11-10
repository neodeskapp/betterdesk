/**
 * The sign-in page's server side (routes/auth.routes.js):
 * GET /api/auth/login-page, what the new sign-in page shows before anyone
 * is signed in (no user data, so no list of accounts either: S4).
 * The way back after sign-in (R3) is answered by the Go console and tested
 * there (console/routes/auth).
 */

const request = require('supertest');
const { createTestApp } = require('./helpers');

jest.mock('../services/database', () => ({
    logAction: jest.fn().mockResolvedValue(undefined),
    getUserById: jest.fn().mockResolvedValue(null),
    getUserByUsername: jest.fn().mockResolvedValue(null),
    updateLastLogin: jest.fn().mockResolvedValue(undefined),
    getAllUsersForBackup: jest.fn().mockResolvedValue([
        { username: 'alice-the-admin', role: 'super_admin', password_hash: 'x' },
        { username: 'bob-operator', role: 'operator', password_hash: 'y' }
    ])
}));

jest.mock('../services/betterdeskApi', () => ({
    exchangeOIDCCode: jest.fn()
}));

jest.mock('../services/panelSessionService', () => ({
    clientMetaFromRequest: jest.fn(() => ({ ip: '127.0.0.1', userAgent: 'jest', clientFingerprint: '' })),
    establishAuthenticatedSession: jest.fn(async (req, user) => {
        req.session.userId = user.id;
        req.session.user = user;
        return { replaced: [] };
    }),
    getPolicy: jest.fn(() => ({ idleTimeoutMs: 1800000, maxAgeMs: 43200000, warnBeforeMs: 60000 })),
    endCurrentSession: jest.fn().mockResolvedValue(undefined)
}));

const authRoutes = require('../routes/auth.routes');

function app(user) {
    const a = createTestApp();
    a.use((req, res, next) => {
        req.cookies = {};
        res.locals.csrfToken = 'csrf-token-for-test';
        if (user) { req.session.userId = user.id; req.session.user = user; }
        next();
    });
    a.use('/', authRoutes);
    return a;
}

describe('GET /api/auth/login-page', () => {
    test('answers what the sign-in page shows, without a session', async () => {
        const res = await request(app(null)).get('/api/auth/login-page');
        expect(res.status).toBe(200);
        expect(res.headers['cache-control']).toBe('no-store');
        expect(res.body.success).toBe(true);
        expect(res.body.csrfToken).toBe('csrf-token-for-test');
        expect(typeof res.body.appName).toBe('string');
        expect(Array.isArray(res.body.languages)).toBe(true);
        expect(res.body.branding).toEqual(expect.objectContaining({
            loginPromoMode: expect.any(String),
            loginSlides: expect.any(Array),
            showPoweredBy: expect.any(Boolean)
        }));
        expect(res.body.desktopMode).toBe(false);
        expect(res.body.signedIn).toBe(false);
        // Nothing about accounts
        expect(JSON.stringify(res.body)).not.toMatch(/alice-the-admin|bob-operator/);
    });

    test('the old desktop-mode cookie is reported so the page can use the compact layout', async () => {
        const a = createTestApp();
        a.use((req, res, next) => { req.cookies = { betterdesk_desktop_mode: 'true' }; next(); });
        a.use('/', authRoutes);
        const res = await request(a).get('/api/auth/login-page');
        expect(res.body.desktopMode).toBe(true);
    });
});

describe('GET /api/auth/login-page signed in', () => {
    test('says so (error pages keep the console shell), but not for a pro account', async () => {
        expect((await request(app({ id: 1, username: 'a', role: 'admin' })).get('/api/auth/login-page')).body.signedIn).toBe(true);
        expect((await request(app({ id: 9, username: 'p', role: 'pro' })).get('/api/auth/login-page')).body.signedIn).toBe(false);
    });
});

