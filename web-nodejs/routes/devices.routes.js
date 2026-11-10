/**
 * BetterDesk Console - Devices Routes
 *
 * The routes that reach into a running agent over the BetterDesk websocket
 * relay (services/bdRelay.js). The other device routes are answered by the
 * Go console (betterdesk-server/console/routes/devices).
 */

const express = require('express');
const router = express.Router();
const db = require('../services/database');
const serverBackend = require('../services/serverBackend');
const betterdeskApi = require('../services/betterdeskApi');
const deviceGroupService = require('../services/deviceGroupService');
const { requireAuth, requirePermission } = require('../middleware/auth');
const { bodyInt, bodyString, bodyBool, plainBodyObject } = require('../lib/bodyScalars');

async function rejectIfDeviceOutOfScope(req, res, device) {
    if (await deviceGroupService.userCanAccessDevice(db, req.session.user, device)) return false;
    res.status(403).json({ success: false, error: req.t('errors.forbidden') });
    return true;
}

// ===========================================================================
//  Phase 2 — Live agent introspection (services, processes, events, files,
//  terminal, screenshot, activity). All endpoints proxy a command request to
//  the agent over the signal WebSocket (bdRelay.requestFromDevice) and return
//  the agent's reply. If the agent is offline, 503 is returned.
// ===========================================================================

const bdRelay = require('../services/bdRelay');

/**
 * Wrap an agent-proxy request with consistent error handling and timeout.
 * Distinct error codes help the UI render appropriate states.
 */
async function proxyAgentRequest(req, res, type, payload = null, timeoutMs = 15000) {
    try {
        const device = await serverBackend.getDeviceById(req.params.id);
        if (!device) {
            return res.status(404).json({
                success: false,
                error: req.t('devices.not_found')
            });
        }
        if (await rejectIfDeviceOutOfScope(req, res, device)) return;

        const data = await bdRelay.requestFromDevice(req.params.id, type, payload, timeoutMs);
        res.json({ success: true, data });
    } catch (err) {
        const msg = err && err.message ? err.message : 'agent_error';
        const commandMap = {
            'services.list': 'collect.services',
            'processes.list': 'collect.processes',
            'events.list': 'collect.events',
            'activity.get': 'collect.activity',
            'files.browse': 'files.browse',
            'files.read': 'files.read',
        };
        const snapshotMap = {
            'services.list': 'services',
            'processes.list': 'processes',
            'events.list': 'events',
            'activity.get': 'activity',
            'files.browse': 'files',
        };
        if (msg === 'agent_offline' && snapshotMap[type]) {
            try {
                const telemetry = await betterdeskApi.getPeerTelemetry(req.params.id);
                const snapshot = telemetry?.data?.snapshots?.[snapshotMap[type]];
                if (snapshot?.data) {
                    return res.json({
                        success: true,
                        cached: true,
                        collected_at: snapshot.collected_at,
                        data: snapshot.data,
                    });
                }
            } catch (_) {
                // Fall through to a queued collection request.
            }
        }
        if (msg === 'agent_offline' && commandMap[type]) {
            const queued = await betterdeskApi.queuePeerTelemetryCommand(
                req.params.id,
                commandMap[type],
                payload || {}
            );
            if (queued?.success !== false) {
                return res.status(202).json({
                    success: true,
                    pending: true,
                    data: { pending: true, command: commandMap[type] },
                });
            }
        }
        const status = msg === 'agent_offline' ? 503
            : msg === 'agent_timeout' ? 504
            : 502;
        res.status(status).json({ success: false, error: msg });
    }
}

/** GET /api/devices/:id/services — live OS services list */
router.get('/api/devices/:id/services', requireAuth, requirePermission('device.view'), (req, res) => {
    proxyAgentRequest(req, res, 'services.list');
});

/** GET /api/devices/:id/processes — live process list */
router.get('/api/devices/:id/processes', requireAuth, requirePermission('device.view'), (req, res) => {
    proxyAgentRequest(req, res, 'processes.list');
});

/** GET /api/devices/:id/events?limit=100 — recent OS event log / journalctl */
router.get('/api/devices/:id/events', requireAuth, requirePermission('device.view'), (req, res) => {
    const limit = Math.min(Math.max(parseInt(req.query.limit, 10) || 100, 1), 500);
    proxyAgentRequest(req, res, 'events.list', { limit });
});

/** GET /api/devices/:id/activity — live activity tracker (app usage) */
router.get('/api/devices/:id/activity', requireAuth, requirePermission('device.view'), (req, res) => {
    proxyAgentRequest(req, res, 'activity.get');
});

/**
 * POST /api/devices/:id/files/browse
 * Body: { path: '/some/folder', show_hidden: false }
 */
router.post('/api/devices/:id/files/browse', requireAuth, requirePermission('device.edit'), (req, res) => {
    const path = bodyString(req.body?.path, '').slice(0, 4096);
    const showHidden = bodyBool(req.body?.show_hidden, false);
    proxyAgentRequest(req, res, 'files.browse', { path, show_hidden: showHidden });
});

/**
 * POST /api/devices/:id/files/read
 * Body: { path, offset, length }
 */
router.post('/api/devices/:id/files/read', requireAuth, requirePermission('device.edit'), (req, res) => {
    const body = plainBodyObject(req.body);
    const path = bodyString(body.path, '').slice(0, 4096);
    const offset = bodyInt(body.offset, 0, { min: 0 });
    const length = bodyInt(body.length, 65536, { min: 0, max: 1024 * 1024 });
    proxyAgentRequest(req, res, 'files.read', { path, offset, length }, 30000);
});

/**
 * POST /api/devices/:id/files/write   (Phase 63)
 * Body: { path, data: <base64>, mode?: 'overwrite|append|create' }
 * Max payload ~16 MB (enforced agent-side). Audited.
 */
router.post('/api/devices/:id/files/write', requireAuth, requirePermission('device.edit'), async (req, res) => {
    const path = String(req.body?.path || '').slice(0, 4096);
    const data = String(req.body?.data || '');
    const mode = ['overwrite', 'append', 'create'].includes(req.body?.mode) ? req.body.mode : 'overwrite';
    if (!path) return res.status(400).json({ success: false, error: 'path_required' });
    if (data.length > 22 * 1024 * 1024) {
        return res.status(413).json({ success: false, error: 'payload_too_large' });
    }
    try {
        await db.logAction(req.session.userId, 'files.write',
            `Write ${mode} on ${req.params.id}: ${path}`, req.ip || null);
    } catch (_) { /* non-fatal */ }
    proxyAgentRequest(req, res, 'files.write', { path, data, mode }, 30000);
});

/**
 * POST /api/devices/:id/files/delete   (Phase 63)
 * Body: { path, recursive?: bool }
 */
router.post('/api/devices/:id/files/delete', requireAuth, requirePermission('device.edit'), async (req, res) => {
    const path = String(req.body?.path || '').slice(0, 4096);
    const recursive = req.body?.recursive === true;
    if (!path) return res.status(400).json({ success: false, error: 'path_required' });
    try {
        await db.logAction(req.session.userId, 'files.delete',
            `Delete${recursive ? ' (recursive)' : ''} on ${req.params.id}: ${path}`, req.ip || null);
    } catch (_) { /* non-fatal */ }
    proxyAgentRequest(req, res, 'files.delete', { path, recursive }, 15000);
});

/**
 * POST /api/devices/:id/files/rename   (Phase 63)
 * Body: { from, to }
 */
router.post('/api/devices/:id/files/rename', requireAuth, requirePermission('device.edit'), async (req, res) => {
    const from = String(req.body?.from || '').slice(0, 4096);
    const to = String(req.body?.to || '').slice(0, 4096);
    if (!from || !to) return res.status(400).json({ success: false, error: 'paths_required' });
    try {
        await db.logAction(req.session.userId, 'files.rename',
            `Rename on ${req.params.id}: ${from} -> ${to}`, req.ip || null);
    } catch (_) { /* non-fatal */ }
    proxyAgentRequest(req, res, 'files.rename', { from, to }, 10000);
});

/**
 * POST /api/devices/:id/files/mkdir   (Phase 63)
 * Body: { path, recursive?: bool (default true) }
 */
router.post('/api/devices/:id/files/mkdir', requireAuth, requirePermission('device.edit'), async (req, res) => {
    const path = String(req.body?.path || '').slice(0, 4096);
    const recursive = req.body?.recursive !== false;
    if (!path) return res.status(400).json({ success: false, error: 'path_required' });
    try {
        await db.logAction(req.session.userId, 'files.mkdir',
            `Mkdir on ${req.params.id}: ${path}`, req.ip || null);
    } catch (_) { /* non-fatal */ }
    proxyAgentRequest(req, res, 'files.mkdir', { path, recursive }, 10000);
});

/**
 * GET /api/devices/:id/clipboard   (Phase 64)
 * Reads the device's text clipboard.
 */
router.get('/api/devices/:id/clipboard', requireAuth, requirePermission('device.view'), (req, res) => {
    proxyAgentRequest(req, res, 'clipboard.get', null, 5000);
});

/**
 * POST /api/devices/:id/clipboard   (Phase 64)
 * Body: { text }   (max 1 MiB enforced agent-side)
 */
router.post('/api/devices/:id/clipboard', requireAuth, requirePermission('device.edit'), async (req, res) => {
    const text = typeof req.body?.text === 'string' ? req.body.text : '';
    if (text.length > 1024 * 1024) {
        return res.status(413).json({ success: false, error: 'text_too_large' });
    }
    try {
        await db.logAction(req.session.userId, 'clipboard.set',
            `Clipboard set on ${req.params.id} (${text.length} chars)`, req.ip || null);
    } catch (_) { /* non-fatal */ }
    proxyAgentRequest(req, res, 'clipboard.set', { text }, 5000);
});

/**
 * POST /api/devices/:id/screenshot
 * Captures a JPEG snapshot from the agent. Returns base64 image.
 */
router.post('/api/devices/:id/screenshot', requireAuth, requirePermission('device.view'), (req, res) => {
    proxyAgentRequest(req, res, 'screenshot.capture', null, 20000);
});

/**
 * POST /api/devices/:id/input/mouse
 * Body: { action: 'move|down|up|click|wheel', x?, y?, x_rel?, y_rel?,
 *         screen_w?, screen_h?, button?: 'left|right|middle',
 *         wheel_dx?, wheel_dy? }
 * Forwards a single mouse event to the agent (Phase 58).
 */
router.post('/api/devices/:id/input/mouse', requireAuth, requirePermission('device.edit'), (req, res) => {
    const b = req.body || {};
    const payload = {
        action: String(b.action || 'move'),
        button: typeof b.button === 'string' ? b.button : undefined,
    };
    if (typeof b.x === 'number') payload.x = Math.trunc(b.x);
    if (typeof b.y === 'number') payload.y = Math.trunc(b.y);
    if (typeof b.x_rel === 'number') payload.x_rel = b.x_rel;
    if (typeof b.y_rel === 'number') payload.y_rel = b.y_rel;
    if (typeof b.screen_w === 'number') payload.screen_w = Math.trunc(b.screen_w);
    if (typeof b.screen_h === 'number') payload.screen_h = Math.trunc(b.screen_h);
    if (typeof b.wheel_dx === 'number') payload.wheel_dx = Math.trunc(b.wheel_dx);
    if (typeof b.wheel_dy === 'number') payload.wheel_dy = Math.trunc(b.wheel_dy);
    proxyAgentRequest(req, res, 'input.mouse', payload, 5000);
});

/**
 * POST /api/devices/:id/input/key
 * Body: { key: 'Enter|Escape|a|F5|...', action?: 'press|down|up' }
 */
router.post('/api/devices/:id/input/key', requireAuth, requirePermission('device.edit'), (req, res) => {
    const key = String(req.body?.key || '').slice(0, 32);
    if (!key) return res.status(400).json({ success: false, error: 'key_required' });
    const action = String(req.body?.action || 'press');
    proxyAgentRequest(req, res, 'input.key', { key, action }, 5000);
});

/**
 * POST /api/devices/:id/input/text
 * Body: { text: 'hello world' }
 */
router.post('/api/devices/:id/input/text', requireAuth, requirePermission('device.edit'), (req, res) => {
    const text = String(req.body?.text || '').slice(0, 4096);
    if (!text) return res.status(400).json({ success: false, error: 'text_required' });
    proxyAgentRequest(req, res, 'input.text', { text }, 8000);
});

/**
 * POST /api/devices/:id/terminal/execute
 * Body: { command: 'ls -la /tmp' }
 * One-shot command execution (no PTY). For interactive terminal use the WS
 * endpoint (future Phase 4 integration).
 */
router.post('/api/devices/:id/terminal/execute', requireAuth, requirePermission('device.edit'), async (req, res) => {
    const command = String(req.body?.command || '').slice(0, 4096);
    if (!command.trim()) {
        return res.status(400).json({ success: false, error: 'command_required' });
    }
    // Audit this — terminal execution is a sensitive action.
    try {
        await db.logAction(req.session.userId, 'terminal.execute',
            `Terminal command on ${req.params.id}: ${command.substring(0, 200)}`,
            req.ip || null);
    } catch (_) { /* audit failure should not block the command */ }
    proxyAgentRequest(req, res, 'terminal.execute', { command }, 30000);
});

module.exports = router;
