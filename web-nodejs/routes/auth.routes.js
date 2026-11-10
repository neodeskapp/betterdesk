/**
 * BetterDesk Console - Auth Routes
 * The sign-in page's data. The other sign-in routes are answered by the Go
 * console (console/routes/auth).
 */

const express = require('express');
const router = express.Router();
const brandingService = require('../services/brandingService');
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
const { manager: i18nManager } = require('../services/i18nService');
const config = require('../config/config');

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

module.exports = router;
