/**
 * Alert rules are evaluated: offline time in a sweep over the device list.
 * The checks on telemetry, inventory and activity reports run where those
 * routes are, in the Go console (console/alerts).
 */

const mockAdapter = {
    getAlertRules: jest.fn(),
    getRecentAlert: jest.fn(),
    createAlert: jest.fn(),
};
const mockDb = {
    getPeerById: jest.fn().mockResolvedValue(null),
};
const mockBackend = { getAllDevices: jest.fn() };

jest.mock('../services/database', () => mockDb);
jest.mock('../services/dbAdapter', () => ({ getAdapter: jest.fn(() => mockAdapter) }));
jest.mock('../services/betterdeskApi', () => ({ getAllPeers: jest.fn().mockResolvedValue([]) }));
jest.mock('../services/serverBackend', () => mockBackend);
jest.mock('../services/emailService', () => ({ sendAlertEmail: jest.fn().mockResolvedValue(undefined) }));

const engine = require('../services/alertRulesEngine');

function rule(over = {}) {
    return {
        id: 1,
        name: 'Rule',
        enabled: 1,
        condition_type: 'cpu_usage',
        condition_op: 'gt',
        condition_value: 90,
        severity: 'warning',
        scope_device_id: null,
        cooldown_secs: 300,
        notify_emails: '',
        ...over,
    };
}

/** Rules by condition type, as getAlertRules({ enabled: true, condition_type }) answers */
function useRules(list) {
    mockAdapter.getAlertRules.mockImplementation(async (f = {}) =>
        list.filter((r) => (!f.condition_type || r.condition_type === f.condition_type) && (f.enabled === undefined || !!r.enabled === f.enabled)));
}

beforeEach(() => {
    jest.clearAllMocks();
    mockAdapter.getRecentAlert.mockResolvedValue(null);
    mockAdapter.createAlert.mockImplementation(async (a) => ({ id: 1, ...a }));
    useRules([]);
});

describe('offline sweep', () => {
    const now = Date.parse('2026-11-09T12:00:00Z');
    const devices = [
        { id: 'off-2h', online: false, last_online: '2026-11-09T10:00:00Z' },
        { id: 'off-1m', online: false, last_online: '2026-11-09T11:59:00Z' },
        { id: 'on', online: true, last_online: '2026-11-09T09:00:00Z' },
        { id: 'banned', online: false, banned: true, last_online: '2026-11-01T00:00:00Z' },
        { id: 'gone', online: false, soft_deleted: true, last_online: '2026-11-01T00:00:00Z' },
        { id: 'never', online: false, last_online: '' },
    ];

    it('does not read the device list when no offline rule is on', async () => {
        useRules([rule({ condition_type: 'cpu_usage' })]);
        await engine.periodicCheck({ now });
        expect(mockBackend.getAllDevices).not.toHaveBeenCalled();
    });

    it('raises one alert per device that has been offline longer than the limit', async () => {
        useRules([rule({ id: 6, condition_type: 'offline_duration', condition_value: 3600 })]);
        mockBackend.getAllDevices.mockResolvedValue(devices);
        await engine.periodicCheck({ now });
        expect(mockAdapter.createAlert).toHaveBeenCalledTimes(1);
        expect(mockAdapter.createAlert).toHaveBeenCalledWith(expect.objectContaining({ rule_id: 6, device_id: 'off-2h' }));
    });

    it('does not raise it again while the device stays offline', async () => {
        useRules([rule({ id: 6, condition_type: 'offline_duration', condition_value: 3600, cooldown_secs: 300 })]);
        mockBackend.getAllDevices.mockResolvedValue(devices);
        await engine.periodicCheck({ now });
        // The cooldown covers the whole time the device has been offline (2 h), not just 5 minutes
        expect(mockAdapter.getRecentAlert).toHaveBeenCalledWith(6, 'off-2h', 7200);
    });

    it('keeps a rule for one device to that device', async () => {
        useRules([rule({ id: 7, condition_type: 'offline_duration', condition_value: 30, scope_device_id: 'off-1m' })]);
        mockBackend.getAllDevices.mockResolvedValue(devices);
        await engine.periodicCheck({ now });
        expect(mockAdapter.createAlert.mock.calls.map(([a]) => a.device_id)).toEqual(['off-1m']);
    });
});

describe('start', () => {
    it('runs the sweep on a timer and stops it again', async () => {
        jest.useFakeTimers();
        try {
            const spy = jest.spyOn(engine, 'periodicCheck').mockResolvedValue(undefined);
            const stop = engine.start({ every: 1000 });
            await jest.advanceTimersByTimeAsync(2500);
            stop();
            await jest.advanceTimersByTimeAsync(5000);
            expect(spy).toHaveBeenCalledTimes(2);
            spy.mockRestore();
        } finally {
            jest.useRealTimers();
        }
    });
});
