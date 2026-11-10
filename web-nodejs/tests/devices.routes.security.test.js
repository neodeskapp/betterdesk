const request = require('supertest');
const { createTestApp } = require('./helpers');

jest.mock('../services/bdRelay', () => ({
    requestFromDevice: jest.fn()
}));

jest.mock('../services/database', () => ({}));

jest.mock('../services/serverBackend', () => ({
    getDeviceById: jest.fn()
}));

jest.mock('../services/betterdeskApi', () => ({
    getPeerTelemetry: jest.fn()
}));

jest.mock('../services/deviceGroupService', () => ({
    userCanAccessDevice: jest.fn()
}));

const bdRelay = require('../services/bdRelay');
const serverBackend = require('../services/serverBackend');
const deviceGroupService = require('../services/deviceGroupService');
const devicesRoutes = require('../routes/devices.routes');

function createAuthenticatedApp(user) {
    const app = createTestApp();
    app.use((req, _res, next) => {
        req.session.userId = user.id;
        req.session.user = user;
        next();
    });
    app.use('/', devicesRoutes);
    return app;
}

describe('device route authorization', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        serverBackend.getDeviceById.mockResolvedValue({ id: 'TARGET01' });
        deviceGroupService.userCanAccessDevice.mockResolvedValue(true);
    });

    test('blocks agent proxy requests for devices outside the user scope', async () => {
        deviceGroupService.userCanAccessDevice.mockResolvedValue(false);

        const app = createAuthenticatedApp({
            id: 7,
            username: 'viewer',
            role: 'viewer'
        });
        const response = await request(app).get('/api/devices/TARGET01/services');

        expect(response.status).toBe(403);
        expect(bdRelay.requestFromDevice).not.toHaveBeenCalled();
    });

    test('checks device scope before proxying an allowed agent request', async () => {
        bdRelay.requestFromDevice.mockResolvedValue({ services: [] });

        const app = createAuthenticatedApp({
            id: 8,
            username: 'operator',
            role: 'operator'
        });
        const response = await request(app).get('/api/devices/TARGET01/services');

        expect(response.status).toBe(200);
        expect(bdRelay.requestFromDevice).toHaveBeenCalledWith(
            'TARGET01',
            'services.list',
            null,
            15000
        );
    });
});
