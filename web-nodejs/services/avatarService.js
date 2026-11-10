'use strict';

/**
 * Profile pictures for console users.
 *
 * The picture is stored as checked image bytes in auth.db (user_avatars) and
 * served by GET /api/users/:id/avatar. Where it comes from:
 *   - OIDC sign-in: the identity provider's "picture" claim (source 'oidc'),
 *     stored by the Go console (console/avatars)
 *   - the profile's avatar URL field for local users (source 'profile')
 *
 * Fetching or decoding a picture can fail (IdP down, too large, wrong type);
 * that is logged and never stops a sign-in or a profile save.
 */

const db = require('./database');
const { loadAvatarPicture } = require('../lib/avatarImage');

// Who has a picture, read from the database at most every 30 s and dropped
// on every write here. The TTL covers writes from another process (the dev
// seed script, a second console instance).
const DIRECTORY_TTL_MS = 30 * 1000;
let directoryCache = null;
let directoryLoadedAt = 0;

function forget() {
    directoryCache = null;
}

async function storePicture(userId, picture, source, options) {
    const img = await loadAvatarPicture(picture, options);
    await db.setUserAvatar(userId, {
        mimeType: img.mimeType,
        data: img.data,
        version: img.version,
        source,
        sourceUrl: img.sourceUrl,
    });
    forget(userId);
    return img.version;
}

/**
 * Apply the avatar URL a user saved in their profile. An http(s) URL is
 * fetched and stored; an empty value removes a picture that came from the
 * profile (a picture from OIDC stays). Resolves to { action, code? }.
 */
async function syncProfileAvatarUrl(userId, avatarUrl, options = {}) {
    const value = String(avatarUrl == null ? '' : avatarUrl).trim();
    try {
        if (!value) {
            const removed = await db.deleteUserAvatar(userId, 'profile');
            forget(userId);
            return { action: removed ? 'cleared' : 'skipped' };
        }
        if (!/^https?:\/\//i.test(value)) return { action: 'failed', code: 'invalid_url' };
        await storePicture(userId, value, 'profile', options);
        return { action: 'stored' };
    } catch (err) {
        console.warn(`[Avatar] Profile picture for user ${userId} not stored (${err.code || 'error'}): ${err.message}`);
        return { action: 'failed', code: err.code || 'error' };
    }
}

/** The stored picture row or null */
async function getAvatar(userId) {
    return db.getUserAvatar(userId);
}

/** Version string of a user's picture, or null when there is none */
async function getAvatarVersion(userId) {
    const id = Number(userId);
    if (!Number.isInteger(id) || id <= 0) return null;
    try {
        const map = await getVersionMap();
        return map.get(id) || null;
    } catch (_) {
        return null;
    }
}

/** Map userId → version for every user with a picture */
async function getVersionMap() {
    if (!directoryCache || Date.now() - directoryLoadedAt > DIRECTORY_TTL_MS) {
        const rows = await db.listUserAvatarVersions();
        directoryCache = rows || [];
        directoryLoadedAt = Date.now();
    }
    return new Map(directoryCache.map((r) => [Number(r.user_id), r.version]));
}

/** [{ id, username, avatar_version }] for every user with a picture */
async function listDirectory() {
    await getVersionMap();
    return directoryCache.map((r) => ({ id: Number(r.user_id), username: r.username, avatar_version: r.version }));
}

/** Add has_avatar and avatar_version to a user payload */
function withAvatarFields(user, versionMap) {
    const version = versionMap.get(Number(user.id)) || null;
    return { ...user, has_avatar: !!version, avatar_version: version };
}

function resetCache() {
    directoryCache = null;
}

module.exports = {
    syncProfileAvatarUrl,
    getAvatar,
    getAvatarVersion,
    getVersionMap,
    listDirectory,
    withAvatarFields,
    resetCache,
};
