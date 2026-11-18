/**
 * BetterDesk Console — remote agent token of the device API
 *
 * The device API under /api/bd is answered by the Go console
 * (betterdesk-server/console/routes/bdapi). What is left here is the one
 * route that hands out tokens for the remote agent socket
 * (/ws/remote-agent), which lives in services/remoteRelay.js; it moves with
 * that socket.
 *
 *   POST /api/bd/remote-agent-token — Single-use token for /ws/remote-agent
 */

'use strict';

const express = require('express');
const router = express.Router();
const remoteRelay = require('../services/remoteRelay');
const {
    requireDeviceToken,
    requireTokenDeviceMatch,
} = require('../middleware/deviceAuth');

router.post('/remote-agent-token', requireDeviceToken, requireTokenDeviceMatch, async (req, res) => {
    try {
        const id = req.body.device_id || req.deviceId;
        if (!id || !/^[A-Za-z0-9_-]{3,64}$/.test(id)) {
            return res.status(400).json({ error: 'device_id is required' });
        }
        if (req.deviceId && req.deviceId !== id) {
            return res.status(403).json({ error: 'device_id mismatch' });
        }
        const issued = remoteRelay.issueRemoteAgentToken(id);
        res.json({ success: true, device_id: id, ...issued });
    } catch (err) {
        console.error('[BD-API] remote-agent-token error:', err.message);
        res.status(500).json({ error: 'Token issuance failed' });
    }
});

module.exports = router;
