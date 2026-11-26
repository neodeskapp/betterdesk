/**
 * Who has a profile picture, for avatars whose data carries none (chat
 * people, assignees, message senders): account id → picture version from
 * GET /api/users/avatars. Read when an avatar first asks and again when the
 * list is older than a minute, so a colleague's new picture shows up
 * without a reload.
 */

import { SvelteMap } from 'svelte/reactivity';
import { listAvatarVersions } from '#lib/api/audit.ts';

const MAX_AGE_MS = 60_000;

export const avatarDirectory = { versions: new SvelteMap<string, string>() };

let loadedAt = 0;
let loading = false;

/** Read the list when it has not been read for a minute; errors keep the old list */
export function refreshAvatarDirectory(now = Date.now()): void {
	if (loading || (loadedAt && now - loadedAt < MAX_AGE_MS)) return;
	loading = true;
	listAvatarVersions()
		.then((versions) => {
			avatarDirectory.versions.clear();
			for (const [id, version] of versions) avatarDirectory.versions.set(id, version);
		})
		.catch(() => {})
		.finally(() => {
			loadedAt = Date.now();
			loading = false;
		});
}

/** Put one account's picture into the list at once (after your own upload or removal) */
export function setDirectoryVersion(id: string | number, version: string | null): void {
	if (version) avatarDirectory.versions.set(String(id), version);
	else avatarDirectory.versions.delete(String(id));
}

/** Forget the list (tests, sign-out) */
export function resetAvatarDirectory(): void {
	avatarDirectory.versions.clear();
	loadedAt = 0;
	loading = false;
}
