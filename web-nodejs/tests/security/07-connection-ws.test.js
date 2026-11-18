/**
 * Connection / WebSocket endpoints used by RustDesk web viewer and BetterDesk clients.
 * All servers bind 127.0.0.1 only.
 */
'use strict';

const { validateOrigin } = require('../../middleware/wsOrigin');
const { createTestApp } = require('./helpers');
const request = require('supertest');
const { EVIL_ORIGINS } = require('./payloads');

describe('[sec/07] Connection WebSocket / BD relay auth', () => {
    describe('CSWSH — Origin policy', () => {
        it('rejects evil Origin when session cookie is present', () => {
            for (const origin of EVIL_ORIGINS) {
                const result = validateOrigin({
                    headers: {
                        host: 'panel.local:5000',
                        origin,
                        cookie: 'bd.sid=abc',
                    },
                });
                expect(result.ok).toBe(false);
            }
        });

        it('allows same-host Origin', () => {
            expect(validateOrigin({
                headers: {
                    host: 'panel.local:5000',
                    origin: 'http://panel.local:5000',
                    cookie: 'bd.sid=abc',
                },
            }).ok).toBe(true);
        });

        it('rejects missing Origin with session cookie', () => {
            expect(validateOrigin({
                headers: {
                    host: 'panel.local:5000',
                    cookie: 'bd.sid=abc',
                },
            }).ok).toBe(false);
        });

        it('allows missing Origin without session cookie (token clients)', () => {
            expect(validateOrigin({
                headers: { host: 'panel.local:5000' },
            }).ok).toBe(true);
        });
    });

    describe('BD API Bearer-only (session cookie cannot drive connect)', () => {
        it('rejects cookie-only POST /api/bd/connect', async () => {
            async function requireDeviceAuth(req, res) {
                const auth = req.headers.authorization || '';
                if (!/^Bearer\s+\S+/i.test(auth)) {
                    return res.status(401).json({ error: 'Missing or invalid Bearer access token' });
                }
                return res.status(401).json({ error: 'Missing or invalid Bearer access token' });
            }
            const app = createTestApp();
            app.post('/api/bd/connect', (req, _res, next) => {
                req.session.userId = 1;
                req.session.user = { id: 1, role: 'admin' };
                next();
            }, requireDeviceAuth);

            const res = await request(app)
                .post('/api/bd/connect')
                .set('Cookie', 'bd.sid=fake-session')
                .send({ target: 'BD-1' });
            expect(res.status).toBe(401);
        });
    });

    describe('CSP hardened for connection/viewer pages', () => {
        it('defaults to no unsafe-eval / no scriptSrcAttr unsafe-inline', () => {
            const src = require('fs').readFileSync(
                require('path').join(__dirname, '..', '..', 'middleware', 'security.js'),
                'utf8'
            );
            expect(src).toContain("scriptSrcAttr: [\"'none'\"]");
            expect(src).not.toContain("scriptSrcAttr: [\"'unsafe-inline'\"]");
            expect(src).toContain("scriptSrc: [\"'self'\"]");
            expect(src).not.toContain('unsafe-eval');
        });
    });
});

