'use strict';

/**
 * getDeviceGroupsForPeers loads the groups of many devices in one query
 * and answers what one query per device would.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

jest.setTimeout(20000);

describe('getDeviceGroupsForPeers (SQLite)', () => {
    let tmpDir;
    let adapter;

    beforeEach(async () => {
        tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-groups-batch-'));
        const dataDir = path.join(tmpDir, 'data');
        fs.mkdirSync(dataDir);
        process.env.DATA_DIR = dataDir;
        process.env.DB_TYPE = 'sqlite';
        process.env.DB_PATH = path.join(dataDir, 'db_v2.sqlite3');
        jest.resetModules();
        adapter = require('../services/dbAdapter').getAdapter();
        await adapter.init();
    });

    afterEach(async () => {
        if (adapter && typeof adapter.close === 'function') await adapter.close();
        fs.rmSync(tmpDir, { recursive: true, force: true });
    });

    test('matches the per-device lookup for every device', async () => {
        const office = await adapter.createDeviceGroup({ name: 'Office' });
        const lab = await adapter.createDeviceGroup({ name: 'Lab' });
        await adapter.addDeviceToGroup(office.guid, '100');
        await adapter.addDeviceToGroup(lab.guid, '100');
        await adapter.addDeviceToGroup(lab.guid, '200');

        const ids = ['100', '200', '300'];
        const batch = await adapter.getDeviceGroupsForPeers(ids);
        for (const id of ids) {
            const single = await adapter.getDeviceGroupsForPeer(id);
            expect(batch.get(id) || []).toEqual(single);
        }
        expect(batch.get('100').map((g) => g.name)).toEqual(['Lab', 'Office']);
        expect(batch.has('300')).toBe(false);
        expect((await adapter.getDeviceGroupsForPeers([])).size).toBe(0);
        // Only the devices asked for
        expect([...(await adapter.getDeviceGroupsForPeers(['200'])).keys()]).toEqual(['200']);
    });
});
