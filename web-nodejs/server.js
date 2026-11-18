/**
 * BetterDesk Console - Server Entry Point
 * Professional Web Management Panel for RustDesk Server
 * 
 * @author UNITRONIX
 * @version 2.1.0
 * @license AGPL-3.0
 */

const express = require('express');
const session = require('express-session');
const cookieParser = require('cookie-parser');
const path = require('path');
const fs = require('fs');
const http = require('http');
const https = require('https');

const config = require('./config/config');
const { redactUrlForLog } = require('./lib/logRedact');
const logger = require('./lib/logger');
const securityMiddleware = require('./middleware/security');
const { initI18n } = require('./middleware/i18n');
const {
    apiLimiter,
    sessionReadLimiter,
    widgetLimiter,
    panelReadLimiter,
    panelPreferenceLimiter,
    getPanelPollMountPaths,
    getPanelReadMountPaths
} = require('./middleware/rateLimiter');
const { csrfTokenProvider, csrfGate, downgradeToHttp: csrfDowngradeToHttp } = require('./middleware/csrf');
const { loadEffectivePermissions } = require('./middleware/auth');
const authService = require('./services/authService');
const db = require('./services/database');
const { DatabaseSessionStore } = require('./services/databaseSessionStore');
const userSync = require('./services/userSync');
const { loadEffectivePolicy } = require('./services/panelAccessPolicyService');
const { panelAccessGuard } = require('./middleware/panelAccess');
const { startDiscoveryService } = require('./services/lanDiscovery');
const { initHelpRequestEmailService } = require('./services/helpRequestEmailService');
const routes = require('./routes');
const { mountRoutes } = require('./lib/mountRoutes');
const { parseTrustProxy } = require('./lib/parseTrustProxy');
const {
    resolvePortForCurrentUser,
    attachPrivilegedPortErrorHandler,
} = require('./lib/privilegedPorts');
const { resolveRedirectHost, buildSafeRedirectUrl } = require('./lib/redirectHost');

// Create Express app
const app = express();

// Trust proxy (for rate limiting behind reverse proxy)
// Configurable via TRUST_PROXY env var: false/0=off, 1/true=single proxy, 'loopback'=localhost only
// Default: false (safest). Set TRUST_PROXY=1 when behind nginx/Apache/cloudflare
const trustProxy = parseTrustProxy(process.env.TRUST_PROXY);
app.set('trust proxy', trustProxy);

// Ensure data directory exists (required before auth.db / session secret)
try {
    if (!fs.existsSync(config.dataDir)) {
        fs.mkdirSync(config.dataDir, { recursive: true });
    }
} catch (err) {
    console.error(`Failed to create data directory (${config.dataDir}): ${err.message}`);
    console.error('If the console runs as a dedicated user, run: sudo node scripts/linux-ensure-console-user.js');
    process.exit(1);
}

// ============ Middleware Pipeline ============

// Security headers (Helmet)
app.use(securityMiddleware);

// The console (web-ui/build) at the root. Its pages are mounted after the
// routes (lib/mountRoutes.js); res.sendErrorPage() is there for every
// handler from here on, the error handlers included.
const webUi = require('./lib/webUi').createWebUi();
app.use(webUi.errorPages);

// CORS for BetterDesk desktop clients (Tauri webview origins)
app.use('/api/', (req, res, next) => {
    const origin = req.headers.origin || '';
    const allowed = [
        'http://localhost:1420',    // Tauri dev
        'tauri://localhost',        // Tauri production (macOS/Linux)
        'https://tauri.localhost',  // Tauri production (Windows)
    ];
    if (allowed.includes(origin)) {
        res.setHeader('Access-Control-Allow-Origin', origin);
        res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, PATCH, DELETE, OPTIONS');
        res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Authorization, X-API-Key, X-CSRF-Token');
        res.setHeader('Access-Control-Allow-Credentials', 'true');
        res.setHeader('Access-Control-Max-Age', '86400');
    }
    if (req.method === 'OPTIONS') return res.sendStatus(204);
    next();
});

// Body parsing (2MB limit for base64 logo images, lib/jsonBody.js)
app.use(require('./lib/jsonBody').jsonBody());
app.use(express.urlencoded({ extended: false, limit: '2mb' }));

// Cookie parsing
app.use(cookieParser());

// Session management — also kept as a standalone middleware ref for WebSocket upgrades
// Use a different cookie name in HTTP mode to avoid collision with stale
// Secure cookies left over from a previous HTTPS configuration (Issue #82).
//
// Store sessions in the selected BetterDesk database. This makes logout,
// password/role changes and process restarts enforceable without another
// hidden auth database.
const SESSION_COOKIE = config.httpsEnabled ? 'betterdesk.sid' : 'bd.sid';
const persistentSessionStore = new DatabaseSessionStore({
    config,
    ttlMs: config.sessionMaxAge,
    idleTimeoutMs: config.sessionIdleTimeout,
    maxAgeMs: config.sessionMaxAge
});
const panelSessionService = require('./services/panelSessionService');
panelSessionService.attachStore(persistentSessionStore);
persistentSessionStore.ready.catch((err) => {
    logger.error('[Session] Persistent session store initialization failed:', err.message);
});
const sessionCleanupTimer = setInterval(
    () => persistentSessionStore.cleanup((err) => err && logger.warn('[Session] Cleanup failed:', err.message)),
    Math.max(5 * 60 * 1000, Math.min(60 * 60 * 1000, config.sessionIdleTimeout))
);
sessionCleanupTimer.unref?.();
const sessionMiddleware = session({
    secret: config.sessionSecret,
    name: SESSION_COOKIE,
    resave: false,
    // Rolling idle window: activity extends cookie until absolute maxAge.
    rolling: true,
    saveUninitialized: false,
    store: persistentSessionStore,
    cookie: {
        secure: config.httpsEnabled,
        httpOnly: true,
        sameSite: 'lax',
        maxAge: config.sessionIdleTimeout
    }
});
app.use(sessionMiddleware);
// Server-side idle/absolute enforcement + IP-change audit for authenticated sessions.
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
                        code: reason === 'idle' ? 'session_idle' : 'session_expired'
                    });
                }
                // The way back to this page after signing in again (lib/returnUrl.js, R3)
                return res.redirect(require('./lib/returnUrl').loginRedirectUrl(req));
            });
        });
        db.logAction(userId, 'session_expired', `reason=${reason}`, req.ip).catch(() => {});
        return;
    }
    panelSessionService.noteIpChange(req, (uid, action, details, ip) => db.logAction(uid, action, details, ip));
    next();
});

// Generate the double-submit token before routes that need route-level CSRF
// protection, including the session-authenticated notification endpoints.
app.use(csrfTokenProvider);

// Cache version — changes on every restart/deployment, stable during runtime.
// Used in ?v= query strings so browsers cache assets per deployment.
app.locals.cacheVersion = config.appVersion + '.' + Date.now();

// Static files
app.use(express.static(path.join(__dirname, 'public'), {
    maxAge: config.isProduction ? '7d' : '0',
    etag: true
}));

// Serve proto files for remote client (protobufjs dynamic loading)
app.use('/protos', express.static(path.join(__dirname, 'protos'), {
    maxAge: config.isProduction ? '7d' : '0',
    etag: true
}));

// Serve uploaded branding assets (logos etc.) from persistent data dir
const uploadsDir = path.join(config.dataDir || path.join(__dirname, 'data'), 'uploads');
if (!fs.existsSync(uploadsDir)) fs.mkdirSync(uploadsDir, { recursive: true });
app.use('/uploads', express.static(uploadsDir, {
    maxAge: config.isProduction ? '30d' : '0',
    etag: true
}));

// Serve desktop wallpapers
app.use('/wallpapers', express.static(path.join(__dirname, 'wallpapers'), {
    maxAge: config.isProduction ? '30d' : '0',
    etag: true,
    immutable: true
}));

// Resolve role overrides before browser-facing routers. Static assets are
// intentionally served first so authenticated pages do not add a Go lookup
// for every CSS, image, and JavaScript request.
app.use(loadEffectivePermissions);
app.use(loadEffectivePolicy);
app.use(panelAccessGuard);

// Rate limiting for API.
// SECURITY (audit fix M-03, 2026-04-10): high-frequency widget refresh paths
// have their own higher-quota limiter mounted BEFORE the general one so they
// are still bounded but do not eat into the regular API budget.
// Reads by someone signed in count per person (the per-address limiters skip them).
app.use('/api/', sessionReadLimiter);
for (const p of getPanelPollMountPaths()) {
    app.use(p, widgetLimiter);
}
for (const p of getPanelReadMountPaths()) {
    app.use(p, panelReadLimiter);
}
app.use('/api/panel', widgetLimiter);
app.use('/api/desktop/layout', panelPreferenceLimiter);
app.use('/api/', apiLimiter);

// i18n middleware
app.use(initI18n());

// Only static files may be cached: no answer that isn't one is stored
app.use((req, res, next) => {
    if (!req.path.match(/\.(js|css|png|jpg|jpeg|gif|svg|ico|woff2?|ttf|eot|map|proto)$/)) {
        res.set('Cache-Control', 'no-cache, no-store, must-revalidate');
    }
    next();
});

// CSRF protection — validate on POST/PUT/DELETE/PATCH.
// Device-facing API routes (/api/bd/*) authenticate via Bearer access token
// and skip it, unless the request carries a signed-in browser session
// (admin routers are also mounted under /api/bd; see csrfGate).
//
// SECURITY (audit fix C-02, 2026-04-10): the previous Origin-based CSRF skip
// for Tauri webview origins (`tauri://localhost`, `https://tauri.localhost`,
// `http://localhost:1420`) was removed — `Origin` is freely forgeable by any
// non-browser HTTP client, so it is unsafe as a CSRF-bypass signal. Tauri
// desktop clients receive the CSRF token via `csrfTokenProvider` and must
// echo it back in the `X-CSRF-Token` header (csrf-csrf double-submit).
app.use(csrfGate);

// ============ Routes ============

// The console API's JSON answers go out compressed when the browser asks
app.use('/api/', require('./lib/jsonCompression').jsonCompression());

// The server's routes, then the console's pages, then the error handlers
mountRoutes(app, { routes, webUi });

// ============ Startup ============

/**
 * Warn if the user set Go-server-only TLS env vars in the Node.js environment.
 * These variables (TLS_CERT, TLS_KEY) are read exclusively by the Go server.
 * The Node.js console uses SSL_CERT_PATH / SSL_KEY_PATH instead.
 * Silently ignoring them causes issue #104 — port 21121 stays HTTP while the
 * RustDesk client expects HTTPS, producing InvalidContentType errors.
 */
function warnGoTlsEnvVars() {
    const hasTlsCert = !!process.env.TLS_CERT;
    const hasTlsKey  = !!process.env.TLS_KEY;
    if (!hasTlsCert && !hasTlsKey) return;

    const hasSslCertPath = !!process.env.SSL_CERT_PATH;
    const hasSslKeyPath  = !!process.env.SSL_KEY_PATH;

    if (hasTlsCert || hasTlsKey) {
        console.warn('');
        console.warn('  ┌─────────────────────────────────────────────────────┐');
        console.warn('  │  ⚠  MISCONFIGURATION WARNING — TLS / SSL           │');
        console.warn('  ├─────────────────────────────────────────────────────┤');
        console.warn('  │  TLS_CERT / TLS_KEY are Go server environment       │');
        console.warn('  │  variables and are IGNORED by this Node.js console. │');
        console.warn('  │                                                     │');
        console.warn('  │  To enable HTTPS on this console set:               │');
        console.warn('  │    SSL_CERT_PATH=/path/to/fullchain.pem             │');
        console.warn('  │    SSL_KEY_PATH=/path/to/privkey.pem                │');
        console.warn('  │                                                     │');
        if (!hasSslCertPath && !hasSslKeyPath) {
            console.warn('  │  ❌ SSL_CERT_PATH and SSL_KEY_PATH are NOT set.    │');
            console.warn('  │     Port 21121 (RustDesk Client API) is HTTP.     │');
            console.warn('  │     Clients connecting via HTTPS will fail with   │');
            console.warn('  │     InvalidContentType errors.                    │');
        } else {
            console.warn('  │  ✅ SSL_CERT_PATH / SSL_KEY_PATH are set — OK.    │');
        }
        console.warn('  └─────────────────────────────────────────────────────┘');
        console.warn('');
    }
}

/**
 * Load SSL certificates for HTTPS
 */
function loadSslCertificates() {
    const options = {};
    
    if (!config.sslCertPath || !config.sslKeyPath) {
        return null;
    }
    
    try {
        if (!fs.existsSync(config.sslCertPath)) {
            console.error(`SSL certificate not found: ${config.sslCertPath}`);
            return null;
        }
        if (!fs.existsSync(config.sslKeyPath)) {
            console.error(`SSL private key not found: ${config.sslKeyPath}`);
            return null;
        }
        
        options.cert = fs.readFileSync(config.sslCertPath);
        options.key = fs.readFileSync(config.sslKeyPath);
        
        // Optional CA bundle (for Let's Encrypt chain)
        if (config.sslCaPath && fs.existsSync(config.sslCaPath)) {
            options.ca = fs.readFileSync(config.sslCaPath);
        }
        
        return options;
    } catch (err) {
        console.error('Failed to load SSL certificates:', err.message);
        return null;
    }
}

/**
 * Create HTTP redirect server (redirects all HTTP to HTTPS)
 *
 * SECURITY (audit H5, 2026-11-13): the Location header must not be derived from
 * the request `Host` header. See `lib/redirectHost.js` for the reasoning; this
 * function only ever redirects to a configured host or the bound listener
 * address.
 */
function createHttpRedirectServer(httpsPort, options = {}) {
    const redirectApp = express();
    const publicHost = (() => {
        try {
            return require('./services/rustDeskPublicEndpointsService').readPanelPublicHostValue();
        } catch (_) {
            return '';
        }
    })();
    const target = resolveRedirectHost({
        configuredHost: options.configuredHost || publicHost || process.env.PANEL_PUBLIC_HOST || '',
        listenHost: options.listenHost || config.host,
    });

    redirectApp.use((req, res) => {
        const httpsUrl = buildSafeRedirectUrl({
            host: target.host,
            port: httpsPort,
            urlPath: req.url,
        });
        res.setHeader('Cache-Control', 'no-store');
        res.redirect(307, httpsUrl);
    });

    return http.createServer(redirectApp);
}

async function startServer() {
    // Warn early about common TLS misconfiguration (Go env vars used instead of Node.js vars)
    warnGoTlsEnvVars();

    try {
        // Initialize database adapter (creates tables, runs migrations)
        await db.init();

        // Warm branding cache from database (must run after db.init)
        const brandingService = require('./services/brandingService');
        await brandingService.loadBranding();

        // Recover/sync global users before deciding whether a default admin is needed.
        // This protects upgrades where local auth.db was recreated but Go still has users.
        await userSync.backfillFromGo();

        // Ensure default admin exists
        await authService.ensureDefaultAdmin();

        // Keep Go organization-linkable users aligned with the panel store.
        await userSync.backfillFromNode();
        
        let server;
        let protocol = 'http';
        const listenPort = resolvePortForCurrentUser(config.port, 5000, 'HTTP');
        const listenHttpsPort = resolvePortForCurrentUser(config.httpsPort, 5443, 'HTTPS');
        let displayPort = listenPort;
        
        // HTTPS mode
        if (config.httpsEnabled) {
            const sslOptions = loadSslCertificates();
            
            if (sslOptions) {
                // Create HTTPS server
                server = https.createServer(sslOptions, app);
                protocol = 'https';
                displayPort = listenHttpsPort;
                
                attachPrivilegedPortErrorHandler(server, { port: listenHttpsPort, label: 'HTTPS' });
                server.listen(listenHttpsPort, config.host, () => {
                    printStartupBanner(protocol, displayPort);
                });
                
                // Optionally start HTTP redirect server
                if (config.httpRedirect) {
                    const redirectServer = createHttpRedirectServer(listenHttpsPort, {
                        listenHost: config.host,
                    });
                    attachPrivilegedPortErrorHandler(redirectServer, { port: listenPort, label: 'HTTP redirect' });
                    redirectServer.listen(listenPort, config.host, () => {
                        console.log(`  HTTP -> HTTPS redirect active on port ${listenPort}`);
                        console.log('');
                    });
                    
                    // Graceful shutdown for redirect server too
                    const shutdownRedirect = () => { redirectServer.close(); };
                    process.on('SIGTERM', shutdownRedirect);
                    process.on('SIGINT', shutdownRedirect);
                }
            } else {
                console.warn('WARNING: HTTPS enabled but certificates not found/invalid');
                console.warn('Falling back to HTTP mode');
                console.warn('  → Session and CSRF cookies downgraded to non-Secure');
                console.warn('  → Fix: check SSL_CERT_PATH and SSL_KEY_PATH in .env');
                
                // BD-2026-082: Downgrade cookie flags to match actual HTTP mode.
                // Session middleware was initialised with secure:true at module
                // load time.  Without this fixup, browsers would ignore all
                // cookies and every request would fail CSRF validation.
                if (sessionMiddleware && sessionMiddleware.options) {
                    sessionMiddleware.options.cookie = sessionMiddleware.options.cookie || {};
                    sessionMiddleware.options.cookie.secure = false;
                }
                csrfDowngradeToHttp();
                
                server = http.createServer(app);
                attachPrivilegedPortErrorHandler(server, { port: listenPort, label: 'HTTP' });
                server.listen(listenPort, config.host, () => {
                    printStartupBanner(protocol, listenPort);
                });
            }
        } else {
            // HTTP mode (default)
            server = http.createServer(app);
            attachPrivilegedPortErrorHandler(server, { port: listenPort, label: 'HTTP' });
            server.listen(listenPort, config.host, () => {
                printStartupBanner(protocol, listenPort);
            });
        }
        


        initHelpRequestEmailService(config.betterdeskApiUrl, config.betterdeskApiKey);

        // Panel section catalog: Go is the source of truth (strangler stage P3).
        // The bundled copy in lib/panelAccessCatalog.js stays as the fallback
        // for an older server binary; a failure here must never block startup.
        require('./lib/panelAccessCatalog').refreshPanelCatalog({ force: true })
            .then((catalog) => {
                if (catalog) {
                    console.log(`[panel] authorization catalog loaded from Go (${catalog.sections.length} sections)`);
                }
            })
            .catch((err) => {
                console.warn('[panel] catalog refresh failed, using the bundled copy:', err.message);
            });

        // Start LAN Discovery UDP service
        startDiscoveryService();
        try {
            const panelDiscovery = require('./services/panelDiscovery');
            panelDiscovery.startPanelMdns();
        } catch (err) {
            console.warn('[server] mDNS panel discovery disabled:', err.message);
        }

        // Defer build workers until after listen + event-bus WS connect settle
        // (#353): toolchain/DB work racing native addon init can abort Node 24.
        setImmediate(() => {
            // BetterDesk Support Generator — local embedded Client builder.
            // Disabled when AGENT_BUILD_WORKER=off.
            if (process.env.AGENT_BUILD_WORKER !== 'off') {
                try {
                    const clientTemplateWorker = require('./services/clientTemplateWorker');
                    clientTemplateWorker.startWorker();
                    if (String(
                        process.env.BETTERDESK_CLIENT_GENERATOR_MODE || 'download'
                    ).trim().toLowerCase() === 'embedded') {
                        const clientBuilder = require('./services/clientBuilderService');
                        clientBuilder.syncAndQueueRebuilds()
                            .then((result) => {
                                if (result.queued) {
                                    console.log(
                                        `[server] BetterDesk-Client ${result.sha} changed; `
                                        + 'embedded Support rebuild queued'
                                    );
                                }
                            })
                            .catch((err) => {
                                console.warn('[server] BetterDesk-Client sync/rebuild deferred:', err.message);
                            });
                    }
                } catch (err) {
                    console.warn('[server] client template worker disabled:', err.message);
                }
            }
        });
        
        // ============ Periodic Housekeeping ============
        // Old metrics, client audit rows and stale device states are
        // cleaned up by the Go console in front of this one.
        const housekeepingInterval = setInterval(async () => {
            await authService.cleanupHousekeeping();
        }, 60 * 60 * 1000); // Every hour
        // Offline-time alert rules, checked once a minute
        const stopAlertSweep = require('./services/alertRulesEngine').start();

        // Graceful shutdown
        const shutdown = (signal) => {
            console.log(`\n${signal} received. Shutting down gracefully...`);
            clearInterval(housekeepingInterval);
            stopAlertSweep();

            const closePromises = [new Promise(r => server.close(r))];
            
            Promise.all(closePromises).then(() => {
                console.log('All servers closed.');
                process.exit(0);
            });
            
            // Force exit after 10 seconds
            setTimeout(() => {
                console.error('Forced shutdown after timeout');
                process.exit(1);
            }, 10000);
        };
        
        process.on('SIGTERM', () => shutdown('SIGTERM'));
        process.on('SIGINT', () => shutdown('SIGINT'));
        
    } catch (err) {
        console.error('Failed to start server:', err);
        process.exit(1);
    }
}

/**
 * Print startup banner with server info
 */
function printStartupBanner(protocol, port) {
    const sslStatus = config.httpsEnabled ? '🔒 HTTPS' : '🔓 HTTP';
    const panelUrl = `${protocol}://${config.host}:${port}`;
    const goApiUrl = redactUrlForLog(config.betterdeskApiUrl || process.env.BETTERDESK_API_URL || 'http://localhost:21114/api');
    console.log('');
    console.log('  ╔══════════════════════════════════════════════════╗');
    console.log('  ║                                                  ║');
    console.log('  ║   🖥️  BetterDesk Console v' + config.appVersion.padEnd(23) + '  ║');
    console.log('  ║                                                  ║');
    console.log('  ╠══════════════════════════════════════════════════╣');
    console.log('  ║                                                  ║');
    console.log(`  ║   Panel:      ${panelUrl}`.padEnd(53) + '║');
    if (config.httpsEnabled && config.httpRedirect) {
        console.log(`  ║   Redirect:   http://${config.host}:${config.port} → :${config.httpsPort}`.padEnd(53) + '║');
    }
    console.log(`  ║   Go API:     ${goApiUrl}`.padEnd(53) + '║');
    console.log(`  ║   Mode:       ${config.nodeEnv}`.padEnd(53) + '║');
    console.log(`  ║   Security:   ${sslStatus}`.padEnd(53) + '║');
    const dbLabel = (db.DB_TYPE === 'postgres' || db.DB_TYPE === 'postgresql')
        ? `PostgreSQL (${process.env.DATABASE_URL ? new URL(process.env.DATABASE_URL).hostname : 'localhost'})`
        : path.basename(config.dbPath);
    console.log(`  ║   Database:   ${dbLabel}`.padEnd(53) + '║');
    console.log(`  ║   Keys:       ${config.keysPath}`.padEnd(53) + '║');
    console.log('  ║                                                  ║');
    console.log('  ╚══════════════════════════════════════════════════╝');
    console.log('');

    // BD-2026-006: Warn if panel is bound to all interfaces in non-Docker environments
    if (config.host === '0.0.0.0' && !config.isDocker) {
        console.log('  ⚠️  WARNING [SECURITY]: Panel bound to 0.0.0.0 (all interfaces).');
        console.log('     Set HOST=127.0.0.1 in .env to restrict to localhost only.');
        console.log('');
    }

    if (config.isProduction && config.host === '0.0.0.0' && !config.httpsEnabled) {
        const betterdeskApi = require('./services/betterdeskApi');
        betterdeskApi.getEnrollmentMode().then((result) => {
            const mode = (result && result.data && (result.data.mode || result.data)) || 'open';
            if (String(mode).toLowerCase() === 'open') {
                console.log('  ⛔ ERROR [SECURITY]: Production panel on 0.0.0.0 without HTTPS and enrollment=open.');
                console.log('     Prefer managed/locked enrollment, enable HTTPS, or bind HOST=127.0.0.1 behind a reverse proxy.');
                console.log('');
            }
        }).catch(() => { /* Go API may not be ready yet */ });
    }

    // BD-2026-008: Warn if plaintext credentials file exists
    const credFile = path.join(config.keysPath, '.admin_credentials');
    if (fs.existsSync(credFile)) {
        console.log('  ⚠️  WARNING [SECURITY]: Plaintext .admin_credentials file detected.');
        console.log('     Delete it after noting the password: ' + credFile);
        console.log('');
    }

    // BD-2026-009: Warn when proxy trust is enabled
    if (trustProxy && trustProxy !== false && trustProxy !== 0) {
        console.log('  ⚠️  NOTICE [SECURITY]: TRUST_PROXY is enabled (' + trustProxy + ').');
        console.log('     Ensure a trusted reverse proxy sets X-Forwarded-For correctly.');
        console.log('');
    }

    // L-01 (audit 2026-04-10): warn about disabled proxy trust in production
    // — rate limiters and audit logs will see the proxy IP, not the client IP.
    if (process.env.NODE_ENV === 'production' && (!trustProxy || trustProxy === false || trustProxy === 0)) {
        console.log('  ⚠️  WARNING [SECURITY]: NODE_ENV=production but TRUST_PROXY is disabled.');
        console.log('     If the panel is behind a reverse proxy (nginx, Cloudflare, ALB,');
        console.log('     Traefik…) rate-limit keys and audit logs will record the proxy IP,');
        console.log('     not the real client IP. Set TRUST_PROXY=1 (single proxy) or a CIDR list.');
        console.log('');
    }

    // H-04 (audit 2026-04-10): unconditional banner when the RustDesk client
    // API TOTP bypass is enabled, regardless of acknowledgement — the bypass
    // weakens 2FA on the WAN-facing :21121 endpoint and operators MUST be
    // aware of it on every restart.
    if (config.rustdeskApiDisableTotp) {
        if (!config.rustdeskApiDisableTotpAck) {
            console.log('  ⛔  ERROR  [SECURITY]: RUSTDESK_API_DISABLE_TOTP=true but ACK flag is missing.');
            console.log('     The bypass is IGNORED. Set RUSTDESK_API_DISABLE_TOTP_ACKNOWLEDGED=true');
            console.log('     to confirm you accept disabling 2FA on the RustDesk client login.');
            console.log('');
        } else {
            console.log('  ⚠️  WARNING [SECURITY]: TOTP is DISABLED on the RustDesk client API (:21121).');
            console.log('     RustDesk desktop clients can log in with username+password only.');
            console.log('     The web panel still enforces 2FA independently.');
            console.log('');
        }
    }
}

// Start the server
startServer();

module.exports = app;
