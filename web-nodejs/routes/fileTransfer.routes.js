/**
 * BetterDesk Console — File Transfer API Routes (device-facing)
 *
 * The routes people use (start, list, progress, cancel, download under
 * /api/files and /api/bd) are in the Go console. What is left here are the
 * two routes a device calls with its token:
 *
 *   POST   /api/bd/file-transfer        — Agent initiates or sends chunks
 *   GET    /api/bd/file-transfer/:id     — Agent gets transfer info
 *
 * They keep their own transfer registry (services/fileTransferService.js).
 *
 * @author UNITRONIX
 * @version 1.0.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const fileTransferService = require('../services/fileTransferService');

const { requireDeviceToken, requireTokenDeviceMatch } = require('../middleware/deviceAuth');

// ---------------------------------------------------------------------------
//  Device-facing endpoints
// ---------------------------------------------------------------------------

/**
 * POST /api/bd/file-transfer — Agent sends file transfer messages
 *
 * Body JSON with { action, transfer_id, chunk_index, data, filename, size, mime_type }
 */
router.post('/file-transfer', requireDeviceToken, requireTokenDeviceMatch, (req, res) => {
    try {
        const result = fileTransferService.handleWsMessage(req.deviceId, req.body);
        res.json(result);
    } catch (err) {
        res.status(400).json({ action: 'error', error: err.message });
    }
});

/**
 * GET /api/bd/file-transfer/:id — Agent gets transfer info
 */
router.get('/file-transfer/:id', requireDeviceToken, (req, res) => {
    const transfer = fileTransferService.getTransfer(req.params.id);
    if (!transfer) {
        return res.status(404).json({ error: 'Transfer not found' });
    }
    // Only allow the device that owns the transfer
    if (transfer.deviceId !== req.deviceId) {
        return res.status(403).json({ error: 'Access denied' });
    }
    res.json(fileTransferService.getProgress(req.params.id));
});

module.exports = router;
