'use strict';

// A small Express app around the Node console's real session and CSRF code,
// for the Go interop tests (interop_test.go, build tag nodeinterop).
//
// It takes its settings from the environment like the console does:
// SESSION_SECRET, DB_PATH or DATABASE_URL, HTTPS_ENABLED, TRUST_PROXY and
// the SESSION_* policy. It listens on a free port of 127.0.0.1 and prints
// "LISTENING <port>".

const WEB = '/repo/web-nodejs';
const express = require(`${WEB}/node_modules/express`);
const session = require(`${WEB}/node_modules/express-session`);
const cookieParser = require(`${WEB}/node_modules/cookie-parser`);
const config = require(`${WEB}/config/config`);
const { DatabaseSessionStore } = require(`${WEB}/services/databaseSessionStore`);
const panelSessionService = require(`${WEB}/services/panelSessionService`);
const { END_REASONS } = require(`${WEB}/lib/sessionPolicy`);
const { csrfTokenProvider, csrfGate } = require(`${WEB}/middleware/csrf`);
const { parseTrustProxy } = require(`${WEB}/lib/parseTrustProxy`);

const audits = [];
const db = {
    logAction: async (userId, action, details, ip) => {
        audits.push({ userId, action, details, ip });
    },
};

const app = express();
app.set('trust proxy', parseTrustProxy(process.env.TRUST_PROXY));
app.use(express.json({ limit: '2mb' }));
app.use(express.urlencoded({ extended: false, limit: '2mb' }));
app.use(cookieParser());

// From here to the CSRF provider: server.js, "Session management"
const SESSION_COOKIE = config.httpsEnabled ? 'betterdesk.sid' : 'bd.sid';
const persistentSessionStore = new DatabaseSessionStore({
    config,
    ttlMs: config.sessionMaxAge,
    idleTimeoutMs: config.sessionIdleTimeout,
    maxAgeMs: config.sessionMaxAge,
});
panelSessionService.attachStore(persistentSessionStore);
const sessionMiddleware = session({
    secret: config.sessionSecret,
    name: SESSION_COOKIE,
    resave: false,
    rolling: true,
    saveUninitialized: false,
    store: persistentSessionStore,
    cookie: {
        secure: config.httpsEnabled,
        httpOnly: true,
        sameSite: 'lax',
        maxAge: config.sessionIdleTimeout,
    },
});
app.use(sessionMiddleware);
app.use((req, res, next) => {
    if (!req.session || !req.session.userId) return next();
    const touch = panelSessionService.touchActivity(req);
    if (!touch.ok) {
        const reason = touch.reason || 'expired';
        const userId = req.session.userId;
        panelSessionService.endCurrentSession(req, reason).finally(() => {
            req.session.destroy(() => {
                res.clearCookie(SESSION_COOKIE);
                res.clearCookie('betterdesk.sid');
                res.clearCookie('bd.sid');
                const wantsJson = req.path.startsWith('/api/')
                    || (req.get('accept') || '').includes('application/json');
                if (wantsJson) {
                    return res.status(401).json({
                        success: false,
                        error: 'Session expired. Please log in again.',
                        code: reason === 'idle' ? 'session_idle' : 'session_expired',
                    });
                }
                return res.redirect(require(`${WEB}/lib/returnUrl`).loginRedirectUrl(req));
            });
        });
        db.logAction(userId, 'session_expired', `reason=${reason}`, req.ip).catch(() => {});
        return;
    }
    panelSessionService.noteIpChange(req, (uid, action, details, ip) => db.logAction(uid, action, details, ip));
    next();
});
app.use(csrfTokenProvider);

// Routes the tests drive. The first group stands in for routes that come
// before the CSRF gate, so the tests need no token for them.
function whoami(req, res) {
    res.json({
        sid: req.sessionID,
        userId: req.session.userId ?? null,
        user: req.session.user ?? null,
        session: JSON.parse(JSON.stringify(req.session)),
    });
}
app.get('/t/whoami', whoami);
app.get('/api/t/whoami', whoami);

// Sign-in as routes/auth.routes.js does it once the password was checked
app.post('/t/login', (req, res) => {
    const clientMeta = panelSessionService.clientMetaFromRequest(req, req.body);
    req.session.regenerate(async (err) => {
        if (err) return res.status(500).json({ success: false, error: 'Server error' });
        try {
            const { replaced } = await panelSessionService.establishAuthenticatedSession(req, req.body.user, {
                meta: clientMeta,
            });
            res.json({ success: true, sid: req.sessionID, replaced: replaced.length });
        } catch (e) {
            res.status(500).json({ success: false, error: e.message });
        }
    });
});

app.post('/t/set', (req, res) => {
    req.session[req.body.key] = req.body.value;
    res.json({ success: true, sid: req.sessionID });
});

// POST /api/auth/logout of routes/auth.routes.js
app.post('/t/logout', async (req, res) => {
    const userId = req.session?.userId;
    if (userId) {
        await db.logAction(userId, 'logout', 'User logged out', req.ip);
    }
    await panelSessionService.endCurrentSession(req, END_REASONS.LOGOUT);
    req.session.destroy(() => {
        res.clearCookie(req.sessionID ? req.session?.cookie?.name : 'betterdesk.sid');
        res.clearCookie('betterdesk.sid');
        res.clearCookie('bd.sid');
        res.json({ success: true });
    });
});

// GET /logout of routes/auth.routes.js
app.get('/t/logout', async (req, res) => {
    await panelSessionService.endCurrentSession(req, END_REASONS.LOGOUT);
    req.session.destroy(() => {
        res.clearCookie('betterdesk.sid');
        res.clearCookie('bd.sid');
        res.redirect('/login');
    });
});

// The payload of GET /api/auth/sessions
app.get('/t/sessions', async (req, res) => {
    const sessions = await panelSessionService.listSessionsForUser(req.session.userId, req.sessionID);
    res.json({ sessions, policy: panelSessionService.getPolicy() });
});

app.get('/t/csrf', (req, res) => res.json({ token: res.locals.csrfToken }));
app.get('/t/audits', (_req, res) => res.json(audits));

app.use(csrfGate);
app.post('/t/write', (_req, res) => res.json({ success: true }));
app.post('/api/bd/t/write', (_req, res) => res.json({ success: true }));

app.use((err, _req, res, _next) => {
    if (err.code === 'EBADCSRFTOKEN') return res.status(403).json({ success: false, error: 'csrf' });
    res.status(500).json({ success: false, error: String(err && err.message) });
});

persistentSessionStore.ready.then(() => {
    const server = app.listen(0, '127.0.0.1', () => {
        console.log(`LISTENING ${server.address().port}`);
    });
}, (err) => {
    console.error(`session store failed: ${err.message}`);
    process.exit(1);
});
