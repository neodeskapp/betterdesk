/**
 * BetterDesk Console - Auth Routes
 * The sign-in page's data and the last hop of a single sign-on. The other
 * sign-in routes are answered by the Go console (console/routes/auth).
 */

const express = require('express');
const router = express.Router();
const db = require('../services/database');
const betterdeskApi = require('../services/betterdeskApi');
const brandingService = require('../services/brandingService');
const avatarService = require('../services/avatarService');
let serverAttestation;
try {
    serverAttestation = require('../services/serverAttestation');
} catch (err) {
    console.warn('[auth] serverAttestation unavailable:', err.message);
    serverAttestation = {
        getLastResult: async () => null,
        buildPublicSummary: () => ({ tier: null, maxConnections: null }),
        getBadgePresentation: async () => null
    };
}
const { clearGuestCookie } = require('../middleware/guestAccess');
const panelSessionService = require('../services/panelSessionService');
const { safeReturnPath } = require('../lib/returnUrl');
const { manager: i18nManager } = require('../services/i18nService');
const config = require('../config/config');
const crypto = require('crypto');
const bcrypt = require('bcrypt');

/**
 * Where to go after sign-in: the ?return= path when it is a path on this
 * console (lib/returnUrl.js, stricter than auth.IsRelativeReturnURL in the
 * Go server), otherwise the start page.
 */
function returnPathOr(raw, fallback = '/') {
    return safeReturnPath(raw) || fallback;
}

/** The sign-in page's capacity badge (Settings › Server attestation), or one that says nothing */
async function attestationSummary(req) {
    try {
        const last = await serverAttestation.getLastResult();
        const presentation = await serverAttestation.getBadgePresentation({
            tier: last && last.tier,
            translate: req.t,
            brandName: brandingService.getBranding().appName
        });
        return serverAttestation.buildPublicSummary(last, presentation);
    } catch (_) {
        return { tier: null, maxConnections: null };
    }
}

const PROMO_MODES = new Set(['classic', 'single', 'slides']);

/**
 * GET /api/auth/login-page - What the new sign-in page (web-ui) shows before
 * anyone is signed in, the same values the old page rendered: the CSRF
 * token for the sign-in requests, name, version and branding of the
 * console, the languages and the capacity badge. No user data.
 */
router.get('/api/auth/login-page', async (req, res) => {
    const branding = brandingService.getBranding() || {};
    const mode = PROMO_MODES.has(branding.loginPromoMode) ? branding.loginPromoMode : 'classic';
    const slides = mode === 'slides' && Array.isArray(branding.loginSlides) ? branding.loginSlides : [];
    const badge = await attestationSummary(req);
    res.setHeader('Cache-Control', 'no-store');
    res.json({
        success: true,
        csrfToken: res.locals.csrfToken || '',
        appName: branding.appName || config.appName || 'BetterDesk',
        appVersion: config.appVersion || '',
        // The old desktop mode's cookie still asks for the compact sign-in
        desktopMode: !!(req.cookies && req.cookies.betterdesk_desktop_mode === 'true'),
        // Whether this browser has a console session: error pages then keep the
        // console shell, and nobody else gets a refused session request
        signedIn: !!(req.session && req.session.userId
            && !(req.session.user && req.session.user.role === 'pro')),
        branding: {
            logoType: branding.logoType || 'default',
            logoSvg: branding.logoSvg || '',
            logoUrl: branding.logoUrl || '',
            logoText: branding.logoText || '',
            logoTextAccent: branding.logoTextAccent || '',
            loginTitle: branding.loginTitle || '',
            loginSubtitle: branding.loginSubtitle || '',
            loginPromoMode: mode,
            loginSlides: slides.map((s) => ({
                id: String(s.id || ''),
                slideKey: String(s.slideKey || ''),
                imageUrl: String(s.imageUrl || ''),
                title: String(s.title || ''),
                body: String(s.body || ''),
                overlay: Number(s.overlay) || 0
            })),
            footerText: branding.footerText || '',
            showPoweredBy: String(branding.showPoweredBy) !== 'false'
        },
        languages: i18nManager.getAvailableList().map((l) => ({
            code: l.code,
            name: l.name,
            native: l.native || l.name,
            flag: l.flag || '',
            rtl: !!l.rtl
        })),
        attestation: badge && badge.valid && badge.tier
            ? {
                tier: String(badge.tier),
                brand: badge.brand || '',
                badgeTitle: badge.badgeTitle || '',
                tierLabel: badge.tierLabel || ''
            }
            : null
    });
});

/**
 * GET /api/auth/oidc/session - Session creation after OIDC callback.
 *
 * Security model: Go server NEVER passes the JWT or user identity through
 * the browser URL bar. Instead the IdP callback handler stores a one-time
 * auth code (60s TTL) and redirects here with only `?code=<32 bytes>`.
 * Node.js POSTs back to Go's /api/auth/oidc/exchange (server-to-server)
 * to retrieve the JWT plus the verified username/role.
 *
 * This prevents JWT/role leakage via:
 *   - browser history / Referer headers
 *   - access logs (Go, Node.js, reverse proxy)
 *   - role spoofing through URL tampering
 */
router.get('/api/auth/oidc/session', async (req, res) => {
    const code = typeof req.query.code === 'string' ? req.query.code : '';

    if (!code) {
        return res.redirect('/login?error=oidc_invalid');
    }

    try {
        // Server-to-server exchange. Go returns { token, username, role, return_url }
        // only for a valid, unconsumed, unexpired code.
        const exchange = await betterdeskApi.exchangeOIDCCode(code);
        if (!exchange || !exchange.success || !exchange.data || !exchange.data.token || !exchange.data.username) {
            console.warn('[OIDC] Exchange rejected by Go server');
            return res.redirect('/login?error=oidc_invalid');
        }

        const { token, username, role, return_url } = exchange.data;

        // Defense in depth: re-validate the return URL Go provided.
        const safeReturnUrl = returnPathOr(return_url);

        // Look up the local user (may have been auto-provisioned by Go).
        let user = await db.getUserByUsername(username);

        if (!user) {
            // Mirror Go-side auto-provisioning into auth.db so sessions persist
            // and middleware/RBAC can resolve the user ID locally.
            const randomPass = crypto.randomBytes(32).toString('hex');
            const hash = await bcrypt.hash(randomPass, 12);
            try {
                await db.createUser(username, hash, role || 'viewer', 'oidc');
            } catch (err) {
                console.error('[OIDC] Failed to auto-provision local user', username, err.message);
            }
            user = await db.getUserByUsername(username);
        } else {
            try {
                await db.syncUserFromGo(user.id, {
                    role: role || user.role,
                    authProvider: 'oidc',
                });
                user = await db.getUserByUsername(username);
            } catch (err) {
                console.warn('[OIDC] Failed to sync local user from Go', username, err.message);
            }
        }

        if (!user) {
            return res.redirect('/login?error=oidc_error');
        }

        // Profile picture from the IdP's "picture" claim. Runs in the
        // background: a slow or failing picture fetch never delays or stops
        // the sign-in (services/avatarService.js).
        avatarService.syncOidcPicture(user.id, exchange.data);

        // Regenerate session to prevent fixation across the authentication boundary.
        req.session.regenerate(async (err) => {
            if (err) {
                console.error('OIDC session regenerate error:', err);
                return res.redirect('/login?error=oidc_error');
            }

            // Trust the role from the exchange response (server-to-server), not
            // the URL. Fall back to the locally stored role only if Go omitted one.
            const effectiveRole = role || user.role || 'viewer';
            const clientMeta = panelSessionService.clientMetaFromRequest(req);

            try {
                const { replaced } = await panelSessionService.establishAuthenticatedSession(req, {
                    id: user.id,
                    username: user.username,
                    role: effectiveRole,
                    preferred_language: user.preferred_language || null
                }, {
                    meta: clientMeta,
                    extra: {
                        username: user.username,
                        role: effectiveRole,
                        goToken: token,
                        authMethod: 'oidc'
                    }
                });
                clearGuestCookie(res);

                req.session.save(async (saveErr) => {
                    if (saveErr) {
                        console.error('OIDC session save error:', saveErr);
                        return res.redirect('/login?error=oidc_error');
                    }

                    try {
                        await db.updateLastLogin(user.id);
                        const replaceNote = replaced.length
                            ? `; replaced=${replaced.length} prior session(s)`
                            : '';
                        await db.logAction(
                            user.id,
                            'login',
                            `User logged in via OIDC; ip=${clientMeta.ip}; ua=${clientMeta.userAgent.slice(0, 160)}${replaceNote}`,
                            req.ip
                        );
                        if (replaced.length) {
                            await db.logAction(
                                user.id,
                                'session_replaced',
                                `OIDC login replaced ${replaced.length} prior session(s); newIp=${clientMeta.ip}`,
                                req.ip
                            );
                        }
                    } catch (bookkeepingErr) {
                        console.error('OIDC post-auth bookkeeping error:', bookkeepingErr);
                    }
                    res.redirect(safeReturnUrl);
                });
            } catch (sessionErr) {
                console.error('OIDC session establish error:', sessionErr);
                return res.redirect('/login?error=oidc_error');
            }
        });
    } catch (err) {
        console.error('OIDC session creation error:', err);
        res.redirect('/login?error=oidc_error');
    }
});

module.exports = router;
