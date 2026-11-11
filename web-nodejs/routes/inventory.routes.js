/**
 * BetterDesk Console — Inventory & Telemetry API Routes (device-facing)
 *
 * The routes people read inventory with (GET /api/inventory and
 * /api/inventory/:id) are answered by the Go console
 * (betterdesk-server/console/routes/inventory). What is left here are the
 * routes of the desktop agent, which take a device token. The router is
 * mounted at /api/bd and at /api/inventory, so each answers at both:
 *
 *   POST   /api/bd/inventory   — Full inventory upload (HW + SW)
 *   POST   /api/bd/telemetry   — Lightweight telemetry (CPU/RAM)
 *   GET    /api/bd/inventory/:id — Get last inventory for a device
 *
 * @author UNITRONIX
 * @version 2.0.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const db = require('../services/database');
const { getAdapter } = require('../services/dbAdapter');
const alertRulesEngine = require('../services/alertRulesEngine');

// ---------------------------------------------------------------------------
//  Helpers (shared with bd-api.routes.js)
// ---------------------------------------------------------------------------

function extractBearerToken(req) {
    const auth = req.headers['authorization'];
    const match = typeof auth === 'string' && /^Bearer\s+(\S+)$/.exec(auth);
    return match ? match[1] : null;
}

/**
 * Authenticate a device with an unrevoked, non-expired access token.
 *
 * X-Device-Id is intentionally not an authentication credential: accepting it
 * alone would let any client impersonate an enrolled device.
 */
async function requireDeviceToken(req, res, next) {
    const token = extractBearerToken(req);
    if (!token) {
        return res.status(401).json({ error: 'Bearer access token required' });
    }

    let tokenRow;
    try {
        tokenRow = await db.getAccessToken(token);
    } catch (_) {
        return res.status(401).json({ error: 'Invalid or expired access token' });
    }

    if (!tokenRow || !tokenRow.client_id) {
        return res.status(401).json({ error: 'Invalid or unbound access token' });
    }

    req.deviceId = tokenRow.client_id;
    req.deviceToken = tokenRow;
    try {
        await db.touchAccessToken(token);
    } catch (_) {
        // Recording last use must not invalidate an already validated token.
    }
    return next();
}

/**
 * Require the access token to belong to the requested device before any data
 * lookup or write. This also protects against a valid token reading another
 * device's inventory.
 */
function requireTokenDeviceMatch(req, res, deviceId) {
    if (req.deviceId !== deviceId) {
        res.status(403).json({ success: false, error: 'Device ID mismatch' });
        return false;
    }
    return true;
}

/**
 * The fullest disk of a report, for the disk alert rules.
 * Agents send sizes in bytes or in GB, and used space directly or as
 * free/available space. Returns {} when no disk has a usable size.
 */
function fullestDisk(disks) {
    if (!Array.isArray(disks)) return {};
    const GB = 1024 ** 3;
    const num = (v) => (v === null || v === undefined || v === '' ? null : Number(v));
    let best = null;
    for (const disk of disks) {
        if (!disk || typeof disk !== 'object' || disk.removable) continue;
        const total = num(disk.total_bytes) ?? (num(disk.total_gb) !== null ? num(disk.total_gb) * GB : null);
        if (!(total > 0)) continue;
        let used = num(disk.used_bytes) ?? (num(disk.used_gb) !== null ? num(disk.used_gb) * GB : null);
        if (used === null && num(disk.available_bytes) !== null) used = total - num(disk.available_bytes);
        if (used === null && num(disk.free_bytes) !== null) used = total - num(disk.free_bytes);
        if (used === null || !Number.isFinite(used)) continue;
        const pct = Math.max(0, Math.min(100, (used / total) * 100));
        if (!best || pct > best.pct) {
            best = { pct, total, free: Math.max(0, total - used), mount: String(disk.mount_point || disk.device || disk.name || '').slice(0, 120) };
        }
    }
    if (!best) return {};
    return {
        disk_max_used_percent: Math.round(best.pct * 10) / 10,
        disk_max_mount: best.mount,
        disk_max_free_bytes: Math.round(best.free),
    };
}

// ---------------------------------------------------------------------------
//  Device-facing endpoints (authenticated via device-bound access token)
// ---------------------------------------------------------------------------

/**
 * POST /api/bd/inventory — Full inventory upload (HW + SW).
 *
 * Body: { device_id, hardware: {...}, software: {...}, collected_at }
 */
router.post('/inventory', requireDeviceToken, async (req, res) => {
    try {
        const { device_id, hardware, software, collected_at } = req.body;

        if (!device_id || !hardware) {
            return res.status(400).json({ success: false, error: 'Missing device_id or hardware' });
        }

        // Validate that the authenticated device matches
        if (!requireTokenDeviceMatch(req, res, device_id)) return;

        // Persist to database
        const adapter = getAdapter();
        await adapter.upsertInventory(device_id, hardware, software, collected_at);

        // Also update peer info in main database if peer exists
        try {
            const peer = await db.getPeerById(device_id);
            if (peer) {
                const info = {
                    ...(peer.info ? JSON.parse(peer.info) : {}),
                    hostname: hardware.hostname || undefined,
                    os: hardware.os_name || undefined,
                    os_version: hardware.os_version || undefined,
                    cpu: hardware.cpu?.brand || undefined,
                    cpu_cores: hardware.cpu?.logical_cores || undefined,
                    memory_mb: hardware.memory?.total_bytes
                        ? Math.round(hardware.memory.total_bytes / 1048576)
                        : undefined,
                };
                await db.updatePeer(device_id, { info: JSON.stringify(info) });
            }
        } catch (dbErr) {
            console.warn('[Inventory] Failed to update peer info:', dbErr.message);
        }

        // Disk alert rules, on the fullest disk
        const disk = fullestDisk(hardware.disks).disk_max_used_percent;
        if (disk !== undefined) {
            alertRulesEngine.evaluateInBackground(() => alertRulesEngine.evaluateRules(device_id, 'disk_usage', disk));
        }

        console.log(`[Inventory] Full inventory received from ${device_id} (HW + ${software?.apps?.length || 0} apps)`);

        res.json({ success: true });
    } catch (err) {
        console.error('[Inventory] Upload error:', err.message);
        res.status(500).json({ success: false, error: 'Internal server error' });
    }
});

/**
 * POST /api/bd/telemetry — Lightweight telemetry (CPU/RAM/uptime).
 *
 * Body: { device_id, cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs, timestamp }
 */
router.post('/telemetry', requireDeviceToken, async (req, res) => {
    try {
        const {
            device_id,
            cpu_usage_percent,
            memory_used_bytes,
            memory_total_bytes,
            uptime_secs,
            timestamp,
        } = req.body;

        if (!device_id) {
            return res.status(400).json({ success: false, error: 'Missing device_id' });
        }

        if (!requireTokenDeviceMatch(req, res, device_id)) return;

        const adapter = getAdapter();
        await adapter.upsertTelemetry(device_id, {
            cpu_usage_percent: cpu_usage_percent ?? 0,
            memory_used_bytes: memory_used_bytes ?? 0,
            memory_total_bytes: memory_total_bytes ?? 0,
            uptime_secs: uptime_secs ?? 0,
            timestamp: timestamp || new Date().toISOString(),
        });

        // CPU and memory alert rules
        alertRulesEngine.evaluateInBackground(() =>
            alertRulesEngine.evaluateTelemetry(device_id, { cpu_usage_percent, memory_used_bytes, memory_total_bytes }));

        res.json({ success: true });
    } catch (err) {
        console.error('[Telemetry] Upload error:', err.message);
        res.status(500).json({ success: false, error: 'Internal server error' });
    }
});

/**
 * GET /api/bd/inventory/:id — Get last inventory for a specific device.
 * Accessible by the device itself (via token) or by admin.
 */
router.get('/inventory/:id', requireDeviceToken, async (req, res) => {
    try {
        const deviceId = req.params.id;
        if (!requireTokenDeviceMatch(req, res, deviceId)) return;
        const adapter = getAdapter();
        const entry = await adapter.getInventory(deviceId);

        if (!entry) {
            return res.status(404).json({ error: 'No inventory data for this device' });
        }

        res.json(entry);
    } catch (err) {
        console.error('[Inventory] Get error:', err.message);
        res.status(500).json({ error: 'Internal server error' });
    }
});

module.exports = router;
