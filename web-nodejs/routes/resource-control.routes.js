'use strict';

const express = require('express');
const router = express.Router();
const { assertSafeApiId } = require('../lib/goApiPath');
const { requireDeviceToken, requireTokenDeviceMatch } = require('../middleware/deviceAuth');

let apiClient;
try {
    apiClient = require('../services/betterdeskApi').apiClient;
} catch (e) {
    apiClient = null;
}

function deviceResourcePath(deviceId, suffix = '') {
    const id = assertSafeApiId(deviceId, 'deviceId');
    return `/devices/${encodeURIComponent(id)}/resources${suffix}`;
}

// --- Device-facing API ---

/**
 * GET /api/bd/resource-policy — Agent fetches its resource policy
 */
router.get('/api/bd/resource-policy', requireDeviceToken, requireTokenDeviceMatch, async (req, res) => {
    const deviceId = req.headers['x-device-id'] || req.query.device_id || req.deviceId;
    if (!deviceId) {
        return res.status(400).json({ error: 'Missing device_id' });
    }
    try {
        if (!apiClient) {
            return res.json({ policy: null });
        }
        const resp = await apiClient.get(deviceResourcePath(deviceId));
        res.json(resp.data);
    } catch (err) {
        if (err.message && /^Invalid /.test(err.message)) {
            return res.status(400).json({ error: err.message });
        }
        res.json({ policy: null });
    }
});

module.exports = router;
