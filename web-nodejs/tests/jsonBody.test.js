'use strict';

/**
 * The console caps every JSON body at 2 MB. Ticket attachments, which may
 * be larger, are read by the Go console's attachment route; its tests cover
 * the 35 MB limit there (betterdesk-server/console/routes/tickets).
 */

const express = require('express');
const request = require('supertest');
const { jsonBody } = require('../lib/jsonBody');

function app() {
    const a = express();
    a.use(jsonBody());
    a.post('/api/other', (req, res) => res.json({ size: req.body.data.length }));
    a.use((err, _req, res, _next) => res.status(err.status || 500).json({ error: err.type || 'error' }));
    return a;
}

// A 4 MB file as base64 (about 5.6 MB of JSON)
const big = { filename: 'log.zip', data: Buffer.alloc(4 * 1024 * 1024, 7).toString('base64') };

describe('JSON body limits', () => {
    test('bodies above 2 MB are refused', async () => {
        const res = await request(app()).post('/api/other').send(big);
        expect(res.status).toBe(413);
    });

    test('small bodies parse on every path', async () => {
        const res = await request(app()).post('/api/other').send({ data: 'abc' });
        expect(res.body.size).toBe(3);
    });
});
