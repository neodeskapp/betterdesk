/**
 * BetterDesk Console — Automation API Routes
 *
 * The device side of remote commands. The panel routes for alert rules,
 * the alert history, remote commands and the SMTP configuration
 * (GET/POST /api/automation/rules, GET/PATCH/DELETE /api/automation/rules/:id,
 * GET /api/automation/alerts, POST /api/automation/alerts/:id/ack,
 * POST/GET /api/automation/commands, GET /api/automation/commands/:id,
 * GET/PUT /api/automation/smtp, POST /api/automation/smtp/test) are served
 * by the Go console.
 *
 * Endpoints:
 *
 * Device-facing (agent polls for commands):
 *   GET    /api/bd/commands               — Get pending commands
 *   POST   /api/bd/commands/:id/result    — Submit command result
 *
 * @author UNITRONIX
 * @version 1.0.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const db = require('../services/database');
const { getAdapter } = require('../services/dbAdapter');
const { requireDeviceToken } = require('../middleware/deviceAuth');

// ===========================================================================
//  Device-facing: Agent polls for commands
// ===========================================================================

/**
 * GET /api/bd/commands — Get pending commands for this device.
 */
router.get('/commands', requireDeviceToken, async (req, res) => {
    try {
        const adapter = getAdapter();
        const commands = await adapter.getPendingCommands(req.deviceId);
        res.json({ commands });
    } catch (err) {
        console.error('[Automation] Pending commands error:', err.message);
        res.status(500).json({ error: 'Internal server error' });
    }
});

/**
 * POST /api/bd/commands/:id/result — Agent submits command result.
 */
router.post('/commands/:id(\\d+)/result', requireDeviceToken, async (req, res) => {
    try {
        const { status, result } = req.body;
        if (!status || !['completed', 'failed', 'running'].includes(status)) {
            return res.status(400).json({ error: 'Status must be completed, failed, or running' });
        }

        const adapter = getAdapter();
        const cmd = await adapter.getRemoteCommandById(+req.params.id);
        if (!cmd) {
            return res.status(404).json({ error: 'Command not found' });
        }
        if (cmd.device_id !== req.deviceId) {
            return res.status(403).json({ error: 'Device ID mismatch' });
        }

        await adapter.updateRemoteCommand(cmd.id, {
            status,
            result: result !== undefined ? String(result) : null,
        });

        console.log(`[Automation] Command #${cmd.id} result from ${req.deviceId}: ${status}`);
        res.json({ success: true });
    } catch (err) {
        console.error('[Automation] Command result error:', err.message);
        res.status(500).json({ error: 'Internal server error' });
    }
});

module.exports = router;
