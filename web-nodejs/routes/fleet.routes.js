'use strict';

const express = require('express');
const router = express.Router();
const { apiClient } = require('../services/betterdeskApi');
const { proxyToGo } = require('../lib/goApiProxy');
const { requireDeviceToken, requireTokenDeviceMatch } = require('../middleware/deviceAuth');

// ---------------------------------------------------------------------------
// Device-facing API (agents report back — body JSON, unchanged for compatibility)
// ---------------------------------------------------------------------------

router.post('/api/bd/fleet/task-result', requireDeviceToken, requireTokenDeviceMatch, (req, res) => {
    proxyToGo(apiClient, req, res, 'POST', '/fleet/task-result', req.body);
});

router.post('/api/bd/fleet/software', requireDeviceToken, requireTokenDeviceMatch, (req, res) => {
    proxyToGo(apiClient, req, res, 'POST', '/fleet/software', req.body);
});

module.exports = router;
