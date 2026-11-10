'use strict';

/**
 * Profile pictures stored by the profile route and the avatar service,
 * against a real SQLite database (user_avatars table, services/dbAdapter.js).
 * A picture from single sign-on is written by the Go console
 * (betterdesk-server/console/avatars); here it is a row the test writes.
 * The routes that serve the pictures are answered by the Go console
 * (betterdesk-server/console/routes/avatars) and tested there.
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const request = require('supertest');

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

    it('fetches and stores an http(s) avatar URL saved in the profile instead of hotlinking it', async () => {
        const os2 = require('os');
        const lan = Object.values(os2.networkInterfaces()).flat().find((a) => a && a.family === 'IPv4' && !a.internal);
        if (!lan) return; // no LAN address to serve from (outside Docker)
        const http = require('http');
        const server = http.createServer((req, res) => {
            if (req.url === '/me.png') { res.writeHead(200, { 'Content-Type': 'image/png' }); return res.end(PNG); }
            res.writeHead(404); res.end();
        });
        await new Promise((r) => server.listen(0, '0.0.0.0', r));
        const url = `http://${lan.address}:${server.address().port}/me.png`;
        try {
            const profileRoutes = require('../routes/phase4_5.routes');
            const express = require('express');
            const papp = express();
            papp.use(express.json());
            papp.use((req, _res, next) => { req.session = { userId: alice.id, user: alice }; next(); });
            papp.use('/', profileRoutes);

            const saved = await request(papp).put('/api/users/me/profile').send({ first_name: 'Alice', avatar_url: url });
            expect(saved.status).toBe(200);
            expect(saved.body).toEqual(expect.objectContaining({ success: true, avatar: 'stored' }));
            const row = await db.getUserAvatar(alice.id);
            expect(row.source).toBe('profile');
            expect(row.source_url).toBe(url);
            expect(Buffer.compare(Buffer.from(row.data), PNG)).toBe(0);

            const bad = await request(papp).put('/api/users/me/profile').send({ avatar_url: 'http://127.0.0.1/x.png' });
            expect(bad.status).toBe(200);
            expect(bad.body).toEqual(expect.objectContaining({ success: true, avatar: 'failed', avatar_error: 'address_refused' }));

            const cleared = await request(papp).put('/api/users/me/profile').send({ avatar_url: '' });
            expect(cleared.body.avatar).toBe('cleared');
            expect(await db.getUserAvatar(alice.id)).toBeNull();
        } finally {
            await new Promise((r) => server.close(r));
        }
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
