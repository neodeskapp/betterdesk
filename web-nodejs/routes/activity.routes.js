/**
 * BetterDesk Console — Activity Monitoring API Routes (device-facing)
 *
 * The routes people read activity with (GET /api/activity, /api/activity/:id
 * and /api/activity/:id/top) are answered by the Go console
 * (betterdesk-server/console/routes/activity). What is left here is the
 * route of the desktop agent, which takes a device token. The router is
 * mounted at /api/bd and at /api/activity, so it answers at both:
 *
 *   POST   /api/bd/activity      — Upload activity sessions + idle info
 *
 * @author UNITRONIX
 * @version 1.0.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const { getAdapter } = require('../services/dbAdapter');
const alertRulesEngine = require('../services/alertRulesEngine');

const {
    requireDeviceToken,
    requireTokenDeviceMatch,
} = require('../middleware/deviceAuth');

// ---------------------------------------------------------------------------
//  Device-facing endpoint
// ---------------------------------------------------------------------------

/**
 * POST /api/bd/activity — Upload activity sessions and idle state.
 *
 * Body: {
 *   device_id: string,
 *   sessions: [{ app_name, window_title, category, started_at, ended_at, duration_secs }],
 *   idle_seconds: number,
 *   timestamp: string (ISO 8601)
 * }
 */
router.post('/activity', requireDeviceToken, requireTokenDeviceMatch, async (req, res) => {
    try {
        const { device_id, sessions, idle_seconds, timestamp } = req.body;

        if (!device_id) {
            return res.status(400).json({ success: false, error: 'Missing device_id' });
        }

        // Validate that the authenticated device matches
        if (req.deviceId && req.deviceId !== device_id) {
            return res.status(403).json({ success: false, error: 'Device ID mismatch' });
        }

        const adapter = getAdapter();

        // Insert individual activity sessions (if any)
        if (Array.isArray(sessions) && sessions.length > 0) {
            // Validate and sanitize sessions
            const valid = sessions
                .filter(s => s.started_at && s.ended_at)
                .slice(0, 500); // Max 500 per upload

            if (valid.length > 0) {
                await adapter.insertActivitySessions(device_id, valid);
            }
        }

        // Upsert summary
        const totalActive = Array.isArray(sessions)
            ? sessions.reduce((sum, s) => sum + (s.duration_secs || 0), 0)
            : 0;

        await adapter.upsertActivitySummary(device_id, {
            idle_seconds: idle_seconds ?? 0,
            session_count: Array.isArray(sessions) ? sessions.length : 0,
            total_active_secs: totalActive,
            reported_at: timestamp || new Date().toISOString(),
        });

        // Idle-time alert rules
        if (idle_seconds !== undefined && idle_seconds !== null) {
            alertRulesEngine.evaluateInBackground(() => alertRulesEngine.evaluateRules(device_id, 'idle_duration', idle_seconds));
        }

        console.log(`[Activity] Received ${sessions?.length || 0} sessions from ${device_id} (idle: ${idle_seconds ?? 0}s)`);
        res.json({ success: true });
    } catch (err) {
        console.error('[Activity] Upload error:', err.message);
        res.status(500).json({ success: false, error: 'Internal server error' });
    }
});

module.exports = router;
