/**
 * BetterDesk Console — organization policies and device attestation, the
 * routes devices call. The routes of the admin panel
 * (/api/panel/policies/*, /api/panel/attestation*) are in the Go console
 * (betterdesk-server/console/routes/policies).
 *
 *   GET    /api/bd/device-policy                    — Agent fetches its policy
 *   POST   /api/bd/attestation                      — Agent reports attestation
 */

'use strict';

const express = require('express');
const router = express.Router();
const { apiClient } = require('../services/betterdeskApi');
const { assertSafeApiId } = require('../lib/goApiPath');
const { requireDeviceToken, requireTokenDeviceMatch } = require('../middleware/deviceAuth');

// ---------------------------------------------------------------------------
//  Device-facing: agent fetches its policy
// ---------------------------------------------------------------------------

router.get('/api/bd/device-policy', requireDeviceToken, requireTokenDeviceMatch, async (req, res) => {
    const rawDeviceId = req.query.device_id || req.headers['x-device-id'] || req.deviceId;
    if (!rawDeviceId) {
        return res.status(400).json({ error: 'device_id required' });
    }
    let deviceId;
    try {
        deviceId = assertSafeApiId(rawDeviceId, 'device_id');
    } catch (err) {
        return res.status(400).json({ error: err.message });
    }
    try {
        const resp = await apiClient({ method: 'get', url: `/peers/${encodeURIComponent(deviceId)}/policy` });
        res.json(resp.data);
    } catch (err) {
        const status = err.response?.status || 500;
        res.status(status).json(err.response?.data || { error: 'Policy fetch failed' });
    }
});

// ---------------------------------------------------------------------------
//  Device-facing: agent reports attestation
// ---------------------------------------------------------------------------

// Device reports attestation data
router.post('/api/bd/attestation', requireDeviceToken, requireTokenDeviceMatch, async (req, res) => {
    const { device_id, fingerprint, platform_data } = req.body;
    if (!device_id || !fingerprint) {
        return res.status(400).json({ error: 'device_id and fingerprint required' });
    }
    let deviceId;
    try {
        deviceId = assertSafeApiId(device_id, 'device_id');
    } catch (err) {
        return res.status(400).json({ error: err.message });
    }
    try {
        const resp = await apiClient({
            method: 'post',
            url: `/peers/${encodeURIComponent(deviceId)}/attestation`,
            data: { fingerprint, platform_data }
        });
        res.json(resp.data);
    } catch (err) {
        const status = err.response?.status || 500;
        res.status(status).json(err.response?.data || { error: 'Attestation report failed' });
    }
});

module.exports = router;
