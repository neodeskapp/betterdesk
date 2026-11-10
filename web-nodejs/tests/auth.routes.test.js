/**
 * BetterDesk Console - Auth Routes Tests
 *
 * The route file holds the sign-in page's data (tests/auth.loginPage.test.js)
 * and the last hop of a single sign-on. The other sign-in routes are
 * answered by the Go console and tested there (console/routes/auth).
 */

const request = require('supertest');
const { createTestApp } = require('./helpers');

// Mock dependencies before requiring routes
jest.mock('../services/database', () => ({
    logAction: jest.fn().mockResolvedValue(undefined),
    getUserByUsername: jest.fn().mockResolvedValue(null),
    createUser: jest.fn().mockResolvedValue(undefined),
    syncUserFromGo: jest.fn().mockResolvedValue(undefined),
    updateLastLogin: jest.fn().mockResolvedValue(undefined)
}));

jest.mock('../services/betterdeskApi', () => ({
    exchangeOIDCCode: jest.fn()
}));

const db = require('../services/database');
const betterdeskApi = require('../services/betterdeskApi');
const authRoutes = require('../routes/auth.routes');

describe('Auth Routes', () => {
    let app;

    beforeEach(() => {
        app = createTestApp();
        app.use('/', authRoutes);
        // What the session holds after the sign-on, for the test to look at
        app.get('/__test/session-user', (req, res) => res.json({ userId: req.session.userId, user: req.session.user }));
        jest.clearAllMocks();
    });

    describe('GET /api/auth/oidc/session', () => {
        it('redirects to oidc_invalid when code is missing', async () => {
            const res = await request(app).get('/api/auth/oidc/session');

            expect(res.status).toBe(302);
            expect(res.headers.location).toBe('/login?error=oidc_invalid');
            expect(betterdeskApi.exchangeOIDCCode).not.toHaveBeenCalled();
        });

        it('redirects to oidc_invalid when Go exchange fails', async () => {
            betterdeskApi.exchangeOIDCCode.mockResolvedValue({ success: false, error: 'invalid or expired code' });

            const res = await request(app).get('/api/auth/oidc/session?code=bad-code');

            expect(res.status).toBe(302);
            expect(res.headers.location).toBe('/login?error=oidc_invalid');
            expect(betterdeskApi.exchangeOIDCCode).toHaveBeenCalledWith('bad-code');
        });

        it('creates an OIDC session and redirects to the safe return URL', async () => {
            betterdeskApi.exchangeOIDCCode.mockResolvedValue({
                success: true,
                data: {
                    token: 'go-jwt-token',
                    username: 'sso-user',
                    role: 'operator',
                    return_url: '/dashboard',
                },
            });
            db.getUserByUsername.mockResolvedValue({
                id: 42,
                username: 'sso-user',
                role: 'viewer',
                preferred_language: null,
            });

            const agent = request.agent(app);
            const res = await agent.get('/api/auth/oidc/session?code=valid-code');

            expect(res.status).toBe(302);
            expect(res.headers.location).toBe('/dashboard');
            expect(betterdeskApi.exchangeOIDCCode).toHaveBeenCalledWith('valid-code');
            expect(db.syncUserFromGo).toHaveBeenCalledWith(42, {
                role: 'operator',
                authProvider: 'oidc',
            });
            expect(db.updateLastLogin).toHaveBeenCalledWith(42);

            const verify = await agent.get('/__test/session-user');
            expect(verify.status).toBe(200);
            expect(verify.body.userId).toBe(42);
            expect(verify.body.user).toEqual({
                id: 42,
                username: 'sso-user',
                role: 'operator',
                preferred_language: null,
            });
        });
    });
});
