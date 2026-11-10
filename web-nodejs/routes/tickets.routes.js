/**
 * BetterDesk Console — Helpdesk / Ticketing API Routes (device-facing)
 *
 * The routes people use (/api/tickets and below: tickets, comments,
 * attachments, statistics) are answered by the Go console
 * (betterdesk-server/console/routes/tickets). What is left here are the two
 * routes of the desktop agent, which take a device token:
 *
 *   POST   /api/tickets/bd        — Create ticket from agent
 *   GET    /api/tickets/bd        — List own tickets
 *
 * @author UNITRONIX
 * @version 1.0.0
 */

'use strict';

const express = require('express');
const router = express.Router();
const { getAdapter } = require('../services/dbAdapter');
const { requireDeviceToken } = require('../middleware/deviceAuth');

const VALID_PRIORITIES = ['low', 'medium', 'high', 'critical'];
const VALID_CATEGORIES = ['general', 'hardware', 'software', 'network', 'access', 'other'];

// ---------------------------------------------------------------------------
//  SLA defaults (hours per priority)
// ---------------------------------------------------------------------------

const SLA_HOURS = {
    critical: 4,
    high: 8,
    medium: 24,
    low: 72,
};

function calculateSlaDue(priority) {
    const hours = SLA_HOURS[priority] || SLA_HOURS.medium;
    const due = new Date();
    due.setHours(due.getHours() + hours);
    return due.toISOString();
}

// ---------------------------------------------------------------------------
//  Device-facing endpoints (agent creates/views tickets via token)
// ---------------------------------------------------------------------------

/**
 * POST /api/tickets/bd — Create ticket from desktop agent.
 */
router.post('/bd', requireDeviceToken, async (req, res) => {
    try {
        const { title, description, priority, category } = req.body;

        if (!title || !title.trim()) {
            return res.status(400).json({ error: 'Title is required' });
        }

        const effectivePriority = VALID_PRIORITIES.includes(priority) ? priority : 'medium';
        const effectiveCategory = VALID_CATEGORIES.includes(category) ? category : 'general';

        const adapter = getAdapter();
        const ticket = await adapter.createTicket({
            title: title.trim(),
            description: description || '',
            priority: effectivePriority,
            category: effectiveCategory,
            deviceId: req.deviceId,
            createdBy: `agent:${req.deviceId}`,
            assignedTo: null,
            slaDueAt: calculateSlaDue(effectivePriority),
        });

        console.log(`[Tickets] #${ticket.id} created by agent ${req.deviceId}: ${title}`);
        res.status(201).json({ success: true, ticket_id: ticket.id });
    } catch (err) {
        console.error('[Tickets] Agent create error:', err.message);
        res.status(500).json({ error: 'Internal server error' });
    }
});

/**
 * GET /api/tickets/bd — List own tickets (agent).
 */
router.get('/bd', requireDeviceToken, async (req, res) => {
    try {
        const adapter = getAdapter();
        const tickets = await adapter.getAllTickets({ device_id: req.deviceId });
        res.json({ tickets, total: tickets.length });
    } catch (err) {
        console.error('[Tickets] Agent list error:', err.message);
        res.status(500).json({ error: 'Internal server error' });
    }
});

module.exports = router;
