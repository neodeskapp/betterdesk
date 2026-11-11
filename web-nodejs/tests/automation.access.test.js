/**
 * The device routes of automation on /api/bd keep working: an agent
 * polling GET /api/bd/commands with its token reaches the device handler
 * (the admin routes of the routers mounted at /api/bd before it used to
 * answer 401). The panel routes for rules, alerts, commands and the SMTP
 * settings, which need server.config, are served by the Go console and
 * tested there.
 */

const request = require('supertest');
const { createTestApp, withAuth } = require('./helpers');

const mockAdapter = {
    getRemoteCommandById: jest.fn(),
    getPendingCommands: jest.fn(),
    updateRemoteCommand: jest.fn().mockResolvedValue(undefined),
    getInventory: jest.fn(),
    getAllInventories: jest.fn().mockResolvedValue([]),
    getActivitySessions: jest.fn().mockResolvedValue([]),
};
const mockDb = {
    getAccessToken: jest.fn(),
    touchAccessToken: jest.fn().mockResolvedValue(undefined),
};

jest.mock('../services/database', () => mockDb);
jest.mock('../services/dbAdapter', () => ({ getAdapter: jest.fn(() => mockAdapter) }));
jest.mock('../services/betterdeskApi', () => ({ getAllPeers: jest.fn().mockResolvedValue([]) }));

const automationRoutes = require('../routes/automation.routes');
const inventoryRoutes = require('../routes/inventory.routes');
const activityRoutes = require('../routes/activity.routes');

beforeEach(() => {
    jest.clearAllMocks();
    mockAdapter.getRemoteCommandById.mockResolvedValue({ id: 5, device_id: 'dev-1', payload: 'whoami', result: 'root' });
    mockAdapter.getPendingCommands.mockResolvedValue([{ id: 5, command_type: 'shell', payload: 'whoami' }]);
});

/** The device side as routes/index.js mounts it: inventory, activity, then automation at /api/bd */
function deviceApp(user) {
    const app = createTestApp();
    if (user) withAuth(app, user);
    app.use('/api/bd', inventoryRoutes);
    app.use('/api/bd', activityRoutes);
    app.use('/api/bd', automationRoutes);
    app.use('/api/automation', automationRoutes);
    return app;
}

describe('device routes on /api/bd', () => {
    beforeEach(() => {
        mockDb.getAccessToken.mockResolvedValue({ client_id: 'dev-1' });
    });

    it('an agent polling GET /api/bd/commands with its token gets its pending commands', async () => {
        const res = await request(deviceApp()).get('/api/bd/commands').set('Authorization', 'Bearer device-token');
        expect(res.status).toBe(200);
        expect(res.body).toEqual({ commands: [{ id: 5, command_type: 'shell', payload: 'whoami' }] });
        expect(mockAdapter.getPendingCommands).toHaveBeenCalledWith('dev-1');
    });

    it('an agent can report a command result', async () => {
        const res = await request(deviceApp())
            .post('/api/bd/commands/5/result')
            .set('Authorization', 'Bearer device-token')
            .send({ status: 'completed', result: 'root' });
        expect(res.status).toBe(200);
        expect(mockAdapter.updateRemoteCommand).toHaveBeenCalledWith(5, { status: 'completed', result: 'root' });
    });

    it('a request without a token still gets 401, and no admin route answers on /api/bd', async () => {
        mockDb.getAccessToken.mockResolvedValue(null);
        expect((await request(deviceApp()).get('/api/bd/commands')).status).toBe(401);
        // This router has no admin route left
        const app = deviceApp({ id: 1, username: 'admin', role: 'admin' });
        expect((await request(app).get('/api/automation/smtp')).status).toBe(404);
        expect((await request(app).get('/api/bd/smtp')).status).toBe(404);
        expect((await request(app).get('/api/bd/rules')).status).toBe(404);
    });
});
