'use strict';

/**
 * Profile pictures stored by the avatar service (the profile route is
 * answered by the Go console, betterdesk-server/console/routes/phase45),
 * against a real SQLite database (user_avatars table, services/dbAdapter.js).
 * A picture from single sign-on is written by the Go console
 * (betterdesk-server/console/avatars); here it is a row the test writes.
 * The routes that serve the pictures are answered by the Go console
 * (betterdesk-server/console/routes/avatars) and tested there.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');

// 1x1 PNG
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC', 'base64');

describe('profile pictures', () => {
    let tempDir;
    let db;
    let avatarService;
    let alice;

    beforeAll(async () => {
        tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'betterdesk-avatars-'));
        process.env.DATA_DIR = path.join(tempDir, 'panel-data');
        process.env.DB_PATH = path.join(tempDir, 'db_v2.sqlite3');
        process.env.DB_TYPE = 'sqlite';
        jest.resetModules();
        db = require('../services/database');
        await db.init();
        avatarService = require('../services/avatarService');

        alice = await db.createUser('alice', 'x', 'operator');
    });

    afterAll(async () => {
        await db.close();
        delete process.env.DATA_DIR;
        delete process.env.DB_PATH;
        delete process.env.DB_TYPE;
        fs.rmSync(tempDir, { recursive: true, force: true });
    });

    beforeEach(async () => {
        await db.deleteUserAvatar(alice.id);
        avatarService.resetCache();
    });

    it('keeps an OIDC picture when a profile picture is cleared, and removes it with the user', async () => {
        await db.setUserAvatar(alice.id, { mimeType: 'image/png', data: PNG, version: 'v1', source: 'oidc', sourceUrl: null });
        await avatarService.syncProfileAvatarUrl(alice.id, '');
        expect((await db.getUserAvatar(alice.id)).source).toBe('oidc');

        const carol = await db.createUser('carol', 'x', 'viewer');
        await db.setUserAvatar(carol.id, { mimeType: 'image/png', data: PNG, version: 'v1', source: 'oidc', sourceUrl: null });
        expect(await db.getUserAvatar(carol.id)).toBeTruthy();
        await db.deleteUser(carol.id);
        expect(await db.getUserAvatar(carol.id)).toBeNull();
    });
});
