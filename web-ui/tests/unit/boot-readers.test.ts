import { afterEach, describe, expect, test, vi } from 'vitest';
import { i18n } from '#lib/i18n/i18n.svelte.ts';
import { dropEarlySession, publicPage, startPublic } from '#lib/public/boot.svelte.ts';
import { startRemotePage } from '#lib/remote/boot.ts';

// /console-boot.js (console/routes/console/boot.go) leaves the requests it
// started in window.__ND_BOOT__; the pages outside the console take them
// from there instead of asking again.
type BootWindow = { __ND_BOOT__?: Record<string, unknown> };
const w = window as unknown as BootWindow;

const ok = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

afterEach(() => {
	delete w.__ND_BOOT__;
	vi.unstubAllGlobals();
});

describe('the pages outside the console use what the boot script started', () => {
	test('a public page takes the early strings request', async () => {
		const fetchMock = vi.fn(() => Promise.resolve(ok({ from: 'fetch' })));
		vi.stubGlobal('fetch', fetchMock);
		w.__ND_BOOT__ = {
			lang: 'de',
			dir: 'ltr',
			stringsUrl: '/api/console/strings/de?v=1',
			strings: Promise.resolve(ok({ from: 'boot' }))
		};

		await startPublic();
		expect(fetchMock).not.toHaveBeenCalled();
		expect(publicPage.ready).toBe(true);
		expect(i18n.lang).toBe('de');
		expect(i18n.strings).toEqual({ from: 'boot' });
	});

	test('a public page ends the early session request', async () => {
		const text = vi.fn(() => Promise.resolve(''));
		w.__ND_BOOT__ = { session: Promise.resolve({ text } as unknown as Response) };

		dropEarlySession();
		await vi.waitFor(() => expect(text).toHaveBeenCalled());
		expect(w.__ND_BOOT__.session).toBeUndefined();
	});

	test('a remote page takes the early strings request', async () => {
		const fetchMock = vi.fn(() => Promise.resolve(ok({ from: 'fetch' })));
		vi.stubGlobal('fetch', fetchMock);
		w.__ND_BOOT__ = {
			lang: 'fr',
			dir: 'ltr',
			stringsUrl: '/api/console/strings/fr?v=1',
			strings: Promise.resolve(ok({ from: 'boot' }))
		};

		await startRemotePage();
		expect(fetchMock).not.toHaveBeenCalled();
		expect(i18n.lang).toBe('fr');
		expect(i18n.strings).toEqual({ from: 'boot' });
	});
});
