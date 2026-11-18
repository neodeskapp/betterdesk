/**
 * BetterDesk Console - Routes Index
 * Mounts all route modules
 */

const express = require('express');
const router = express.Router();

const settingsRoutes = require('./settings.routes');
const remoteRoutes = require('./remote.routes');

/**
 * Lazy-load wrapper — defers require() until first request.
 * Reduces startup time by ~200-400ms for rarely-used modules.
 */
function lazyRoute(modulePath) {
    let _router;
    return function (req, res, next) {
        if (!_router) _router = require(modulePath);
        _router(req, res, next);
    };
}

// Lazy-loaded route modules (loaded on first request)
const serverManagementRoutes = lazyRoute('./server-management.routes');

// JSON-only writes to /api/* (lib/requireJsonContentType.js)
const { requireJsonContentType } = require('../lib/requireJsonContentType');

// Apply JSON content type check middleware
router.use(requireJsonContentType);

// Mount routes
router.use('/', settingsRoutes);
router.use('/', require('./client-branding.routes'));
router.use('/', remoteRoutes);
router.use('/', serverManagementRoutes);                 // admin-facing: /server-management, /api/server-management/* (BETA)

module.exports = router;
