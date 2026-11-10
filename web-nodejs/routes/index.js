/**
 * BetterDesk Console - Routes Index
 * Mounts all route modules
 */

const express = require('express');
const router = express.Router();

const authRoutes = require('./auth.routes');
const devicesRoutes = require('./devices.routes');
const settingsRoutes = require('./settings.routes');
const generatorRoutes = require('./generator.routes');
const usersRoutes = require('./users.routes');
const remoteRoutes = require('./remote.routes');
// bdApiRoutes mounted in server.js (before CSRF) for desktop client access
const pagesRoutes = require('./pages.routes');
const registrationRoutes = require('./registration.routes');

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
const inventoryRoutes = lazyRoute('./inventory.routes');
const activityRoutes = lazyRoute('./activity.routes');
const automationRoutes = lazyRoute('./automation.routes');
const fileTransferRoutes = lazyRoute('./fileTransfer.routes');
const commercializationRoutes = lazyRoute('./commercialization.routes');
const phase45Routes = lazyRoute('./phase4_5.routes');
const serverManagementRoutes = lazyRoute('./server-management.routes');
const serverAttestationRoutes = lazyRoute('./server-attestation.routes');

// JSON-only writes to /api/* (lib/requireJsonContentType.js)
const { requireJsonContentType } = require('../lib/requireJsonContentType');

// Apply JSON content type check middleware
router.use(requireJsonContentType);

// Mount routes
router.use('/', authRoutes);
router.use('/', require('./console.routes'));   // Svelte console: /console-boot.js, /api/console/*
router.use('/', devicesRoutes);
router.use('/', settingsRoutes);
router.use('/', generatorRoutes);
router.use('/', require('./client-branding.routes'));
router.use('/', usersRoutes);
router.use('/', remoteRoutes);
// bdApiRoutes now mounted in server.js (before CSRF) for desktop client access
router.use('/api/bd', inventoryRoutes);     // device-facing: /api/bd/inventory, /api/bd/telemetry
router.use('/api/inventory', inventoryRoutes); // admin-facing: /api/inventory, /api/inventory/:id
router.use('/api/bd', activityRoutes);           // device-facing: /api/bd/activity
router.use('/api/activity', activityRoutes);     // admin-facing: /api/activity
router.use('/api/automation', automationRoutes); // admin-facing: /api/automation/*
router.use('/api/bd', automationRoutes);         // device-facing: /api/bd/commands
router.use('/api/files', fileTransferRoutes);    // device-facing, second address: /api/files/file-transfer (the routes for people are in the Go console)
router.use('/api/bd', fileTransferRoutes);       // device-facing: /api/bd/file-transfer
router.use('/', registrationRoutes);               // admin-facing: /registrations, /api/registrations/*
router.use('/api/bd', registrationRoutes);          // device-facing: /api/bd/register-request, /api/bd/register-status
router.use('/', pagesRoutes);                          // page routes: /inventory, /tickets, /automation, etc.
router.use('/', commercializationRoutes);                // admin-facing: /commercialization, /api/panel/billing/*
router.use('/', serverManagementRoutes);                 // admin-facing: /server-management, /api/server-management/* (BETA)
router.use('/', serverAttestationRoutes);                // admin-facing: /server-attestation, /api/server-attestation/*
router.use('/', phase45Routes);                          // Phase 4/5: /api/users/me/profile, /api/agent-templates, /portal

module.exports = router;
