/**
 * Appearance: the readability check names what it found in words, not by
 * the server's ids (BUGS L374): "page-text", "console-background" and
 * "image" become the names of what they stand for.
 */
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { setStrings } from '../../../src/lib/i18n/i18n.svelte.ts';
import en from '../../../../neodesk-server/console/static/files/lang/en.json';

vi.mock('#lib/api/settings.ts', async (orig) => ({
	...(await orig<typeof import('../../../src/lib/api/settings.ts')>()),
	getBranding: vi.fn(async () => ({
		data: { appName: 'Desk' },
		readability: {
			ok: false,
			issues: [
				{
					id: 'page-text',
					severity: 'error',
					messageKey: 'branding.readability_contrast',
					messageParams: { item: 'page-text', ratio: '1.16', minimum: 4.5 },
					message: 'page-text contrast ratio 1.16 is below 4.5:1'
				},
				{
					id: 'console-background',
					severity: 'warning',
					messageKey: 'branding.readability_background',
					messageParams: { item: 'console-background', type: 'image' },
					message: 'console-background uses image with a low overlay; text may be hard to read.'
				}
			]
		}
	})),
	listBackgrounds: vi.fn(async () => []),
	listLocalFonts: vi.fn(async () => []),
	listProfiles: vi.fn(async () => []),
	listThemes: vi.fn(async () => [])
}));

import AppearanceTab from '../../../src/lib/settings/tabs/AppearanceTab.svelte';
import { session } from '../../../src/lib/state/session.svelte.ts';

beforeEach(() => {
	session.isSuperAdmin = true;
	setStrings('en', 'ltr', en as unknown as Parameters<typeof setStrings>[2]);
});

afterEach(() => {
	cleanup();
	session.isSuperAdmin = false;
	setStrings('en', 'ltr', {});
});

describe('readability check', () => {
	test('names the parts in words', async () => {
		render(AppearanceTab);
		const notice = await screen.findByText(/contrast/i);
		const text = notice.closest('#branding-readability-alert')?.textContent ?? notice.textContent ?? '';
		expect(text).toContain('text on the page background');
		expect(text).toContain('The console background is a picture');
		for (const id of ['page-text', 'console-background', ' image ']) expect(text).not.toContain(id);
	});
});
