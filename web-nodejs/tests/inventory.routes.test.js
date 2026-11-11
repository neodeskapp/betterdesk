/**
 * BetterDesk Console - Inventory Routes Tests
 *
 * The device routes. The routes people read inventory with are in the Go
 * console (betterdesk-server/console/routes/inventory).
 */

const request = require('supertest');
const { createTestApp } = require('./helpers');

const mockDb = {
    getAccessToken: jest.fn(),
    touchAccessToken: jest.fn().mockResolvedValue(undefined),
};

const mockAdapter = {
    getInventory: jest.fn(),
};

jest.mock('../services/database', () => mockDb);
jest.mock('../services/dbAdapter', () => ({
    getAdapter: jest.fn(() => mockAdapter),
}));

const inventoryRoutes = require('../routes/inventory.routes');

describe('Inventory Routes', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    it('rejects an unauthenticated device inventory read', async () => {
        const app = createTestApp();
        app.use('/api/bd', inventoryRoutes);

        const res = await request(app).get('/api/bd/inventory/device-a');

        expect(res.status).toBe(401);
        expect(mockDb.getAccessToken).not.toHaveBeenCalled();
        expect(mockAdapter.getInventory).not.toHaveBeenCalled();
    });

    it('rejects an X-Device-Id-only inventory read', async () => {
        const app = createTestApp();
        app.use('/api/bd', inventoryRoutes);

        const res = await request(app)
            .get('/api/bd/inventory/device-a')
            .set('X-Device-Id', 'device-a');

        expect(res.status).toBe(401);
        expect(mockDb.getAccessToken).not.toHaveBeenCalled();
        expect(mockAdapter.getInventory).not.toHaveBeenCalled();
    });

    it('rejects a valid device token reading another device inventory', async () => {
        mockDb.getAccessToken.mockResolvedValue({ client_id: 'device-b' });

        const app = createTestApp();
        app.use('/api/bd', inventoryRoutes);

        const res = await request(app)
            .get('/api/bd/inventory/device-a')
            .set('Authorization', 'Bearer valid-device-b-token');

        expect(res.status).toBe(403);
        expect(mockDb.touchAccessToken).toHaveBeenCalledWith('valid-device-b-token');
        expect(mockAdapter.getInventory).not.toHaveBeenCalled();
    });
});
