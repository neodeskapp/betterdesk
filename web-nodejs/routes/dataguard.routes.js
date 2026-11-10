/**
 * BetterDesk Console — DataGuard / DLP API Routes (device-facing)
 *
 * The routes people use (policies, events and statistics under
 * /api/dataguard and /api/bd) are answered by the Go console
 * (betterdesk-server/console/routes/dataguard). What is left here are the
 * two routes of the desktop agent, which take a device token. The router is
 * mounted at /api/dataguard and at /api/bd, so each answers at both:
 *
 *   GET    /api/bd/dlp-policies         — Fetch active policies for agent
 *   POST   /api/bd/dlp-events           — Report a DLP event (violation / info)
 *
 * @author  UNITRONIX
 * @version 1.0.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const { getAdapter } = require('../services/dbAdapter');

const { requireDeviceToken } = require('../middleware/deviceAuth');

// =========================================================================
//  Device-facing — Policy sync & event reporting
// =========================================================================

/**
 * GET /dlp-policies
 * Returns only enabled policies for the requesting agent.
 * Mounted under /api/bd so full path is /api/bd/dlp-policies
 */
router.get('/dlp-policies', requireDeviceToken, async (req, res) => {
    try {
        const db = getAdapter();
        const policies = await db.getDlpPolicies();
        const active = policies
            .filter(p => {
                const enabled = typeof p.enabled === 'number' ? p.enabled === 1 : !!p.enabled;
                return enabled;
            })
            .map(p => ({
                id: p.id,
                name: p.name,
                rules: typeof p.rules === 'string' ? JSON.parse(p.rules) : (p.rules || []),
            }));
        res.json(active);
    } catch (err) {
        console.error('[DataGuard] GET /dlp-policies error:', err.message);
        res.status(500).json({ error: 'Failed to fetch policies' });
    }
});

/**
 * POST /dlp-events
 * Body: { event_source, event_type, policy_id?, policy_name?, action?, details? }
 * Mounted under /api/bd so full path is /api/bd/dlp-events
 */
router.post('/dlp-events', requireDeviceToken, async (req, res) => {
    try {
        const deviceId = req.deviceId || (req.session && req.session.user && req.session.user.username) || 'unknown';
        const { event_source, event_type, policy_id, policy_name, action, details } = req.body;

        if (!event_source || typeof event_source !== 'string') {
            return res.status(400).json({ error: 'event_source is required (usb | file)' });
        }

        const db = getAdapter();
        const result = await db.insertDlpEvent({
            device_id: deviceId,
            event_source,
            event_type: event_type || 'info',
            policy_id: policy_id || null,
            policy_name: policy_name || '',
            action: action || 'log',
            details: details || {},
        });

        res.status(201).json({ ok: true, id: result.id });
    } catch (err) {
        console.error('[DataGuard] POST /dlp-events error:', err.message);
        res.status(500).json({ error: 'Failed to record event' });
    }
});

module.exports = router;
